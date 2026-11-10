package github_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	internalgithub "github.com/gjed/github-activity-exporter/internal/github"
)

// rawServer answers every request with status and body, and records the last
// request path and body.
func rawServer(t *testing.T, status int, body string) (*httptest.Server, *string, *string) {
	t.Helper()
	var path, reqBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		path, reqBody = r.URL.Path, string(b)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &path, &reqBody
}

func TestQueryRaw_PartialErrors(t *testing.T) {
	srv, path, reqBody := rawServer(t, http.StatusOK, `{
		"data": {"r0": {"name": "a"}, "r1": null},
		"errors": [{"type": "NOT_FOUND", "path": ["r1"], "message": "Could not resolve to a Repository"}]
	}`)
	c, err := internalgithub.NewClient(&mockAuthenticator{token: "tok"}, internalgithub.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	resp, err := c.QueryRaw(context.Background(), "query($o0: String!) { r0: x }", map[string]any{"o0": "acme"})
	if err != nil {
		t.Fatalf("QueryRaw: %v", err)
	}
	if *path != "/graphql" {
		t.Errorf("path = %q, want /graphql", *path)
	}
	var sent struct {
		Query     string
		Variables map[string]any
	}
	if err := json.Unmarshal([]byte(*reqBody), &sent); err != nil || sent.Variables["o0"] != "acme" || !strings.HasPrefix(sent.Query, "query(") {
		t.Errorf("request body = %s (%v)", *reqBody, err)
	}
	if string(resp.Data["r0"]) != `{"name": "a"}` || string(resp.Data["r1"]) != "null" {
		t.Errorf("data = %v", resp.Data)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Type != "NOT_FOUND" || len(resp.Errors[0].Path) != 1 || resp.Errors[0].Path[0] != "r1" {
		t.Errorf("errors = %+v", resp.Errors)
	}
}

func TestQueryRaw_Non200(t *testing.T) {
	srv, _, _ := rawServer(t, http.StatusBadGateway, `bad gateway`)
	c, err := internalgithub.NewClient(&mockAuthenticator{token: "tok"}, internalgithub.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.QueryRaw(context.Background(), "query { x }", nil); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("err = %v, want a 502 error", err)
	}
}

func TestQueryRaw_UndecodableBody(t *testing.T) {
	srv, _, _ := rawServer(t, http.StatusOK, `not json`)
	c, err := internalgithub.NewClient(&mockAuthenticator{token: "tok"}, internalgithub.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.QueryRaw(context.Background(), "query { x }", nil); err == nil {
		t.Error("err = nil, want a decoding error")
	}
}

// The rateLimit field of the body is recorded as the graphql pool state: below
// the threshold the pool pauses and the hook sees the body's values.
func TestQueryRaw_RateLimitFromBody(t *testing.T) {
	resetAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	srv, _, _ := rawServer(t, http.StatusOK, `{"data": {"rateLimit": {"cost": 1, "remaining": 150, "resetAt": "`+resetAt.Format(time.RFC3339)+`"}}}`)

	var got []internalgithub.RateLimitState
	c, err := internalgithub.NewClient(&mockAuthenticator{token: "tok"},
		internalgithub.WithBaseURL(srv.URL),
		internalgithub.WithRateLimitHook(func(s internalgithub.RateLimitState) { got = append(got, s) }),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.QueryRaw(context.Background(), "query { rateLimit { cost remaining resetAt } }", nil); err != nil {
		t.Fatalf("QueryRaw: %v", err)
	}

	if len(got) != 1 || got[0].Resource != "graphql" || got[0].Remaining != 150 || !got[0].ResetAt.Equal(resetAt) {
		t.Fatalf("hook states = %+v, want one graphql state with remaining 150, reset %v", got, resetAt)
	}
	if s := c.RateLimitState("graphql"); s.Remaining != 150 {
		t.Errorf("graphql state = %+v", s)
	}

	// The pool is paused: the next GraphQL request waits for resetAt.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.QueryRaw(ctx, "query { x }", nil); err == nil {
		t.Error("second QueryRaw: err = nil, want the context deadline while the pool is paused")
	}
}

// With X-RateLimit headers, the transport records the pool state and the body
// does not record it again.
func TestQueryRaw_RateLimitHeadersWin(t *testing.T) {
	reset := time.Now().Add(time.Hour).Unix()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Resource", "graphql")
		w.Header().Set("X-RateLimit-Remaining", "4000")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
		_, _ = w.Write([]byte(`{"data": {"rateLimit": {"cost": 1, "remaining": 3999, "resetAt": "2030-01-01T00:00:00Z"}}}`))
	}))
	defer srv.Close()

	var got []internalgithub.RateLimitState
	c, err := internalgithub.NewClient(&mockAuthenticator{token: "tok"},
		internalgithub.WithBaseURL(srv.URL),
		internalgithub.WithRateLimitHook(func(s internalgithub.RateLimitState) { got = append(got, s) }),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.QueryRaw(context.Background(), "query { rateLimit { cost remaining resetAt } }", nil); err != nil {
		t.Fatalf("QueryRaw: %v", err)
	}
	if len(got) != 1 || got[0].Remaining != 4000 || got[0].ResetAt.Unix() != reset {
		t.Errorf("hook states = %+v, want one state from the headers (4000)", got)
	}
}
