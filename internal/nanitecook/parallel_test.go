package nanitecook

// parallel_test.go — a cook of several slots, and `foundry nanite cook` of
// several files: how long they take, and (with the slots and files cooked
// side by side) that they cook exactly what one at a time does.

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"foundry/internal/model"
	"foundry/internal/testenv"
)

// quarteredRock is SampleRock with its triangles dealt into four slots by
// the quarter of the compass their centre lies in — four slots of about the
// same size, every one meeting two others.
func quarteredRock(level int, seed float32) *model.ImportMesh {
	m := SampleRock(level, seed)
	m.Slots = []model.ImportSlot{
		{Name: "north", BaseColor: [4]float32{0.5, 0.48, 0.45, 1}},
		{Name: "east", BaseColor: [4]float32{0.4, 0.4, 0.4, 1}},
		{Name: "south", BaseColor: [4]float32{0.3, 0.45, 0.2, 1}, DoubleSided: true},
		{Name: "west", BaseColor: [4]float32{0.6, 0.5, 0.4, 1}},
	}
	for t := range m.TriSlot {
		c := m.Positions[m.Indices[3*t]].Add(m.Positions[m.Indices[3*t+1]]).Add(m.Positions[m.Indices[3*t+2]])
		a := math.Atan2(float64(c[2]), float64(c[0])) + math.Pi // 0..2π
		m.TriSlot[t] = int32(min(3, int(a/(math.Pi/2))))
	}
	return m
}

func writeGLB(tb testing.TB, path string, m *model.ImportMesh) string {
	tb.Helper()
	glb, err := model.EncodeGLB(m)
	if err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(path, glb, 0o644); err != nil {
		tb.Fatal(err)
	}
	return path
}

func BenchmarkCookFourSlots(b *testing.B) {
	src := writeGLB(b, filepath.Join(b.TempDir(), "rock4.glb"), quarteredRock(6, 0.3)) // 81,920 triangles
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Cook(src, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

// manyRocks writes n small rocks, each its own shape, under dir/resources.
func manyRocks(tb testing.TB, dir string, n, level int) []string {
	tb.Helper()
	res := filepath.Join(dir, "resources")
	if err := os.MkdirAll(res, 0o755); err != nil {
		tb.Fatal(err)
	}
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, writeGLB(tb, filepath.Join(res, fmt.Sprintf("rock%02d.glb", i)), SampleRock(level, 0.1+0.17*float32(i))))
	}
	return out
}

func BenchmarkCookMainFiles(b *testing.B) {
	dir := b.TempDir()
	srcs := manyRocks(b, dir, 8, 5) // 20,480 triangles each
	args := append([]string{"-root", dir, "-force"}, srcs...)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if code := cookMain(dir, args, io.Discard); code != 0 {
			b.Fatalf("cook exited %d", code)
		}
	}
}

// cookBytes cooks src and returns the file it writes.
func cookBytes(t *testing.T, src string) []byte {
	t.Helper()
	m, err := Cook(src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.fnm")
	if err := m.Write(out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSlotsCookedSideBySideMatchOneAtATime(t *testing.T) {
	prev := parallelCook
	t.Cleanup(func() { parallelCook = prev })
	src := writeGLB(t, filepath.Join(t.TempDir(), "rock4.glb"), quarteredRock(4, 0.3))
	parallelCook = false
	serial := cookBytes(t, src)
	parallelCook = true
	side := cookBytes(t, src)
	if !bytes.Equal(serial, side) {
		t.Fatal("a four-slot rock cooked with its slots side by side differs from one slot at a time")
	}
}

// timings matches the seconds each cook line ends with, which differ run to
// run.
var timings = regexp.MustCompile(`\([0-9.]+s\)`)

func TestFilesCookedSideBySideMatchOneAtATime(t *testing.T) {
	prev := parallelCook
	t.Cleanup(func() { parallelCook = prev })
	dir := t.TempDir()
	srcs := manyRocks(t, dir, 6, 3)
	// The repo's own models, as a .fmdl cooks; one source named twice (two
	// cooks of one file, in order); one that is not there (a failure line,
	// in its place).
	for _, name := range []string{"dropship.fmdl", "dropship_glass.fmdl"} {
		data, err := os.ReadFile(filepath.Join(testenv.RepoRoot(t), "resources", "models", name))
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "resources", name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		srcs = append(srcs, p)
	}
	srcs = append(srcs, srcs[1], filepath.Join(dir, "resources", "missing.glb"))
	srcs[0], srcs[3] = srcs[3], srcs[0]

	run := func() (string, map[string][]byte) {
		var log bytes.Buffer
		if failed := cookAll(srcs, "", true, Options{}, &log); failed != 1 {
			t.Fatalf("%d cooks failed, want the missing one:\n%s", failed, log.String())
		}
		files := map[string][]byte{}
		for _, s := range srcs[:len(srcs)-1] {
			data, err := os.ReadFile(CookedPath(s))
			if err != nil {
				t.Fatal(err)
			}
			files[CookedPath(s)] = data
		}
		return timings.ReplaceAllString(log.String(), ""), files
	}
	parallelCook = false
	serialLog, serial := run()
	parallelCook = true
	sideLog, side := run()
	if serialLog != sideLog {
		t.Fatalf("the log differs:\none at a time:\n%s\nside by side:\n%s", serialLog, sideLog)
	}
	if strings.Count(sideLog, "\n") != len(srcs) {
		t.Fatalf("%d lines for %d sources:\n%s", strings.Count(sideLog, "\n"), len(srcs), sideLog)
	}
	for p, want := range serial {
		if !bytes.Equal(side[p], want) {
			t.Fatalf("%s cooked side by side differs from one at a time", filepath.Base(p))
		}
	}
}
