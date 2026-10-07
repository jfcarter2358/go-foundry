package main

// main_test.go — a whole run of the generator, timed, into a scratch
// directory (save writes under resources/textures relative to the working
// directory, so each run moves into a fresh one first).

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// inScratch runs f with the working directory a fresh one holding an empty
// resources/textures, and returns that directory.
func inScratch(tb testing.TB, f func()) string {
	tb.Helper()
	dir := tb.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "resources", "textures"), 0o755); err != nil {
		tb.Fatal(err)
	}
	prev, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		tb.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(prev); err != nil {
			tb.Fatal(err)
		}
	}()
	f()
	return dir
}

func TestSkiesAlongsideWriteWhatOneAtATimeDoes(t *testing.T) {
	prev := parallelSkies
	t.Cleanup(func() { parallelSkies = prev })
	parallelSkies = false
	serial := inScratch(t, main)
	parallelSkies = true
	side := inScratch(t, main)
	names, err := os.ReadDir(filepath.Join(serial, "resources", "textures"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadDir(filepath.Join(side, "resources", "textures")); err != nil || len(got) != len(names) {
		t.Fatalf("side by side wrote %d files (%v), one at a time %d", len(got), err, len(names))
	}
	if len(names) < 30 {
		t.Fatalf("only %d files written", len(names))
	}
	for _, n := range names {
		a, err := os.ReadFile(filepath.Join(serial, "resources", "textures", n.Name()))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(side, "resources", "textures", n.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s differs", n.Name())
		}
	}
}

func BenchmarkGenerateAll(b *testing.B) {
	for i := 0; i < b.N; i++ {
		inScratch(b, main)
	}
}
