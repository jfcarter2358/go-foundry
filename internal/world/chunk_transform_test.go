package world

// chunk_transform_test.go — Chunk.ModelMatrix()/WorldBounds() as a live
// transform over chunk-local geometry. See chunk.go's own doc comment: Points,
// MinBound/MaxBound and everything derived from them are chunk-local always;
// Origin/Rotation/Scale is the one thing that carries them into world space
// (or into an owning frame's space).

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func approxVec3(t *testing.T, got, want mgl32.Vec3, eps float32, msg string) {
	t.Helper()
	for i := 0; i < 3; i++ {
		if d := got[i] - want[i]; d > eps || d < -eps {
			t.Errorf("%s: got %v, want %v", msg, got, want)
			return
		}
	}
}

func TestChunkModelMatrixIsIdentityWhenUnplaced(t *testing.T) {
	ch := Chunk{}
	if m := ch.ModelMatrix(); m != mgl32.Ident4() {
		t.Errorf("zero-value chunk's ModelMatrix = %v, want identity", m)
	}
	// Scale's zero value means "1.0", same convention the loader has always
	// used — still identity, not a degenerate zero-scale matrix.
	ch2 := Chunk{Scale: 0}
	if m := ch2.ModelMatrix(); m != mgl32.Ident4() {
		t.Errorf("chunk with Scale=0's ModelMatrix = %v, want identity (0 means 1.0)", m)
	}
}

func TestChunkModelMatrixComposesOriginRotationScale(t *testing.T) {
	ch := Chunk{
		Origin:   mgl32.Vec3{10, 5, -3},
		Rotation: float32(math.Pi / 2), // 90°, so local +X maps to world -Z
		Scale:    2,
	}
	m := ch.ModelMatrix()

	// A local point one unit along +X: scaled to 2, rotated 90° about Y
	// (local +X -> world -Z, mgl32's convention for HomogRotate3DY), then
	// translated by Origin.
	local := mgl32.Vec3{1, 0, 0}
	r := m.Mul4x1(local.Vec4(1))
	got := mgl32.Vec3{r.X(), r.Y(), r.Z()}
	want := mgl32.Vec3{10, 5, -3 - 2}
	approxVec3(t, got, want, 1e-4, "transformed local point")

	// The local origin itself always lands exactly on Origin, regardless of
	// rotation/scale.
	r0 := m.Mul4x1(mgl32.Vec3{0, 0, 0}.Vec4(1))
	approxVec3(t, mgl32.Vec3{r0.X(), r0.Y(), r0.Z()}, ch.Origin, 1e-5, "local origin")
}

func TestChunkModelMatrixScalesYUniformly(t *testing.T) {
	// The chunk's own doc comment on Scale: uniform, not XZ-only — a local
	// point's Y (e.g. floor Height baked into a vertex) is carried by the same
	// matrix as X/Z, so it scales too.
	ch := Chunk{Scale: 3}
	m := ch.ModelMatrix()
	r := m.Mul4x1(mgl32.Vec3{0, 2, 0}.Vec4(1))
	got := mgl32.Vec3{r.X(), r.Y(), r.Z()}
	want := mgl32.Vec3{0, 6, 0}
	approxVec3(t, got, want, 1e-5, "Y under uniform scale")
}

func TestChunkModelMatrixComposesUnderFrameXform(t *testing.T) {
	// A frame-owned chunk keeps its own authored local placement even as its
	// frame moves: ModelMatrix = Xform * local, not just Xform.
	ch := Chunk{
		FrameOwner: "ship",
		Origin:     mgl32.Vec3{1, 0, 0},
		Xform:      mgl32.Translate3D(100, 0, 0),
	}
	m := ch.ModelMatrix()
	r := m.Mul4x1(mgl32.Vec3{0, 0, 0}.Vec4(1))
	got := mgl32.Vec3{r.X(), r.Y(), r.Z()}
	want := mgl32.Vec3{101, 0, 0}
	approxVec3(t, got, want, 1e-5, "frame-owned chunk's local placement under its frame")
}

func TestChunkWorldBoundsIsPassthroughWhenUnplaced(t *testing.T) {
	ch := Chunk{
		MinBound: mgl32.Vec3{-1, -2, -3},
		MaxBound: mgl32.Vec3{4, 5, 6},
	}
	min, max := ch.WorldBounds()
	if min != ch.MinBound || max != ch.MaxBound {
		t.Errorf("WorldBounds() = %v..%v for an unplaced chunk, want MinBound/MaxBound unchanged", min, max)
	}
}

func TestChunkWorldBoundsCarriesLocalBoundsThroughOrigin(t *testing.T) {
	ch := Chunk{
		Origin:   mgl32.Vec3{100, 0, 200},
		MinBound: mgl32.Vec3{-1, 0, -1},
		MaxBound: mgl32.Vec3{1, 2, 1},
	}
	min, max := ch.WorldBounds()
	approxVec3(t, min, mgl32.Vec3{99, 0, 199}, 1e-4, "min")
	approxVec3(t, max, mgl32.Vec3{101, 2, 201}, 1e-4, "max")
}
