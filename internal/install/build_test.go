package install

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/huyng/zap/internal/bundle"
)

// makeSourceArchive writes a tarball shaped like a GitHub source archive: one
// {repo}-{sha}/ directory holding the repo's files.
func makeSourceArchive(t *testing.T, dir string, files map[string]string) string {
	t.Helper()

	path := filepath.Join(dir, "src.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	for name, body := range files {
		hdr := &tar.Header{
			Name: "repo-abc1234/" + name,
			Mode: 0o755,
			Size: int64(len(body)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// buildFromTree is the common setup: one flat source tree, packed with b.
func buildFromTree(t *testing.T, treeManifest string, b Builder) (*bundle.Manifest, bool) {
	t.Helper()
	dir := t.TempDir()

	archive := makeSourceArchive(t, dir, map[string]string{
		bundle.ManifestFile: treeManifest,
		"bin/tool":          "#!/bin/sh\necho hi\n",
	})

	out := filepath.Join(dir, "prepared")
	m, fromTree, err := b.build(archive, out)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if _, err := bundle.VerifyDir(out); err != nil {
		t.Errorf("built root should conform: %v", err)
	}
	return m, fromTree
}

// TestBuildUsesTreeIdentity covers a tree that ships its own app.toml: the
// author's id and name win over what was derived from the source, while the
// version still comes from the commit.
func TestBuildUsesTreeIdentity(t *testing.T) {
	m, fromTree := buildFromTree(t, `id = "org.example.authored"
name = "Authored Name"
version = "9.9.9"
entrypoint = "bin/tool"
`, Builder{ID: "io.github.owner.repo", Name: "repo", Version: "abc1234", Flatten: true})

	if !fromTree {
		t.Error("expected the identity to be reported as coming from the tree")
	}
	if m.ID != "org.example.authored" {
		t.Errorf("tree's id should win, got %q", m.ID)
	}
	if m.Name != "Authored Name" {
		t.Errorf("tree's name should win, got %q", m.Name)
	}
	if m.Version != "abc1234" {
		t.Errorf("version comes from the commit, got %q", m.Version)
	}
	if m.Entrypoint != "bin/tool" {
		t.Errorf("entrypoint from the tree, got %q", m.Entrypoint)
	}
}

// TestBuildFlagBeatsTree covers --id and --name beating the tree's manifest.
func TestBuildFlagBeatsTree(t *testing.T) {
	m, fromTree := buildFromTree(t, `id = "org.example.authored"
name = "Authored Name"
entrypoint = "bin/tool"
`, Builder{
		ID:           "io.github.owner.repo",
		Name:         "repo",
		Version:      "abc1234",
		OverrideID:   "org.example.flagged",
		OverrideName: "Flagged",
		Flatten:      true,
	})

	if fromTree {
		t.Error("a flag result should not be reported as coming from the tree")
	}
	if m.ID != "org.example.flagged" {
		t.Errorf("--id should win, got %q", m.ID)
	}
	if m.Name != "Flagged" {
		t.Errorf("--name should win, got %q", m.Name)
	}
}

// TestBuildWithoutTreeIdentity covers a tree with no manifest, or one that names
// nothing: the derived identity is used and the bundle is a payload.
func TestBuildWithoutTreeIdentity(t *testing.T) {
	for _, name := range []string{"no manifest", "manifest without identity"} {
		t.Run(name, func(t *testing.T) {
			treeManifest := ""
			if name != "no manifest" {
				treeManifest = `version = "1.0"`
			}

			dir := t.TempDir()
			files := map[string]string{"bin/tool": "#!/bin/sh\necho hi\n"}
			if treeManifest != "" {
				files[bundle.ManifestFile] = treeManifest
			}
			archive := makeSourceArchive(t, dir, files)

			out := filepath.Join(dir, "prepared")
			m, fromTree, err := Builder{
				ID: "io.github.owner.repo", Name: "repo", Version: "abc1234", Flatten: true,
			}.build(archive, out)
			if err != nil {
				t.Fatalf("build failed: %v", err)
			}
			if fromTree {
				t.Error("nothing was declared, so the identity is not from the tree")
			}
			if m.ID != "io.github.owner.repo" || m.Name != "repo" {
				t.Errorf("expected the derived identity, got %q / %q", m.ID, m.Name)
			}
			if m.Entrypoint != "" {
				t.Errorf("expected no entrypoint, got %q", m.Entrypoint)
			}
		})
	}
}

// TestBuildRejectsInvalidTreeManifest covers a tree whose manifest is malformed
// or names an unusable id or entrypoint: an error, never a silent fallback.
func TestBuildRejectsInvalidTreeManifest(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
	}{
		{"malformed toml", "id = \n"},
		{"invalid id", "id = \"nope\"\n"},
		{"absolute entrypoint", "entrypoint = \"/bin/sh\"\n"},
		{"escaping entrypoint", "entrypoint = \"../outside\"\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			archive := makeSourceArchive(t, dir, map[string]string{
				bundle.ManifestFile: tc.manifest,
			})

			_, _, err := Builder{
				ID: "io.github.owner.repo", Name: "repo", Version: "abc1234", Flatten: true,
			}.build(archive, filepath.Join(dir, "prepared"))
			if err == nil {
				t.Fatal("expected the tree's manifest to be refused")
			}
		})
	}
}
