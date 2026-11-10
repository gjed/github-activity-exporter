package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v93/github"
	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"

	sc "github.com/gjed/github-activity-exporter/internal/semconv"
)

const (
	// rateLimitPauseThreshold is the number of remaining requests in a pool
	// below which new requests to that pool are paused until its window resets.
	//
	// SIMPLIFIED: one fixed threshold for every pool. It fits core and graphql
	// (5,000+ per hour) but search (30/min) and code_search (10/min) are always
	// below it, so every search request after the first would wait for the
	// window reset. The exporter makes no search calls. Upgrade path: derive
	// the threshold per pool from X-RateLimit-Limit.
	rateLimitPauseThreshold = 200
)

// documentedPools are the X-RateLimit-Resource values GitHub documents
// (GET /rate_limit). Any other value is recorded as "other" so the pool
// attribute on the rate limit gauges stays bounded.
var documentedPools = map[string]bool{
	sc.AttrGitHubApiRateLimitResourceCore:                      true,
	sc.AttrGitHubApiRateLimitResourceSearch:                    true,
	sc.AttrGitHubApiRateLimitResourceCodeSearch:                true,
	sc.AttrGitHubApiRateLimitResourceGraphQL:                   true,
	sc.AttrGitHubApiRateLimitResourceIntegrationManifest:       true,
	sc.AttrGitHubApiRateLimitResourceSourceImport:              true,
	sc.AttrGitHubApiRateLimitResourceActionsRunnerRegistration: true,
	sc.AttrGitHubApiRateLimitResourceSCIM:                      true,
	sc.AttrGitHubApiRateLimitResourceDependencySnapshots:       true,
	sc.AttrGitHubApiRateLimitResourceDependencySBOM:            true,
	sc.AttrGitHubApiRateLimitResourceCodeScanningUpload:        true,
	sc.AttrGitHubApiRateLimitResourceCodeScanningAutofix:       true,
	sc.AttrGitHubApiRateLimitResourceAuditLog:                  true,
}

// RateLimitState holds the current rate limit snapshot of one pool for a target.
type RateLimitState struct {
	// Resource is the rate limit pool (X-RateLimit-Resource), e.g. "core" or "graphql".
	Resource  string
	Remaining int
	ResetAt   time.Time
}

// RateLimitHook is called whenever the rate limit state of a pool changes.
// It is invoked from the response transport, so implementations must not block.
type RateLimitHook func(state RateLimitState)

// AuthResultHook is called once per GitHub HTTP response with whether GitHub
// answered 401 Unauthorized, and with true when a GitHub App installation
// token exchange is rejected with 401. It runs in the client transport, so
// implementations must not block.
type AuthResultHook func(unauthorized bool)

// Client wraps the GitHub REST v3 and GraphQL v4 clients with authentication
// and per-target rate limit tracking.
type Client struct {
	auth   Authenticator
	logger *slog.Logger

	// REST client
	rest *github.Client

	// GraphQL client
	graphql *githubv4.Client

	// httpClient sends every request, REST and GraphQL, through the auth and
	// rate limit transports; gqlURL is the GraphQL endpoint QueryRaw posts to.
	httpClient *http.Client
	gqlURL     string

	// Rate limit state, keyed by pool.
	mu      sync.Mutex
	rlPools map[string]*poolState
	rlHooks []RateLimitHook

	authHooks []AuthResultHook

	// base is the transport under the rate limit transport.
	base http.RoundTripper

	// baseURL is the REST API root without a trailing slash.
	baseURL string

	// restPath and graphqlPath are the URL paths of the REST API root and the
	// GraphQL endpoint, used to classify a request's rate limit pool.
	restPath    string
	graphqlPath string
}

// poolState is the rate limit state of one pool.
type poolState struct {
	state  RateLimitState
	paused bool
}

// ClientOption is a functional option for Client.
type ClientOption func(*Client)

// WithRateLimitHook registers a callback invoked on rate limit state changes.
func WithRateLimitHook(hook RateLimitHook) ClientOption {
	return func(c *Client) {
		c.rlHooks = append(c.rlHooks, hook)
	}
}

// WithAuthResultHook registers a callback invoked on every GitHub response.
func WithAuthResultHook(hook AuthResultHook) ClientOption {
	return func(c *Client) {
		c.authHooks = append(c.authHooks, hook)
	}
}

// WithBaseTransport sets the transport the client sends requests through,
// under its rate limit and auth handling; see NewTransport. Default
// http.DefaultTransport.
func WithBaseTransport(rt http.RoundTripper) ClientOption {
	return func(c *Client) {
		c.base = rt
	}
}

// WithLogger sets the logger for the client.
func WithLogger(logger *slog.Logger) ClientOption {
	return func(c *Client) {
		c.logger = logger
	}
}

// WithBaseURL sets the REST API root, e.g. https://ghes.example.com/api/v3.
// It is used verbatim for REST; the GraphQL URL is derived from it.
func WithBaseURL(url string) ClientOption {
	return func(c *Client) {
		c.baseURL = url
	}
}

// NewClient creates a GitHub client backed by the given Authenticator.
func NewClient(auth Authenticator, opts ...ClientOption) (*Client, error) {
	c := &Client{
		auth:    auth,
		logger:  slog.Default(),
		baseURL: defaultGitHubAPIURL,
		rlPools: make(map[string]*poolState),
		base:    http.DefaultTransport,
	}
	for _, o := range opts {
		o(c)
	}

	transport := &rateLimitTransport{client: c, inner: c.base}
	tokenSource := &authenticatorTokenSource{auth: auth, client: c}
	oauthTransport := &oauth2.Transport{Source: tokenSource, Base: transport}
	httpClient := &http.Client{Transport: oauthTransport}

	// Use WithURLs, not WithEnterpriseURLs: the latter would append api/v3/
	// to any URL not already ending in it.
	rest, err := github.NewClient(github.WithHTTPClient(httpClient), github.WithURLs(&c.baseURL, nil))
	if err != nil {
		return nil, fmt.Errorf("creating REST client for %q: %w", c.baseURL, err)
	}
	c.rest = rest
	gqlURL := graphqlURL(c.baseURL)
	c.graphql = githubv4.NewEnterpriseClient(gqlURL, httpClient)
	c.httpClient = httpClient
	c.gqlURL = gqlURL

	restU, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing base URL %q: %w", c.baseURL, err)
	}
	gqlU, err := url.Parse(gqlURL)
	if err != nil {
		return nil, fmt.Errorf("parsing GraphQL URL %q: %w", gqlURL, err)
	}
	c.restPath = strings.TrimSuffix(restU.Path, "/")
	c.graphqlPath = gqlU.Path

	return c, nil
}

// graphqlURL derives the GraphQL endpoint from the REST API root:
// https://api.github.com -> https://api.github.com/graphql,
// https://<host>/api/v3 -> https://<host>/api/graphql (GHES).
func graphqlURL(base string) string {
	if strings.HasSuffix(base, "/api/v3") {
		return strings.TrimSuffix(base, "/api/v3") + "/api/graphql"
	}
	return base + "/graphql"
}

// REST returns the underlying go-github REST client.
func (c *Client) REST() *github.Client { return c.rest }

// GraphQL returns the underlying githubv4 GraphQL client.
func (c *Client) GraphQL() *githubv4.Client { return c.graphql }

// Query runs a typed GraphQL query; see githubv4.Client.Query.
func (c *Client) Query(ctx context.Context, q interface{}, variables map[string]interface{}) error {
	return c.graphql.Query(ctx, q, variables)
}

// GraphQLError is one entry of a GraphQL response's errors. Path starts with
// the top-level field or alias the error belongs to; it is empty for an error
// about the whole request.
type GraphQLError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Path    []any  `json:"path"`
}

// RawResponse is an undecoded GraphQL response: data per top-level field or
// alias (JSON null for a field that failed) and the errors, which may come
// with partial data.
type RawResponse struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []GraphQLError             `json:"errors"`
}

// graphqlRateLimit is the rateLimit field of a GraphQL response.
type graphqlRateLimit struct {
	Cost      int       `json:"cost"`
	Remaining int       `json:"remaining"`
	ResetAt   time.Time `json:"resetAt"`
}

// QueryRaw posts query with variables to the GraphQL endpoint through the
// same transports as Query, and returns the response without decoding it into
// a struct, so the caller sees which alias each error belongs to. A non-200
// status or an undecodable body is an error; GraphQL errors are not. When the
// data holds a rateLimit field, its cost is logged at debug level, and its
// remaining and resetAt are recorded as the graphql pool state when the
// response carries no X-RateLimit-Remaining header, which rateLimitTransport
// has already recorded.
func (c *Client) QueryRaw(ctx context.Context, query string, variables map[string]any) (*RawResponse, error) {
	body, err := json.Marshal(struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables,omitempty"`
	}{query, variables})
	if err != nil {
		return nil, fmt.Errorf("encoding graphql request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.gqlURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("graphql: non-200 OK status code: %v body: %q", resp.Status, b)
	}
	var out RawResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding graphql response: %w", err)
	}
	if raw, ok := out.Data["rateLimit"]; ok {
		var rl *graphqlRateLimit
		if err := json.Unmarshal(raw, &rl); err == nil && rl != nil {
			if resp.Header.Get("X-RateLimit-Remaining") == "" {
				c.updateRateLimit(sc.AttrGitHubApiRateLimitResourceGraphQL, rl.Remaining, rl.ResetAt)
			}
			c.logger.Debug("graphql query cost", "cost", rl.Cost, "remaining", rl.Remaining)
		}
	}
	return &out, nil
}

// RateLimitState returns a snapshot of the rate limit state of a pool
// (e.g. "core", "graphql"). It is the zero value, with Resource set, until a
// response for that pool has been seen.
func (c *Client) RateLimitState(resource string) RateLimitState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.rlPools[resource]; ok {
		return p.state
	}
	return RateLimitState{Resource: resource}
}

// classifyPool returns the rate limit pool a request will count against,
// derived from its URL before it is sent: the GraphQL endpoint is graphql,
// REST code search is code_search, other REST search is search, and every
// other REST endpoint is core. The remaining documented pools are single
// endpoints the exporter does not call; they classify as core.
func (c *Client) classifyPool(u *url.URL) string {
	if u.Path == c.graphqlPath {
		return sc.AttrGitHubApiRateLimitResourceGraphQL
	}
	rel := strings.TrimPrefix(u.Path, c.restPath)
	switch {
	case rel == "/search/code":
		return sc.AttrGitHubApiRateLimitResourceCodeSearch
	case strings.HasPrefix(rel, "/search/"):
		return sc.AttrGitHubApiRateLimitResourceSearch
	default:
		return sc.AttrGitHubApiRateLimitResourceCore
	}
}

// responsePool returns the pool a response's rate limit headers belong to.
// X-RateLimit-Resource is authoritative. A value GitHub does not document is
// recorded as "other": keeping it raw would make the gauge attribute
// unbounded, and folding it into a known pool would pause that pool for
// another pool's usage. A missing header (older GHES releases) falls back to
// the pool classified from the request URL, so the pause keeps working there.
func responsePool(header, classified string) string {
	switch {
	case header == "":
		return classified
	case documentedPools[header]:
		return header
	default:
		return sc.AttrGitHubApiRateLimitResourceOther
	}
}

// updateRateLimit records a new rate limit snapshot for a pool and invokes hooks.
func (c *Client) updateRateLimit(resource string, remaining int, resetAt time.Time) {
	c.mu.Lock()
	p, ok := c.rlPools[resource]
	if !ok {
		p = &poolState{}
		c.rlPools[resource] = p
	}
	p.state = RateLimitState{Resource: resource, Remaining: remaining, ResetAt: resetAt}
	pausing := remaining < rateLimitPauseThreshold
	if pausing && !p.paused {
		p.paused = true
		c.logger.Warn("rate limit threshold reached, pausing requests",
			"resource", resource,
			"remaining", remaining,
			"reset_at", resetAt,
		)
	} else if !pausing && p.paused {
		p.paused = false
		c.logger.Info("rate limit recovered, resuming requests",
			"resource", resource,
			"remaining", remaining,
		)
	}
	hooks := make([]RateLimitHook, len(c.rlHooks))
	copy(hooks, c.rlHooks)
	state := p.state
	c.mu.Unlock()

	for _, h := range hooks {
		h(state)
	}
}

// waitIfPaused blocks until the window of a pool resets, if that pool is paused.
func (c *Client) waitIfPaused(ctx context.Context, resource string) error {
	c.mu.Lock()
	p, ok := c.rlPools[resource]
	if !ok || !p.paused {
		c.mu.Unlock()
		return nil
	}
	resetAt := p.state.ResetAt
	c.mu.Unlock()

	waitDur := time.Until(resetAt)
	if waitDur <= 0 {
		return nil
	}
	c.logger.Info("rate limited: waiting for window reset",
		"resource", resource,
		"wait", waitDur.Round(time.Second),
	)

	select {
	case <-time.After(waitDur):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// reportAuthResult invokes the auth result hooks.
func (c *Client) reportAuthResult(unauthorized bool) {
	for _, h := range c.authHooks {
		h(unauthorized)
	}
}

// ---------------------------------------------------------------------------
// rateLimitTransport: wraps an http.RoundTripper, reads rate limit headers.
// ---------------------------------------------------------------------------

type rateLimitTransport struct {
	client *Client
	inner  http.RoundTripper
}

func (t *rateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	pool := t.client.classifyPool(req.URL)
	if err := t.client.waitIfPaused(req.Context(), pool); err != nil {
		return nil, err
	}

	resp, err := t.inner.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	t.client.reportAuthResult(resp.StatusCode == http.StatusUnauthorized)

	// Parse rate limit headers (REST and GraphQL send the same ones).
	if rem := resp.Header.Get("X-RateLimit-Remaining"); rem != "" {
		remaining, err := strconv.Atoi(rem)
		if err != nil {
			// Malformed header — skip the update rather than defaulting to 0
			// (defaulting to 0 would spuriously trigger the pause logic).
			t.client.logger.Debug("ignoring malformed X-RateLimit-Remaining header", "value", rem)
		} else {
			var resetAt time.Time
			if resetStr := resp.Header.Get("X-RateLimit-Reset"); resetStr != "" {
				if resetUnix, err := strconv.ParseInt(resetStr, 10, 64); err == nil {
					resetAt = time.Unix(resetUnix, 0)
				}
			}
			// Only enter paused state if we have a valid future reset time.
			// Without a reset time, pause would block indefinitely (zero time → no wait).
			resource := responsePool(resp.Header.Get("X-RateLimit-Resource"), pool)
			if remaining < rateLimitPauseThreshold && resetAt.IsZero() {
				t.client.logger.Warn("rate limit low but X-RateLimit-Reset missing; applying 60s fallback backoff",
					"resource", resource,
					"remaining", remaining,
				)
				resetAt = time.Now().Add(60 * time.Second)
			}
			t.client.updateRateLimit(resource, remaining, resetAt)
		}
	}

	return resp, nil
}

// ---------------------------------------------------------------------------
// authenticatorTokenSource: bridges Authenticator to oauth2.TokenSource.
// ---------------------------------------------------------------------------

// tokenFetchTimeout is the maximum time allowed to fetch/refresh a token.
// oauth2.TokenSource.Token() has no context parameter, so we enforce a
// deadline here to prevent unbounded hangs on network stalls.
const tokenFetchTimeout = 30 * time.Second

type authenticatorTokenSource struct {
	auth   Authenticator
	client *Client
}

func (s *authenticatorTokenSource) Token() (*oauth2.Token, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tokenFetchTimeout)
	defer cancel()
	tok, err := s.auth.Token(ctx)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			s.client.reportAuthResult(true)
		}
		return nil, err
	}
	return &oauth2.Token{AccessToken: tok}, nil
}
