package config_test

import (
	"testing"

	"github.com/gjed/renovate-exporter/internal/config"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		envVars map[string]string
		check   func(t *testing.T, cfg *config.Config)
		wantErr bool
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
			name:    "no auth is error",
			file:    "testdata/no_auth.yaml",
			wantErr: true,
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
