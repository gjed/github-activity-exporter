package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// PermissionCheck describes what a target reads, which decides the GitHub
// permissions its credential needs.
type PermissionCheck struct {
	// Target names the target in log lines.
	Target string
	// Owners are the accounts the target collects from: its orgs entries,
	// or the owners of its explicit repos.
	Owners []string
	// Dashboard is true when the Dependency Dashboard collector is enabled.
	Dashboard bool
}

// requirement is one permission a collector needs, with the GraphQL field a
// fine-grained PAT is probed with. alias names the field in the probe query.
type requirement struct {
	permission string // GitHub App / fine-grained PAT permission, read access
	collector  string
	alias      string
	field      string
}

// requirements returns the permissions the target's collectors need. The PR
// and issue collectors are always on. Metadata is needed by discovery; it is
// granted to every App installation and fine-grained PAT.
func (p PermissionCheck) requirements() []requirement {
	reqs := []requirement{
		{"metadata", "repo discovery", "", ""},
		{"pull_requests", "PR collector", "pulls", "pullRequests(first: 1) { totalCount }"},
		{"issues", "issue collector", "issues", "issues(first: 1) { totalCount }"},
	}
	if p.Dashboard {
		reqs = append(reqs, requirement{"issues", "Dependency Dashboard collector", "dashboard", "issues(first: 1, states: OPEN) { totalCount }"})
	}
	return reqs
}

// classicRepoScope is the classic PAT scope that grants read access to
// private repos; public repos need no scope.
const classicRepoScope = "repo"

// CheckPermissions verifies that the client's credential grants every
// permission p needs, and returns an error naming each missing one.
//
// A GitHub App is checked against the permissions of its installation
// token, with no extra request. Any other credential is sent GET /user: a
// response with X-OAuth-Scopes (classic PAT, OAuth token) must list the repo
// scope; without it (fine-grained PAT) the GraphQL fields the collectors read
// are probed on one private repo per owner. Requests go through the client's
// rate limit transport.
func (c *Client) CheckPermissions(ctx context.Context, p PermissionCheck) error {
	reqs := p.requirements()
	if app, ok := c.auth.(*AppAuthenticator); ok {
		return c.checkApp(ctx, app, p.Target, reqs)
	}
	_, resp, err := c.rest.Users.Get(ctx, "")
	if err != nil {
		return fmt.Errorf("permission check: GET /user: %w", err)
	}
	if scopes, ok := resp.Header[http.CanonicalHeaderKey("X-OAuth-Scopes")]; ok {
		header := strings.Join(scopes, ",")
		if err := checkScopes(header); err != nil {
			return err
		}
		c.logger.Info("GitHub permissions checked", "target", p.Target, "credential", "classic token", "scopes", header)
		return nil
	}
	return c.probeFineGrained(ctx, p.Target, p.Owners, reqs)
}

// checkApp checks the installation's granted permissions; write and admin
// satisfy read.
func (c *Client) checkApp(ctx context.Context, app *AppAuthenticator, target string, reqs []requirement) error {
	grants, err := app.Grants(ctx)
	if err != nil {
		return fmt.Errorf("permission check: %w", err)
	}
	var missing []requirement
	for _, r := range reqs {
		switch grants.Permissions[r.permission] {
		case "read", "write", "admin":
		default:
			missing = append(missing, r)
		}
	}
	if err := missingError("GitHub App installation", missing); err != nil {
		return err
	}
	c.logger.Info("GitHub permissions checked", "target", target, "credential", "GitHub App",
		"repository_selection", grants.RepositorySelection, "permissions", grants.Permissions)
	return nil
}

// checkScopes checks a classic token's X-OAuth-Scopes value.
func checkScopes(header string) error {
	var granted []string
	for s := range strings.SplitSeq(header, ",") {
		if s = strings.TrimSpace(s); s != "" {
			granted = append(granted, s)
		}
	}
	if slices.Contains(granted, classicRepoScope) {
		return nil
	}
	return fmt.Errorf("classic token missing OAuth scope: %s, needed to read private repos (granted: %s)",
		classicRepoScope, strings.Join(granted, ", "))
}

// probeOwner is one repositoryOwner selection of the fine-grained PAT probe
// query; nodes holds at most one private repo.
type probeOwner struct {
	Repositories struct {
		Nodes []*struct {
			NameWithOwner string `json:"nameWithOwner"`
		} `json:"nodes"`
	} `json:"repositories"`
}

// probeRepoAlias is the probe query selection that names the probed repo.
const probeRepoAlias = "repo"

// privateRepo selects one private repo of $owner with the given fields.
func privateRepo(alias, fields string) string {
	return alias + `: repositoryOwner(login: $owner) { repositories(first: 1, privacy: PRIVATE) { nodes { ` + fields + ` } } }`
}

// probeFineGrained reads each probe field on one private repo of each owner
// the token can see. Every token reads public repos, so an owner with no
// private repo visible to the token has nothing to check. A FORBIDDEN error
// on a field means its permission is missing.
//
// Each field sits under its own repositoryOwner alias: GitHub nulls the repo
// node on the first forbidden field and reports no error for the others in
// the same node, so one shared node would name only one missing permission.
func (c *Client) probeFineGrained(ctx context.Context, target string, owners []string, reqs []requirement) error {
	selections := []string{privateRepo(probeRepoAlias, "nameWithOwner")}
	byAlias := map[string]requirement{}
	for _, r := range reqs {
		if r.alias == "" {
			continue
		}
		selections = append(selections, privateRepo(r.alias, r.field))
		byAlias[r.alias] = r
	}
	query := `query($owner: String!) { ` + strings.Join(selections, " ") + ` }`

	var missing []requirement
	var probed []string
	for _, owner := range owners {
		resp, err := c.QueryRaw(ctx, query, map[string]any{"owner": owner})
		if err != nil {
			return fmt.Errorf("permission check: probing owner %q: %w", owner, err)
		}
		var repo *probeOwner
		if raw, ok := resp.Data[probeRepoAlias]; ok {
			_ = json.Unmarshal(raw, &repo)
		}
		if repo != nil && len(repo.Repositories.Nodes) > 0 && repo.Repositories.Nodes[0] != nil {
			probed = append(probed, repo.Repositories.Nodes[0].NameWithOwner)
		} else if len(resp.Errors) == 0 {
			c.logger.Info("fine-grained token sees no private repo of owner, nothing to check",
				"target", target, "owner", owner)
			continue
		}
		for _, e := range resp.Errors {
			r, ok := byAlias[firstPathKey(e.Path)]
			if e.Type != "FORBIDDEN" || !ok {
				return fmt.Errorf("permission check: probing owner %q: %s (%s)", owner, e.Message, e.Type)
			}
			missing = append(missing, r)
		}
	}
	if err := missingError("fine-grained token, probed on "+strings.Join(probed, ", "), missing); err != nil {
		return err
	}
	c.logger.Info("GitHub permissions checked", "target", target, "credential", "fine-grained token", "probed", probed)
	return nil
}

// firstPathKey returns the top-level field or alias of a GraphQL error path.
func firstPathKey(path []any) string {
	if len(path) == 0 {
		return ""
	}
	s, _ := path[0].(string)
	return s
}

// missingError names every missing permission once, with the collectors
// that need it, or returns nil when none is missing.
func missingError(source string, missing []requirement) error {
	if len(missing) == 0 {
		return nil
	}
	var perms []string
	users := map[string][]string{}
	for _, r := range missing {
		if _, ok := users[r.permission]; !ok {
			perms = append(perms, r.permission)
		}
		if !slices.Contains(users[r.permission], r.collector) {
			users[r.permission] = append(users[r.permission], r.collector)
		}
	}
	parts := make([]string, len(perms))
	for i, p := range perms {
		parts[i] = fmt.Sprintf("%s: read (%s)", p, strings.Join(users[p], ", "))
	}
	return fmt.Errorf("missing GitHub permissions: %s (%s)", strings.Join(parts, ", "), source)
}
