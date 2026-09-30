package world

// decal_test.go — the parts of a decal that are invisible when they go wrong.
//
// A decal has an unusual failure mode: every way of getting it wrong produces
// either NOTHING on screen or something that looks like a rendering bug rather
// than an authoring mistake. Facing the wrong way draws nothing. A depth too
// large paints the far side of the wall as well, which reads as z-fighting. A
// max angle too high smears the texture into streaks, which reads as a broken
// shader. So the things worth pinning down here are the geometric invariants —
// which way it points, what it catches, what it refuses — rather than the
// serialization, which fails loudly.

import (
	"reflect"
	"testing"

	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// decalTestLevel is a level with one chunk holding one wall brush: a slab
// spanning x and y at z = 0, one unit thick, whose +Z face is the surface a
// decal is meant to land on.
func decalTestLevel(t *testing.T) *Level {
	t.Helper()
	gfx.GeometryUploadsEnabled = false
	t.Cleanup(func() { gfx.GeometryUploadsEnabled = true })

	b := NewBoxBrush("wall", mgl32.Vec3{0, 0, -0.5}, mgl32.Vec3{4, 4, 0.5}, "wall")
	if err := b.Load(); err != nil {
		t.Fatalf("brush load: %v", err)
	}
	ch := Chunk{ID: "room", Brushes: []Brush{b}}
	ch.RecomputeBounds()
	return &Level{Chunks: []Chunk{ch}}
}

// wallDecal is a decal facing +Z — out of the wall, back at where a viewer would
// be — centred on the wall's face.
func wallDecal() Decal {
	d := NewDecal("mark", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 0, 1})
	d.Width, d.Height, d.Depth = 2, 2, 1
	return d
}

func decalTriCount(d *Decal) int {
	n := 0
	for i := range d.Pieces {
		n += len(d.Pieces[i].Geometry.Vertices) / 5 / 3
	}
	return n
}

// The whole premise: a decal aimed at a surface gets a mesh out of that
// surface's own triangles, in that surface's chunk.
func TestBuildDecalClipsTheSurfaceItFaces(t *testing.T) {
	lvl := decalTestLevel(t)
	d := wallDecal()
	BuildDecal(lvl, &d)

	if len(d.Pieces) != 1 {
		t.Fatalf("pieces: got %d, want 1", len(d.Pieces))
	}
	if d.Pieces[0].Chunk != "room" {
		t.Errorf("piece chunk: got %q, want %q", d.Pieces[0].Chunk, "room")
	}
	if d.Pieces[0].Brush != "" {
		t.Errorf("a static brush should share its chunk's piece, got brush %q", d.Pieces[0].Brush)
	}
	if decalTriCount(&d) == 0 {
		t.Fatal("no triangles came out of a decal aimed squarely at a wall")
	}
}

// Facing is the one field with no visible symptom when it is wrong: a decal
// pointing INTO its wall projects onto the back of the wall's near face, which
// is culled away from the eye, and the author sees nothing at all.
func TestBuildDecalIgnoresSurfacesFacingAway(t *testing.T) {
	lvl := decalTestLevel(t)
	d := wallDecal()
	d.Rotation = DecalRotationFor(mgl32.Vec3{0, 0, -1}, 0) // into the wall

	BuildDecal(lvl, &d)
	if decalTriCount(&d) != 0 {
		t.Fatalf("a decal facing away from its wall caught %d triangles", decalTriCount(&d))
	}
}

// The clip has to bound the mesh to the box. Without it a decal would take the
// whole of every triangle it touched, so a 2-unit mark on an 8-unit wall would
// paint the entire wall.
func TestBuildDecalClipsToTheBox(t *testing.T) {
	lvl := decalTestLevel(t)
	d := wallDecal()
	BuildDecal(lvl, &d)

	half := mgl32.Vec2{d.Width * 0.5, d.Height * 0.5}
	const eps = 1e-4
	for pi := range d.Pieces {
		v := d.Pieces[pi].Geometry.Vertices
		for i := 0; i+4 < len(v); i += 5 {
			if x := v[i]; x < -half.X()-eps || x > half.X()+eps {
				t.Fatalf("vertex x %v is outside the box's %v", x, half.X())
			}
			if y := v[i+1]; y < -half.Y()-eps || y > half.Y()+eps {
				t.Fatalf("vertex y %v is outside the box's %v", y, half.Y())
			}
		}
	}
}

// The UVs are the projection, and getting them upside down or mirrored is the
// mistake that ships: a bullet hole survives it and a stencilled "BAY 4" does
// not. The decal's local +X is u=1 and its local +Y is v=0 — V runs DOWN the
// image, matching the rest of the engine.
func TestBuildDecalProjectsUVsFromTheDecalsOwnAxes(t *testing.T) {
	lvl := decalTestLevel(t)
	d := wallDecal()
	BuildDecal(lvl, &d)

	// The corner of the box at local (+X, +Y) — the top right of the image.
	var found bool
	for pi := range d.Pieces {
		v := d.Pieces[pi].Geometry.Vertices
		for i := 0; i+4 < len(v); i += 5 {
			if decNear(v[i], 1) && decNear(v[i+1], 1) {
				found = true
				if !decNear(v[i+3], 1) {
					t.Errorf("u at the +X corner: got %v, want 1", v[i+3])
				}
				if !decNear(v[i+4], 0) {
					t.Errorf("v at the +Y corner: got %v, want 0 (V runs down the image)", v[i+4])
				}
			}
		}
	}
	if !found {
		t.Fatal("the box's +X/+Y corner is not in the mesh at all")
	}
}

// Depth is what stops a decal on a wall from also printing itself on the far
// side of that wall, and it is the field an author is most likely to overreach
// with.
func TestBuildDecalDepthBoundsWhatItReaches(t *testing.T) {
	lvl := decalTestLevel(t)
	// A second wall a metre behind the first, facing the same way.
	b := NewBoxBrush("back", mgl32.Vec3{0, 0, -2.5}, mgl32.Vec3{4, 4, 0.5}, "wall")
	if err := b.Load(); err != nil {
		t.Fatalf("brush load: %v", err)
	}
	lvl.Chunks[0].Brushes = append(lvl.Chunks[0].Brushes, b)
	lvl.Chunks[0].RecomputeBounds()

	shallow := wallDecal()
	shallow.Depth = 0.5
	BuildDecal(lvl, &shallow)
	shallowTris := decalTriCount(&shallow)

	deep := wallDecal()
	deep.Depth = 8
	BuildDecal(lvl, &deep)

	if shallowTris == 0 {
		t.Fatal("the shallow decal caught nothing at all")
	}
	if decalTriCount(&deep) <= shallowTris {
		t.Fatalf("a decal deep enough to reach the second wall caught %d triangles, "+
			"no more than the shallow one's %d — the depth is not bounding the projection",
			decalTriCount(&deep), shallowTris)
	}
}

// A dynamic brush moves relative to its chunk, so its piece of a decal has to be
// in the BRUSH's space with the brush's own matrix, or a bullet hole in a door
// stays behind when the door opens.
func TestBuildDecalGivesADynamicBrushItsOwnPiece(t *testing.T) {
	lvl := decalTestLevel(t)
	lvl.Chunks[0].Brushes[0].Dynamic = true

	d := wallDecal()
	BuildDecal(lvl, &d)
	if len(d.Pieces) != 1 {
		t.Fatalf("pieces: got %d, want 1", len(d.Pieces))
	}
	if d.Pieces[0].Brush != "wall" {
		t.Fatalf("a dynamic brush's piece should name it, got %q", d.Pieces[0].Brush)
	}

	// And the matrix the piece draws under follows the brush when a script moves
	// it, which is the whole point of keeping them apart.
	lvl.Chunks[0].Brushes[0].Offset = mgl32.Vec3{3, 0, 0}
	m, ok := lvl.DecalPieceMatrix(&d.Pieces[0])
	if !ok {
		t.Fatal("the piece's source could not be resolved")
	}
	moved := m.Mul4x1(mgl32.Vec4{0, 0, 0, 1})
	if !decNear(moved.X(), 3) {
		t.Errorf("the piece's matrix did not follow the brush: x = %v, want 3", moved.X())
	}
}

// A decal on a chunk that has been switched off must go with it, or a streamed
// level shows marks floating where a district used to be.
func TestDecalPieceIsSkippedWhileItsChunkIsOff(t *testing.T) {
	lvl := decalTestLevel(t)
	d := wallDecal()
	BuildDecal(lvl, &d)

	if _, ok := lvl.DecalPieceMatrix(&d.Pieces[0]); !ok {
		t.Fatal("the piece should draw while its chunk is on")
	}
	lvl.Chunks[0].Disabled = true
	if _, ok := lvl.DecalPieceMatrix(&d.Pieces[0]); ok {
		t.Fatal("the piece still draws with its chunk switched off")
	}
}

// The rebuild is driven entirely by a fingerprint, so the two halves of that
// bargain both have to hold: a field that moves a vertex forces a rebuild, and
// one that does not, does not. The second half is the one that costs
// performance when it breaks, and it breaks silently.
func TestDecalRebuildsOnlyForGeometryChanges(t *testing.T) {
	lvl := decalTestLevel(t)
	d := wallDecal()
	BuildDecal(lvl, &d)
	before := d.built

	d.Alpha, d.Tint, d.Texture = 0.5, mgl32.Vec3{1, 0, 0}, "other"
	d.Visible, d.Order = false, 3
	if got := decalFingerprint(&d); got != before {
		t.Error("changing a decal's colour, texture or visibility re-clipped its mesh")
	}

	d.Position = d.Position.Add(mgl32.Vec3{0.5, 0, 0})
	if decalFingerprint(&d) == before {
		t.Error("moving a decal did not mark its mesh stale")
	}
}

// A decal that finds nothing must still count as built, or SyncDecals re-clips
// the whole level for it on every frame for ever.
func TestBuildDecalInMidAirIsStillBuilt(t *testing.T) {
	lvl := decalTestLevel(t)
	d := wallDecal()
	d.Position = mgl32.Vec3{0, 0, 100}
	BuildDecal(lvl, &d)

	if len(d.Pieces) != 0 {
		t.Fatalf("a decal in mid-air caught %d pieces", len(d.Pieces))
	}
	SyncDecals(lvl, 0)
	if !d.builtOK {
		t.Error("a decal that found no geometry is not marked built, so it will rebuild every frame")
	}
}

// The transient cap is what keeps a firefight from growing the level without
// bound. Authored decals must never be candidates, however many there are.
func TestCapTransientDecalsDropsTheOldestAndSparesAuthoredOnes(t *testing.T) {
	lvl := &Level{}
	lvl.Decals = append(lvl.Decals, Decal{ID: "sign"}) // authored: never dropped
	for i := range MaxTransientDecals + 10 {
		lvl.Decals = append(lvl.Decals, Decal{ID: "hole" + itoa(i), Transient: true})
	}

	if !CapTransientDecals(lvl) {
		t.Fatal("the cap reported no change with the list over the limit")
	}
	transient := 0
	for i := range lvl.Decals {
		if lvl.Decals[i].Transient {
			transient++
		}
	}
	if transient != MaxTransientDecals {
		t.Errorf("transient decals after the cap: got %d, want %d", transient, MaxTransientDecals)
	}
	if FindDecal(lvl, "sign") == nil {
		t.Error("the cap dropped an authored decal")
	}
	// The OLDEST go: the first transient one placed is the first to be recycled.
	if FindDecal(lvl, "hole0") != nil {
		t.Error("the cap kept the oldest transient decal and dropped a newer one")
	}
	if FindDecal(lvl, "hole"+itoa(MaxTransientDecals+9)) == nil {
		t.Error("the cap dropped the newest transient decal")
	}
}

// A lifetime fades the mark out rather than blinking it off, and the fade must
// not destroy the authored alpha — a script reading it back gets what it set.
func TestDecalLifetimeFadesWithoutWritingAlpha(t *testing.T) {
	d := wallDecal()
	d.Alpha = 0.8

	d.Lifetime = 10
	if got := d.DrawAlpha(); !decNear(got, 0.8) {
		t.Errorf("a decal with plenty of life left draws at %v, want its own 0.8", got)
	}
	d.Lifetime = DecalFadeTime / 2
	if got := d.DrawAlpha(); !decNear(got, 0.4) {
		t.Errorf("halfway through the fade: got %v, want 0.4", got)
	}
	if !decNear(d.Alpha, 0.8) {
		t.Errorf("the fade overwrote the authored alpha: %v", d.Alpha)
	}
}

// StepDecals reaps the expired and leaves everything else alone.
func TestStepDecalsReapsExpired(t *testing.T) {
	lvl := &Level{Decals: []Decal{
		{ID: "forever"},
		{ID: "brief", Lifetime: 0.5},
	}}
	if changed := StepDecals(lvl, 0.25); changed {
		t.Error("a decal with life left was reaped")
	}
	if !StepDecals(lvl, 0.5) {
		t.Error("an expired decal was not reaped")
	}
	if len(lvl.Decals) != 1 || lvl.Decals[0].ID != "forever" {
		t.Errorf("after the reap: %v", lvl.Decals)
	}
}

// DecalRotationFor is the one conversion every placement path shares, so its
// convention has to hold for the three cases an author actually meets.
func TestDecalRotationForPointsTheFacingAtTheNormal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		normal mgl32.Vec3
	}{
		{"a wall facing +Z", mgl32.Vec3{0, 0, 1}},
		{"a wall facing -X", mgl32.Vec3{-1, 0, 0}},
		{"a floor facing up", mgl32.Vec3{0, 1, 0}},
		{"a ceiling facing down", mgl32.Vec3{0, -1, 0}},
		{"a slope", mgl32.Vec3{1, 1, 1}},
	} {
		d := Decal{Rotation: DecalRotationFor(tc.normal, 0)}
		want := tc.normal.Normalize()
		if got := d.Facing(); !vecNear(got, want, 1e-4) {
			t.Errorf("%s: facing %v, want %v", tc.name, got, want)
		}
	}
}

// The roll spins the image without moving the facing, which is what lets ten
// bullet holes in one plank look like ten holes.
func TestDecalRotationForRollKeepsTheFacing(t *testing.T) {
	n := mgl32.Vec3{0, 0, 1}
	rolled := Decal{Rotation: DecalRotationFor(n, 1.2)}
	if got := rolled.Facing(); !vecNear(got, n, 1e-4) {
		t.Errorf("a rolled decal faces %v, want %v", got, n)
	}
	plain := Decal{Rotation: DecalRotationFor(n, 0)}
	u0, _, _ := plain.Basis()
	u1, _, _ := rolled.Basis()
	if vecNear(u0, u1, 1e-3) {
		t.Error("the roll did not turn the image's U axis")
	}
}

// SurfaceNormalNear is what turns an impact point into an orientation, and it is
// the whole reason `decal "hole" point` can be the entire call.
func TestSurfaceNormalNearFindsTheWallItIsAgainst(t *testing.T) {
	lvl := decalTestLevel(t)
	n, ok := SurfaceNormalNear(lvl, mgl32.Vec3{0, 0, 0.01}, 0.5)
	if !ok {
		t.Fatal("no surface found just in front of a wall")
	}
	if !vecNear(n, mgl32.Vec3{0, 0, 1}, 1e-3) {
		t.Errorf("normal: got %v, want the wall's +Z", n)
	}
	if _, ok := SurfaceNormalNear(lvl, mgl32.Vec3{0, 0, 50}, 0.5); ok {
		t.Error("a surface was found in open air")
	}
}

// ---------- serialization ----------

// The YAML contract: a decal nobody has touched beyond placing it writes only
// what it must. Decals are the object a level gets hundreds of, so this is the
// difference between a readable level file and an unreadable one.
func TestDecalToRawOmitsEveryDefault(t *testing.T) {
	d := NewDecal("mark", mgl32.Vec3{1, 2, 3}, mgl32.Vec3{0, 0, 1})
	r := DecalToRaw(&d)

	if r.Width != nil || r.Height != nil || r.Depth != nil {
		t.Error("a default-sized decal wrote its size")
	}
	if r.Alpha != nil || r.Tint != nil || r.Visible != nil {
		t.Error("an untinted, opaque, visible decal wrote those")
	}
	if r.MaxAngle != nil || r.Push != nil {
		t.Error("a decal with default projection settings wrote them")
	}
	if r.Blend != "" {
		t.Errorf("an alpha-blended decal wrote its blend as %q", r.Blend)
	}
}

// And the round trip preserves what an author did change.
func TestDecalRoundTripsThroughRaw(t *testing.T) {
	d := NewDecal("scorch", mgl32.Vec3{1, 2, 3}, mgl32.Vec3{0, 1, 0})
	d.Width, d.Height, d.Depth = 1.5, 0.75, 0.3
	d.Texture, d.Material = "burn", "sooty"
	d.Blend = gfx.DecalBlendMultiply
	d.Tint = mgl32.Vec3{0.4, 0.4, 0.5}
	d.Alpha, d.MaxAngle, d.Push, d.Order = 0.6, 50, 0.01, 2
	d.Unlit, d.Visible = true, false

	back, err := DecalFromRaw(DecalToRaw(&d))
	if err != nil {
		t.Fatalf("from raw: %v", err)
	}
	// Runtime-only state never crosses the file, so compare what is authored.
	d.Pieces, d.built, d.builtOK = nil, 0, false
	if !reflect.DeepEqual(back, d) {
		t.Errorf("round trip changed the decal:\n got %+v\nwant %+v", back, d)
	}
}

// A decal whose texture went missing must still open the level.
func TestDecalFromRawFallsBackToATexture(t *testing.T) {
	back, err := DecalFromRaw(RawDecal{ID: "mark"})
	if err != nil {
		t.Fatalf("from raw: %v", err)
	}
	if back.Texture != DefaultDecalTexture {
		t.Errorf("a decal naming no texture got %q, want %q", back.Texture, DefaultDecalTexture)
	}
}

// ---------- small helpers ----------

// decNear is a tolerance compare for one float; the package's own `near` takes
// an explicit epsilon and vecNear a vector, and neither reads well inline here.
func decNear(a, b float32) bool {
	d := a - b
	return d < 1e-4 && d > -1e-4
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [12]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[n:])
}

// A mark on the ground lands on terrain the way it lands on a floor brush: on
// the chunk's own piece, clipped from the faces under it and none of the rest
// — and mining under it re-clips it.
func TestBuildDecalLandsOnTerrain(t *testing.T) {
	gfx.GeometryUploadsEnabled = false
	t.Cleanup(func() { gfx.GeometryUploadsEnabled = true })
	tr := NewTerrain(mgl32.Vec3{-20, -4, -20}, 1, 40, 8, 40)
	tr.Generate(TerrainFill{Height: 4, Top: 1, Under: 1, Deep: 1})
	ch := Chunk{ID: "ground", Origin: mgl32.Vec3{5, 1, 0}, Rotation: 0.4, Scale: 1, Terrain: tr}
	ch.RecomputeBounds()
	lvl := &Level{Chunks: []Chunk{ch}}

	c := &lvl.Chunks[0]
	at := c.ModelMatrix().Mul4x1(mgl32.Vec4{0.5, 0, 0.5, 1}).Vec3()
	d := NewDecal("scorch", at, mgl32.Vec3{0, 1, 0})
	d.Width, d.Height, d.Depth = 2, 2, 1
	BuildDecal(lvl, &d)
	if len(d.Pieces) != 1 || d.Pieces[0].Chunk != "ground" {
		t.Fatalf("pieces: %+v", d.Pieces)
	}
	n := decalTriCount(&d)
	if n == 0 {
		t.Fatal("a decal on the ground clipped nothing out of the terrain")
	}
	if n > 64 {
		t.Fatalf("a 2x2 decal took %d triangles — more than the faces under it", n)
	}
	before := decalFingerprint(&d)
	tr.SetBlock(20, 3, 20, 0)
	if decalFingerprint(&d) == before {
		t.Fatal("mining the ground under a decal did not mark it stale")
	}
}
