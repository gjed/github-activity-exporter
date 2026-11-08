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
		// Rate-Limited lists 11 branches plus create-all-rate-limited-prs.
		{"gjed_songbooks_100.txt", 0, map[string]int64{
			semconv.AttrRenovateDashboardSectionAwaitingSchedule: 0,
			semconv.AttrRenovateDashboardSectionRateLimited:      11,
			semconv.AttrRenovateDashboardSectionPendingApproval:  0,
			semconv.AttrRenovateDashboardSectionPending:          0,
		}},
		// Pending Approval, Awaiting Schedule and Open each end with a bulk
		// action; PR Edited (Blocked) and PR Closed (Blocked) are untracked.
		{"zextras_carbonio-mails-ui_1064.txt", 0, map[string]int64{
			semconv.AttrRenovateDashboardSectionAwaitingSchedule: 27,
			semconv.AttrRenovateDashboardSectionRateLimited:      0,
			semconv.AttrRenovateDashboardSectionPendingApproval:  38,
			semconv.AttrRenovateDashboardSectionPending:          18,
		}},
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

// TestParseDashboard_CustomHeader covers repos that override
// dependencyDashboardHeader: each remaining marker alone must be enough.
func TestParseDashboard_CustomHeader(t *testing.T) {
	for name, body := range map[string]string{
		"no-branches notice": "Custom header.\n\nThis repository currently has no open or pending branches.\n",
		"detected deps":      "Custom header.\n\n## Detected Dependencies\n",
		"branch checkbox":    "Custom header.\n\n## PR Edited (Blocked)\n\n - [ ] <!-- rebase-branch=renovate/foo -->foo\n",
	} {
		t.Run(name, func(t *testing.T) {
			got := parseDashboard(body)
			if got.parseError != 0 {
				t.Errorf("parseError = %d, want 0", got.parseError)
			}
			if !maps.Equal(got.sections, noQueue) {
				t.Errorf("sections = %v, want %v", got.sections, noQueue)
			}
		})
	}
}

// TestParseDashboard_CategorizedSection uses the layout Renovate writes when
// branches carry dependencyDashboardCategory: ### subheadings inside the ##
// section, with the bulk action under "### All".
func TestParseDashboard_CategorizedSection(t *testing.T) {
	body := `This issue lists Renovate updates and detected dependencies.

## Rate-Limited

The following updates are currently rate-limited. To force their creation now, click on a checkbox below.

### ci

 - [ ] <!-- unlimit-branch=renovate/actions-checkout-5.x -->Update actions/checkout action to v5

### docker

 - [x] <!-- unlimit-branch=renovate/alpine-3.x -->Update alpine Docker tag to v3.22

### Others

 - [ ] <!-- unlimit-branch=renovate/foo-2.x -->Update dependency foo to v2

### All

 - [ ] <!-- create-all-rate-limited-prs -->🔐 **Create all rate-limited PRs at once** 🔐

## Detected Dependencies
`
	got := parseDashboard(body)
	if got.parseError != 0 {
		t.Errorf("parseError = %d, want 0", got.parseError)
	}
	if n := got.sections[semconv.AttrRenovateDashboardSectionRateLimited]; n != 3 {
		t.Errorf("rate-limited = %d, want 3", n)
	}
}
