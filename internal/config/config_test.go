package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/gjed/github-activity-exporter/internal/config"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		envVars map[string]string
		check   func(t *testing.T, cfg *config.Config)
		wantErr bool
		// errContains, when set, must be a substring of the returned error.
		errContains string
	}{
		{
			name: "pat token from config",
			file: "testdata/pat_token.yaml",
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				if len(cfg.Targets) != 1 {
					t.Fatalf("expected 1 target, got %d", len(cfg.Targets))
				}
				if cfg.Targets[0].Auth.Token != "ghp_testtoken123" {
					t.Errorf("expected token ghp_testtoken123, got %q", cfg.Targets[0].Auth.Token)
				}
			},
		},
		{
			name: "export defaults",
			file: "testdata/pat_token.yaml",
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				if cfg.CollectionInterval != 5*time.Minute {
					t.Errorf("expected collection_interval 5m, got %s", cfg.CollectionInterval)
				}
				otlp := cfg.Export.OTLP
				if otlp.Endpoint != "http://localhost:4318" {
					t.Errorf("expected endpoint http://localhost:4318, got %q", otlp.Endpoint)
				}
				if otlp.Protocol != "http" {
					t.Errorf("expected protocol http, got %q", otlp.Protocol)
				}
				if len(otlp.Headers) != 0 || otlp.TLS.Insecure {
					t.Errorf("expected no headers and secure TLS, got %+v", otlp)
				}
				prom := cfg.Export.Prometheus
				if prom.Enabled || prom.ListenAddress != ":9090" {
					t.Errorf("expected prometheus disabled on :9090, got %+v", prom)
				}
			},
		},
		{
			name: "pr author and branch filters off by default",
			file: "testdata/pat_token.yaml",
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				prs := cfg.Targets[0].Filters.PRs
				if len(prs.Authors) != 0 || prs.HeadBranchPrefix != "" {
					t.Errorf("expected no author/branch filters, got %+v", prs)
				}
			},
		},
		{
			name: "pr author and branch filters",
			file: "testdata/pr_filters.yaml",
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				prs := cfg.Targets[0].Filters.PRs
				if len(prs.Authors) != 2 || prs.Authors[0] != "renovate[bot]" || prs.Authors[1] != "renovate-bot" {
					t.Errorf("authors = %v, want [renovate[bot] renovate-bot]", prs.Authors)
				}
				if prs.HeadBranchPrefix != "renovate/" {
					t.Errorf("head_branch_prefix = %q, want renovate/", prs.HeadBranchPrefix)
				}
			},
		},
		{
			name: "renovate dashboard disabled by default",
			file: "testdata/pat_token.yaml",
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				if cfg.Targets[0].RenovateDashboard.Enabled {
					t.Error("expected renovate_dashboard disabled by default")
				}
			},
		},
		{
			name: "renovate dashboard enabled",
			file: "testdata/renovate_dashboard.yaml",
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				if !cfg.Targets[0].RenovateDashboard.Enabled {
					t.Error("expected renovate_dashboard enabled")
				}
			},
		},
		{
			name: "full export section",
			file: "testdata/export_full.yaml",
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				if cfg.CollectionInterval != 15*time.Minute {
					t.Errorf("expected collection_interval 15m, got %s", cfg.CollectionInterval)
				}
				otlp := cfg.Export.OTLP
				if otlp.Endpoint != "otel-collector:4317" || otlp.Protocol != "grpc" || !otlp.TLS.Insecure {
					t.Errorf("unexpected otlp config: %+v", otlp)
				}
				// The config loader lower-cases map keys.
				want := map[string]string{"authorization": "Bearer secret", "x-scope-orgid": "tenant-a"}
				if len(otlp.Headers) != len(want) {
					t.Fatalf("expected %d headers, got %v", len(want), otlp.Headers)
				}
				for k, v := range want {
					if otlp.Headers[k] != v {
						t.Errorf("header %q: expected %q, got %q", k, v, otlp.Headers[k])
					}
				}
				prom := cfg.Export.Prometheus
				if !prom.Enabled || prom.ListenAddress != ":9464" {
					t.Errorf("expected prometheus enabled on :9464, got %+v", prom)
				}
			},
		},
		{
			name: "health defaults",
			file: "testdata/pat_token.yaml",
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				h := cfg.Health
				if h.ListenAddress != ":10062" {
					t.Errorf("expected health.listen_address :10062, got %q", h.ListenAddress)
				}
				// 2 × 5m + 1h
				if h.StallTimeout != 70*time.Minute {
					t.Errorf("expected health.stall_timeout 1h10m, got %s", h.StallTimeout)
				}
				if h.UnauthorizedCycles != 3 {
					t.Errorf("expected health.unauthorized_cycles 3, got %d", h.UnauthorizedCycles)
				}
			},
		},
		{
			name: "custom health section",
			file: "testdata/health_custom.yaml",
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				want := config.Health{ListenAddress: ":8081", StallTimeout: 30 * time.Minute, UnauthorizedCycles: 1}
				if cfg.Health != want {
					t.Errorf("expected %+v, got %+v", want, cfg.Health)
				}
			},
		},
		{
			name:    "health stall timeout from env var without a file key",
			file:    "testdata/pat_token.yaml",
			envVars: map[string]string{"GITHUB_ACTIVITY_EXPORTER_HEALTH_STALL_TIMEOUT": "3h"},
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				if cfg.Health.StallTimeout != 3*time.Hour {
					t.Errorf("expected health.stall_timeout 3h from env, got %s", cfg.Health.StallTimeout)
				}
			},
		},
		{
			name:        "health address equal to enabled bridge address is fatal",
			file:        "testdata/health_addr_clash.yaml",
			wantErr:     true,
			errContains: "health.listen_address and export.prometheus.listen_address",
		},
		{
			name:        "negative health stall timeout is fatal",
			file:        "testdata/health_bad_stall.yaml",
			wantErr:     true,
			errContains: "health.stall_timeout",
		},
		{
			name:        "health stall timeout not longer than collection interval is fatal",
			file:        "testdata/health_short_stall.yaml",
			wantErr:     true,
			errContains: "health.stall_timeout must be longer than collection_interval",
		},
		{
			name:        "health unauthorized cycles below 1 is fatal",
			file:        "testdata/health_bad_cycles.yaml",
			wantErr:     true,
			errContains: "health.unauthorized_cycles",
		},
		{
			name:    "missing otlp endpoint is fatal",
			file:    "testdata/no_endpoint.yaml",
			wantErr: true,
		},
		{
			name:    "http endpoint without scheme is fatal",
			file:    "testdata/bad_endpoint_http.yaml",
			wantErr: true,
		},
		{
			name:    "grpc endpoint with scheme is fatal",
			file:    "testdata/bad_endpoint_grpc.yaml",
			wantErr: true,
		},
		{
			name:    "unknown otlp protocol is fatal",
			file:    "testdata/bad_protocol.yaml",
			wantErr: true,
		},
		{
			name:    "non-positive collection interval is fatal",
			file:    "testdata/bad_interval.yaml",
			wantErr: true,
		},
		{
			name:    "pat token from env var",
			file:    "testdata/pat_env.yaml",
			envVars: map[string]string{"TEST_GITHUB_TOKEN": "ghp_from_env"},
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				if cfg.Targets[0].Auth.Token != "ghp_from_env" {
					t.Errorf("expected token from env, got %q", cfg.Targets[0].Auth.Token)
				}
			},
		},
		{
			name:    "pat env var missing is fatal",
			file:    "testdata/pat_env.yaml",
			wantErr: true,
		},
		{
			name: "app auth with private key path",
			file: "testdata/app_auth.yaml",
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				app := cfg.Targets[0].Auth.App
				if app.AppID != 12345 {
					t.Errorf("expected app_id 12345, got %d", app.AppID)
				}
				if app.InstallationID != 67890 {
					t.Errorf("expected installation_id 67890, got %d", app.InstallationID)
				}
				if app.PrivateKeyPath != "/tmp/key.pem" {
					t.Errorf("expected private_key_path, got %q", app.PrivateKeyPath)
				}
			},
		},
		{
			name:    "app auth with private key env",
			file:    "testdata/app_key_env.yaml",
			envVars: map[string]string{"TEST_GITHUB_APP_KEY": "base64pemcontent=="},
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				app := cfg.Targets[0].Auth.App
				// PrivateKeyEnv retains the original env var name.
				if app.PrivateKeyEnv != "TEST_GITHUB_APP_KEY" {
					t.Errorf("expected PrivateKeyEnv to retain env var name, got %q", app.PrivateKeyEnv)
				}
				// PrivateKeyValue holds the resolved base64-encoded PEM.
				if app.PrivateKeyValue != "base64pemcontent==" {
					t.Errorf("expected PrivateKeyValue to hold resolved key, got %q", app.PrivateKeyValue)
				}
			},
		},
		{
			name:    "app key env missing is fatal",
			file:    "testdata/app_key_env.yaml",
			wantErr: true,
		},
		{
			name:    "both pat and app is error",
			file:    "testdata/both_auth.yaml",
			wantErr: true,
		},
		{
			name:        "empty auth without GITHUB_TOKEN is fatal",
			file:        "testdata/no_auth.yaml",
			envVars:     map[string]string{"GITHUB_TOKEN": ""},
			wantErr:     true,
			errContains: `target "myorg": no auth configured and GITHUB_TOKEN is not set`,
		},
		{
			name:    "empty auth falls back to GITHUB_TOKEN",
			file:    "testdata/no_auth.yaml",
			envVars: map[string]string{"GITHUB_TOKEN": "ghp_fallback"},
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				if cfg.Targets[0].Auth.Token != "ghp_fallback" {
					t.Errorf("expected token from GITHUB_TOKEN, got %q", cfg.Targets[0].Auth.Token)
				}
			},
		},
		{
			name:    "omitted auth falls back to GITHUB_TOKEN",
			file:    "testdata/omitted_auth.yaml",
			envVars: map[string]string{"GITHUB_TOKEN": "ghp_fallback"},
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				if cfg.Targets[0].Auth.Token != "ghp_fallback" {
					t.Errorf("expected token from GITHUB_TOKEN, got %q", cfg.Targets[0].Auth.Token)
				}
			},
		},
		{
			name:    "explicit token wins over GITHUB_TOKEN",
			file:    "testdata/pat_token.yaml",
			envVars: map[string]string{"GITHUB_TOKEN": "ghp_fallback"},
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				if cfg.Targets[0].Auth.Token != "ghp_testtoken123" {
					t.Errorf("expected explicit token, got %q", cfg.Targets[0].Auth.Token)
				}
			},
		},
		{
			name:    "explicit token_env wins over GITHUB_TOKEN",
			file:    "testdata/pat_env.yaml",
			envVars: map[string]string{"TEST_GITHUB_TOKEN": "ghp_from_env", "GITHUB_TOKEN": "ghp_fallback"},
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				if cfg.Targets[0].Auth.Token != "ghp_from_env" {
					t.Errorf("expected token from token_env, got %q", cfg.Targets[0].Auth.Token)
				}
			},
		},
		{
			name:    "explicit app wins over GITHUB_TOKEN",
			file:    "testdata/app_auth.yaml",
			envVars: map[string]string{"GITHUB_TOKEN": "ghp_fallback"},
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				a := cfg.Targets[0].Auth
				if a.Token != "" || a.App.AppID != 12345 {
					t.Errorf("expected app auth without token, got %+v", a)
				}
			},
		},
		{
			name:        "partial app auth does not fall back",
			file:        "testdata/partial_app.yaml",
			envVars:     map[string]string{"GITHUB_TOKEN": "ghp_fallback"},
			wantErr:     true,
			errContains: `target "myorg": must specify auth.token, auth.token_env, or auth.app`,
		},
		{
			name:    "no orgs and no repos is error",
			file:    "testdata/no_orgs_repos.yaml",
			wantErr: true,
		},
		{
			name: "explicit repos bypasses orgs",
			file: "testdata/explicit_repos.yaml",
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				repos := cfg.Targets[0].Repos
				if len(repos) != 2 {
					t.Fatalf("expected 2 repos, got %d", len(repos))
				}
			},
		},
		{
			name: "base_url defaults to api.github.com",
			file: "testdata/pat_token.yaml",
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				if got := cfg.Targets[0].BaseURL; got != "https://api.github.com" {
					t.Errorf("expected default base_url, got %q", got)
				}
			},
		},
		{
			name: "base_url per target",
			file: "testdata/base_url.yaml",
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				want := map[string]string{
					"dotcom": "https://api.github.com",
					"ghes":   "https://ghes.example.com/api/v3",
				}
				for _, tg := range cfg.Targets {
					if tg.BaseURL != want[tg.Name] {
						t.Errorf("target %q: expected base_url %q, got %q", tg.Name, want[tg.Name], tg.BaseURL)
					}
				}
			},
		},
		{
			name:        "invalid base_url is error",
			file:        "testdata/bad_base_url.yaml",
			wantErr:     true,
			errContains: `target "ghes": base_url must be an http(s) URL without query or fragment, got "ghes.example.com/api/v3"`,
		},
		{
			name:        "base_url with query is error",
			file:        "testdata/base_url_query.yaml",
			wantErr:     true,
			errContains: `target "ghes": base_url must be an http(s) URL without query or fragment, got "https://ghes.example.com/api/v3?x=1"`,
		},
		{
			name: "telemetry defaults",
			file: "testdata/pat_token.yaml",
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				want := config.Telemetry{
					Metrics:  config.Signal{Enabled: true},
					Traces:   config.Signal{Enabled: true},
					Logs:     config.Signal{Enabled: true},
					Profiles: config.Profiles{Interval: time.Minute},
				}
				if cfg.Telemetry != want {
					t.Errorf("telemetry = %+v, want %+v", cfg.Telemetry, want)
				}
			},
		},
		{
			name: "telemetry from file",
			file: "testdata/telemetry.yaml",
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				tel := cfg.Telemetry
				if !tel.Enabled || tel.Traces.Enabled || !tel.Metrics.Enabled || !tel.Logs.Enabled || tel.Profiles.Enabled {
					t.Errorf("telemetry = %+v, want enabled with traces off", tel)
				}
			},
		},
		{
			name: "telemetry env overrides",
			file: "testdata/telemetry.yaml",
			envVars: map[string]string{
				"GITHUB_ACTIVITY_EXPORTER_TELEMETRY_ENABLED":          "false",
				"GITHUB_ACTIVITY_EXPORTER_TELEMETRY_TRACES_ENABLED":   "true",
				"GITHUB_ACTIVITY_EXPORTER_TELEMETRY_LOGS_ENABLED":     "false",
				"GITHUB_ACTIVITY_EXPORTER_TELEMETRY_PROFILES_ENABLED": "true",
			},
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				tel := cfg.Telemetry
				if tel.Enabled || !tel.Traces.Enabled || tel.Logs.Enabled || !tel.Profiles.Enabled || !tel.Metrics.Enabled {
					t.Errorf("telemetry = %+v, want env values", tel)
				}
			},
		},
		{
			name: "profiles from file",
			file: "testdata/telemetry_profiles.yaml",
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				want := config.Profiles{Enabled: true, Endpoint: "http://otel-collector:4040", Interval: 30 * time.Second, MutexFraction: 100, BlockRate: 100000}
				if cfg.Telemetry.Profiles != want {
					t.Errorf("profiles = %+v, want %+v", cfg.Telemetry.Profiles, want)
				}
			},
		},
		{
			name: "profiles env overrides",
			file: "testdata/pat_token.yaml",
			envVars: map[string]string{
				"GITHUB_ACTIVITY_EXPORTER_TELEMETRY_PROFILES_ENDPOINT":       "http://c:4040",
				"GITHUB_ACTIVITY_EXPORTER_TELEMETRY_PROFILES_INTERVAL":       "10s",
				"GITHUB_ACTIVITY_EXPORTER_TELEMETRY_PROFILES_MUTEX_FRACTION": "5",
				"GITHUB_ACTIVITY_EXPORTER_TELEMETRY_PROFILES_BLOCK_RATE":     "7",
			},
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				want := config.Profiles{Endpoint: "http://c:4040", Interval: 10 * time.Second, MutexFraction: 5, BlockRate: 7}
				if cfg.Telemetry.Profiles != want {
					t.Errorf("profiles = %+v, want %+v", cfg.Telemetry.Profiles, want)
				}
			},
		},
		{
			name:        "telemetry non-boolean in file is error",
			file:        "testdata/telemetry_bad.yaml",
			wantErr:     true,
			errContains: "telemetry.metrics.enabled",
		},
		{
			name:        "telemetry non-boolean in env is error",
			file:        "testdata/pat_token.yaml",
			envVars:     map[string]string{"GITHUB_ACTIVITY_EXPORTER_TELEMETRY_ENABLED": "yes"},
			wantErr:     true,
			errContains: "telemetry.enabled",
		},
		{
			name: "multi-org config loads correctly",
			file: "testdata/multi_org.yaml",
			check: func(t *testing.T, cfg *config.Config) {
				t.Helper()
				orgs := cfg.Targets[0].Orgs
				if len(orgs) != 2 {
					t.Fatalf("expected 2 orgs, got %d", len(orgs))
				}
				if orgs[0].Org != "orgA" || len(orgs[0].IncludeRepos) != 1 {
					t.Errorf("orgA config wrong: %+v", orgs[0])
				}
				if orgs[1].Org != "orgB" || len(orgs[1].ExcludeRepos) != 1 {
					t.Errorf("orgB config wrong: %+v", orgs[1])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Cannot use t.Parallel() with t.Setenv — env manipulation requires serial execution.

			// Set env vars for this test.
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			cfg, err := config.Load(tt.file)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

func TestLoadHeadersEnv(t *testing.T) {
	const secret = "s3cret"
	tests := []struct {
		name string
		file string
		env  *string // nil leaves TEST_OTLP_HEADERS unset
		want map[string]string
		// errContains, when set, means Load must fail with this substring.
		errContains string
	}{
		{
			name: "env only",
			file: "testdata/headers_env_only.yaml",
			env:  ptr("Authorization=Bearer " + secret),
			want: map[string]string{"authorization": "Bearer " + secret},
		},
		{
			name: "merged with file, env wins",
			file: "testdata/headers_env.yaml",
			env:  ptr("authorization=Bearer " + secret + ", x-extra = one "),
			want: map[string]string{"x-scope-orgid": "tenant-a", "authorization": "Bearer " + secret, "x-extra": "one"},
		},
		{
			name: "file headers kept when env does not overlap",
			file: "testdata/headers_env.yaml",
			env:  ptr("x-extra=one"),
			want: map[string]string{"x-scope-orgid": "tenant-a", "authorization": "Bearer file", "x-extra": "one"},
		},
		{
			name: "percent-decoded value and = in value",
			file: "testdata/headers_env_only.yaml",
			env:  ptr("x-token=a%2Cb,authorization=Basic dXNlcjpwYXNz=="),
			want: map[string]string{"x-token": "a,b", "authorization": "Basic dXNlcjpwYXNz=="},
		},
		{
			name: "trailing newline trimmed",
			file: "testdata/headers_env_only.yaml",
			env:  ptr("authorization=Bearer " + secret + "\n"),
			want: map[string]string{"authorization": "Bearer " + secret},
		},
		{
			name:        "unset",
			file:        "testdata/headers_env_only.yaml",
			errContains: `export.otlp.headers_env: env var "TEST_OTLP_HEADERS" is not set or empty`,
		},
		{
			name:        "empty",
			file:        "testdata/headers_env_only.yaml",
			env:         ptr(" "),
			errContains: `export.otlp.headers_env: env var "TEST_OTLP_HEADERS" is not set or empty`,
		},
		{
			name:        "entry without =",
			file:        "testdata/headers_env_only.yaml",
			env:         ptr("authorization=Bearer " + secret + "," + secret),
			errContains: `env var "TEST_OTLP_HEADERS", entry 2: not a name=value pair`,
		},
		{
			name:        "trailing comma",
			file:        "testdata/headers_env_only.yaml",
			env:         ptr("authorization=Bearer " + secret + ","),
			errContains: `env var "TEST_OTLP_HEADERS", entry 2: not a name=value pair`,
		},
		{
			name:        "colon syntax",
			file:        "testdata/headers_env_only.yaml",
			env:         ptr("Authorization: Bearer " + secret + "=="),
			errContains: `env var "TEST_OTLP_HEADERS", entry 1: invalid header name`,
		},
		{
			name:        "empty name",
			file:        "testdata/headers_env_only.yaml",
			env:         ptr("=" + secret),
			errContains: `env var "TEST_OTLP_HEADERS", entry 1: invalid header name`,
		},
		{
			name:        "bad percent-encoding",
			file:        "testdata/headers_env_only.yaml",
			env:         ptr("authorization=Bearer " + secret + "%zz"),
			errContains: `env var "TEST_OTLP_HEADERS", entry 1: invalid percent-encoding in value`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != nil {
				t.Setenv("TEST_OTLP_HEADERS", *tt.env)
			}
			cfg, err := config.Load(tt.file)
			if tt.errContains != "" {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err)
				}
				// Header values never appear in an error.
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error leaks the header value: %q", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := cfg.Export.OTLP.Headers
			if len(got) != len(tt.want) {
				t.Fatalf("headers = %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("header %q = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func ptr(s string) *string { return &s }
