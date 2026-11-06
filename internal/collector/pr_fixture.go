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

// ExportedPRNode is the public test-facing equivalent of prNode.
// It lets tests construct fixture PRs without depending on the internal type.
type ExportedPRNode struct {
	Number    int
	State     string // "OPEN", "CLOSED", "MERGED"
	IsDraft   bool
	CreatedAt time.Time
	// UpdatedAt defaults to the latest of CreatedAt, MergedAt and ClosedAt.
	UpdatedAt   time.Time
	MergedAt    *time.Time
	ClosedAt    *time.Time
	HeadRefName string
	// Author is the GraphQL login: a GitHub App bot has no "[bot]" suffix.
	Author         string
	AuthorIsBot    bool
	Labels         []string
	Reviews        []string // review states ("APPROVED", "COMMENTED", etc.)
	ReviewDecision *string
}

// FixturePRClient is a GraphQLClient stub serving Nodes like the GitHub
// pullRequests connection: it honours the $states, $labels, $orderBy and
// $cursor variables and pages PageSize nodes at a time.
type FixturePRClient struct {
	Nodes []ExportedPRNode
	// PageSize defaults to 100, the page size of the real query.
	PageSize int
	// Err, when set, fails every query.
	Err error
	// FailEventScan fails queries ordered by UPDATED_AT.
	FailEventScan bool

	mu       sync.Mutex
	requests []FixtureRequest
}

// FixtureRequest records the variables of one query.
type FixtureRequest struct {
	States  []string
	Labels  []string
	OrderBy string // "CREATED_AT" or "UPDATED_AT"
}

// NewFixturePRClient creates a FixturePRClient serving the given nodes.
func NewFixturePRClient(nodes []ExportedPRNode) *FixturePRClient {
	return &FixturePRClient{Nodes: nodes}
}

// Requests returns the queries received so far.
func (f *FixturePRClient) Requests() []FixtureRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FixtureRequest(nil), f.requests...)
}

// RequestCount returns how many queries used the given order field.
func (f *FixturePRClient) RequestCount(orderBy string) int {
	n := 0
	for _, r := range f.Requests() {
		if r.OrderBy == orderBy {
			n++
		}
	}
	return n
}

// Query fills q (a *prQuery) with one page of matching fixture nodes.
func (f *FixturePRClient) Query(_ context.Context, q interface{}, vars map[string]interface{}) error {
	pq, ok := q.(*prQuery)
	if !ok {
		return nil
	}

	req := FixtureRequest{}
	if s, _ := vars["states"].(*[]githubv4.PullRequestState); s != nil {
		for _, st := range *s {
			req.States = append(req.States, string(st))
		}
	}
	if l, _ := vars["labels"].(*[]githubv4.String); l != nil {
		for _, name := range *l {
			req.Labels = append(req.Labels, string(name))
		}
	}
	order, _ := vars["orderBy"].(githubv4.IssueOrder)
	req.OrderBy = string(order.Field)

	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()

	if f.Err != nil {
		return f.Err
	}
	if f.FailEventScan && order.Field == githubv4.IssueOrderFieldUpdatedAt {
		return errFixtureEventScan
	}

	var matched []prNode
	for _, n := range f.Nodes {
		if len(req.States) > 0 && !containsFold(req.States, n.State) {
			continue
		}
		if len(req.Labels) > 0 && !anyFold(req.Labels, n.Labels) {
			continue
		}
		matched = append(matched, toPRNode(n))
	}

	sort.SliceStable(matched, func(i, j int) bool {
		a, b := matched[i], matched[j]
		if order.Field == githubv4.IssueOrderFieldUpdatedAt {
			if order.Direction == githubv4.OrderDirectionAsc {
				return a.UpdatedAt.Before(b.UpdatedAt)
			}
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		if order.Direction == githubv4.OrderDirectionDesc {
			return a.CreatedAt.After(b.CreatedAt)
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})

	offset := 0
	if c, _ := vars["cursor"].(*githubv4.String); c != nil {
		offset, _ = strconv.Atoi(string(*c))
	}
	size := f.PageSize
	if size <= 0 {
		size = 100
	}
	end := min(offset+size, len(matched))
	if offset > end {
		offset = end
	}

	pq.Repository.PullRequests.Nodes = matched[offset:end]
	pq.Repository.PullRequests.PageInfo.HasNextPage = end < len(matched)
	pq.Repository.PullRequests.PageInfo.EndCursor = githubv4.String(strconv.Itoa(end))
	return nil
}

type fixtureError string

func (e fixtureError) Error() string { return string(e) }

const errFixtureEventScan = fixtureError("fixture: event scan failed")

func toPRNode(n ExportedPRNode) prNode {
	node := prNode{
		Number:         n.Number,
		State:          n.State,
		IsDraft:        n.IsDraft,
		CreatedAt:      n.CreatedAt,
		UpdatedAt:      n.UpdatedAt,
		MergedAt:       n.MergedAt,
		ClosedAt:       n.ClosedAt,
		HeadRefName:    n.HeadRefName,
		ReviewDecision: n.ReviewDecision,
	}
	if node.UpdatedAt.IsZero() {
		node.UpdatedAt = n.CreatedAt
		for _, t := range []*time.Time{n.MergedAt, n.ClosedAt} {
			if t != nil && t.After(node.UpdatedAt) {
				node.UpdatedAt = *t
			}
		}
	}
	if n.Author != "" {
		typename := "User"
		if n.AuthorIsBot {
			typename = "Bot"
		}
		node.Author = &struct {
			Typename string `graphql:"__typename"`
			Login    string
		}{Typename: typename, Login: n.Author}
	}
	for _, l := range n.Labels {
		node.Labels.Nodes = append(node.Labels.Nodes, struct{ Name string }{Name: l})
	}
	for _, r := range n.Reviews {
		node.Reviews.Nodes = append(node.Reviews.Nodes, struct{ State string }{State: r})
	}
	return node
}

func containsFold(items []string, s string) bool {
	for _, it := range items {
		if strings.EqualFold(it, s) {
			return true
		}
	}
	return false
}

func anyFold(want, have []string) bool {
	for _, h := range have {
		if containsFold(want, h) {
			return true
		}
	}
	return false
}
