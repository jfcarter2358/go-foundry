// Package setup is Foundry's Windows installer: the format of a Setup.exe, and
// installing and uninstalling one.
//
// A Setup.exe used to be NSIS's to make, which meant makensis had to be
// installed wherever a release was built — and an editor running on Windows,
// where it almost never is, built releases with no installer in them. So the
// installer is a program of Foundry's own (cmd/foundrysetup) with the install
// tree appended to it: the program, then a zip of the tree, then a fixed-size
// trailer saying where the zip starts. Making one is concatenation, which any
// machine can do with nothing installed, and the program is pure Go, so it
// cross-compiles from anywhere without a C compiler.
//
// The same program with nothing appended is the uninstaller. Install writes a
// copy of itself, minus the payload, into the install tree; run with nothing to
// install, it uninstalls.
package setup

// payload.go — what a Setup.exe is made of.

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// trailerMagic ends every installer, followed by the zip's offset and size.
const trailerMagic = "FOUNDRY-SETUP-01"

// trailerSize is the magic plus two little-endian uint64s.
const trailerSize = len(trailerMagic) + 16

// ErrNoPayload is a program with no install tree appended — the uninstaller.
var ErrNoPayload = errors.New("no install payload")

// Write makes an installer at out: the program at stub, followed by the tree at
// dir.
func Write(stub, dir, out string) error {
	return writeAtomically(out, func(f *os.File) error {
		in, err := os.Open(stub)
		if err != nil {
			return err
		}
		start, err := io.Copy(f, in)
		in.Close()
		if err != nil {
			return err
		}
		cw := &countingWriter{w: f}
		if err := zipTree(cw, dir); err != nil {
			return err
		}
		return writeTrailer(f, start, cw.n)
	})
}

// zipTree writes every file under dir as a zip entry named by its slash path
// under dir. Directories are not entries: Extract makes each file's parents.
func zipTree(w io.Writer, dir string) error {
	zw := zip.NewWriter(w)
	err := filepath.WalkDir(dir, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, full)
		if err != nil {
			return err
		}
		hdr := &zip.FileHeader{Name: filepath.ToSlash(rel), Method: zip.Deflate, Modified: info.ModTime()}
		hdr.SetMode(0644)
		entry, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		src, err := os.Open(full)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(entry, src)
		return err
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

func writeTrailer(w io.Writer, start, size int64) error {
	var t [trailerSize]byte
	copy(t[:], trailerMagic)
	binary.LittleEndian.PutUint64(t[len(trailerMagic):], uint64(start))
	binary.LittleEndian.PutUint64(t[len(trailerMagic)+8:], uint64(size))
	_, err := w.Write(t[:])
	return err
}

// Installer is an opened Setup.exe: the tree it installs, and the program it
// was appended to.
type Installer struct {
	Files *zip.Reader
	// StubSize is how much of the file is the program, before the payload.
	StubSize int64
	f        *os.File
}

// Open reads the payload out of an installer. A program with none appended
// returns ErrNoPayload.
func Open(path string) (*Installer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	in, err := open(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return in, nil
}

func open(f *os.File) (*Installer, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	if size < int64(trailerSize) {
		return nil, ErrNoPayload
	}
	var t [trailerSize]byte
	if _, err := f.ReadAt(t[:], size-int64(trailerSize)); err != nil {
		return nil, err
	}
	if string(t[:len(trailerMagic)]) != trailerMagic {
		return nil, ErrNoPayload
	}
	start := int64(binary.LittleEndian.Uint64(t[len(trailerMagic):]))
	n := int64(binary.LittleEndian.Uint64(t[len(trailerMagic)+8:]))
	if start < 0 || n < 0 || start > size || n > size-int64(trailerSize)-start {
		return nil, fmt.Errorf("%s: the installer is damaged (its payload runs past the end of the file)", f.Name())
	}
	zr, err := zip.NewReader(io.NewSectionReader(f, start, n), n)
	if err != nil {
		return nil, fmt.Errorf("%s: the installer is damaged: %w", f.Name(), err)
	}
	return &Installer{Files: zr, StubSize: start, f: f}, nil
}

// Close releases the installer's file.
func (in *Installer) Close() error { return in.f.Close() }

// Stamp is the install's .foundrypack, read out of the payload.
func (in *Installer) Stamp() (Stamp, error) {
	for _, zf := range in.Files.File {
		if zf.Name != StampName {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return Stamp{}, err
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			return Stamp{}, err
		}
		return ParseStamp(data)
	}
	return Stamp{}, fmt.Errorf("the installer carries no %s, so it does not say what it installs", StampName)
}

// Extract writes every file in the payload under dir, and returns how many
// bytes that came to.
func (in *Installer) Extract(dir string) (int64, error) {
	return in.ExtractMatching(dir, nil)
}

// ExtractMatching writes the files in the payload whose slash-separated names
// keep accepts — all of them when keep is nil — under dir, and returns how many
// bytes that came to.
func (in *Installer) ExtractMatching(dir string, keep func(name string) bool) (int64, error) {
	var total int64
	for _, zf := range in.Files.File {
		if keep != nil && !keep(zf.Name) {
			continue
		}
		target, err := within(dir, zf.Name)
		if err != nil {
			return total, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return total, err
		}
		n, err := extractOne(zf, target)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func extractOne(zf *zip.File, target string) (int64, error) {
	rc, err := zf.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, rc)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return n, err
}

// within is where a payload entry goes under dir, refusing any name that would
// put it anywhere else. A payload is only ever written by Write, but an
// installer is a file a player downloaded, and one that could write outside its
// own folder is not a risk worth the few lines this takes.
func within(dir, name string) (string, error) {
	if !fs.ValidPath(name) || strings.ContainsAny(name, `\:`) {
		return "", fmt.Errorf("the installer is damaged: it holds a file named %q", name)
	}
	return filepath.Join(dir, filepath.FromSlash(name)), nil
}

// Size is how many bytes the payload's files come to, unpacked.
func (in *Installer) Size() int64 {
	var n int64
	for _, zf := range in.Files.File {
		n += int64(zf.UncompressedSize64)
	}
	return n
}

// WriteUninstaller copies the program, without its payload, to dest.
func (in *Installer) WriteUninstaller(dest string) error {
	return writeAtomically(dest, func(f *os.File) error {
		_, err := io.Copy(f, io.NewSectionReader(in.f, 0, in.StubSize))
		return err
	})
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// writeAtomically writes out through a temporary file beside it, so a failure
// part way leaves nothing with the right name.
func writeAtomically(out string, write func(*os.File) error) error {
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
	if err := os.Chmod(tmp.Name(), 0755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), out)
}
