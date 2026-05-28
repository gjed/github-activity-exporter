package github_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	internalgithub "github.com/gjed/renovate-exporter/internal/github"
)

// mockAuthenticator is a simple PAT authenticator for tests.
type mockAuthenticator struct{ token string }

func (m *mockAuthenticator) Token(_ context.Context) (string, error) { return m.token, nil }
func (m *mockAuthenticator) Ping(_ context.Context) error            { return nil }

// TestClient_RateLimitTracking verifies that REST rate limit headers are parsed
// and the hook is invoked with the correct state.
func TestClient_RateLimitTracking(t *testing.T) {
	resetTime := time.Now().Add(10 * time.Minute).Unix()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "150")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", resetTime))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	var hookCalled atomic.Bool
	var gotState internalgithub.RateLimitState

	c, err := internalgithub.NewClient(
		&mockAuthenticator{token: "tok"},
		internalgithub.WithRateLimitHook(func(s internalgithub.RateLimitState) {
			hookCalled.Store(true)
			gotState = s
		}),
		internalgithub.WithBaseURL(srv.URL),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	// Make a REST call to trigger header parsing.
	_, _, err = c.REST().Repositories.Get(context.Background(), "owner", "repo")
	// Ignore 404 — we only care about headers being read.
	_ = err

	if !hookCalled.Load() {
		t.Fatal("rate limit hook was not called")
	}
	if gotState.Remaining != 150 {
		t.Errorf("expected remaining=150, got %d", gotState.Remaining)
	}
	if gotState.ResetAt.Unix() != resetTime {
		t.Errorf("expected reset_at=%d, got %d", resetTime, gotState.ResetAt.Unix())
	}
}

// TestClient_PauseBelowThreshold verifies that the client enters paused state
// when remaining drops below the threshold, and exits it after the reset window.
func TestClient_PauseBelowThreshold(t *testing.T) {
	// Reset window: 1.5s from now. Unix() truncates to seconds so we need > 1s.
	resetTime := time.Now().Add(1500 * time.Millisecond).Unix()

	callCount := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := callCount.Add(1)
		if n == 1 {
			// First call: set remaining below threshold; reset in ~1-2s (Unix truncation).
			w.Header().Set("X-RateLimit-Remaining", "50")
			w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", resetTime))
		} else {
			// After reset: plenty of quota.
			w.Header().Set("X-RateLimit-Remaining", "5000")
			w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Hour).Unix()))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c, err := internalgithub.NewClient(
		&mockAuthenticator{token: "tok"},
		internalgithub.WithBaseURL(srv.URL),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx := context.Background()

	// First call: triggers pause (remaining=50 < 200).
	_, _, _ = c.REST().Repositories.Get(ctx, "owner", "repo")

	state := c.RateLimitState()
	if state.Remaining != 50 {
		t.Fatalf("expected paused state with remaining=50, got %d", state.Remaining)
	}

	start := time.Now()
	// Second call: should wait until resetTime (~1-2s away due to Unix truncation).
	_, _, _ = c.REST().Repositories.Get(ctx, "owner", "repo")
	elapsed := time.Since(start)

	// Must have waited at least 500ms (reset is 1-2s away; give generous slack).
	if elapsed < 500*time.Millisecond {
		t.Errorf("expected second call to be delayed by ~1s pause, elapsed=%v", elapsed)
	}

	// After the pause, state should show plenty of quota.
	state = c.RateLimitState()
	if state.Remaining < 200 {
		t.Errorf("expected recovered state, got remaining=%d", state.Remaining)
	}
}

// TestClient_RateLimitState verifies the state accessor returns current values.
func TestClient_RateLimitState(t *testing.T) {
	resetTime := time.Now().Add(5 * time.Minute).Unix()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "4999")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", resetTime))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c, err := internalgithub.NewClient(
		&mockAuthenticator{token: "tok"},
		internalgithub.WithBaseURL(srv.URL),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, _, _ = c.REST().Repositories.Get(context.Background(), "owner", "repo")

	state := c.RateLimitState()
	if state.Remaining != 4999 {
		t.Errorf("expected remaining=4999, got %d", state.Remaining)
	}
}
