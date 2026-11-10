package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/shurcooL/githubv4"

	"github.com/gjed/github-activity-exporter/internal/discovery"
	ghclient "github.com/gjed/github-activity-exporter/internal/github"
)

// The batched selection is the one githubv4 sends for the per-repo query.
func TestSelectionOf_MatchesGithubv4(t *testing.T) {
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Query string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		sent = req.Query
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()
	gql := githubv4.NewEnterpriseClient(srv.URL, srv.Client())

	for _, tt := range []struct {
		name  string
		query any
		vars  map[string]any
		repo  reflect.Type
	}{
		{"issue count", &issueCountQuery{}, map[string]any{"states": (*[]githubv4.IssueState)(nil), "cursor": (*githubv4.String)(nil)}, reflect.TypeFor[issueCountRepo]()},
		{"dashboard", &dashboardQuery{}, map[string]any{"filterBy": (*githubv4.IssueFilters)(nil), "cursor": (*githubv4.String)(nil)}, reflect.TypeFor[dashboardRepo]()},
	} {
		tt.vars["owner"], tt.vars["name"] = githubv4.String("o"), githubv4.String("n")
		if err := gql.Query(context.Background(), tt.query, tt.vars); err != nil {
			t.Fatalf("%s: Query: %v", tt.name, err)
		}
		want := "{repository(owner: $owner, name: $name)" + selectionOf(tt.repo) + "}"
		if !strings.HasSuffix(sent, want) {
			t.Errorf("%s: githubv4 sent %q, want it to end with %q", tt.name, sent, want)
		}
	}
}

func TestAliasIndex(t *testing.T) {
	for _, tt := range []struct {
		path []any
		want int
		ok   bool
	}{
		{[]any{"r0"}, 0, true},
		{[]any{"r19", "labels", float64(3)}, 19, true},
		{[]any{"r20"}, 0, false},
		{[]any{"r01"}, 0, false},
		{[]any{"r-1"}, 0, false},
		{[]any{"rateLimit"}, 0, false},
		{[]any{float64(0)}, 0, false},
		{nil, 0, false},
	} {
		got, ok := aliasIndex(tt.path, 20)
		if got != tt.want || ok != tt.ok {
			t.Errorf("aliasIndex(%v) = %d, %v, want %d, %v", tt.path, got, ok, tt.want, tt.ok)
		}
	}
}

// The batched query aliases repos by position and passes names as variables.
func TestRunRepoBatch_Query(t *testing.T) {
	var gotQuery string
	var gotVars map[string]any
	fake := rawFunc(func(query string, vars map[string]any) {
		gotQuery, gotVars = query, vars
	})
	runRepoBatch(context.Background(), fake, issueCountBatch, []discovery.Repo{
		{Owner: "org", Name: ".github"}, {Owner: "org", Name: "my-repo.v2"},
	})

	if strings.Contains(gotQuery, ".github") || strings.Contains(gotQuery, "my-repo.v2") {
		t.Errorf("query contains a repo name: %s", gotQuery)
	}
	for _, s := range []string{"query($states: [IssueState!], $cursor: String, $o0: String!, $n0: String!, $o1: String!, $n1: String!)", "rateLimit { cost remaining resetAt }", " r0: repository(owner: $o0, name: $n0) {open: issues", " r1: repository(owner: $o1, name: $n1) {"} {
		if !strings.Contains(gotQuery, s) {
			t.Errorf("query %q does not contain %q", gotQuery, s)
		}
	}
	if gotVars["n0"] != ".github" || gotVars["n1"] != "my-repo.v2" || gotVars["o1"] != "org" {
		t.Errorf("vars = %v", gotVars)
	}
}

// rawFunc is a RawGraphQLClient that records the query and answers no data.
type rawFunc func(query string, vars map[string]any)

func (f rawFunc) QueryRaw(_ context.Context, query string, vars map[string]any) (*ghclient.RawResponse, error) {
	f(query, vars)
	return &ghclient.RawResponse{}, nil
}

// An error on rateLimit leaves the repos of the batch to the response's data.
func TestRunRepoBatch_RateLimitErrorIgnored(t *testing.T) {
	fake := respFunc{&ghclient.RawResponse{
		Data:   map[string]json.RawMessage{"rateLimit": json.RawMessage("null"), "r0": json.RawMessage(`{"a":1}`), "r1": json.RawMessage(`{"a":2}`)},
		Errors: []ghclient.GraphQLError{{Type: "FORBIDDEN", Path: []any{"rateLimit"}, Message: "rate limiting is disabled"}},
	}}
	out := runRepoBatch(context.Background(), fake, issueCountBatch, []discovery.Repo{{Owner: "o", Name: "a"}, {Owner: "o", Name: "b"}})
	for i := range 2 {
		if out.retry[i] || out.failed[i] != nil || out.data[i] == nil {
			t.Errorf("repo %d: retry = %v, failed = %v, data = %s, want the data", i, out.retry[i], out.failed[i], out.data[i])
		}
	}
}

// respFunc is a RawGraphQLClient that answers every query with one response.
type respFunc struct{ resp *ghclient.RawResponse }

func (r respFunc) QueryRaw(context.Context, string, map[string]any) (*ghclient.RawResponse, error) {
	return r.resp, nil
}
