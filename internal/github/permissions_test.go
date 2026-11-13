package github_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	internalgithub "github.com/gjed/github-activity-exporter/internal/github"
)

// appServer answers the installation token exchange with grants, and fails
// the test on any other request: the App check sends nothing else.
func appServer(t *testing.T, permissions map[string]string, selection string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/2/access_tokens" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":                "ghs_x",
			"expires_at":           time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"permissions":          permissions,
			"repository_selection": selection,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func checkApp(t *testing.T, permissions map[string]string, p internalgithub.PermissionCheck) error {
	t.Helper()
	_, keyPEM := generateTestRSAKey(t)
	srv := appServer(t, permissions, "selected")
	auth, err := internalgithub.NewAppAuthenticator(internalgithub.AppAuthOptions{
		AppID: 1, InstallationID: 2, PrivateKeyPEM: keyPEM, BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("NewAppAuthenticator: %v", err)
	}
	c, err := internalgithub.NewClient(auth, internalgithub.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c.CheckPermissions(context.Background(), p)
}

func TestCheckPermissions_App(t *testing.T) {
	tests := []struct {
		name        string
		permissions map[string]string
		dashboard   bool
		want        []string // substrings of the error; nil means no error
	}{
		{
			name:        "minimum",
			permissions: map[string]string{"metadata": "read", "pull_requests": "read", "issues": "read"},
		},
		{
			name:        "write satisfies read",
			permissions: map[string]string{"metadata": "read", "pull_requests": "write", "issues": "write"},
			dashboard:   true,
		},
		{
			name:        "missing issues",
			permissions: map[string]string{"metadata": "read", "pull_requests": "read"},
			dashboard:   true,
			want:        []string{"issues: read (issue collector, Dependency Dashboard collector)", "GitHub App installation"},
		},
		{
			name:        "missing several",
			permissions: map[string]string{"metadata": "read"},
			want:        []string{"pull_requests: read (PR collector)", "issues: read (issue collector)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkApp(t, tt.permissions, internalgithub.PermissionCheck{Owners: []string{"acme"}, Dashboard: tt.dashboard})
			assertMissing(t, err, tt.want)
		})
	}
}

func assertMissing(t *testing.T, err error, want []string) {
	t.Helper()
	if want == nil {
		if err != nil {
			t.Fatalf("CheckPermissions: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("CheckPermissions: nil error, want %q", want)
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not contain %q", err, w)
		}
	}
}

// patServer answers GET /user with the given X-OAuth-Scopes header (none
// when scopes is nil) and GraphQL probes with graphql(owner).
func patServer(t *testing.T, scopes *string, graphql func(owner string) string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var probes atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user":
			if scopes != nil {
				w.Header().Set("X-OAuth-Scopes", *scopes)
			}
			_, _ = w.Write([]byte(`{"login":"me"}`))
		case "/graphql":
			probes.Add(1)
			b, _ := io.ReadAll(r.Body)
			var req struct {
				Query     string
				Variables map[string]string
			}
			_ = json.Unmarshal(b, &req)
			if !strings.Contains(req.Query, "privacy: PRIVATE") {
				t.Errorf("probe query does not select private repos: %s", req.Query)
			}
			_, _ = w.Write([]byte(graphql(req.Variables["owner"])))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &probes
}

func TestCheckPermissions_ClassicScopes(t *testing.T) {
	tests := []struct {
		scopes string
		want   []string
	}{
		{scopes: "repo, read:org"},
		{scopes: "gist, read:org, repo, workflow"},
		{scopes: "read:org, public_repo", want: []string{"missing OAuth scope: repo", "granted: read:org, public_repo"}},
		{scopes: "", want: []string{"missing OAuth scope: repo"}},
	}
	for _, tt := range tests {
		t.Run(tt.scopes, func(t *testing.T) {
			scopes := tt.scopes
			srv, probes := patServer(t, &scopes, func(string) string { return `{}` })
			c := newPoolClient(t, srv.URL)
			err := c.CheckPermissions(context.Background(), internalgithub.PermissionCheck{Owners: []string{"acme"}})
			assertMissing(t, err, tt.want)
			if probes.Load() != 0 {
				t.Errorf("classic token sent %d GraphQL probes, want 0", probes.Load())
			}
		})
	}
}

const forbidden = `{"type":"FORBIDDEN","path":["repositoryOwner","repositories","nodes",0,"%s"],"message":"Resource not accessible by personal access token"}`

func TestCheckPermissions_FineGrained(t *testing.T) {
	private := `{"repositoryOwner":{"repositories":{"nodes":[{"nameWithOwner":"acme/secret"}]}}}`
	tests := []struct {
		name    string
		respond func(owner string) string
		want    []string
	}{
		{
			name:    "granted",
			respond: func(string) string { return `{"data":` + private + `}` },
		},
		{
			name: "no private repo",
			respond: func(string) string {
				return `{"data":{"repositoryOwner":{"repositories":{"nodes":[]}}}}`
			},
		},
		{
			name: "missing pull requests",
			respond: func(string) string {
				return `{"data":` + private + `,"errors":[` + strings.ReplaceAll(forbidden, "%s", "pulls") + `]}`
			},
			want: []string{"pull_requests: read (PR collector)", "probed on acme/secret"},
		},
		{
			name: "missing both, across owners",
			respond: func(owner string) string {
				alias := map[string]string{"acme": "pulls", "other": "issues"}[owner]
				return `{"data":` + strings.ReplaceAll(private, "acme", owner) + `,"errors":[` + strings.ReplaceAll(forbidden, "%s", alias) + `]}`
			},
			want: []string{"pull_requests: read (PR collector)", "issues: read (issue collector)", "acme/secret, other/secret"},
		},
		{
			name: "other error",
			respond: func(string) string {
				return `{"errors":[{"type":"INTERNAL","path":["repositoryOwner"],"message":"boom"}]}`
			},
			want: []string{"boom"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, probes := patServer(t, nil, tt.respond)
			c := newPoolClient(t, srv.URL)
			err := c.CheckPermissions(context.Background(), internalgithub.PermissionCheck{Owners: []string{"acme", "other"}})
			assertMissing(t, err, tt.want)
			if probes.Load() == 0 {
				t.Error("no GraphQL probe sent")
			}
		})
	}
}

// The probe query reads the dashboard field only when the collector is on.
func TestCheckPermissions_FineGrainedDashboardField(t *testing.T) {
	for _, dashboard := range []bool{false, true} {
		var query string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/graphql" {
				b, _ := io.ReadAll(r.Body)
				query = string(b)
			}
			_, _ = w.Write([]byte(`{"data":{}}`))
		}))
		c := newPoolClient(t, srv.URL)
		if err := c.CheckPermissions(context.Background(), internalgithub.PermissionCheck{Owners: []string{"acme"}, Dashboard: dashboard}); err != nil {
			t.Fatalf("dashboard=%v: %v", dashboard, err)
		}
		srv.Close()
		if got := strings.Contains(query, "dashboard:"); got != dashboard {
			t.Errorf("dashboard=%v: query has dashboard field = %v: %s", dashboard, got, query)
		}
	}
}

// A probe answered with a secondary rate limit waits for the target pause
// and is retried, like any request of the client.
func TestCheckPermissions_WaitsOnSecondaryRateLimit(t *testing.T) {
	srv, hits := scriptedServer(t, func(n int, r *http.Request, w http.ResponseWriter) bool {
		if n == 0 {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"You have exceeded a secondary rate limit"}`))
			return true
		}
		w.Header().Set("X-OAuth-Scopes", "repo")
		return false
	})
	c, s, _ := newSleepClient(t, srv)
	if err := c.CheckPermissions(context.Background(), internalgithub.PermissionCheck{Owners: []string{"acme"}}); err != nil {
		t.Fatalf("CheckPermissions: %v", err)
	}
	if hits.Load() != 2 {
		t.Errorf("requests = %d, want 2", hits.Load())
	}
	if w := s.all(); len(w) != 1 || !about(w[0], 30*time.Second) {
		t.Errorf("waits = %v, want one of about 30s", w)
	}
}
