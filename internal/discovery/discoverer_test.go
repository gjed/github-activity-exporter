package discovery_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	gogithub "github.com/google/go-github/v62/github"

	"github.com/gjed/renovate-exporter/internal/config"
	"github.com/gjed/renovate-exporter/internal/discovery"
)

// ---------------------------------------------------------------------------
// Mock helpers
// ---------------------------------------------------------------------------

// buildOrgServer creates an httptest.Server responding to /orgs/{org}/repos.
// It serves repos from the given map, keyed by org name.
func buildOrgServer(t *testing.T, reposByOrg map[string][]*gogithub.Repository) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// URL shape: /api/v3/orgs/{org}/repos
		org := extractOrg(r.URL.Path)
		repos := reposByOrg[org]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(repos)
	}))
}

// extractOrg parses the org name from paths like /orgs/{org}/repos
// or /api/v3/orgs/{org}/repos (enterprise URL format used by go-github).
func extractOrg(path string) string {
	// Find "orgs/" and take the next segment.
	const prefix = "orgs/"
	idx := 0
	for i := 0; i <= len(path)-len(prefix); i++ {
		if path[i:i+len(prefix)] == prefix {
			idx = i + len(prefix)
			break
		}
	}
	rest := path[idx:]
	for i, c := range rest {
		if c == '/' {
			return rest[:i]
		}
	}
	return rest
}

// httpRepoLister delegates to the real go-github REST client against a test server.
type httpRepoLister struct {
	client *gogithub.Client
}

func newHTTPLister(t *testing.T, serverURL string) *httpRepoLister {
	t.Helper()
	c, err := gogithub.NewClient(nil).WithEnterpriseURLs(serverURL+"/", serverURL+"/")
	if err != nil {
		t.Fatalf("creating test GitHub client: %v", err)
	}
	return &httpRepoLister{client: c}
}

func (l *httpRepoLister) ListByOrg(ctx context.Context, org string, opts *gogithub.RepositoryListByOrgOptions) ([]*gogithub.Repository, *gogithub.Response, error) {
	return l.client.Repositories.ListByOrg(ctx, org, opts)
}

// makeRepo creates a minimal *github.Repository for testing.
func makeRepo(fullName string, archived, fork bool) *gogithub.Repository {
	owner, name := splitOwnerName(fullName)
	return &gogithub.Repository{
		Name:     strPtr(name),
		FullName: strPtr(fullName),
		Owner:    &gogithub.User{Login: strPtr(owner)},
		Archived: boolPtr(archived),
		Fork:     boolPtr(fork),
	}
}

func splitOwnerName(full string) (string, string) {
	for i, c := range full {
		if c == '/' {
			return full[:i], full[i+1:]
		}
	}
	return "", full
}

func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }

// repoNames extracts sorted full names from a Repo slice.
func repoNames(repos []discovery.Repo) []string {
	names := make([]string, len(repos))
	for i, r := range repos {
		names[i] = r.FullName
	}
	sort.Strings(names)
	return names
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestDiscoverer_Autodiscovery_basic(t *testing.T) {
	srv := buildOrgServer(t, map[string][]*gogithub.Repository{
		"myorg": {
			makeRepo("myorg/alpha", false, false),
			makeRepo("myorg/beta", false, false),
			makeRepo("myorg/archived-repo", true, false),
			makeRepo("myorg/forked-repo", false, true),
		},
	})
	defer srv.Close()

	target := config.Target{
		Name: "myorg",
		Auth: config.Auth{Token: "tok"},
		Orgs: []config.OrgConfig{{Org: "myorg"}},
	}

	d := discovery.New(target, newHTTPLister(t, srv.URL))
	repos, err := d.Repos(context.Background())
	if err != nil {
		t.Fatalf("Repos(): %v", err)
	}

	got := repoNames(repos)
	want := []string{"myorg/alpha", "myorg/beta"}
	if !equalSlices(got, want) {
		t.Errorf("got repos %v, want %v", got, want)
	}
}

func TestDiscoverer_IncludeFilter(t *testing.T) {
	srv := buildOrgServer(t, map[string][]*gogithub.Repository{
		"myorg": {
			makeRepo("myorg/carbonio-files-ce", false, false),
			makeRepo("myorg/carbonio-auth", false, false),
			makeRepo("myorg/other-tool", false, false),
		},
	})
	defer srv.Close()

	target := config.Target{
		Name: "myorg",
		Auth: config.Auth{Token: "tok"},
		Orgs: []config.OrgConfig{{
			Org:          "myorg",
			IncludeRepos: []string{"carbonio-*"},
		}},
	}

	d := discovery.New(target, newHTTPLister(t, srv.URL))
	repos, err := d.Repos(context.Background())
	if err != nil {
		t.Fatalf("Repos(): %v", err)
	}

	got := repoNames(repos)
	want := []string{"myorg/carbonio-auth", "myorg/carbonio-files-ce"}
	if !equalSlices(got, want) {
		t.Errorf("got repos %v, want %v", got, want)
	}
}

func TestDiscoverer_ExcludeFilter(t *testing.T) {
	srv := buildOrgServer(t, map[string][]*gogithub.Repository{
		"myorg": {
			makeRepo("myorg/carbonio-files-ce", false, false),
			makeRepo("myorg/carbonio-i18n", false, false),
			makeRepo("myorg/carbonio-auth", false, false),
		},
	})
	defer srv.Close()

	target := config.Target{
		Name: "myorg",
		Auth: config.Auth{Token: "tok"},
		Orgs: []config.OrgConfig{{
			Org:          "myorg",
			ExcludeRepos: []string{"*-i18n"},
		}},
	}

	d := discovery.New(target, newHTTPLister(t, srv.URL))
	repos, err := d.Repos(context.Background())
	if err != nil {
		t.Fatalf("Repos(): %v", err)
	}

	got := repoNames(repos)
	want := []string{"myorg/carbonio-auth", "myorg/carbonio-files-ce"}
	if !equalSlices(got, want) {
		t.Errorf("got repos %v, want %v", got, want)
	}
}

func TestDiscoverer_IncludeAndExclude(t *testing.T) {
	srv := buildOrgServer(t, map[string][]*gogithub.Repository{
		"myorg": {
			makeRepo("myorg/carbonio-files-ce", false, false),
			makeRepo("myorg/carbonio-i18n", false, false),
			makeRepo("myorg/carbonio-auth", false, false),
			makeRepo("myorg/other-tool", false, false),
		},
	})
	defer srv.Close()

	target := config.Target{
		Name: "myorg",
		Auth: config.Auth{Token: "tok"},
		Orgs: []config.OrgConfig{{
			Org:          "myorg",
			IncludeRepos: []string{"carbonio-*"},
			ExcludeRepos: []string{"*-i18n"},
		}},
	}

	d := discovery.New(target, newHTTPLister(t, srv.URL))
	repos, err := d.Repos(context.Background())
	if err != nil {
		t.Fatalf("Repos(): %v", err)
	}

	got := repoNames(repos)
	want := []string{"myorg/carbonio-auth", "myorg/carbonio-files-ce"}
	if !equalSlices(got, want) {
		t.Errorf("got repos %v, want %v", got, want)
	}
}

func TestDiscoverer_ExplicitList(t *testing.T) {
	// No server needed; explicit repos bypass the API.
	target := config.Target{
		Name:  "myorg",
		Auth:  config.Auth{Token: "tok"},
		Repos: []string{"zextras/carbonio-files-ce", "zextras/carbonio-auth"},
	}

	// Lister is never called for explicit repos — pass nil.
	d := discovery.New(target, nil)
	repos, err := d.Repos(context.Background())
	if err != nil {
		t.Fatalf("Repos(): %v", err)
	}

	got := repoNames(repos)
	want := []string{"zextras/carbonio-auth", "zextras/carbonio-files-ce"}
	if !equalSlices(got, want) {
		t.Errorf("got repos %v, want %v", got, want)
	}
}

func TestDiscoverer_MultiOrg_Dedup(t *testing.T) {
	srv := buildOrgServer(t, map[string][]*gogithub.Repository{
		"orgA": {
			makeRepo("orgA/alpha", false, false),
			makeRepo("orgA/shared", false, false),
		},
		"orgB": {
			makeRepo("orgB/beta", false, false),
			makeRepo("orgA/shared", false, false), // same full name as orgA/shared — should dedup
		},
	})
	defer srv.Close()

	target := config.Target{
		Name: "multi",
		Auth: config.Auth{Token: "tok"},
		Orgs: []config.OrgConfig{
			{Org: "orgA"},
			{Org: "orgB"},
		},
	}

	d := discovery.New(target, newHTTPLister(t, srv.URL))
	repos, err := d.Repos(context.Background())
	if err != nil {
		t.Fatalf("Repos(): %v", err)
	}

	// orgA/shared appears twice across orgs but must be deduplicated.
	got := repoNames(repos)
	for _, name := range got {
		count := 0
		for _, n := range got {
			if n == name {
				count++
			}
		}
		if count > 1 {
			t.Errorf("duplicate repo %q in result", name)
		}
	}

	if len(repos) != 3 {
		t.Errorf("expected 3 unique repos, got %d: %v", len(repos), got)
	}
}

func TestDiscoverer_ArchivedOptIn(t *testing.T) {
	srv := buildOrgServer(t, map[string][]*gogithub.Repository{
		"myorg": {
			makeRepo("myorg/active", false, false),
			makeRepo("myorg/archived-repo", true, false),
		},
	})
	defer srv.Close()

	target := config.Target{
		Name: "myorg",
		Auth: config.Auth{Token: "tok"},
		Orgs: []config.OrgConfig{{
			Org:             "myorg",
			IncludeArchived: true,
		}},
	}

	d := discovery.New(target, newHTTPLister(t, srv.URL))
	repos, err := d.Repos(context.Background())
	if err != nil {
		t.Fatalf("Repos(): %v", err)
	}

	got := repoNames(repos)
	want := []string{"myorg/active", "myorg/archived-repo"}
	if !equalSlices(got, want) {
		t.Errorf("got repos %v, want %v", got, want)
	}
}

func TestDiscoverer_IncrementalRefresh(t *testing.T) {
	var mu sync.Mutex
	apiRepos := []*gogithub.Repository{makeRepo("myorg/alpha", false, false)}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		current := make([]*gogithub.Repository, len(apiRepos))
		copy(current, apiRepos)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(current)
	}))
	defer srv.Close()

	target := config.Target{
		Name: "myorg",
		Auth: config.Auth{Token: "tok"},
		Orgs: []config.OrgConfig{{Org: "myorg"}},
	}

	d := discovery.New(target, newHTTPLister(t, srv.URL),
		discovery.WithRefreshInterval(100*time.Millisecond),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go d.Start(ctx)

	// Poll until initial discovery produces exactly 1 repo (or deadline).
	initial := pollUntil(ctx, t, d, 1)
	if len(initial) != 1 {
		t.Fatalf("expected 1 repo initially, got %d", len(initial))
	}

	// Add a new repo to the "API".
	mu.Lock()
	apiRepos = append(apiRepos, makeRepo("myorg/beta", false, false))
	mu.Unlock()

	// Poll until the refresh produces 2 repos (or deadline).
	updated := pollUntil(ctx, t, d, 2)
	if len(updated) != 2 {
		t.Errorf("expected 2 repos after refresh, got %d: %v", len(updated), repoNames(updated))
	}
}

// pollUntil polls d.Repos until len(repos) == want or ctx expires.
func pollUntil(ctx context.Context, t *testing.T, d interface {
	Repos(context.Context) ([]discovery.Repo, error)
}, want int) []discovery.Repo {
	t.Helper()
	for {
		repos, err := d.Repos(ctx)
		if err != nil {
			if ctx.Err() != nil {
				t.Fatalf("context expired while polling for %d repos: %v", want, ctx.Err())
			}
			t.Logf("Repos() error (retrying): %v", err)
		}
		if len(repos) == want {
			return repos
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %d repos; last count: %d", want, len(repos))
		case <-time.After(10 * time.Millisecond):
		}
	}
}
