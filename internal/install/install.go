// Package install installs and removes conforming app bundles in a scope, as
// described by §6 of the App Bundle Layout Specification.
package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/huyng/zap/internal/bundle"
	"github.com/huyng/zap/internal/github"
	"github.com/huyng/zap/internal/scope"
)

// Installer installs bundles. All of its inputs are fields; it never reads the
// environment.
type Installer struct {
	Scope scope.Scope
	// Force replaces an existing app root, including its data/ directory.
	Force bool
	// Tool names the installer in the receipt it writes (§6.1). An empty Name
	// leaves the [installer] table out, so a receipt then records only where
	// the bundle came from.
	Tool Tool
	// Log receives progress messages; nil discards them.
	Log io.Writer
}

// Tool identifies the program writing a receipt.
type Tool struct {
	Name    string
	Version string
}

// Result describes a finished install.
type Result struct {
	Manifest *bundle.Manifest
	Root     string
	Exposed  string
	// Receipt is the install record written to the root (§6.1).
	Receipt *bundle.Receipt
}

// fetched is a bundle that has been downloaded and wrapped but not installed.
// Its staging directory is removed by Close, so a caller can fetch, decide
// whether to install, and clean up either way.
type fetched struct {
	staging  string
	prepared string
	manifest *bundle.Manifest
	receipt  *bundle.Receipt
}

// Close discards the staging directory the bundle was assembled in.
func (f *fetched) Close() error {
	return os.RemoveAll(f.staging)
}

// promoteMode says what promoting should do when an app root is already there.
type promoteMode int

const (
	// promoteFresh fails when the id is already installed.
	promoteFresh promoteMode = iota
	// promoteForce removes the existing root, data/ included, and installs.
	promoteForce
	// promoteUpgrade swaps the tree and carries the mutable directories over.
	promoteUpgrade
)

// mode is what a plain install should do, given the Force flag.
func (i *Installer) mode() promoteMode {
	if i.Force {
		return promoteForce
	}
	return promoteFresh
}

// InstallArchive installs a bundle archive that already conforms to the
// specification.
//
// Args:
//
//	archivePath: a .tar.gz, .tgz or .zip file whose root holds one <id>/.
//
// Returns:
//
//	The installed app, or an error if the archive does not conform or the
//	target already exists and Force is false.
func (i *Installer) InstallArchive(archivePath string) (*Result, error) {
	f, err := i.fetchArchive(archivePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return i.promote(f, i.mode())
}

// fetchArchive unpacks a conforming bundle archive into a staging directory.
func (i *Installer) fetchArchive(archivePath string) (*fetched, error) {
	staging, err := i.stagingDir()
	if err != nil {
		return nil, err
	}

	unpacked := filepath.Join(staging, "unpacked")
	if err := os.MkdirAll(unpacked, 0o700); err != nil {
		os.RemoveAll(staging)
		return nil, err
	}

	i.logf("unpacking %s", filepath.Base(archivePath))
	if err := extractArchive(archivePath, unpacked); err != nil {
		os.RemoveAll(staging)
		return nil, err
	}

	prepared, err := singleBundleDir(unpacked)
	if err != nil {
		os.RemoveAll(staging)
		return nil, err
	}

	m, err := bundle.VerifyDir(prepared)
	if err != nil {
		os.RemoveAll(staging)
		return nil, err
	}
	if name := filepath.Base(prepared); name != m.ID {
		os.RemoveAll(staging)
		return nil, fmt.Errorf("bundle directory %q does not match manifest id %q", name, m.ID)
	}

	sha, err := fileSHA256(archivePath)
	if err != nil {
		os.RemoveAll(staging)
		return nil, err
	}
	return &fetched{
		staging:  staging,
		prepared: prepared,
		manifest: m,
		receipt: &bundle.Receipt{
			Source: bundle.ReceiptSource{
				Kind: "archive",
				URL:  absolutePath(archivePath),
			},
			Artifact: bundle.ReceiptArtifact{
				Name:      filepath.Base(archivePath),
				SHA256:    sha,
				FetchedAt: now(),
			},
		},
	}, nil
}

// InstallRelease builds a bundle from a GitHub release asset and installs it.
// It is the shortcut behind `zap install <github-url>`.
//
// Args:
//
//	ctx: request context for the API and download.
//	client: HTTP client to use.
//	rel: the release to install.
//	b: bundle identity to synthesize (id, name, version).
//	goos, goarch: platform used to choose the release asset.
//
// Returns:
//
//	The installed app, or an error if the release cannot be resolved,
//	downloaded, bundled or installed.
func (i *Installer) InstallRelease(ctx context.Context, client *http.Client, rel github.Release, b Builder, goos, goarch string) (*Result, error) {
	f, err := i.fetchRelease(ctx, client, rel, b, goos, goarch)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return i.promote(f, i.mode())
}

// fetchRelease downloads the platform asset of a release and wraps it.
func (i *Installer) fetchRelease(ctx context.Context, client *http.Client, rel github.Release, b Builder, goos, goarch string) (*fetched, error) {
	staging, err := i.stagingDir()
	if err != nil {
		return nil, err
	}

	i.logf("fetching %s", rel)
	assets, err := github.FetchAssets(ctx, client, rel)
	if err != nil {
		os.RemoveAll(staging)
		return nil, err
	}

	asset, err := github.SelectAsset(assets, goos, goarch)
	if err != nil {
		os.RemoveAll(staging)
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	i.logf("downloading %s", asset.Name)

	download := filepath.Join(staging, asset.Name)
	sha, err := downloadTo(ctx, client, asset.URL, download)
	if err != nil {
		os.RemoveAll(staging)
		return nil, err
	}
	if err := i.verify(ctx, client, asset, assets, sha); err != nil {
		os.RemoveAll(staging)
		return nil, err
	}

	prepared := filepath.Join(staging, "prepared")
	m, fromTree, err := b.build(download, prepared)
	if err != nil {
		os.RemoveAll(staging)
		return nil, err
	}
	if fromTree {
		i.logf("using the id %q declared by the tree's %s", m.ID, bundle.ManifestFile)
	}

	m, err = bundle.VerifyDir(prepared)
	if err != nil {
		os.RemoveAll(staging)
		return nil, err
	}
	if m.Runnable() {
		i.logf("bundled %s %s; entrypoint %s comes from the tree's %s", m.ID, m.Version, m.Entrypoint, bundle.ManifestFile)
	} else {
		i.logf("bundled %s %s with no entrypoint", m.ID, m.Version)
	}

	return &fetched{
		staging:  staging,
		prepared: prepared,
		manifest: m,
		receipt: &bundle.Receipt{
			Source: bundle.ReceiptSource{
				Kind: "release",
				URL:  rel.URL(),
				Ref:  rel.Tag,
			},
			Artifact: bundle.ReceiptArtifact{
				Name:      asset.Name,
				SHA256:    sha,
				FetchedAt: now(),
			},
		},
	}, nil
}

// InstallSource builds a bundle from a repository source tree and installs it.
// It is the shortcut behind `zap install <repo-url>` for repos that cut no
// releases. The tree's own app.toml supplies the entrypoint if it declares one;
// nothing is guessed from the files in the tree.
//
// Args:
//
//	ctx: request context for the download.
//	client: HTTP client to use.
//	src: the commit to install; its short hash becomes the bundle version.
//	b: bundle identity to synthesize (id, name); Version is filled in here.
//
// Returns:
//
//	The installed app, or an error if the tree cannot be downloaded or wrapped.
func (i *Installer) InstallSource(ctx context.Context, client *http.Client, src github.Source, b Builder) (*Result, error) {
	f, err := i.fetchSource(ctx, client, src, b)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return i.promote(f, i.mode())
}

// verify compares a downloaded asset against the digest its release publishes.
// A mismatch is fatal: the bytes on disk are not what the release says they
// are, and nothing has been installed at that point. A release that publishes
// no digest at all is installed unverified, with a line saying so (§6.1).
//
// Args:
//
//	ctx: request context.
//	client: HTTP client to use.
//	asset: the asset that was downloaded.
//	assets: every asset in the release, searched for a checksum file.
//	got: the SHA-256 of the downloaded file.
//
// Returns:
//
//	An error when the release publishes a digest and it does not match.
func (i *Installer) verify(ctx context.Context, client *http.Client, asset github.Asset, assets []github.Asset, got string) error {
	want, from, ok, err := github.Checksum(ctx, client, asset, assets)
	if err != nil {
		return err
	}
	if !ok {
		i.logf("no checksum published for %s; installing unverified", asset.Name)
		return nil
	}
	if !strings.EqualFold(want, got) {
		return fmt.Errorf("%s does not match the checksum published by %s: expected %s, downloaded %s",
			asset.Name, from, want, got)
	}
	i.logf("verified %s against %s", asset.Name, from)
	return nil
}

// fetchSource downloads one commit's tree and wraps it as a bundle.
func (i *Installer) fetchSource(ctx context.Context, client *http.Client, src github.Source, b Builder) (*fetched, error) {
	staging, err := i.stagingDir()
	if err != nil {
		return nil, err
	}

	i.logf("downloading %s", src)
	download := filepath.Join(staging, src.Name+"-"+src.ShortSHA()+".tar.gz")
	sha, err := downloadTo(ctx, client, src.ArchiveURL(), download)
	if err != nil {
		os.RemoveAll(staging)
		return nil, err
	}

	b.Version = src.ShortSHA()
	b.Flatten = true

	prepared := filepath.Join(staging, "prepared")
	m, fromTree, err := b.build(download, prepared)
	if err != nil {
		os.RemoveAll(staging)
		return nil, fmt.Errorf("%s: %w", src, err)
	}
	if fromTree {
		i.logf("using the id %q declared by the tree's %s", m.ID, bundle.ManifestFile)
	}

	m, err = bundle.VerifyDir(prepared)
	if err != nil {
		os.RemoveAll(staging)
		return nil, err
	}
	if m.Runnable() {
		i.logf("bundled %s %s from %s; entrypoint %s comes from the tree's %s", m.ID, m.Version, src, m.Entrypoint, bundle.ManifestFile)
	} else {
		i.logf("bundled %s %s from %s with no entrypoint", m.ID, m.Version, src)
	}

	return &fetched{
		staging:  staging,
		prepared: prepared,
		manifest: m,
		receipt: &bundle.Receipt{
			Source: bundle.ReceiptSource{
				Kind: "repo",
				URL:  src.Repo.URL(),
				Ref:  src.SHA,
			},
			Artifact: bundle.ReceiptArtifact{
				Name:      filepath.Base(download),
				SHA256:    sha,
				FetchedAt: now(),
			},
		},
	}, nil
}

// promote moves a verified bundle into place, records the installation and
// exposes its entrypoint.
func (i *Installer) promote(f *fetched, mode promoteMode) (*Result, error) {
	m := f.manifest

	if err := os.MkdirAll(i.Scope.AppsDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", i.Scope.AppsDir, err)
	}

	target := i.Scope.Root(m.ID)
	installed, err := isAppRoot(target)
	if err != nil {
		return nil, err
	}

	switch mode {
	case promoteUpgrade:
		if !installed {
			return nil, fmt.Errorf("%s is not installed at %s", m.ID, target)
		}
	case promoteFresh:
		if installed {
			return nil, fmt.Errorf("%s is already installed at %s; pass --force to replace it, which also removes its data/", m.ID, target)
		}
	case promoteForce:
		if installed {
			i.logf("replacing %s", target)
			if err := os.RemoveAll(target); err != nil {
				return nil, err
			}
		}
	}

	if err := i.place(f.prepared, target); err != nil {
		return nil, err
	}
	if err := os.Chmod(target, i.Scope.RootMode()); err != nil {
		return nil, err
	}

	// recordReceipt runs after the tree is in place, so a failed receipt write
	// never leaves a root that claims to be installed but has no contents/.
	if err := i.recordReceipt(target, f.receipt); err != nil {
		return nil, err
	}

	exposed, err := i.expose(m, target, mode != promoteFresh)
	if err != nil {
		return nil, err
	}
	return &Result{Manifest: m, Root: target, Exposed: exposed, Receipt: f.receipt}, nil
}

// recordReceipt writes receipt.toml for a finished install (§6.1). A receipt
// with nothing to say is not written at all, which keeps an app root down to
// app.toml and contents/ when there is nothing to record.
func (i *Installer) recordReceipt(root string, r *bundle.Receipt) error {
	if r == nil {
		return nil
	}
	if i.Tool.Name != "" {
		r.Installer = bundle.ReceiptInstaller{
			Name:    i.Tool.Name,
			Version: i.Tool.Version,
			At:      now(),
		}
	}
	if *r == (bundle.Receipt{}) {
		return nil
	}
	return r.Write(filepath.Join(root, bundle.ReceiptFile))
}

// isAppRoot reports whether dir holds an app.toml, and so is an installed app.
// A directory without one is not: a non-purge removal leaves data/ alone, and
// install treats that leftover as absent (§6).
func isAppRoot(dir string) (bool, error) {
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	case !info.IsDir():
		return false, fmt.Errorf("%s is not a directory", dir)
	}

	if _, err := os.Stat(filepath.Join(dir, bundle.ManifestFile)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// place moves the prepared tree to target. A target already there is either a
// leftover from a removal, holding data/ and nothing else, or an installation
// being upgraded; either way its mutable directories are carried across so the
// app finds its data again (§7.1).
func (i *Installer) place(prepared, target string) error {
	if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
		// Same filesystem as the root, so the rename is atomic (§6).
		return os.Rename(prepared, target)
	}

	// Move the leftover aside first, so the new tree still arrives by rename.
	aside, err := os.MkdirTemp(i.Scope.AppsDir, ".staging-old-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(aside)

	old := filepath.Join(aside, "old")
	if err := os.Rename(target, old); err != nil {
		return err
	}
	if err := os.Rename(prepared, target); err != nil {
		// Put the leftover back; the install failed before anything was added.
		if restoreErr := os.Rename(old, target); restoreErr != nil {
			return fmt.Errorf("%w (restoring %s failed: %v)", err, target, restoreErr)
		}
		return err
	}

	// The mutable directories belong to the app, not to the tree, so they
	// survive the swap (§7.2).
	for _, name := range mutableDirs {
		dir := filepath.Join(old, name)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		i.logf("carrying over %s/%s from the previous install", target, name)
		if err := os.Rename(dir, filepath.Join(target, name)); err != nil {
			return err
		}
	}

	// Anything else in there belongs to no install, so say so rather than
	// resurrecting it.
	if remaining, err := os.ReadDir(old); err == nil && len(remaining) > 0 {
		names := make([]string, 0, len(remaining))
		for _, e := range remaining {
			names = append(names, e.Name())
		}
		i.logf("discarding %s: %v", aside, names)
	}
	return nil
}

// mutableDirs are the app's own directories, which an install never replaces
// (§4).
var mutableDirs = []string{"data", "cache", "run"}

// expose symlinks or wraps the entrypoint into Scope.BinDir (§6.1). A bundle
// with no entrypoint exposes nothing and returns an empty path. An exposure
// that already points at this root is left alone, which is what an upgrade
// finds on every run.
func (i *Installer) expose(m *bundle.Manifest, root string, replace bool) (string, error) {
	if !m.Runnable() {
		return "", nil
	}
	if err := os.MkdirAll(i.Scope.BinDir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", i.Scope.BinDir, err)
	}
	exposed := i.Scope.Link(m.EntryName())

	if _, err := os.Lstat(exposed); err == nil {
		if exposes(exposed, root, m) {
			return exposed, nil
		}
		if !replace {
			return "", fmt.Errorf("%s already exists; pass --force to replace it", exposed)
		}
		if err := os.Remove(exposed); err != nil {
			return "", err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	if i.Scope.System {
		if err := writeWrapper(exposed, root, m); err != nil {
			return "", err
		}
	} else if err := os.Symlink(m.EntryPath(root), exposed); err != nil {
		return "", err
	}
	return exposed, nil
}

// exposes reports whether path already exposes m from root: a symlink into the
// root, or a wrapper naming it.
func exposes(path, root string, m *bundle.Manifest) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return isManagedWrapper(path, root)
	}
	target, err := os.Readlink(path)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return filepath.Clean(target) == filepath.Clean(m.EntryPath(root))
}

// writeWrapper writes a launcher that pins APP_ROOT to the installed root.
func writeWrapper(exposed, root string, m *bundle.Manifest) error {
	script := "#!/bin/sh\nset -eu\n" +
		"APP_ROOT=" + shellQuote(root) + "\nexport APP_ROOT\n" +
		"exec \"$APP_ROOT/" + bundle.ContentsDir + "/" + path.Clean(m.Entrypoint) + "\" \"$@\"\n"

	tmp := exposed + ".tmp"
	if err := os.WriteFile(tmp, []byte(script), 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, exposed); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// stagingDir creates a scratch directory inside the scope's root so that the
// final rename stays on one filesystem.
func (i *Installer) stagingDir() (string, error) {
	if err := os.MkdirAll(i.Scope.AppsDir, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", i.Scope.AppsDir, err)
	}
	return os.MkdirTemp(i.Scope.AppsDir, ".staging-")
}

func (i *Installer) logf(format string, args ...any) {
	if i.Log == nil {
		return
	}
	fmt.Fprintf(i.Log, format+"\n", args...)
}

// downloadTo saves url to path and returns the SHA-256 of what was written,
// which the receipt records so a later fetch can be compared against it.
func downloadTo(ctx context.Context, client *http.Client, url, path string) (string, error) {
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

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(f, digest), resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// fileSHA256 returns the digest of a file on disk.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	digest := sha256.New()
	if _, err := io.Copy(digest, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// absolutePath resolves path against the working directory, so a receipt
// records where an archive was read from rather than how it was spelled.
func absolutePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

// now is the timestamp format receipts use (RFC 3339, UTC).
func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}
