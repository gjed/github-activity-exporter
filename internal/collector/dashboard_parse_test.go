package collector

import (
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/gjed/github-activity-exporter/internal/semconv"
)

// noQueue is the expected section map of a dashboard with nothing queued in
// any tracked section.
var noQueue = map[string]int64{
	semconv.AttrRenovateDashboardSectionAwaitingSchedule: 0,
	semconv.AttrRenovateDashboardSectionRateLimited:      0,
	semconv.AttrRenovateDashboardSectionPendingApproval:  0,
	semconv.AttrRenovateDashboardSectionPending:          0,
}

// TestParseDashboard_RealBodies parses Dependency Dashboard bodies fetched
// verbatim from GitHub (testdata/dashboards/<owner>_<repo>_<issue>.txt).
func TestParseDashboard_RealBodies(t *testing.T) {
	tests := []struct {
		file       string
		parseError int64
		sections   map[string]int64
	}{
		// "no open or pending branches" + Detected Dependencies only.
		{"gjed_github-configs-public_16.txt", 0, noQueue},
		{"gjed_renovate-config_3.txt", 0, noQueue},
		{"gjed_siemens-converter_19.txt", 0, noQueue},
		{"gjed_sinapsi-converter_18.txt", 0, noQueue},
		{"gjed_terraform-github-config-as-yaml_99.txt", 0, noQueue},
		// Only an untracked section: PR Edited (Blocked).
		{"zextras_service-discover-template_10.txt", 0, noQueue},
	}

	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("testdata", "dashboards", tc.file))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			got := parseDashboard(string(body))
			if got.parseError != tc.parseError {
				t.Errorf("parseError = %d, want %d", got.parseError, tc.parseError)
			}
			if !maps.Equal(got.sections, tc.sections) {
				t.Errorf("sections = %v, want %v", got.sections, tc.sections)
			}
		})
	}
}

func TestParseDashboard_NotADashboard(t *testing.T) {
	got := parseDashboard("This issue has no recognised sections.\n\n## Awaiting Schedule\n- [ ] foo\n")
	if got.parseError != 1 {
		t.Errorf("parseError = %d, want 1", got.parseError)
	}
	if len(got.sections) != 0 {
		t.Errorf("sections = %v, want none", got.sections)
	}
}
