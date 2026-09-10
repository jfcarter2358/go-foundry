package editor

// editor_sprite.go — placing, selecting and picking sprites.
//
// The volume workflow, minus the shape: a sprite has no definition to
// instantiate and no models to reload, so placement is "make one here", and the
// property panel is a transform, a texture, and the tags/attributes/script every
// scriptable object shares.
//
// The selection model is the same one for the same reason: an index rather than
// a pointer, because undo swaps the whole level out from under the editor and a
// pointer into the old one would outlive it.
//
// Picking is where a sprite differs from everything else in the viewport. It has
// no solid to test a ray against — it is a quad that faces wherever the camera
// happens to be — so the test is done against the quad as it is currently
// oriented, which is the only thing the author can actually see to click on.

import (
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/world"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// ---------- selection ----------

// activeSprite returns the selected sprite, or nil.
func activeSprite() *world.Sprite {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveSpriteIdx < 0 || Editor.ActiveSpriteIdx >= len(lvl.Sprites) {
		return nil
	}
	return &lvl.Sprites[Editor.ActiveSpriteIdx]
}

// editorSelectSprite makes the sprite at idx the selection, giving it the gizmo
// and opening its properties section.
func editorSelectSprite(idx int) {
	lvl := Editor.level
	if lvl == nil || idx < 0 || idx >= len(lvl.Sprites) {
		return
	}
	editorClearObjectSelections()
	Editor.ActiveSpriteIdx = idx
	Editor.GizmoKind = gizmoKindSprite
	Editor.PanelFocus = panelFocusSprite
}

// editorClearSpriteSelection drops the sprite from the selection, taking the
// gizmo with it so it cannot keep dragging something no longer selected.
func editorClearSpriteSelection() {
	Editor.ActiveSpriteIdx = -1
	if Editor.GizmoKind == gizmoKindSprite {
		Editor.GizmoKind = gizmoKindNone
	}
}

// activeGizmoSprite returns the sprite the gizmo is attached to, or nil. The
// gizmo kind is checked as well as the index, so a stale selection left by
// another tool cannot silently be dragged.
func activeGizmoSprite() *world.Sprite {
	if Editor.GizmoKind != gizmoKindSprite {
		return nil
	}
	return activeSprite()
}

// ---------- placement ----------

// editorArmSpritePlacement makes the next left-click in the viewport drop a
// sprite.
func editorArmSpritePlacement() {
	Editor.SpritePlacementMode = true
	Editor.VolumePlacementMode = false
	Editor.EntityPlacementMode = false
	Editor.SpawnPlacementMode = false
}

// placeSpriteAt creates a sprite at pos, selects it, and leaves placement mode.
// Reports whether one was placed.
func placeSpriteAt(pos mgl32.Vec3) bool {
	Editor.SpritePlacementMode = false
	lvl := Editor.level
	if lvl == nil {
		return false
	}

	EditorPushUndo()
	base := Editor.NewSpriteID
	if base == "" {
		base = "sprite"
	}
	s := world.NewSprite(world.UniqueSpriteID(lvl, base), pos)
	if Editor.NewSpriteTexture != "" {
		s.Texture = Editor.NewSpriteTexture
	}
	// A sprite standing on the ground reads better placed on it than centred
	// through it, and the click that placed it landed on a surface.
	s.Position = s.Position.Add(mgl32.Vec3{0, s.Height * 0.5, 0})
	if err := s.EnsureSpriteTexture(); err != nil {
		editorSetStatus("Sprite %s: %v", s.ID, err)
	}
	lvl.Sprites = append(lvl.Sprites, s)
	editorSelectSprite(len(lvl.Sprites) - 1)
	editorSetStatus("Placed sprite %q", s.ID)
	return true
}

// ---------- duplicate / delete ----------

// duplicateActiveSprite copies the selected sprite beside itself and selects the
// copy. Offset rather than coincident, for the reason a duplicated volume is:
// two quads in exactly the same place cannot be told apart or clicked apart.
func duplicateActiveSprite() {
	lvl := Editor.level
	src := activeSprite()
	if lvl == nil || src == nil {
		return
	}
	EditorPushUndo()
	cp := *src
	cp.ScriptData = world.CloneScriptData(src.ScriptData)
	cp.ID = world.UniqueSpriteID(lvl, src.ID)
	cp.Position = src.Position.Add(mgl32.Vec3{src.Width, 0, 0})
	lvl.Sprites = append(lvl.Sprites, cp)
	editorSelectSprite(len(lvl.Sprites) - 1)
	editorSetStatus("Duplicated sprite %q", cp.ID)
}

// deleteActiveSprite removes the selected sprite.
func deleteActiveSprite() {
	lvl := Editor.level
	idx := Editor.ActiveSpriteIdx
	if lvl == nil || idx < 0 || idx >= len(lvl.Sprites) {
		return
	}
	EditorPushUndo()
	id := lvl.Sprites[idx].ID
	// No GPU handles to release: the sprite pass owns one shared quad and one
	// shared instance buffer, so a sprite is pure data.
	lvl.Sprites = append(lvl.Sprites[:idx], lvl.Sprites[idx+1:]...)
	editorClearSpriteSelection()
	editorSetStatus("Deleted sprite %q", id)
}

// ---------- picking ----------

// spriteQuadCorners returns the sprite's four world-space corners as it is
// currently oriented, in the winding the picker walks: bottom-left,
// bottom-right, top-right, top-left.
//
// This has to agree with sprite_common.glsl's spriteBasis, or a sprite would be
// clicked somewhere other than where it is drawn. The two are separate
// implementations of one rule, which is a real duplication — but the shader
// cannot be asked where it put something, and reading the framebuffer back to
// find out would cost a stall per click.
func spriteQuadCorners(lvl *world.Level, s *world.Sprite, camPos mgl32.Vec3) [4]mgl32.Vec3 {
	pos := s.WorldPosition(lvl)
	var right, up mgl32.Vec3

	switch s.Billboard {
	case gfx.BillboardFixed:
		m := world.EulerMatrixYXZ(s.Rotation)
		r := m.Mul4x1(mgl32.Vec4{1, 0, 0, 0})
		u := m.Mul4x1(mgl32.Vec4{0, 1, 0, 0})
		right = mgl32.Vec3{r.X(), r.Y(), r.Z()}
		up = mgl32.Vec3{u.X(), u.Y(), u.Z()}
	case gfx.BillboardYAxis:
		up = mgl32.Vec3{0, 1, 0}
		toView := camPos.Sub(pos)
		level := toView.Sub(up.Mul(toView.Dot(up)))
		if level.LenSqr() < 1e-8 {
			right = mgl32.Vec3{1, 0, 0}
		} else {
			right = up.Cross(level.Normalize()).Normalize()
		}
	default:
		toView := camPos.Sub(pos)
		if toView.LenSqr() < 1e-8 {
			return [4]mgl32.Vec3{pos, pos, pos, pos}
		}
		toView = toView.Normalize()
		ref := mgl32.Vec3{0, 1, 0}
		if float32(math.Abs(float64(toView.Y()))) > 0.999 {
			ref = mgl32.Vec3{0, 0, 1}
		}
		right = ref.Cross(toView).Normalize()
		up = toView.Cross(right)
	}

	// Roll, applied after the basis, exactly as the shader does it.
	if s.Rotation.Z() != 0 && s.Billboard != gfx.BillboardFixed {
		c := float32(math.Cos(float64(s.Rotation.Z())))
		sn := float32(math.Sin(float64(s.Rotation.Z())))
		r := right.Mul(c).Add(up.Mul(sn))
		up = up.Mul(c).Sub(right.Mul(sn))
		right = r
	}

	size := s.DrawnSize()
	hx := right.Mul(size.X() * 0.5)
	hy := up.Mul(size.Y() * 0.5)
	return [4]mgl32.Vec3{
		pos.Sub(hx).Sub(hy),
		pos.Add(hx).Sub(hy),
		pos.Add(hx).Add(hy),
		pos.Sub(hx).Add(hy),
	}
}

// pickSpriteRayT returns the distance at which a picking ray crosses sprite s,
// or +Inf when it misses — what puts a sprite into the 3D viewport's
// closest-hit race alongside the entities, volumes and sector faces.
//
// Visible is ignored, for the reason pickVolumeAt ignores Enabled: a script that
// hid a sprite must not thereby make it unselectable in the editor.
func pickSpriteRayT(origin, dir mgl32.Vec3, lvl *world.Level, s *world.Sprite, camPos mgl32.Vec3) float32 {
	if !s.HasSize() {
		return float32(math.MaxFloat32)
	}
	c := spriteQuadCorners(lvl, s, camPos)
	// Two triangles, because the quad is a strip and a ray does not care.
	if t, ok := rayTriangle(origin, dir, c[0], c[1], c[2]); ok {
		return t
	}
	if t, ok := rayTriangle(origin, dir, c[0], c[2], c[3]); ok {
		return t
	}
	return float32(math.MaxFloat32)
}

// rayTriangle is Moller-Trumbore, double-sided: a sprite has no meaningful back
// face, and being unable to click one from behind would be a bug rather than a
// feature.
func rayTriangle(origin, dir, a, b, c mgl32.Vec3) (float32, bool) {
	const eps = 1e-7
	e1 := b.Sub(a)
	e2 := c.Sub(a)
	p := dir.Cross(e2)
	det := e1.Dot(p)
	if det > -eps && det < eps {
		return 0, false
	}
	inv := 1 / det
	tv := origin.Sub(a)
	u := tv.Dot(p) * inv
	if u < 0 || u > 1 {
		return 0, false
	}
	q := tv.Cross(e1)
	v := dir.Dot(q) * inv
	if v < 0 || u+v > 1 {
		return 0, false
	}
	t := e2.Dot(q) * inv
	if t <= 0 {
		return 0, false
	}
	return t, true
}

// ---------- menu helpers ----------

// editor3DMoveSpriteTo puts the selected sprite at pos — the context menu's
// "move here", matching editor3DMoveVolumeTo.
func editor3DMoveSpriteTo(pos mgl32.Vec3) {
	s := activeSprite()
	if s == nil {
		return
	}
	EditorPushUndo()
	s.Position = pos
}

// editor3DAddSpriteAt drops a sprite at pos, the way the panel's placement
// button does — the menu offers "add one here" rather than arming a mode, since
// it was opened on the very point the sprite would go.
func editor3DAddSpriteAt(pos mgl32.Vec3) { placeSpriteAt(pos) }

// ---------- selection mark ----------

// spriteSelColor is the selected sprite's outline colour — the same yellow a
// selected vertex handle and a selected volume cage use.
var spriteSelColor = mgl32.Vec3{1, 1, 0}

// drawSelectedSpriteOutline traces the selected sprite's quad through the gizmo
// line buffer, which draws with depth testing off — so the mark survives the
// sprite being behind a wall, as a selected face's outline does.
//
// It also draws the quad's normal as a short stub, because a billboarded sprite
// gives no other clue which way it is facing, and BillboardFixed is authored
// entirely by that facing.
func drawSelectedSpriteOutline(camPos mgl32.Vec3) {
	s := activeSprite()
	if s == nil || !s.HasSize() {
		return
	}
	c := spriteQuadCorners(Editor.level, s, camPos)
	for i := 0; i < 4; i++ {
		gizmo.GizmoLine(c[i], c[(i+1)%4], spriteSelColor)
	}
	centre := c[0].Add(c[2]).Mul(0.5)
	n := c[1].Sub(c[0]).Cross(c[3].Sub(c[0]))
	if n.LenSqr() > 1e-8 {
		size := s.DrawnSize()
		stub := size.X()
		if size.Y() < stub {
			stub = size.Y()
		}
		gizmo.GizmoLine(centre, centre.Add(n.Normalize().Mul(stub*0.5)), spriteSelColor)
	}
}
