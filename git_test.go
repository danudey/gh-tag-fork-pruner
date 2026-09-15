package main

import "testing"

// newTestRepo creates an empty repo in a temp dir and makes it the working
// directory for the duration of the test.
func newTestRepo(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	if _, err := git("init", "--quiet"); err != nil {
		t.Fatalf("git init: %v", err)
	}
}

func TestRemoteTagOpt(t *testing.T) {
	newTestRepo(t)
	if _, err := git("remote", "add", "fork", "git@github.com:danudey/cli.git"); err != nil {
		t.Fatalf("git remote add: %v", err)
	}

	if got := remoteTagOpt("fork"); got != "" {
		t.Errorf("unset tagopt returned %q, want empty", got)
	}
	if got := remoteTagOpt("nonexistent"); got != "" {
		t.Errorf("unknown remote returned %q, want empty", got)
	}

	if err := setRemoteNoTags("fork"); err != nil {
		t.Fatalf("setRemoteNoTags: %v", err)
	}
	if got := remoteTagOpt("fork"); got != noTagsOpt {
		t.Errorf("tagopt = %q, want %q", got, noTagsOpt)
	}

	// Setting it again is a no-op rather than appending a second value.
	if err := setRemoteNoTags("fork"); err != nil {
		t.Fatalf("second setRemoteNoTags: %v", err)
	}
	if got := remoteTagOpt("fork"); got != noTagsOpt {
		t.Errorf("tagopt after re-set = %q, want %q", got, noTagsOpt)
	}
}

func TestSetRemoteNoTagsOverwritesExisting(t *testing.T) {
	newTestRepo(t)
	if _, err := git("remote", "add", "fork", "git@github.com:danudey/cli.git"); err != nil {
		t.Fatalf("git remote add: %v", err)
	}
	if _, err := git("config", "remote.fork.tagopt", "--tags"); err != nil {
		t.Fatalf("git config: %v", err)
	}

	if err := setRemoteNoTags("fork"); err != nil {
		t.Fatalf("setRemoteNoTags: %v", err)
	}
	if got := remoteTagOpt("fork"); got != noTagsOpt {
		t.Errorf("tagopt = %q, want %q", got, noTagsOpt)
	}
}

func TestDeleteLocalTags(t *testing.T) {
	newTestRepo(t)
	if _, err := git("commit", "--quiet", "--allow-empty", "-m", "root"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	for _, name := range []string{"keep", "drop-a", "drop-b"} {
		if _, err := git("tag", name); err != nil {
			t.Fatalf("git tag %s: %v", name, err)
		}
	}

	if err := deleteLocalTags([]string{"drop-a", "drop-b"}); err != nil {
		t.Fatalf("deleteLocalTags: %v", err)
	}

	tags, err := listLocalTags()
	if err != nil {
		t.Fatalf("listLocalTags: %v", err)
	}
	if len(tags) != 1 || tags[0] != "keep" {
		t.Errorf("remaining tags = %v, want [keep]", tags)
	}
}
