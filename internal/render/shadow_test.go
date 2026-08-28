package render

import (
	"foundry/internal/gfx"
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// testProjection mirrors the projection main.go builds, so the cascade fit is
// exercised against the real FOV/aspect/near the renderer uses.
func testProjection() mgl32.Mat4 {
	return mgl32.Perspective(mgl32.DegToRad(45.0), 16.0/9.0, 0.1, 1000.0)
}

func TestPerspectiveParamsRoundTrip(t *testing.T) {
	cases := []struct {
		fovDeg, aspect, near float32
	}{
		{45, 16.0 / 9.0, 0.1},
		{60, 4.0 / 3.0, 0.05},
		{90, 1.0, 1.0},
	}
	for _, c := range cases {
		p := mgl32.Perspective(mgl32.DegToRad(c.fovDeg), c.aspect, c.near, 1000)
		tanHalfV, aspect, near := perspectiveParams(p)
		wantTan := float32(math.Tan(float64(mgl32.DegToRad(c.fovDeg)) / 2))
		if math.Abs(float64(tanHalfV-wantTan)) > 1e-4 {
			t.Errorf("fov %v: tanHalfV = %v, want %v", c.fovDeg, tanHalfV, wantTan)
		}
		if math.Abs(float64(aspect-c.aspect)) > 1e-4 {
			t.Errorf("fov %v: aspect = %v, want %v", c.fovDeg, aspect, c.aspect)
		}
		if math.Abs(float64(near-c.near)) > 1e-4 {
			t.Errorf("fov %v: near = %v, want %v", c.fovDeg, near, c.near)
		}
	}
}

func TestCascadeSplitsIncreasingAndPinned(t *testing.T) {
	splits := CascadeSplits(0.1, ShadowMaxDistance)
	prev := float32(0)
	for i, s := range splits {
		if s <= prev {
			t.Errorf("split %d = %v is not greater than previous %v (splits %v)", i, s, prev, splits)
		}
		prev = s
	}
	if last := splits[ShadowCascades-1]; last != ShadowMaxDistance {
		t.Errorf("last split = %v, want exactly ShadowMaxDistance %v", last, ShadowMaxDistance)
	}
	// The first cascade should stay tight enough to be worth having — a split at
	// the uniform 1/N point would give up all the near-field resolution the
	// cascades exist to preserve.
	if splits[0] > ShadowMaxDistance/float32(ShadowCascades) {
		t.Errorf("first split %v is no tighter than a uniform split %v",
			splits[0], ShadowMaxDistance/float32(ShadowCascades))
	}
}

func TestCascadeSliceNearOverlapsPreviousBlendBand(t *testing.T) {
	near := float32(0.1)
	splits := CascadeSplits(near, ShadowMaxDistance)

	if got := cascadeSliceNear(0, near, splits); got != near {
		t.Errorf("cascade 0 near = %v, want camera near %v", got, near)
	}
	for c := 1; c < ShadowCascades; c++ {
		got := cascadeSliceNear(c, near, splits)
		if got >= splits[c-1] {
			t.Errorf("cascade %d near = %v does not reach back before split %v",
				c, got, splits[c-1])
		}
		prevPrev := near
		if c > 1 {
			prevPrev = splits[c-2]
		}
		// The reach-back must cover the whole band the shader cross-fades over,
		// or the blend samples a cascade that holds no depth there.
		blendStart := splits[c-1] - ShadowCascadeBlend*(splits[c-1]-prevPrev)
		if got > blendStart+1e-3 {
			t.Errorf("cascade %d near = %v starts after the blend band at %v",
				c, got, blendStart)
		}
	}
}

// projectsInside reports whether world point p falls inside the light-space box
// described by lsm — the same [0,1] test fragment.glsl applies.
func projectsInside(lsm mgl32.Mat4, p mgl32.Vec3) bool {
	v := lsm.Mul4x1(mgl32.Vec4{p.X(), p.Y(), p.Z(), 1})
	if v.W() == 0 {
		return false
	}
	n := mgl32.Vec3{v.X() / v.W(), v.Y() / v.W(), v.Z() / v.W()}.
		Mul(0.5).Add(mgl32.Vec3{0.5, 0.5, 0.5})
	return n.X() >= 0 && n.X() <= 1 && n.Y() >= 0 && n.Y() <= 1 && n.Z() >= 0 && n.Z() <= 1
}

// TestCascadesCoverTheWholeShadowRange is the regression test for the original
// bug: a single fixed 50-unit ortho box left everything past ~50 units from the
// camera unshadowed. Every point along the view axis out to ShadowMaxDistance
// must now land inside the cascade the shader would select for it.
func TestCascadesCoverTheWholeShadowRange(t *testing.T) {
	tanHalfV, aspect, near := perspectiveParams(testProjection())
	splits := CascadeSplits(near, ShadowMaxDistance)

	camPos := mgl32.Vec3{10, 2, -30}
	viewDir := mgl32.Vec3{0.6, -0.1, 0.8}.Normalize()
	view := mgl32.LookAtV(camPos, camPos.Add(viewDir), gfx.WorldUp)

	lightDir := mgl32.Vec3{0.5, -1, 0.3}.Normalize()

	var mats [ShadowCascades]mgl32.Mat4
	for c := 0; c < ShadowCascades; c++ {
		corners := frustumSliceCorners(view, tanHalfV, aspect,
			cascadeSliceNear(c, near, splits), splits[c])
		mats[c], _ = CascadeLightMatrix(lightDir, corners)
	}

	for d := float32(1); d <= ShadowMaxDistance; d += 1 {
		// The cascade the shader picks for this view depth.
		sel := -1
		for c := 0; c < ShadowCascades; c++ {
			if d <= splits[c] {
				sel = c
				break
			}
		}
		if sel < 0 {
			t.Fatalf("depth %v selected no cascade (splits %v)", d, splits)
		}
		p := camPos.Add(viewDir.Mul(d))
		if !projectsInside(mats[sel], p) {
			t.Errorf("point at %v units (cascade %d) falls outside its cascade box", d, sel)
		}
	}
}

func TestCascadeLightMatrixIsStableUnderCameraRotation(t *testing.T) {
	tanHalfV, aspect, near := perspectiveParams(testProjection())
	splits := CascadeSplits(near, ShadowMaxDistance)
	lightDir := mgl32.Vec3{0.4, -1, 0.2}.Normalize()
	camPos := mgl32.Vec3{0, 3, 0}

	// The bounding-sphere fit makes each cascade's box the same size at every
	// camera heading; without it the box would breathe as the player turns and
	// the shadow texels would swim across surfaces.
	var wantDepth float32
	for i, yaw := range []float64{0, 0.3, 1.1, 2.7, 4.9} {
		dir := mgl32.Vec3{float32(math.Cos(yaw)), -0.2, float32(math.Sin(yaw))}.Normalize()
		view := mgl32.LookAtV(camPos, camPos.Add(dir), gfx.WorldUp)
		corners := frustumSliceCorners(view, tanHalfV, aspect, near, splits[0])
		_, depth := CascadeLightMatrix(lightDir, corners)
		if i == 0 {
			wantDepth = depth
			continue
		}
		if math.Abs(float64(depth-wantDepth)) > 1e-3 {
			t.Errorf("yaw %v: cascade depth %v, want %v (box must not resize with heading)",
				yaw, depth, wantDepth)
		}
	}
}

func TestCascadeLightMatrixCapturesCastersTowardTheLight(t *testing.T) {
	tanHalfV, aspect, near := perspectiveParams(testProjection())
	splits := CascadeSplits(near, ShadowMaxDistance)
	lightDir := mgl32.Vec3{0, -1, 0}.Normalize() // straight down
	camPos := mgl32.Vec3{0, 2, 0}
	view := mgl32.LookAtV(camPos, camPos.Add(mgl32.Vec3{0, 0, 1}), gfx.WorldUp)

	corners := frustumSliceCorners(view, tanHalfV, aspect, near, splits[0])
	lsm, _ := CascadeLightMatrix(lightDir, corners)

	// A caster hovering well above the slice must still be inside the box, or it
	// would drop no shadow onto the ground the player is looking at.
	var center mgl32.Vec3
	for _, c := range corners {
		center = center.Add(c)
	}
	center = center.Mul(1.0 / 8.0)

	above := center.Add(mgl32.Vec3{0, ShadowCasterExtrusion * 0.5, 0})
	if !projectsInside(lsm, above) {
		t.Errorf("caster %v units above the slice centre is outside the cascade box",
			ShadowCasterExtrusion*0.5)
	}
}

func TestFrustumSliceCornersSpanTheSlice(t *testing.T) {
	tanHalfV, aspect, _ := perspectiveParams(testProjection())
	camPos := mgl32.Vec3{5, 1, -2}
	dir := mgl32.Vec3{0, 0, 1}
	view := mgl32.LookAtV(camPos, camPos.Add(dir), gfx.WorldUp)

	corners := frustumSliceCorners(view, tanHalfV, aspect, 10, 40)

	// First four corners sit on the near plane of the slice, last four on the far
	// plane; with a +Z view the depth is the Z offset from the camera.
	for i, c := range corners {
		wantZ := camPos.Z() + 10
		if i >= 4 {
			wantZ = camPos.Z() + 40
		}
		if math.Abs(float64(c.Z()-wantZ)) > 1e-3 {
			t.Errorf("corner %d Z = %v, want %v", i, c.Z(), wantZ)
		}
	}
	// The far corners must be wider than the near ones.
	nearHalfW := math.Abs(float64(corners[0].X() - camPos.X()))
	farHalfW := math.Abs(float64(corners[4].X() - camPos.X()))
	if farHalfW <= nearHalfW {
		t.Errorf("far half-width %v is not greater than near %v", farHalfW, nearHalfW)
	}
	if want := 10 * float64(tanHalfV*aspect); math.Abs(nearHalfW-want) > 1e-3 {
		t.Errorf("near half-width = %v, want %v", nearHalfW, want)
	}
}

func TestSectorLocalAABBIncludesOffsetsAndSurfaceMeshes(t *testing.T) {
	s := world.Sector{
		MinBound: mgl32.Vec2{-4, -6},
		MaxBound: mgl32.Vec2{4, 6},
		Floor: world.Floor{
			Height:        0,
			VertexOffsets: []float32{0, -1.5, 0.5},
			// A pit carved into the floor: the box has to reach its bottom, or
			// the sector is culled while you can still see down into it.
			Mesh: world.SurfaceMesh{
				Points: []world.SurfacePoint{
					{U: -1, V: -1, Depth: -3, Boundary: world.SurfaceFree},
					{U: 1, V: -1, Depth: -3, Boundary: world.SurfaceFree},
					{U: 0, V: 1, Depth: -3, Boundary: world.SurfaceFree},
				},
				Faces: []world.SurfaceFace{{Verts: []int{0, 1, 2}}},
			},
		},
		Ceiling: world.Ceiling{
			Height:        5,
			VertexOffsets: []float32{0, 2.25, 0},
		},
	}
	min, max := s.LocalAABB()

	if min.X() != -4 || min.Z() != -6 || max.X() != 4 || max.Z() != 6 {
		t.Errorf("XZ bounds = %v..%v, want (-4,-6)..(4,6)", min, max)
	}
	if min.Y() != -3 {
		t.Errorf("min Y = %v, want -3 (floor extrusion)", min.Y())
	}
	if max.Y() != 7.25 {
		t.Errorf("max Y = %v, want 7.25 (ceiling vertex offset)", max.Y())
	}
}
