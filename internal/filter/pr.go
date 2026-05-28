// Package filter implements per-target filtering rules for pull requests and
// issues. Filters are applied at collection time — before metric recording —
// to avoid unnecessary processing.
package filter

import (
	"strings"

	"github.com/gjed/renovate-exporter/internal/config"
)

// PRFilter decides whether a pull request should be collected.
//
// Rules (all must pass):
//   - If IncludeLabels is non-empty, the PR must have at least one matching label.
//   - If ExcludeLabels is non-empty, the PR must not carry any of those labels.
//   - If States is non-empty, the PR state must match one of the registry values
//     ("open", "closed", "draft", "merged").
type PRFilter struct {
	cfg config.PRFilters
}

// NewPRFilter creates a PRFilter from the given config.
func NewPRFilter(cfg config.PRFilters) *PRFilter {
	return &PRFilter{cfg: cfg}
}

// PR is the minimal PR representation needed by the filter.
type PR struct {
	// State is one of "open", "closed", "merged".
	State  string
	Labels []string
}

// Match returns true if the PR passes all configured filter rules.
func (f *PRFilter) Match(pr PR) bool {
	// ── State filter ─────────────────────────────────────────────────────────
	if len(f.cfg.States) > 0 {
		if !containsCI(f.cfg.States, pr.State) {
			return false
		}
	}

	// ── Include-label filter ─────────────────────────────────────────────────
	if len(f.cfg.IncludeLabels) > 0 {
		matched := false
		for _, il := range f.cfg.IncludeLabels {
			if containsCI(pr.Labels, il) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// ── Exclude-label filter ─────────────────────────────────────────────────
	for _, el := range f.cfg.ExcludeLabels {
		if containsCI(pr.Labels, el) {
			return false
		}
	}

	return true
}

// containsCI reports whether items contains target (case-insensitive).
func containsCI(items []string, target string) bool {
	tl := strings.ToLower(target)
	for _, item := range items {
		if strings.ToLower(item) == tl {
			return true
		}
	}
	return false
}
