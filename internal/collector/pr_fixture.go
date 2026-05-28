package collector

import (
	"context"
	"time"

	"github.com/shurcooL/githubv4"
)

// ExportedPRNode is the public test-facing equivalent of prNode.
// It lets tests construct fixture PRs without depending on the internal type.
type ExportedPRNode struct {
	Number         int
	State          string // "OPEN", "CLOSED", "MERGED"
	IsDraft        bool
	CreatedAt      time.Time
	MergedAt       *time.Time
	ClosedAt       *time.Time
	Labels         []string
	Reviews        []string // review states ("APPROVED", "COMMENTED", etc.)
	ReviewDecision *string
}

// fixturePRClient implements GraphQLClient using a pre-baked list of nodes.
type fixturePRClient struct {
	nodes []ExportedPRNode
}

// NewFixturePRClient creates a GraphQLClient stub returning the given nodes
// in a single page (no pagination).
func NewFixturePRClient(nodes []ExportedPRNode) GraphQLClient {
	return &fixturePRClient{nodes: nodes}
}

// Query fills q (a *prQuery) with the fixture nodes.
func (f *fixturePRClient) Query(_ context.Context, q interface{}, _ map[string]interface{}) error {
	pq, ok := q.(*prQuery)
	if !ok {
		return nil
	}

	for _, n := range f.nodes {
		node := prNode{
			Number:         n.Number,
			State:          n.State,
			IsDraft:        n.IsDraft,
			CreatedAt:      n.CreatedAt,
			MergedAt:       n.MergedAt,
			ClosedAt:       n.ClosedAt,
			ReviewDecision: n.ReviewDecision,
		}
		for _, l := range n.Labels {
			node.Labels.Nodes = append(node.Labels.Nodes, struct{ Name string }{Name: l})
		}
		for _, r := range n.Reviews {
			node.Reviews.Nodes = append(node.Reviews.Nodes, struct{ State string }{State: r})
		}
		pq.Repository.PullRequests.Nodes = append(pq.Repository.PullRequests.Nodes, node)
	}
	pq.Repository.PullRequests.PageInfo.HasNextPage = false
	pq.Repository.PullRequests.PageInfo.EndCursor = githubv4.String("")
	return nil
}

// InjectPRQueryResult is exported for use in external test packages that need
// to fill a prQuery via the Query interface.
// It is a no-op if q is not a *prQuery.
func InjectPRQueryResult(q interface{}, nodes []ExportedPRNode, hasNextPage bool) error {
	pq, ok := q.(*prQuery)
	if !ok {
		return nil
	}
	for _, n := range nodes {
		node := prNode{
			Number:         n.Number,
			State:          n.State,
			IsDraft:        n.IsDraft,
			CreatedAt:      n.CreatedAt,
			MergedAt:       n.MergedAt,
			ClosedAt:       n.ClosedAt,
			ReviewDecision: n.ReviewDecision,
		}
		for _, l := range n.Labels {
			node.Labels.Nodes = append(node.Labels.Nodes, struct{ Name string }{Name: l})
		}
		for _, r := range n.Reviews {
			node.Reviews.Nodes = append(node.Reviews.Nodes, struct{ State string }{State: r})
		}
		pq.Repository.PullRequests.Nodes = append(pq.Repository.PullRequests.Nodes, node)
	}
	pq.Repository.PullRequests.PageInfo.HasNextPage = hasNextPage
	return nil
}


