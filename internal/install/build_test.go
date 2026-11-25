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

// bundleArchive writes a bundle-shaped archive: one <id>/ holding app.toml and
// contents/, which is what a project publishes when it ships its own bundles.
func bundleArchive(t *testing.T, dir string, files map[string]string) string {
	t.Helper()

	path := filepath.Join(dir, "bundle.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	for name, body := range files {
		hdr := &tar.Header{
			Name: "org.example.prebuilt/" + name,
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

// TestBuildAdoptsPrebuiltBundle covers a release asset that already conforms: it
// is installed as that bundle, not wrapped again, and its own version is kept.
func TestBuildAdoptsPrebuiltBundle(t *testing.T) {
	dir := t.TempDir()
	archive := bundleArchive(t, dir, map[string]string{
		bundle.ManifestFile: "id = \"org.example.prebuilt\"\nname = \"Prebuilt\"\nversion = \"1.2.3\"\nentrypoint = \"bin/tool\"\n",
		"contents/bin/tool": "#!/bin/sh\necho hi\n",
	})

	out := filepath.Join(dir, "prepared")
	m, err := Builder{ID: "io.github.owner.repo", Name: "repo", Version: "tag", Flatten: false}.
		Build(archive, out)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	if m.ID != "org.example.prebuilt" || m.Version != "1.2.3" {
		t.Errorf("the shipped manifest should be kept, got %q %q", m.ID, m.Version)
	}

	// Nothing is nested under contents/: the bundle was adopted as it stands.
	if _, err := os.Stat(filepath.Join(out, bundle.ContentsDir, "bin", "tool")); err != nil {
		t.Errorf("expected contents/bin/tool, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, bundle.ContentsDir, "org.example.prebuilt")); err == nil {
		t.Error("the bundle was wrapped inside itself")
	}
	if _, err := bundle.VerifyDir(out); err != nil {
		t.Errorf("adopted root should conform: %v", err)
	}
}

// TestBuildWrapsPlainReleaseAsset covers an ordinary release asset: a tree with
// no bundle around it is wrapped as before.
func TestBuildWrapsPlainReleaseAsset(t *testing.T) {
	dir := t.TempDir()
	archive := makeSourceArchive(t, dir, map[string]string{
		"bin/tool": "#!/bin/sh\necho hi\n",
	})

	out := filepath.Join(dir, "prepared")
	m, err := Builder{ID: "io.github.owner.repo", Name: "repo", Version: "v1"}.Build(archive, out)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}
	if m.ID != "io.github.owner.repo" || m.Version != "v1" {
		t.Errorf("expected the derived identity, got %q %q", m.ID, m.Version)
	}
	if _, err := bundle.VerifyDir(out); err != nil {
		t.Errorf("wrapped root should conform: %v", err)
	}
}
