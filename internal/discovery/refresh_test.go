package discovery_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	gogithub "github.com/google/go-github/v93/github"
	"go.opentelemetry.io/otel/sdk/metric"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
)

// fakeOrgAPI serves GET /orgs/myorg/repos from a mutable repo list, or 500
// while failing is set. It runs in-process: a loopback server would keep
// goroutines blocked on network I/O, so synctest time would never advance.
type fakeOrgAPI struct {
	mu       sync.Mutex
	repos    []string
	failing  bool
	listings int
}

func (f *fakeOrgAPI) set(repos ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repos = repos
}

func (f *fakeOrgAPI) fail(failing bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing = failing
}

func (f *fakeOrgAPI) listCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listings
}

func (f *fakeOrgAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if serveOrgType(w, r) {
		return
	}
	if r.URL.Path != "/api/v3/orgs/myorg/repos" {
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	f.listings++
	failing := f.failing
	repos := make([]*gogithub.Repository, len(f.repos))
	for i, name := range f.repos {
		repos[i] = makeRepo(name, false, false)
	}
	f.mu.Unlock()
	if failing {
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(repos)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// newInProcessClient returns a go-github client whose requests are served by
// h in the calling goroutine.
func newInProcessClient(t *testing.T, h http.Handler) *gogithub.Client {
	t.Helper()
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Result(), nil
	})
	c, err := gogithub.NewClient(
		gogithub.WithTransport(transport),
		gogithub.WithEnterpriseURLs("http://github.test/", "http://github.test/"),
	)
	if err != nil {
		t.Fatalf("creating test GitHub client: %v", err)
	}
	return c
}

var myorgTarget = config.Target{
	Name: "myorg",
	Auth: config.Auth{Token: "tok"},
	Orgs: []config.OrgConfig{{Org: "myorg"}},
}

func TestStart_DoesNotDiscoverAtStartup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := &fakeOrgAPI{}
		api.set("myorg/a")
		d := discovery.New(myorgTarget, newInProcessClient(t, api),
			discovery.WithRefreshInterval(10*time.Minute))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go d.Start(ctx)
		synctest.Wait()
		if n := api.listCount(); n != 0 {
			t.Fatalf("Start listed repos %d times before the first tick, want 0", n)
		}

		if _, err := d.Repos(ctx); err != nil {
			t.Fatalf("Repos: %v", err)
		}
		if n := api.listCount(); n != 1 {
			t.Errorf("repo listings after first Repos = %d, want 1", n)
		}
	})
}

func TestStart_RefreshAddsAndDropsRepos(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := &fakeOrgAPI{}
		api.set("myorg/a")
		d := discovery.New(myorgTarget, newInProcessClient(t, api),
			discovery.WithRefreshInterval(10*time.Minute))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if _, err := d.Repos(ctx); err != nil {
			t.Fatalf("initial Repos: %v", err)
		}
		go d.Start(ctx)

		api.set("myorg/b")
		time.Sleep(10*time.Minute - time.Second)
		synctest.Wait()
		repos, _ := d.Repos(ctx)
		if got := repoNames(repos); !equalSlices(got, []string{"myorg/a"}) {
			t.Fatalf("repos before the refresh interval = %v, want [myorg/a]", got)
		}

		time.Sleep(time.Second)
		synctest.Wait()
		repos, _ = d.Repos(ctx)
		if got := repoNames(repos); !equalSlices(got, []string{"myorg/b"}) {
			t.Errorf("repos after the refresh interval = %v, want [myorg/b]", got)
		}
	})
}

func TestStart_FailedRefreshKeepsLastList(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := &fakeOrgAPI{}
		api.set("myorg/a")
		var logs bytes.Buffer
		failures := 0
		d := discovery.New(myorgTarget, newInProcessClient(t, api),
			discovery.WithRefreshInterval(10*time.Minute),
			discovery.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))),
			discovery.WithRefreshFailed(func() { failures++ }),
		)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if _, err := d.Repos(ctx); err != nil {
			t.Fatalf("initial Repos: %v", err)
		}
		go d.Start(ctx)

		api.set("myorg/b")
		api.fail(true)
		time.Sleep(10 * time.Minute)
		synctest.Wait()

		repos, err := d.Repos(ctx)
		if err != nil {
			t.Fatalf("Repos after a failed refresh: %v", err)
		}
		if got := repoNames(repos); !equalSlices(got, []string{"myorg/a"}) {
			t.Errorf("repos after a failed refresh = %v, want [myorg/a]", got)
		}
		if failures != 1 {
			t.Errorf("refresh failures reported = %d, want 1", failures)
		}
		if !strings.Contains(logs.String(), "repo refresh failed") || !strings.Contains(logs.String(), "target=myorg") {
			t.Errorf("failed refresh not logged with the target:\n%s", logs.String())
		}

		// The next successful refresh replaces the list.
		api.fail(false)
		time.Sleep(10 * time.Minute)
		synctest.Wait()
		repos, _ = d.Repos(ctx)
		if got := repoNames(repos); !equalSlices(got, []string{"myorg/b"}) {
			t.Errorf("repos after recovery = %v, want [myorg/b]", got)
		}
	})
}

// cycleRecorder wraps a RepoSource and records the repos each collection
// cycle got.
type cycleRecorder struct {
	src collector.RepoSource

	mu     sync.Mutex
	cycles [][]string
}

func (c *cycleRecorder) Repos(ctx context.Context) ([]discovery.Repo, error) {
	repos, err := c.src.Repos(ctx)
	c.mu.Lock()
	c.cycles = append(c.cycles, repoNames(repos))
	c.mu.Unlock()
	return repos, err
}

// TestRunner_CollectsReposFoundByRefresh runs a Runner on a Discoverer as
// main does: a repo created after startup is collected by the first cycle
// after the refresh interval, and a removed repo stops being collected.
func TestRunner_CollectsReposFoundByRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := &fakeOrgAPI{}
		api.set("myorg/a")
		d := discovery.New(myorgTarget, newInProcessClient(t, api),
			discovery.WithRefreshInterval(7*time.Minute))
		rec := &cycleRecorder{src: d}
		r, err := collector.NewRunner(myorgTarget, rec, collector.RunnerCollectors{},
			metric.NewMeterProvider().Meter("test"), nil, collector.WithInterval(5*time.Minute))
		if err != nil {
			t.Fatalf("NewRunner: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			r.Run(ctx)
			close(done)
		}()
		go d.Start(ctx)

		// Cycles at 0, 5, 10 and 15 minutes; refreshes at 7 and 14.
		time.Sleep(time.Minute)
		api.set("myorg/a", "myorg/b") // created after startup
		time.Sleep(7 * time.Minute)   // t = 8m
		api.set("myorg/b")            // myorg/a removed
		time.Sleep(8 * time.Minute)   // t = 16m
		cancel()
		<-done

		want := [][]string{
			{"myorg/a"},
			{"myorg/a"},
			{"myorg/a", "myorg/b"},
			{"myorg/b"},
		}
		if len(rec.cycles) != len(want) {
			t.Fatalf("cycles = %v, want %v", rec.cycles, want)
		}
		for i := range want {
			if !equalSlices(rec.cycles[i], want[i]) {
				t.Errorf("cycle %d collected %v, want %v", i, rec.cycles[i], want[i])
			}
		}
	})
}
