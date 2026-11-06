// Package health tracks per-target collection state and serves the /healthz
// (liveness) and /readyz (readiness) endpoints.
package health

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Tracker holds the health state of every target. All methods are safe for
// concurrent use; methods called with an unregistered target are no-ops.
type Tracker struct {
	stallTimeout       time.Duration
	unauthorizedCycles int
	now                func() time.Time

	mu      sync.Mutex
	order   []string
	targets map[string]*targetState
}

type targetState struct {
	credentialChecked bool
	loopStartedAt     time.Time // zero until the collection loop starts
	lastCycleAt       time.Time // zero until the first cycle completes
	ready             bool
	loopExited        bool
	consecutiveUnauth int

	// Response counts for the current cycle.
	unauthResponses int
	otherResponses  int
}

// Option configures a Tracker.
type Option func(*Tracker)

// WithClock overrides the time source. For tests.
func WithClock(now func() time.Time) Option {
	return func(t *Tracker) { t.now = now }
}

// NewTracker creates a Tracker. /healthz fails for a target that completes no
// cycle within stallTimeout, or whose last unauthorizedCycles cycles were all
// unauthorized.
func NewTracker(stallTimeout time.Duration, unauthorizedCycles int, opts ...Option) *Tracker {
	t := &Tracker{
		stallTimeout:       stallTimeout,
		unauthorizedCycles: unauthorizedCycles,
		now:                time.Now,
		targets:            make(map[string]*targetState),
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Register adds a target. Registered targets count towards /readyz from the
// start.
func (t *Tracker) Register(target string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.targets[target]; ok {
		return
	}
	t.order = append(t.order, target)
	t.targets[target] = &targetState{}
}

// update runs fn on the target's state under the lock.
func (t *Tracker) update(target string, fn func(s *targetState)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok := t.targets[target]; ok {
		fn(s)
	}
}

// CredentialChecked records that the target passed its startup credential check.
func (t *Tracker) CredentialChecked(target string) {
	t.update(target, func(s *targetState) { s.credentialChecked = true })
}

// LoopStarted records that the target's collection loop started.
func (t *Tracker) LoopStarted(target string) {
	now := t.now()
	t.update(target, func(s *targetState) { s.loopStartedAt = now })
}

// LoopExited records that the target's collection loop returned before shutdown.
func (t *Tracker) LoopExited(target string) {
	t.update(target, func(s *targetState) { s.loopExited = true })
}

// AuthResult records one GitHub response for the target's current cycle.
func (t *Tracker) AuthResult(target string, unauthorized bool) {
	t.update(target, func(s *targetState) {
		if unauthorized {
			s.unauthResponses++
		} else {
			s.otherResponses++
		}
	})
}

// CycleStarted resets the target's per-cycle response counts.
func (t *Tracker) CycleStarted(target string) {
	t.update(target, func(s *targetState) {
		s.unauthResponses, s.otherResponses = 0, 0
	})
}

// CycleFinished records a completed cycle. The target becomes ready on its
// first cycle with successful repo discovery. A cycle with at least one 401
// and no other response is unauthorized; a cycle with any other response
// resets the count; a cycle with no response leaves it unchanged.
func (t *Tracker) CycleFinished(target string, discoveryOK bool) {
	now := t.now()
	t.update(target, func(s *targetState) {
		s.lastCycleAt = now
		if discoveryOK {
			s.ready = true
		}
		switch {
		case s.otherResponses > 0:
			s.consecutiveUnauth = 0
		case s.unauthResponses > 0:
			s.consecutiveUnauth++
		}
	})
}

// liveness returns one problem line per unhealthy target.
func (t *Tracker) liveness() []string {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	var problems []string
	for _, name := range t.order {
		s := t.targets[name]
		if s.loopExited {
			problems = append(problems, fmt.Sprintf("target %q: collection loop exited", name))
		}
		if !s.loopStartedAt.IsZero() {
			since := s.lastCycleAt
			if since.IsZero() {
				since = s.loopStartedAt
			}
			if now.Sub(since) > t.stallTimeout {
				problems = append(problems, fmt.Sprintf("target %q: stalled, no collection cycle completed since %s (stall timeout %s)",
					name, since.UTC().Format(time.RFC3339), t.stallTimeout))
			}
		}
		if s.consecutiveUnauth >= t.unauthorizedCycles {
			problems = append(problems, fmt.Sprintf("target %q: unauthorized, GitHub answered only 401 in %d cycles in a row",
				name, s.consecutiveUnauth))
		}
	}
	return problems
}

// readiness returns one problem line per target that is not ready.
func (t *Tracker) readiness() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var problems []string
	for _, name := range t.order {
		s := t.targets[name]
		switch {
		case !s.credentialChecked:
			problems = append(problems, fmt.Sprintf("target %q: credential check pending", name))
		case !s.ready:
			problems = append(problems, fmt.Sprintf("target %q: no collection cycle with successful repo discovery yet", name))
		}
	}
	return problems
}

// Handler serves /healthz and /readyz.
func (t *Tracker) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/healthz", probe(t.liveness))
	mux.Handle("/readyz", probe(t.readiness))
	return mux
}

// probe answers 200 "ok" when check reports no problems, 503 with one line
// per problem otherwise.
func probe(check func() []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if problems := check(); len(problems) > 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprintln(w, strings.Join(problems, "\n"))
			return
		}
		_, _ = fmt.Fprintln(w, "ok")
	})
}
