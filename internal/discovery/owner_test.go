package discovery_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gogithub "github.com/google/go-github/v93/github"

	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
)

// ownerServer is a GitHub REST mock covering every discovery endpoint. It
// records each request as "METHOD path" (without the /api/v3 prefix) and
// answers unknown paths 404.
type ownerServer struct {
	t     *testing.T
	types map[string]string // owner login -> "User" | "Organization"
	self  string            // GET /user login; empty answers 403 like an installation token

	orgRepos     map[string][]*gogithub.Repository
	userRepos    map[string][]*gogithub.Repository // public repos per user
	selfRepos    []*gogithub.Repository            // GET /user/repos?affiliation=owner
	installRepos []*gogithub.Repository

	mu   sync.Mutex
	reqs []string
}

func (s *ownerServer) start() *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	s.t.Cleanup(srv.Close)
	return srv
}

func (s *ownerServer) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v3")
	s.mu.Lock()
	s.reqs = append(s.reqs, r.Method+" "+path)
	s.mu.Unlock()

	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case path == "/user":
		if s.self == "" {
			http.Error(w, `{"message":"Resource not accessible by integration"}`, http.StatusForbidden)
			return
		}
		writeJSON(w, map[string]string{"login": s.self, "type": "User"})
	case path == "/user/repos":
		if r.URL.Query().Get("affiliation") != "owner" {
			s.t.Errorf("GET /user/repos without affiliation=owner: %s", r.URL.RawQuery)
		}
		writeJSON(w, s.selfRepos)
	case path == "/installation/repositories":
		writeJSON(w, map[string]any{"total_count": len(s.installRepos), "repositories": s.installRepos})
	case len(parts) == 2 && parts[0] == "users":
		typ, ok := s.types[parts[1]]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]string{"login": parts[1], "type": typ})
	case len(parts) == 3 && parts[0] == "users" && parts[2] == "repos":
		writeJSON(w, s.userRepos[parts[1]])
	case len(parts) == 3 && parts[0] == "orgs" && parts[2] == "repos":
		writeJSON(w, s.orgRepos[parts[1]])
	default:
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}
}

// count returns how many recorded requests equal req, or start with req when
// prefix is true.
func (s *ownerServer) count(req string, prefix bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.reqs {
		if r == req || (prefix && strings.HasPrefix(r, req)) {
			n++
		}
	}
	return n
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func discover(t *testing.T, srv *httptest.Server, target config.Target) ([]string, error) {
	t.Helper()
	repos, err := discovery.New(target, newTestClient(t, srv.URL)).Repos(context.Background())
	return repoNames(repos), err
}

func patTarget(owner string) config.Target {
	return config.Target{
		Name: "t1",
		Auth: config.Auth{Token: "tok"},
		Orgs: []config.OrgConfig{{Org: owner}},
	}
}

func TestOwner_Organization(t *testing.T) {
	s := &ownerServer{
		t:        t,
		types:    map[string]string{"acme": "Organization"},
		self:     "bot",
		orgRepos: map[string][]*gogithub.Repository{"acme": {makeRepo("acme/a", false, false)}},
	}
	srv := s.start()

	got, err := discover(t, srv, patTarget("acme"))
	if err != nil {
		t.Fatalf("Repos(): %v", err)
	}
	if want := []string{"acme/a"}; !equalSlices(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if n := s.count("GET /user", false) + s.count("GET /user/", true); n != 0 {
		t.Errorf("org owner requested GET /user* %d times, want 0", n)
	}
}

func TestOwner_UserSelfPAT_IncludesPrivate(t *testing.T) {
	private := makeRepo("gjed/secret", false, false)
	private.Private = boolPtr(true)
	s := &ownerServer{
		t:         t,
		types:     map[string]string{"gjed": "User"},
		self:      "GJed", // login comparison is case-insensitive
		selfRepos: []*gogithub.Repository{makeRepo("gjed/public", false, false), private},
		userRepos: map[string][]*gogithub.Repository{"gjed": {makeRepo("gjed/public", false, false)}},
	}
	srv := s.start()

	got, err := discover(t, srv, patTarget("gjed"))
	if err != nil {
		t.Fatalf("Repos(): %v", err)
	}
	if want := []string{"gjed/public", "gjed/secret"}; !equalSlices(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if n := s.count("GET /users/gjed/repos", false); n != 0 {
		t.Errorf("self PAT used the public listing %d times", n)
	}
}

func TestOwner_UserOtherPAT_PublicOnly(t *testing.T) {
	s := &ownerServer{
		t:         t,
		types:     map[string]string{"alice": "User"},
		self:      "bot",
		selfRepos: []*gogithub.Repository{makeRepo("bot/mine", false, false)},
		userRepos: map[string][]*gogithub.Repository{"alice": {makeRepo("alice/pub", false, false)}},
	}
	srv := s.start()

	got, err := discover(t, srv, patTarget("alice"))
	if err != nil {
		t.Fatalf("Repos(): %v", err)
	}
	if want := []string{"alice/pub"}; !equalSlices(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if n := s.count("GET /user/repos", false); n != 0 {
		t.Errorf("other user listing hit /user/repos %d times", n)
	}
}

func TestOwner_UserAppInstallation(t *testing.T) {
	s := &ownerServer{
		t:     t,
		types: map[string]string{"gjed": "User"},
		// self empty: installation tokens get 403 on GET /user.
		installRepos: []*gogithub.Repository{
			makeRepo("gjed/one", false, false),
			makeRepo("gjed/two", false, false),
			makeRepo("other/three", false, false), // different owner, dropped
		},
	}
	srv := s.start()

	target := config.Target{
		Name: "app",
		Auth: config.Auth{App: config.AppAuth{AppID: 1, InstallationID: 2}},
		Orgs: []config.OrgConfig{{Org: "gjed"}},
	}
	got, err := discover(t, srv, target)
	if err != nil {
		t.Fatalf("Repos(): %v", err)
	}
	if want := []string{"gjed/one", "gjed/two"}; !equalSlices(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if n := s.count("GET /user", false) + s.count("GET /user/", true); n != 0 {
		t.Errorf("App target requested GET /user* %d times, want 0", n)
	}
}

func TestOwner_UserFilters(t *testing.T) {
	s := &ownerServer{
		t:     t,
		types: map[string]string{"gjed": "User"},
		self:  "gjed",
		selfRepos: []*gogithub.Repository{
			makeRepo("gjed/app-api", false, false),
			makeRepo("gjed/app-i18n", false, false),
			makeRepo("gjed/app-old", true, false),
			makeRepo("gjed/app-fork", false, true),
			makeRepo("gjed/notes", false, false),
		},
	}
	srv := s.start()

	target := patTarget("gjed")
	target.Orgs[0].IncludeRepos = []string{"app-*"}
	target.Orgs[0].ExcludeRepos = []string{"*-i18n"}
	target.Orgs[0].IncludeArchived = true

	got, err := discover(t, srv, target)
	if err != nil {
		t.Fatalf("Repos(): %v", err)
	}
	if want := []string{"gjed/app-api", "gjed/app-old"}; !equalSlices(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestOwner_NotFound(t *testing.T) {
	s := &ownerServer{t: t, types: map[string]string{}, self: "bot"}
	srv := s.start()

	d := discovery.New(patTarget("ghost"), newTestClient(t, srv.URL))
	for i := range 2 {
		_, err := d.Repos(context.Background())
		if err == nil {
			t.Fatalf("attempt %d: expected error for unknown owner", i)
		}
		for _, want := range []string{`target "t1"`, `owner "ghost" not found`} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not contain %q", err, want)
			}
		}
	}
	// Not-found lookups are not cached: each refresh retries.
	if n := s.count("GET /users/ghost", false); n != 2 {
		t.Errorf("GET /users/ghost requested %d times, want 2", n)
	}
}

func TestOwner_TypeCachedAcrossRefreshes(t *testing.T) {
	s := &ownerServer{
		t:         t,
		types:     map[string]string{"gjed": "User", "acme": "Organization"},
		self:      "gjed",
		selfRepos: []*gogithub.Repository{makeRepo("gjed/a", false, false)},
		orgRepos:  map[string][]*gogithub.Repository{"acme": {makeRepo("acme/b", false, false)}},
	}
	srv := s.start()

	target := config.Target{
		Name: "t1",
		Auth: config.Auth{Token: "tok"},
		Orgs: []config.OrgConfig{{Org: "gjed"}, {Org: "acme"}},
	}
	d := discovery.New(target, newTestClient(t, srv.URL), discovery.WithRefreshInterval(time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for s.count("GET /user/repos", false) < 3 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for 3 refreshes")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	for _, req := range []string{"GET /users/gjed", "GET /users/acme", "GET /user"} {
		if n := s.count(req, false); n != 1 {
			t.Errorf("%s requested %d times, want 1", req, n)
		}
	}
}
