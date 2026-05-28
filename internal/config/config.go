// Package config handles loading and validation of the exporter configuration.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config is the top-level configuration.
type Config struct {
	Targets []Target `mapstructure:"targets"`
}

// Target represents a single monitored GitHub target (set of orgs/repos with shared auth).
type Target struct {
	Name      string      `mapstructure:"name"`
	Auth      Auth        `mapstructure:"auth"`
	Orgs      []OrgConfig `mapstructure:"orgs"`
	Repos     []string    `mapstructure:"repos"`
	Discovery Discovery   `mapstructure:"discovery"`
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

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
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

// validate checks that each target has valid, non-conflicting auth and required fields.
func validate(cfg *Config) error {
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
