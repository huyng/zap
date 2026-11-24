package install

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/huyng/zap/internal/bundle"
	"github.com/huyng/zap/internal/scope"
)

// Remover uninstalls app roots. Like Installer, all of its inputs are fields.
type Remover struct {
	Scope scope.Scope
	// Purge also deletes data/, losing user data.
	Purge bool
	// Force removes the directory even when it holds no readable manifest.
	Force bool
	// Log receives progress messages; nil discards them.
	Log io.Writer
}

// RemoveResult describes a finished removal.
type RemoveResult struct {
	// ID is the app that was removed.
	ID string
	// Root is the app root that was removed, or what is left of it.
	Root string
	// Unlinked is the exposure symlink or wrapper that was deleted, if any.
	Unlinked string
	// KeptData is data/ that survived because Purge was false.
	KeptData string
}

// Remove uninstalls the app with the given id (§6): contents/, cache/, run/ and
// app.toml go; data/ survives unless Purge is set. The exposure symlink or
// wrapper is removed when it points into this root.
//
// Args:
//
//	id: the app id, which is also the directory name under the scope root.
//
// Returns:
//
//	A summary of what was removed, or an error if the app is not installed or
//	cannot be read.
func (r *Remover) Remove(id string) (*RemoveResult, error) {
	if err := bundle.ValidID(id); err != nil {
		return nil, err
	}

	root := r.Scope.Root(id)
	info, err := os.Stat(root)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("%s is not installed at %s", id, root)
	case err != nil:
		return nil, err
	case !info.IsDir():
		return nil, fmt.Errorf("%s is not a directory", root)
	}

	result := &RemoveResult{ID: id, Root: root}

	manifest, err := r.loadManifest(root)
	if err != nil {
		return nil, err
	}

	if manifest != nil && manifest.Runnable() {
		unlinked, err := r.unlink(manifest, root)
		if err != nil {
			return nil, err
		}
		result.Unlinked = unlinked
	}

	// Everything the layout owns except data/.
	for _, name := range []string{bundle.ContentsDir, "cache", "run", bundle.ManifestFile} {
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			return nil, err
		}
	}

	dataDir := filepath.Join(root, "data")
	if r.Purge {
		if err := os.RemoveAll(dataDir); err != nil {
			return nil, err
		}
	} else if info, err := os.Stat(dataDir); err == nil && info.IsDir() {
		result.KeptData = dataDir
	}

	// The root goes too unless data/ was kept. Removing an empty directory is
	// safe; a non-empty one means something unexpected is in there.
	if result.KeptData == "" {
		if err := os.Remove(root); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("removing %s: %w", root, err)
		}
	}

	return result, nil
}

// loadManifest reads the manifest that makes root an app root. A directory
// without one is not installed unless Force says to delete it anyway.
func (r *Remover) loadManifest(root string) (*bundle.Manifest, error) {
	manifestPath := filepath.Join(root, bundle.ManifestFile)

	m, err := bundle.Load(manifestPath)
	switch {
	case err == nil:
		return m, nil
	case errors.Is(err, fs.ErrNotExist) && r.Force:
		r.logf("%s has no %s; removing it anyway", root, bundle.ManifestFile)
		return nil, nil
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("%s has no %s, so it is not an app root: a removed app leaves only data/ behind; pass --force to delete the directory anyway", root, bundle.ManifestFile)
	case r.Force:
		r.logf("ignoring unreadable %s: %s", manifestPath, err)
		return nil, nil
	default:
		return nil, fmt.Errorf("reading %s: %w (pass --force to delete the directory anyway)", manifestPath, err)
	}
}

// unlink removes the exposed entrypoint for m, but only when zap put it there: a
// symlink pointing into root, or a wrapper naming root.
func (r *Remover) unlink(m *bundle.Manifest, root string) (string, error) {
	link := r.Scope.Link(m.EntryName())

	info, err := os.Lstat(link)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(link)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(link), target)
		}
		if !inside(root, target) {
			r.logf("%s points to %s, outside %s; leaving it alone", link, target, root)
			return "", nil
		}
	} else if !isManagedWrapper(link, root) {
		r.logf("%s is not managed by zap; leaving it alone", link)
		return "", nil
	}

	if err := os.Remove(link); err != nil {
		return "", err
	}
	return link, nil
}

// isManagedWrapper reports whether path is the wrapper writeWrapper wrote for
// root.
func isManagedWrapper(path, root string) bool {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 4096 {
		return false
	}
	text := string(data)
	return strings.HasPrefix(text, "#!") && strings.Contains(text, "APP_ROOT="+shellQuote(root))
}

func (r *Remover) logf(format string, args ...any) {
	if r.Log == nil {
		return
	}
	fmt.Fprintf(r.Log, format+"\n", args...)
}
