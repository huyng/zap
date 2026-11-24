// Package scope locates app roots on disk and reports what is installed in them.
package scope

import (
	"os"
	"path/filepath"
)

// Scope is where app roots and exposure links live (§1.1, §6).
type Scope struct {
	// AppsDir holds one directory per app, e.g. ~/.apps.
	AppsDir string
	// BinDir receives the exposed entrypoint, e.g. ~/.local/bin.
	BinDir string
	// System selects system-scope exposure, which needs a wrapper that sets
	// APP_ROOT because there is no default base for system installs (§6.1).
	System bool
}

// Root is the app root for id.
func (s Scope) Root(id string) string {
	return filepath.Join(s.AppsDir, id)
}

// Link is where the exposed entrypoint for name would live.
func (s Scope) Link(name string) string {
	return filepath.Join(s.BinDir, name)
}

// RootMode is the app root mode from §1.3.
func (s Scope) RootMode() os.FileMode {
	if s.System {
		return 0o750
	}
	return 0o700
}

// Label names the scope for humans.
func (s Scope) Label() string {
	if s.System {
		return "system"
	}
	return "user"
}
