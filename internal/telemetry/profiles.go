package telemetry

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"time"

	"github.com/gjed/github-activity-exporter/internal/config"
)

// pprofPath is the push path of the OpenTelemetry Collector pprof receiver
// in server mode.
const pprofPath = "/v1/pprof"

// pushTimeout bounds one profile upload.
const pushTimeout = 30 * time.Second

// profilesErrors checks the profiles settings and returns one error per bad
// key. They are logged, never fatal: a broken profiles setting must not stop
// the exporter.
func profilesErrors(p config.Profiles) map[string]error {
	errs := map[string]error{}
	if u, err := url.Parse(p.Endpoint); p.Endpoint == "" {
		errs["telemetry.profiles.endpoint"] = fmt.Errorf("not set")
	} else if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		errs["telemetry.profiles.endpoint"] = fmt.Errorf("must be an http(s) URL without query or fragment, got %q", p.Endpoint)
	}
	if p.Interval <= 0 {
		errs["telemetry.profiles.interval"] = fmt.Errorf("must be a positive duration, got %s", p.Interval)
	}
	if p.MutexFraction < 0 {
		errs["telemetry.profiles.mutex_fraction"] = fmt.Errorf("must not be negative, got %d", p.MutexFraction)
	}
	if p.BlockRate < 0 {
		errs["telemetry.profiles.block_rate"] = fmt.Errorf("must not be negative, got %d", p.BlockRate)
	}
	return errs
}

// profiler pushes pprof profiles of the process to a Collector pprof
// receiver: every interval a CPU profile of that interval, then the heap,
// goroutine and, when their rates are set, mutex and block profiles.
type profiler struct {
	url      string
	interval time.Duration
	lookups  []string
	cfg      config.Profiles
	client   *http.Client
	logger   *slog.Logger

	// ctx bounds pushes; cancelled when the shutdown deadline expires.
	ctx    context.Context
	cancel context.CancelFunc
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
}

func startProfiler(p config.Profiles, logger *slog.Logger) *profiler {
	lookups := []string{"heap", "goroutine"}
	if p.MutexFraction > 0 {
		runtime.SetMutexProfileFraction(p.MutexFraction)
		lookups = append(lookups, "mutex")
	}
	if p.BlockRate > 0 {
		runtime.SetBlockProfileRate(p.BlockRate)
		lookups = append(lookups, "block")
	}
	ctx, cancel := context.WithCancel(context.Background())
	pr := &profiler{
		url:      strings.TrimRight(p.Endpoint, "/") + pprofPath,
		interval: p.Interval,
		lookups:  lookups,
		cfg:      p,
		client:   &http.Client{Timeout: pushTimeout},
		logger:   logger,
		ctx:      ctx,
		cancel:   cancel,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go pr.run()
	return pr
}

func (p *profiler) run() {
	defer close(p.done)
	timer := time.NewTimer(p.interval)
	defer timer.Stop()
	for {
		var cpu bytes.Buffer
		cpuOn := true
		if err := pprof.StartCPUProfile(&cpu); err != nil {
			p.logger.Error("profiles: cannot start the CPU profile", "err", err)
			cpuOn = false
		}
		stopped := false
		select {
		case <-timer.C:
			timer.Reset(p.interval)
		case <-p.stop:
			stopped = true
		}
		if cpuOn {
			pprof.StopCPUProfile()
			p.push("cpu", cpu.Bytes())
		}
		for _, name := range p.lookups {
			var buf bytes.Buffer
			// debug 0 writes the gzip-compressed protobuf format.
			if err := pprof.Lookup(name).WriteTo(&buf, 0); err != nil {
				p.logger.Error("profiles: cannot write the profile", "profile", name, "err", err)
				continue
			}
			p.push(name, buf.Bytes())
		}
		if stopped {
			return
		}
	}
}

// push posts one gzip-compressed profile.proto. Failures are logged and not
// retried: the next interval pushes fresh profiles.
func (p *profiler) push(name string, body []byte) {
	req, err := http.NewRequestWithContext(p.ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		p.logger.Error("profiles: push failed", "profile", name, "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "gzip")
	resp, err := p.client.Do(req)
	if err != nil {
		p.logger.Error("profiles: push failed", "profile", name, "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		p.logger.Error("profiles: push rejected", "profile", name, "status", resp.Status, "body", string(msg))
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
}

// shutdown ends the current CPU window, pushes it with the other profiles,
// and resets the runtime rates. Pushes still running when ctx expires are
// cancelled. Safe to call more than once.
func (p *profiler) shutdown(ctx context.Context) error {
	var err error
	p.once.Do(func() {
		close(p.stop)
		select {
		case <-p.done:
		case <-ctx.Done():
			p.cancel()
			<-p.done
			err = fmt.Errorf("profiles: final push: %w", ctx.Err())
		}
		p.cancel()
		if p.cfg.MutexFraction > 0 {
			runtime.SetMutexProfileFraction(0)
		}
		if p.cfg.BlockRate > 0 {
			runtime.SetBlockProfileRate(0)
		}
	})
	return err
}
