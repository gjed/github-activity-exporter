//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v93/github"
)

// The mock serves exactly what the exporter calls for one target over
// base_url <mock>/api/v3: GET /user (PAT check), GET /users/{org} (owner
// type lookup), GET /orgs/{org}/repos,
// and POST /api/graphql (the PR query, and the issue count and Dependency
// Dashboard queries, per repo or batched with one alias r<i> per repo, whose
// response shapes mirror the struct tags of prQuery in
// internal/collector/pr.go, issueCountRepo in issue.go and dashboardRepo in
// dashboard.go). Any other request is recorded and answered 404;
// the test fails on recorded requests.

const e2eOrg = "acme"

// dependencyDashboardBody mirrors the sections the Renovate dashboard collector parses.
const dependencyDashboardBody = `This issue lists Renovate updates and detected dependencies.

## Awaiting Schedule
- [ ] <!-- unschedule-branch=renovate/foo -->foo
- [ ] <!-- unschedule-branch=renovate/bar -->bar

## Rate-Limited
- [x] <!-- unlimit-branch=renovate/baz -->baz

## Pending Approval

## Open
- [ ] <!-- rebase-branch=renovate/quux -->quux
`

type gqlAuthor struct {
	Typename string `json:"__typename"`
	Login    string `json:"login"`
}

type gqlNodes[T any] struct {
	Nodes []T `json:"nodes"`
}

type gqlName struct {
	Name string `json:"name"`
}

type gqlReviewState struct {
	State string `json:"state"`
}

// gqlIssue is a fixture issue as GitHub's GraphQL API describes it.
type gqlIssue struct {
	State     string // "OPEN" or "CLOSED"
	Title     string
	Body      string
	Author    *gqlAuthor
	CreatedAt time.Time
	Labels    []string
}

// gqlPR is the PR node of the GraphQL response, with the field names of prNode.
type gqlPR struct {
	Number         int                      `json:"number"`
	State          string                   `json:"state"`
	IsDraft        bool                     `json:"isDraft"`
	CreatedAt      time.Time                `json:"createdAt"`
	UpdatedAt      time.Time                `json:"updatedAt"`
	MergedAt       *time.Time               `json:"mergedAt"`
	ClosedAt       *time.Time               `json:"closedAt"`
	HeadRefName    string                   `json:"headRefName"`
	Author         *gqlAuthor               `json:"author"`
	Labels         gqlNodes[gqlName]        `json:"labels"`
	Reviews        gqlNodes[gqlReviewState] `json:"reviews"`
	ReviewDecision *string                  `json:"reviewDecision"`
}

type mockGitHub struct {
	// URL is the server root without /api/v3.
	URL string

	fixturePRs map[string][]gqlPR
	fixtureIss map[string][]gqlIssue

	mu           sync.Mutex
	nextNumber   int
	eventPRs     map[string][]gqlPR
	issuesFailed bool
	unhandled    []string
}

// newMockGitHub starts the mock with one org (acme) and two repos. The first
// issue count of repo-b fails once: an HTTP 500 for a per-repo query, a
// GraphQL error on its alias for a batched one.
func newMockGitHub(t *testing.T) *mockGitHub {
	t.Helper()
	now := time.Now().UTC()
	m := &mockGitHub{
		fixturePRs: fixturePRs(now),
		fixtureIss: fixtureIssues(now),
		eventPRs:   make(map[string][]gqlPR),
		nextNumber: 1000,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/user", m.user)
	mux.HandleFunc("GET /api/v3/users/{name}", m.owner)
	mux.HandleFunc("GET /api/v3/orgs/{org}/repos", m.orgRepos)
	mux.HandleFunc("POST /api/graphql", m.graphql)
	mux.HandleFunc("/", m.unhandledRequest)

	srv := httptest.NewServer(withRateLimitHeaders(mux))
	t.Cleanup(srv.Close)
	m.URL = srv.URL
	return m
}

// unhandledRequests returns "METHOD path body" for every request the mock did not handle.
func (m *mockGitHub) unhandledRequests() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.unhandled...)
}

func (m *mockGitHub) unhandledRequest(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	m.mu.Lock()
	m.unhandled = append(m.unhandled, fmt.Sprintf("%s %s %s", r.Method, r.URL.RequestURI(), body))
	m.mu.Unlock()
	slog.Default().Warn("mock github: unhandled request", "method", r.Method, "path", r.URL.Path, "body", string(body))
	http.Error(w, `{"message":"not handled by e2e mock"}`, http.StatusNotFound)
}

// withRateLimitHeaders sets the rate limit headers GitHub sends on every
// response. REST is the core pool, the GraphQL endpoint is graphql.
func withRateLimitHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resource := "core"
		if r.URL.Path == "/api/graphql" {
			resource = "graphql"
		}
		h := w.Header()
		h.Set("X-RateLimit-Limit", "5000")
		h.Set("X-RateLimit-Remaining", "4999")
		h.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		h.Set("X-RateLimit-Used", "1")
		h.Set("X-RateLimit-Resource", resource)
		next.ServeHTTP(w, r)
	})
}

func (m *mockGitHub) user(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{"login": "e2e-bot"})
}

func (m *mockGitHub) owner(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("name") != e2eOrg {
		m.unhandledRequest(w, r)
		return
	}
	writeJSON(w, map[string]string{"login": e2eOrg, "type": "Organization"})
}

func (m *mockGitHub) orgRepos(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("org") != e2eOrg {
		m.unhandledRequest(w, r)
		return
	}
	repos := []*github.Repository{}
	for _, name := range []string{"repo-a", "repo-b"} {
		repos = append(repos, &github.Repository{
			Name:     github.Ptr(name),
			FullName: github.Ptr(e2eOrg + "/" + name),
			Owner:    &github.User{Login: github.Ptr(e2eOrg)},
			Archived: github.Ptr(false),
			Fork:     github.Ptr(false),
		})
	}
	writeJSON(w, repos)
}

// graphql serves the PR, issue count and dashboard queries. The PR open scan (CREATED_AT ASC, states [OPEN])
// returns the fixture PRs. The event scan (UPDATED_AT DESC, states null) first
// records a fresh merged PR without approval and a fresh closed PR, both
// stamped with this request's time. The exporter counts an event in the
// cycle after the request, when the request time falls in that cycle's window.
func (m *mockGitHub) graphql(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Query     string `json:"query"`
		Variables struct {
			Owner   string   `json:"owner"`
			Name    string   `json:"name"`
			States  []string `json:"states"`
			OrderBy struct {
				Field string `json:"field"`
			} `json:"orderBy"`
			FilterBy *struct {
				CreatedBy string `json:"createdBy"`
			} `json:"filterBy"`
		} `json:"variables"`
	}
	fullName := ""
	if err := json.Unmarshal(body, &req); err == nil {
		fullName = req.Variables.Owner + "/" + req.Variables.Name
	}
	if strings.Contains(req.Query, "r0: repository(owner: $o0, name: $n0)") {
		if m.batch(w, body) {
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		m.unhandledRequest(w, r)
		return
	}
	_, known := m.fixturePRs[fullName]
	switch {
	case known && strings.Contains(req.Query, "pullRequests("):
	case known && strings.Contains(req.Query, "oldest: issues("):
		if m.failIssuesOnce(fullName) {
			http.Error(w, `{"message":"e2e: injected failure"}`, http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"data": map[string]any{"repository": m.issueCounts(fullName, req.Variables.States)}})
		return
	case known && req.Variables.FilterBy != nil && strings.Contains(req.Query, "issues(first: 100, states: OPEN, filterBy: $filterBy"):
		writeJSON(w, map[string]any{"data": map[string]any{"repository": m.dashboard(fullName, req.Variables.FilterBy.CreatedBy)}})
		return
	default:
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		m.unhandledRequest(w, r)
		return
	}

	m.mu.Lock()
	if req.Variables.OrderBy.Field == "UPDATED_AT" {
		m.recordEventPRs(fullName, time.Now().UTC())
	}
	nodes := append(append([]gqlPR(nil), m.fixturePRs[fullName]...), m.eventPRs[fullName]...)
	m.mu.Unlock()

	var out []gqlPR
	for _, n := range nodes {
		if req.Variables.States == nil || slices.Contains(req.Variables.States, n.State) {
			out = append(out, n)
		}
	}
	if req.Variables.OrderBy.Field == "UPDATED_AT" {
		sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	} else {
		sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	}

	writeJSON(w, map[string]any{"data": map[string]any{"repository": map[string]any{
		"pullRequests": map[string]any{
			"nodes":    out,
			"pageInfo": map[string]any{"hasNextPage": false, "endCursor": ""},
		},
	}}})
}

// batch answers a batched issue count or dashboard query: alias r<i> is the
// repo $o<i>/$n<i>, with rateLimit as GitHub returns it. It reports false
// for a query or repo it does not serve.
func (m *mockGitHub) batch(w http.ResponseWriter, body []byte) bool {
	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return false
	}
	var states []string
	if st, ok := req.Variables["states"].([]any); ok {
		states = []string{}
		for _, v := range st {
			states = append(states, v.(string))
		}
	}
	createdBy := ""
	if fb, ok := req.Variables["filterBy"].(map[string]any); ok {
		createdBy, _ = fb["createdBy"].(string)
	}

	data := map[string]any{"rateLimit": map[string]any{
		"cost": 1, "remaining": 4999, "resetAt": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}}
	var errs []map[string]any
	for i := 0; ; i++ {
		owner, ok := req.Variables["o"+strconv.Itoa(i)].(string)
		if !ok {
			break
		}
		name, _ := req.Variables["n"+strconv.Itoa(i)].(string)
		fullName, alias := owner+"/"+name, "r"+strconv.Itoa(i)
		if _, known := m.fixturePRs[fullName]; !known {
			return false
		}
		switch {
		case strings.Contains(req.Query, "oldest: issues("):
			if m.failIssuesOnce(fullName) {
				data[alias] = nil
				errs = append(errs, map[string]any{"type": "INTERNAL", "path": []string{alias}, "message": "e2e: injected failure"})
				continue
			}
			data[alias] = m.issueCounts(fullName, states)
		case createdBy != "" && strings.Contains(req.Query, "issues(first: 100, states: OPEN, filterBy: $filterBy"):
			data[alias] = m.dashboard(fullName, createdBy)
		default:
			return false
		}
	}
	out := map[string]any{"data": data}
	if errs != nil {
		out["errors"] = errs
	}
	writeJSON(w, out)
	return true
}

// failIssuesOnce reports true for the first issue count query of repo-b, so
// github_exporter.api.errors gets a series.
func (m *mockGitHub) failIssuesOnce(fullName string) bool {
	if fullName != e2eOrg+"/repo-b" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fail := !m.issuesFailed
	m.issuesFailed = true
	return fail
}

// issueCounts returns the issue count query's repository: per-state totals,
// the oldest open issue and every label with its issue count in states.
func (m *mockGitHub) issueCounts(fullName string, states []string) map[string]any {
	total := map[string]int{}
	var oldestAt time.Time
	labels := map[string]int{} // every repo label, with its issue count in states
	for _, iss := range m.fixtureIss[fullName] {
		total[iss.State]++
		if iss.State == "OPEN" && (oldestAt.IsZero() || iss.CreatedAt.Before(oldestAt)) {
			oldestAt = iss.CreatedAt
		}
		in := states == nil || slices.Contains(states, iss.State)
		for _, l := range iss.Labels {
			if in {
				labels[l]++
			} else if _, ok := labels[l]; !ok {
				labels[l] = 0
			}
		}
	}
	oldest := []map[string]any{}
	if !oldestAt.IsZero() {
		oldest = append(oldest, map[string]any{"createdAt": oldestAt})
	}
	var labelNodes []map[string]any
	for _, name := range slices.Sorted(maps.Keys(labels)) {
		labelNodes = append(labelNodes, map[string]any{"name": name, "issues": map[string]any{"totalCount": labels[name]}})
	}
	return map[string]any{
		"open":   map[string]any{"totalCount": total["OPEN"]},
		"closed": map[string]any{"totalCount": total["CLOSED"]},
		"oldest": map[string]any{"nodes": oldest},
		"labels": map[string]any{
			"nodes":    labelNodes,
			"pageInfo": map[string]any{"hasNextPage": false, "endCursor": ""},
		},
	}
}

// dashboard returns the Dependency Dashboard query's repository: the open
// issues created by createdBy, a REST login ("renovate[bot]" for the
// Renovate app).
func (m *mockGitHub) dashboard(fullName, createdBy string) map[string]any {
	nodes := []map[string]any{}
	for _, iss := range m.fixtureIss[fullName] {
		login := iss.Author.Login
		if iss.Author.Typename == "Bot" {
			login += "[bot]"
		}
		if iss.State == "OPEN" && login == createdBy {
			nodes = append(nodes, map[string]any{"title": iss.Title, "body": iss.Body, "author": iss.Author})
		}
	}
	return map[string]any{
		"issues": map[string]any{
			"nodes":    nodes,
			"pageInfo": map[string]any{"hasNextPage": false, "endCursor": ""},
		},
	}
}

// recordEventPRs appends one merged and one closed PR created at now. Caller holds m.mu.
func (m *mockGitHub) recordEventPRs(fullName string, now time.Time) {
	dev := &gqlAuthor{Typename: "User", Login: "dev"}
	merged := gqlPR{
		Number: m.nextNumber, State: "MERGED", CreatedAt: now, UpdatedAt: now,
		MergedAt: &now, ClosedAt: &now, HeadRefName: "feat/e2e", Author: dev,
	}
	closed := gqlPR{
		Number: m.nextNumber + 1, State: "CLOSED", CreatedAt: now, UpdatedAt: now,
		ClosedAt: &now, HeadRefName: "feat/e2e-closed", Author: dev,
	}
	m.nextNumber += 2
	m.eventPRs[fullName] = append(m.eventPRs[fullName], merged, closed)
}

// fixturePRs are the open PRs per repo: one approved, one with changes
// requested, one draft, and labels. repo-b has one pending review.
func fixturePRs(now time.Time) map[string][]gqlPR {
	labels := func(names ...string) gqlNodes[gqlName] {
		var n gqlNodes[gqlName]
		for _, name := range names {
			n.Nodes = append(n.Nodes, gqlName{Name: name})
		}
		return n
	}
	reviews := func(states ...string) gqlNodes[gqlReviewState] {
		var n gqlNodes[gqlReviewState]
		for _, s := range states {
			n.Nodes = append(n.Nodes, gqlReviewState{State: s})
		}
		return n
	}
	dev := &gqlAuthor{Typename: "User", Login: "dev"}
	bot := &gqlAuthor{Typename: "Bot", Login: "renovate"} // GraphQL omits [bot]; the exporter adds it back.
	approved, changes, pending := "APPROVED", "CHANGES_REQUESTED", "REVIEW_REQUIRED"
	open := func(n int, created time.Duration) (time.Time, time.Time) {
		return now.Add(-created), now.Add(-time.Hour + time.Duration(n)*time.Minute)
	}
	c1, u1 := open(1, 48*time.Hour)
	c2, u2 := open(2, 24*time.Hour)
	c3, u3 := open(3, 5*time.Hour)
	c4, u4 := open(4, 2*time.Hour)
	return map[string][]gqlPR{
		e2eOrg + "/repo-a": {
			{Number: 1, State: "OPEN", CreatedAt: c1, UpdatedAt: u1, HeadRefName: "renovate/foo", Author: bot,
				Labels: labels("dependencies"), Reviews: reviews("APPROVED"), ReviewDecision: &approved},
			{Number: 2, State: "OPEN", CreatedAt: c2, UpdatedAt: u2, HeadRefName: "fix/bar", Author: dev,
				Labels: labels("bug"), Reviews: reviews("CHANGES_REQUESTED"), ReviewDecision: &changes},
			{Number: 3, State: "OPEN", IsDraft: true, CreatedAt: c3, UpdatedAt: u3, HeadRefName: "feat/wip", Author: dev,
				Labels: labels("dependencies")},
		},
		e2eOrg + "/repo-b": {
			{Number: 1, State: "OPEN", CreatedAt: c4, UpdatedAt: u4, HeadRefName: "feat/baz", Author: dev,
				Labels: labels("enhancement"), Reviews: reviews(), ReviewDecision: &pending},
		},
	}
}

// fixtureIssues are the issues per repo. repo-a carries the Renovate Dependency Dashboard.
func fixtureIssues(now time.Time) map[string][]gqlIssue {
	dev := &gqlAuthor{Typename: "User", Login: "dev"}
	bot := &gqlAuthor{Typename: "Bot", Login: "renovate"} // GraphQL omits [bot].
	return map[string][]gqlIssue{
		e2eOrg + "/repo-a": {
			{State: "OPEN", Title: "Flaky lint rule", Author: dev, CreatedAt: now.Add(-72 * time.Hour), Labels: []string{"bug", "help wanted"}},
			{State: "CLOSED", Title: "Old cleanup", Author: dev, CreatedAt: now.Add(-240 * time.Hour)},
			{State: "OPEN", Title: "Dependency Dashboard", Body: dependencyDashboardBody, Author: bot, CreatedAt: now.Add(-360 * time.Hour), Labels: []string{"renovate"}},
		},
		e2eOrg + "/repo-b": {
			{State: "OPEN", Title: "Docs typo", Author: dev, CreatedAt: now.Add(-12 * time.Hour), Labels: []string{"documentation"}},
			{State: "CLOSED", Title: "Done thing", Author: dev, CreatedAt: now.Add(-96 * time.Hour)},
		},
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
