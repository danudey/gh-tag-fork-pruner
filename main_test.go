package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

// parseArgs runs the command far enough to populate opts, stopping before the
// real action so no git or API calls happen.
func parseArgs(t *testing.T, args ...string) (options, error) {
	t.Helper()
	opts := options{}
	cmd := newCommand(&opts)
	cmd.Action = func(context.Context, *cli.Command) error { return nil }
	cmd.Writer = io.Discard
	cmd.ErrWriter = io.Discard
	err := cmd.Run(context.Background(), append([]string{"gh-tag-fork-pruner"}, args...))
	return opts, err
}

func TestFlagDefaults(t *testing.T) {
	opts, err := parseArgs(t)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.confirm || opts.skipLocal || opts.disableTagFetch || opts.verbose {
		t.Errorf("boolean flags should default to false, got %+v", opts)
	}
	if opts.batchSize != defaultBatchSize {
		t.Errorf("batchSize = %d, want %d", opts.batchSize, defaultBatchSize)
	}
}

func TestFlagsSet(t *testing.T) {
	opts, err := parseArgs(t, "--confirm", "--skip-local", "--disable-tag-fetch", "--verbose", "--batch-size", "10")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !opts.confirm || !opts.skipLocal || !opts.disableTagFetch || !opts.verbose {
		t.Errorf("boolean flags should all be true, got %+v", opts)
	}
	if opts.batchSize != 10 {
		t.Errorf("batchSize = %d, want 10", opts.batchSize)
	}
}

func TestBatchSizeMustBePositive(t *testing.T) {
	for _, arg := range []string{"0", "-5"} {
		if _, err := parseArgs(t, "--batch-size", arg); err == nil {
			t.Errorf("--batch-size %s should be rejected", arg)
		} else if !strings.Contains(err.Error(), "at least 1") {
			t.Errorf("--batch-size %s error = %v, want it to mention the minimum", arg, err)
		}
	}
}

func TestUnknownFlagRejected(t *testing.T) {
	if _, err := parseArgs(t, "--definitely-not-a-flag"); err == nil {
		t.Error("unknown flag should be rejected")
	}
}

func TestPositionalArgumentRejected(t *testing.T) {
	_, err := parseArgs(t, "extra")
	if err == nil {
		t.Fatal("positional argument should be rejected")
	}
	if !strings.Contains(err.Error(), "unexpected argument") {
		t.Errorf("error = %v, want it to name the unexpected argument", err)
	}
}

func TestPrintTagList(t *testing.T) {
	names := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("v%d", i)
		}
		return out
	}

	cases := []struct {
		name      string
		count     int
		verbose   bool
		wantLines int
		wantText  string
	}{
		{name: "empty prints nothing", count: 0, wantLines: 0},
		{name: "short list prints in full", count: 3, wantLines: 3, wantText: "v0"},
		{name: "at the cap prints in full", count: maxListed, wantLines: maxListed, wantText: "v9"},
		{name: "over the cap prints a count only", count: maxListed + 1, wantLines: 1, wantText: "11 tags, too many to list"},
		{name: "verbose prints them all", count: 50, verbose: true, wantLines: 50, wantText: "v49"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf strings.Builder
			printTagList(&buf, names(c.count), c.verbose)

			got := buf.String()
			lines := 0
			if got != "" {
				lines = strings.Count(got, "\n")
			}
			if lines != c.wantLines {
				t.Errorf("printed %d line(s), want %d:\n%s", lines, c.wantLines, got)
			}
			if c.wantText != "" && !strings.Contains(got, c.wantText) {
				t.Errorf("output is missing %q:\n%s", c.wantText, got)
			}
			// A suppressed list must not leak any tag names.
			if c.count > maxListed && !c.verbose && strings.Contains(got, "v0") {
				t.Errorf("suppressed list still printed tag names:\n%s", got)
			}
		})
	}
}
