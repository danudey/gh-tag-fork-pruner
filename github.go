package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"
)

// clientSet hands out one GraphQL client per host, so a checkout with remotes
// on both github.com and a GHES instance still works.
type clientSet struct {
	clients map[string]*api.GraphQLClient
}

func newClientSet() *clientSet {
	return &clientSet{clients: map[string]*api.GraphQLClient{}}
}

func (cs *clientSet) forHost(host string) (*api.GraphQLClient, error) {
	if c, ok := cs.clients[host]; ok {
		return c, nil
	}
	c, err := api.NewGraphQLClient(api.ClientOptions{Host: host})
	if err != nil {
		return nil, fmt.Errorf("could not create a GraphQL client for %s: %w", host, err)
	}
	cs.clients[host] = c
	return c, nil
}

// repoVars is the owner/name variable pair every query in this file declares.
func repoVars(repo repository.Repository) map[string]interface{} {
	return map[string]interface{}{"owner": repo.Owner, "name": repo.Name}
}

// repoInfo is the subset of repository metadata needed to tell an original
// repository apart from its forks.
type repoInfo struct {
	ID            string
	NameWithOwner string
	IsFork        bool
	Parent        string
	ViewerCanPush bool
}

func fetchRepoInfo(cs *clientSet, repo repository.Repository) (repoInfo, error) {
	client, err := cs.forHost(repo.Host)
	if err != nil {
		return repoInfo{}, err
	}

	const query = `
query RepoInfo($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    id
    nameWithOwner
    isFork
    viewerPermission
    parent { nameWithOwner }
  }
}`

	var resp struct {
		Repository struct {
			ID               string
			NameWithOwner    string
			IsFork           bool
			ViewerPermission string
			Parent           *struct{ NameWithOwner string }
		}
	}
	vars := repoVars(repo)
	if err := client.Do(query, vars, &resp); err != nil {
		return repoInfo{}, fmt.Errorf("could not look up %s/%s: %w", repo.Owner, repo.Name, err)
	}

	info := repoInfo{
		ID:            resp.Repository.ID,
		NameWithOwner: resp.Repository.NameWithOwner,
		IsFork:        resp.Repository.IsFork,
		ViewerCanPush: resp.Repository.ViewerPermission == "WRITE" ||
			resp.Repository.ViewerPermission == "MAINTAIN" ||
			resp.Repository.ViewerPermission == "ADMIN",
	}
	if resp.Repository.Parent != nil {
		info.Parent = resp.Repository.Parent.NameWithOwner
	}
	return info, nil
}

// tagRef is a tag name paired with the node ID needed to delete it.
type tagRef struct {
	Name string
	ID   string
}

const tagPageSize = 100

// listTags pages through every tag in a repository, advancing pb as it goes.
func listTags(cs *clientSet, repo repository.Repository, pb *progressBar) ([]tagRef, error) {
	client, err := cs.forHost(repo.Host)
	if err != nil {
		return nil, err
	}

	const query = `
query RepoTags($owner: String!, $name: String!, $pageSize: Int!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    refs(refPrefix: "refs/tags/", first: $pageSize, after: $cursor) {
      totalCount
      pageInfo { hasNextPage endCursor }
      nodes { name id }
    }
  }
}`

	var tags []tagRef
	var cursor *string
	knownTotal := false

	for {
		var resp struct {
			Repository struct {
				Refs struct {
					TotalCount int
					PageInfo   struct {
						HasNextPage bool
						EndCursor   string
					}
					Nodes []tagRef
				}
			}
		}
		vars := repoVars(repo)
		vars["pageSize"] = tagPageSize
		vars["cursor"] = cursor
		if err := client.Do(query, vars, &resp); err != nil {
			return nil, fmt.Errorf("could not list tags for %s/%s: %w", repo.Owner, repo.Name, err)
		}

		refs := resp.Repository.Refs
		if pb != nil && !knownTotal {
			pb.SetTotal(refs.TotalCount)
			knownTotal = true
		}
		tags = append(tags, refs.Nodes...)
		if pb != nil {
			pb.Add(len(refs.Nodes))
		}

		if !refs.PageInfo.HasNextPage {
			break
		}
		end := refs.PageInfo.EndCursor
		cursor = &end
	}
	return tags, nil
}

// fetchRefIDs resolves tag names to the node IDs that deleteRef needs, using
// aliased ref lookups so a batch costs one request.
//
// This pairs with the ls-remote listing path, which yields names only. Names
// that no longer resolve come back in missing rather than as an error: the tag
// was deleted between listing and now, which is the outcome we wanted anyway.
func fetchRefIDs(cs *clientSet, repo repository.Repository, names []string, batchSize int, pb *progressBar) (found []tagRef, missing []string, err error) {
	if len(names) == 0 {
		return nil, nil, nil
	}

	client, err := cs.forHost(repo.Host)
	if err != nil {
		return nil, nil, err
	}

	for start := 0; start < len(names); start += batchSize {
		end := min(start+batchSize, len(names))
		batch := names[start:end]

		query, vars := buildRefIDQuery(repo, batch)
		var resp struct {
			Repository map[string]*struct {
				ID   string
				Name string
			}
		}
		if err := client.Do(query, vars, &resp); err != nil {
			return nil, nil, fmt.Errorf("could not resolve tag IDs in %s/%s: %w", repo.Owner, repo.Name, err)
		}

		for i, name := range batch {
			node := resp.Repository[fmt.Sprintf("t%d", i)]
			if node == nil || node.ID == "" {
				missing = append(missing, name)
				continue
			}
			found = append(found, tagRef{Name: name, ID: node.ID})
		}
		if pb != nil {
			pb.Add(len(batch))
		}
	}
	return found, missing, nil
}

// buildRefIDQuery writes one aliased ref lookup per tag name. Names travel as
// variables, never as query text.
func buildRefIDQuery(repo repository.Repository, batch []string) (string, map[string]interface{}) {
	var decls, fields strings.Builder
	decls.WriteString("$owner: String!, $name: String!")
	vars := repoVars(repo)
	for i, tag := range batch {
		fmt.Fprintf(&decls, ", $q%d: String!", i)
		fmt.Fprintf(&fields, "    t%d: ref(qualifiedName: $q%d) { id name }\n", i, i)
		vars[fmt.Sprintf("q%d", i)] = "refs/tags/" + tag
	}
	query := fmt.Sprintf("query RefIDs(%s) {\n  repository(owner: $owner, name: $name) {\n%s  }\n}",
		decls.String(), fields.String())
	return query, vars
}

// deleteResult records the outcome of a batched delete.
type deleteResult struct {
	Deleted []string
	Failed  map[string]string // tag name -> reason
}

// deleteTags removes refs with batched, aliased deleteRef mutations. Each batch
// is one HTTP request; a failure inside a batch is attributed to the individual
// tag by its alias, so the rest of the batch still counts as deleted.
func deleteTags(cs *clientSet, repo repository.Repository, refs []tagRef, batchSize int, pb *progressBar) (deleteResult, error) {
	result := deleteResult{Failed: map[string]string{}}
	if len(refs) == 0 {
		return result, nil
	}

	client, err := cs.forHost(repo.Host)
	if err != nil {
		return result, err
	}

	for start := 0; start < len(refs); start += batchSize {
		end := min(start+batchSize, len(refs))
		batch := refs[start:end]

		query, vars := buildDeleteMutation(batch)
		var resp map[string]interface{}
		err := client.Do(query, vars, &resp)

		failedAliases := map[int]string{}
		if err != nil {
			var gqlErr *api.GraphQLError
			if errors.As(err, &gqlErr) {
				for _, item := range gqlErr.Errors {
					if idx, ok := aliasIndex(item.Path); ok {
						failedAliases[idx] = item.Message
						continue
					}
					// An error with no usable path applies to the whole batch.
					for i := range batch {
						failedAliases[i] = item.Message
					}
				}
			} else {
				// Transport-level failure: nothing in this batch is confirmed.
				for i := range batch {
					failedAliases[i] = err.Error()
				}
			}
		}

		for i, ref := range batch {
			if reason, bad := failedAliases[i]; bad {
				result.Failed[ref.Name] = reason
			} else {
				result.Deleted = append(result.Deleted, ref.Name)
			}
		}
		if pb != nil {
			pb.Add(len(batch))
		}
	}
	return result, nil
}

// buildDeleteMutation writes one aliased deleteRef field per tag. Ref IDs are
// passed as variables rather than interpolated into the query text.
func buildDeleteMutation(batch []tagRef) (string, map[string]interface{}) {
	var decls, fields strings.Builder
	vars := make(map[string]interface{}, len(batch))
	for i, ref := range batch {
		if i > 0 {
			decls.WriteString(", ")
		}
		fmt.Fprintf(&decls, "$ref%d: ID!", i)
		fmt.Fprintf(&fields, "  d%d: deleteRef(input: {refId: $ref%d}) { clientMutationId }\n", i, i)
		vars[fmt.Sprintf("ref%d", i)] = ref.ID
	}
	query := fmt.Sprintf("mutation DeleteTags(%s) {\n%s}", decls.String(), fields.String())
	return query, vars
}

// aliasIndex recovers the batch position from a GraphQL error path like ["d7"].
func aliasIndex(path []interface{}) (int, bool) {
	if len(path) == 0 {
		return 0, false
	}
	s, ok := path[0].(string)
	if !ok || !strings.HasPrefix(s, "d") {
		return 0, false
	}
	var idx int
	if _, err := fmt.Sscanf(s, "d%d", &idx); err != nil {
		return 0, false
	}
	return idx, true
}
