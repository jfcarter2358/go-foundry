package pack

// archive.go — the file a player downloads.
//
// A staged install tree is a directory; what gets handed to a player is one
// file. The Taskfile makes the Linux one with tar, which is not something an
// editor running on Windows can count on having, so both archives are written
// here in Go instead.

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// TarGz writes the install tree at dir to a gzipped tarball at out, with every
// entry under a top-level prefix/ directory so that unpacking it makes one
// folder rather than scattering a game across somebody's Downloads.
func TarGz(dir, out, prefix, exeName string) error {
	return writeAtomically(out, func(w io.Writer) error {
		gz := gzip.NewWriter(w)
		tw := tar.NewWriter(gz)
		err := walkTree(dir, prefix, func(name, full string, info fs.FileInfo) error {
			if info.IsDir() {
				return tw.WriteHeader(&tar.Header{
					Typeflag: tar.TypeDir, Name: name + "/", Mode: 0755, ModTime: info.ModTime(),
				})
			}
			hdr := &tar.Header{
				Typeflag: tar.TypeReg, Name: name, Size: info.Size(),
				Mode: int64(archiveMode(name, prefix, exeName)), ModTime: info.ModTime(),
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			return copyInto(tw, full)
		})
		if err != nil {
			return err
		}
		if err := tw.Close(); err != nil {
			return err
		}
		return gz.Close()
	})
}

// Zip writes the install tree at dir to a zip at out, under prefix/ like TarGz.
// The Windows archive: Explorer opens a zip with nothing installed.
func Zip(dir, out, prefix, exeName string) error {
	return writeAtomically(out, func(w io.Writer) error {
		zw := zip.NewWriter(w)
		err := walkTree(dir, prefix, func(name, full string, info fs.FileInfo) error {
			if info.IsDir() {
				hdr := &zip.FileHeader{Name: name + "/", Method: zip.Store, Modified: info.ModTime()}
				hdr.SetMode(fs.ModeDir | 0755)
				_, err := zw.CreateHeader(hdr)
				return err
			}
			hdr := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: info.ModTime()}
			hdr.SetMode(archiveMode(name, prefix, exeName))
			entry, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			return copyInto(entry, full)
		})
		if err != nil {
			return err
		}
		return zw.Close()
	})
}

// archiveMode is the permission bits an archived file gets.
//
// Set here rather than read off the disk, because the tree may have been staged
// on NTFS — a Windows editor packing a Linux release — where there is no
// executable bit to read, and a tarball whose game will not start without a
// chmod is a broken release. So the executable and every shell script are
// executable, and nothing else is.
func archiveMode(name, prefix, exeName string) fs.FileMode {
	rel := strings.TrimPrefix(name, prefix+"/")
	if rel == exeName || strings.HasSuffix(rel, ".sh") {
		return 0755
	}
	return 0644
}

// walkTree visits the prefix directory and then everything under dir, in
// lexical order, handing fn each entry's slash-separated archive name and its
// path on disk. Dotfiles are included — the .foundrypack stamp is what
// install.sh reads.
func walkTree(dir, prefix string, fn func(name, full string, info fs.FileInfo) error) error {
	return filepath.WalkDir(dir, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, full)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return nil // a pack only ever writes plain files
		}
		name := prefix
		if rel != "." {
			name = path.Join(prefix, filepath.ToSlash(rel))
		}
		return fn(name, full, info)
	})
}

func copyInto(w io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// writeAtomically writes out through a temporary file beside it, so that a
// build that fails half way through an archive leaves the last good archive in
// place rather than a truncated one with the right name.
func writeAtomically(out string, write func(io.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(out), filepath.Base(out)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once the rename succeeds
	if err := write(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), out)
}
