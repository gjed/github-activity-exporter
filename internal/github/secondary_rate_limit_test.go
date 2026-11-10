package github_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	internalgithub "github.com/gjed/github-activity-exporter/internal/github"
)

// sleeps records the waits of a client instead of sleeping.
type sleeps struct {
	mu sync.Mutex
	d  []time.Duration
}

func (s *sleeps) sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.d = append(s.d, d)
	s.mu.Unlock()
	return ctx.Err()
}

func (s *sleeps) all() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.d...)
}

// about reports whether got is within 2s below want: a wait computed from a
// deadline has already lost a little time.
func about(got, want time.Duration) bool {
	return got <= want && got > want-2*time.Second
}

// scriptedServer answers the n-th request (from 0) with respond(n, r, w), and
// every request with rate limit headers for a healthy pool unless respond
// sets its own.
func scriptedServer(t *testing.T, respond func(n int, r *http.Request, w http.ResponseWriter) bool) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(hits.Add(1) - 1)
		if respond(n, r, w) {
			return
		}
		w.Header().Set("X-RateLimit-Remaining", "4000")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		if strings.HasSuffix(r.URL.Path, "/graphql") {
			_, _ = w.Write([]byte(`{"data":{"viewer":{"login":"me"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// newSleepClient builds a client on srv with recorded sleeps and a counter
// of secondary rate limit hook calls.
func newSleepClient(t *testing.T, srv *httptest.Server, opts ...internalgithub.ClientOption) (*internalgithub.Client, *sleeps, *atomic.Int64) {
	t.Helper()
	s := &sleeps{}
	var hooks atomic.Int64
	opts = append(opts,
		internalgithub.WithSleep(s.sleep),
		internalgithub.WithSecondaryRateLimitHook(func() { hooks.Add(1) }),
	)
	return newPoolClient(t, srv.URL, opts...), s, &hooks
}

// A 403 with Retry-After pauses every request of the target, in every pool,
// and the request is retried after the pause.
func TestSecondaryRateLimit_RetryAfterPausesTarget(t *testing.T) {
	srv, hits := scriptedServer(t, func(n int, _ *http.Request, w http.ResponseWriter) bool {
		if n == 0 {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"You have exceeded a secondary rate limit."}`))
			return true
		}
		return false
	})
	c, s, hooks := newSleepClient(t, srv)

	if err := graphqlCall(context.Background(), c); err != nil {
		t.Fatalf("graphql call: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Errorf("requests = %d, want 2 (one retry)", got)
	}
	if got := hooks.Load(); got != 1 {
		t.Errorf("secondary hook calls = %d, want 1", got)
	}
	if d := s.all(); len(d) != 1 || !about(d[0], 30*time.Second) {
		t.Fatalf("sleeps = %v, want one of about 30s", d)
	}

	// The pause holds the core pool too.
	if err := restCall(context.Background(), c); err != nil {
		t.Fatalf("rest call: %v", err)
	}
	if d := s.all(); len(d) != 2 || !about(d[1], 30*time.Second) {
		t.Errorf("sleeps = %v, want a second one of about 30s before the REST call", d)
	}
}

// Without Retry-After, a 429 pauses 60s, doubling on each further one.
func TestSecondaryRateLimit_FallbackDoubles(t *testing.T) {
	srv, hits := scriptedServer(t, func(n int, _ *http.Request, w http.ResponseWriter) bool {
		if n < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		return false
	})
	c, s, hooks := newSleepClient(t, srv)

	if err := restCall(context.Background(), c); err != nil {
		t.Fatalf("rest call: %v", err)
	}
	if got := hits.Load(); got != 3 {
		t.Errorf("requests = %d, want 3", got)
	}
	if got := hooks.Load(); got != 2 {
		t.Errorf("secondary hook calls = %d, want 2", got)
	}
	d := s.all()
	if len(d) != 2 || !about(d[0], time.Minute) || !about(d[1], 2*time.Minute) {
		t.Errorf("sleeps = %v, want about [1m 2m]", d)
	}
}

// A 403 that is not a rate limit is returned as is.
func TestSecondaryRateLimit_Plain403PassesThrough(t *testing.T) {
	srv, hits := scriptedServer(t, func(_ int, _ *http.Request, w http.ResponseWriter) bool {
		w.Header().Set("X-RateLimit-Remaining", "4000")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
		return true
	})
	c, s, hooks := newSleepClient(t, srv)

	err := restCall(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "Resource not accessible by integration") {
		t.Fatalf("err = %v, want the 403 with its body", err)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
	if hooks.Load() != 0 || len(s.all()) != 0 {
		t.Errorf("hooks = %d, sleeps = %v, want none", hooks.Load(), s.all())
	}
}

// After 3 retries the last rate limit response goes to the caller, body
// included, and every response is counted.
func TestSecondaryRateLimit_RetriesSpent(t *testing.T) {
	srv, hits := scriptedServer(t, func(_ int, _ *http.Request, w http.ResponseWriter) bool {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"You have exceeded a secondary rate limit. Please wait."}`))
		return true
	})
	c, _, hooks := newSleepClient(t, srv)

	err := graphqlCall(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "secondary rate limit") {
		t.Fatalf("err = %v, want the last 403 with its body", err)
	}
	if got := hits.Load(); got != 4 {
		t.Errorf("requests = %d, want 4", got)
	}
	if got := hooks.Load(); got != 4 {
		t.Errorf("secondary hook calls = %d, want 4", got)
	}
}

// A primary exhaustion pauses only its pool until the reset, then retries,
// and is not counted as a secondary rate limit.
func TestSecondaryRateLimit_PrimaryExhaustionPausesOnlyPool(t *testing.T) {
	srv, hits := scriptedServer(t, func(n int, _ *http.Request, w http.ResponseWriter) bool {
		if n == 0 {
			w.Header().Set("X-RateLimit-Resource", "graphql")
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10))
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
			return true
		}
		return false
	})
	c, s, hooks := newSleepClient(t, srv)

	if err := graphqlCall(context.Background(), c); err != nil {
		t.Fatalf("graphql call: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
	if d := s.all(); len(d) != 1 || !about(d[0], 10*time.Minute) {
		t.Fatalf("sleeps = %v, want one of about 10m", d)
	}
	if got := hooks.Load(); got != 0 {
		t.Errorf("secondary hook calls = %d, want 0", got)
	}

	if err := restCall(context.Background(), c); err != nil {
		t.Fatalf("rest call: %v", err)
	}
	if d := s.all(); len(d) != 1 {
		t.Errorf("sleeps = %v: the REST call waited", d)
	}
}

// Cancellation during a pause returns at once.
func TestSecondaryRateLimit_CancelDuringPause(t *testing.T) {
	srv, _ := scriptedServer(t, func(_ int, _ *http.Request, w http.ResponseWriter) bool {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
		return true
	})
	c := newPoolClient(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := restCall(ctx, c)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Errorf("returned after %s", el)
	}
}

// Every request of the target sent after a secondary rate limit response
// waits for the same pause, whichever worker sends it.
func TestSecondaryRateLimit_ConcurrentRequestsWait(t *testing.T) {
	srv, _ := scriptedServer(t, func(n int, _ *http.Request, w http.ResponseWriter) bool {
		if n == 0 {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		return false
	})
	c, s, _ := newSleepClient(t, srv)
	if err := graphqlCall(context.Background(), c); err != nil {
		t.Fatalf("graphql call: %v", err)
	}

	const workers = 10
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			call := restCall
			if i%2 == 0 {
				call = graphqlCall
			}
			if err := call(context.Background(), c); err != nil {
				t.Errorf("call: %v", err)
			}
		})
	}
	wg.Wait()
	d := s.all()
	if len(d) != 1+workers {
		t.Fatalf("sleeps = %d, want %d", len(d), 1+workers)
	}
	for _, w := range d {
		if !about(w, 30*time.Second) {
			t.Errorf("sleep %s, want about 30s", w)
		}
	}
}

// Concurrent requests to a paused pool all wait for its reset.
func TestRateLimit_ConcurrentRequestsWaitOnPausedPool(t *testing.T) {
	srv := newPoolServer(t, "/graphql", func(string) poolHeaders {
		return poolHeaders{resource: "graphql", remaining: 100}
	})
	s := &sleeps{}
	c := newPoolClient(t, srv.URL, internalgithub.WithSleep(s.sleep))
	if err := graphqlCall(context.Background(), c); err != nil {
		t.Fatalf("graphql call: %v", err)
	}

	const workers = 10
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() { _ = graphqlCall(context.Background(), c) })
	}
	wg.Wait()
	d := s.all()
	if len(d) != workers {
		t.Fatalf("sleeps = %d, want %d", len(d), workers)
	}
	for _, w := range d {
		if !about(w, time.Hour) {
			t.Errorf("sleep %s, want about 1h", w)
		}
	}
}

// Concurrent 401 responses are each reported once to the auth hook.
func TestAuthResult_ConcurrentUnauthorized(t *testing.T) {
	srv, _ := scriptedServer(t, func(_ int, _ *http.Request, w http.ResponseWriter) bool {
		w.WriteHeader(http.StatusUnauthorized)
		return true
	})
	var unauthorized, ok atomic.Int64
	c := newPoolClient(t, srv.URL, internalgithub.WithAuthResultHook(func(u bool) {
		if u {
			unauthorized.Add(1)
		} else {
			ok.Add(1)
		}
	}))

	const workers = 10
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() { _ = restCall(context.Background(), c) })
	}
	wg.Wait()
	if unauthorized.Load() != workers || ok.Load() != 0 {
		t.Errorf("401 reports = %d, others = %d, want %d and 0", unauthorized.Load(), ok.Load(), workers)
	}
}
