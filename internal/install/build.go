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
	// ID and Name are the identity to use when the tree declares none. A tree
	// that ships its own app.toml overrides them, because the author knows the
	// namespace the app is installed under (§2.3) and a rename of the source
	// must not move an existing app root.
	ID      string
	Name    string
	Version string
	// OverrideID and OverrideName are chosen by the person running the install,
	// so they beat the tree's own manifest. They apply only to a GitHub install,
	// where the identity would otherwise be derived.
	OverrideID   string
	OverrideName string
	// Flatten strips a single wrapper directory from the archive root, so a
	// source tree cut by GitHub ({repo}-{sha}/...) becomes contents/ directly.
	Flatten bool
}

// Build unpacks an archive into destDir as a conforming bundle.
//
// The identity and entrypoint are taken from an app.toml at the tree's root
// when it declares them, and otherwise from the Builder: a source tree's own
// manifest is the author's statement about the app, while the Builder holds
// what was derived from where the tree was fetched. `version` always comes from
// the Builder, since only the installer knows the revision it installed
// (§2.1).
//
// The entrypoint is never inferred from which files happen to carry an
// executable bit: guessing picks the wrong file as readily as the right one,
// and a bundle whose single command does nothing is worse than one that exposes
// nothing.
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
	m, _, err := b.build(archivePath, destDir)
	return m, err
}

// build is Build, also reporting whether the tree's own manifest supplied the
// identity. The installer logs that, because an authored id can name a
// namespace the caller did not derive from where the tree came from.
func (b Builder) build(archivePath, destDir string) (*bundle.Manifest, bool, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, false, err
	}

	// Unpack into a scratch directory first so a wrapper level can be dropped
	// before the tree becomes contents/.
	scratch, err := os.MkdirTemp(destDir, ".unpack-")
	if err != nil {
		return nil, false, err
	}
	defer os.RemoveAll(scratch)

	if err := extractArchive(archivePath, scratch); err != nil {
		return nil, false, err
	}

	tree := scratch
	if b.Flatten {
		// GitHub source archives nest everything under one {repo}-{ref}/.
		if inner, ok := singleSubdir(scratch); ok {
			tree = inner
		}
	}

	// An archive that already holds a conforming bundle is installed as that
	// bundle rather than wrapped again, which is how a project that publishes
	// bundles is installed from its own release assets.
	if m, adopted, err := b.adoptBundle(tree, destDir); err != nil || adopted {
		return m, false, err
	}

	declared, err := bundle.DeclaredFromTree(tree)
	if err != nil {
		return nil, false, fmt.Errorf("the tree's %s cannot be used: %w", bundle.ManifestFile, err)
	}

	contents := filepath.Join(destDir, bundle.ContentsDir)
	if err := os.Rename(tree, contents); err != nil {
		return nil, false, err
	}

	m := &bundle.Manifest{
		ID:         b.ID,
		Name:       b.Name,
		Version:    b.Version,
		Entrypoint: declared.Entrypoint,
	}
	if declared.ID != "" {
		m.ID = declared.ID
	}
	if declared.Name != "" {
		m.Name = declared.Name
	}
	if b.OverrideID != "" {
		m.ID = b.OverrideID
	}
	if b.OverrideName != "" {
		m.Name = b.OverrideName
	}
	if err := m.Validate(); err != nil {
		return nil, false, err
	}
	if err := bundle.Write(m, filepath.Join(destDir, bundle.ManifestFile)); err != nil {
		return nil, false, err
	}

	// The identity came from the tree when it declared one and no flag
	// overrode it.
	fromTree := (declared.ID != "" && b.OverrideID == "") || (declared.Name != "" && b.OverrideName == "")
	return m, fromTree, nil
}

// adoptBundle installs tree as-is when it holds exactly one conforming bundle
// directory, moving that bundle's app.toml and contents/ into destDir.
//
// An identity override is applied here, since the bundle already carries a
// manifest of its own; the version is left alone, because the shipped manifest
// is the author's statement about which build this is (§2.1).
//
// Args:
//
//	tree: the extracted archive root.
//	destDir: directory that will hold app.toml and contents/.
//
// Returns:
//
//	The manifest and true when tree held a bundle; nil and false when it did
//	not, leaving destDir untouched so the caller can wrap the tree instead.
func (b Builder) adoptBundle(tree, destDir string) (*bundle.Manifest, bool, error) {
	root, ok := singleSubdir(tree)
	if !ok {
		return nil, false, nil
	}

	m, err := bundle.VerifyDir(root)
	if err != nil {
		// Not a bundle: the archive is a plain tree and will be wrapped.
		return nil, false, nil
	}

	if b.OverrideID != "" || b.OverrideName != "" {
		if b.OverrideID != "" {
			m.ID = b.OverrideID
		}
		if b.OverrideName != "" {
			m.Name = b.OverrideName
		}
		if err := m.Validate(); err != nil {
			return nil, true, err
		}
		if err := bundle.Write(m, filepath.Join(root, bundle.ManifestFile)); err != nil {
			return nil, true, err
		}
	}

	for _, name := range []string{bundle.ManifestFile, bundle.ContentsDir} {
		if err := os.Rename(filepath.Join(root, name), filepath.Join(destDir, name)); err != nil {
			return nil, true, err
		}
	}
	return m, true, nil
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
