package main

// editor_2d_objects.go — entities, primitives and point lights in the top-down
// 2D map.
//
// The 2D map used to draw sector geometry only, so the object tools the side
// panel offers (catalogs, primitive/entity properties) had nothing to aim at.
// This file gives those objects a top-down footprint, a click target, and a
// spawn position, so placing and selecting them works the same in 2D as in 3D.

import (
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
	GizmoLine(c00, c10, col)
	GizmoLine(c10, c11, col)
	GizmoLine(c11, c01, col)
	GizmoLine(c01, c00, col)
	GizmoLine(c00, c11, col)
	GizmoLine(c10, c01, col)
}

// Editor2DRenderObjects draws the level's entities, the active chunk's
// primitives, and the point lights as top-down footprints.
func Editor2DRenderObjects(lvl *Level, ch *Chunk) {
	yellow := mgl32.Vec3{1, 1, 0}
	green := mgl32.Vec3{0.2, 1, 0.2}
	orange := mgl32.Vec3{1, 0.55, 0.15}
	purple := mgl32.Vec3{0.65, 0.45, 1}

	// Primitives belong to the active chunk, matching what the panel edits.
	for pi := range ch.Primitives {
		p := &ch.Primitives[pi]
		col := orange
		if pi == Editor.ActivePrimitiveIdx {
			col = green
		}
		hx, hz := editor2DObjectHalfExtents(p.BoundHalfX, p.BoundHalfZ)
		editor2DDrawFootprint(p.Position.X(), p.Position.Z(), hx, hz, col)
	}

	// Entities are level-wide, not chunk-scoped.
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		col := purple
		if i == Editor.ActiveEntityIdx {
			col = yellow
		}
		hx, hz := editor2DObjectHalfExtents(e.BoundHalfX, e.BoundHalfZ)
		editor2DDrawFootprint(e.Position.X(), e.Position.Z(), hx, hz, col)
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
		GizmoLine(c.Sub(mgl32.Vec3{r, 0, r}), c.Add(mgl32.Vec3{r, 0, r}), col)
		GizmoLine(c.Sub(mgl32.Vec3{r, 0, -r}), c.Add(mgl32.Vec3{r, 0, -r}), col)
	}
}

// editor2DPickObject selects the entity or primitive whose footprint contains
// (wx,wz), nearest centre first, and reports whether anything was hit.
// Primitives win ties with entities only by being closer; both are tested.
func editor2DPickObject(lvl *Level, ch *Chunk, wx, wz float32) bool {
	bestDist := float32(0)
	bestEntity := -1
	bestPrim := -1

	consider := func(cx, cz, halfX, halfZ float32, entIdx, primIdx int) {
		hx, hz := editor2DObjectHalfExtents(halfX, halfZ)
		dx := wx - cx
		dz := wz - cz
		if dx < -hx || dx > hx || dz < -hz || dz > hz {
			return
		}
		d := dx*dx + dz*dz
		if (bestEntity < 0 && bestPrim < 0) || d < bestDist {
			bestDist = d
			bestEntity = entIdx
			bestPrim = primIdx
		}
	}

	for pi := range ch.Primitives {
		p := &ch.Primitives[pi]
		consider(p.Position.X(), p.Position.Z(), p.BoundHalfX, p.BoundHalfZ, -1, pi)
	}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		consider(e.Position.X(), e.Position.Z(), e.BoundHalfX, e.BoundHalfZ, i, -1)
	}

	if bestEntity < 0 && bestPrim < 0 {
		return false
	}

	clearVertexSelection()
	Editor.ActiveEdgeIdx = -1
	Editor.ActiveEdgeKind = ""
	Editor.ActiveEntityIdx = bestEntity
	Editor.ActivePrimitiveIdx = bestPrim
	if bestEntity >= 0 {
		Editor.PanelFocus = panelFocusEntity
		Editor.GizmoKind = gizmoKindEntity
	} else {
		Editor.PanelFocus = panelFocusPrimitive
		Editor.GizmoKind = gizmoKindPrimitive
	}
	return true
}

// editor2DFloorHeightAt returns the floor height of the sector containing
// (wx,wz), or 0 when the point is outside every sector. New objects spawn
// relative to it so a click on a raised floor doesn't drop them through it.
func editor2DFloorHeightAt(ch *Chunk, wx, wz float32) float32 {
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if pointInPolygon(mgl32.Vec2{wx, wz}, SectorPoints(ch, s)) {
			return s.Floor.Height
		}
	}
	return 0
}

// editor2DHandlePlacement spawns the armed entity or primitive at the clicked
// point and reports whether it consumed the click. Placement is checked before
// every other 2D tool: arming it is an explicit request, so it must not be
// swallowed by the biome brush or by vertex editing.
func editor2DHandlePlacement(t sdl.MouseButtonEvent, ch *Chunk, wx, wz float32) bool {
	if t.Button != sdl.ButtonLeft {
		return false
	}
	if !Editor.PlacementMode && !Editor.PrimPlacementMode {
		return false
	}
	y := editor2DFloorHeightAt(ch, wx, wz)

	if Editor.PlacementMode {
		// Entity origins are model-space bases, so the floor height puts the
		// model on the ground — the same alignment the 3D placement does.
		placeSelectedEntityAt(mgl32.Vec3{wx, y, wz})
		return true
	}

	// Primitives are centred on their origin, so lift the solid kinds by half
	// their default height to rest them on the floor. Planes and water are
	// flat and stay at floor level.
	switch Editor.PrimPlacementType {
	case "plane", "water":
	default:
		y += 0.5
	}
	placeSelectedPrimitiveAt(mgl32.Vec3{wx, y, wz})
	return true
}
