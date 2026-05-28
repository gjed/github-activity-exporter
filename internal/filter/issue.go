package filter

import (
	"github.com/gjed/renovate-exporter/internal/config"
)

// IssueFilter decides whether an issue should be collected.
//
// Rules (all must pass):
//   - If ExcludeTitleRegexps is non-empty, the issue title must NOT match any
//     of the compiled patterns (patterns are pre-compiled at config load time
//     by config.compileFilters).
//   - If States is non-empty, the issue state must match one of the values
//     ("open", "closed").
type IssueFilter struct {
	cfg config.IssueFilters
}

// NewIssueFilter creates an IssueFilter from the given config.
// The ExcludeTitleRegexps field must already be populated (by config.Load).
func NewIssueFilter(cfg config.IssueFilters) *IssueFilter {
	return &IssueFilter{cfg: cfg}
}

// Issue is the minimal issue representation needed by the filter.
type Issue struct {
	// State is one of "open", "closed".
	State  string
	Title  string
	Labels []string
}

// Match returns true if the issue passes all configured filter rules.
func (f *IssueFilter) Match(issue Issue) bool {
	// ── State filter ─────────────────────────────────────────────────────────
	if len(f.cfg.States) > 0 {
		if !containsCI(f.cfg.States, issue.State) {
			return false
		}
	}

	// ── Title exclusion patterns ─────────────────────────────────────────────
	for _, re := range f.cfg.ExcludeTitleRegexps {
		if re.MatchString(issue.Title) {
			return false
		}
	}

	return true
}
