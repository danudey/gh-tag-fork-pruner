package main

import (
	"context"
	"fmt"
	"os"

	"github.com/cli/go-gh/v2/pkg/repository"
)

// tagListing is every tag name in a repository, plus the node IDs when the
// path that produced it happened to supply them. The ls-remote path does not,
// so ids is nil there and deletions resolve what they need on demand.
type tagListing struct {
	names  []string
	ids    map[string]string
	source string
}

// listRepoTags lists a repository's tags, preferring a single git ls-remote
// round trip over paging the GraphQL API 100 tags at a time. Any ls-remote
// failure (missing credentials, an unreachable host, a timeout) falls back to
// the API rather than stopping the run.
func listRepoTags(ctx context.Context, cs *clientSet, u ui, repo repository.Repository, remoteURL string, useLsRemote bool) (tagListing, error) {
	label := fmt.Sprintf("Listing tags in %s/%s", repo.Owner, repo.Name)

	if useLsRemote && remoteURL != "" {
		sp := newSpinner(u.progressOut, u.showProgress, label+" (ls-remote)")
		names, err := lsRemoteTags(ctx, remoteURL)
		sp.Finish()
		if err == nil {
			return tagListing{names: names, source: "ls-remote"}, nil
		}
		fmt.Fprintf(os.Stderr, "note: git ls-remote failed for %s/%s (%s); using the API instead\n",
			repo.Owner, repo.Name, err)
	}

	pb := newProgressBar(u.progressOut, u.showProgress, u.termWidth, label, 0)
	refs, err := listTags(cs, repo, pb)
	pb.Finish()
	if err != nil {
		return tagListing{}, err
	}

	listing := tagListing{
		names:  make([]string, 0, len(refs)),
		ids:    make(map[string]string, len(refs)),
		source: "api",
	}
	for _, r := range refs {
		listing.names = append(listing.names, r.Name)
		listing.ids[r.Name] = r.ID
	}
	return listing, nil
}

// resolve turns tag names into the refs deleteRef needs, reusing IDs the
// listing already carries and querying for them otherwise.
func (l tagListing) resolve(cs *clientSet, repo repository.Repository, names []string, batchSize int, pb *progressBar) (found []tagRef, missing []string, err error) {
	if l.ids != nil {
		for _, n := range names {
			if id, ok := l.ids[n]; ok {
				found = append(found, tagRef{Name: n, ID: id})
			} else {
				missing = append(missing, n)
			}
		}
		if pb != nil {
			pb.Add(len(names))
		}
		return found, missing, nil
	}
	return fetchRefIDs(cs, repo, names, batchSize, pb)
}

// remoteURLFor picks the URL to hand ls-remote. A repository reachable through
// a remote uses that remote's URL and so its credentials; otherwise we build an
// HTTPS URL, which may not authenticate and will then fall back to the API.
func remoteURLFor(repo repository.Repository, remote *gitRemote) string {
	if remote != nil {
		return remote.URL
	}
	return fmt.Sprintf("https://%s/%s/%s.git", repo.Host, repo.Owner, repo.Name)
}
