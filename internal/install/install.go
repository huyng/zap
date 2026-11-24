// Package install installs and removes conforming app bundles in a scope, as
// described by §6 of the App Bundle Layout Specification.
package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

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
	// Log receives progress messages; nil discards them.
	Log io.Writer
}

// Result describes a finished install.
type Result struct {
	Manifest *bundle.Manifest
	Root     string
	Exposed  string
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
	staging, err := i.stagingDir()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)

	unpacked := filepath.Join(staging, "unpacked")
	if err := os.MkdirAll(unpacked, 0o700); err != nil {
		return nil, err
	}

	i.logf("unpacking %s", filepath.Base(archivePath))
	if err := extractArchive(archivePath, unpacked); err != nil {
		return nil, err
	}

	prepared, err := singleBundleDir(unpacked)
	if err != nil {
		return nil, err
	}

	m, err := bundle.VerifyDir(prepared)
	if err != nil {
		return nil, err
	}
	if name := filepath.Base(prepared); name != m.ID {
		return nil, fmt.Errorf("bundle directory %q does not match manifest id %q", name, m.ID)
	}
	return i.promote(prepared, m)
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
	staging, err := i.stagingDir()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)

	i.logf("fetching %s", rel)
	assets, err := github.FetchAssets(ctx, client, rel)
	if err != nil {
		return nil, err
	}

	asset, err := github.SelectAsset(assets, goos, goarch)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	i.logf("downloading %s", asset.Name)

	download := filepath.Join(staging, asset.Name)
	if err := downloadTo(ctx, client, asset.URL, download); err != nil {
		return nil, err
	}

	prepared := filepath.Join(staging, "prepared")
	if _, err := b.Build(download, prepared); err != nil {
		return nil, err
	}

	m, err := bundle.VerifyDir(prepared)
	if err != nil {
		return nil, err
	}
	if m.Runnable() {
		i.logf("bundled %s %s; entrypoint %s comes from the tree's %s", m.ID, m.Version, m.Entrypoint, bundle.ManifestFile)
	} else {
		i.logf("bundled %s %s with no entrypoint", m.ID, m.Version)
	}
	return i.promote(prepared, m)
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
	staging, err := i.stagingDir()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)

	i.logf("downloading %s", src)
	download := filepath.Join(staging, src.Name+"-"+src.ShortSHA()+".tar.gz")
	if err := downloadTo(ctx, client, src.ArchiveURL(), download); err != nil {
		return nil, err
	}

	b.Version = src.ShortSHA()
	b.Flatten = true

	prepared := filepath.Join(staging, "prepared")
	if _, err := b.Build(download, prepared); err != nil {
		return nil, fmt.Errorf("%s: %w", src, err)
	}

	m, err := bundle.VerifyDir(prepared)
	if err != nil {
		return nil, err
	}
	if m.Runnable() {
		i.logf("bundled %s %s from %s; entrypoint %s comes from the tree's %s", m.ID, m.Version, src, m.Entrypoint, bundle.ManifestFile)
	} else {
		i.logf("bundled %s %s from %s with no entrypoint", m.ID, m.Version, src)
	}
	return i.promote(prepared, m)
}

// promote moves a verified bundle into place and exposes its entrypoint.
func (i *Installer) promote(prepared string, m *bundle.Manifest) (*Result, error) {
	if err := os.MkdirAll(i.Scope.RootDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", i.Scope.RootDir, err)
	}

	target := i.Scope.Root(m.ID)
	installed, err := isAppRoot(target)
	if err != nil {
		return nil, err
	}

	switch {
	case installed && !i.Force:
		return nil, fmt.Errorf("%s is already installed at %s; pass --force to replace it, which also removes its data/", m.ID, target)
	case installed && i.Force:
		i.logf("replacing %s", target)
		if err := os.RemoveAll(target); err != nil {
			return nil, err
		}
	}

	if err := i.place(prepared, target); err != nil {
		return nil, err
	}
	if err := os.Chmod(target, i.Scope.RootMode()); err != nil {
		return nil, err
	}

	exposed, err := i.expose(m, target)
	if err != nil {
		return nil, err
	}
	return &Result{Manifest: m, Root: target, Exposed: exposed}, nil
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

// place moves the prepared tree to target. A target left over from a removal
// holds data/ and nothing else, and that data is carried across so a reinstall
// finds it again.
func (i *Installer) place(prepared, target string) error {
	if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
		// Same filesystem as the root, so the rename is atomic (§6).
		return os.Rename(prepared, target)
	}

	// Move the leftover aside first, so the new tree still arrives by rename.
	aside, err := os.MkdirTemp(i.Scope.RootDir, ".staging-old-")
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

	dataDir := filepath.Join(old, "data")
	if _, err := os.Stat(dataDir); err == nil {
		i.logf("carrying over %s/data from the previous install", target)
		if err := os.Rename(dataDir, filepath.Join(target, "data")); err != nil {
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

// expose symlinks or wraps the entrypoint into Scope.BinDir (§6.1). A bundle
// with no entrypoint exposes nothing and returns an empty path.
func (i *Installer) expose(m *bundle.Manifest, root string) (string, error) {
	if !m.Runnable() {
		return "", nil
	}
	if err := os.MkdirAll(i.Scope.BinDir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", i.Scope.BinDir, err)
	}
	exposed := i.Scope.Link(m.EntryName())

	if _, err := os.Lstat(exposed); err == nil {
		if !i.Force {
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
	if err := os.MkdirAll(i.Scope.RootDir, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", i.Scope.RootDir, err)
	}
	return os.MkdirTemp(i.Scope.RootDir, ".staging-")
}

func (i *Installer) logf(format string, args ...any) {
	if i.Log == nil {
		return
	}
	fmt.Fprintf(i.Log, format+"\n", args...)
}

// downloadTo saves url to path.
func downloadTo(ctx context.Context, client *http.Client, url, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %s", resp.Status)
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
