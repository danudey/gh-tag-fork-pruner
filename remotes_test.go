package main

import (
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/repository"
)

func TestParseRemoteURL(t *testing.T) {
	cases := []struct {
		in                string
		host, owner, name string
		wantErr           bool
	}{
		{in: "https://github.com/cli/cli.git", host: "github.com", owner: "cli", name: "cli"},
		{in: "https://github.com/cli/cli", host: "github.com", owner: "cli", name: "cli"},
		{in: "https://github.com/cli/cli/", host: "github.com", owner: "cli", name: "cli"},
		{in: "git@github.com:cli/cli.git", host: "github.com", owner: "cli", name: "cli"},
		{in: "git@github.com:cli/cli", host: "github.com", owner: "cli", name: "cli"},
		{in: "ssh://git@github.com/cli/cli.git", host: "github.com", owner: "cli", name: "cli"},
		{in: "ssh://git@ssh.github.com:443/cli/cli.git", host: "ssh.github.com", owner: "cli", name: "cli"},
		{in: "git://github.com/cli/cli.git", host: "github.com", owner: "cli", name: "cli"},
		{in: "https://ghes.example.com/org/repo.git", host: "ghes.example.com", owner: "org", name: "repo"},
		{in: "https://user:token@github.com/cli/cli.git", host: "github.com", owner: "cli", name: "cli"},
		{in: "/srv/git/bare.git", wantErr: true},
		{in: "https://github.com/cli", wantErr: true},
		{in: "", wantErr: true},
	}

	for _, c := range cases {
		got, err := parseRemoteURL(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseRemoteURL(%q) = %+v, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseRemoteURL(%q) returned error: %v", c.in, err)
			continue
		}
		if got.Host != c.host || got.Owner != c.owner || got.Name != c.name {
			t.Errorf("parseRemoteURL(%q) = %s/%s/%s, want %s/%s/%s",
				c.in, got.Host, got.Owner, got.Name, c.host, c.owner, c.name)
		}
	}
}

func remote(name, owner, repoName, resolved string) gitRemote {
	return gitRemote{
		Name:     name,
		Repo:     repository.Repository{Host: "github.com", Owner: owner, Name: repoName},
		Resolved: resolved,
	}
}

func TestResolveDefaultRepoNoDefault(t *testing.T) {
	_, _, err := resolveDefaultRepo([]gitRemote{
		remote("origin", "danudey", "cli", ""),
		remote("upstream", "cli", "cli", ""),
	})
	if err == nil {
		t.Fatal("expected an error when no default repo is set")
	}
	if !strings.Contains(err.Error(), "gh repo set-default") {
		t.Errorf("error should tell the user how to fix it, got: %v", err)
	}
}

func TestResolveDefaultRepoMultipleBase(t *testing.T) {
	_, _, err := resolveDefaultRepo([]gitRemote{
		remote("origin", "danudey", "cli", "base"),
		remote("upstream", "cli", "cli", "base"),
	})
	if err == nil {
		t.Fatal("expected an error when two remotes are marked base")
	}
	msg := err.Error()
	for _, want := range []string{"origin", "upstream", "git config --unset remote.upstream.gh-resolved", "gh repo set-default"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %q, got:\n%s", want, msg)
		}
	}
}

func TestResolveDefaultRepoSingleBase(t *testing.T) {
	repo, from, err := resolveDefaultRepo([]gitRemote{
		remote("origin", "danudey", "cli", ""),
		remote("upstream", "cli", "cli", "base"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repoKey(repo) != "github.com/cli/cli" {
		t.Errorf("got base repo %s, want github.com/cli/cli", repoKey(repo))
	}
	if from == nil || from.Name != "upstream" {
		t.Errorf("got source remote %v, want upstream", from)
	}
}

func TestResolveDefaultRepoNamedValue(t *testing.T) {
	repo, from, err := resolveDefaultRepo([]gitRemote{
		remote("origin", "danudey", "cli", "octocat/cli"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repoKey(repo) != "github.com/octocat/cli" {
		t.Errorf("got base repo %s, want github.com/octocat/cli", repoKey(repo))
	}
	if from != nil {
		t.Errorf("base repo is not one of the remotes, got source remote %q", from.Name)
	}
}

func TestResolveDefaultRepoNamedValueMatchingRemote(t *testing.T) {
	repo, from, err := resolveDefaultRepo([]gitRemote{
		remote("origin", "danudey", "cli", "cli/cli"),
		remote("upstream", "cli", "cli", ""),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repoKey(repo) != "github.com/cli/cli" {
		t.Errorf("got base repo %s, want github.com/cli/cli", repoKey(repo))
	}
	if from == nil || from.Name != "upstream" {
		t.Errorf("base repo should be attributed to the upstream remote, got %v", from)
	}
}

func TestResolveDefaultRepoConflictingNamedValues(t *testing.T) {
	_, _, err := resolveDefaultRepo([]gitRemote{
		remote("origin", "danudey", "cli", "cli/cli"),
		remote("other", "danudey", "cli", "octocat/cli"),
	})
	if err == nil {
		t.Fatal("expected an error when remotes disagree about the base repo")
	}
	if !strings.Contains(err.Error(), "gh repo set-default") {
		t.Errorf("error should tell the user how to fix it, got: %v", err)
	}
}

func TestResolveDefaultRepoDuplicateNamedValues(t *testing.T) {
	repo, _, err := resolveDefaultRepo([]gitRemote{
		remote("origin", "danudey", "cli", "cli/cli"),
		remote("other", "someone", "cli", "cli/cli"),
	})
	if err != nil {
		t.Fatalf("duplicate identical values should not conflict: %v", err)
	}
	if repoKey(repo) != "github.com/cli/cli" {
		t.Errorf("got base repo %s, want github.com/cli/cli", repoKey(repo))
	}
}
