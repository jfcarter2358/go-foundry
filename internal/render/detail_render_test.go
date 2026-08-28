package render

// detail_render_test.go — Phase 4 regression coverage for forEachDetailFace's
// chunk-transform composition (see HANDOFF.md): each emitted detailFace must
// carry the owning chunk's live ModelMatrix, and cull composition must test
// WORLD-space bounds (chunk-local bounds carried through that same matrix),
// not raw chunk-local coordinates straight against a world-space frustum —
// the same class of bug Phase 2's spatial-hash fix caught for collision.

import (
	"foundry/internal/world"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestForEachDetailFaceComposesChunkTransform(t *testing.T) {
	origin := mgl32.Vec3{500, 0, 0}
	ch := world.Chunk{
		Origin: origin, Scale: 1,
		Sectors: []world.Sector{{
			MinBound: mgl32.Vec2{-5, -5}, MaxBound: mgl32.Vec2{5, 5},
			Floor: world.Floor{Enabled: true, Detail: world.DetailSpec{Enabled: true}},
		}},
	}
	level := &world.Level{Chunks: []world.Chunk{ch}}

	var got []detailFace
	forEachDetailFace(level, nil, func(f detailFace) { got = append(got, f) })
	if len(got) != 1 {
		t.Fatalf("got %d faces with no cull, want 1", len(got))
	}
	if want := ch.ModelMatrix(); got[0].model != want {
		t.Errorf("face model = %v, want ch.ModelMatrix() = %v", got[0].model, want)
	}

	// A frustum framing world X in [400,600] must still find the moved
	// chunk's face: its sector sits at local X in [-5,5], but the chunk's
	// Origin puts it at world X == 500. Proves the cull test is composed
	// through the chunk's transform, not run against raw local coordinates.
	// Ortho's left/right/bottom/top are VIEW-space bounds, and this camera's
	// axes line up 1:1 with world axes (LookAtV with no roll, eye on the
	// world Z axis) — so keeping the eye at world X==0 lets Ortho's X bounds
	// be read directly as the world X range this frustum frames.
	seesChunk := ExtractFrustum(mgl32.Ortho(400, 600, -50, 50, 0.1, 50).
		Mul4(mgl32.LookAtV(mgl32.Vec3{0, 0, 25}, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})))
	got = nil
	forEachDetailFace(level, &seesChunk, func(f detailFace) { got = append(got, f) })
	if len(got) != 1 {
		t.Errorf("moved chunk's face was culled by a frustum that should see it: got %d faces, want 1", len(got))
	}

	// The same chunk against a frustum framing the ORIGIN — where it would
	// wrongly appear if its local coordinates were tested directly against a
	// world-space frustum with no transform applied at all — must NOT be
	// seen: the chunk actually lives 500 units away.
	seesOrigin := ExtractFrustum(mgl32.Ortho(-100, 100, -50, 50, 0.1, 50).
		Mul4(mgl32.LookAtV(mgl32.Vec3{0, 0, 25}, mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 1, 0})))
	got = nil
	forEachDetailFace(level, &seesOrigin, func(f detailFace) { got = append(got, f) })
	if len(got) != 0 {
		t.Errorf("a frustum framing the origin saw the moved chunk's face (got %d) — "+
			"cull box is not composed through the chunk's transform", len(got))
	}
}
