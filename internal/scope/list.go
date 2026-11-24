package scope

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/huyng/zap/internal/bundle"
)

// Entry is one installed app, as recorded by its own manifest.
type Entry struct {
	ID         string
	Name       string
	Version    string
	Entrypoint string
	Root       string
	// ReadErr is set when the directory holds an app.toml that cannot be used,
	// so a single broken install does not hide the rest.
	ReadErr error
}

// Runnable reports whether the app declares an entrypoint.
func (e Entry) Runnable() bool {
	return e.Entrypoint != ""
}

// List reports the apps installed in s, ordered by id.
//
// A directory without an app.toml is not an app and is skipped, which is what a
// non-purge removal leaves behind (§6). A directory whose app.toml cannot be
// read is reported with ReadErr set rather than omitted.
//
// Returns:
//
//	The installed apps, or an error if the scope root itself cannot be read. A
//	missing scope root is not an error; it holds no apps.
func List(s Scope) ([]Entry, error) {
	dirs, err := os.ReadDir(s.AppsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(dirs))
	for _, d := range dirs {
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			continue
		}

		root := s.Root(d.Name())
		manifestPath := filepath.Join(root, bundle.ManifestFile)
		if _, err := os.Stat(manifestPath); err != nil {
			continue
		}

		m, err := bundle.Load(manifestPath)
		if err != nil {
			entries = append(entries, Entry{ID: d.Name(), Root: root, ReadErr: err})
			continue
		}
		entries = append(entries, Entry{
			ID:         m.ID,
			Name:       m.Name,
			Version:    m.Version,
			Entrypoint: m.Entrypoint,
			Root:       root,
		})
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return entries, nil
}
