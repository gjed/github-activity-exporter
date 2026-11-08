//go:build integration

package integration

import (
	"context"
	"time"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/discovery"
)

// makeIssues returns a simple set of non-dashboard open issues.
func makeIssues(now time.Time) []collector.ExportedIssueNode {
	return []collector.ExportedIssueNode{
		{State: "OPEN", Title: "Real bug", CreatedAt: now.Add(-24 * time.Hour), Author: "renovate", AuthorIsBot: true},
	}
}

// makeDashboardIssueWithBody returns a single dashboard issue by renovate[bot]
// with the given body.
func makeDashboardIssueWithBody(body string) []collector.ExportedIssueNode {
	return []collector.ExportedIssueNode{
		{State: "OPEN", Title: "Dependency Dashboard", Body: body, CreatedAt: time.Now().Add(-time.Hour), Author: "renovate", AuthorIsBot: true},
	}
}

// fakeRepoSource implements collector.RepoSource using a static list.
type fakeRepoSource struct {
	repos []discovery.Repo
}

func (f *fakeRepoSource) Repos(_ context.Context) ([]discovery.Repo, error) {
	return f.repos, nil
}
