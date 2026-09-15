package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// lsRemoteTimeout bounds the ls-remote call. The point of using it is speed, so
// a stalled connection should fall back to the API rather than hang.
const lsRemoteTimeout = 30 * time.Second

// lsRemoteTags returns every tag name a remote advertises, in one round trip.
//
// This is the fast path for listing: git ls-remote costs a single request no
// matter how many tags exist, while the GraphQL API pages 100 at a time. It
// returns no node IDs, so deletions still resolve those separately.
func lsRemoteTags(ctx context.Context, url string) ([]string, error) {
	// Remote URLs come from .git/config, which a checkout can arrive carrying.
	// A value like "--upload-pack=...@github.com:a/b" parses as a plausible
	// owner/repo but reaches git as an option rather than a URL, so refuse it
	// outright on top of the end-of-options marker below.
	if strings.HasPrefix(url, "-") {
		return nil, fmt.Errorf("refusing remote URL %q: it would be read as a command-line option", url)
	}

	ctx, cancel := context.WithTimeout(ctx, lsRemoteTimeout)
	defer cancel()

	// --refs drops the peeled "^{}" entries that annotated tags would otherwise
	// add, leaving one line per tag. "--" stops anything after it being taken
	// as an option.
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--tags", "--refs", "--", url)

	// ls-remote authenticates as git, not as gh, so a remote needing
	// credentials we do not have must fail rather than block on a prompt.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -oBatchMode=yes")
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = nil

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("timed out after %s", lsRemoteTimeout)
		}
		if msg := firstLine(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s", msg)
		}
		return nil, err
	}

	return parseLsRemoteTags(stdout.String()), nil
}

// parseLsRemoteTags pulls tag names out of "<sha>\trefs/tags/<name>" lines.
func parseLsRemoteTags(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		_, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		name, ok := strings.CutPrefix(ref, "refs/tags/")
		if !ok || name == "" {
			continue
		}
		// Defensive: --refs should already have removed these.
		if strings.HasSuffix(name, "^{}") {
			continue
		}
		names = append(names, name)
	}
	return names
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}
