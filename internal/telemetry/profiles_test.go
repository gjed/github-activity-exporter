package telemetry_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/telemetry"
)

// pushed is one profile received by pprofReceiver.
type pushed struct {
	path, contentType, contentEncoding string
	// proto is the gunzipped body.
	proto []byte
}

// pprofReceiver stands in for the Collector pprof receiver in server mode.
type pprofReceiver struct {
	*httptest.Server
	status int
	mu     sync.Mutex
	got    []pushed
}

func newPprofReceiver(t *testing.T, status int) *pprofReceiver {
	t.Helper()
	r := &pprofReceiver{status: status}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		p := pushed{path: req.URL.Path, contentType: req.Header.Get("Content-Type"), contentEncoding: req.Header.Get("Content-Encoding")}
		if zr, err := gzip.NewReader(req.Body); err == nil {
			p.proto, _ = io.ReadAll(zr)
		}
		r.mu.Lock()
		r.got = append(r.got, p)
		r.mu.Unlock()
		w.WriteHeader(r.status)
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *pprofReceiver) pushes() []pushed {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]pushed(nil), r.got...)
}

func profilesOn(endpoint string, interval time.Duration) config.Telemetry {
	cfg := toggles(true, false, false, false, true)
	cfg.Profiles.Endpoint = endpoint
	cfg.Profiles.Interval = interval
	return cfg
}

// checkPushes checks that every push is a gzip profile.proto on /v1/pprof and
// that the i-th push contains marker[i], a string of its sample types.
func checkPushes(t *testing.T, got []pushed, markers ...string) {
	t.Helper()
	if len(got) != len(markers) {
		t.Fatalf("got %d pushes, want %d", len(got), len(markers))
	}
	for i, p := range got {
		if p.path != "/v1/pprof" || p.contentType != "application/octet-stream" || p.contentEncoding != "gzip" {
			t.Errorf("push %d: path %q, Content-Type %q, Content-Encoding %q", i, p.path, p.contentType, p.contentEncoding)
		}
		if !bytes.Contains(p.proto, []byte(markers[i])) {
			t.Errorf("push %d: profile without %q", i, markers[i])
		}
	}
}

func TestProfiles_PushedOnShutdown(t *testing.T) {
	clearEnv(t, nil)
	rcv := newPprofReceiver(t, http.StatusNoContent)
	tel := newTelemetry(t, profilesOn(rcv.URL, time.Hour), config.OTLP{}, nil, nil)
	if !tel.Active(telemetry.SignalProfiles) {
		t.Fatal("profiles inactive")
	}
	if got := runtime.SetMutexProfileFraction(-1); got != 0 {
		t.Errorf("mutex fraction = %d, want 0", got)
	}
	if err := tel.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	// One partial window: CPU, heap, goroutine; no mutex or block.
	checkPushes(t, rcv.pushes(), "cpu", "inuse_space", "goroutine")
}

func TestProfiles_PushedEveryInterval(t *testing.T) {
	clearEnv(t, nil)
	rcv := newPprofReceiver(t, http.StatusNoContent)
	newTelemetry(t, profilesOn(rcv.URL, 50*time.Millisecond), config.OTLP{}, nil, nil)
	deadline := time.Now().Add(5 * time.Second)
	for len(rcv.pushes()) < 6 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got := rcv.pushes()
	if len(got) < 6 {
		t.Fatalf("got %d pushes in 5s, want two windows", len(got))
	}
	checkPushes(t, got[:6], "cpu", "inuse_space", "goroutine", "cpu", "inuse_space", "goroutine")
}

func TestProfiles_MutexAndBlock(t *testing.T) {
	clearEnv(t, nil)
	rcv := newPprofReceiver(t, http.StatusNoContent)
	cfg := profilesOn(rcv.URL, time.Hour)
	cfg.Profiles.MutexFraction = 100
	cfg.Profiles.BlockRate = 100000
	tel := newTelemetry(t, cfg, config.OTLP{}, nil, nil)
	if got := runtime.SetMutexProfileFraction(-1); got != 100 {
		t.Errorf("mutex fraction while active = %d, want 100", got)
	}
	if err := tel.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	checkPushes(t, rcv.pushes(), "cpu", "inuse_space", "goroutine", "contentions", "contentions")
	if got := runtime.SetMutexProfileFraction(-1); got != 0 {
		t.Errorf("mutex fraction after shutdown = %d, want 0", got)
	}
}

func TestProfiles_RatesIgnoredWhenInactive(t *testing.T) {
	clearEnv(t, nil)
	cfg := profilesOn("http://127.0.0.1:1", time.Hour)
	cfg.Enabled = false
	cfg.Profiles.MutexFraction = 100
	newTelemetry(t, cfg, config.OTLP{}, nil, nil)
	if got := runtime.SetMutexProfileFraction(-1); got != 0 {
		t.Errorf("mutex fraction = %d, want 0", got)
	}
}

func TestProfiles_InactiveSendsNothing(t *testing.T) {
	rcv := newPprofReceiver(t, http.StatusNoContent)
	off := profilesOn(rcv.URL, 10*time.Millisecond)
	off.Profiles.Enabled = false
	global := profilesOn(rcv.URL, 10*time.Millisecond)
	global.Enabled = false
	for _, tc := range []struct {
		name string
		cfg  config.Telemetry
		env  map[string]string
	}{
		{"profiles disabled", off, nil},
		{"telemetry disabled", global, nil},
		{"sdk disabled", profilesOn(rcv.URL, 10*time.Millisecond), map[string]string{"OTEL_SDK_DISABLED": "true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t, tc.env)
			tel := newTelemetry(t, tc.cfg, config.OTLP{}, nil, nil)
			time.Sleep(50 * time.Millisecond)
			if err := tel.Shutdown(context.Background()); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
			if tel.Active(telemetry.SignalProfiles) {
				t.Error("profiles active")
			}
		})
	}
	if n := len(rcv.pushes()); n != 0 {
		t.Errorf("inactive profiles sent %d requests", n)
	}
}

func TestProfiles_InvalidSettingsAreLogged(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		edit      func(*config.Profiles)
	}{
		{"missing endpoint", "telemetry.profiles.endpoint", func(p *config.Profiles) { p.Endpoint = "" }},
		{"non-http endpoint", "telemetry.profiles.endpoint", func(p *config.Profiles) { p.Endpoint = "otel-collector:4040" }},
		{"zero interval", "telemetry.profiles.interval", func(p *config.Profiles) { p.Interval = 0 }},
		{"negative mutex fraction", "telemetry.profiles.mutex_fraction", func(p *config.Profiles) { p.MutexFraction = -1 }},
		{"negative block rate", "telemetry.profiles.block_rate", func(p *config.Profiles) { p.BlockRate = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t, nil)
			rcv := newPprofReceiver(t, http.StatusNoContent)
			cfg := profilesOn(rcv.URL, 10*time.Millisecond)
			tc.edit(&cfg.Profiles)
			var buf bytes.Buffer
			tel := newTelemetry(t, cfg, config.OTLP{}, nil, slog.New(slog.NewTextHandler(&buf, nil)))
			if tel.Active(telemetry.SignalProfiles) {
				t.Error("profiles active with an invalid setting")
			}
			if !strings.Contains(buf.String(), "level=ERROR") || !strings.Contains(buf.String(), "key="+tc.key) {
				t.Errorf("no error naming %s: %q", tc.key, buf.String())
			}
			time.Sleep(50 * time.Millisecond)
			if n := len(rcv.pushes()); n != 0 {
				t.Errorf("sent %d requests", n)
			}
		})
	}
}

func TestProfiles_PushErrorsAreLogged(t *testing.T) {
	clearEnv(t, nil)
	rcv := newPprofReceiver(t, http.StatusInternalServerError)
	var buf bytes.Buffer
	tel := newTelemetry(t, profilesOn(rcv.URL, time.Hour), config.OTLP{}, nil, slog.New(slog.NewTextHandler(&buf, nil)))
	if err := tel.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	for _, want := range []string{"level=ERROR", "profile=cpu", "status=\"500 Internal Server Error\""} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("log lacks %q: %q", want, buf.String())
		}
	}
}
