package editor

// editor_2d_objects.go — the spawn point, the placed entities and the point
// lights in the top-down 2D map.
//
// The 2D map draws sector geometry, which left the things that are not geometry
// — the player spawn, the entities, the lights — with nothing to show for
// themselves up here. This file gives them a top-down footprint, and the spawn
// point and entities a click target and a placement position, so placing and
// selecting either works the same in 2D as in 3D.

import (
	"foundry/internal/geom"
	"foundry/internal/gizmo"
	"foundry/internal/world"
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// editor2DObjectMinPx is the smallest on-screen half-extent an object footprint
// is drawn (and hit-tested) at, so a tiny or zoomed-out object stays visible
// and clickable instead of collapsing to a dot.
const editor2DObjectMinPx = float32(6.0)

// editor2DObjectHalfExtents returns the top-down half-extents to draw and
// hit-test an object with, widened so it never falls below the minimum
// on-screen size.
func editor2DObjectHalfExtents(halfX, halfZ float32) (float32, float32) {
	min := screenToWorldSize(editor2DObjectMinPx, editor2DOrtho())
	if halfX < min {
		halfX = min
	}
	if halfZ < min {
		halfZ = min
	}
	return halfX, halfZ
}

// editor2DDrawFootprint draws an axis-aligned rectangle plus a centre cross at
// world XZ (cx,cz), flat at y=0 so it reads against the map regardless of the
// object's actual height.
func editor2DDrawFootprint(cx, cz, halfX, halfZ float32, col mgl32.Vec3) {
	c00 := mgl32.Vec3{cx - halfX, 0, cz - halfZ}
	c10 := mgl32.Vec3{cx + halfX, 0, cz - halfZ}
	c11 := mgl32.Vec3{cx + halfX, 0, cz + halfZ}
	c01 := mgl32.Vec3{cx - halfX, 0, cz + halfZ}
	gizmo.GizmoLine(c00, c10, col)
	gizmo.GizmoLine(c10, c11, col)
	gizmo.GizmoLine(c11, c01, col)
	gizmo.GizmoLine(c01, c00, col)
	gizmo.GizmoLine(c00, c11, col)
	gizmo.GizmoLine(c10, c01, col)
}

// editor2DDrawHeading draws a facing arrow on the map at world XZ centre c,
// pointing along the (unnormalised) XZ direction dir and sized against the
// footprint it grows out of, so it stays readable at any zoom.
//
// Shared by the spawn point and the entities: both answer "which way is this
// thing pointing", and drawing that two different ways would make one of them
// look like a different kind of annotation.
func editor2DDrawHeading(c, dir mgl32.Vec2, hx, hz float32, col mgl32.Vec3) {
	if dir.Len() < 1e-4 {
		// Pointing straight up or down has no heading to draw.
		return
	}
	flat := dir.Normalize()

	reach := hx
	if hz > reach {
		reach = hz
	}
	reach *= 2.2

	c3 := mgl32.Vec3{c.X(), 0, c.Y()}
	tip := c3.Add(mgl32.Vec3{flat.X() * reach, 0, flat.Y() * reach})
	gizmo.GizmoLine(c3, tip, col)

	// Barbs at ±140° from the heading, a third of the shaft long.
	const barbAngle = 140 * math.Pi / 180
	barb := reach / 3
	for _, sign := range []float64{1, -1} {
		a := math.Atan2(float64(flat.Y()), float64(flat.X())) + sign*barbAngle
		gizmo.GizmoLine(tip, tip.Add(mgl32.Vec3{
			float32(math.Cos(a)) * barb, 0, float32(math.Sin(a)) * barb,
		}), col)
	}
}

// Editor2DRenderObjects draws the level's spawn point, its entities and its
// point lights as top-down footprints.
func Editor2DRenderObjects(lvl *world.Level, ch *world.Chunk) {
	green := mgl32.Vec3{0.2, 1, 0.2}
	orange := mgl32.Vec3{1, 0.55, 0.15}

	if lvl.Spawn != nil {
		col := orange
		if Editor.SpawnSelected {
			col = green
		}
		hxRaw, _, hzRaw := world.SpawnPointBounds()
		hx, hz := editor2DObjectHalfExtents(hxRaw, hzRaw)
		editor2DDrawFootprint(lvl.Spawn.Position.X(), lvl.Spawn.Position.Z(), hx, hz, col)
		// A square footprint says nothing about facing, and facing is half of
		// what a spawn point is — draw its heading on the map too.
		editor2DDrawSpawnHeading(lvl.Spawn, hx, hz, col)
	}

	// Entities: their bounding footprint, filled in green when selected. An
	// entity with no authored bounds still gets one at the minimum on-screen
	// size — it is a thing in the level whether or not it has a volume, and one
	// that drew nothing would be unfindable from up here.
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		col := entityMapColor
		if i == Editor.ActiveEntityIdx {
			col = green
		}
		hx, hz := editor2DEntityHalfExtents(e)
		c := e.BoundCenter()
		editor2DDrawFootprint(c.X(), c.Z(), hx, hz, col)
		// Facing matters for an entity the same way it does for the spawn
		// point: a square says nothing about which way the model is pointing.
		editor2DDrawEntityHeading(e, hx, hz, col)
	}

	// Point lights: a cross tinted with the light's own color, so several
	// lights stay distinguishable. They are edited from the Lights panel
	// rather than by clicking, so they get no hit test.
	r := screenToWorldSize(7, editor2DOrtho())
	for i := range lvl.Lights.Points {
		pl := &lvl.Lights.Points[i]
		col := pl.Color
		if col.Len() < 0.1 {
			col = mgl32.Vec3{1, 0.8, 0.2}
		}
		c := mgl32.Vec3{pl.Position.X(), 0, pl.Position.Z()}
		gizmo.GizmoLine(c.Sub(mgl32.Vec3{r, 0, r}), c.Add(mgl32.Vec3{r, 0, r}), col)
		gizmo.GizmoLine(c.Sub(mgl32.Vec3{r, 0, -r}), c.Add(mgl32.Vec3{r, 0, -r}), col)
	}
}

// entityMapColor is the footprint colour of an unselected entity — distinct
// from the spawn point's orange so the two do not read as the same kind of
// thing on a busy map.
var entityMapColor = mgl32.Vec3{0.35, 0.75, 1}

// editor2DEntityHalfExtents returns the top-down half-extents to draw and
// hit-test an entity with, widened to the minimum on-screen size.
func editor2DEntityHalfExtents(e *world.Entity) (float32, float32) {
	h := e.BoundHalfExtents()
	return editor2DObjectHalfExtents(h.X(), h.Z())
}

// editor2DDrawEntityHeading draws an entity's yaw as an arrow on the map, using
// the same shape the spawn point's heading uses so the two read alike.
func editor2DDrawEntityHeading(e *world.Entity, hx, hz float32, col mgl32.Vec3) {
	d := world.EulerMatrixYXZ(e.Rotation).Mul4x1(mgl32.Vec4{1, 0, 0, 0})
	c := e.BoundCenter()
	editor2DDrawHeading(mgl32.Vec2{c.X(), c.Z()}, mgl32.Vec2{d.X(), d.Z()}, hx, hz, col)
}

// editor2DPickObject selects the spawn point or an entity when (wx,wz) falls
// inside its footprint, and reports whether it did.
//
// Entities are tested first: they are things *in* the level, and the spawn
// marker is an annotation on it, so where the two overlap the click should
// reach the one that is actually part of the world.
func editor2DPickObject(lvl *world.Level, ch *world.Chunk, wx, wz float32) bool {
	if idx, ok := editor2DEntityAt(lvl, wx, wz); ok {
		clearVertexSelection()
		editorClearSpawnSelection()
		editorClearVolumeSelection()
		editorClearChunkSelection()
		editorClearLightSelection()
		Editor.ActiveEdgeIdx = -1
		Editor.ActiveEdgeKind = ""
		editorSelectEntity(idx)
		return true
	}

	if lvl.Spawn == nil {
		return false
	}
	hxRaw, _, hzRaw := world.SpawnPointBounds()
	hx, hz := editor2DObjectHalfExtents(hxRaw, hzRaw)
	dx := wx - lvl.Spawn.Position.X()
	dz := wz - lvl.Spawn.Position.Z()
	if dx < -hx || dx > hx || dz < -hz || dz > hz {
		return false
	}

	clearVertexSelection()
	editorClearEntitySelection()
	editorClearVolumeSelection()
	editorClearChunkSelection()
	editorClearLightSelection()
	Editor.ActiveEdgeIdx = -1
	Editor.ActiveEdgeKind = ""
	editorSelectSpawn()
	return true
}

// editor2DEntityAt returns the index of the entity whose footprint covers
// (wx,wz). Later entities win, so the most recently placed one is on top —
// which is what a user who just dropped an entity onto a pile expects to grab.
func editor2DEntityAt(lvl *world.Level, wx, wz float32) (int, bool) {
	found, ok := -1, false
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		hx, hz := editor2DEntityHalfExtents(e)
		c := e.BoundCenter()
		dx, dz := wx-c.X(), wz-c.Z()
		if dx < -hx || dx > hx || dz < -hz || dz > hz {
			continue
		}
		found, ok = i, true
	}
	return found, ok
}

// editor2DFloorHeightAt returns the floor height of the sector containing
// (wx,wz), or 0 when the point is outside every sector. The spawn point is
// placed relative to it so a click on a raised floor doesn't drop it through.
func editor2DFloorHeightAt(ch *world.Chunk, wx, wz float32) float32 {
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if geom.PointInPolygon(mgl32.Vec2{wx, wz}, world.SectorPoints(ch, s)) {
			return s.Floor.Height
		}
	}
	return 0
}

// editor2DHandlePlacement drops the spawn point at the clicked position and
// reports whether it consumed the click. Placement is checked before every
// other 2D tool: arming it is an explicit request, so it must not be swallowed
// by the biome brush or by vertex editing.
func editor2DHandlePlacement(t sdl.MouseButtonEvent, ch *world.Chunk, wx, wz float32) bool {
	if t.Button != sdl.ButtonLeft {
		return false
	}
	switch {
	case Editor.SpawnPlacementMode:
		// The marker's Position is the player's feet, so it sits at floor
		// height rather than being lifted like a solid would be.
		placeSpawnPointAt(mgl32.Vec3{wx, editor2DFloorHeightAt(ch, wx, wz), wz})
		return true
	case Editor.EntityPlacementMode:
		// Same reasoning: an entity's Position is its base, so the floor is
		// where it belongs. The 3D view puts it on the surface under the
		// cursor; up here there is only one surface it can mean.
		placeEntityAt(mgl32.Vec3{wx, editor2DFloorHeightAt(ch, wx, wz), wz})
		return true
	}
	return false
}
