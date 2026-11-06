package github_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	internalgithub "github.com/gjed/github-activity-exporter/internal/github"
)

// poolHeaders are the rate limit headers a fake GitHub sends for one path.
// An empty resource omits X-RateLimit-Resource.
type poolHeaders struct {
	resource  string
	remaining int
}

// newPoolServer serves GraphQL on gqlPath and REST everywhere else, with the
// rate limit headers chosen per request by pick. The reset is an hour away,
// so a paused pool stays paused for the whole test.
func newPoolServer(t *testing.T, gqlPath string, pick func(path string) poolHeaders) *httptest.Server {
	t.Helper()
	reset := time.Now().Add(time.Hour).Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := pick(r.URL.Path)
		if h.resource != "" {
			w.Header().Set("X-RateLimit-Resource", h.resource)
		}
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", h.remaining))
		w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", reset))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == gqlPath {
			_, _ = w.Write([]byte(`{"data":{"viewer":{"login":"octocat"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newPoolClient(t *testing.T, baseURL string, opts ...internalgithub.ClientOption) *internalgithub.Client {
	t.Helper()
	opts = append([]internalgithub.ClientOption{internalgithub.WithBaseURL(baseURL)}, opts...)
	c, err := internalgithub.NewClient(&mockAuthenticator{token: "tok"}, opts...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func restCall(ctx context.Context, c *internalgithub.Client) error {
	_, _, err := c.REST().Repositories.Get(ctx, "owner", "repo")
	return err
}

func graphqlCall(ctx context.Context, c *internalgithub.Client) error {
	var q struct {
		Viewer struct{ Login string }
	}
	return c.GraphQL().Query(ctx, &q, nil)
}

// requireNoWait fails if call blocks on a rate limit pause.
func requireNoWait(t *testing.T, name string, call func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := call(ctx); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("%s waited %v; its pool is not low", name, elapsed)
	}
}

// requirePaused fails unless call blocks on a rate limit pause.
func requirePaused(t *testing.T, name string, call func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := call(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%s: got err %v, want it paused until the context deadline", name, err)
	}
}

// TestClient_LowGraphQLDoesNotPauseREST: a graphql pool below the threshold
// pauses GraphQL requests only.
func TestClient_LowGraphQLDoesNotPauseREST(t *testing.T) {
	srv := newPoolServer(t, "/graphql", func(path string) poolHeaders {
		if path == "/graphql" {
			return poolHeaders{resource: "graphql", remaining: 50}
		}
		return poolHeaders{resource: "core", remaining: 4000}
	})
	c := newPoolClient(t, srv.URL)

	if err := graphqlCall(context.Background(), c); err != nil {
		t.Fatalf("first GraphQL call: %v", err)
	}
	if got := c.RateLimitState("graphql").Remaining; got != 50 {
		t.Fatalf("graphql remaining = %d, want 50", got)
	}

	requireNoWait(t, "REST call", func(ctx context.Context) error { return restCall(ctx, c) })
	if got := c.RateLimitState("core").Remaining; got != 4000 {
		t.Errorf("core remaining = %d, want 4000", got)
	}
	requirePaused(t, "GraphQL call", func(ctx context.Context) error { return graphqlCall(ctx, c) })
}

// TestClient_LowCoreDoesNotPauseGraphQL: a core pool below the threshold
// pauses REST requests only.
func TestClient_LowCoreDoesNotPauseGraphQL(t *testing.T) {
	srv := newPoolServer(t, "/graphql", func(path string) poolHeaders {
		if path == "/graphql" {
			return poolHeaders{resource: "graphql", remaining: 4000}
		}
		return poolHeaders{resource: "core", remaining: 50}
	})
	c := newPoolClient(t, srv.URL)

	_ = restCall(context.Background(), c)
	if got := c.RateLimitState("core").Remaining; got != 50 {
		t.Fatalf("core remaining = %d, want 50", got)
	}

	requireNoWait(t, "GraphQL call", func(ctx context.Context) error { return graphqlCall(ctx, c) })
	requirePaused(t, "REST call", func(ctx context.Context) error { return restCall(ctx, c) })
}

// TestClient_RateLimitPool_MissingHeader: without X-RateLimit-Resource the
// state goes to the pool classified from the request URL, here on a GHES
// base URL (REST under /api/v3, GraphQL at /api/graphql).
func TestClient_RateLimitPool_MissingHeader(t *testing.T) {
	srv := newPoolServer(t, "/api/graphql", func(path string) poolHeaders {
		switch {
		case path == "/api/graphql":
			return poolHeaders{remaining: 50}
		case path == "/api/v3/search/code":
			return poolHeaders{remaining: 9}
		case strings.HasPrefix(path, "/api/v3/search/"):
			return poolHeaders{remaining: 29}
		default:
			return poolHeaders{remaining: 4000}
		}
	})
	c := newPoolClient(t, srv.URL+"/api/v3")
	ctx := context.Background()

	if err := graphqlCall(ctx, c); err != nil {
		t.Fatalf("GraphQL call: %v", err)
	}
	_ = restCall(ctx, c)
	_, _, _ = c.REST().Search.Code(ctx, "foo", nil)
	_, _, _ = c.REST().Search.Issues(ctx, "foo", nil)

	for pool, want := range map[string]int{"graphql": 50, "core": 4000, "code_search": 9, "search": 29} {
		if got := c.RateLimitState(pool).Remaining; got != want {
			t.Errorf("%s remaining = %d, want %d", pool, got, want)
		}
	}
	// The low graphql pool does not hold back REST.
	requireNoWait(t, "REST call", func(ctx context.Context) error { return restCall(ctx, c) })
}

// TestClient_RateLimitPool_UndocumentedHeader: a resource value GitHub does
// not document is recorded as "other" and pauses no request.
func TestClient_RateLimitPool_UndocumentedHeader(t *testing.T) {
	srv := newPoolServer(t, "/graphql", func(string) poolHeaders {
		return poolHeaders{resource: "brand_new_pool", remaining: 10}
	})
	c := newPoolClient(t, srv.URL)

	_ = restCall(context.Background(), c)

	if got := c.RateLimitState("other").Remaining; got != 10 {
		t.Errorf("other remaining = %d, want 10", got)
	}
	if got := c.RateLimitState("core"); got.Remaining != 0 || !got.ResetAt.IsZero() {
		t.Errorf("core state = %+v, want unset", got)
	}
	requireNoWait(t, "REST call", func(ctx context.Context) error { return restCall(ctx, c) })
}

// TestClient_RateLimitHook_ReportsPool: the hook receives each pool's own
// state, so consumers can keep one series per pool.
func TestClient_RateLimitHook_ReportsPool(t *testing.T) {
	srv := newPoolServer(t, "/graphql", func(path string) poolHeaders {
		if path == "/graphql" {
			return poolHeaders{resource: "graphql", remaining: 4500}
		}
		return poolHeaders{resource: "core", remaining: 4900}
	})

	var mu sync.Mutex
	got := map[string]int{}
	c := newPoolClient(t, srv.URL, internalgithub.WithRateLimitHook(func(s internalgithub.RateLimitState) {
		mu.Lock()
		defer mu.Unlock()
		got[s.Resource] = s.Remaining
	}))
	ctx := context.Background()

	_ = restCall(ctx, c)
	if err := graphqlCall(ctx, c); err != nil {
		t.Fatalf("GraphQL call: %v", err)
	}
	_ = restCall(ctx, c)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got["core"] != 4900 || got["graphql"] != 4500 {
		t.Errorf("hook states = %v, want map[core:4900 graphql:4500]", got)
	}
}
