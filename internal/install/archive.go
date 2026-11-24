package install

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractArchive unpacks a .tar.gz, .tgz or .zip file into destDir, preserving
// executable bits and symlinks (§6).
//
// Args:
//
//	archivePath: the archive on disk.
//	destDir: directory to unpack into; must already exist.
//
// Returns:
//
//	An error if the format is unsupported or the archive cannot be unpacked.
func extractArchive(archivePath, destDir string) error {
	lower := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarGz(archivePath, destDir)
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(archivePath, destDir)
	default:
		return fmt.Errorf("unsupported archive format: %s (expected .tar.gz or .zip)", filepath.Base(archivePath))
	}
}

func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		target, err := safeJoin(destDir, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(target, tr, os.FileMode(hdr.Mode).Perm()); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := writeSymlink(destDir, target, hdr.Linkname); err != nil {
				return err
			}
		}
	}
}

func extractZip(archivePath, destDir string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer zr.Close()

	for _, f := range zr.File {
		target, err := safeJoin(destDir, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeFile(target, rc, f.Mode().Perm())
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFile(target string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// writeSymlink creates a symlink at target, but only when its destination stays
// inside destDir.
func writeSymlink(destDir, target, linkname string) error {
	if filepath.IsAbs(linkname) {
		return fmt.Errorf("refusing absolute symlink %q -> %q", target, linkname)
	}
	resolved := filepath.Join(filepath.Dir(target), filepath.FromSlash(linkname))
	if !inside(destDir, resolved) {
		return fmt.Errorf("refusing symlink escaping the archive: %q -> %q", target, linkname)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	_ = os.Remove(target)
	return os.Symlink(linkname, target)
}

// safeJoin joins base and an untrusted archive path, returning an error when
// the result would land outside base.
func safeJoin(base, untrusted string) (string, error) {
	clean := filepath.FromSlash(strings.TrimPrefix(untrusted, "/"))
	target := filepath.Join(base, clean)
	if !inside(base, target) {
		return "", fmt.Errorf("path traversal detected: %s", untrusted)
	}
	return target, nil
}

// inside reports whether path is base or sits below it.
func inside(base, path string) bool {
	base = filepath.Clean(base)
	path = filepath.Clean(path)
	return path == base || strings.HasPrefix(path, base+string(os.PathSeparator))
}
