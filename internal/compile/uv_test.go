package compile

// uv_test.go — a painted face stays painted when the compiler cuts it.

import (
	"testing"

	"foundry/internal/geom"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// A slab with a pillar sunk into it: CSG cuts the slab's top face into pieces
// around the pillar's footprint, which is the cut a painted floor most often
// gets. (A pillar merely resting on the slab would cut nothing — a face lying
// ON a solid's surface is not inside it.) Every piece has to sample the texels
// the uncut face had in the same place: inside the face's island, and exactly
// its stated UV at any of its corners a piece kept.
func TestAnUnwrappedFaceKeepsItsTexelsWhereItIsCut(t *testing.T) {
	const top = 3 // NewBoxBrush's +Y face
	lvl := levelOf(
		world.NewBoxBrush("slab", mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{4, 0.5, 4}, "floor_atlas"),
		world.NewBoxBrush("pillar", mgl32.Vec3{0, 0.5, 0}, mgl32.Vec3{0.5, 1, 0.5}, ""),
	)
	slab := &lvl.Chunks[0].Brushes[0]
	af := []world.AtlasFace{{Mesh: &slab.BrushMesh, Face: top}}
	lay, err := world.LayoutBrushAtlas(af, geom.AtlasOptions{})
	if err != nil {
		t.Fatalf("LayoutBrushAtlas: %v", err)
	}
	world.ApplyBrushAtlas(af, lay)

	src := &slab.Faces[top]
	lo := mgl32.Vec2{1, 1}
	var hi mgl32.Vec2
	for _, uv := range src.UVs {
		lo = mgl32.Vec2{min(lo.X(), uv.X()), min(lo.Y(), uv.Y())}
		hi = mgl32.Vec2{max(hi.X(), uv.X()), max(hi.Y(), uv.Y())}
	}

	pieces, cornersKept := 0, 0
	for _, f := range mustCSG(t, lvl) {
		if f.Brush != "slab" || f.FaceIdx != top {
			if f.UVMapped {
				t.Errorf("%s face %d was never unwrapped but carries a UV map", f.Brush, f.FaceIdx)
			}
			continue
		}
		pieces++
		if !f.UVMapped {
			t.Fatal("a piece of the unwrapped face came out without its UV map")
		}
		for _, p := range f.Points {
			uv := f.UVMap.At(p)
			const eps = 1e-4
			if uv.X() < lo.X()-eps || uv.Y() < lo.Y()-eps || uv.X() > hi.X()+eps || uv.Y() > hi.Y()+eps {
				t.Errorf("piece corner %v samples %v, outside the face's island %v..%v", p, uv, lo, hi)
			}
			for c, vi := range src.Indices {
				if slab.Verts[vi].Sub(p).Len() > eps {
					continue
				}
				cornersKept++
				if uv.Sub(src.UVs[c]).Len() > eps {
					t.Errorf("the face's own corner %v samples %v in its piece, %v uncut", p, uv, src.UVs[c])
				}
			}
		}
	}
	if pieces < 2 {
		t.Fatalf("the slab's top came through in %d piece(s); this test needs it cut", pieces)
	}
	if cornersKept == 0 {
		t.Error("no piece kept one of the face's own corners to check the map against")
	}
}

// A texture override replaces the painting in the compiled level too: every
// piece wears the override's texture and transform, and none carries the
// unwrap's map, which would sample the override's image in pieces.
func TestAnOverriddenFaceCompilesWithItsOverride(t *testing.T) {
	const top = 3
	lvl := levelOf(
		world.NewBoxBrush("slab", mgl32.Vec3{0, -0.5, 0}, mgl32.Vec3{4, 0.5, 4}, "floor_atlas"),
	)
	slab := &lvl.Chunks[0].Brushes[0]
	af := []world.AtlasFace{{Mesh: &slab.BrushMesh, Face: top}}
	lay, err := world.LayoutBrushAtlas(af, geom.AtlasOptions{})
	if err != nil {
		t.Fatalf("LayoutBrushAtlas: %v", err)
	}
	world.ApplyBrushAtlas(af, lay)
	src := &slab.Faces[top]
	src.TextureOverride = true
	src.OverrideTexture = "tiles"
	src.OverrideUVOffset = mgl32.Vec2{0.25, 0.5}

	found := false
	for _, f := range mustCSG(t, lvl) {
		if f.FaceIdx != top {
			continue
		}
		found = true
		if f.Texture != "tiles" || f.UVOffset != src.OverrideUVOffset || f.UVMapped {
			t.Errorf("piece compiled with texture %q, offset %v, mapped %v; want the override",
				f.Texture, f.UVOffset, f.UVMapped)
		}
	}
	if !found {
		t.Fatal("the slab's top did not come through the compile")
	}
}
