// Package github provides an authenticated GitHub API client with rate limit handling.
package github

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// defaultGitHubAPIURL is the base URL for the public GitHub API.
// Used as the default in both authenticators and the client to avoid
// repeated string literals that could silently diverge on a typo.
const defaultGitHubAPIURL = "https://api.github.com"

// ErrUnauthorized is wrapped by Authenticator.Token errors when GitHub
// rejects the credential with HTTP 401.
var ErrUnauthorized = errors.New("github: credential rejected with 401 Unauthorized")

// Authenticator is implemented by any type that can produce a GitHub API token.
type Authenticator interface {
	// Token returns a valid GitHub API bearer token.
	// Implementations must refresh automatically as needed.
	Token(ctx context.Context) (string, error)

	// Ping performs a lightweight API call to verify the credentials are valid.
	// Returns nil if credentials are valid, or a descriptive error otherwise.
	Ping(ctx context.Context) error
}

// ---------------------------------------------------------------------------
// PATAuthenticator
// ---------------------------------------------------------------------------

// PATAuthenticator provides authentication via a GitHub Personal Access Token.
type PATAuthenticator struct {
	token   string
	baseURL string // REST API root, e.g. https://ghes.example.com/api/v3
	client  *http.Client
}

// NewPATAuthenticator creates a PAT authenticator with the given token.
func NewPATAuthenticator(token string) *PATAuthenticator {
	return &PATAuthenticator{token: token, baseURL: defaultGitHubAPIURL, client: http.DefaultClient}
}

// NewPATAuthenticatorWithBase creates a PAT authenticator against the given
// REST API root. client sends the credential check; nil means http.DefaultClient.
func NewPATAuthenticatorWithBase(token, baseURL string, client *http.Client) *PATAuthenticator {
	if client == nil {
		client = http.DefaultClient
	}
	return &PATAuthenticator{token: token, baseURL: baseURL, client: client}
}

// Token returns the PAT directly (no refresh required).
func (p *PATAuthenticator) Token(_ context.Context) (string, error) {
	return p.token, nil
}

// Ping calls GET /user to validate the PAT.
func (p *PATAuthenticator) Ping(ctx context.Context) error {
	return pingURL(ctx, p.client, p.baseURL+"/user", p.token)
}

// ---------------------------------------------------------------------------
// AppAuthenticator
// ---------------------------------------------------------------------------

// AppAuthOptions configures a GitHub App authenticator.
type AppAuthOptions struct {
	AppID          int64
	InstallationID int64
	// PrivateKeyPEM holds the RSA private key in PEM format.
	// Exactly one of PrivateKeyPEM, PrivateKeyPath, or PrivateKeyBase64 must be set.
	PrivateKeyPEM    []byte
	PrivateKeyPath   string
	PrivateKeyBase64 string // base64-encoded PEM (from env var)
	BaseURL          string // REST API root; defaults to https://api.github.com
	// HTTPClient sends the credential check and the installation token
	// exchange; defaults to http.DefaultClient.
	HTTPClient *http.Client
}

// AppAuthenticator authenticates as a GitHub App using JWT + installation token.
type AppAuthenticator struct {
	opts       AppAuthOptions
	privateKey *rsa.PrivateKey

	mu          sync.Mutex
	cachedToken string
	expiresAt   time.Time
	grants      InstallationGrants
}

// InstallationGrants is what a GitHub App installation grants, as returned
// with each installation token.
type InstallationGrants struct {
	// Permissions maps a permission (e.g. "pull_requests") to its access
	// level ("read", "write" or "admin").
	Permissions map[string]string
	// RepositorySelection is "all" or "selected".
	RepositorySelection string
}

// NewAppAuthenticator creates an AppAuthenticator, loading the private key eagerly.
func NewAppAuthenticator(opts AppAuthOptions) (*AppAuthenticator, error) {
	if opts.BaseURL == "" {
		opts.BaseURL = defaultGitHubAPIURL
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}

	pem, err := loadPrivateKeyPEM(opts)
	if err != nil {
		return nil, fmt.Errorf("loading GitHub App private key: %w", err)
	}

	key, err := parseRSAPrivateKey(pem)
	if err != nil {
		return nil, fmt.Errorf("parsing GitHub App private key: %w", err)
	}

	return &AppAuthenticator{opts: opts, privateKey: key}, nil
}

// Token returns a valid installation token, refreshing if needed.
func (a *AppAuthenticator) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Refresh if the token is missing or expires within 5 minutes.
	if a.cachedToken == "" || time.Until(a.expiresAt) < 5*time.Minute {
		token, exp, grants, err := a.fetchInstallationToken(ctx)
		if err != nil {
			return "", err
		}
		a.cachedToken = token
		a.expiresAt = exp
		a.grants = grants
	}

	return a.cachedToken, nil
}

// Grants returns what the installation grants, from the cached installation
// token, exchanging one first if needed. It sends no other request.
func (a *AppAuthenticator) Grants(ctx context.Context) (InstallationGrants, error) {
	if _, err := a.Token(ctx); err != nil {
		return InstallationGrants{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.grants, nil
}

// Ping calls GET /app to verify the GitHub App credentials.
func (a *AppAuthenticator) Ping(ctx context.Context) error {
	jwtToken, err := a.generateJWT()
	if err != nil {
		return fmt.Errorf("generating JWT for Ping: %w", err)
	}
	return pingURL(ctx, a.opts.HTTPClient, a.opts.BaseURL+"/app", jwtToken)
}

// generateJWT creates a signed JWT for GitHub App authentication.
func (a *AppAuthenticator) generateJWT() (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Add(-60 * time.Second).Unix(), // issued slightly in the past for clock skew
		"exp": now.Add(10 * time.Minute).Unix(),
		"iss": fmt.Sprintf("%d", a.opts.AppID),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(a.privateKey)
}

// fetchInstallationToken exchanges a JWT for a GitHub App installation token.
func (a *AppAuthenticator) fetchInstallationToken(ctx context.Context) (string, time.Time, InstallationGrants, error) {
	var none InstallationGrants
	jwtToken, err := a.generateJWT()
	if err != nil {
		return "", time.Time{}, none, fmt.Errorf("generating JWT: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", a.opts.BaseURL, a.opts.InstallationID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", time.Time{}, none, fmt.Errorf("creating installation token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := a.opts.HTTPClient.Do(req)
	if err != nil {
		return "", time.Time{}, none, fmt.Errorf("requesting installation token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("installation token request failed (%d): %s", resp.StatusCode, string(body))
		if resp.StatusCode == http.StatusUnauthorized {
			err = fmt.Errorf("%w: %w", ErrUnauthorized, err)
		}
		return "", time.Time{}, none, err
	}

	var result struct {
		Token               string            `json:"token"`
		ExpiresAt           string            `json:"expires_at"`
		Permissions         map[string]string `json:"permissions"`
		RepositorySelection string            `json:"repository_selection"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", time.Time{}, none, fmt.Errorf("reading installation token response: %w", err)
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", time.Time{}, none, fmt.Errorf("parsing installation token response: %w", err)
	}

	exp, err := time.Parse(time.RFC3339, result.ExpiresAt)
	if err != nil {
		// Default to 55 minutes (installation tokens last 1h).
		exp = time.Now().Add(55 * time.Minute)
	}

	grants := InstallationGrants{Permissions: result.Permissions, RepositorySelection: result.RepositorySelection}
	return result.Token, exp, grants, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// pingURL makes an authenticated GET request and returns an error if non-2xx.
func pingURL(ctx context.Context, client *http.Client, url, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating ping request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ping request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("authentication failed (%d): %s", resp.StatusCode, string(body))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ping returned unexpected status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// loadPrivateKeyPEM returns the PEM bytes from whichever source is configured.
func loadPrivateKeyPEM(opts AppAuthOptions) ([]byte, error) {
	if len(opts.PrivateKeyPEM) > 0 {
		return opts.PrivateKeyPEM, nil
	}
	if opts.PrivateKeyBase64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(opts.PrivateKeyBase64)
		if err != nil {
			return nil, fmt.Errorf("base64 decoding private key: %w", err)
		}
		return decoded, nil
	}
	if opts.PrivateKeyPath != "" {
		data, err := os.ReadFile(opts.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("reading private key file %q: %w", opts.PrivateKeyPath, err)
		}
		return data, nil
	}
	return nil, fmt.Errorf("no private key source configured (set PrivateKeyPEM, PrivateKeyBase64, or PrivateKeyPath)")
}

// parseRSAPrivateKey decodes a PEM-encoded RSA private key.
// It delegates directly to jwt.ParseRSAPrivateKeyFromPEM which returns
// a descriptive error for missing or malformed PEM blocks.
func parseRSAPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("parsing RSA private key: %w", err)
	}
	return key, nil
}
