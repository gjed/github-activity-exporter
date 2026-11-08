// Package discovery implements GitHub repository discovery with org and user
// account listing, glob filtering, explicit repo lists, multi-org support, and
// incremental refresh.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v93/github"

	"github.com/gjed/github-activity-exporter/internal/config"
)

const defaultRefreshInterval = 60 * time.Minute

// Repo is a discovered repository.
type Repo struct {
	Owner string
	Name  string
	// FullName is "owner/name".
	FullName string
}

// ownerType is the GitHub account type of an owner listed under orgs.
type ownerType string

const (
	ownerOrganization ownerType = "Organization"
	ownerUser         ownerType = "User"
)

// Discoverer discovers and maintains the list of monitored repositories for a target.
type Discoverer struct {
	target   config.Target
	client   *github.Client
	logger   *slog.Logger
	interval time.Duration

	mu          sync.RWMutex
	repos       []Repo
	initialized bool // true after the first successful refresh

	// Owner types and the PAT login are cached for the process lifetime.
	// They are only touched from refresh, which never runs concurrently
	// with itself in practice; ownerMu guards them anyway.
	ownerMu    sync.Mutex
	ownerTypes map[string]ownerType
	selfLogin  string
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

// New creates a Discoverer for the given target. client may be nil when the
// target lists explicit repos only. Call Start to begin background refresh.
func New(target config.Target, client *github.Client, opts ...Option) *Discoverer {
	d := &Discoverer{
		target:     target,
		client:     client,
		ownerTypes: make(map[string]ownerType),
		logger:     slog.Default(),
		interval:   defaultRefreshInterval,
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
		repos, err := d.listOwnerRepos(ctx, orgCfg)
		if err != nil {
			return nil, fmt.Errorf("listing repos for owner %q: %w", orgCfg.Org, err)
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

// listOwnerRepos fetches all repos of one orgs entry, organization or user
// account, and applies archived/fork/include/exclude filters.
func (d *Discoverer) listOwnerRepos(ctx context.Context, orgCfg config.OrgConfig) ([]Repo, error) {
	typ, err := d.ownerType(ctx, orgCfg.Org)
	if err != nil {
		return nil, err
	}

	var all []*github.Repository
	switch {
	case typ == ownerOrganization:
		all, err = d.listOrg(ctx, orgCfg.Org)
	case d.target.Auth.App.AppID != 0:
		all, err = d.listInstallation(ctx, orgCfg.Org)
	default:
		all, err = d.listUser(ctx, orgCfg.Org)
	}
	if err != nil {
		return nil, err
	}
	return d.filter(orgCfg, all), nil
}

// ownerType returns the cached account type of owner, looking it up with
// GET /users/{owner} on first use. Failed lookups are not cached.
func (d *Discoverer) ownerType(ctx context.Context, owner string) (ownerType, error) {
	d.ownerMu.Lock()
	typ, ok := d.ownerTypes[owner]
	d.ownerMu.Unlock()
	if ok {
		return typ, nil
	}

	u, _, err := d.client.Users.Get(ctx, owner)
	if err != nil {
		var ghErr *github.ErrorResponse
		if errors.As(err, &ghErr) && ghErr.Response != nil && ghErr.Response.StatusCode == http.StatusNotFound {
			return "", fmt.Errorf("target %q: owner %q not found (GET /users/%s returned 404)", d.target.Name, owner, owner)
		}
		return "", fmt.Errorf("target %q: looking up owner %q: %w", d.target.Name, owner, err)
	}

	switch ownerType(u.GetType()) {
	case ownerOrganization:
		typ = ownerOrganization
	case ownerUser:
		typ = ownerUser
	default:
		return "", fmt.Errorf("target %q: owner %q has unsupported account type %q", d.target.Name, owner, u.GetType())
	}

	d.ownerMu.Lock()
	d.ownerTypes[owner] = typ
	d.ownerMu.Unlock()
	return typ, nil
}

// listOrg pages GET /orgs/{org}/repos?type=all.
func (d *Discoverer) listOrg(ctx context.Context, org string) ([]*github.Repository, error) {
	var all []*github.Repository
	opts := &github.RepositoryListByOrgOptions{
		Type:        "all",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		page, resp, err := d.client.Repositories.ListByOrg(ctx, org, opts)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if resp.NextPage == 0 {
			return all, nil
		}
		opts.Page = resp.NextPage
	}
}

// listUser lists a user account's repos with a PAT. When the PAT belongs to
// the user, GET /user/repos?affiliation=owner includes private repos;
// otherwise GET /users/{user}/repos returns public repos only.
func (d *Discoverer) listUser(ctx context.Context, user string) ([]*github.Repository, error) {
	self, err := d.patLogin(ctx)
	if err != nil {
		return nil, err
	}

	var all []*github.Repository
	if strings.EqualFold(self, user) {
		opts := &github.RepositoryListByAuthenticatedUserOptions{
			Affiliation: "owner",
			ListOptions: github.ListOptions{PerPage: 100},
		}
		for {
			page, resp, err := d.client.Repositories.ListByAuthenticatedUser(ctx, opts)
			if err != nil {
				return nil, err
			}
			all = append(all, page...)
			if resp.NextPage == 0 {
				return all, nil
			}
			opts.Page = resp.NextPage
		}
	}

	d.logger.Info("user account is not the token owner, discovering public repos only",
		"target", d.target.Name, "owner", user, "token_owner", self)
	opts := &github.RepositoryListByUserOptions{
		Type:        "owner",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		page, resp, err := d.client.Repositories.ListByUser(ctx, user, opts)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if resp.NextPage == 0 {
			return all, nil
		}
		opts.Page = resp.NextPage
	}
}

// patLogin returns the cached login of the PAT owner (GET /user).
func (d *Discoverer) patLogin(ctx context.Context) (string, error) {
	d.ownerMu.Lock()
	login := d.selfLogin
	d.ownerMu.Unlock()
	if login != "" {
		return login, nil
	}

	u, _, err := d.client.Users.Get(ctx, "")
	if err != nil {
		return "", fmt.Errorf("target %q: looking up token owner: %w", d.target.Name, err)
	}

	d.ownerMu.Lock()
	d.selfLogin = u.GetLogin()
	d.ownerMu.Unlock()
	return u.GetLogin(), nil
}

// listInstallation lists the GitHub App installation's repos
// (GET /installation/repositories) and keeps those owned by owner.
func (d *Discoverer) listInstallation(ctx context.Context, owner string) ([]*github.Repository, error) {
	var all []*github.Repository
	opts := &github.ListOptions{PerPage: 100}
	for {
		page, resp, err := d.client.Apps.ListRepos(ctx, opts)
		if err != nil {
			return nil, err
		}
		for _, r := range page.Repositories {
			if strings.EqualFold(r.GetOwner().GetLogin(), owner) {
				all = append(all, r)
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	if len(all) == 0 {
		d.logger.Warn("GitHub App installation has no repos owned by this user account",
			"target", d.target.Name, "owner", owner)
	}
	return all, nil
}

// filter applies archived/fork exclusion and the include/exclude globs.
func (d *Discoverer) filter(orgCfg config.OrgConfig, all []*github.Repository) []Repo {
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
	return repos
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
