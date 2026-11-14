package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Attribute keys of the OTel HTTP client and GraphQL semantic conventions
// that otelhttp does not set itself.
const (
	attrURLTemplate          = "url.template"
	attrErrorType            = "error.type"
	attrHTTPResendCount      = "http.request.resend_count"
	attrGraphQLOperationType = "graphql.operation.type"
	attrGraphQLOperationName = "graphql.operation.name"
)

// NewTransport returns the base HTTP transport for GitHub requests to the
// REST API root baseURL. With a nil MeterProvider and a nil TracerProvider it
// is http.DefaultTransport.
//
// With mp, every request is recorded as http.client.request.duration and
// http.client.request.body.size (OTel HTTP client semantic conventions), with
// url.template for the routes the exporter calls and error.type for 4xx and
// 5xx responses.
//
// With tp, every request is an HTTP client span named "{method} {url.template}"
// ("{method}" for any other path), with url.template, http.request.resend_count
// on a rate limit retry, and graphql.operation.type and graphql.operation.name
// on GraphQL requests. No trace context is sent to GitHub.
//
// It goes under the Client's rate limit transport (WithBaseTransport) and into
// the authenticators' HTTP client, so durations and spans cover one round
// trip, not the wait on a paused rate limit pool or for a token.
func NewTransport(baseURL string, mp metric.MeterProvider, tp trace.TracerProvider) (http.RoundTripper, error) {
	if mp == nil && tp == nil {
		return http.DefaultTransport, nil
	}
	if mp == nil {
		mp = metricnoop.NewMeterProvider()
	}
	if tp == nil {
		tp = tracenoop.NewTracerProvider()
	}
	if baseURL == "" {
		baseURL = defaultGitHubAPIURL
	}
	restU, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing base URL %q: %w", baseURL, err)
	}
	gqlU, err := url.Parse(graphqlURL(baseURL))
	if err != nil {
		return nil, fmt.Errorf("parsing GraphQL URL of %q: %w", baseURL, err)
	}
	rt := &routeTransport{
		inner:       http.DefaultTransport,
		restPath:    strings.TrimSuffix(restU.Path, "/"),
		graphqlPath: gqlU.Path,
	}
	// No propagator: GitHub takes no part in the trace, and a traceparent
	// header would hand trace IDs to a third party.
	return otelhttp.NewTransport(rt,
		otelhttp.WithMeterProvider(mp),
		otelhttp.WithTracerProvider(tp),
		otelhttp.WithPropagators(propagation.NewCompositeTextMapPropagator()),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			if tmpl := rt.template(r.URL.Path); tmpl != "" {
				return r.Method + " " + tmpl
			}
			return r.Method
		}),
	), nil
}

// restRoutes are the REST routes the exporter calls, relative to the API root.
// Each entry is split on "/"; a "{...}" segment matches any one segment.
// A request to any other path gets no url.template, so the attribute stays
// bounded and never carries an org, repo or installation identifier.
var restRoutes = [][]string{
	strings.Split("/user", "/"), // PAT credential check
	strings.Split("/app", "/"),  // App credential check
	strings.Split("/app/installations/{installation_id}/access_tokens", "/"), // App token exchange
	strings.Split("/orgs/{org}/repos", "/"),                                  // discovery
}

// routeTransport adds url.template and error.type to the otelhttp Labeler of
// each request, and url.template, the resend count and the GraphQL operation
// to its span when the span is recording. It sits inside otelhttp, which
// reads the Labeler after this round trip returns.
type routeTransport struct {
	inner       http.RoundTripper
	restPath    string // REST API root path, "" on github.com, "/api/v3" on GHES
	graphqlPath string
}

func (t *routeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	labeler, _ := otelhttp.ClientLabelerFromContext(req.Context())
	tmpl := t.template(req.URL.Path)
	if tmpl != "" {
		labeler.Add(attribute.String(attrURLTemplate, tmpl))
	}
	if span := trace.SpanFromContext(req.Context()); span.IsRecording() {
		if tmpl != "" {
			span.SetAttributes(attribute.String(attrURLTemplate, tmpl))
		}
		if n := resendCount(req.Context()); n > 0 {
			span.SetAttributes(attribute.Int(attrHTTPResendCount, n))
		}
		if req.URL.Path == t.graphqlPath {
			span.SetAttributes(graphqlOperation(req)...)
		}
	}
	resp, err := t.inner.RoundTrip(req)
	// otelhttp sets error.type for a failed round trip; for HTTP clients
	// a 4xx or 5xx status is an error too, typed by the status code.
	if err == nil && resp.StatusCode >= 400 {
		labeler.Add(attribute.String(attrErrorType, strconv.Itoa(resp.StatusCode)))
	}
	return resp, err
}

// graphqlOperation returns graphql.operation.type and, for a named operation,
// graphql.operation.name of a GraphQL request, read from a copy of its body.
// It returns nothing when the body cannot be read again or holds no
// recognisable operation.
func graphqlOperation(req *http.Request) []attribute.KeyValue {
	if req.GetBody == nil {
		return nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil
	}
	defer func() { _ = body.Close() }()
	var payload struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		return nil
	}
	typ, name := parseOperation(payload.Query)
	if typ == "" {
		return nil
	}
	attrs := []attribute.KeyValue{attribute.String(attrGraphQLOperationType, typ)}
	if name != "" {
		attrs = append(attrs, attribute.String(attrGraphQLOperationName, name))
	}
	return attrs
}

// parseOperation returns the operation type and name of the first operation
// of a GraphQL document: "query" for the "{ ... }" shorthand, and an empty
// name for an anonymous operation.
//
// SIMPLIFIED: reads the first definition only, and does not skip comments
// before it. The exporter sends single-operation documents without comments.
func parseOperation(doc string) (typ, name string) {
	const ignored = " \t\r\n,\ufeff"
	doc = strings.TrimLeft(doc, ignored)
	if strings.HasPrefix(doc, "{") {
		return "query", ""
	}
	for _, kw := range []string{"query", "mutation", "subscription"} {
		rest, ok := strings.CutPrefix(doc, kw)
		if !ok || (rest != "" && isNameChar(rest[0])) {
			continue
		}
		rest = strings.TrimLeft(rest, ignored)
		i := 0
		for i < len(rest) && isNameChar(rest[i]) && (i > 0 || rest[i] < '0' || rest[i] > '9') {
			i++
		}
		return kw, rest[:i]
	}
	return "", ""
}

func isNameChar(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// template returns the url.template of path, or "" for an unknown route.
func (t *routeTransport) template(path string) string {
	if path == t.graphqlPath {
		return path
	}
	rel, ok := strings.CutPrefix(path, t.restPath)
	if !ok {
		return ""
	}
	segs := strings.Split(rel, "/")
	for _, route := range restRoutes {
		if matchRoute(route, segs) {
			return t.restPath + strings.Join(route, "/")
		}
	}
	return ""
}

func matchRoute(route, segs []string) bool {
	if len(route) != len(segs) {
		return false
	}
	for i, r := range route {
		if strings.HasPrefix(r, "{") {
			if segs[i] == "" {
				return false
			}
			continue
		}
		if r != segs[i] {
			return false
		}
	}
	return true
}
