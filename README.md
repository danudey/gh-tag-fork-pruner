# gh-tag-fork-pruner

A [GitHub CLI](https://cli.github.com) extension that deletes tags from your forks
when the original repository does not have them, and removes those tags locally too.

Forks inherit every tag the parent had at fork time. When upstream deletes or
rewrites a tag, your fork keeps the old one forever, and so does your clone.
This prunes both.

## Install

```sh
gh extension install danudey/gh-tag-fork-pruner
```

## Use

```sh
cd your-checkout
gh tag-fork-pruner            # print what would be deleted
gh tag-fork-pruner --confirm  # delete it
```

By default nothing is changed. `--confirm` is required to delete anything.

### Example

```
$ gh tag-fork-pruner
Original repository: transmission/transmission (remote "origin")

transmission/transmission has 132 tag(s).

danudey/transmission: 1 of 126 tag(s) not in transmission/transmission
    4.0.1-beta.1

Local: 1 tag(s) to delete
    4.0.1-beta.1

Would delete 1 remote tag(s) across 1 fork(s) and 1 local tag(s).
Dry run: nothing was changed. Re-run with --confirm to delete.
```

### Flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `--confirm` | off | Apply the changes. Without it, only print the summary. |
| `--skip-local` | off | Leave local tags alone. |
| `--disable-tag-fetch` | off | Set `remote.<name>.tagopt` to `--no-tags` on each fork remote. |
| `--no-ls-remote` | off | Always list tags through the GitHub API. |
| `--batch-size N` | 50 | Tags per GraphQL request. |
| `--verbose` | off | Print every tag name, and say which listing path each repo used. |

Lists of more than 10 tags are summarised as a count rather than printed, since
a partial list is no more useful than the number. `--verbose` prints them all.

### Stopping the tags coming back

A pruned tag returns the moment you `git fetch` the fork again, because git
fetches tags reachable from the fetched branches by default. `--disable-tag-fetch`
sets `remote.<name>.tagopt` to `--no-tags` on every fork remote, so git only
takes tags from the original:

```sh
gh tag-fork-pruner --disable-tag-fetch --confirm
```

Only fork remotes are touched; the remote holding the original repository keeps
fetching tags. Remotes already set to `--no-tags` are left alone, and a remote
set to something else (such as `--tags`) is overwritten. To undo it for one
remote:

```sh
git config --unset remote.origin.tagopt
```

## How repositories are chosen

**Original** is the default repository for the checkout, the one
`gh repo set-default` records in `remote.<name>.gh-resolved`.

- No default set: the extension stops and tells you to run `gh repo set-default`.
- Several remotes marked `base`: the extension stops and prints the
  `git config --unset` commands that fix it.

**Forks** are the remaining remotes that GitHub reports as forks whose parent is
the original. Any other remote is skipped with a reason, and so is any fork you
cannot push to.

## What gets deleted

1. On each fork, every tag whose name is not a tag in the original.
2. Locally, every tag from that same set. Local tags that exist in the original,
   and purely local tags that no fork has, are left alone.
3. With `--disable-tag-fetch`, `remote.<name>.tagopt` on each fork remote.

Remote deletions use batched `deleteRef` GraphQL mutations, 50 per request by
default. A failure inside a batch is reported against its own tag; the rest of
the batch still applies.

## How tags are listed

Listing uses `git ls-remote`, which costs one round trip however many tags a
repository has. The GraphQL API pages 100 tags at a time, so it gets steadily
slower as repositories grow:

| repo | tags | `ls-remote` | GraphQL paging |
| --- | --- | --- | --- |
| transmission/transmission | 132 | 0.38s | 0.95s |
| helm/helm | 265 | 0.59s | 1.32s |
| kubernetes/kubernetes | 1245 | 0.52s | 4.56s |

`ls-remote` returns no node IDs, and `deleteRef` needs one per tag. Those are
fetched in a second batched query covering only the tags being deleted, which
is normally a handful, so a dry run makes no tag API calls at all.

`ls-remote` authenticates as git, not as `gh`. If it fails for any reason, the
run prints a note and falls back to the API:

```
note: git ls-remote failed for owner/repo (...); using the API instead
```

`--no-ls-remote` skips it and uses the API from the start.

## Build from source

```sh
go build
go test ./...
gh extension install .
```
