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
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
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

	// maxRateLimitRetries is how many times a request answered with a rate
	// limit (403 or 429) is sent again after the pause.
	maxRateLimitRetries = 3

	// secondaryFallbackWait is the pause after a secondary rate limit
	// response that gives no Retry-After, doubled on each further one of the
	// same request, as GitHub's guidance asks.
	secondaryFallbackWait = time.Minute

	// secondaryBodyLimit is how much of a 403 body is read to look for a
	// secondary rate limit message.
	secondaryBodyLimit = 64 << 10
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

// SecondaryRateLimitHook is called once per GitHub response treated as a
// secondary rate limit. It runs in the client transport, so implementations
// must not block.
type SecondaryRateLimitHook func()

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

	secondaryHooks []SecondaryRateLimitHook
	// pausedUntil holds every request of the target until then, after a
	// secondary rate limit; guarded by mu.
	pausedUntil time.Time
	// sleep waits for d or until ctx is done; replaced in tests.
	sleep func(ctx context.Context, d time.Duration) error

	// tracer records rate limit waits as spans.
	tracer trace.Tracer

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

// WithSecondaryRateLimitHook registers a callback invoked on every response
// treated as a secondary rate limit.
func WithSecondaryRateLimitHook(hook SecondaryRateLimitHook) ClientOption {
	return func(c *Client) {
		c.secondaryHooks = append(c.secondaryHooks, hook)
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

// WithTracer records each wait for a rate limit pause as a
// github_exporter.rate_limit.wait span in the waiting request's context.
// Default: no spans.
func WithTracer(t trace.Tracer) ClientOption {
	return func(c *Client) {
		if t != nil {
			c.tracer = t
		}
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
		sleep:   sleepCtx,
		tracer:  tracenoop.NewTracerProvider().Tracer(""),
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

func (e GraphQLError) Error() string { return "graphql " + e.Type + ": " + e.Message }

// ErrorType returns the error.type of a failed GitHub collection, bounded:
// the GraphQL error type when GitHub returned one, the HTTP status code of a
// REST error or rate limit response, "_OTHER" otherwise.
func ErrorType(err error) string {
	var gqlErr GraphQLError
	if errors.As(err, &gqlErr) && gqlErr.Type != "" {
		return gqlErr.Type
	}
	var restErr *github.ErrorResponse
	if errors.As(err, &restErr) && restErr.Response != nil {
		return strconv.Itoa(restErr.Response.StatusCode)
	}
	var rlErr *github.RateLimitError
	if errors.As(err, &rlErr) && rlErr.Response != nil {
		return strconv.Itoa(rlErr.Response.StatusCode)
	}
	var abuseErr *github.AbuseRateLimitError
	if errors.As(err, &abuseErr) && abuseErr.Response != nil {
		return strconv.Itoa(abuseErr.Response.StatusCode)
	}
	return "_OTHER"
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
	return c.wait(ctx, waitDur, resource, sc.AttrGitHubExporterRateLimitReasonPrimary)
}

// wait sleeps for d as a github_exporter.rate_limit.wait span of a request
// to pool, paused for reason.
func (c *Client) wait(ctx context.Context, d time.Duration, pool, reason string) error {
	ctx, span := c.tracer.Start(ctx, sc.SpanGitHubExporterRateLimitWait, trace.WithAttributes(
		attribute.String(sc.AttrGitHubApiRateLimitResource, pool),
		attribute.String(sc.AttrGitHubExporterRateLimitReason, reason),
	))
	defer span.End()
	err := c.sleep(ctx, d)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.String(attrErrorType, ErrorType(err)))
	}
	return err
}

// resendKey carries the http.request.resend_count of a retried request to
// the base transport.
type resendKey struct{}

// resendCount returns how many times the request in ctx was sent before.
func resendCount(ctx context.Context) int {
	n, _ := ctx.Value(resendKey{}).(int)
	return n
}

// sleepCtx waits for d or until ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// waitIfTargetPaused blocks until a secondary rate limit pause of the
// target ends, if one is in progress. It holds requests of every pool; pool
// is the waiting request's.
// waited is the end of the last pause the request already waited for: a
// pause extended during the wait is waited for again, one already waited
// for is not.
func (c *Client) waitIfTargetPaused(ctx context.Context, pool string, waited *time.Time) error {
	for {
		c.mu.Lock()
		until := c.pausedUntil
		c.mu.Unlock()
		if time.Until(until) <= 0 || !until.After(*waited) {
			return nil
		}
		if err := c.wait(ctx, time.Until(until), pool, sc.AttrGitHubExporterRateLimitReasonSecondary); err != nil {
			return err
		}
		*waited = until
	}
}

// pauseTarget holds every request of the target for d. A pause already in
// progress that ends later is kept, so concurrent rate limited requests do
// not shorten each other's pause.
func (c *Client) pauseTarget(d time.Duration) {
	until := time.Now().Add(d)
	c.mu.Lock()
	if until.After(c.pausedUntil) {
		c.pausedUntil = until
	}
	c.mu.Unlock()
}

// rateLimitKind classifies a response as no rate limit, a primary rate
// limit (its pool is exhausted) or a secondary rate limit.
type rateLimitKind uint8

const (
	notRateLimited rateLimitKind = iota
	primaryRateLimit
	secondaryRateLimit
)

// classifyRateLimit tells whether resp is a rate limit and, for a secondary
// one, how long to pause the target: Retry-After when present, else
// secondaryFallbackWait doubled for each earlier attempt. A primary limit
// (X-RateLimit-Remaining: 0) pauses only its pool, through updateRateLimit.
// A 403 is a secondary limit only with Retry-After or a body naming one; any
// other 403 (no access) is returned as is. The body, when read, is restored.
func classifyRateLimit(resp *http.Response, attempt int) (rateLimitKind, time.Duration) {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return notRateLimited, 0
	}
	if resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return primaryRateLimit, 0
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			return secondaryRateLimit, max(time.Duration(secs)*time.Second, 0)
		}
		if at, err := http.ParseTime(ra); err == nil {
			return secondaryRateLimit, max(time.Until(at), 0)
		}
	}
	fallback := secondaryFallbackWait << (attempt - 1)
	if resp.StatusCode == http.StatusTooManyRequests {
		return secondaryRateLimit, fallback
	}
	if resp.Body == nil {
		return notRateLimited, 0
	}
	head, _ := io.ReadAll(io.LimitReader(resp.Body, secondaryBodyLimit))
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), resp.Body), resp.Body}
	if bytes.Contains(bytes.ToLower(head), []byte("secondary rate limit")) {
		return secondaryRateLimit, fallback
	}
	return notRateLimited, 0
}

// retryRequest returns req ready to be sent again: req itself without a
// body, else a clone with a fresh body. ok is false when the body cannot be
// replayed.
func retryRequest(req *http.Request) (next *http.Request, ok bool) {
	if req.Body == nil || req.Body == http.NoBody {
		return req, true
	}
	if req.GetBody == nil {
		return nil, false
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, false
	}
	next = req.Clone(req.Context())
	next.Body = body
	return next, true
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

// RoundTrip sends req once the target's secondary rate limit pause and its
// pool's pause are over. A response that is a rate limit is retried after
// the pause, at most maxRateLimitRetries times; then, or when the body
// cannot be replayed, it is returned to the caller.
func (t *rateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c := t.client
	pool := c.classifyPool(req.URL)
	var waited time.Time
	for attempt := 1; ; attempt++ {
		if err := c.waitIfTargetPaused(req.Context(), pool, &waited); err != nil {
			return nil, err
		}
		if err := c.waitIfPaused(req.Context(), pool); err != nil {
			return nil, err
		}
		// A target pause may have started during the pool wait.
		if err := c.waitIfTargetPaused(req.Context(), pool, &waited); err != nil {
			return nil, err
		}

		resp, err := t.inner.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		c.reportAuthResult(resp.StatusCode == http.StatusUnauthorized)
		resource := t.recordRateLimit(resp, pool)

		kind, wait := classifyRateLimit(resp, attempt)
		if kind == notRateLimited {
			return resp, nil
		}
		if kind == secondaryRateLimit {
			for _, h := range c.secondaryHooks {
				h()
			}
			c.pauseTarget(wait)
		}
		next, ok := retryRequest(req)
		if attempt > maxRateLimitRetries || !ok {
			c.logger.Warn("rate limited: giving up on the request",
				"status", resp.StatusCode,
				"resource", resource,
				"secondary", kind == secondaryRateLimit,
				"attempts", attempt,
			)
			return resp, nil
		}
		if kind == secondaryRateLimit {
			c.logger.Warn("secondary rate limit: pausing all requests of the target",
				"status", resp.StatusCode,
				"resource", resource,
				"wait", wait.Round(time.Second),
				"attempt", attempt,
			)
		} else if resource != pool {
			// The next attempt waits on the request's pool; wait on the
			// exhausted one too.
			if err := c.waitIfPaused(req.Context(), resource); err != nil {
				_ = resp.Body.Close()
				return nil, err
			}
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, secondaryBodyLimit))
		_ = resp.Body.Close()
		req = next.WithContext(context.WithValue(next.Context(), resendKey{}, attempt))
	}
}

// recordRateLimit records the X-RateLimit-* headers of resp, sent for a
// request classified in pool, and returns the pool they belong to.
func (t *rateLimitTransport) recordRateLimit(resp *http.Response, pool string) string {
	resource := responsePool(resp.Header.Get("X-RateLimit-Resource"), pool)
	rem := resp.Header.Get("X-RateLimit-Remaining")
	if rem == "" {
		return resource
	}
	remaining, err := strconv.Atoi(rem)
	if err != nil {
		// Malformed header — skip the update rather than defaulting to 0
		// (defaulting to 0 would spuriously trigger the pause logic).
		t.client.logger.Debug("ignoring malformed X-RateLimit-Remaining header", "value", rem)
		return resource
	}
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
			"resource", resource,
			"remaining", remaining,
		)
		resetAt = time.Now().Add(60 * time.Second)
	}
	t.client.updateRateLimit(resource, remaining, resetAt)
	return resource
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
