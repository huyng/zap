package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Checksum returns the SHA-256 a release publishes for one of its assets, and
// says where it came from so the caller can report it.
//
// GitHub digests every uploaded asset and returns the digest with the release.
// Releases cut by hand often publish a checksum file instead, either beside the
// asset ("foo.tar.gz.sha256sum") or as one file covering the whole release
// ("SHA256SUMS"). Both are consulted, in that order; neither is required.
//
// Args:
//
//	ctx: request context.
//	client: HTTP client to use.
//	asset: the asset the digest belongs to.
//	assets: every asset in the release, used to find a checksum file.
//
// Returns:
//
//	The lowercase hex digest, the name it was read from, and whether one was
//	found. An asset with no published digest returns false and no error.
func Checksum(ctx context.Context, client *http.Client, asset Asset, assets []Asset) (string, string, bool, error) {
	if digest, ok := parseDigestField(asset.Digest); ok {
		return digest, "the GitHub API", true, nil
	}

	sidecar, ok := FindChecksum(assets, asset.Name)
	if !ok {
		return "", "", false, nil
	}

	text, err := fetchText(ctx, client, sidecar.URL)
	if err != nil {
		return "", "", false, fmt.Errorf("reading %s: %w", sidecar.Name, err)
	}
	digest, err := ParseChecksum(text, asset.Name)
	if err != nil {
		return "", "", false, fmt.Errorf("%s: %w", sidecar.Name, err)
	}
	return digest, sidecar.Name, true, nil
}

// parseDigestField reads the API's "sha256:<hex>" form. A digest computed with
// another algorithm is ignored rather than trusted.
func parseDigestField(field string) (string, bool) {
	const prefix = "sha256:"
	if !strings.HasPrefix(field, prefix) {
		return "", false
	}
	hex := strings.TrimSpace(strings.TrimPrefix(field, prefix))
	if !isSHA256Hex(hex) {
		return "", false
	}
	return strings.ToLower(hex), true
}

// sidecarSuffixes are the names a per-asset checksum file usually takes, in the
// order they are preferred.
var sidecarSuffixes = []string{".sha256", ".sha256sum", ".sha256.txt", ".sums"}

// combinedNames are files that list checksums for every asset in a release.
var combinedNames = []string{"sha256sums", "sha256sums.txt", "checksums.txt", "checksums.sha256", "sha256.txt"}

// FindChecksum locates the asset that publishes a checksum for name.
//
// Args:
//
//	assets: every asset in the release.
//	name: the file name whose checksum is wanted, e.g. "tool-linux-arm64.tar.gz".
//
// Returns:
//
//	The checksum asset and true, or a zero Asset and false when the release
//	publishes none.
func FindChecksum(assets []Asset, name string) (Asset, bool) {
	byName := make(map[string]Asset, len(assets))
	for _, a := range assets {
		byName[strings.ToLower(a.Name)] = a
	}

	// A checksum file named after the asset is unambiguous, so it wins.
	for _, suffix := range sidecarSuffixes {
		if a, ok := byName[strings.ToLower(name)+suffix]; ok {
			return a, true
		}
	}
	// Otherwise a combined file, whose contents name the asset.
	for _, candidate := range combinedNames {
		if a, ok := byName[candidate]; ok {
			return a, true
		}
	}
	return Asset{}, false
}

// ParseChecksum reads the SHA-256 for name out of a checksum file.
//
// Three layouts are accepted, which between them cover what `sha256sum`,
// `shasum -a 256`, and hand-written files produce:
//
//	9f2c…                       a file holding one digest
//	9f2c…  tool-linux.tar.gz     the sha256sum layout
//	9f2c… *tool-linux.tar.gz     the sha256sum binary-mode layout
//
// Args:
//
//	text: the checksum file's contents.
//	name: the asset whose digest is wanted.
//
// Returns:
//
//	The lowercase hex digest, or an error when the file is malformed or names
//	no digest for this asset.
func ParseChecksum(text, name string) (string, error) {
	var lone string
	found := 0

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		digest := strings.ToLower(fields[0])
		if !isSHA256Hex(digest) {
			continue
		}

		if len(fields) == 1 {
			lone = digest
			found++
			continue
		}

		// The file name may be prefixed with '*' to mark binary mode.
		listed := strings.TrimPrefix(strings.Join(fields[1:], " "), "*")
		listed = strings.TrimPrefix(listed, "./")
		if listed == name {
			return digest, nil
		}
	}

	switch {
	case found == 1 && lone != "":
		return lone, nil
	case lone != "":
		return "", fmt.Errorf("holds %d checksums and none for %s", found, name)
	default:
		return "", fmt.Errorf("holds no checksum for %s", name)
	}
}

// isSHA256Hex reports whether s looks like a hex SHA-256 digest.
func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

// fetchText downloads a small file, such as a checksum listing.
func fetchText(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned %s", resp.Status)
	}

	// A checksum file is tiny; refuse to read something unbounded.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
