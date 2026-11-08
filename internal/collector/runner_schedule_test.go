package collector_test

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.opentelemetry.io/otel/sdk/metric"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
)

// timedSource is a RepoSource whose Repos call, made once at the start of
// every cycle, takes durations[i] for cycle i (the last value repeats) and
// records when each cycle started and ended.
type timedSource struct {
	durations []time.Duration

	mu          sync.Mutex
	starts      []time.Time
	ends        []time.Time
	inFlight    int
	maxInFlight int
}

func (s *timedSource) Repos(ctx context.Context) ([]discovery.Repo, error) {
	s.mu.Lock()
	i := len(s.starts)
	s.starts = append(s.starts, time.Now())
	s.inFlight++
	s.maxInFlight = max(s.maxInFlight, s.inFlight)
	d := s.durations[min(i, len(s.durations)-1)]
	s.mu.Unlock()

	select {
	case <-time.After(d):
	case <-ctx.Done():
	}

	s.mu.Lock()
	s.ends = append(s.ends, time.Now())
	s.inFlight--
	s.mu.Unlock()
	return nil, nil
}

// runFor runs a Runner with the given interval over src for d of virtual
// time, then cancels it and waits for Run to return. It must be called inside
// a synctest bubble.
func runFor(t *testing.T, interval, d time.Duration, src collector.RepoSource, self *fakeSelfMetrics, logs *bytes.Buffer) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(logs, nil))
	r, err := collector.NewRunner(config.Target{Name: "tgt"}, src, collector.RunnerCollectors{},
		metric.NewMeterProvider().Meter("test"), logger,
		collector.WithInterval(interval), collector.WithSelfMetrics(self))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()
	time.Sleep(d)
	cancel()
	<-done
}

// offsets returns each time as its offset from t0.
func offsets(t0 time.Time, ts []time.Time) []time.Duration {
	out := make([]time.Duration, len(ts))
	for i, ts := range ts {
		out[i] = ts.Sub(t0)
	}
	return out
}

func TestRunner_StartsCyclesEveryInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		t0 := time.Now()
		src := &timedSource{durations: []time.Duration{time.Minute}}
		self := &fakeSelfMetrics{}
		var logs bytes.Buffer

		runFor(t, 5*time.Minute, 16*time.Minute, src, self, &logs)

		want := []time.Duration{0, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute}
		if got := offsets(t0, src.starts); !slices.Equal(got, want) {
			t.Errorf("cycle starts = %v, want %v", got, want)
		}
		if len(self.overruns) != 0 {
			t.Errorf("overruns = %v, want none", self.overruns)
		}
		if len(self.inits) != 1 || self.inits[0] != "tgt" {
			t.Errorf("overrun series initialised for %v, want once for tgt", self.inits)
		}
	})
}

func TestRunner_OverrunStartsNextCycleAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		t0 := time.Now()
		src := &timedSource{durations: []time.Duration{7 * time.Minute, time.Minute}}
		self := &fakeSelfMetrics{}
		var logs bytes.Buffer

		runFor(t, 5*time.Minute, 13*time.Minute, src, self, &logs)

		want := []time.Duration{0, 7 * time.Minute, 12 * time.Minute}
		if got := offsets(t0, src.starts); !slices.Equal(got, want) {
			t.Errorf("cycle starts = %v, want %v", got, want)
		}
		if src.maxInFlight != 1 {
			t.Errorf("max cycles in flight = %d, want 1", src.maxInFlight)
		}
		for i := 1; i < len(src.starts); i++ {
			if src.starts[i].Before(src.ends[i-1]) {
				t.Errorf("cycle %d started at %v before cycle %d ended at %v",
					i, src.starts[i].Sub(t0), i-1, src.ends[i-1].Sub(t0))
			}
		}
		if len(self.overruns) != 1 || self.overruns[0] != "tgt" {
			t.Errorf("overruns = %v, want one for tgt", self.overruns)
		}
		if !strings.Contains(logs.String(), "collection cycle overran collection_interval") {
			t.Errorf("no overrun warning logged:\n%s", logs.String())
		}
	})
}

func TestRunner_NoCatchUpAfterOverrun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		t0 := time.Now()
		// The first cycle spans more than two periods; the missed ones are
		// not run back to back afterwards.
		src := &timedSource{durations: []time.Duration{40 * time.Minute, time.Minute}}
		self := &fakeSelfMetrics{}
		var logs bytes.Buffer

		runFor(t, 15*time.Minute, 56*time.Minute, src, self, &logs)

		want := []time.Duration{0, 40 * time.Minute, 55 * time.Minute}
		if got := offsets(t0, src.starts); !slices.Equal(got, want) {
			t.Errorf("cycle starts = %v, want %v", got, want)
		}
		if len(self.overruns) != 1 {
			t.Errorf("overruns = %v, want 1", self.overruns)
		}
	})
}

func TestRunner_ShutdownDuringCycleIsNoOverrun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &timedSource{durations: []time.Duration{time.Hour}}
		self := &fakeSelfMetrics{}
		var logs bytes.Buffer

		runFor(t, 5*time.Minute, 10*time.Minute, src, self, &logs)

		if len(src.starts) != 1 {
			t.Errorf("cycles started = %d, want 1", len(src.starts))
		}
		if len(self.overruns) != 0 {
			t.Errorf("overruns = %v, want none on shutdown", self.overruns)
		}
	})
}
