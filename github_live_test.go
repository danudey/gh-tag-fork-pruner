package main

import (
	"os"
	"testing"

	"github.com/cli/go-gh/v2/pkg/repository"
)

// Live tests hit the real GitHub API read-only. They need a working gh auth
// token, so they are opt-in.
func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv("GH_TAG_FORK_PRUNER_LIVE") == "" {
		t.Skip("set GH_TAG_FORK_PRUNER_LIVE=1 to run tests that call the GitHub API")
	}
}

var helmRepo = repository.Repository{Host: "github.com", Owner: "helm", Name: "helm"}

func TestFetchRefIDsLive(t *testing.T) {
	requireLive(t)

	names := []string{"v3.16.2", "definitely-not-a-tag", "v3.16.1"}
	found, missing, err := fetchRefIDs(newClientSet(), helmRepo, names, 50, nil)
	if err != nil {
		t.Fatalf("fetchRefIDs: %v", err)
	}

	if len(found) != 2 {
		t.Fatalf("found %d refs, want 2: %+v", len(found), found)
	}
	for _, ref := range found {
		if ref.ID == "" {
			t.Errorf("%s resolved to an empty ID", ref.Name)
		}
	}
	// Order must line up with the names that resolved, not the input order.
	if found[0].Name != "v3.16.2" || found[1].Name != "v3.16.1" {
		t.Errorf("got names %s and %s, want v3.16.2 and v3.16.1", found[0].Name, found[1].Name)
	}
	if len(missing) != 1 || missing[0] != "definitely-not-a-tag" {
		t.Errorf("missing = %v, want [definitely-not-a-tag]", missing)
	}
}

// Batching must not change the result, and a batch boundary must not drop or
// misalign a name.
func TestFetchRefIDsBatchingLive(t *testing.T) {
	requireLive(t)

	names := []string{"v3.16.2", "v3.16.1", "v3.16.0", "nope-1", "v3.15.0"}
	found, missing, err := fetchRefIDs(newClientSet(), helmRepo, names, 2, nil)
	if err != nil {
		t.Fatalf("fetchRefIDs: %v", err)
	}
	if len(found) != 4 {
		t.Errorf("found %d refs across batches, want 4: %+v", len(found), found)
	}
	if len(missing) != 1 || missing[0] != "nope-1" {
		t.Errorf("missing = %v, want [nope-1]", missing)
	}
	for _, ref := range found {
		if ref.Name == "nope-1" {
			t.Error("a missing name was reported as found")
		}
	}
}

// The ls-remote path and the API path must agree on the tag set.
func TestLsRemoteMatchesAPILive(t *testing.T) {
	requireLive(t)

	viaGit, err := lsRemoteTags(t.Context(), "https://github.com/helm/helm.git")
	if err != nil {
		t.Fatalf("lsRemoteTags: %v", err)
	}
	viaAPI, err := listTags(newClientSet(), helmRepo, nil)
	if err != nil {
		t.Fatalf("listTags: %v", err)
	}

	gitSet := map[string]bool{}
	for _, n := range viaGit {
		gitSet[n] = true
	}
	apiSet := map[string]bool{}
	for _, r := range viaAPI {
		apiSet[r.Name] = true
	}

	for n := range apiSet {
		if !gitSet[n] {
			t.Errorf("%q came from the API but not from ls-remote", n)
		}
	}
	for n := range gitSet {
		if !apiSet[n] {
			t.Errorf("%q came from ls-remote but not from the API", n)
		}
	}
}
