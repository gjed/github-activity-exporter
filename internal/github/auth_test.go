package github_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	internalgithub "github.com/gjed/renovate-exporter/internal/github"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// generateTestRSAKey creates a throw-away RSA key for tests.
func generateTestRSAKey(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return key, pemBytes
}

// ---------------------------------------------------------------------------
// PATAuthenticator tests
// ---------------------------------------------------------------------------

func TestPATAuthenticator_Token(t *testing.T) {
	auth := internalgithub.NewPATAuthenticator("ghp_testtoken")
	tok, err := auth.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error: %v", err)
	}
	if tok != "ghp_testtoken" {
		t.Errorf("expected ghp_testtoken, got %q", tok)
	}
}

func TestPATAuthenticator_Ping_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Errorf("unexpected path: %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer ghp_ok" {
			t.Errorf("unexpected auth header: %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	auth := internalgithub.NewPATAuthenticatorWithBase("ghp_ok", srv.URL)
	if err := auth.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() unexpected error: %v", err)
	}
}

func TestPATAuthenticator_Ping_unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer srv.Close()

	auth := internalgithub.NewPATAuthenticatorWithBase("ghp_bad", srv.URL)
	err := auth.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
}

// ---------------------------------------------------------------------------
// AppAuthenticator tests
// ---------------------------------------------------------------------------

func TestAppAuthenticator_Token_success(t *testing.T) {
	_, keyPEM := generateTestRSAKey(t)

	expiresAt := time.Now().Add(55 * time.Minute).UTC().Format(time.RFC3339)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/installations/67890/access_tokens":
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			w.WriteHeader(http.StatusCreated)
			resp := map[string]string{
				"token":      "ghs_installationtoken",
				"expires_at": expiresAt,
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("unexpected path: %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	auth, err := internalgithub.NewAppAuthenticator(internalgithub.AppAuthOptions{
		AppID:          12345,
		InstallationID: 67890,
		PrivateKeyPEM:  keyPEM,
		BaseURL:        srv.URL,
	})
	if err != nil {
		t.Fatalf("NewAppAuthenticator error: %v", err)
	}

	tok, err := auth.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error: %v", err)
	}
	if tok != "ghs_installationtoken" {
		t.Errorf("expected ghs_installationtoken, got %q", tok)
	}
}

func TestAppAuthenticator_Token_cached(t *testing.T) {
	_, keyPEM := generateTestRSAKey(t)

	callCount := 0
	expiresAt := time.Now().Add(55 * time.Minute).UTC().Format(time.RFC3339)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"token":      "ghs_cached",
			"expires_at": expiresAt,
		})
	}))
	defer srv.Close()

	auth, err := internalgithub.NewAppAuthenticator(internalgithub.AppAuthOptions{
		AppID:          12345,
		InstallationID: 67890,
		PrivateKeyPEM:  keyPEM,
		BaseURL:        srv.URL,
	})
	if err != nil {
		t.Fatalf("NewAppAuthenticator error: %v", err)
	}

	ctx := context.Background()
	// Call Token twice; should only hit the server once.
	if _, err := auth.Token(ctx); err != nil {
		t.Fatalf("first Token() error: %v", err)
	}
	if _, err := auth.Token(ctx); err != nil {
		t.Fatalf("second Token() error: %v", err)
	}

	if callCount != 1 {
		t.Errorf("expected 1 server call for caching, got %d", callCount)
	}
}

func TestAppAuthenticator_Token_refresh_near_expiry(t *testing.T) {
	_, keyPEM := generateTestRSAKey(t)

	callCount := 0
	// First token expires in 3 minutes (within the 5-minute refresh threshold).
	expiresAt1 := time.Now().Add(3 * time.Minute).UTC().Format(time.RFC3339)
	expiresAt2 := time.Now().Add(55 * time.Minute).UTC().Format(time.RFC3339)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		expiresAt := expiresAt2
		if callCount == 1 {
			expiresAt = expiresAt1
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"token":      "ghs_token",
			"expires_at": expiresAt,
		})
	}))
	defer srv.Close()

	auth, err := internalgithub.NewAppAuthenticator(internalgithub.AppAuthOptions{
		AppID:          12345,
		InstallationID: 67890,
		PrivateKeyPEM:  keyPEM,
		BaseURL:        srv.URL,
	})
	if err != nil {
		t.Fatalf("NewAppAuthenticator error: %v", err)
	}

	ctx := context.Background()
	// First call fetches; second call should refresh because token expires in 3 min.
	if _, err := auth.Token(ctx); err != nil {
		t.Fatalf("first Token() error: %v", err)
	}
	if _, err := auth.Token(ctx); err != nil {
		t.Fatalf("second Token() error: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 server calls (auto-refresh), got %d", callCount)
	}
}

func TestAppAuthenticator_Ping_success(t *testing.T) {
	_, keyPEM := generateTestRSAKey(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app" {
			t.Errorf("unexpected path: %q", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	auth, err := internalgithub.NewAppAuthenticator(internalgithub.AppAuthOptions{
		AppID:          12345,
		InstallationID: 67890,
		PrivateKeyPEM:  keyPEM,
		BaseURL:        srv.URL,
	})
	if err != nil {
		t.Fatalf("NewAppAuthenticator error: %v", err)
	}

	if err := auth.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() unexpected error: %v", err)
	}
}

func TestAppAuthenticator_Ping_failure(t *testing.T) {
	_, keyPEM := generateTestRSAKey(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	auth, err := internalgithub.NewAppAuthenticator(internalgithub.AppAuthOptions{
		AppID:          12345,
		InstallationID: 67890,
		PrivateKeyPEM:  keyPEM,
		BaseURL:        srv.URL,
	})
	if err != nil {
		t.Fatalf("NewAppAuthenticator error: %v", err)
	}

	if err := auth.Ping(context.Background()); err == nil {
		t.Fatal("expected error for 401, got nil")
	}
}
