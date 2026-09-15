package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/repository"
)

func TestParseLsRemoteTags(t *testing.T) {
	out := strings.Join([]string{
		"4d7c681ba0aa43b45d122faa998c243422019be4\trefs/tags/1.999.0",
		"e1c35b2e9d2755c94ec19dbb1b23e2759cd85789\trefs/tags/v1.0",
		// A peeled entry, in case --refs is unavailable or ignored.
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\trefs/tags/v1.0^{}",
		// Branches and other refs must not be picked up.
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\trefs/heads/main",
		"cccccccccccccccccccccccccccccccccccccccc\tHEAD",
		// Tag names may contain slashes.
		"dddddddddddddddddddddddddddddddddddddddd\trefs/tags/release/2.0",
		"",
		"   ",
	}, "\n")

	got := parseLsRemoteTags(out)
	want := []string{"1.999.0", "v1.0", "release/2.0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseLsRemoteTags = %v, want %v", got, want)
	}
}

func TestParseLsRemoteTagsEmpty(t *testing.T) {
	if got := parseLsRemoteTags(""); len(got) != 0 {
		t.Errorf("empty output gave %v, want nothing", got)
	}
}

func TestLsRemoteTagsFailsFast(t *testing.T) {
	// A path that is not a repository must return an error rather than block
	// on a credential prompt, so the caller can fall back to the API.
	_, err := lsRemoteTags(t.Context(), t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a non-repository path")
	}
}

func TestBuildRefIDQuery(t *testing.T) {
	repo := repository.Repository{Host: "github.com", Owner: "helm", Name: "helm"}
	query, vars := buildRefIDQuery(repo, []string{"v1.0", "weird\"name"})

	for _, want := range []string{
		"query RefIDs($owner: String!, $name: String!, $q0: String!, $q1: String!)",
		"repository(owner: $owner, name: $name)",
		"t0: ref(qualifiedName: $q0) { id name }",
		"t1: ref(qualifiedName: $q1) { id name }",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("query is missing %q:\n%s", want, query)
		}
	}

	if vars["owner"] != "helm" || vars["name"] != "helm" {
		t.Errorf("got owner/name %v/%v, want helm/helm", vars["owner"], vars["name"])
	}
	if vars["q0"] != "refs/tags/v1.0" || vars["q1"] != `refs/tags/weird"name` {
		t.Errorf("got qualified names %v and %v", vars["q0"], vars["q1"])
	}

	// Tag names must travel as variables so a quote cannot break the query.
	if strings.Contains(query, "v1.0") || strings.Contains(query, "weird") {
		t.Errorf("tag names leaked into the query text:\n%s", query)
	}
}

func TestTagListingResolveUsesCachedIDs(t *testing.T) {
	listing := tagListing{
		names:  []string{"a", "b"},
		ids:    map[string]string{"a": "REF_a", "b": "REF_b"},
		source: "api",
	}

	found, missing, err := listing.resolve(nil, repository.Repository{}, []string{"a", "gone"}, 50, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(found) != 1 || found[0].ID != "REF_a" {
		t.Errorf("found = %+v, want one ref with ID REF_a", found)
	}
	if len(missing) != 1 || missing[0] != "gone" {
		t.Errorf("missing = %v, want [gone]", missing)
	}
}

func TestRemoteURLFor(t *testing.T) {
	repo := repository.Repository{Host: "github.com", Owner: "cli", Name: "cli"}

	r := gitRemote{Name: "origin", URL: "git@github.com:cli/cli.git", Repo: repo}
	if got := remoteURLFor(repo, &r); got != "git@github.com:cli/cli.git" {
		t.Errorf("with a remote, got %q, want the remote's own URL", got)
	}

	if got := remoteURLFor(repo, nil); got != "https://github.com/cli/cli.git" {
		t.Errorf("without a remote, got %q, want a synthesised HTTPS URL", got)
	}
}

func TestLsRemoteTagsRejectsOptionLikeURL(t *testing.T) {
	// A crafted .git/config value can parse as a plausible owner/repo while
	// reaching git as an option instead of a URL.
	for _, url := range []string{
		"--upload-pack=touch /tmp/pwned@github.com:a/b",
		"-x@github.com:a/b",
		"--help",
	} {
		_, err := lsRemoteTags(t.Context(), url)
		if err == nil {
			t.Errorf("lsRemoteTags(%q) succeeded, want a refusal", url)
			continue
		}
		if !strings.Contains(err.Error(), "command-line option") {
			t.Errorf("lsRemoteTags(%q) error = %v, want it refused as option-like", url, err)
		}
	}
}
