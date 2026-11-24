// Package bundle reads, validates and verifies app bundles laid out as
// described by the App Bundle Layout Specification v1.0.
package bundle

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	// ManifestFile is the manifest name at the app root (§1.2).
	ManifestFile = "app.toml"
	// ContentsDir is the immutable tree name at the app root (§1.2).
	ContentsDir = "contents"
)

// Manifest is the parsed app.toml. Optional fields are kept as zero values
// when absent; unknown keys are ignored, as the specification requires.
type Manifest struct {
	ID          string
	Name        string
	Version     string
	Entrypoint  string
	Icon        string
	Description string
	License     string
	Homepage    string
	MIMEHandles []string
}

// rawManifest mirrors app.toml for decoding and encoding.
type rawManifest struct {
	ID          string   `toml:"id,omitempty"`
	Name        string   `toml:"name,omitempty"`
	Version     string   `toml:"version,omitempty"`
	Entrypoint  string   `toml:"entrypoint,omitempty"`
	Icon        string   `toml:"icon,omitempty"`
	Description string   `toml:"description,omitempty"`
	License     string   `toml:"license,omitempty"`
	Homepage    string   `toml:"homepage,omitempty"`
	MIME        *rawMIME `toml:"mime,omitempty"`
}

type rawMIME struct {
	Handles []string `toml:"handles,omitempty"`
}

// Parse decodes an app.toml document and validates it.
//
// Args:
//
//	data: the raw TOML bytes.
//
// Returns:
//
//	The parsed manifest, or an error if it is malformed or non-conforming.
func Parse(data []byte) (*Manifest, error) {
	var raw rawManifest
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", ManifestFile, err)
	}

	m := &Manifest{
		ID:          raw.ID,
		Name:        raw.Name,
		Version:     raw.Version,
		Entrypoint:  raw.Entrypoint,
		Icon:        raw.Icon,
		Description: raw.Description,
		License:     raw.License,
		Homepage:    raw.Homepage,
	}
	if raw.MIME != nil {
		m.MIMEHandles = raw.MIME.Handles
	}

	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

// Load reads and validates the manifest at path.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Validate checks the manifest against §2.
//
// Returns:
//
//	An error describing the first field that breaks the specification.
func (m *Manifest) Validate() error {
	required := []struct {
		name  string
		value string
	}{
		{"id", m.ID},
		{"name", m.Name},
		{"version", m.Version},
	}
	for _, f := range required {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("%s: required field %q is missing", ManifestFile, f.name)
		}
	}
	if err := ValidID(m.ID); err != nil {
		return err
	}
	// entrypoint is optional (§2.2); a bundle without one is installed and
	// exposed to nothing.
	if m.Entrypoint != "" {
		if err := validEntrypoint(m.Entrypoint); err != nil {
			return err
		}
	}
	return nil
}

// Runnable reports whether the bundle declares an entrypoint.
func (m *Manifest) Runnable() bool {
	return m.Entrypoint != ""
}

// EntryPath returns the absolute path of the entrypoint for a bundle rooted at
// root. The manifest paths are always relative to contents/ (§2).
func (m *Manifest) EntryPath(root string) string {
	return filepath.Join(root, ContentsDir, filepath.FromSlash(m.Entrypoint))
}

// EntryName is the file name of the entrypoint, used when exposing the app.
func (m *Manifest) EntryName() string {
	return path.Base(m.Entrypoint)
}

// idPattern is the filesystem-safe shape required by §2.3.
var idPattern = regexp.MustCompile(`^[a-z0-9_-]+(\.[a-z0-9_-]+)+$`)

// ValidID reports whether id satisfies §2.3.
func ValidID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("%s: id %q must be lowercase, dot-separated, at least two segments, and use only a-z, 0-9, '.', '-', '_'", ManifestFile, id)
	}
	if strings.Contains(id, "..") {
		return fmt.Errorf("%s: id %q must not contain %q", ManifestFile, id, "..")
	}
	return nil
}

// validEntrypoint rejects absolute paths and paths that escape contents/.
func validEntrypoint(entry string) error {
	if strings.Contains(entry, `\`) {
		return fmt.Errorf("%s: entrypoint %q must use forward slashes", ManifestFile, entry)
	}
	if path.IsAbs(entry) {
		return fmt.Errorf("%s: entrypoint %q must be relative to %s/", ManifestFile, entry, ContentsDir)
	}
	clean := path.Clean(entry)
	if clean == ".." || strings.HasPrefix(clean, "../") || clean == "." {
		return fmt.Errorf("%s: entrypoint %q must stay inside %s/", ManifestFile, entry, ContentsDir)
	}
	return nil
}

// Declared is what an app.toml inside a source tree says about itself. Every
// field is optional: a tree may ship no manifest, or one that is incomplete.
type Declared struct {
	// Entrypoint is the executable path the tree declares, relative to where
	// the tree's files will land. Empty when none is declared.
	Entrypoint string
	// ID and Name are the identity the author chose, empty when unset.
	ID   string
	Name string
}

// DeclaredFromTree reads the identity and entrypoint an app.toml inside a tree
// declares, without requiring the rest of that manifest to be valid.
//
// A tree's manifest is authored, so it may be incomplete or stale: `version` is
// normally absent because the installer supplies it from the revision
// installed (§2.1). A malformed file, or one declaring an invalid entrypoint or
// id, is an error rather than something to ignore, so a typo surfaces here
// instead of as a surprising app root.
//
// Args:
//
//	treeDir: directory that may hold an app.toml, typically the tree that will
//	  become contents/.
//
// Returns:
//
//	What the tree declares; zero when it holds no manifest. An error if the
//	file is unreadable, malformed, or declares an invalid entrypoint or id.
func DeclaredFromTree(treeDir string) (Declared, error) {
	data, err := os.ReadFile(filepath.Join(treeDir, ManifestFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Declared{}, nil
	}
	if err != nil {
		return Declared{}, err
	}

	var raw rawManifest
	if err := toml.Unmarshal(data, &raw); err != nil {
		return Declared{}, fmt.Errorf("parsing %s in the tree: %w", ManifestFile, err)
	}

	d := Declared{}
	if strings.TrimSpace(raw.Entrypoint) != "" {
		if err := validEntrypoint(raw.Entrypoint); err != nil {
			return Declared{}, err
		}
		d.Entrypoint = raw.Entrypoint
	}
	if strings.TrimSpace(raw.ID) != "" {
		if err := ValidID(raw.ID); err != nil {
			return Declared{}, err
		}
		d.ID = raw.ID
	}
	d.Name = strings.TrimSpace(raw.Name)
	return d, nil
}

// Write encodes m as an app.toml at path.
func Write(m *Manifest, path string) error {
	raw := rawManifest{
		ID:          m.ID,
		Name:        m.Name,
		Version:     m.Version,
		Entrypoint:  m.Entrypoint,
		Icon:        m.Icon,
		Description: m.Description,
		License:     m.License,
		Homepage:    m.Homepage,
	}
	if len(m.MIMEHandles) > 0 {
		raw.MIME = &rawMIME{Handles: m.MIMEHandles}
	}

	data, err := toml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", ManifestFile, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	return nil
}

// ErrNotABundle is returned when a directory does not look like a bundle root.
var ErrNotABundle = errors.New("not an app bundle")
