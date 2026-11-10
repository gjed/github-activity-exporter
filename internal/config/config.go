// Package config handles loading and validation of the exporter configuration.
package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config is the top-level configuration.
type Config struct {
	// CollectionInterval is the period between the starts of consecutive
	// collection cycles of a target. Defaults to 5m.
	CollectionInterval time.Duration `mapstructure:"collection_interval"`
	Export             Export        `mapstructure:"export"`
	Health             Health        `mapstructure:"health"`
	Telemetry          Telemetry     `mapstructure:"telemetry"`
	Targets            []Target      `mapstructure:"targets"`
}

// Telemetry holds the self-telemetry toggles: telemetry about the exporter
// itself, separate from the GitHub metrics it exports. A signal is active only
// when Enabled and the signal's own Enabled are both true.
type Telemetry struct {
	// Enabled is the global toggle. Defaults to false.
	Enabled  bool     `mapstructure:"enabled"`
	Metrics  Signal   `mapstructure:"metrics"`
	Traces   Signal   `mapstructure:"traces"`
	Logs     Signal   `mapstructure:"logs"`
	Profiles Profiles `mapstructure:"profiles"`
}

// Profiles holds the profiles signal settings. Enabled defaults to false.
// The settings are checked only when the signal is active, by the telemetry
// package, which logs errors instead of failing startup.
type Profiles struct {
	Enabled bool `mapstructure:"enabled"`
	// Endpoint is the base URL of an OpenTelemetry Collector pprof receiver
	// in server mode; profiles are posted to Endpoint + "/v1/pprof".
	Endpoint string `mapstructure:"endpoint"`
	// Interval is the CPU profile window and the push period. Defaults to 60s.
	Interval time.Duration `mapstructure:"interval"`
	// MutexFraction is the runtime mutex profile fraction; 0 (default) is off.
	MutexFraction int `mapstructure:"mutex_fraction"`
	// BlockRate is the runtime block profile rate in nanoseconds; 0 (default) is off.
	BlockRate int `mapstructure:"block_rate"`
}

// Signal holds the toggle of one self-telemetry signal.
type Signal struct {
	// Enabled defaults to true; the signal still needs Telemetry.Enabled.
	Enabled bool `mapstructure:"enabled"`
}

// Export holds the metric export settings.
type Export struct {
	OTLP       OTLP       `mapstructure:"otlp"`
	Prometheus Prometheus `mapstructure:"prometheus"`
}

// OTLP holds the OTLP push settings. Endpoint is required.
type OTLP struct {
	Endpoint string `mapstructure:"endpoint"`
	// Protocol is "http" (default) or "grpc".
	Protocol string `mapstructure:"protocol"`
	// Headers are sent on every OTLP request. Keys are lower-cased by the config loader.
	Headers map[string]string `mapstructure:"headers"`
	// HeadersEnv names an env var holding more headers as comma-separated
	// name=value pairs, the OTEL_EXPORTER_OTLP_HEADERS format. Load merges them
	// into Headers; they win over Headers for the same name.
	HeadersEnv string `mapstructure:"headers_env"`
	TLS        TLS    `mapstructure:"tls"`
}

// TLS holds the OTLP TLS settings.
type TLS struct {
	// Insecure skips certificate verification (HTTP) or disables TLS (gRPC).
	Insecure bool `mapstructure:"insecure"`
}

// Prometheus holds the optional Prometheus /metrics bridge settings.
type Prometheus struct {
	Enabled bool `mapstructure:"enabled"`
	// ListenAddress defaults to ":9090".
	ListenAddress string `mapstructure:"listen_address"`
}

// Health holds the /healthz and /readyz server settings.
type Health struct {
	// ListenAddress defaults to ":10062".
	ListenAddress string `mapstructure:"listen_address"`
	// StallTimeout is how long a target may go without completing a
	// collection cycle before /healthz fails. Load resolves 0 to
	// 2 × CollectionInterval + 1h: a cycle completes at most
	// CollectionInterval plus its own duration after the previous one, so
	// the default allows a cycle of up to CollectionInterval + 1h.
	StallTimeout time.Duration `mapstructure:"stall_timeout"`
	// UnauthorizedCycles is how many consecutive cycles answered only with
	// HTTP 401 fail /healthz. Defaults to 3.
	UnauthorizedCycles int `mapstructure:"unauthorized_cycles"`
}

// DefaultBaseURL is the GitHub REST API root used when a target sets no base_url.
const DefaultBaseURL = "https://api.github.com"

// DefaultGraphQLBatchSize is the number of repos per batched GraphQL query
// when a target sets no graphql_batch_size, and MaxGraphQLBatchSize the
// largest accepted value. See
// openspec/changes/archive/2026-11-09-graphql-batching/design.md for
// how the default was measured.
const (
	DefaultGraphQLBatchSize = 20
	MaxGraphQLBatchSize     = 100
)

// Target represents a single monitored GitHub target (set of orgs/repos with shared auth).
type Target struct {
	Name string `mapstructure:"name"`
	// BaseURL is the GitHub REST API root (e.g. https://ghes.example.com/api/v3).
	// Load defaults it to DefaultBaseURL and trims trailing slashes.
	BaseURL   string      `mapstructure:"base_url"`
	Auth      Auth        `mapstructure:"auth"`
	Orgs      []OrgConfig `mapstructure:"orgs"`
	Repos     []string    `mapstructure:"repos"`
	Discovery Discovery   `mapstructure:"discovery"`
	Filters   Filters     `mapstructure:"filters"`
	// RenovateDashboard configures the Renovate Dependency Dashboard collector.
	RenovateDashboard RenovateDashboard `mapstructure:"renovate_dashboard"`
	// GraphQLBatchSize is the number of repos the issue count and Dependency
	// Dashboard queries read per GraphQL request, 1 to MaxGraphQLBatchSize.
	// nil means DefaultGraphQLBatchSize; see BatchSize.
	GraphQLBatchSize *int `mapstructure:"graphql_batch_size"`
}

// BatchSize returns GraphQLBatchSize, or DefaultGraphQLBatchSize when unset.
func (t Target) BatchSize() int {
	if t.GraphQLBatchSize == nil {
		return DefaultGraphQLBatchSize
	}
	return *t.GraphQLBatchSize
}

// RenovateDashboard holds the Renovate Dependency Dashboard collector settings.
type RenovateDashboard struct {
	// Enabled turns the collector on for the target. Defaults to false.
	Enabled bool `mapstructure:"enabled"`
}

// Filters holds per-target filtering rules for PRs and issues.
type Filters struct {
	PRs    PRFilters    `mapstructure:"prs"`
	Issues IssueFilters `mapstructure:"issues"`
}

// PRFilters defines author, head branch, label and state filters for pull requests.
type PRFilters struct {
	// Authors restricts collection to PRs opened by one of these logins
	// (case-insensitive). GitHub App bots use their REST login, e.g. "renovate[bot]".
	// When empty, all authors are included.
	Authors []string `mapstructure:"authors"`
	// HeadBranchPrefix restricts collection to PRs whose head branch starts with
	// this prefix (case-sensitive). When empty, all branches are included.
	HeadBranchPrefix string `mapstructure:"head_branch_prefix"`
	// IncludeLabels restricts collection to PRs carrying at least one of these labels.
	// When empty, all labels are included.
	IncludeLabels []string `mapstructure:"include_labels"`
	// ExcludeLabels drops PRs carrying any of these labels.
	ExcludeLabels []string `mapstructure:"exclude_labels"`
	// States restricts collection to PRs in these states ("open", "closed", "draft", "merged").
	// When empty, all states are included.
	States []string `mapstructure:"states"`
}

// IssueFilters defines label and title-pattern filters for issues.
type IssueFilters struct {
	// ExcludeTitlePatterns holds raw regex strings from config.
	// Compiled forms are stored in ExcludeTitleRegexps after Load().
	ExcludeTitlePatterns []string `mapstructure:"exclude_title_patterns"`
	// ExcludeTitleRegexps is populated by compileFilters; never set from YAML.
	ExcludeTitleRegexps []*regexp.Regexp `mapstructure:"-"`
	// States restricts collection to issues in these states ("open", "closed").
	// When empty, all states are included.
	States []string `mapstructure:"states"`
}

// Auth specifies authentication configuration for a target.
// Exactly one of Token/TokenEnv (PAT) or App (GitHub App) must be set.
// An empty Auth falls back to the GITHUB_TOKEN env var as a PAT.
type Auth struct {
	Token    string  `mapstructure:"token"`
	TokenEnv string  `mapstructure:"token_env"`
	App      AppAuth `mapstructure:"app"`
}

// AppAuth holds GitHub App authentication parameters.
type AppAuth struct {
	AppID          int64  `mapstructure:"app_id"`
	InstallationID int64  `mapstructure:"installation_id"`
	PrivateKeyPath string `mapstructure:"private_key_path"`
	// PrivateKeyEnv is the name of the env var holding a base64-encoded PEM private key.
	// After config loading, the resolved key material is in PrivateKeyValue; PrivateKeyEnv
	// retains the original env var name for diagnostics.
	PrivateKeyEnv string `mapstructure:"private_key_env"`
	// PrivateKeyValue is populated by Load from the env var named in PrivateKeyEnv.
	// It holds the raw base64-encoded PEM; it is never set from the config file.
	PrivateKeyValue string `mapstructure:"-"`
}

// OrgConfig defines per-organisation discovery settings.
type OrgConfig struct {
	Org             string   `mapstructure:"org"`
	IncludeRepos    []string `mapstructure:"include_repos"`
	ExcludeRepos    []string `mapstructure:"exclude_repos"`
	IncludeArchived bool     `mapstructure:"include_archived"`
}

// Discovery holds global discovery settings for a target.
type Discovery struct {
	RefreshInterval time.Duration `mapstructure:"refresh_interval"`
}

// Load reads configuration from the given file path using viper.
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)

	// Allow env var overrides with GITHUB_ACTIVITY_EXPORTER_ prefix.
	v.SetEnvPrefix("GITHUB_ACTIVITY_EXPORTER")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	v.SetDefault("collection_interval", "5m")
	v.SetDefault("export.otlp.protocol", "http")
	v.SetDefault("export.prometheus.listen_address", ":9090")
	v.SetDefault("health.listen_address", ":10062")
	v.SetDefault("health.unauthorized_cycles", 3)
	v.SetDefault("health.stall_timeout", "0s")
	// Every telemetry key needs a default: viper maps env vars only onto known keys.
	v.SetDefault("telemetry.enabled", false)
	for _, s := range []string{"metrics", "traces", "logs"} {
		v.SetDefault("telemetry."+s+".enabled", true)
	}
	v.SetDefault("telemetry.profiles.enabled", false)
	v.SetDefault("telemetry.profiles.endpoint", "")
	v.SetDefault("telemetry.profiles.interval", "60s")
	v.SetDefault("telemetry.profiles.mutex_fraction", 0)
	v.SetDefault("telemetry.profiles.block_rate", 0)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("reading config %q: %w", path, err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshalling config: %w", err)
	}

	if err := resolveEnvVars(&cfg); err != nil {
		return nil, err
	}

	if err := resolveHeadersEnv(&cfg.Export.OTLP); err != nil {
		return nil, err
	}

	if err := compileFilters(&cfg); err != nil {
		return nil, err
	}

	for i := range cfg.Targets {
		t := &cfg.Targets[i]
		if t.BaseURL == "" {
			t.BaseURL = DefaultBaseURL
		}
		t.BaseURL = strings.TrimRight(t.BaseURL, "/")
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	if cfg.Health.StallTimeout == 0 {
		cfg.Health.StallTimeout = 2*cfg.CollectionInterval + time.Hour
	}

	return &cfg, nil
}

// compileFilters compiles issue title exclusion patterns into regexp.Regexp values.
// It returns an error if any pattern is invalid, reporting the target name and pattern.
func compileFilters(cfg *Config) error {
	for i := range cfg.Targets {
		t := &cfg.Targets[i]
		patterns := t.Filters.Issues.ExcludeTitlePatterns
		if len(patterns) == 0 {
			continue
		}
		compiled := make([]*regexp.Regexp, 0, len(patterns))
		for _, p := range patterns {
			re, err := regexp.Compile(p)
			if err != nil {
				return fmt.Errorf("target %q: invalid filters.issues.exclude_title_patterns %q: %w", t.Name, p, err)
			}
			compiled = append(compiled, re)
		}
		t.Filters.Issues.ExcludeTitleRegexps = compiled
	}
	return nil
}

// resolveEnvVars fills token/private-key values from environment variables
// as specified by token_env and private_key_env. A target with an empty auth
// block takes its PAT from GITHUB_TOKEN.
func resolveEnvVars(cfg *Config) error {
	for i := range cfg.Targets {
		t := &cfg.Targets[i]

		if t.Auth == (Auth{}) {
			val := os.Getenv("GITHUB_TOKEN")
			if val == "" {
				return fmt.Errorf("target %q: no auth configured and GITHUB_TOKEN is not set or empty; set auth.token, auth.token_env, auth.app, or GITHUB_TOKEN", t.Name)
			}
			t.Auth.Token = val
			continue
		}

		if t.Auth.TokenEnv != "" {
			val := os.Getenv(t.Auth.TokenEnv)
			if val == "" {
				return fmt.Errorf("target %q: env var %q (auth.token_env) is not set or empty", t.Name, t.Auth.TokenEnv)
			}
			t.Auth.Token = val
		}

		if t.Auth.App.PrivateKeyEnv != "" {
			val := os.Getenv(t.Auth.App.PrivateKeyEnv)
			if val == "" {
				return fmt.Errorf("target %q: env var %q (auth.app.private_key_env) is not set or empty", t.Name, t.Auth.App.PrivateKeyEnv)
			}
			// Store the resolved base64-encoded PEM in PrivateKeyValue.
			// PrivateKeyEnv retains the original env var name for diagnostics.
			t.Auth.App.PrivateKeyValue = val
		}
	}
	return nil
}

// resolveHeadersEnv merges the headers from the env var named by
// export.otlp.headers_env into Headers. Errors never carry a header value.
func resolveHeadersEnv(o *OTLP) error {
	if o.HeadersEnv == "" {
		return nil
	}
	raw := os.Getenv(o.HeadersEnv)
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("export.otlp.headers_env: env var %q is not set or empty", o.HeadersEnv)
	}
	if o.Headers == nil {
		o.Headers = make(map[string]string)
	}
	for i, pair := range strings.Split(raw, ",") {
		name, value, found := strings.Cut(pair, "=")
		name = strings.ToLower(strings.TrimSpace(name))
		if !found {
			return fmt.Errorf("export.otlp.headers_env: env var %q, entry %d: not a name=value pair", o.HeadersEnv, i+1)
		}
		if !validHeaderName(name) {
			return fmt.Errorf("export.otlp.headers_env: env var %q, entry %d: invalid header name", o.HeadersEnv, i+1)
		}
		value, err := url.PathUnescape(value)
		if err != nil {
			return fmt.Errorf("export.otlp.headers_env: env var %q, entry %d: invalid percent-encoding in value", o.HeadersEnv, i+1)
		}
		value = strings.TrimSpace(value)
		if strings.ContainsFunc(value, func(r rune) bool { return r < ' ' && r != '\t' || r == 0x7f }) {
			return fmt.Errorf("export.otlp.headers_env: env var %q, entry %d: control character in value", o.HeadersEnv, i+1)
		}
		o.Headers[name] = value
	}
	return nil
}

// validHeaderName reports whether name is a non-empty RFC 9110 token.
func validHeaderName(name string) bool {
	return name != "" && !strings.ContainsFunc(name, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("!#$%&'*+-.^_`|~", r)
	})
}

// validate checks the export settings and that each target has valid,
// non-conflicting auth and required fields.
func validate(cfg *Config) error {
	if err := validateExport(cfg); err != nil {
		return err
	}
	if err := validateHealth(cfg); err != nil {
		return err
	}

	names := make(map[string]bool)
	for _, t := range cfg.Targets {
		if t.Name == "" {
			return fmt.Errorf("each target must have a non-empty name")
		}
		if names[t.Name] {
			return fmt.Errorf("duplicate target name %q", t.Name)
		}
		names[t.Name] = true

		if err := validateAuth(t.Name, t.Auth); err != nil {
			return err
		}

		if u, err := url.Parse(t.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("target %q: base_url must be an http(s) URL without query or fragment, got %q", t.Name, t.BaseURL)
		}

		if len(t.Orgs) == 0 && len(t.Repos) == 0 {
			return fmt.Errorf("target %q: must specify at least one org or explicit repo", t.Name)
		}

		if n := t.BatchSize(); n < 1 || n > MaxGraphQLBatchSize {
			return fmt.Errorf("target %q: graphql_batch_size must be between 1 and %d, got %d", t.Name, MaxGraphQLBatchSize, n)
		}
	}
	return nil
}

// validateExport checks the OTLP endpoint and protocol, and the collection interval.
// The endpoint shape depends on the protocol: an http(s) URL for http, host:port for grpc.
func validateExport(cfg *Config) error {
	if cfg.Export.OTLP.Endpoint == "" {
		return fmt.Errorf("export.otlp.endpoint is required")
	}
	switch cfg.Export.OTLP.Protocol {
	case "http":
		u, err := url.Parse(cfg.Export.OTLP.Endpoint)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("export.otlp.endpoint must be an http(s) URL for protocol http, got %q", cfg.Export.OTLP.Endpoint)
		}
	case "grpc":
		if strings.Contains(cfg.Export.OTLP.Endpoint, "://") {
			return fmt.Errorf("export.otlp.endpoint must be host:port for protocol grpc, got %q", cfg.Export.OTLP.Endpoint)
		}
	default:
		return fmt.Errorf("export.otlp.protocol must be \"http\" or \"grpc\", got %q", cfg.Export.OTLP.Protocol)
	}
	if cfg.CollectionInterval <= 0 {
		return fmt.Errorf("collection_interval must be positive, got %s", cfg.CollectionInterval)
	}
	return nil
}

// validateHealth checks the health server settings.
func validateHealth(cfg *Config) error {
	h := cfg.Health
	if h.ListenAddress == "" {
		return fmt.Errorf("health.listen_address must not be empty")
	}
	if cfg.Export.Prometheus.Enabled && h.ListenAddress == cfg.Export.Prometheus.ListenAddress {
		return fmt.Errorf("health.listen_address and export.prometheus.listen_address must differ, both are %q", h.ListenAddress)
	}
	if h.StallTimeout < 0 {
		return fmt.Errorf("health.stall_timeout must not be negative, got %s", h.StallTimeout)
	}
	if h.StallTimeout > 0 && h.StallTimeout <= cfg.CollectionInterval {
		return fmt.Errorf("health.stall_timeout must be longer than collection_interval (%s), got %s", cfg.CollectionInterval, h.StallTimeout)
	}
	if h.UnauthorizedCycles < 1 {
		return fmt.Errorf("health.unauthorized_cycles must be at least 1, got %d", h.UnauthorizedCycles)
	}
	return nil
}

// validateAuth ensures exactly one auth method is configured.
func validateAuth(targetName string, a Auth) error {
	hasPAT := a.Token != "" || a.TokenEnv != ""
	hasApp := a.App.AppID != 0

	if hasPAT && hasApp {
		return fmt.Errorf("target %q: auth.token/token_env and auth.app are mutually exclusive", targetName)
	}
	if !hasPAT && !hasApp {
		return fmt.Errorf("target %q: must specify auth.token, auth.token_env, or auth.app", targetName)
	}

	if hasApp {
		app := a.App
		if app.InstallationID == 0 {
			return fmt.Errorf("target %q: auth.app.installation_id is required for GitHub App auth", targetName)
		}
		if app.PrivateKeyPath == "" && app.PrivateKeyEnv == "" {
			return fmt.Errorf("target %q: auth.app.private_key_path or auth.app.private_key_env is required", targetName)
		}
	}
	return nil
}
