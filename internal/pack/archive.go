package pack

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"
)

// epoch is the fixed timestamp every entry carries. Two packs of the same tree
// then give the same bytes, which makes a checksum of the archive meaningful.
var epoch = time.Unix(0, 0)

// writeTarGz writes entries as a .tar.gz, sorted, with fixed timestamps.
func writeTarGz(w io.Writer, entries []entry) error {
	gz := gzip.NewWriter(w)
	gz.Header.ModTime = epoch
	tw := tar.NewWriter(gz)

	for _, e := range entries {
		hdr, err := tarHeader(e)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if err := copyFile(tw, e.src); err != nil {
			return err
		}
	}

	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// tarHeader describes one entry, preserving the executable bit that §6
// requires an installer to keep.
func tarHeader(e entry) (*tar.Header, error) {
	hdr := &tar.Header{
		Name:    e.name,
		Mode:    int64(e.info.Mode().Perm()),
		ModTime: epoch,
	}

	switch {
	case e.info.IsDir():
		hdr.Typeflag = tar.TypeDir
		hdr.Name = e.name + "/"
		hdr.Mode = 0o755
	case e.info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(e.src)
		if err != nil {
			return nil, err
		}
		if err := checkLink(e.name, target); err != nil {
			return nil, err
		}
		hdr.Typeflag = tar.TypeSymlink
		hdr.Linkname = target
		hdr.Mode = 0o777
	case e.info.Mode().IsRegular():
		hdr.Typeflag = tar.TypeReg
		hdr.Size = e.info.Size()
	default:
		return nil, fmt.Errorf("%s: cannot pack %s", e.src, e.info.Mode().Type())
	}
	return hdr, nil
}

// checkLink rejects a symlink that would land outside the bundle, matching what
// an installer refuses to unpack.
//
// Args:
//
//	name: the symlink's path inside the archive, starting with <id>/.
//	target: the link's target, as stored in the archive.
//
// Returns:
//
//	An error if the target is absolute or climbs out of <id>/.
func checkLink(name, target string) error {
	if path.IsAbs(target) {
		return fmt.Errorf("symlink %s points to absolute path %s", name, target)
	}
	prefix := name[:strings.Index(name, "/")]
	resolved := path.Join(path.Dir(name), target)
	if resolved != prefix && !strings.HasPrefix(resolved, prefix+"/") {
		return fmt.Errorf("symlink %s -> %s escapes the bundle", name, target)
	}
	return nil
}

// copyFile copies the contents of src into w.
func copyFile(w io.Writer, src string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(w, f)
	return err
}
