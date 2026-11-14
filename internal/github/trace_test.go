package github_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	internalgithub "github.com/gjed/github-activity-exporter/internal/github"
	sc "github.com/gjed/github-activity-exporter/internal/semconv"
)

func newTracerProvider(t *testing.T) (*sdktrace.TracerProvider, *tracetest.SpanRecorder) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return tp, rec
}

func spanAttrs(s sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	m := map[attribute.Key]attribute.Value{}
	for _, kv := range s.Attributes() {
		m[kv.Key] = kv.Value
	}
	return m
}

func spansNamed(rec *tracetest.SpanRecorder, name string) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range rec.Ended() {
		if s.Name() == name {
			out = append(out, s)
		}
	}
	return out
}

// Each request is a client span named after its route, with url.template,
// and the GraphQL operation read from the document; no trace context is sent.
func TestTransport_ClientSpans(t *testing.T) {
	var traceparent atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Traceparent") != "" {
			traceparent.Store(true)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	tp, rec := newTracerProvider(t)
	rt, err := internalgithub.NewTransport(srv.URL, nil, tp)
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}

	cases := []struct {
		method, path, query string
		wantName            string
		wantAttrs           map[attribute.Key]string
		absent              []attribute.Key
	}{
		{
			method: http.MethodPost, path: "/graphql", query: `query IssueCountBatch($o0: String!) { rateLimit { cost } }`,
			wantName:  "POST /graphql",
			wantAttrs: map[attribute.Key]string{"url.template": "/graphql", "graphql.operation.type": "query", "graphql.operation.name": "IssueCountBatch"},
		},
		{
			method: http.MethodPost, path: "/graphql", query: `query($owner:String!){repository(owner:$owner){id}}`,
			wantName:  "POST /graphql",
			wantAttrs: map[attribute.Key]string{"graphql.operation.type": "query"},
			absent:    []attribute.Key{"graphql.operation.name"},
		},
		{
			method: http.MethodPost, path: "/graphql", query: `{ viewer { login } }`,
			wantName:  "POST /graphql",
			wantAttrs: map[attribute.Key]string{"graphql.operation.type": "query"},
			absent:    []attribute.Key{"graphql.operation.name"},
		},
		{
			method: http.MethodGet, path: "/orgs/zextras/repos?page=2",
			wantName:  "GET /orgs/{org}/repos",
			wantAttrs: map[attribute.Key]string{"url.template": "/orgs/{org}/repos"},
			absent:    []attribute.Key{"graphql.operation.type"},
		},
		{
			method: http.MethodGet, path: "/repos/zextras/carbonio",
			wantName: "GET",
			absent:   []attribute.Key{"url.template"},
		},
	}
	for _, tc := range cases {
		rec.Reset()
		var body io.Reader
		if tc.query != "" {
			body = bytes.NewReader([]byte(`{"query":` + strconv.Quote(tc.query) + `}`))
		}
		req, _ := http.NewRequest(tc.method, srv.URL+tc.path, body)
		resp, err := rt.RoundTrip(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		ended := rec.Ended()
		if len(ended) != 1 {
			t.Fatalf("%s %s: %d spans, want 1", tc.method, tc.path, len(ended))
		}
		s := ended[0]
		if s.Name() != tc.wantName || s.SpanKind() != trace.SpanKindClient {
			t.Errorf("%s %s: span %q kind %v, want %q client", tc.method, tc.path, s.Name(), s.SpanKind(), tc.wantName)
		}
		got := spanAttrs(s)
		for k, v := range tc.wantAttrs {
			if got[k].AsString() != v {
				t.Errorf("%s %s: %s = %q, want %q", tc.method, tc.path, k, got[k].AsString(), v)
			}
		}
		for _, k := range tc.absent {
			if _, ok := got[k]; ok {
				t.Errorf("%s %s: unexpected %s = %v", tc.method, tc.path, k, got[k].String())
			}
		}
	}
	if traceparent.Load() {
		t.Error("a request carried a traceparent header")
	}
}

// Without a TracerProvider the GraphQL request body is never read again.
func TestTransport_NoTracesReadsNoBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	mp := sdkmetric.NewMeterProvider()
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	rt, err := internalgithub.NewTransport(srv.URL, mp, nil)
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	payload := []byte(`{"query":"query Named { viewer { login } }"}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/graphql", bytes.NewReader(payload))
	var reads atomic.Int64
	req.GetBody = func() (io.ReadCloser, error) {
		reads.Add(1)
		return io.NopCloser(bytes.NewReader(payload)), nil
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()
	if n := reads.Load(); n != 0 {
		t.Errorf("GetBody calls = %d, want 0", n)
	}
}

// newTracedClient builds a client whose transport and rate limit waits
// record spans on tp, with recorded sleeps.
func newTracedClient(t *testing.T, srv *httptest.Server, tp trace.TracerProvider) *internalgithub.Client {
	t.Helper()
	rt, err := internalgithub.NewTransport(srv.URL, nil, tp)
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	c, _, _ := newSleepClient(t, srv,
		internalgithub.WithBaseTransport(rt),
		internalgithub.WithTracer(tp.Tracer("test")),
	)
	return c
}

// A request that waits for its pool's window reset records a primary wait
// span under its caller's span, and its retry carries the resend count.
func TestTrace_PrimaryWait(t *testing.T) {
	srv, _ := scriptedServer(t, func(n int, _ *http.Request, w http.ResponseWriter) bool {
		if n == 0 {
			w.Header().Set("X-RateLimit-Resource", "graphql")
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10))
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
			return true
		}
		return false
	})
	tp, rec := newTracerProvider(t)
	c := newTracedClient(t, srv, tp)

	ctx, parent := tp.Tracer("test").Start(context.Background(), "unit")
	if err := graphqlCall(ctx, c); err != nil {
		t.Fatalf("graphql call: %v", err)
	}
	parent.End()

	waits := spansNamed(rec, sc.SpanGitHubExporterRateLimitWait)
	if len(waits) != 1 {
		t.Fatalf("wait spans = %d, want 1", len(waits))
	}
	w := spanAttrs(waits[0])
	if w[sc.AttrGitHubApiRateLimitResource].AsString() != "graphql" || w[sc.AttrGitHubExporterRateLimitReason].AsString() != "primary" {
		t.Errorf("wait attributes = %v", waits[0].Attributes())
	}
	if waits[0].Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Error("wait span is not a child of the caller's span")
	}

	calls := spansNamed(rec, "POST /graphql")
	if len(calls) != 2 {
		t.Fatalf("client spans = %d, want 2", len(calls))
	}
	first, second := spanAttrs(calls[0]), spanAttrs(calls[1])
	if calls[0].Status().Code != codes.Error || first["error.type"].AsString() != "403" {
		t.Errorf("first attempt: status %v, error.type %q", calls[0].Status(), first["error.type"].AsString())
	}
	if _, ok := first["http.request.resend_count"]; ok {
		t.Error("first attempt has http.request.resend_count")
	}
	if got := second["http.request.resend_count"].AsInt64(); got != 1 {
		t.Errorf("second attempt http.request.resend_count = %d, want 1", got)
	}
}

// A secondary rate limit records a secondary wait span with the request's
// pool; a request that does not wait records none.
func TestTrace_SecondaryWait(t *testing.T) {
	srv, _ := scriptedServer(t, func(n int, _ *http.Request, w http.ResponseWriter) bool {
		if n == 0 {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"You have exceeded a secondary rate limit."}`))
			return true
		}
		return false
	})
	tp, rec := newTracerProvider(t)
	c := newTracedClient(t, srv, tp)

	if err := restCall(context.Background(), c); err != nil {
		t.Fatalf("rest call: %v", err)
	}
	waits := spansNamed(rec, sc.SpanGitHubExporterRateLimitWait)
	if len(waits) != 1 {
		t.Fatalf("wait spans = %d, want 1", len(waits))
	}
	w := spanAttrs(waits[0])
	if w[sc.AttrGitHubApiRateLimitResource].AsString() != "core" || w[sc.AttrGitHubExporterRateLimitReason].AsString() != "secondary" {
		t.Errorf("wait attributes = %v", waits[0].Attributes())
	}

	// The recorded sleeps return at once, so the pause is still on: a
	// healthy target shows the no-wait case.
	healthy, _ := scriptedServer(t, func(int, *http.Request, http.ResponseWriter) bool { return false })
	tp2, rec2 := newTracerProvider(t)
	c2 := newTracedClient(t, healthy, tp2)
	if err := restCall(context.Background(), c2); err != nil {
		t.Fatalf("rest call: %v", err)
	}
	if n := len(spansNamed(rec2, sc.SpanGitHubExporterRateLimitWait)); n != 0 {
		t.Errorf("wait spans without a pause = %d, want 0", n)
	}
}

// A wait cancelled by the caller ends with status Error.
func TestTrace_CancelledWait(t *testing.T) {
	srv, _ := scriptedServer(t, func(n int, _ *http.Request, w http.ResponseWriter) bool {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		return true
	})
	tp, rec := newTracerProvider(t)
	c := newTracedClient(t, srv, tp)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = restCall(ctx, c)
	for _, s := range spansNamed(rec, sc.SpanGitHubExporterRateLimitWait) {
		if s.Status().Code != codes.Error {
			t.Errorf("cancelled wait status = %v, want Error", s.Status())
		}
	}
}
