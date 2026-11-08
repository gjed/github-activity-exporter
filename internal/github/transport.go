package github

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// Attribute keys of the OTel HTTP client semantic conventions that otelhttp
// does not set itself.
const (
	attrURLTemplate = "url.template"
	attrErrorType   = "error.type"
)

// NewTransport returns the base HTTP transport for GitHub requests to the
// REST API root baseURL. With a nil MeterProvider it is http.DefaultTransport.
// Otherwise every request is recorded as http.client.request.duration and
// http.client.request.body.size (OTel HTTP client semantic conventions) on mp,
// with url.template for the routes the exporter calls and error.type for 4xx
// and 5xx responses.
//
// It goes under the Client's rate limit transport (WithBaseTransport) and into
// the authenticators' HTTP client, so durations cover one round trip, not the
// wait on a paused rate limit pool or for a token.
func NewTransport(baseURL string, mp metric.MeterProvider) (http.RoundTripper, error) {
	if mp == nil {
		return http.DefaultTransport, nil
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
	// Spans and trace propagation are #75: no tracer, no injected headers.
	return otelhttp.NewTransport(rt,
		otelhttp.WithMeterProvider(mp),
		otelhttp.WithTracerProvider(tracenoop.NewTracerProvider()),
		otelhttp.WithPropagators(propagation.NewCompositeTextMapPropagator()),
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
// each request. It sits inside otelhttp, which reads the Labeler after this
// round trip returns.
type routeTransport struct {
	inner       http.RoundTripper
	restPath    string // REST API root path, "" on github.com, "/api/v3" on GHES
	graphqlPath string
}

func (t *routeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	labeler, _ := otelhttp.ClientLabelerFromContext(req.Context())
	if tmpl := t.template(req.URL.Path); tmpl != "" {
		labeler.Add(attribute.String(attrURLTemplate, tmpl))
	}
	resp, err := t.inner.RoundTrip(req)
	// otelhttp sets error.type for a failed round trip; for HTTP clients
	// a 4xx or 5xx status is an error too, typed by the status code.
	if err == nil && resp.StatusCode >= 400 {
		labeler.Add(attribute.String(attrErrorType, strconv.Itoa(resp.StatusCode)))
	}
	return resp, err
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
