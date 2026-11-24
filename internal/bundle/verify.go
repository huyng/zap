package bundle

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

// VerifyDir checks that dir is a conforming bundle root and returns its
// manifest. It is the installer's pre-extraction and pre-install check (§6).
//
// Args:
//
//	dir: directory expected to hold app.toml and contents/.
//
// Returns:
//
//	The validated manifest, or an error naming the failing requirement.
func VerifyDir(dir string) (*Manifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	// The bundle root holds app.toml and contents/, and nothing else: no
	// install scripts or hooks (§6).
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	expected := []string{ManifestFile, ContentsDir}
	if !slices.Equal(names, expected) {
		return nil, fmt.Errorf("%w: %s must contain exactly %q and %q, found %v",
			ErrNotABundle, dir, ManifestFile, ContentsDir, names)
	}

	contents := filepath.Join(dir, ContentsDir)
	info, err := os.Stat(contents)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a directory", ErrNotABundle, contents)
	}

	m, err := Load(filepath.Join(dir, ManifestFile))
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotABundle, err)
	}

	// A bundle with no entrypoint is installed but exposes nothing (§2.2).
	if m.Runnable() {
		entry := m.EntryPath(dir)
		info, err = os.Stat(entry) // follows symlinks, as §2 requires of entrypoint
		if err != nil {
			return nil, fmt.Errorf("%w: entrypoint %q does not resolve inside %s/: %s",
				ErrNotABundle, m.Entrypoint, ContentsDir, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return nil, fmt.Errorf("%w: entrypoint %q is not an executable file", ErrNotABundle, m.Entrypoint)
		}
	}
	return m, nil
}
