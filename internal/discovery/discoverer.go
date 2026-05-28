// Package discovery implements GitHub repository discovery with org listing,
// glob filtering, explicit repo lists, multi-org support, and incremental refresh.
package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/go-github/v62/github"

	"github.com/gjed/renovate-exporter/internal/config"
)

const defaultRefreshInterval = 60 * time.Minute

// Repo is a discovered repository.
type Repo struct {
	Owner string
	Name  string
	// FullName is "owner/name".
	FullName string
}

// RepoLister abstracts the GitHub REST API call for listing org repos (injectable in tests).
type RepoLister interface {
	ListByOrg(ctx context.Context, org string, opts *github.RepositoryListByOrgOptions) ([]*github.Repository, *github.Response, error)
}

// Discoverer discovers and maintains the list of monitored repositories for a target.
type Discoverer struct {
	target   config.Target
	lister   RepoLister
	logger   *slog.Logger
	interval time.Duration

	mu          sync.RWMutex
	repos       []Repo
	initialized bool // true after the first successful refresh
}

// Option is a functional option for Discoverer.
type Option func(*Discoverer)

// WithLogger sets the logger.
func WithLogger(l *slog.Logger) Option {
	return func(d *Discoverer) { d.logger = l }
}

// WithRefreshInterval overrides the default refresh interval (for testing).
func WithRefreshInterval(dur time.Duration) Option {
	return func(d *Discoverer) { d.interval = dur }
}

// New creates a Discoverer for the given target. Call Start to begin background refresh.
func New(target config.Target, lister RepoLister, opts ...Option) *Discoverer {
	d := &Discoverer{
		target:   target,
		lister:   lister,
		logger:   slog.Default(),
		interval: defaultRefreshInterval,
	}
	if target.Discovery.RefreshInterval > 0 {
		d.interval = target.Discovery.RefreshInterval
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Repos returns a snapshot copy of the current discovered repositories.
// It performs an initial discovery on first call if not yet initialized.
// The returned slice is safe to read without a lock; callers must not modify it.
func (d *Discoverer) Repos(ctx context.Context) ([]Repo, error) {
	d.mu.RLock()
	initialized := d.initialized
	repos := d.repos
	d.mu.RUnlock()

	if initialized {
		cp := make([]Repo, len(repos))
		copy(cp, repos)
		return cp, nil
	}

	// Initial discovery.
	return d.refresh(ctx)
}

// Start begins background periodic refresh. It blocks until ctx is cancelled.
// Callers should run this in a goroutine.
func (d *Discoverer) Start(ctx context.Context) {
	// Initial discovery.
	if _, err := d.refresh(ctx); err != nil {
		d.logger.Error("initial repo discovery failed", "target", d.target.Name, "err", err)
	}

	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := d.refresh(ctx); err != nil {
				d.logger.Error("repo refresh failed", "target", d.target.Name, "err", err)
			}
		}
	}
}

// refresh re-discovers repositories and updates the internal cache.
func (d *Discoverer) refresh(ctx context.Context) ([]Repo, error) {
	var repos []Repo

	if len(d.target.Repos) > 0 {
		// Explicit repo list mode: bypass API.
		var err error
		repos, err = parseExplicitRepos(d.target.Repos)
		if err != nil {
			return nil, err
		}
	} else {
		// Org autodiscovery mode.
		var err error
		repos, err = d.discoverOrgs(ctx)
		if err != nil {
			return nil, err
		}
	}

	d.mu.Lock()
	d.repos = repos
	d.initialized = true
	d.mu.Unlock()

	d.logDiscovered(repos)
	// Return a copy so callers cannot mutate the internal cache.
	cp := make([]Repo, len(repos))
	copy(cp, repos)
	return cp, nil
}

// discoverOrgs lists repos for all configured orgs and deduplicates.
func (d *Discoverer) discoverOrgs(ctx context.Context) ([]Repo, error) {
	seen := make(map[string]bool)
	var result []Repo

	for _, orgCfg := range d.target.Orgs {
		repos, err := d.listOrgRepos(ctx, orgCfg)
		if err != nil {
			return nil, fmt.Errorf("listing repos for org %q: %w", orgCfg.Org, err)
		}

		for _, r := range repos {
			if !seen[r.FullName] {
				seen[r.FullName] = true
				result = append(result, r)
			}
		}
	}

	return result, nil
}

// listOrgRepos fetches all repos for one org and applies include/exclude filters.
func (d *Discoverer) listOrgRepos(ctx context.Context, orgCfg config.OrgConfig) ([]Repo, error) {
	var all []*github.Repository

	opts := &github.RepositoryListByOrgOptions{
		Type: "all",
		ListOptions: github.ListOptions{PerPage: 100},
	}

	for {
		page, resp, err := d.lister.ListByOrg(ctx, orgCfg.Org, opts)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	var repos []Repo
	for _, r := range all {
		// Skip archived (unless opted in) and forks.
		if r.GetArchived() && !orgCfg.IncludeArchived {
			d.logger.Debug("excluding archived repo", "repo", r.GetFullName())
			continue
		}
		if r.GetFork() {
			d.logger.Debug("excluding fork", "repo", r.GetFullName())
			continue
		}

		name := r.GetName()

		// Apply include filter.
		if len(orgCfg.IncludeRepos) > 0 && !d.matchesAny(name, orgCfg.IncludeRepos) {
			d.logger.Debug("excluding repo (not in include list)", "repo", r.GetFullName())
			continue
		}

		// Apply exclude filter.
		if d.matchesAny(name, orgCfg.ExcludeRepos) {
			d.logger.Debug("excluding repo (in exclude list)", "repo", r.GetFullName())
			continue
		}

		repos = append(repos, Repo{
			Owner:    orgCfg.Org,
			Name:     name,
			FullName: r.GetFullName(),
		})
	}

	return repos, nil
}

// parseExplicitRepos converts "owner/name" strings into Repo values.
// Returns an error if any entry is not in "owner/name" format.
func parseExplicitRepos(explicit []string) ([]Repo, error) {
	repos := make([]Repo, 0, len(explicit))
	for _, s := range explicit {
		owner, name := splitOwnerRepo(s)
		if owner == "" || name == "" {
			return nil, fmt.Errorf("explicit repo %q is not in owner/name format", s)
		}
		repos = append(repos, Repo{Owner: owner, Name: name, FullName: s})
	}
	return repos, nil
}

// splitOwnerRepo splits "owner/name" into its two components.
// If the format is unexpected, owner is empty.
func splitOwnerRepo(full string) (owner, name string) {
	for i, c := range full {
		if c == '/' {
			return full[:i], full[i+1:]
		}
	}
	return "", full
}

// matchesAny returns true if name matches at least one glob pattern.
// Invalid patterns are logged via d.logger as warnings and treated as non-matching.
func (d *Discoverer) matchesAny(name string, patterns []string) bool {
	for _, pat := range patterns {
		ok, err := filepath.Match(pat, name)
		if err != nil {
			// filepath.Match only errors on malformed syntax (e.g. unclosed bracket).
			// Log and skip rather than silently dropping the pattern.
			d.logger.Warn("invalid glob pattern — skipping", "pattern", pat, "err", err)
			continue
		}
		if ok {
			return true
		}
	}
	return false
}

// logDiscovered logs the discovered repo list at info level.
func (d *Discoverer) logDiscovered(repos []Repo) {
	const maxLoggedRepos = 20
	if len(repos) <= maxLoggedRepos {
		names := make([]string, len(repos))
		for i, r := range repos {
			names[i] = r.FullName
		}
		d.logger.Info("discovered repos", "target", d.target.Name, "count", len(repos), "repos", names)
	} else {
		d.logger.Info("discovered repos", "target", d.target.Name, "count", len(repos))
	}
}
