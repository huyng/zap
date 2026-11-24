// Package github resolves GitHub release URLs and picks the release asset that
// matches the current platform.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Release identifies a GitHub release.
type Release struct {
	Owner string
	Repo  string
	Tag   string
}

// String renders the release as owner/repo@tag.
func (r Release) String() string {
	return fmt.Sprintf("%s/%s@%s", r.Owner, r.Repo, r.Tag)
}

// URL is the page a human would open for this release, and is what a receipt
// records as its source.
func (r Release) URL() string {
	return fmt.Sprintf("https://github.com/%s/%s/releases/tag/%s", r.Owner, r.Repo, r.Tag)
}

// LatestRelease finds the newest non-draft, non-prerelease release of repo.
//
// Args:
//
//	ctx: request context.
//	client: HTTP client to use; http.DefaultClient works.
//	repo: the repository to look up.
//
// Returns:
//
//	The latest release, or an error when the repository has published none.
func LatestRelease(ctx context.Context, client *http.Client, repo Repo) (Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repo.Owner, repo.Name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return Release{}, fmt.Errorf("%s has no published release", repo)
	}
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub API returned %s for %s", resp.Status, repo)
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Release{}, err
	}
	if payload.TagName == "" {
		return Release{}, fmt.Errorf("%s has no published release", repo)
	}
	return Release{Owner: repo.Owner, Repo: repo.Name, Tag: payload.TagName}, nil
}

// Asset is a downloadable file attached to a release.
type Asset struct {
	Name string
	URL  string
	// Digest is the "sha256:<hex>" string GitHub computes for an uploaded
	// asset. It is empty for assets GitHub has not digested, in which case a
	// checksum file published alongside is the fallback (§6.1).
	Digest string
}

// ParseReleaseURL extracts the release identity from a URL like
// https://github.com/neovim/neovim/releases/tag/v0.12.2
func ParseReleaseURL(rawURL string) (Release, error) {
	const shape = "expected https://github.com/{owner}/{repo}/releases/tag/{tag}"

	s, ok := trimGitHubPrefix(rawURL)
	if !ok {
		return Release{}, fmt.Errorf("%s", shape)
	}

	parts := strings.Split(strings.TrimSuffix(s, "/"), "/")
	if len(parts) != 5 || parts[2] != "releases" || parts[3] != "tag" {
		return Release{}, fmt.Errorf("%s", shape)
	}
	for _, p := range parts {
		if p == "" {
			return Release{}, fmt.Errorf("%s", shape)
		}
	}
	return Release{Owner: parts[0], Repo: parts[1], Tag: parts[4]}, nil
}

// apiRelease is the subset of the GitHub release payload we use.
type apiRelease struct {
	Assets []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"`
	} `json:"assets"`
}

// FetchAssets lists the assets attached to rel.
//
// Args:
//
//	ctx: request context.
//	client: HTTP client to use; http.DefaultClient works.
//	rel: the release to look up.
//
// Returns:
//
//	The release assets, or an error if the release cannot be read.
func FetchAssets(ctx context.Context, client *http.Client, rel Release) ([]Asset, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", rel.Owner, rel.Repo, rel.Tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %s for %s", resp.Status, rel)
	}

	var payload apiRelease
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	assets := make([]Asset, 0, len(payload.Assets))
	for _, a := range payload.Assets {
		assets = append(assets, Asset{Name: a.Name, URL: a.URL, Digest: a.Digest})
	}
	return assets, nil
}

// extensions we can download and the installer can unpack (§6).
var archiveExts = []string{".tar.gz", ".tgz", ".zip"}

// SelectAsset picks the best asset for the given platform by scoring asset
// names against platform keywords.
//
// Args:
//
//	assets: assets from a release.
//	goos: target operating system, e.g. runtime.GOOS.
//	goarch: target architecture, e.g. runtime.GOARCH.
//
// Returns:
//
//	The best-scoring asset, or an error when nothing matches.
func SelectAsset(assets []Asset, goos, goarch string) (Asset, error) {
	osKeys := platformOSKeywords(goos)
	archKeys := platformArchKeywords(goarch)

	// Suffixes that indicate checksums, signatures or side files.
	skipSuffixes := []string{".sha256", ".sha512", ".md5", ".asc", ".sig", ".sbom", ".txt", ".json"}
	// Name fragments that indicate source archives.
	skipFragments := []string{"source", "src"}

	best := Asset{}
	bestScore := 0

	for _, a := range assets {
		lower := strings.ToLower(a.Name)
		if hasAnySuffix(lower, skipSuffixes) || containsAny(lower, skipFragments) {
			continue
		}
		if !hasAnySuffix(lower, archiveExts) {
			continue
		}

		score := 0
		for _, k := range osKeys {
			if strings.Contains(lower, k) {
				score += 10
			}
		}
		for _, k := range archKeys {
			if strings.Contains(lower, k) {
				score += 5
			}
		}
		if score == 0 || score < bestScore {
			continue
		}

		// Ties go to .tar.gz, which is the format the spec prefers.
		tieBreak := score == bestScore && strings.HasSuffix(lower, ".tar.gz") &&
			!strings.HasSuffix(strings.ToLower(best.Name), ".tar.gz")
		if score > bestScore || tieBreak {
			best = a
			bestScore = score
		}
	}

	if bestScore == 0 {
		return Asset{}, fmt.Errorf("no asset for %s/%s", goos, goarch)
	}
	return best, nil
}

func hasAnySuffix(s string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}

func containsAny(s string, fragments []string) bool {
	for _, fragment := range fragments {
		if strings.Contains(s, fragment) {
			return true
		}
	}
	return false
}

func platformOSKeywords(goos string) []string {
	switch goos {
	case "darwin":
		return []string{"macos", "darwin", "osx"}
	case "windows":
		return []string{"windows", "win"}
	default:
		return []string{goos}
	}
}

func platformArchKeywords(goarch string) []string {
	switch goarch {
	case "amd64":
		return []string{"x86_64", "amd64"}
	case "arm64":
		return []string{"arm64", "aarch64"}
	case "386":
		return []string{"i386", "386", "x86"}
	default:
		return []string{goarch}
	}
}
