//go:build integration

package integration

import (
	"context"
	"time"

	"github.com/google/go-github/v62/github"

	"github.com/gjed/renovate-exporter/internal/discovery"
)

// fakeIssueListClient serves pre-baked issues for both the issue and dashboard collectors.
type fakeIssueListClient struct {
	issues []*github.Issue
}

func newFakeIssueListClient(issues []*github.Issue) *fakeIssueListClient {
	return &fakeIssueListClient{issues: issues}
}

func (f *fakeIssueListClient) ListByRepo(_ context.Context, _, _ string, _ *github.IssueListByRepoOptions) ([]*github.Issue, *github.Response, error) {
	return f.issues, &github.Response{}, nil
}

// makeIssues returns a simple set of non-dashboard open issues.
func makeIssues(now time.Time) []*github.Issue {
	return []*github.Issue{
		makeIssue(1, "open", "Real bug", now.Add(-24*time.Hour), "renovate[bot]"),
	}
}

func makeIssue(number int, state, title string, createdAt time.Time, authorLogin string) *github.Issue {
	n := number
	body := ""
	ts := github.Timestamp{Time: createdAt}
	return &github.Issue{
		Number:    &n,
		State:     &state,
		Title:     &title,
		Body:      &body,
		CreatedAt: &ts,
		User:      &github.User{Login: &authorLogin},
	}
}

// makeDashboardIssueWithBody returns a single dashboard issue with the given body.
// This is exported for use in TestCollectionLoop_MockData.
func makeDashboardIssueWithBody(body string) []*github.Issue {
	n := 1
	state := "open"
	title := "Dependency Dashboard"
	author := "renovate[bot]"
	ts := github.Timestamp{Time: time.Now().Add(-time.Hour)}
	return []*github.Issue{
		{
			Number:    &n,
			State:     &state,
			Title:     &title,
			Body:      &body,
			CreatedAt: &ts,
			User:      &github.User{Login: &author},
		},
	}
}

// fakeRepoSource implements collector.RepoSource using a static list.
type fakeRepoSource struct {
	repos []discovery.Repo
}

func (f *fakeRepoSource) Repos(_ context.Context) ([]discovery.Repo, error) {
	return f.repos, nil
}
