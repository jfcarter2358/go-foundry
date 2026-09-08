package levelio

// slab_save_test.go — a surface's thickness as authored data (world/slab.go).
//
// The geometry a slab compiles to is derived and tested in internal/world; what
// matters here is that the numbers and texture names survive the file, and that
// a surface with no thickness writes nothing at all — which is what keeps every
// level that predates the feature byte-identical through a save.

import (
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestSlabRoundTripsThroughTheLevelFile(t *testing.T) {
	ch := squareChunk()
	s := &ch.Sectors[0]
	s.Floor.Slab = world.Slab{
		Thickness:        0.3,
		Hidden:           true,
		Texture:          "concrete",
		BlendTexture:     "moss",
		BlendMask:        "splat",
		RimTexture:       "concrete_edge",
		RimAcrossPortals: true,
		DoubleSided:      true,
		UVOffset:         mgl32.Vec2{0.25, 0.5},
		UVScale:          mgl32.Vec2{2, 4},
		UVRotation:       1.5,
	}
	s.Ceiling.Slab = world.Slab{Thickness: 0.1, UVScale: mgl32.Vec2{1, 1}}

	rc, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatal(err)
	}
	rs := rc.Sectors[0]
	if rs.Floor.Slab == nil {
		t.Fatal("floor slab was not written")
	}
	if rs.Ceiling.Slab == nil || rs.Ceiling.Slab.Thickness != 0.1 {
		t.Fatal("ceiling slab was not written")
	}

	got := world.SlabFromRaw(rs.Floor.Slab)
	// Compared field by field rather than with ==: a Slab carries derived
	// gfx.Geometry, which is never written and never read back.
	want := s.Floor.Slab
	switch {
	case got.Thickness != want.Thickness:
		t.Errorf("thickness = %v, want %v", got.Thickness, want.Thickness)
	case got.Hidden != want.Hidden:
		t.Errorf("hidden = %v, want %v", got.Hidden, want.Hidden)
	case got.Texture != want.Texture || got.BlendTexture != want.BlendTexture ||
		got.BlendMask != want.BlendMask || got.RimTexture != want.RimTexture:
		t.Errorf("textures = %q/%q/%q/%q, want %q/%q/%q/%q",
			got.Texture, got.BlendTexture, got.BlendMask, got.RimTexture,
			want.Texture, want.BlendTexture, want.BlendMask, want.RimTexture)
	case got.RimAcrossPortals != want.RimAcrossPortals:
		t.Errorf("rim_across_portals = %v, want %v", got.RimAcrossPortals, want.RimAcrossPortals)
	case got.DoubleSided != want.DoubleSided:
		t.Errorf("double_sided = %v, want %v", got.DoubleSided, want.DoubleSided)
	case got.UVOffset != want.UVOffset || got.UVScale != want.UVScale ||
		got.UVRotation != want.UVRotation:
		t.Errorf("uv = %v/%v/%v, want %v/%v/%v",
			got.UVOffset, got.UVScale, got.UVRotation,
			want.UVOffset, want.UVScale, want.UVRotation)
	}
}

// The whole point of the zero value: a level that never asked for a slab must
// not grow a block describing one.
func TestNoSlabBlockWithoutThickness(t *testing.T) {
	ch := squareChunk()
	rc, err := chunkToRaw(&ch)
	if err != nil {
		t.Fatal(err)
	}
	if rc.Sectors[0].Floor.Slab != nil {
		t.Error("a floor with no thickness wrote a slab block")
	}
	if rc.Sectors[0].Ceiling.Slab != nil {
		t.Error("a ceiling with no thickness wrote a slab block")
	}
}

// An absent uv_scale has to come back as {1,1} rather than {0,0}, or a slab
// authored by hand — or one saved before a scale was ever set — would collapse
// its texture to a point.
func TestAbsentSlabUVScaleLoadsAsOne(t *testing.T) {
	sl := world.SlabFromRaw(&world.RawSlab{Thickness: 0.2})
	if sl.UVScale != (mgl32.Vec2{1, 1}) {
		t.Errorf("uv_scale = %v, want {1 1}", sl.UVScale)
	}
}
