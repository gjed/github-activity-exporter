package collector

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shurcooL/githubv4"
)

// ExportedIssueNode is a fixture issue for FixtureIssueClient.
type ExportedIssueNode struct {
	State     string // "OPEN" or "CLOSED"
	Title     string
	Body      string
	CreatedAt time.Time
	// Author is the GraphQL login: a GitHub App bot has no "[bot]" suffix.
	Author      string
	AuthorIsBot bool
	Labels      []string
}

// FixtureIssueClient is a GraphQLClient stub for one repo. It serves Issues
// to the issue and dashboard queries like the GitHub issues connection, which
// never returns pull requests, honouring $states, $filterBy.createdBy (a REST
// login) and $cursor, PageSize nodes at a time. PullRequests are served to the
// PR query only.
type FixtureIssueClient struct {
	Issues       []ExportedIssueNode
	PullRequests []ExportedPRNode
	// ExtraLabels are repo labels no issue carries.
	ExtraLabels []string
	// PageSize defaults to 100, the page size of the real queries.
	PageSize int
	// Err, when set, fails every query.
	Err error

	mu       sync.Mutex
	requests []FixtureIssueRequest
}

// FixtureIssueRequest records one query.
type FixtureIssueRequest struct {
	// Query is "issue-count", "issue-list", "dashboard" or "pr".
	Query  string
	States []string
	// CreatedBy is the $filterBy.createdBy login, empty when unset.
	CreatedBy string
	// Cursor is the query's $cursor, empty for the first page.
	Cursor string
}

// Requests returns the queries received so far.
func (f *FixtureIssueClient) Requests() []FixtureIssueRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FixtureIssueRequest(nil), f.requests...)
}

// Query fills q with one page of the fixture repo.
func (f *FixtureIssueClient) Query(ctx context.Context, q interface{}, vars map[string]interface{}) error {
	req := FixtureIssueRequest{}
	if s, _ := vars["states"].(*[]githubv4.IssueState); s != nil {
		for _, st := range *s {
			req.States = append(req.States, string(st))
		}
	}
	if fb, _ := vars["filterBy"].(*githubv4.IssueFilters); fb != nil && fb.CreatedBy != nil {
		req.CreatedBy = string(*fb.CreatedBy)
	}
	offset := 0
	if c, _ := vars["cursor"].(*githubv4.String); c != nil {
		req.Cursor = string(*c)
		offset, _ = strconv.Atoi(req.Cursor)
	}

	switch q.(type) {
	case *issueCountQuery:
		req.Query = "issue-count"
	case *issueListQuery:
		req.Query = "issue-list"
	case *dashboardQuery:
		req.Query = "dashboard"
	case *prQuery:
		req.Query = "pr"
	}
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()

	if f.Err != nil {
		return f.Err
	}
	size := f.PageSize
	if size <= 0 {
		size = 100
	}

	switch q := q.(type) {
	case *issueCountQuery:
		f.count(q, req.States, offset, size)
	case *issueListQuery:
		issues := f.issues(req.States, "")
		page, next, end := pageOf(issues, offset, size)
		for _, iss := range page {
			n := struct {
				State     string
				Title     string
				CreatedAt time.Time
				Labels    struct {
					Nodes []struct{ Name string }
				} `graphql:"labels(first: 100)"`
			}{State: iss.State, Title: iss.Title, CreatedAt: iss.CreatedAt}
			for _, l := range iss.Labels {
				n.Labels.Nodes = append(n.Labels.Nodes, struct{ Name string }{Name: l})
			}
			q.Repository.Issues.Nodes = append(q.Repository.Issues.Nodes, n)
		}
		q.Repository.Issues.PageInfo = pageInfo{HasNextPage: next, EndCursor: end}
	case *dashboardQuery:
		issues := f.issues([]string{string(githubv4.IssueStateOpen)}, req.CreatedBy)
		page, next, end := pageOf(issues, offset, size)
		for _, iss := range page {
			n := dashboardNode{Title: iss.Title, Body: iss.Body}
			if iss.Author != "" {
				n.Author = &struct {
					Typename string `graphql:"__typename"`
					Login    string
				}{Typename: authorTypename(iss.AuthorIsBot), Login: iss.Author}
			}
			q.Repository.Issues.Nodes = append(q.Repository.Issues.Nodes, n)
		}
		q.Repository.Issues.PageInfo = pageInfo{HasNextPage: next, EndCursor: end}
	case *prQuery:
		return (&FixturePRClient{Nodes: f.PullRequests, PageSize: f.PageSize}).Query(ctx, q, vars)
	}
	return nil
}

// count fills an issueCountQuery: state totals, the oldest open issue and one
// page of labels sorted by name, each with its issue count in states.
func (f *FixtureIssueClient) count(q *issueCountQuery, states []string, offset, size int) {
	r := &q.Repository
	r.Open.TotalCount = int64(len(f.issues([]string{"OPEN"}, "")))
	r.Closed.TotalCount = int64(len(f.issues([]string{"CLOSED"}, "")))
	open := f.issues([]string{"OPEN"}, "")
	sort.SliceStable(open, func(i, j int) bool { return open[i].CreatedAt.Before(open[j].CreatedAt) })
	if len(open) > 0 {
		r.Oldest.Nodes = append(r.Oldest.Nodes, struct{ CreatedAt time.Time }{open[0].CreatedAt})
	}

	// Every repo label, with the number of its issues in states.
	counts := map[string]int64{}
	for _, l := range f.ExtraLabels {
		counts[l] = 0
	}
	for _, iss := range f.Issues {
		in := len(states) == 0 || containsFold(states, iss.State)
		for _, l := range iss.Labels {
			if in {
				counts[l]++
			} else if _, ok := counts[l]; !ok {
				counts[l] = 0
			}
		}
	}
	names := make([]string, 0, len(counts))
	for n := range counts {
		names = append(names, n)
	}
	sort.Strings(names)
	page, next, end := pageOf(names, offset, size)
	for _, n := range page {
		node := struct {
			Name   string
			Issues struct{ TotalCount int64 } `graphql:"issues(states: $states)"`
		}{Name: n}
		node.Issues.TotalCount = counts[n]
		r.Labels.Nodes = append(r.Labels.Nodes, node)
	}
	r.Labels.PageInfo = pageInfo{HasNextPage: next, EndCursor: end}
}

// issues returns the fixture issues in states (all when empty) created by
// createdBy (any author when empty), matching GitHub's createdBy, which takes
// the REST login.
func (f *FixtureIssueClient) issues(states []string, createdBy string) []ExportedIssueNode {
	var out []ExportedIssueNode
	for _, iss := range f.Issues {
		if len(states) > 0 && !containsFold(states, iss.State) {
			continue
		}
		if createdBy != "" && !strings.EqualFold(restLogin(authorTypename(iss.AuthorIsBot), iss.Author), createdBy) {
			continue
		}
		out = append(out, iss)
	}
	return out
}

func authorTypename(bot bool) string {
	if bot {
		return "Bot"
	}
	return "User"
}

// pageOf returns items[offset:offset+size], whether more follow, and the
// cursor of the next page.
func pageOf[T any](items []T, offset, size int) ([]T, bool, githubv4.String) {
	end := min(offset+size, len(items))
	offset = min(offset, end)
	return items[offset:end], end < len(items), githubv4.String(strconv.Itoa(end))
}
