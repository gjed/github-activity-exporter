package health_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gjed/github-activity-exporter/internal/health"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

const stall = 70 * time.Minute

func newTracker(cycles int, targets ...string) (*health.Tracker, *fakeClock) {
	clk := &fakeClock{t: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)}
	tr := health.NewTracker(stall, cycles, health.WithClock(clk.now))
	for _, name := range targets {
		tr.Register(name)
	}
	return tr, clk
}

// start marks the target's credential checked and its loop started.
func start(tr *health.Tracker, target string) {
	tr.CredentialChecked(target)
	tr.LoopStarted(target)
}

// cycle runs one cycle in which GitHub answered with the given statuses.
func cycle(tr *health.Tracker, target string, discoveryOK bool, statuses ...int) {
	tr.CycleStarted(target)
	for _, s := range statuses {
		tr.AuthResult(target, s == http.StatusUnauthorized)
	}
	tr.CycleFinished(target, discoveryOK)
}

func get(t *testing.T, tr *health.Tracker, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	tr.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, string(body)
}

func expect(t *testing.T, tr *health.Tracker, path string, wantCode int, wantBody string) {
	t.Helper()
	code, body := get(t, tr, path)
	if code != wantCode || !strings.Contains(body, wantBody) {
		t.Errorf("GET %s = %d %q, want %d containing %q", path, code, body, wantCode, wantBody)
	}
}

func TestStartup(t *testing.T) {
	tr, _ := newTracker(3, "acme")

	expect(t, tr, "/healthz", http.StatusOK, "ok")
	expect(t, tr, "/readyz", http.StatusServiceUnavailable, `target "acme": credential check pending`)

	start(tr, "acme")
	expect(t, tr, "/readyz", http.StatusServiceUnavailable, `target "acme": no collection cycle`)
}

func TestReadyAfterEveryTargetCollected(t *testing.T) {
	tr, _ := newTracker(3, "a", "b")
	start(tr, "a")
	start(tr, "b")

	cycle(tr, "a", true, http.StatusOK)
	expect(t, tr, "/readyz", http.StatusServiceUnavailable, `target "b"`)
	if _, body := get(t, tr, "/readyz"); strings.Contains(body, `"a"`) {
		t.Errorf("ready target a listed in /readyz body %q", body)
	}

	cycle(tr, "b", true, http.StatusOK)
	expect(t, tr, "/readyz", http.StatusOK, "ok")

	// Readiness sticks, even through failed discovery and 401s.
	for i := 0; i < 3; i++ {
		cycle(tr, "a", false, http.StatusUnauthorized)
	}
	expect(t, tr, "/readyz", http.StatusOK, "ok")
}

func TestDiscoveryFailureDoesNotMakeReady(t *testing.T) {
	tr, _ := newTracker(3, "acme")
	start(tr, "acme")
	cycle(tr, "acme", false, http.StatusBadGateway)

	expect(t, tr, "/readyz", http.StatusServiceUnavailable, `target "acme"`)
	expect(t, tr, "/healthz", http.StatusOK, "ok")
}

func TestStall(t *testing.T) {
	tr, clk := newTracker(3, "acme")
	start(tr, "acme")

	clk.advance(stall)
	expect(t, tr, "/healthz", http.StatusOK, "ok")
	clk.advance(time.Second)
	expect(t, tr, "/healthz", http.StatusServiceUnavailable, `target "acme": stalled`)

	cycle(tr, "acme", true, http.StatusOK)
	expect(t, tr, "/healthz", http.StatusOK, "ok")

	clk.advance(stall + time.Second)
	expect(t, tr, "/healthz", http.StatusServiceUnavailable, `target "acme": stalled`)
}

func TestCustomStallTimeout(t *testing.T) {
	clk := &fakeClock{t: time.Now()}
	tr := health.NewTracker(30*time.Minute, 3, health.WithClock(clk.now))
	tr.Register("acme")
	start(tr, "acme")
	cycle(tr, "acme", true, http.StatusOK)

	clk.advance(31 * time.Minute)
	expect(t, tr, "/healthz", http.StatusServiceUnavailable, "stalled")
}

func TestLoopExited(t *testing.T) {
	tr, _ := newTracker(3, "acme")
	start(tr, "acme")
	tr.LoopExited("acme")

	expect(t, tr, "/healthz", http.StatusServiceUnavailable, `target "acme": collection loop exited`)
}

func TestUnauthorizedThreshold(t *testing.T) {
	tr, _ := newTracker(3, "acme")
	start(tr, "acme")

	cycle(tr, "acme", false, http.StatusUnauthorized)
	cycle(tr, "acme", false, http.StatusUnauthorized, http.StatusUnauthorized)
	expect(t, tr, "/healthz", http.StatusOK, "ok")

	cycle(tr, "acme", false, http.StatusUnauthorized)
	expect(t, tr, "/healthz", http.StatusServiceUnavailable, `target "acme": unauthorized`)
	expect(t, tr, "/readyz", http.StatusServiceUnavailable, "no collection cycle")

	// Recovery: one cycle that is not unauthorized resets the count.
	cycle(tr, "acme", true, http.StatusOK)
	expect(t, tr, "/healthz", http.StatusOK, "ok")
}

func TestCustomUnauthorizedThreshold(t *testing.T) {
	tr, _ := newTracker(1, "acme")
	start(tr, "acme")
	cycle(tr, "acme", false, http.StatusUnauthorized)

	expect(t, tr, "/healthz", http.StatusServiceUnavailable, "unauthorized")
}

func TestIsolated401ResetsCount(t *testing.T) {
	tr, _ := newTracker(3, "acme")
	start(tr, "acme")

	cycle(tr, "acme", true, http.StatusUnauthorized)
	cycle(tr, "acme", true, http.StatusUnauthorized)
	cycle(tr, "acme", true, http.StatusUnauthorized, http.StatusOK)
	cycle(tr, "acme", true, http.StatusUnauthorized)
	cycle(tr, "acme", true, http.StatusUnauthorized)

	expect(t, tr, "/healthz", http.StatusOK, "ok")
}

func TestCycleWithoutResponsesLeavesCountUnchanged(t *testing.T) {
	tr, _ := newTracker(3, "acme")
	start(tr, "acme")

	cycle(tr, "acme", false)
	expect(t, tr, "/healthz", http.StatusOK, "ok")

	cycle(tr, "acme", false, http.StatusUnauthorized)
	cycle(tr, "acme", false, http.StatusUnauthorized)
	cycle(tr, "acme", false)
	expect(t, tr, "/healthz", http.StatusOK, "ok")

	cycle(tr, "acme", false, http.StatusUnauthorized)
	expect(t, tr, "/healthz", http.StatusServiceUnavailable, "unauthorized")
}

func TestMethods(t *testing.T) {
	tr, _ := newTracker(3)
	h := tr.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("HEAD /readyz = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz = %d, want 405", rec.Code)
	}
}
