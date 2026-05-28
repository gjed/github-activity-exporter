package filter_test

import (
	"testing"

	"github.com/gjed/renovate-exporter/internal/config"
	"github.com/gjed/renovate-exporter/internal/filter"
)

func TestPRFilter_Match(t *testing.T) {
	tests := []struct {
		name   string
		cfg    config.PRFilters
		pr     filter.PR
		wantOK bool
	}{
		{
			name:   "empty config passes everything",
			cfg:    config.PRFilters{},
			pr:     filter.PR{State: "open", Labels: []string{"renovate"}},
			wantOK: true,
		},
		{
			name:   "state match passes",
			cfg:    config.PRFilters{States: []string{"open"}},
			pr:     filter.PR{State: "open"},
			wantOK: true,
		},
		{
			name:   "state mismatch fails",
			cfg:    config.PRFilters{States: []string{"open"}},
			pr:     filter.PR{State: "closed"},
			wantOK: false,
		},
		{
			name:   "state merged passes when included",
			cfg:    config.PRFilters{States: []string{"open", "merged"}},
			pr:     filter.PR{State: "merged"},
			wantOK: true,
		},
		{
			name:   "include label match passes",
			cfg:    config.PRFilters{IncludeLabels: []string{"renovate"}},
			pr:     filter.PR{State: "open", Labels: []string{"renovate", "dependencies"}},
			wantOK: true,
		},
		{
			name:   "include label no match fails",
			cfg:    config.PRFilters{IncludeLabels: []string{"renovate"}},
			pr:     filter.PR{State: "open", Labels: []string{"bug"}},
			wantOK: false,
		},
		{
			name:   "include label case insensitive",
			cfg:    config.PRFilters{IncludeLabels: []string{"Renovate"}},
			pr:     filter.PR{State: "open", Labels: []string{"renovate"}},
			wantOK: true,
		},
		{
			name:   "exclude label match fails",
			cfg:    config.PRFilters{ExcludeLabels: []string{"skip-ci"}},
			pr:     filter.PR{State: "open", Labels: []string{"renovate", "skip-ci"}},
			wantOK: false,
		},
		{
			name:   "exclude label not present passes",
			cfg:    config.PRFilters{ExcludeLabels: []string{"skip-ci"}},
			pr:     filter.PR{State: "open", Labels: []string{"renovate"}},
			wantOK: true,
		},
		{
			name: "include and exclude combined: in include but also excluded",
			cfg: config.PRFilters{
				IncludeLabels: []string{"renovate"},
				ExcludeLabels: []string{"do-not-merge"},
			},
			pr:     filter.PR{State: "open", Labels: []string{"renovate", "do-not-merge"}},
			wantOK: false,
		},
		{
			name: "include and exclude combined: in include and not excluded",
			cfg: config.PRFilters{
				IncludeLabels: []string{"renovate"},
				ExcludeLabels: []string{"do-not-merge"},
			},
			pr:     filter.PR{State: "open", Labels: []string{"renovate"}},
			wantOK: true,
		},
		{
			name: "all three filters combined: passes all",
			cfg: config.PRFilters{
				States:        []string{"open"},
				IncludeLabels: []string{"renovate"},
				ExcludeLabels: []string{"skip-ci"},
			},
			pr:     filter.PR{State: "open", Labels: []string{"renovate"}},
			wantOK: true,
		},
		{
			name: "all three filters combined: fails state",
			cfg: config.PRFilters{
				States:        []string{"open"},
				IncludeLabels: []string{"renovate"},
				ExcludeLabels: []string{"skip-ci"},
			},
			pr:     filter.PR{State: "merged", Labels: []string{"renovate"}},
			wantOK: false,
		},
		{
			name:   "no labels and no include filter passes",
			cfg:    config.PRFilters{},
			pr:     filter.PR{State: "open", Labels: nil},
			wantOK: true,
		},
		{
			name:   "include filter with no labels on PR fails",
			cfg:    config.PRFilters{IncludeLabels: []string{"renovate"}},
			pr:     filter.PR{State: "open", Labels: nil},
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := filter.NewPRFilter(tc.cfg)
			got := f.Match(tc.pr)
			if got != tc.wantOK {
				t.Errorf("Match() = %v, want %v", got, tc.wantOK)
			}
		})
	}
}
