package nav

// signature_test.go — Signature's compare-don't-hash memo against the plain
// hash it replaced: the same number for every level, after every kind of edit
// — corners moved in place, faces re-indexed in a new array, chunks switched
// off, props moved.

import (
	"math"
	"math/rand"
	"sync"
	"testing"

	"foundry/internal/meshcollide"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestSignatureMatchesThePlainHashThroughEdits(t *testing.T) {
	lvl := bigLevel()
	lvl.Chunks = append(lvl.Chunks, roomLevel().Chunks[0])
	pos, idx := navBox(mgl32.Vec3{-1, 0, -1}, mgl32.Vec3{1, 2, 1})
	lvl.Scenery = []world.Scenery{navCooked(t, "rock", mgl32.Vec3{15, 0, 15}, pos, idx, meshcollide.ModeHulls)}
	other := roomLevel(world.NewBoxBrush("pillar", mgl32.Vec3{5, 2, 5}, mgl32.Vec3{2, 2, 2}, "c"))
	rng := rand.New(rand.NewSource(1))
	check := func(what string, l *world.Level) {
		t.Helper()
		if got, want := Signature(l), signatureReference(l); got != want {
			t.Fatalf("%s: Signature %x, the plain hash %x", what, got, want)
		}
	}
	check("first", lvl)
	check("again", lvl)
	for i := 0; i < 600; i++ {
		ch := &lvl.Chunks[rng.Intn(len(lvl.Chunks))]
		b := &ch.Brushes[rng.Intn(len(ch.Brushes))]
		switch rng.Intn(11) {
		case 0: // one corner nudged in place, as the editor's drag does
			v := &b.Verts[rng.Intn(len(b.Verts))]
			v[rng.Intn(3)] += 0.25
		case 1: // a corner's bits changed but not its value: -0 for 0
			v := &b.Verts[rng.Intn(len(b.Verts))]
			if v[0] == 0 {
				v[0] = float32(math.Copysign(0, -1))
			}
		case 2: // a corner dropped, then the slice regrown in place
			b.Verts = b.Verts[:len(b.Verts)-1]
			check("dropped corner", lvl)
			b.Verts = append(b.Verts, mgl32.Vec3{1, 2, 3})
		case 3:
			b.Solid = !b.Solid
		case 4:
			b.Dynamic = !b.Dynamic
		case 5: // a face re-indexed, the way every edit that does it does:
			// in a new Faces array (Signature's invariant; see there)
			b.Faces = append([]world.BrushFace(nil), b.Faces...)
			f := &b.Faces[rng.Intn(len(b.Faces))]
			f.Indices = append(append([]int(nil), f.Indices...), 0)
		case 6:
			ch.Disabled = !ch.Disabled
		case 7:
			ch.Origin = ch.Origin.Add(mgl32.Vec3{0, 0.5, 0})
		case 8:
			lvl.Scenery[0].Position = lvl.Scenery[0].Position.Add(mgl32.Vec3{0.5, 0, 0})
		case 9: // another level in between, sharing nothing
			check("other level", other)
		case 10: // rebuilt in place, which is not an edit
			if err := b.Load(); err != nil {
				t.Fatal(err)
			}
		}
		check("after an edit", lvl)
		check("unchanged since", lvl)
	}
	// A chunk's ID bytes carry no length: renaming chunks shifts words across
	// the boundary the old stream had, which must still be a full compare.
	lvl.Chunks[0].ID, lvl.Chunks[1].ID = lvl.Chunks[1].ID+"x", lvl.Chunks[0].ID
	check("chunks renamed", lvl)
	lvl.Chunks = lvl.Chunks[:1]
	check("a chunk gone", lvl)
	lvl.Scenery = nil
	check("no scenery", lvl)
	lvl.Chunks = nil
	check("empty", lvl)
}

// Signature is exported and pure to its callers; two goroutines asking at once
// must each get the right answer for their own level. Run under -race.
func TestSignatureConcurrentCallers(t *testing.T) {
	a, b := bigLevel(), roomLevel()
	wa, wb := signatureReference(a), signatureReference(b)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if g%2 == 0 && Signature(a) != wa || g%2 == 1 && Signature(b) != wb {
					t.Error("a concurrent Signature answered for the wrong level")
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

// signatureReference is Signature as it was before it memoised: FNV-1a a word
// at a time over the level, hashed afresh every call.
func signatureReference(lvl *world.Level) uint64 {
	const prime = 1099511628211
	h := uint64(14695981039346656037)
	w := func(v uint32) {
		h ^= uint64(v)
		h *= prime
	}
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !navigable(lvl, ch) {
			continue
		}
		for i := 0; i < len(ch.ID); i++ {
			h ^= uint64(ch.ID[i])
			h *= prime
		}
		m := ch.ModelMatrix()
		for _, v := range m {
			w(math.Float32bits(v))
		}
		w(uint32(len(ch.Brushes)))
		for bi := range ch.Brushes {
			b := &ch.Brushes[bi]
			if !b.Solid || b.Dynamic {
				w(0xffffffff)
				continue
			}
			w(uint32(len(b.Verts)))
			for _, v := range b.Verts {
				w(math.Float32bits(v.X()))
				w(math.Float32bits(v.Y()))
				w(math.Float32bits(v.Z()))
			}
			w(uint32(len(b.Faces)))
			for fi := range b.Faces {
				w(uint32(len(b.Faces[fi].Indices)))
			}
		}
		if t := ch.Terrain; t != nil {
			w(uint32(t.Generation))
			w(uint32(t.Generation >> 32))
		}
	}
	for si := range lvl.Scenery {
		sc := &lvl.Scenery[si]
		mode := sc.CollisionMode()
		if mode != meshcollide.ModeHulls && mode != meshcollide.ModeMesh {
			continue
		}
		w(uint32(si))
		w(uint32(mode))
		d := sc.CollisionData()
		w(uint32(d.TriangleCount()))
		w(uint32(len(d.Hulls)))
		for _, v := range [6]float32{d.Lo.X(), d.Lo.Y(), d.Lo.Z(), d.Hi.X(), d.Hi.Y(), d.Hi.Z()} {
			w(math.Float32bits(v))
		}
		sx := sc.EffectiveScale()
		for _, v := range [9]float32{
			sc.Position.X(), sc.Position.Y(), sc.Position.Z(),
			sc.Rotation.X(), sc.Rotation.Y(), sc.Rotation.Z(),
			sx.X(), sx.Y(), sx.Z(),
		} {
			w(math.Float32bits(v))
		}
	}
	return h
}
