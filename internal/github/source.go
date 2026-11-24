package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Repo identifies a GitHub repository.
type Repo struct {
	Owner string
	Name  string
}

// String renders the repo as owner/name.
func (r Repo) String() string {
	return r.Owner + "/" + r.Name
}

// URL is the repository's page, and is what a receipt records as its source.
func (r Repo) URL() string {
	return "https://github.com/" + r.String()
}

// Source is one commit of a repository, used when a tool is installed from its
// source tree rather than from a release asset.
type Source struct {
	Repo
	// SHA is the full commit hash the archive is cut from.
	SHA string
}

// shortSHALen is how much of the hash we show, matching git's default.
const shortSHALen = 7

// ShortSHA is the abbreviated commit hash used as the bundle version.
func (s Source) ShortSHA() string {
	if len(s.SHA) <= shortSHALen {
		return s.SHA
	}
	return s.SHA[:shortSHALen]
}

// ArchiveURL is the tarball URL for this exact commit.
func (s Source) ArchiveURL() string {
	return fmt.Sprintf("https://codeload.github.com/%s/%s/tar.gz/%s", s.Owner, s.Name, s.SHA)
}

// String renders the source as owner/name@shorthash.
func (s Source) String() string {
	return s.Repo.String() + "@" + s.ShortSHA()
}

// ParseRepoURL extracts owner and repo from a URL like
// https://github.com/huyng/bashmarks
func ParseRepoURL(rawURL string) (Repo, error) {
	const shape = "expected https://github.com/{owner}/{repo}"

	s, ok := trimGitHubPrefix(rawURL)
	if !ok {
		return Repo{}, fmt.Errorf("%s", shape)
	}

	parts := strings.Split(strings.TrimSuffix(s, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Repo{}, fmt.Errorf("%s", shape)
	}

	name := strings.TrimSuffix(parts[1], ".git")
	if name == "" || name == "." || name == ".." {
		return Repo{}, fmt.Errorf("%s", shape)
	}
	return Repo{Owner: parts[0], Name: name}, nil
}

// trimGitHubPrefix strips a github.com scheme and host from s.
func trimGitHubPrefix(s string) (string, bool) {
	for _, prefix := range []string{"https://github.com/", "http://github.com/"} {
		if strings.HasPrefix(s, prefix) {
			return strings.TrimPrefix(s, prefix), true
		}
	}
	return s, false
}

// ResolveHead finds the newest commit on the repository's default branch.
//
// Args:
//
//	ctx: request context.
//	client: HTTP client to use; http.DefaultClient works.
//	repo: the repository to look up.
//
// Returns:
//
//	The head commit as a Source, or an error if the repository is empty or
//	unreadable.
func ResolveHead(ctx context.Context, client *http.Client, repo Repo) (Source, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits?per_page=1", repo.Owner, repo.Name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Source{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return Source{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Source{}, fmt.Errorf("GitHub API returned %s for %s", resp.Status, repo)
	}

	var commits []struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&commits); err != nil {
		return Source{}, err
	}
	if len(commits) == 0 || commits[0].SHA == "" {
		return Source{}, fmt.Errorf("%s has no commits to install", repo)
	}
	return Source{Repo: repo, SHA: commits[0].SHA}, nil
}
