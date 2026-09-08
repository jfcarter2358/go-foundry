package editor

// editor_brush_tools.go — making and reshaping brushes.
//
// A brush is a convex polyhedron authored as a vertex + face list, so every
// tool here is a mesh operation, not a parametric one: the stamps below emit
// real corners and real face loops, and extrude/inset/hollow rewrite them. That
// is the whole point of the format (see world/brush.go) — the level is edited
// with the same vocabulary a model is.
//
// Convexity is the one invariant the editor owes the rest of the engine.
// Collision reads a brush's verts as a hull directly, and the compile pipeline
// clips faces as half-spaces; both are wrong on a concave brush rather than
// merely slow. Every operation here either preserves convexity by construction
// (the stamps, extrude along a face normal, inset) or is checked afterwards and
// reported — see warnIfSelectionBrokeConvexity, which a free vertex drag runs
// because a free vertex drag genuinely can break it.
//
// Nothing here rebuilds the whole level. A brush owns its own GPU buffers, so
// an edit reloads that brush and recomputes its chunk's bounds; the chunk's
// other brushes are untouched.

import (
	"math"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	// defaultBrushWallThickness is how deep a hollowed room's shell is, and
	// what a new chunk's starter room uses. Thick enough to be visible from
	// outside and to give the floor something to stand on, thin enough that a
	// small room is still mostly room.
	defaultBrushWallThickness float32 = 0.5

	// defaultBrushHalf is the half-extent a stamped primitive starts at, so a
	// fresh box is 2x2x2 world units — one comfortable step, and a round number
	// on every sensible grid.
	defaultBrushHalf float32 = 1.0

	// brushCylinderSides is how many sides a stamped cylinder has. Twelve is
	// the smallest count that reads as round at arm's length while staying
	// cheap: a cylinder brush is 12 side faces plus a cap at each end, and
	// every one of them is a hull plane collision has to test.
	brushCylinderSides = 12

	// defaultBrushTexture is what a freshly stamped brush's faces are painted
	// with. Something visibly placeholder rather than nothing: an untextured
	// face renders white and reads as a bug.
	defaultBrushTexture = "concrete"
)

// ---------- placing a brush ----------

// editorAddBrushAt stamps a primitive into the active chunk, centred on pos
// (world space) and snapped to the grid when snapping is on.
//
// The brush lands *selected*, unlike the scenery brush's stamps: a level brush
// is placed one at a time and then shaped, so the next thing the author wants
// is to work on what they just made. (Scenery is the opposite — see
// editor_scenery.go's own note.)
func editorAddBrushAt(kind string, pos mgl32.Vec3) {
	lvl := Editor.level
	ch := activeChunk()
	if lvl == nil || ch == nil {
		return
	}
	if Editor.SnapToGrid && Editor.StepSize > 0 {
		pos = snapVec3(pos, Editor.StepSize)
	}
	// pos is where the author clicked, in world space; a brush's verts are
	// chunk-local, so the chunk's own placement is divided out here rather
	// than being baked into the geometry.
	local := ch.WorldToLocal(pos)
	half := mgl32.Vec3{defaultBrushHalf, defaultBrushHalf, defaultBrushHalf}

	_ = gfx.EnsureTextureLoaded(defaultBrushTexture)
	EditorPushUndo()

	id := world.UniqueBrushID(ch, kind)
	var b world.Brush
	switch kind {
	case brushKindWedge:
		b = newWedgeBrush(id, local, half, defaultBrushTexture)
	case brushKindCylinder:
		b = newCylinderBrush(id, local, half, brushCylinderSides, defaultBrushTexture)
	default:
		// A box sits on the click rather than straddling it: the click landed
		// on a surface, and a block half-buried in the floor is never what was
		// meant.
		local[1] += half.Y()
		b = world.NewBoxBrush(id, local, half, defaultBrushTexture)
	}
	appendBrush(ch, b)
	selectNewestBrush(ch)
	editorSetStatus("Added %s brush %s", kind, id)
}

// brushKind* are the primitives the stamp offers.
const (
	brushKindBox      = "box"
	brushKindWedge    = "wedge"
	brushKindCylinder = "cylinder"
)

// brushKinds is the stamp menu's order, which is also roughly how often each
// is reached for.
var brushKinds = []string{brushKindBox, brushKindWedge, brushKindCylinder}

// editorHollowSelectedBrush replaces the brush the selection names with the six
// brushes of a hollow room filling its bounds — the fast way to turn a blocked-
// out solid into somewhere to stand.
//
// It works from the brush's axis-aligned bounds rather than its actual shape,
// because that is what NewHollowRoom can express. Hollowing a wedge therefore
// gives a box-shaped room, which is honest: there is no six-brush shell of an
// arbitrary polyhedron, and pretending otherwise would silently produce
// something other than what was asked for.
func editorHollowSelectedBrush() {
	ci, bi := activeBrushIndices()
	ch, b := editor3DActiveBrush()
	if ch == nil || b == nil || bi < 0 {
		return
	}
	lo, hi := b.LocalAABB()
	if hi.X()-lo.X() <= 2*defaultBrushWallThickness ||
		hi.Y()-lo.Y() <= 2*defaultBrushWallThickness ||
		hi.Z()-lo.Z() <= 2*defaultBrushWallThickness {
		editorSetStatus("%s is too small to hollow", b.ID)
		return
	}
	// The shell goes *inside* the original bounds, so hollowing does not grow
	// the solid the author placed: the outer surface stays where they put it.
	t := defaultBrushWallThickness
	inner := [2]mgl32.Vec3{
		{lo.X() + t, lo.Y() + t, lo.Z() + t},
		{hi.X() - t, hi.Y() - t, hi.Z() - t},
	}
	texture := defaultBrushTexture
	if len(b.Faces) > 0 && b.Faces[0].Texture != "" {
		texture = b.Faces[0].Texture
	}
	id := b.ID

	EditorPushUndo()
	room := world.NewHollowRoom(id, inner[0], inner[1], t, texture)
	b.Release()
	ch.Brushes = append(ch.Brushes[:bi], ch.Brushes[bi+1:]...)
	for i := range room {
		room[i].ID = world.UniqueBrushID(ch, room[i].ID)
		appendBrush(ch, room[i])
	}
	clearFaceSelection()
	clearVertexSelection()
	Editor.ActiveChunkIdx = ci
	editorSetStatus("Hollowed %s into %d brushes", id, len(room))
}

// editorDuplicateSelectedBrush copies the selected brush in place. The copy
// lands selected and exactly on top of the original, ready to be dragged off
// it — the same convention duplicating a chunk follows.
func editorDuplicateSelectedBrush() {
	ch, b := editor3DActiveBrush()
	if ch == nil || b == nil {
		return
	}
	EditorPushUndo()
	clone := world.CloneBrush(b, "_copy")
	clone.ID = world.UniqueBrushID(ch, clone.ID)
	appendBrush(ch, clone)
	selectNewestBrush(ch)
	editorSetStatus("Duplicated %s as %s", b.ID, clone.ID)
}

// appendBrush adds b to ch, uploads its geometry and rolls the chunk's bounds
// up around it. Every path that creates a brush goes through here so none of
// them can forget the upload — an un-Loaded brush is invisible and unclickable,
// which reads as "the tool did nothing".
func appendBrush(ch *world.Chunk, b world.Brush) {
	ch.Brushes = append(ch.Brushes, b)
	_ = ch.Brushes[len(ch.Brushes)-1].Load()
	ch.RecomputeBounds()
}

// selectNewestBrush selects the last brush of ch, by way of its first face, so
// the properties panel opens on something and the gizmo has a handle.
func selectNewestBrush(ch *world.Chunk) {
	bi := len(ch.Brushes) - 1
	if bi < 0 || len(ch.Brushes[bi].Faces) == 0 {
		return
	}
	setFaceSelection([]faceSel{brushFaceSel(Editor.ActiveChunkIdx, bi, 0)})
	Editor.PanelFocus = panelFocusFace
}

// ---------- the primitives ----------

// newWedgeBrush is a box with its +Y+Z edge collapsed onto +Y: a right prism
// with a triangular cross-section, sloping up from -Z to +Z. The ramp, and the
// roof pitch.
//
// Six corners rather than a box's eight, so the two end faces are triangles and
// the slope is a single quad. Winding is CCW seen from outside, as every brush
// face must be (world/brush.go).
func newWedgeBrush(id string, center, half mgl32.Vec3, texture string) world.Brush {
	cx, cy, cz := center.X(), center.Y(), center.Z()
	hx, hy, hz := half.X(), half.Y(), half.Z()
	// Bottom rectangle 0..3, top edge 4..5 along +Z.
	verts := []mgl32.Vec3{
		{cx - hx, cy - hy, cz - hz}, // 0
		{cx + hx, cy - hy, cz - hz}, // 1
		{cx + hx, cy - hy, cz + hz}, // 2
		{cx - hx, cy - hy, cz + hz}, // 3
		{cx - hx, cy + hy, cz + hz}, // 4
		{cx + hx, cy + hy, cz + hz}, // 5
	}
	faces := []world.BrushFace{
		world.NewBrushFace([]int{0, 1, 2, 3}, texture), // -Y, the base
		world.NewBrushFace([]int{3, 2, 5, 4}, texture), // +Z, the tall back
		world.NewBrushFace([]int{0, 4, 5, 1}, texture), // the slope
		world.NewBrushFace([]int{0, 3, 4}, texture),    // -X end
		world.NewBrushFace([]int{1, 5, 2}, texture),    // +X end
	}
	return world.Brush{ID: id, Verts: verts, Faces: faces, Solid: true}
}

// newCylinderBrush is a right prism on a regular n-gon in the XZ plane: a
// column, a pillar, a round tower.
//
// It is a prism rather than a true cylinder because a brush is a polyhedron —
// there is no curved face to author. sides is what trades roundness against
// the number of hull planes collision and the compiler have to carry.
func newCylinderBrush(id string, center, half mgl32.Vec3, sides int, texture string) world.Brush {
	if sides < 3 {
		sides = 3
	}
	cx, cy, cz := center.X(), center.Y(), center.Z()
	rx, hy, rz := half.X(), half.Y(), half.Z()

	// Bottom ring 0..n-1, top ring n..2n-1, both wound CCW seen from above.
	verts := make([]mgl32.Vec3, 0, sides*2)
	for i := 0; i < sides; i++ {
		a := 2 * math.Pi * float64(i) / float64(sides)
		x := cx + rx*float32(math.Cos(a))
		z := cz + rz*float32(math.Sin(a))
		verts = append(verts, mgl32.Vec3{x, cy - hy, z})
	}
	for i := 0; i < sides; i++ {
		v := verts[i]
		verts = append(verts, mgl32.Vec3{v.X(), cy + hy, v.Z()})
	}

	faces := make([]world.BrushFace, 0, sides+2)
	// The caps. The bottom ring is CCW from above, so seen from *below* — from
	// outside the solid — it has to be reversed; the top ring is already right.
	bottom := make([]int, sides)
	top := make([]int, sides)
	for i := 0; i < sides; i++ {
		bottom[i] = sides - 1 - i
		top[i] = sides + i
	}
	faces = append(faces, world.NewBrushFace(bottom, texture))
	faces = append(faces, world.NewBrushFace(top, texture))
	for i := 0; i < sides; i++ {
		j := (i + 1) % sides
		faces = append(faces, world.NewBrushFace([]int{i, j, sides + j, sides + i}, texture))
	}
	return world.Brush{ID: id, Verts: verts, Faces: faces, Solid: true}
}

// ---------- convexity ----------

// warnIfSelectionBrokeConvexity checks every brush the current selection can
// have moved and says so in the status line when one is no longer convex.
//
// It reports rather than refuses. Refusing would mean rejecting the *first*
// step of a two-step edit that ends up convex — pulling one corner out and then
// its neighbour after it — and an editor that will not let you pass through an
// invalid intermediate state is an editor you cannot shape anything in. The
// warning is there so the author knows before they save; the compile pipeline
// is where it becomes an error.
func warnIfSelectionBrokeConvexity() {
	bad := ""
	n := 0
	for _, b := range selectionBrushes() {
		if b.IsConvex() {
			continue
		}
		n++
		if bad == "" {
			bad = b.ID
		}
	}
	switch {
	case n == 1:
		editorSetStatus("Warning: %s is no longer convex", bad)
	case n > 1:
		editorSetStatus("Warning: %s and %d other brush(es) are no longer convex", bad, n-1)
	}
}

// selectionBrushes lists the distinct brushes the current face and vertex
// selections name, in a stable order.
func selectionBrushes() []*world.Brush {
	seen := map[[2]int]struct{}{}
	var out []*world.Brush
	add := func(key [2]int, b *world.Brush) {
		if b == nil {
			return
		}
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, b)
	}
	for _, sel := range Editor.SelectedFaces {
		_, b := faceSelBrush(sel)
		add([2]int{sel.chunkIdx, sel.brushIdx}, b)
	}
	for _, sel := range Editor.SelectedVertices {
		_, b := vertexSelBrush(sel)
		add([2]int{sel.chunkIdx, sel.brushIdx}, b)
	}
	return out
}
