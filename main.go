package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/cli/go-gh/v2/pkg/term"
	"github.com/urfave/cli/v3"
)

const defaultBatchSize = 50

// maxListed is the longest tag list worth printing in full. Past this, a
// truncated list tells the reader nothing useful, so only the count is shown
// unless --verbose asks for the names.
const maxListed = 10

type options struct {
	confirm         bool
	skipLocal       bool
	disableTagFetch bool
	noLsRemote      bool
	batchSize       int
	verbose         bool
}

// ui carries the output destinations and terminal facts the reporting code
// needs, so they do not have to be threaded through as separate arguments.
type ui struct {
	out          io.Writer
	progressOut  io.Writer
	showProgress bool
	termWidth    int
	verbose      bool
}

func main() {
	opts := options{}
	cmd := newCommand(&opts)
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}

// newCommand describes the CLI. The parsed values land in opts, which the
// action then hands to run.
func newCommand(opts *options) *cli.Command {
	return &cli.Command{
		Name:      "gh tag-fork-pruner",
		Usage:     "delete tags from forks that the original repository does not have",
		UsageText: "gh tag-fork-pruner [flags]",
		Description: "The original repository is the default repo for this checkout, as set by\n" +
			`"gh repo set-default". Every other remote that is a fork of it is pruned.` + "\n\n" +
			"Nothing is changed unless --confirm is given.",
		HideHelpCommand: true,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:        "confirm",
				Usage:       "apply the changes (without this, only print what would happen)",
				Destination: &opts.confirm,
			},
			&cli.BoolFlag{
				Name:        "skip-local",
				Usage:       "leave local tags alone",
				Destination: &opts.skipLocal,
			},
			&cli.BoolFlag{
				Name:        "disable-tag-fetch",
				Usage:       "set remote.<name>.tagopt to --no-tags on each fork remote, so git stops fetching its tags (unless specifically asked to)",
				Destination: &opts.disableTagFetch,
			},
			&cli.IntFlag{
				Name:        "batch-size",
				Usage:       "number of tags to delete per GraphQL request",
				Value:       defaultBatchSize,
				Destination: &opts.batchSize,
				Validator: func(n int) error {
					if n < 1 {
						return fmt.Errorf("must be at least 1")
					}
					return nil
				},
			},
			&cli.BoolFlag{
				Name:        "no-ls-remote",
				Usage:       "always list tags through the GitHub API, instead of the faster git ls-remote",
				Destination: &opts.noLsRemote,
			},
			&cli.BoolFlag{
				Name:        "verbose",
				Usage:       "print every tag name, however long the list",
				Destination: &opts.verbose,
			},
		},
		ArgValidator: func(_ context.Context, cmd *cli.Command) error {
			if args := cmd.Args(); args.Len() > 0 {
				return fmt.Errorf("unexpected argument %q; this command takes flags only", args.First())
			}
			return nil
		},
		Action: func(ctx context.Context, _ *cli.Command) error {
			return run(ctx, *opts)
		},
	}
}

func run(ctx context.Context, opts options) error {
	if !inGitRepo() {
		return fmt.Errorf("not inside a git repository")
	}

	t := term.FromEnv()
	// Progress is drawn on stderr, so gate it on stderr being a terminal
	// rather than on stdout, which may be piped into a file on its own.
	termWidth, _, err := t.Size()
	if err != nil || termWidth <= 0 {
		termWidth = 80
	}
	u := ui{
		out:          os.Stdout,
		progressOut:  t.ErrOut(),
		showProgress: term.IsTerminal(os.Stderr),
		termWidth:    termWidth,
		verbose:      opts.verbose,
	}
	out := u.out

	remotes, err := listGitRemotes()
	if err != nil {
		return err
	}
	if len(remotes) == 0 {
		return fmt.Errorf("this repository has no remotes pointing at GitHub")
	}

	originalRepo, originalRemote, err := resolveDefaultRepo(remotes)
	if err != nil {
		return err
	}

	cs := newClientSet()
	originalInfo, err := fetchRepoInfo(cs, originalRepo)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Original repository: %s", originalInfo.NameWithOwner)
	if originalRemote != nil {
		fmt.Fprintf(out, " (remote %q)", originalRemote.Name)
	}
	fmt.Fprintln(out)
	if originalInfo.IsFork && originalInfo.Parent != "" {
		fmt.Fprintf(out, "Note: %s is itself a fork of %s. Tags are compared against %s.\n",
			originalInfo.NameWithOwner, originalInfo.Parent, originalInfo.NameWithOwner)
	}

	forks, skipped := selectForks(cs, remotes, originalRepo, originalInfo)
	for _, s := range skipped {
		fmt.Fprintf(out, "Skipping remote %q (%s): %s\n", s.remote.Name, s.remote, s.reason)
	}
	if len(forks) == 0 {
		fmt.Fprintln(out, "\nNo fork remotes to prune.")
		return nil
	}
	fmt.Fprintln(out)

	useLsRemote := !opts.noLsRemote

	// Tags present in the original are the keep-list for every fork.
	originalListing, err := listRepoTags(ctx, cs, u, originalRepo,
		remoteURLFor(originalRepo, originalRemote), useLsRemote)
	if err != nil {
		return err
	}
	keep := make(map[string]struct{}, len(originalListing.names))
	for _, name := range originalListing.names {
		keep[name] = struct{}{}
	}
	fmt.Fprintf(out, "%s has %d tag(s)", originalInfo.NameWithOwner, len(originalListing.names))
	if opts.verbose {
		fmt.Fprintf(out, " (via %s)", originalListing.source)
	}
	fmt.Fprint(out, ".\n\n")

	type forkPlan struct {
		fork    forkTarget
		listing tagListing
		stale   []string
		total   int
	}

	var plans []forkPlan
	staleEverywhere := map[string]struct{}{}
	for _, fork := range forks {
		listing, err := listRepoTags(ctx, cs, u, fork.repo, fork.remote.URL, useLsRemote)
		if err != nil {
			return err
		}

		var stale []string
		for _, name := range listing.names {
			if _, ok := keep[name]; !ok {
				stale = append(stale, name)
				staleEverywhere[name] = struct{}{}
			}
		}
		sort.Strings(stale)
		plans = append(plans, forkPlan{fork: fork, listing: listing, stale: stale, total: len(listing.names)})
	}

	var localStale []string
	if !opts.skipLocal {
		localTags, err := listLocalTags()
		if err != nil {
			return err
		}
		for _, t := range localTags {
			if _, ok := staleEverywhere[t]; ok {
				localStale = append(localStale, t)
			}
		}
		sort.Strings(localStale)
	}

	// Remotes that still fetch tags, and so would pull the pruned tags back in
	// on the next fetch.
	var tagoptPending []forkTarget
	if opts.disableTagFetch {
		for _, p := range plans {
			if remoteTagOpt(p.fork.remote.Name) != noTagsOpt {
				tagoptPending = append(tagoptPending, p.fork)
			}
		}
	}

	totalRemote := 0
	for _, p := range plans {
		fmt.Fprintf(out, "%s: %d of %d tag(s) not in %s",
			p.fork.info.NameWithOwner, len(p.stale), p.total, originalInfo.NameWithOwner)
		if opts.verbose {
			fmt.Fprintf(out, " (via %s)", p.listing.source)
		}
		fmt.Fprintln(out)
		printTagList(out, p.stale, opts.verbose)
		totalRemote += len(p.stale)
	}

	if !opts.skipLocal {
		fmt.Fprintf(out, "\nLocal: %d tag(s) to delete\n", len(localStale))
		printTagList(out, localStale, opts.verbose)
	}

	if opts.disableTagFetch {
		fmt.Fprintf(out, "\nFetch config: %d remote(s) to set to %s\n", len(tagoptPending), noTagsOpt)
		for _, f := range tagoptPending {
			fmt.Fprintf(out, "    remote.%s.tagopt (%s)\n", f.remote.Name, f.info.NameWithOwner)
		}
	}

	if totalRemote == 0 && len(localStale) == 0 && len(tagoptPending) == 0 {
		fmt.Fprintln(out, "\nNothing to do.")
		return nil
	}

	if !opts.confirm {
		fmt.Fprintf(out, "\nWould delete %d remote tag(s) across %d fork(s)", totalRemote, len(plans))
		if !opts.skipLocal {
			fmt.Fprintf(out, " and %d local tag(s)", len(localStale))
		}
		if opts.disableTagFetch {
			fmt.Fprintf(out, ", and set %s on %d remote(s)", noTagsOpt, len(tagoptPending))
		}
		fmt.Fprint(out, ".\nDry run: nothing was changed. Re-run with --confirm to apply.\n")
		return nil
	}

	fmt.Fprintln(out)
	failures := 0
	for _, p := range plans {
		if len(p.stale) == 0 {
			continue
		}
		if !p.fork.info.ViewerCanPush {
			fmt.Fprintf(out, "%s: skipped, you do not have write access.\n", p.fork.info.NameWithOwner)
			failures += len(p.stale)
			continue
		}
		// ls-remote gave us names only, so the IDs deleteRef needs are looked
		// up here, for the stale tags alone rather than the whole repository.
		idPB := newProgressBar(u.progressOut, u.showProgress, u.termWidth,
			fmt.Sprintf("Resolving tag IDs in %s", p.fork.info.NameWithOwner), len(p.stale))
		refs, gone, err := p.listing.resolve(cs, p.fork.repo, p.stale, opts.batchSize, idPB)
		idPB.Finish()
		if err != nil {
			return err
		}
		for _, name := range gone {
			fmt.Fprintf(out, "%s: %s is already gone.\n", p.fork.info.NameWithOwner, name)
		}
		if len(refs) == 0 {
			continue
		}

		pb := newProgressBar(u.progressOut, u.showProgress, u.termWidth,
			fmt.Sprintf("Deleting tags in %s", p.fork.info.NameWithOwner), len(refs))
		res, err := deleteTags(cs, p.fork.repo, refs, opts.batchSize, pb)
		pb.Finish()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: deleted %d tag(s).\n", p.fork.info.NameWithOwner, len(res.Deleted))
		for _, name := range sortedKeys(res.Failed) {
			fmt.Fprintf(os.Stderr, "  failed to delete %s: %s\n", name, res.Failed[name])
			failures++
		}
	}

	if !opts.skipLocal && len(localStale) > 0 {
		if err := deleteLocalTags(localStale); err != nil {
			return err
		}
		fmt.Fprintf(out, "Deleted %d local tag(s).\n", len(localStale))
	}

	for _, f := range tagoptPending {
		if err := setRemoteNoTags(f.remote.Name); err != nil {
			return err
		}
		fmt.Fprintf(out, "Set remote.%s.tagopt to %s.\n", f.remote.Name, noTagsOpt)
	}

	if failures > 0 {
		return fmt.Errorf("%d tag(s) could not be deleted", failures)
	}
	return nil
}

// forkTarget is a remote confirmed to be a fork of the original repository.
type forkTarget struct {
	remote gitRemote
	repo   repository.Repository
	info   repoInfo
}

type skippedRemote struct {
	remote gitRemote
	reason string
}

// selectForks keeps the remotes that GitHub reports as forks of the original,
// deduplicating remotes that point at the same repository.
func selectForks(cs *clientSet, remotes []gitRemote, originalRepo repository.Repository, originalInfo repoInfo) ([]forkTarget, []skippedRemote) {
	var forks []forkTarget
	var skipped []skippedRemote
	seen := map[string]bool{repoKey(originalRepo): true}

	for _, r := range remotes {
		key := repoKey(r.Repo)
		if seen[key] {
			continue
		}
		seen[key] = true

		info, err := fetchRepoInfo(cs, r.Repo)
		if err != nil {
			skipped = append(skipped, skippedRemote{r, err.Error()})
			continue
		}
		if !info.IsFork {
			skipped = append(skipped, skippedRemote{r, "not a fork"})
			continue
		}
		if !strings.EqualFold(info.Parent, originalInfo.NameWithOwner) {
			skipped = append(skipped, skippedRemote{r, fmt.Sprintf("fork of %s, not %s", info.Parent, originalInfo.NameWithOwner)})
			continue
		}
		forks = append(forks, forkTarget{remote: r, repo: r.Repo, info: info})
	}
	return forks, skipped
}

func printTagList(w io.Writer, names []string, verbose bool) {
	if len(names) == 0 {
		return
	}
	if !verbose && len(names) > maxListed {
		fmt.Fprintf(w, "    (%d tags, too many to list; use --verbose to see them)\n", len(names))
		return
	}
	for _, n := range names {
		fmt.Fprintf(w, "    %s\n", n)
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
