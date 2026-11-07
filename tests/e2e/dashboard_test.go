//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	// defaultCollectionInterval is the exporter's default collection_interval
	// (internal/config), the push cadence the dashboard is sized for.
	defaultCollectionInterval = 5 * time.Minute
	// testCollectionInterval is the collection_interval the e2e runs with.
	testCollectionInterval = 5 * time.Second
	// grafanaScrapeInterval is the Grafana Prometheus data source's default
	// scrape interval, an input of $__rate_interval.
	grafanaScrapeInterval = 15 * time.Second
)

// rangeRE matches a PromQL range selector [<window>].
var rangeRE = regexp.MustCompile(`\[([^\]]+)\]`)

// dashboardJSON holds the parts of a Grafana dashboard the runner reads.
type dashboardJSON struct {
	Templating struct {
		List []dashVariable `json:"list"`
	} `json:"templating"`
	Panels []dashPanel `json:"panels"`
}

type dashVariable struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Definition string `json:"definition"`
}

type dashPanel struct {
	Title    string       `json:"title"`
	Interval string       `json:"interval"` // Min interval
	Panels   []dashPanel  `json:"panels"`   // rows nest panels
	Targets  []dashTarget `json:"targets"`
}

type dashTarget struct {
	RefID string `json:"refId"`
	Expr  string `json:"expr"`
}

// panelTarget is one query of the dashboard, with its panel title.
type panelTarget struct {
	panel, refID, expr, interval string
}

var (
	// labelValuesRE matches a query variable definition label_values(<selector>, <label>).
	labelValuesRE = regexp.MustCompile(`^label_values\((.*),\s*"?([^",()]+)"?\)$`)
	// varRefRE matches $name, ${name} and ${name:format}.
	varRefRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::[^}]*)?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)
)

// rateInterval returns the window Grafana gives $__rate_interval for a panel
// with the given Min interval, max($__interval + scrape, 4 × scrape) with
// $__interval at its floor, scaled from the default collection_interval to
// the e2e's. The e2e window then holds as many pushes as a production one.
// It fails unless the Min interval spans two default collection intervals.
func rateInterval(minInterval string) (string, error) {
	d, err := time.ParseDuration(minInterval)
	if err != nil {
		return "", fmt.Errorf("panel Min interval %q: %v", minInterval, err)
	}
	if d < 2*defaultCollectionInterval {
		return "", fmt.Errorf("panel Min interval %s is shorter than 2 × collection_interval (%s): rate() gets fewer than 2 samples", d, defaultCollectionInterval)
	}
	prod := max(d+grafanaScrapeInterval, 4*grafanaScrapeInterval)
	return fmt.Sprintf("%dms", (prod / (defaultCollectionInterval / testCollectionInterval)).Milliseconds()), nil
}

// runDashboardQueries runs every panel target of dashboards/github-activity.json
// against Prometheus, after resolving the dashboard variables.
func runDashboardQueries(t *testing.T, promURL string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(), "dashboards", "github-activity.json"))
	if err != nil {
		t.Fatalf("read dashboard: %v", err)
	}
	var d dashboardJSON
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("parse dashboard: %v", err)
	}

	vars := make(map[string]string)
	for _, v := range d.Templating.List {
		if v.Type != "query" {
			continue
		}
		if err := resolveVariable(promURL, v, vars); err != nil {
			t.Errorf("variable %q: %v", v.Name, err)
		}
	}

	targets := collectTargets(d.Panels)
	if len(targets) == 0 {
		t.Fatal("dashboard has no panel targets")
	}
	for _, pt := range targets {
		t.Run(fmt.Sprintf("%s/%s", pt.panel, pt.refID), func(t *testing.T) {
			checkPanelTarget(t, promURL, pt, vars)
		})
	}
}

// resolveVariable runs a query variable's label_values() against Prometheus,
// after substituting the variables resolved before it, and stores the values
// as one regex: each value regexp-escaped, joined with "|".
func resolveVariable(promURL string, v dashVariable, vars map[string]string) error {
	m := labelValuesRE.FindStringSubmatch(strings.TrimSpace(v.Definition))
	if m == nil {
		return fmt.Errorf("definition %q is not label_values(<selector>, <label>)", v.Definition)
	}
	selector, err := expandVars(m[1], vars)
	if err != nil {
		return fmt.Errorf("selector %q: %w", m[1], err)
	}
	values, err := labelValues(promURL, m[2], selector)
	if err != nil {
		return fmt.Errorf("label_values(%s, %s): %w", selector, m[2], err)
	}
	if len(values) == 0 {
		return fmt.Errorf("label_values(%s, %s) returned no values", selector, m[2])
	}
	escaped := make([]string, 0, len(values))
	for _, val := range values {
		escaped = append(escaped, regexp.QuoteMeta(val))
	}
	vars[v.Name] = strings.Join(escaped, "|")
	return nil
}

// labelValues calls /api/v1/label/<label>/values with match[]=<selector>.
func labelValues(promURL, label, selector string) ([]string, error) {
	q := url.Values{}
	if selector != "" {
		q.Set("match[]", selector)
	}
	// Prometheus 3.16 takes a dotted label name in the path as is; the U__-escaped
	// form (U__exporter_2e_target) returns the same values.
	u := promURL + "/api/v1/label/" + url.PathEscape(label) + "/values?" + q.Encode()
	body, err := promGet(u)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
		Error  string   `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.Status != "success" {
		return nil, fmt.Errorf("prometheus: %s", resp.Error)
	}
	return resp.Data, nil
}

// expandVars replaces $name, ${name} and ${name:format} with the variable's
// value. An unknown name is an error.
func expandVars(expr string, vars map[string]string) (string, error) {
	var missing []string
	out := varRefRE.ReplaceAllStringFunc(expr, func(ref string) string {
		m := varRefRE.FindStringSubmatch(ref)
		name := m[1]
		if name == "" {
			name = m[2]
		}
		if v, ok := vars[name]; ok {
			return v
		}
		missing = append(missing, name)
		return ref
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("unresolved variable(s) %v", missing)
	}
	return out, nil
}

// collectTargets walks panels recursively (rows nest panels) and returns every target.
func collectTargets(panels []dashPanel) []panelTarget {
	var out []panelTarget
	for _, p := range panels {
		for _, tg := range p.Targets {
			out = append(out, panelTarget{panel: p.Title, refID: tg.RefID, expr: tg.Expr, interval: p.Interval})
		}
		out = append(out, collectTargets(p.Panels)...)
	}
	return out
}

// checkPanelTarget runs one target as an instant query and fails the subtest
// on a query error, an empty result, or only NaN values. The message names
// the panel, refId, expanded expression and Prometheus response.
func checkPanelTarget(t *testing.T, promURL string, pt panelTarget, vars map[string]string) {
	t.Helper()
	for _, r := range rangeRE.FindAllStringSubmatch(pt.expr, -1) {
		if r[1] != "$__rate_interval" {
			t.Errorf("panel %q refId %s: range [%s] is not [$__rate_interval]\nexpr: %s", pt.panel, pt.refID, r[1], pt.expr)
			return
		}
	}
	window, err := rateInterval(pt.interval)
	if err != nil {
		t.Errorf("panel %q refId %s: %v", pt.panel, pt.refID, err)
		return
	}
	withTime := map[string]string{"__rate_interval": window, "__interval": window}
	for k, v := range vars {
		withTime[k] = v
	}
	expr, err := expandVars(pt.expr, withTime)
	if err != nil {
		t.Errorf("panel %q refId %s: %v\nexpr: %s", pt.panel, pt.refID, err, pt.expr)
		return
	}
	body, err := promGet(promURL + "/api/v1/query?" + url.Values{"query": {expr}}.Encode())
	if err == nil {
		var n int
		n, err = checkResult(body)
		if err == nil {
			t.Logf("OK panel %q refId %s: %d series", pt.panel, pt.refID, n)
			return
		}
	}
	t.Errorf("panel %q refId %s: %v\nexpr: %s\nresponse: %s", pt.panel, pt.refID, err, expr, truncate(body, 800))
}

// checkResult decodes an instant query response and returns its series count.
// It fails on an error status, a result with no series, or only NaN values.
func checkResult(body []byte) (int, error) {
	var resp struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			ResultType string          `json:"resultType"`
			Result     json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}
	if resp.Status != "success" {
		return 0, fmt.Errorf("prometheus error: %s", resp.Error)
	}

	// One sample string per series: the value for vector and scalar, the last value for matrix.
	var values []string
	switch resp.Data.ResultType {
	case "vector", "matrix":
		var series []struct {
			Value  []any   `json:"value"`
			Values [][]any `json:"values"`
		}
		if err := json.Unmarshal(resp.Data.Result, &series); err != nil {
			return 0, fmt.Errorf("decode %s result: %w", resp.Data.ResultType, err)
		}
		for _, s := range series {
			if s.Value != nil {
				values = append(values, fmt.Sprint(s.Value[1]))
			} else if len(s.Values) > 0 {
				values = append(values, fmt.Sprint(s.Values[len(s.Values)-1][1]))
			}
		}
	case "scalar":
		var v []any
		if err := json.Unmarshal(resp.Data.Result, &v); err != nil || len(v) != 2 {
			return 0, fmt.Errorf("decode scalar result: %v", err)
		}
		values = []string{fmt.Sprint(v[1])}
	default:
		return 0, fmt.Errorf("unexpected resultType %q", resp.Data.ResultType)
	}

	if len(values) == 0 {
		return 0, fmt.Errorf("empty result: 0 series")
	}
	allNaN := true
	for _, v := range values {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || !math.IsNaN(f) {
			allNaN = false
			break
		}
	}
	if allNaN {
		return 0, fmt.Errorf("all %d series are NaN", len(values))
	}
	return len(values), nil
}

// promGet returns the body of a GET to a Prometheus API URL. A non-200 status
// is an error, and the body is still returned so the caller can report it.
func promGet(u string) ([]byte, error) {
	resp, err := http.Get(u)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return body, err
	}
	if resp.StatusCode != http.StatusOK {
		return body, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "...(truncated)"
}
