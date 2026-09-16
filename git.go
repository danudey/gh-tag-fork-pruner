package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

func git(args ...string) (string, error) {
	// The executable is the literal "git" and the arguments are an argv slice,
	// so nothing here reaches a shell. Every caller builds args from literals
	// or from values git itself reported.
	//
	// No context either: every command routed through here is a local, fast
	// one — rev-parse, config, tag --list, remote get-url. The single git
	// command that touches the network is ls-remote, which uses
	// CommandContext with a timeout in lsremote.go.
	// #nosec G204 -- argv, never a shell
	cmd := exec.Command("git", args...) //nolint:noctx // local git only; see above
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

func gitLines(args ...string) ([]string, error) {
	out, err := git(args...)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

func inGitRepo() bool {
	_, err := git("rev-parse", "--git-dir")
	return err == nil
}

func listLocalTags() ([]string, error) {
	return gitLines("tag", "--list")
}

// noTagsOpt is the remote.<name>.tagopt value that stops git fetching tags
// from a remote unless they are asked for by name.
const noTagsOpt = "--no-tags"

// remoteTagOpt reports the remote.<name>.tagopt setting, or "" when unset.
func remoteTagOpt(remote string) string {
	out, err := git("config", "--get", fmt.Sprintf("remote.%s.tagopt", remote))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func setRemoteNoTags(remote string) error {
	_, err := git("config", fmt.Sprintf("remote.%s.tagopt", remote), noTagsOpt)
	return err
}

// deleteLocalTags removes tags in chunks so a large prune cannot blow the
// command-line argument limit.
func deleteLocalTags(tags []string) error {
	const chunk = 200
	for start := 0; start < len(tags); start += chunk {
		end := start + chunk
		if end > len(tags) {
			end = len(tags)
		}
		// Tag names come from the remote, and a ref named "-f" is valid enough
		// for GitHub to hold even though git refuses to create one locally, so
		// stop anything after "--" being read as an option.
		args := append([]string{"tag", "-d", "--"}, tags[start:end]...)
		if _, err := git(args...); err != nil {
			return err
		}
	}
	return nil
}
