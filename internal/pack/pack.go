// Package pack turns an assembled app tree into a distributable bundle
// archive: the reverse of installing one.
package pack

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/huyng/zap/internal/bundle"
)

// archiveSuffix is the one format pack writes. A tar.gz carries symlinks and
// executable bits, which a bundle needs, and every installer accepts it.
const archiveSuffix = ".tar.gz"

// Options describes the archive to produce.
type Options struct {
	// OutDir receives the archive; empty means the current directory.
	OutDir string
	// OS and Arch are platform tokens in the archive name, e.g. linux and
	// arm64. An empty token is left out of the name.
	OS   string
	Arch string
	// Log receives progress messages; nil discards them.
	Log io.Writer
}

// Result describes a finished archive.
type Result struct {
	// Manifest is the manifest that went into the archive.
	Manifest *bundle.Manifest
	// Path is the archive that was written.
	Path string
}

// Bundle packs srcDir into a bundle archive and reports where it landed.
//
// srcDir is either a bundle root (app.toml beside contents/) or a flat source
// tree (app.toml beside the files themselves, with no contents/). A flat tree
// is transformed the way §6 describes: the manifest goes to the archive root
// and everything else goes under contents/.
//
// Either way the archive holds exactly one <id>/ directory, named from the
// manifest, so the result installs with Installer.InstallArchive. The name of
// srcDir itself is never used, and no file is ever written inside it.
//
// Args:
//
//	srcDir: the tree to pack.
//	opts: output directory, platform tokens and log sink.
//
// Returns:
//
//	The archive path and the manifest it holds, or an error if the tree is not
//	a conforming bundle and cannot be made into one.
func Bundle(srcDir string, opts Options) (*Result, error) {
	m, flat, err := readSource(srcDir)
	if err != nil {
		return nil, err
	}

	name := archiveName(m.ID, opts.OS, opts.Arch)
	path, err := filepath.Abs(filepath.Join(opts.OutDir, name))
	if err != nil {
		return nil, err
	}

	entries, err := collect(srcDir, m.ID, flat, path)
	if err != nil {
		return nil, err
	}

	logf(opts.Log, "packing %s (%d paths)", srcDir, len(entries))
	written, err := writeArchive(opts.OutDir, name, entries)
	if err != nil {
		return nil, err
	}
	return &Result{Manifest: m, Path: written}, nil
}

// readSource loads the manifest from srcDir and reports whether the tree is a
// flat source tree rather than a bundle root.
//
// A tree holding contents/ is a bundle root and must already conform. A tree
// without one is flat, and its entrypoint is checked against the tree itself,
// because contents/ is only created at pack time.
func readSource(srcDir string) (*bundle.Manifest, bool, error) {
	manifestPath := filepath.Join(srcDir, bundle.ManifestFile)
	m, err := bundle.Load(manifestPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, fmt.Errorf("%s holds no %s, so there is no bundle to pack", srcDir, bundle.ManifestFile)
	case err != nil:
		return nil, false, err
	}

	contents := filepath.Join(srcDir, bundle.ContentsDir)
	info, err := os.Stat(contents)
	switch {
	case err == nil && info.IsDir():
		if _, err := bundle.VerifyDir(srcDir); err != nil {
			return nil, false, err
		}
		return m, false, nil
	case err == nil:
		return nil, false, fmt.Errorf("%s is not a directory", contents)
	case !errors.Is(err, fs.ErrNotExist):
		return nil, false, err
	}

	if m.Runnable() {
		entry := filepath.Join(srcDir, filepath.FromSlash(m.Entrypoint))
		if err := bundle.CheckExecutable(entry); err != nil {
			return nil, false, fmt.Errorf("%s: entrypoint %q cannot be packed: %w", manifestPath, m.Entrypoint, err)
		}
	}
	return m, true, nil
}

// entry is one path to write into the archive.
type entry struct {
	// src is the path on disk to read from.
	src string
	// name is the path inside the archive, slash-separated.
	name string
	// info describes the path itself; a symlink is described as a symlink.
	info fs.FileInfo
}

// collect walks srcDir and maps it onto archive paths. Every path is placed
// under <id>/, and a flat tree's files land under <id>/contents/. The archive
// being written is skipped, so packing into the source tree cannot include
// itself.
func collect(srcDir, id string, flat bool, skip string) ([]entry, error) {
	root, err := filepath.Abs(srcDir)
	if err != nil {
		return nil, err
	}

	var entries []entry
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root || path == skip {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		name := id + "/" + rel
		if flat && rel != bundle.ManifestFile {
			name = id + "/" + bundle.ContentsDir + "/" + rel
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		entries = append(entries, entry{src: path, name: name, info: info})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Sorted so the same tree always gives the same bytes.
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	return entries, nil
}

// archiveName is <id>[-os][-arch].tar.gz. The tokens are what SelectAsset
// matches on, so a release is found by its own name.
func archiveName(id, goos, goarch string) string {
	name := id
	if goos != "" {
		name += "-" + goos
	}
	if goarch != "" {
		name += "-" + goarch
	}
	return name + archiveSuffix
}

// writeArchive writes the entries to a temporary file in dir and renames it
// into place, so a failed run leaves no half-written archive behind.
func writeArchive(dir, name string, entries []entry) (string, error) {
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp(dir, ".bundle-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())

	if err := writeTarGz(tmp, entries); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return "", err
	}

	final := filepath.Join(dir, name)
	if err := os.Rename(tmp.Name(), final); err != nil {
		return "", err
	}
	return final, nil
}

// logf writes one progress line, if a log is configured.
func logf(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, format+"\n", args...)
}
