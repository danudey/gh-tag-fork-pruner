package main

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"

	"github.com/cli/go-gh/v2/pkg/repository"
)

// gitRemote is a git remote that points at a GitHub repository.
type gitRemote struct {
	Name     string
	URL      string
	Repo     repository.Repository
	Resolved string // value of remote.<name>.gh-resolved, if set
}

func (r gitRemote) String() string {
	return fmt.Sprintf("%s/%s", r.Repo.Owner, r.Repo.Name)
}

// listGitRemotes returns every remote whose fetch URL parses as a GitHub-style
// repository URL, annotated with its gh-resolved setting. Remotes with URLs we
// cannot parse (local paths, unusual transports) are skipped.
func listGitRemotes() ([]gitRemote, error) {
	names, err := gitLines("remote")
	if err != nil {
		return nil, err
	}

	resolved := map[string]string{}
	// A repo with no gh-resolved keys makes --get-regexp exit 1; that is not an
	// error for us, it just means no default repo has been set.
	if lines, err := gitLines("config", "--get-regexp", `^remote\..*\.gh-resolved$`); err == nil {
		for _, line := range lines {
			key, value, ok := strings.Cut(line, " ")
			if !ok {
				continue
			}
			name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".gh-resolved")
			resolved[name] = strings.TrimSpace(value)
		}
	}

	var remotes []gitRemote
	for _, name := range names {
		out, err := git("remote", "get-url", name)
		if err != nil {
			continue
		}
		rawURL := strings.TrimSpace(out)
		repo, err := parseRemoteURL(rawURL)
		if err != nil {
			continue
		}
		remotes = append(remotes, gitRemote{
			Name:     name,
			URL:      rawURL,
			Repo:     repo,
			Resolved: resolved[name],
		})
	}
	return remotes, nil
}

// parseRemoteURL turns a git remote URL into a repository, accepting both
// scheme URLs (https://host/owner/repo.git) and scp-style (git@host:owner/repo).
func parseRemoteURL(raw string) (repository.Repository, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return repository.Repository{}, fmt.Errorf("empty remote URL")
	}

	var host, path string
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return repository.Repository{}, fmt.Errorf("unrecognized remote URL %q: %w", raw, err)
		}
		host, path = u.Host, u.Path
	} else {
		rest := s
		if at := strings.LastIndex(rest, "@"); at >= 0 && !strings.Contains(rest[:at], "/") {
			rest = rest[at+1:]
		}
		colon := strings.Index(rest, ":")
		if colon < 0 {
			return repository.Repository{}, fmt.Errorf("unrecognized remote URL %q", raw)
		}
		host, path = rest[:colon], rest[colon+1:]
	}

	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimPrefix(host, "www."))
	if host == "" {
		return repository.Repository{}, fmt.Errorf("no host in remote URL %q", raw)
	}

	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[len(parts)-1] == "" || parts[len(parts)-2] == "" {
		return repository.Repository{}, fmt.Errorf("no owner/repo in remote URL %q", raw)
	}

	return repository.Repository{
		Host:  host,
		Owner: parts[len(parts)-2],
		Name:  parts[len(parts)-1],
	}, nil
}

// resolveDefaultRepo determines the base repository the user selected with
// `gh repo set-default`. It returns the repo plus the remote it came from, if
// the base repo is one of the remotes.
func resolveDefaultRepo(remotes []gitRemote) (repository.Repository, *gitRemote, error) {
	var baseRemotes []gitRemote // gh-resolved = base
	var named []gitRemote       // gh-resolved = OWNER/REPO
	for _, r := range remotes {
		switch {
		case r.Resolved == "":
		case r.Resolved == "base":
			baseRemotes = append(baseRemotes, r)
		default:
			named = append(named, r)
		}
	}

	if len(baseRemotes) > 1 {
		var b strings.Builder
		b.WriteString("more than one remote is marked as the base repository:\n")
		for _, r := range baseRemotes {
			fmt.Fprintf(&b, "  %-12s %s\n", r.Name, r)
		}
		b.WriteString("\nClear the extra markers and pick one again:\n")
		for _, r := range baseRemotes[1:] {
			fmt.Fprintf(&b, "  git config --unset remote.%s.gh-resolved\n", r.Name)
		}
		b.WriteString("  gh repo set-default")
		return repository.Repository{}, nil, fmt.Errorf("%s", b.String())
	}

	if len(baseRemotes) == 1 {
		r := baseRemotes[0]
		return r.Repo, &r, nil
	}

	if len(named) > 0 {
		// Collapse duplicates before complaining about conflicts: several
		// remotes commonly carry the same gh-resolved value.
		seen := map[string]repository.Repository{}
		for _, r := range named {
			repo, err := repository.ParseWithHost(r.Resolved, r.Repo.Host)
			if err != nil {
				return repository.Repository{}, nil, fmt.Errorf(
					"remote %q has an unreadable gh-resolved value %q; re-run: gh repo set-default",
					r.Name, r.Resolved)
			}
			seen[repoKey(repo)] = repo
		}
		if len(seen) > 1 {
			keys := make([]string, 0, len(seen))
			for k := range seen {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			return repository.Repository{}, nil, fmt.Errorf(
				"remotes disagree about the base repository (%s); re-run: gh repo set-default",
				strings.Join(keys, ", "))
		}
		for _, repo := range seen {
			// The base repo may still be one of the remotes under a different name.
			for i := range remotes {
				if repoKey(remotes[i].Repo) == repoKey(repo) {
					return repo, &remotes[i], nil
				}
			}
			return repo, nil, nil
		}
	}

	return repository.Repository{}, nil, fmt.Errorf(
		"no default repository is set for this checkout.\n\nSet one with:\n  gh repo set-default")
}

func repoKey(r repository.Repository) string {
	return fmt.Sprintf("%s/%s/%s", strings.ToLower(r.Host), strings.ToLower(r.Owner), strings.ToLower(r.Name))
}
