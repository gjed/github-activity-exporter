package filter_test

import (
	"regexp"
	"testing"

	"github.com/gjed/renovate-exporter/internal/config"
	"github.com/gjed/renovate-exporter/internal/filter"
)

func TestIssueFilter_Match(t *testing.T) {
	tests := []struct {
		name   string
		cfg    config.IssueFilters
		issue  filter.Issue
		wantOK bool
	}{
		{
			name:   "empty config passes everything",
			cfg:    config.IssueFilters{},
			issue:  filter.Issue{State: "open", Title: "Dependency Dashboard"},
			wantOK: true,
		},
		{
			name:   "state match passes",
			cfg:    config.IssueFilters{States: []string{"open"}},
			issue:  filter.Issue{State: "open", Title: "some issue"},
			wantOK: true,
		},
		{
			name:   "state mismatch fails",
			cfg:    config.IssueFilters{States: []string{"open"}},
			issue:  filter.Issue{State: "closed", Title: "some issue"},
			wantOK: false,
		},
		{
			name: "title pattern matches — excluded",
			cfg: config.IssueFilters{
				ExcludeTitleRegexps: []*regexp.Regexp{regexp.MustCompile(`(?i)dependency dashboard`)},
			},
			issue:  filter.Issue{State: "open", Title: "Dependency Dashboard"},
			wantOK: false,
		},
		{
			name: "title pattern does not match — passes",
			cfg: config.IssueFilters{
				ExcludeTitleRegexps: []*regexp.Regexp{regexp.MustCompile(`(?i)dependency dashboard`)},
			},
			issue:  filter.Issue{State: "open", Title: "Bug: something broke"},
			wantOK: true,
		},
		{
			name: "multiple patterns — any match excludes",
			cfg: config.IssueFilters{
				ExcludeTitleRegexps: []*regexp.Regexp{
					regexp.MustCompile(`(?i)dependency dashboard`),
					regexp.MustCompile(`(?i)renovate bot`),
				},
			},
			issue:  filter.Issue{State: "open", Title: "Renovate Bot report"},
			wantOK: false,
		},
		{
			name: "state and title combined: both pass",
			cfg: config.IssueFilters{
				States:              []string{"open"},
				ExcludeTitleRegexps: []*regexp.Regexp{regexp.MustCompile(`(?i)dependency dashboard`)},
			},
			issue:  filter.Issue{State: "open", Title: "Real bug report"},
			wantOK: true,
		},
		{
			name: "state and title combined: state fails",
			cfg: config.IssueFilters{
				States:              []string{"open"},
				ExcludeTitleRegexps: []*regexp.Regexp{regexp.MustCompile(`(?i)dependency dashboard`)},
			},
			issue:  filter.Issue{State: "closed", Title: "Real bug report"},
			wantOK: false,
		},
		{
			name: "state and title combined: title excluded",
			cfg: config.IssueFilters{
				States:              []string{"open"},
				ExcludeTitleRegexps: []*regexp.Regexp{regexp.MustCompile(`(?i)dependency dashboard`)},
			},
			issue:  filter.Issue{State: "open", Title: "Dependency Dashboard"},
			wantOK: false,
		},
		{
			name: "partial regex match excludes",
			cfg: config.IssueFilters{
				ExcludeTitleRegexps: []*regexp.Regexp{regexp.MustCompile(`^Update`)},
			},
			issue:  filter.Issue{State: "open", Title: "Update dependency foo to v2"},
			wantOK: false,
		},
		{
			name: "partial regex no match passes",
			cfg: config.IssueFilters{
				ExcludeTitleRegexps: []*regexp.Regexp{regexp.MustCompile(`^Update`)},
			},
			issue:  filter.Issue{State: "open", Title: "Fix: update handling broken"},
			wantOK: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := filter.NewIssueFilter(tc.cfg)
			got := f.Match(tc.issue)
			if got != tc.wantOK {
				t.Errorf("Match() = %v, want %v", got, tc.wantOK)
			}
		})
	}
}
