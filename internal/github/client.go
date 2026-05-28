package github

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/go-github/v62/github"
	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"
)

const (
	// rateLimitPauseThreshold is the number of remaining REST API requests below
	// which new requests are paused until the window resets.
	rateLimitPauseThreshold = 200
)

// RateLimitState holds the current rate limit snapshot for a target.
type RateLimitState struct {
	Remaining int
	ResetAt   time.Time
}

// RateLimitHook is called whenever the rate limit state changes.
// It is invoked from the response transport, so implementations must not block.
type RateLimitHook func(state RateLimitState)

// Client wraps the GitHub REST v3 and GraphQL v4 clients with authentication
// and per-target rate limit tracking.
type Client struct {
	auth   Authenticator
	logger *slog.Logger

	// REST client
	rest *github.Client

	// GraphQL client
	graphql *githubv4.Client

	// Rate limit state
	mu         sync.Mutex
	rlState    RateLimitState
	rlPaused   bool
	rlHooks    []RateLimitHook

	// baseURL allows tests to override the GitHub API URL.
	baseURL string
}

// ClientOption is a functional option for Client.
type ClientOption func(*Client)

// WithRateLimitHook registers a callback invoked on rate limit state changes.
func WithRateLimitHook(hook RateLimitHook) ClientOption {
	return func(c *Client) {
		c.rlHooks = append(c.rlHooks, hook)
	}
}

// WithLogger sets the logger for the client.
func WithLogger(logger *slog.Logger) ClientOption {
	return func(c *Client) {
		c.logger = logger
	}
}

// WithBaseURL overrides the API base URL (for testing or GitHub Enterprise).
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
	}
	for _, o := range opts {
		o(c)
	}

	transport := &rateLimitTransport{client: c, inner: http.DefaultTransport}
	tokenSource := &authenticatorTokenSource{auth: auth}
	oauthTransport := &oauth2.Transport{Source: tokenSource, Base: transport}
	httpClient := &http.Client{Transport: oauthTransport}

	if c.baseURL == defaultGitHubAPIURL {
		c.rest = github.NewClient(httpClient)
		c.graphql = githubv4.NewClient(httpClient)
	} else {
		var err error
		c.rest, err = github.NewClient(httpClient).WithEnterpriseURLs(c.baseURL+"/", c.baseURL+"/")
		if err != nil {
			return nil, fmt.Errorf("creating REST client with custom URL: %w", err)
		}
		c.graphql = githubv4.NewEnterpriseClient(c.baseURL+"/graphql", httpClient)
	}

	return c, nil
}

// REST returns the underlying go-github REST client.
func (c *Client) REST() *github.Client { return c.rest }

// GraphQL returns the underlying githubv4 GraphQL client.
func (c *Client) GraphQL() *githubv4.Client { return c.graphql }

// RateLimitState returns a snapshot of the current rate limit state.
func (c *Client) RateLimitState() RateLimitState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rlState
}

// updateRateLimit records a new rate limit snapshot and invokes hooks.
func (c *Client) updateRateLimit(remaining int, resetAt time.Time) {
	c.mu.Lock()
	c.rlState = RateLimitState{Remaining: remaining, ResetAt: resetAt}
	pausing := remaining < rateLimitPauseThreshold
	if pausing && !c.rlPaused {
		c.rlPaused = true
		c.logger.Warn("rate limit threshold reached, pausing requests",
			"remaining", remaining,
			"reset_at", resetAt,
		)
	} else if !pausing && c.rlPaused {
		c.rlPaused = false
		c.logger.Info("rate limit recovered, resuming requests", "remaining", remaining)
	}
	hooks := make([]RateLimitHook, len(c.rlHooks))
	copy(hooks, c.rlHooks)
	state := c.rlState
	c.mu.Unlock()

	for _, h := range hooks {
		h(state)
	}
}

// waitIfPaused blocks until the rate limit window resets (if paused).
func (c *Client) waitIfPaused(ctx context.Context) error {
	c.mu.Lock()
	if !c.rlPaused {
		c.mu.Unlock()
		return nil
	}
	resetAt := c.rlState.ResetAt
	c.mu.Unlock()

	waitDur := time.Until(resetAt)
	if waitDur <= 0 {
		return nil
	}
	c.logger.Info("rate limited: waiting for window reset", "wait", waitDur.Round(time.Second))

	select {
	case <-time.After(waitDur):
		return nil
	case <-ctx.Done():
		return ctx.Err()
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
	if err := t.client.waitIfPaused(req.Context()); err != nil {
		return nil, err
	}

	resp, err := t.inner.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	// Parse REST rate limit headers.
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
			if remaining < rateLimitPauseThreshold && resetAt.IsZero() {
				t.client.logger.Warn("rate limit low but X-RateLimit-Reset missing; applying 60s fallback backoff",
					"remaining", remaining,
				)
				resetAt = time.Now().Add(60 * time.Second)
			}
			t.client.updateRateLimit(remaining, resetAt)
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
	auth Authenticator
}

func (s *authenticatorTokenSource) Token() (*oauth2.Token, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tokenFetchTimeout)
	defer cancel()
	tok, err := s.auth.Token(ctx)
	if err != nil {
		return nil, err
	}
	return &oauth2.Token{AccessToken: tok}, nil
}
