package github_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v93/github"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	internalgithub "github.com/gjed/github-activity-exporter/internal/github"
)

// requestPoint is one http.client.request.duration data point.
type requestPoint struct {
	attrs map[string]string
	max   float64
	count uint64
}

// httpPoints collects the http.client.request.duration data points of reader.
func httpPoints(t *testing.T, reader *sdkmetric.ManualReader) []requestPoint {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var points []requestPoint
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "http.client.request.duration" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Histogram[float64]).DataPoints {
				p := requestPoint{attrs: map[string]string{}, count: dp.Count}
				if v, ok := dp.Max.Value(); ok {
					p.max = v
				}
				for _, kv := range dp.Attributes.ToSlice() {
					p.attrs[string(kv.Key)] = kv.Value.String()
				}
				points = append(points, p)
			}
		}
	}
	return points
}

// templates returns the url.template of every data point, "" when absent.
func templates(points []requestPoint) []string {
	var out []string
	for _, p := range points {
		out = append(out, p.attrs["url.template"])
	}
	return out
}

func newInstrumented(t *testing.T, baseURL string) (http.RoundTripper, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	rt, err := internalgithub.NewTransport(baseURL, mp)
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	return rt, reader
}

func TestNewTransport_NilProviderIsDefault(t *testing.T) {
	rt, err := internalgithub.NewTransport("https://api.github.com", nil)
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	if rt != http.DefaultTransport {
		t.Errorf("NewTransport(nil) = %T, want http.DefaultTransport", rt)
	}
}

// fakeGitHub answers every route the exporter calls under prefix (the REST
// API root path) and gqlPath.
func fakeGitHub(t *testing.T, prefix, gqlPath string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == gqlPath:
			_, _ = w.Write([]byte(`{"data":{"viewer":{"login":"octocat"}}}`))
		case strings.HasSuffix(r.URL.Path, "/access_tokens"):
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"token":"ghs_x","expires_at":%q}`, time.Now().Add(time.Hour).Format(time.RFC3339))
		case strings.HasSuffix(r.URL.Path, "/issues"), strings.HasSuffix(r.URL.Path, "/repos"):
			_, _ = w.Write([]byte(`[]`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestNewTransport_URLTemplate sends every kind of GitHub request the exporter
// makes, on github.com and GHES API roots, and checks url.template and that no
// attribute carries the org, repo or installation.
func TestNewTransport_URLTemplate(t *testing.T) {
	for name, tc := range map[string]struct{ prefix, gqlPath string }{
		"github.com": {prefix: "", gqlPath: "/graphql"},
		"GHES":       {prefix: "/api/v3", gqlPath: "/api/graphql"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := fakeGitHub(t, tc.prefix, tc.gqlPath)
			base := srv.URL + tc.prefix
			rt, reader := newInstrumented(t, base)
			hc := &http.Client{Transport: rt}
			ctx := context.Background()

			_, keyPEM := generateTestRSAKey(t)
			app, err := internalgithub.NewAppAuthenticator(internalgithub.AppAuthOptions{
				AppID: 1, InstallationID: 4242, PrivateKeyPEM: keyPEM, BaseURL: base, HTTPClient: hc,
			})
			if err != nil {
				t.Fatalf("NewAppAuthenticator: %v", err)
			}
			if err := app.Ping(ctx); err != nil {
				t.Fatalf("App Ping: %v", err)
			}
			if err := internalgithub.NewPATAuthenticatorWithBase("ghp_x", base, hc).Ping(ctx); err != nil {
				t.Fatalf("PAT Ping: %v", err)
			}
			c, err := internalgithub.NewClient(app, internalgithub.WithBaseURL(base), internalgithub.WithBaseTransport(rt))
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			if _, _, err := c.REST().Issues.ListByRepo(ctx, "acme", "widgets", &github.IssueListByRepoOptions{}); err != nil {
				t.Fatalf("ListByRepo: %v", err)
			}
			if _, _, err := c.REST().Repositories.ListByOrg(ctx, "acme", nil); err != nil {
				t.Fatalf("ListByOrg: %v", err)
			}
			var q struct{ Viewer struct{ Login string } }
			if err := c.GraphQL().Query(ctx, &q, nil); err != nil {
				t.Fatalf("GraphQL: %v", err)
			}
			if _, _, err := c.REST().Repositories.Get(ctx, "acme", "widgets"); err != nil {
				t.Fatalf("Get: %v", err)
			}

			points := httpPoints(t, reader)
			want := map[string]bool{
				tc.prefix + "/app":  true,
				tc.prefix + "/user": true,
				tc.prefix + "/app/installations/{installation_id}/access_tokens": true,
				tc.prefix + "/repos/{owner}/{repo}/issues":                       true,
				tc.prefix + "/orgs/{org}/repos":                                  true,
				tc.gqlPath:                                                       true,
				"":                                                               true, // GET /repos/acme/widgets is not a known route
			}
			got := map[string]bool{}
			for _, tmpl := range templates(points) {
				got[tmpl] = true
			}
			for tmpl := range want {
				if !got[tmpl] {
					t.Errorf("no data point with url.template %q (got %v)", tmpl, templates(points))
				}
			}
			for tmpl := range got {
				if !want[tmpl] {
					t.Errorf("unexpected url.template %q", tmpl)
				}
			}
			for _, p := range points {
				for k, v := range p.attrs {
					if strings.Contains(v, "acme") || strings.Contains(v, "widgets") || strings.Contains(v, "4242") {
						t.Errorf("attribute %s=%q carries an identifier", k, v)
					}
				}
				for _, k := range []string{"http.request.method", "http.response.status_code", "server.address", "server.port"} {
					if p.attrs[k] == "" {
						t.Errorf("data point %v lacks %s", p.attrs, k)
					}
				}
				if p.attrs["url.template"] == tc.prefix+"/app/installations/{installation_id}/access_tokens" && p.attrs["http.request.method"] != http.MethodPost {
					t.Errorf("token exchange method = %q, want POST", p.attrs["http.request.method"])
				}
			}
		})
	}
}

// TestNewTransport_ErrorStatus checks that 401 still reaches the auth result
// hook and that 4xx/5xx responses carry error.type, a transport error with no
// status included.
func TestNewTransport_ErrorStatus(t *testing.T) {
	var mu sync.Mutex
	status := http.StatusUnauthorized
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	rt, reader := newInstrumented(t, srv.URL)
	res := &authResults{}
	c, err := internalgithub.NewClient(&mockAuthenticator{token: "tok"},
		internalgithub.WithBaseURL(srv.URL),
		internalgithub.WithBaseTransport(rt),
		internalgithub.WithAuthResultHook(res.hook),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx := context.Background()
	opts := &github.IssueListByRepoOptions{}
	_, _, _ = c.REST().Issues.ListByRepo(ctx, "o", "r", opts)
	mu.Lock()
	status = http.StatusBadGateway
	mu.Unlock()
	_, _, _ = c.REST().Issues.ListByRepo(ctx, "o", "r", opts)
	mu.Lock()
	status = http.StatusOK
	mu.Unlock()
	_, _, _ = c.REST().Issues.ListByRepo(ctx, "o", "r", opts)

	if got := res.values(); len(got) != 3 || !got[0] || got[1] || got[2] {
		t.Errorf("auth hook calls = %v, want [true false false]", got)
	}

	byStatus := map[string]string{}
	for _, p := range httpPoints(t, reader) {
		byStatus[p.attrs["http.response.status_code"]] = p.attrs["error.type"]
	}
	for statusCode, errType := range map[string]string{"401": "401", "502": "502", "200": ""} {
		got, ok := byStatus[statusCode]
		if !ok {
			t.Errorf("no data point with status %s (got %v)", statusCode, byStatus)
			continue
		}
		if got != errType {
			t.Errorf("status %s: error.type = %q, want %q", statusCode, got, errType)
		}
	}

	// A request that fails without a response.
	srv.Close()
	_, _, _ = c.REST().Issues.ListByRepo(ctx, "o", "r", opts)
	failed := 0
	for _, p := range httpPoints(t, reader) {
		if _, hasStatus := p.attrs["http.response.status_code"]; !hasStatus {
			failed++
			if p.attrs["error.type"] == "" {
				t.Errorf("failed request recorded without error.type: %v", p.attrs)
			}
		}
	}
	if failed == 0 {
		t.Error("request that failed without a response was not recorded")
	}
}

// TestNewTransport_RateLimitPauseUnchanged checks that the instrumented
// transport keeps the pool pause and the rate limit hook, and that the paused
// request's recorded duration excludes the wait.
func TestNewTransport_RateLimitPauseUnchanged(t *testing.T) {
	// X-RateLimit-Reset has second resolution: the pause lasts 2 to 3s.
	reset := time.Now().Add(3 * time.Second)
	var mu sync.Mutex
	remaining := 10
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		w.Header().Set("X-RateLimit-Resource", "core")
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", reset.Unix()))
		remaining = 4999
		mu.Unlock()
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	rt, reader := newInstrumented(t, srv.URL)
	var states []internalgithub.RateLimitState
	c, err := internalgithub.NewClient(&mockAuthenticator{token: "tok"},
		internalgithub.WithBaseURL(srv.URL),
		internalgithub.WithBaseTransport(rt),
		internalgithub.WithRateLimitHook(func(s internalgithub.RateLimitState) { states = append(states, s) }),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx := context.Background()
	opts := &github.IssueListByRepoOptions{}
	if _, _, err := c.REST().Issues.ListByRepo(ctx, "o", "r", opts); err != nil {
		t.Fatalf("first request: %v", err)
	}
	start := time.Now()
	if _, _, err := c.REST().Issues.ListByRepo(ctx, "o", "r", opts); err != nil {
		t.Fatalf("second request: %v", err)
	}
	waited := time.Since(start)
	if waited < time.Second {
		t.Errorf("second request took %v, want it to wait for the reset", waited)
	}
	if len(states) != 2 || states[0].Remaining != 10 || states[1].Remaining != 4999 || states[0].Resource != "core" {
		t.Errorf("rate limit hook states = %+v, want core 10 then 4999", states)
	}

	points := httpPoints(t, reader)
	var count uint64
	for _, p := range points {
		count += p.count
		if p.max >= waited.Seconds()/2 {
			t.Errorf("recorded duration %.3fs includes the %v pause", p.max, waited)
		}
	}
	if count != 2 {
		t.Errorf("recorded %d requests, want 2", count)
	}
}
