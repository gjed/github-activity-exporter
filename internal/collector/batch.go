package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
	ghclient "github.com/gjed/github-activity-exporter/internal/github"
)

// RawGraphQLClient sends a GraphQL query as text and returns the response
// with each error's path (satisfied by *github.Client).
type RawGraphQLClient interface {
	QueryRaw(ctx context.Context, query string, variables map[string]any) (*ghclient.RawResponse, error)
}

// BatchGraphQLClient is the client of the issue and dashboard collectors:
// typed per-repo queries and raw batched queries.
type BatchGraphQLClient interface {
	GraphQLClient
	RawGraphQLClient
}

// CollectorOption configures the issue and Dependency Dashboard collectors.
type CollectorOption func(*collectorOptions)

type collectorOptions struct {
	batchSize int
	limiter   *Limiter
}

// WithGraphQLBatchSize sets the number of repos per batched GraphQL query.
// Values below 1 mean 1. Default config.DefaultGraphQLBatchSize.
func WithGraphQLBatchSize(n int) CollectorOption {
	return func(o *collectorOptions) { o.batchSize = n }
}

// WithLimiter runs the batches, or the repos of the issue title-filter
// listing, of a cycle through l. Without it they run one at a time. Share
// one Limiter between the collectors of a target.
func WithLimiter(l *Limiter) CollectorOption {
	return func(o *collectorOptions) { o.limiter = l }
}

// applyOptions applies opts to the defaults.
func applyOptions(opts []CollectorOption) collectorOptions {
	o := collectorOptions{batchSize: config.DefaultGraphQLBatchSize}
	for _, opt := range opts {
		opt(&o)
	}
	o.batchSize = max(o.batchSize, 1)
	return o
}

// errResourceLimits is the GraphQL error type GitHub returns, on an alias or
// any field under it, when the query exceeds its per-query resource limits.
const errResourceLimits = "RESOURCE_LIMITS_EXCEEDED"

// repoBatchQuery describes one batched query: the selection read for each
// repo and the variables every alias shares.
type repoBatchQuery struct {
	// selection is the body of repository(...) { ... }.
	selection string
	// varDecls declares the shared variables, e.g. "$cursor: String".
	varDecls string
	// vars holds the shared variable values.
	vars map[string]any
}

// batchOutcome is the per-repo result of one batched query, indexed like the
// batch's repos.
type batchOutcome struct {
	// data is the repository JSON of each repo that needs no fallback.
	data []json.RawMessage
	// failed holds the GraphQL error of each repo GitHub answered with an
	// error other than a resource limit (NOT_FOUND, FORBIDDEN, ...).
	failed []error
	// retry marks repos to collect with the per-repo query: a resource limit
	// on the repo, no data for it, or a failure of the whole request.
	retry []bool
}

// runRepoBatch reads repos with one aliased query, plus rateLimit. Repo i is
// aliased "r<i>" with owner and name passed as $o<i> and $n<i>, so repo names
// never reach the query text. It keeps no state: concurrent calls are safe.
func runRepoBatch(ctx context.Context, gql RawGraphQLClient, q repoBatchQuery, repos []discovery.Repo) batchOutcome {
	out := batchOutcome{
		data:   make([]json.RawMessage, len(repos)),
		failed: make([]error, len(repos)),
		retry:  make([]bool, len(repos)),
	}
	retryAll := func() batchOutcome {
		for i := range repos {
			out.data[i], out.failed[i], out.retry[i] = nil, nil, true
		}
		return out
	}

	var b strings.Builder
	vars := make(map[string]any, len(q.vars)+2*len(repos))
	decls := make([]string, 0, 2*len(repos)+1)
	for k, v := range q.vars {
		vars[k] = v
	}
	if q.varDecls != "" {
		decls = append(decls, q.varDecls)
	}
	for i, repo := range repos {
		o, n := "o"+strconv.Itoa(i), "n"+strconv.Itoa(i)
		decls = append(decls, "$"+o+": String!", "$"+n+": String!")
		vars[o], vars[n] = repo.Owner, repo.Name
		fmt.Fprintf(&b, " r%d: repository(owner: $%s, name: $%s) %s", i, o, n, q.selection)
	}
	query := "query(" + strings.Join(decls, ", ") + ") { rateLimit { cost remaining resetAt }" + b.String() + " }"

	resp, err := gql.QueryRaw(ctx, query, vars)
	if err != nil {
		return retryAll()
	}
	for _, e := range resp.Errors {
		if len(e.Path) > 0 && e.Path[0] == "rateLimit" {
			// rateLimit is read for the pool state only; an error on it (a
			// GHES instance without rate limiting) says nothing about the repos.
			continue
		}
		i, ok := aliasIndex(e.Path, len(repos))
		if !ok {
			// Not attributable to one repo: the whole response is suspect.
			return retryAll()
		}
		switch {
		case e.Type == errResourceLimits:
			out.retry[i] = true
		case out.failed[i] == nil:
			out.failed[i] = fmt.Errorf("graphql %s: %s", e.Type, e.Message)
		}
	}
	for i := range repos {
		if out.retry[i] {
			out.failed[i] = nil
			continue
		}
		if out.failed[i] != nil {
			continue
		}
		raw := resp.Data["r"+strconv.Itoa(i)]
		if len(raw) == 0 || string(raw) == "null" {
			out.retry[i] = true
			continue
		}
		out.data[i] = raw
	}
	return out
}

// aliasIndex returns i for an error path starting with alias "r<i>", i < n.
func aliasIndex(path []any, n int) (int, bool) {
	if len(path) == 0 {
		return 0, false
	}
	s, ok := path[0].(string)
	if !ok || !strings.HasPrefix(s, "r") {
		return 0, false
	}
	i, err := strconv.Atoi(s[1:])
	if err != nil || i < 0 || i >= n || "r"+strconv.Itoa(i) != s {
		return 0, false
	}
	return i, true
}

// eachBatch runs fn on every batch of at most size repos through l, and
// returns fn's per-repo results and errors indexed like the batches. A repo
// of a batch that never ran fails with errNotStarted.
func eachBatch[T any](ctx context.Context, l *Limiter, repos []discovery.Repo, size int, fn func([]discovery.Repo) ([]T, []error)) ([][]discovery.Repo, [][]T, [][]error) {
	bs := batches(repos, size)
	results := make([][]T, len(bs))
	errs := make([][]error, len(bs))
	for i, b := range bs {
		results[i] = make([]T, len(b))
		errs[i] = make([]error, len(b))
		for j := range errs[i] {
			errs[i][j] = errNotStarted
		}
	}
	l.each(ctx, len(bs), func(i int) {
		results[i], errs[i] = fn(bs[i])
	})
	return bs, results, errs
}

// batches splits repos into consecutive slices of at most size repos.
func batches(repos []discovery.Repo, size int) [][]discovery.Repo {
	size = max(size, 1)
	var out [][]discovery.Repo
	for start := 0; start < len(repos); start += size {
		out = append(out, repos[start:min(start+size, len(repos))])
	}
	return out
}

// selectionOf returns the GraphQL selection set of struct type t exactly as
// githubv4 builds it from the same struct: each field is its graphql tag or its name
// with a lower-case first letter, followed by the selection of its type;
// pointers and slices select their element; a struct implementing
// json.Unmarshaler (time.Time) is a scalar. Building the batched selection
// from the per-repo query struct keeps both queries reading the same fields.
//
// SIMPLIFIED: lower-cases the first letter only, where githubv4 converts
// MixedCaps to lowerCamelCase (e.g. "ID" -> "id"). Every field of the
// batched structs is a single capitalised word or plain MixedCaps; a field
// such as "URL" would need a graphql tag.
func selectionOf(t reflect.Type) string {
	var b strings.Builder
	writeSelection(&b, t)
	return b.String()
}

var jsonUnmarshaler = reflect.TypeFor[json.Unmarshaler]()

func writeSelection(b *strings.Builder, t reflect.Type) {
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice:
		writeSelection(b, t.Elem())
	case reflect.Struct:
		if reflect.PointerTo(t).Implements(jsonUnmarshaler) {
			return
		}
		b.WriteString("{")
		for i := range t.NumField() {
			if i > 0 {
				b.WriteString(",")
			}
			f := t.Field(i)
			name, ok := f.Tag.Lookup("graphql")
			if !ok {
				r, n := utf8.DecodeRuneInString(f.Name)
				name = string(unicode.ToLower(r)) + f.Name[n:]
			}
			b.WriteString(name)
			writeSelection(b, f.Type)
		}
		b.WriteString("}")
	}
}
