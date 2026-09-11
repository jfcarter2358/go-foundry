package editor

// editor_volume_pick_test.go — the maths behind clicking a volume in the 3D
// viewport, and the selection it leaves behind.

import (
	"foundry/internal/world"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// nearMiss is the tolerance every distance comparison here uses: the shapes are
// solved in float32, so an exact equality would fail on the rounding alone.
const nearMiss = 1e-3

func wantRange(t *testing.T, label string, gotT0, gotT1 float32, ok bool, t0, t1 float32) {
	t.Helper()
	if !ok {
		t.Fatalf("%s: missed, want [%v, %v]", label, t0, t1)
	}
	if math.Abs(float64(gotT0-t0)) > nearMiss || math.Abs(float64(gotT1-t1)) > nearMiss {
		t.Fatalf("%s: got [%v, %v], want [%v, %v]", label, gotT0, gotT1, t0, t1)
	}
}

// TestRayShapeRangeEntryAndExit checks each of the three solids against a ray
// running down -Z from the origin, which enters a shape centred at z = -10 at
// its near face and leaves at its far one.
func TestRayShapeRangeEntryAndExit(t *testing.T) {
	origin := mgl32.Vec3{0, 0, 0}
	dir := mgl32.Vec3{0, 0, -1}
	center := mgl32.Vec3{0, 0, -10}
	half := mgl32.Vec3{1, 2, 3}

	t0, t1, ok := rayShapeRange(origin, dir, center, mgl32.Vec3{}, half, world.BoundingAABB)
	wantRange(t, "box", t0, t1, ok, 7, 13)

	// Down the Z axis the ellipsoid is as deep as its Z half-extent, so it
	// spans the same range as the box that contains it.
	t0, t1, ok = rayShapeRange(origin, dir, center, mgl32.Vec3{}, half, world.BoundingSphere)
	wantRange(t, "sphere", t0, t1, ok, 7, 13)

	t0, t1, ok = rayShapeRange(origin, dir, center, mgl32.Vec3{}, half, world.BoundingCylinder)
	wantRange(t, "cylinder", t0, t1, ok, 7, 13)

	// Off the axis by more than the X half-extent: the box is still hit at that
	// height, but the round shapes have narrowed past it.
	off := mgl32.Vec3{0.99, 0, 0}
	if _, _, ok := rayShapeRange(off, dir, center, mgl32.Vec3{}, half, world.BoundingAABB); !ok {
		t.Fatal("box: an offset ray inside the X extent should still hit")
	}
	for _, kind := range []world.BoundingType{world.BoundingSphere, world.BoundingCylinder} {
		if _, _, ok := rayShapeRange(mgl32.Vec3{1.5, 0, 0}, dir, center, mgl32.Vec3{}, half, kind); ok {
			t.Fatalf("kind %v: a ray outside the X extent should miss", kind)
		}
	}
}

// TestRayShapeRangeRotated checks that a shape's rotation is taken into
// account: a flat slab turned a quarter turn about Y presents its long side to
// a ray that previously grazed its short one.
func TestRayShapeRangeRotated(t *testing.T) {
	origin := mgl32.Vec3{0, 0, 0}
	dir := mgl32.Vec3{0, 0, -1}
	center := mgl32.Vec3{0, 0, -10}
	half := mgl32.Vec3{4, 1, 0.5} // wide in X, thin in Z

	t0, t1, ok := rayShapeRange(origin, dir, center, mgl32.Vec3{}, half, world.BoundingAABB)
	wantRange(t, "unrotated slab", t0, t1, ok, 9.5, 10.5)

	// A quarter turn about Y swaps X and Z, so the ray now runs the slab's
	// length instead of its thickness.
	turned := mgl32.Vec3{0, float32(math.Pi / 2), 0}
	t0, t1, ok = rayShapeRange(origin, dir, center, turned, half, world.BoundingAABB)
	wantRange(t, "rotated slab", t0, t1, ok, 6, 14)
}

// TestRayShapeRangeCylinderCaps checks that a ray coming straight down the
// cylinder's axis is bounded by its caps rather than running for ever.
func TestRayShapeRangeCylinderCaps(t *testing.T) {
	half := mgl32.Vec3{1, 2, 1}
	t0, t1, ok := rayShapeRange(mgl32.Vec3{0, 10, 0}, mgl32.Vec3{0, -1, 0},
		mgl32.Vec3{}, mgl32.Vec3{}, half, world.BoundingCylinder)
	wantRange(t, "down the axis", t0, t1, ok, 8, 12)

	// Parallel to the axis but outside the circle: never inside.
	if _, _, ok := rayShapeRange(mgl32.Vec3{5, 10, 0}, mgl32.Vec3{0, -1, 0},
		mgl32.Vec3{}, mgl32.Vec3{}, half, world.BoundingCylinder); ok {
		t.Fatal("a ray parallel to the axis and outside the circle should miss")
	}
}

// TestPickVolumeRayTSkipsVolumeAroundTheCamera is the rule that keeps a
// room-sized trigger from swallowing every click in the room it wraps.
func TestPickVolumeRayTSkipsVolumeAroundTheCamera(t *testing.T) {
	origin := mgl32.Vec3{0, 0, 0}
	dir := mgl32.Vec3{0, 0, -1}

	ahead := world.NewVolume("ahead", mgl32.Vec3{0, 0, -5})
	if got := pickVolumeRayT(nil, origin, dir, &ahead); math.Abs(float64(got-4)) > nearMiss {
		t.Fatalf("volume ahead: got %v, want ~4", got)
	}

	around := world.NewVolume("around", mgl32.Vec3{0, 0, 0})
	around.HalfX, around.HalfY, around.HalfZ = 50, 50, 50
	if got := pickVolumeRayT(nil, origin, dir, &around); got != float32(math.MaxFloat32) {
		t.Fatalf("volume around the camera: got %v, want a miss", got)
	}

	// A disabled volume is still clickable — a script that disarmed a trigger
	// must not make it unselectable.
	ahead.Enabled = false
	if got := pickVolumeRayT(nil, origin, dir, &ahead); math.Abs(float64(got-4)) > nearMiss {
		t.Fatalf("disabled volume: got %v, want ~4", got)
	}

	// No extents authored: nothing to aim at.
	ahead.HalfX = 0
	if got := pickVolumeRayT(nil, origin, dir, &ahead); got != float32(math.MaxFloat32) {
		t.Fatalf("unbounded volume: got %v, want a miss", got)
	}
}

// TestPick3DPicksVolume checks that a volume joins the closest-hit race, and
// loses to a wall in front of it.
func TestPick3DPicksVolume(t *testing.T) {
	mx, my := setupPickCamera()

	prevLevel := Editor.level
	defer func() { Editor.level = prevLevel }()

	setup := func(brushZ float32) {
		brushGeomLevel(t, world.NewBoxBrush("wall",
			mgl32.Vec3{0, 0, brushZ}, mgl32.Vec3{4, 4, 0.5}, "concrete"))
		Editor.level.Volumes = []world.Volume{world.NewVolume("v0", mgl32.Vec3{0, 0, -8})}
	}

	// Volume at 7 (its near face), brush at 20 → the volume wins.
	setup(-20)
	if hit := pick3D(mx, my); hit.kind != pickVolume || hit.idx != 0 {
		t.Fatalf("near volume: got kind=%q idx=%d, want the volume", hit.kind, hit.idx)
	}

	// Brush at 3, volume at 7 → the brush wins.
	setup(-3)
	if hit := pick3D(mx, my); hit.kind != pickBrushFace {
		t.Fatalf("near brush: got kind=%q, want the brush face", hit.kind)
	}
}

// TestEditorSelectVolumeGivesItTheGizmo covers what selection is for: the
// volume becomes what the translate, rotate and scale gizmos all act on.
func TestEditorSelectVolumeGivesItTheGizmo(t *testing.T) {
	prevLevel, prevMode, prevKind := Editor.level, Editor.Mode, Editor.GizmoKind
	defer func() {
		Editor.level, Editor.Mode, Editor.GizmoKind = prevLevel, prevMode, prevKind
		Editor.ActiveVolumeIdx = -1
	}()

	Editor.Mode = Mode3D
	Editor.GizmoKind = gizmoKindNone
	Editor.level = &world.Level{Volumes: []world.Volume{world.NewVolume("v0", mgl32.Vec3{1, 2, 3})}}

	editorSelectVolume(0)
	if Editor.GizmoKind != gizmoKindVolume {
		t.Fatalf("gizmo kind: got %q, want %q", Editor.GizmoKind, gizmoKindVolume)
	}
	if Editor.PanelFocus != panelFocusVolume {
		t.Fatalf("panel focus: got %q, want %q", Editor.PanelFocus, panelFocusVolume)
	}
	if activeGizmoVolume() == nil {
		t.Fatal("the gizmo has no volume to drag")
	}
	for label, origin := range map[string]func() (mgl32.Vec3, bool){
		"translate": translateGizmoOrigin,
		"rotate":    rotateGizmoOrigin,
		"scale":     scaleGizmoOrigin,
	} {
		pos, ok := origin()
		if !ok || pos != (mgl32.Vec3{1, 2, 3}) {
			t.Fatalf("%s gizmo origin: got %v (ok=%v), want the volume's position", label, pos, ok)
		}
	}

	editorClearVolumeSelection()
	if Editor.GizmoKind != gizmoKindNone || activeVolume() != nil {
		t.Fatalf("after clearing: kind=%q volume=%v", Editor.GizmoKind, activeVolume())
	}
}
