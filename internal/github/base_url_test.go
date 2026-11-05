package github_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/shurcooL/githubv4"

	internalgithub "github.com/gjed/github-activity-exporter/internal/github"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// guardLoopback makes http.DefaultTransport, used by both the client and the
// authenticators, fail any request that is not to a loopback httptest server.
// It must be installed before NewClient, which captures the transport.
func guardLoopback(t *testing.T) {
	t.Helper()
	orig := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if h := r.URL.Hostname(); h != "127.0.0.1" && h != "::1" && h != "localhost" {
			t.Errorf("request left the test servers: %s %s", r.Method, r.URL)
			return nil, fmt.Errorf("blocked non-loopback request to %s", r.URL)
		}
		return orig.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = orig })
}

// fakeGHES is a GHES-shaped API server that serves REST under /api/v3 and
// GraphQL at /api/graphql, and records every request it receives.
type fakeGHES struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string // "METHOD path auth"
}

func newFakeGHES(t *testing.T) *fakeGHES {
	t.Helper()
	f := &fakeGHES{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.seen = append(f.seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		f.mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v3/user", "GET /api/v3/app":
			w.WriteHeader(http.StatusOK)
		case "POST /api/v3/app/installations/2/access_tokens":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"token":      "ghs_installation",
				"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			})
		case "GET /api/v3/repos/o/r":
			_, _ = w.Write([]byte(`{"name":"r"}`))
		case "POST /api/graphql":
			_, _ = w.Write([]byte(`{"data":{"viewer":{"login":"bot"}}}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeGHES) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]string(nil), f.seen...)
	sort.Strings(out)
	return out
}

// exercise makes one REST and one GraphQL call through c.
func exercise(t *testing.T, c *internalgithub.Client) {
	t.Helper()
	ctx := context.Background()
	if _, _, err := c.REST().Repositories.Get(ctx, "o", "r"); err != nil {
		t.Errorf("REST call: %v", err)
	}
	var q struct {
		Viewer struct{ Login githubv4.String }
	}
	if err := c.GraphQL().Query(ctx, &q, nil); err != nil {
		t.Errorf("GraphQL call: %v", err)
	}
}

// TestBaseURL_AppTargetUsesOnlyItsHost checks that a GitHub App target with a
// GHES base URL sends the credential check, the installation token exchange,
// REST and GraphQL to that host and nowhere else.
func TestBaseURL_AppTargetUsesOnlyItsHost(t *testing.T) {
	guardLoopback(t)
	srv := newFakeGHES(t)
	base := srv.URL + "/api/v3"

	_, keyPEM := generateTestRSAKey(t)
	auth, err := internalgithub.NewAppAuthenticator(internalgithub.AppAuthOptions{
		AppID:          1,
		InstallationID: 2,
		PrivateKeyPEM:  keyPEM,
		BaseURL:        base,
	})
	if err != nil {
		t.Fatalf("NewAppAuthenticator: %v", err)
	}
	if err := auth.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	c, err := internalgithub.NewClient(auth, internalgithub.WithBaseURL(base))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	exercise(t, c)

	var paths []string
	for _, r := range srv.requests() {
		var method, path string
		_, _ = fmt.Sscanf(r, "%s %s", &method, &path)
		paths = append(paths, method+" "+path)
	}
	want := []string{
		"GET /api/v3/app",
		"GET /api/v3/repos/o/r",
		"POST /api/graphql",
		"POST /api/v3/app/installations/2/access_tokens",
	}
	if fmt.Sprint(paths) != fmt.Sprint(want) {
		t.Errorf("requests = %v, want %v", paths, want)
	}
}

// TestBaseURL_TargetsIsolated checks that two PAT targets with different base
// URLs each reach only their own server, with their own token.
func TestBaseURL_TargetsIsolated(t *testing.T) {
	guardLoopback(t)
	srvA, srvB := newFakeGHES(t), newFakeGHES(t)

	for _, tg := range []struct {
		srv   *fakeGHES
		token string
	}{{srvA, "ghp_a"}, {srvB, "ghp_b"}} {
		base := tg.srv.URL + "/api/v3"
		auth := internalgithub.NewPATAuthenticatorWithBase(tg.token, base)
		if err := auth.Ping(context.Background()); err != nil {
			t.Fatalf("Ping %s: %v", tg.token, err)
		}
		c, err := internalgithub.NewClient(auth, internalgithub.WithBaseURL(base))
		if err != nil {
			t.Fatalf("NewClient %s: %v", tg.token, err)
		}
		exercise(t, c)
	}

	for _, tg := range []struct {
		name string
		srv  *fakeGHES
		tok  string
	}{{"A", srvA, "ghp_a"}, {"B", srvB, "ghp_b"}} {
		want := []string{
			"GET /api/v3/repos/o/r Bearer " + tg.tok,
			"GET /api/v3/user Bearer " + tg.tok,
			"POST /api/graphql Bearer " + tg.tok,
		}
		if got := tg.srv.requests(); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("server %s requests = %q, want %q", tg.name, got, want)
		}
	}
}
