package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/huyng/zap/internal/bundle"
)

// Builder describes the bundle to synthesize around an archive that does not
// ship one. It is used only for sources that are not already bundles: GitHub
// release assets and repository source trees.
type Builder struct {
	ID      string
	Name    string
	Version string
	// Flatten strips a single wrapper directory from the archive root, so a
	// source tree cut by GitHub ({repo}-{sha}/...) becomes contents/ directly.
	Flatten bool
}

// Build unpacks an archive into destDir as a conforming bundle.
//
// The entrypoint is taken from an app.toml at the tree's root when it declares
// one, and otherwise left unset. It is never inferred from which files happen to
// carry an executable bit: guessing picks the wrong file as readily as the right
// one, and a bundle whose single command does nothing is worse than one that
// exposes nothing.
//
// Args:
//
//	archivePath: the downloaded archive.
//	destDir: directory that will hold app.toml and contents/.
//
// Returns:
//
//	The manifest written for the new bundle.
func (b Builder) Build(archivePath, destDir string) (*bundle.Manifest, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, err
	}

	// Unpack into a scratch directory first so a wrapper level can be dropped
	// before the tree becomes contents/.
	scratch, err := os.MkdirTemp(destDir, ".unpack-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)

	if err := extractArchive(archivePath, scratch); err != nil {
		return nil, err
	}

	tree := scratch
	if b.Flatten {
		// GitHub source archives nest everything under one {repo}-{ref}/.
		if inner, ok := singleSubdir(scratch); ok {
			tree = inner
		}
	}

	entrypoint, _, err := bundle.DeclaredEntrypoint(tree)
	if err != nil {
		return nil, fmt.Errorf("the tree's %s cannot be used: %w", bundle.ManifestFile, err)
	}

	contents := filepath.Join(destDir, bundle.ContentsDir)
	if err := os.Rename(tree, contents); err != nil {
		return nil, err
	}

	m := &bundle.Manifest{
		ID:         b.ID,
		Name:       b.Name,
		Version:    b.Version,
		Entrypoint: entrypoint,
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if err := bundle.Write(m, filepath.Join(destDir, bundle.ManifestFile)); err != nil {
		return nil, err
	}
	return m, nil
}

// singleSubdir reports whether dir holds exactly one entry and that entry is a
// directory, returning its path.
func singleSubdir(dir string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return "", false
	}
	return filepath.Join(dir, entries[0].Name()), true
}

// singleBundleDir checks that unpacked holds exactly one directory and returns
// it. §6 requires exactly one <id>/ at the archive root.
func singleBundleDir(unpacked string) (string, error) {
	entries, err := os.ReadDir(unpacked)
	if err != nil {
		return "", err
	}

	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			return "", fmt.Errorf("archive root holds %q; expected only a single <id>/ directory", e.Name())
		}
		dirs = append(dirs, e.Name())
	}
	if len(dirs) != 1 {
		return "", fmt.Errorf("archive root holds %d directories; expected exactly one <id>/", len(dirs))
	}
	return filepath.Join(unpacked, dirs[0]), nil
}
