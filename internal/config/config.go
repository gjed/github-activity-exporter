// Package config handles loading and validation of the exporter configuration.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config is the top-level configuration.
type Config struct {
	// CollectionInterval is how often metrics are collected and pushed. Defaults to 5m.
	CollectionInterval time.Duration `mapstructure:"collection_interval"`
	Export             Export        `mapstructure:"export"`
	Targets            []Target      `mapstructure:"targets"`
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
	TLS     TLS               `mapstructure:"tls"`
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

// Target represents a single monitored GitHub target (set of orgs/repos with shared auth).
type Target struct {
	Name      string      `mapstructure:"name"`
	Auth      Auth        `mapstructure:"auth"`
	Orgs      []OrgConfig `mapstructure:"orgs"`
	Repos     []string    `mapstructure:"repos"`
	Discovery Discovery   `mapstructure:"discovery"`
	Filters   Filters     `mapstructure:"filters"`
}

// Filters holds per-target filtering rules for PRs and issues.
type Filters struct {
	PRs    PRFilters    `mapstructure:"prs"`
	Issues IssueFilters `mapstructure:"issues"`
}

// PRFilters defines label and state filters for pull requests.
type PRFilters struct {
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

	// Allow env var overrides with RENOVATE_EXPORTER_ prefix.
	v.SetEnvPrefix("RENOVATE_EXPORTER")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	v.SetDefault("collection_interval", "5m")
	v.SetDefault("export.otlp.protocol", "http")
	v.SetDefault("export.prometheus.listen_address", ":9090")

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

	if err := compileFilters(&cfg); err != nil {
		return nil, err
	}

	if err := validate(&cfg); err != nil {
		return nil, err
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
// as specified by token_env and private_key_env.
func resolveEnvVars(cfg *Config) error {
	for i := range cfg.Targets {
		t := &cfg.Targets[i]

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

// validate checks the export settings and that each target has valid,
// non-conflicting auth and required fields.
func validate(cfg *Config) error {
	if err := validateExport(cfg); err != nil {
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

		if len(t.Orgs) == 0 && len(t.Repos) == 0 {
			return fmt.Errorf("target %q: must specify at least one org or explicit repo", t.Name)
		}
	}
	return nil
}

// validateExport checks the OTLP endpoint, protocol and collection interval.
func validateExport(cfg *Config) error {
	if cfg.Export.OTLP.Endpoint == "" {
		return fmt.Errorf("export.otlp.endpoint is required")
	}
	switch cfg.Export.OTLP.Protocol {
	case "http", "grpc":
	default:
		return fmt.Errorf("export.otlp.protocol must be \"http\" or \"grpc\", got %q", cfg.Export.OTLP.Protocol)
	}
	if cfg.CollectionInterval <= 0 {
		return fmt.Errorf("collection_interval must be positive, got %s", cfg.CollectionInterval)
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
