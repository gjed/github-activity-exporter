// Package main is the entry point for the renovate-exporter binary.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/gjed/renovate-exporter/internal/config"
	internalgithub "github.com/gjed/renovate-exporter/internal/github"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var configFile string

	cmd := &cobra.Command{
		Use:   "renovate-exporter",
		Short: "Prometheus exporter for Renovate dependency update metrics",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), configFile)
		},
	}

	cmd.PersistentFlags().StringVar(&configFile, "config", "config.yaml", "path to config file")

	return cmd
}

// run is the main entry point after CLI parsing.
func run(ctx context.Context, configFile string) error {
	logger := slog.Default()

	cfg, err := config.Load(configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Validate credentials for every target on startup (fail fast).
	for _, target := range cfg.Targets {
		auth, err := buildAuthenticator(target.Auth)
		if err != nil {
			return fmt.Errorf("target %q: building authenticator: %w", target.Name, err)
		}

		logger.Info("pinging GitHub API", "target", target.Name)
		if err := auth.Ping(ctx); err != nil {
			return fmt.Errorf("target %q: credential validation failed: %w", target.Name, err)
		}
		logger.Info("credentials validated", "target", target.Name)
	}

	return nil
}

// buildAuthenticator constructs the appropriate Authenticator from an Auth config.
func buildAuthenticator(a config.Auth) (internalgithub.Authenticator, error) {
	if a.Token != "" {
		return internalgithub.NewPATAuthenticator(a.Token), nil
	}

	// GitHub App auth.
	opts := internalgithub.AppAuthOptions{
		AppID:          a.App.AppID,
		InstallationID: a.App.InstallationID,
	}

	switch {
	case a.App.PrivateKeyPath != "":
		opts.PrivateKeyPath = a.App.PrivateKeyPath
	case a.App.PrivateKeyValue != "":
		// PrivateKeyValue is the resolved base64-encoded PEM from the env var.
		opts.PrivateKeyBase64 = a.App.PrivateKeyValue
	}

	return internalgithub.NewAppAuthenticator(opts)
}
