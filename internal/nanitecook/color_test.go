package nanitecook

// color_test.go — vertex colours through the cook (docs/canyon/PHASE_2.md):
// a coloured mesh cooks to format 5 with every pool and fallback vertex
// keeping its source colour, the pages decode them bit for bit (from memory
// and from the file), the DAG's coarse levels only ever use colours the
// source had, and a mesh without colours still cooks to format 4 with no
// colour anywhere.

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/model"
	"foundry/internal/nanite"

	"github.com/go-gl/mathgl/mgl32"
)

// coloredRock is the sample rock painted by position: red rising with x, the
// cavity (alpha) with height, so any vertex's colour is a function of where
// it is.
func coloredRock(level int) *model.ImportMesh {
	m := SampleRock(level, 0.3)
	m.Colors = make([][4]uint8, len(m.Positions))
	for i, p := range m.Positions {
		m.Colors[i] = rockColorAt(p)
	}
	return m
}

func rockColorAt(p mgl32.Vec3) [4]uint8 {
	b := func(v float32) uint8 { return uint8(math.Round(float64(min(max(v, 0), 1) * 255))) }
	return [4]uint8{b(0.5 + 0.4*p[0]), 90, b(0.3 + 0.2*p[2]), b(0.5 + 0.45*p[1])}
}

func unpackColor(w uint32) [4]uint8 {
	return [4]uint8{uint8(w), uint8(w >> 8), uint8(w >> 16), uint8(w >> 24)}
}

func TestColoredMeshCooksWithItsColours(t *testing.T) {
	src := coloredRock(4)
	// The colour each source position had (positions are distinct on the
	// rock, bar the UV seam's duplicates, which share a colour).
	byPos := map[[3]float32][4]uint8{}
	for i, p := range src.Positions {
		byPos[[3]float32(p)] = src.Colors[i]
	}
	m, err := CookMesh(src, "rock.glb", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !m.HasColors || m.Version() != nanite.FileVersion {
		t.Fatalf("HasColors %v, version %d", m.HasColors, m.Version())
	}
	if len(m.Colors) != len(m.Vertices)/5 || len(m.FallbackColors) != len(m.FallbackVertices)/5 {
		t.Fatalf("%d colours for %d vertices, %d fallback colours for %d", len(m.Colors), len(m.Vertices)/5,
			len(m.FallbackColors), len(m.FallbackVertices)/5)
	}
	// Quantisation moves a position by under a step: find the source vertex
	// nearest each pooled one and compare colours.
	near := func(p mgl32.Vec3) [4]uint8 {
		best, bd := [4]uint8{}, float32(math.Inf(1))
		for q, c := range byPos {
			if d := mgl32.Vec3(q).Sub(p).LenSqr(); d < bd {
				best, bd = c, d
			}
		}
		return best
	}
	for v := 0; v < len(m.Colors); v += 37 {
		p := mgl32.Vec3{m.Vertices[5*v], m.Vertices[5*v+1], m.Vertices[5*v+2]}
		if got, want := unpackColor(m.Colors[v]), near(p); got != want {
			t.Fatalf("pool vertex %d at %v: colour %v, want %v", v, p, got, want)
		}
	}
	for v := 0; v < len(m.FallbackColors); v += 11 {
		fv := m.FallbackVertices
		p := mgl32.Vec3{fv[5*v], fv[5*v+1], fv[5*v+2]}
		if got, want := unpackColor(m.FallbackColors[v]), near(p); got != want {
			t.Fatalf("fallback vertex %d at %v: colour %v, want %v", v, p, got, want)
		}
	}

	// Through the file: the pages decode the same words.
	var buf bytes.Buffer
	if err := m.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if v := le32(buf.Bytes()[4:8]); v != nanite.FileVersion {
		t.Fatalf("written as format %d", v)
	}
	path := filepath.Join(t.TempDir(), "rock.fnm")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := nanite.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !r.HasColors || len(r.FallbackColors) != len(m.FallbackColors) {
		t.Fatal("the file lost the colours")
	}
	if err := r.Flat(); err != nil {
		t.Fatal(err)
	}
	if len(r.Colors) != len(m.Colors) {
		t.Fatalf("read back %d colours, cooked %d", len(r.Colors), len(m.Colors))
	}
	for i := range r.Colors {
		if r.Colors[i] != m.Colors[i] {
			t.Fatalf("colour %d reads %08x, cooked %08x", i, r.Colors[i], m.Colors[i])
		}
	}
	d, err := r.LoadPage(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Colors) != len(d.Vertices)/5 {
		t.Fatalf("page 0 decodes %d colours for %d vertices", len(d.Colors), len(d.Vertices)/5)
	}

	// The coarse levels index the same pool, so they only use colours the
	// source had.
	have := map[uint32]bool{}
	for _, c := range src.Colors {
		have[uint32(c[0])|uint32(c[1])<<8|uint32(c[2])<<16|uint32(c[3])<<24] = true
	}
	for ci := range m.Clusters {
		c := &m.Clusters[ci]
		if c.Level != m.Levels-1 {
			continue
		}
		for _, ix := range m.Indices[3*c.TriFirst : 3*(c.TriFirst+c.TriCount)] {
			if !have[m.Colors[ix]] {
				t.Fatalf("a root cluster uses colour %08x, which no source vertex had", m.Colors[ix])
			}
		}
	}
}

func TestPlainMeshCooksToFormat4(t *testing.T) {
	m, err := CookMesh(SampleRock(3, 0.3), "rock.glb", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if m.HasColors || m.Colors != nil || m.FallbackColors != nil || m.Version() != nanite.FileVersionPlain {
		t.Fatalf("a mesh without colours cooked with them (version %d)", m.Version())
	}
	var buf bytes.Buffer
	if err := m.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if v := le32(buf.Bytes()[4:8]); v != nanite.FileVersionPlain {
		t.Fatalf("written as format %d, want %d", v, nanite.FileVersionPlain)
	}
	r, err := nanite.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	d, err := r.LoadPage(0)
	if err != nil {
		t.Fatal(err)
	}
	if d.Colors != nil {
		t.Fatal("a format-4 page decoded colours")
	}
}

func TestColoredCookFileSkipsAndUpgrades(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "rock.fnm")
	src := coloredRock(3)
	res, err := CookMeshFile(src, out, false, Options{})
	if err != nil || res.Skipped {
		t.Fatalf("first cook: %v (skipped %v)", err, res.Skipped)
	}
	if res, err = CookMeshFile(src, out, false, Options{}); err != nil || !res.Skipped {
		t.Fatalf("an unchanged coloured mesh recooked (err %v)", err)
	}
	// A glTF with COLOR_0 whose cook is format 4 (made before colours were
	// read) is stale even though its hash matches.
	glb, err := model.EncodeGLB(src)
	if err != nil {
		t.Fatal(err)
	}
	gpath := filepath.Join(dir, "rock.glb")
	if err := os.WriteFile(gpath, glb, 0o644); err != nil {
		t.Fatal(err)
	}
	fres, err := CookFile(gpath, "", false, Options{})
	if err != nil || fres.Skipped {
		t.Fatalf("cook the glTF: %v", err)
	}
	if fresh, err := UpToDateWith(gpath, fres.Out, Options{}); err != nil || !fresh {
		t.Fatalf("the coloured glTF's cook is not up to date (%v)", err)
	}
	data, err := os.ReadFile(fres.Out)
	if err != nil {
		t.Fatal(err)
	}
	data[4] = nanite.FileVersionPlain
	if err := os.WriteFile(fres.Out, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if fresh, _ := UpToDateWith(gpath, fres.Out, Options{}); fresh {
		t.Fatal("a format-4 cook of a coloured glTF counted as up to date")
	}
}

func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
