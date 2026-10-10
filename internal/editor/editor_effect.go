package editor

// editor_effect.go — placing, selecting and picking procedural effects.
//
// The sound workflow again: an effect is a point with nothing solid to click,
// so the picker tests a camera-facing disc at its origin and the viewport
// draws that disc — a burst rather than a ring and cross, so an explosion is
// told apart from an emitter at a glance.
//
// What this adds is the reach sphere on the selected one. An explosion's size
// is the one number that decides whether it fits the room it goes off in, and
// it is only visible for the second or so the fireball is at full size; the
// outline shows it all the time.

import (
	"math"

	"foundry/internal/gizmo"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// effectMarkerRadius is the clickable disc's size in world units, matching the
// sound's and the emitter's so the three feel the same to click.
const effectMarkerRadius = 0.25

var (
	effectMarkerColor = mgl32.Vec3{1, 0.55, 0.15}
	effectSelColor    = mgl32.Vec3{1, 1, 0}
	effectReachColor  = mgl32.Vec3{0.85, 0.4, 0.12}
)

// ---------- selection ----------

// activeEffect returns the selected effect, or nil.
func activeEffect() *world.Effect {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveEffectIdx < 0 || Editor.ActiveEffectIdx >= len(lvl.Effects) {
		return nil
	}
	return &lvl.Effects[Editor.ActiveEffectIdx]
}

// editorSelectEffect makes the effect at idx the selection.
func editorSelectEffect(idx int) {
	lvl := Editor.level
	if lvl == nil || idx < 0 || idx >= len(lvl.Effects) {
		return
	}
	editorClearObjectSelections()
	Editor.ActiveEffectIdx = idx
	Editor.GizmoKind = gizmoKindEffect
	Editor.PanelFocus = panelFocusEffect
}

// editorClearEffectSelection drops the effect from the selection, taking the
// gizmo with it.
func editorClearEffectSelection() {
	Editor.ActiveEffectIdx = -1
	if Editor.GizmoKind == gizmoKindEffect {
		Editor.GizmoKind = gizmoKindNone
	}
}

// activeGizmoEffect returns the effect the gizmo is attached to, or nil.
func activeGizmoEffect() *world.Effect {
	if Editor.GizmoKind != gizmoKindEffect {
		return nil
	}
	return activeEffect()
}

// ---------- placement ----------

// editorArmEffectPlacement makes the next left-click drop an effect.
func editorArmEffectPlacement() {
	editorDisarmBrush()
	editorDisarmBrushPlacement()
	Editor.EffectPlacementMode = true
	Editor.TreePlacementMode = false
	Editor.SoundPlacementMode = false
	Editor.EmitterPlacementMode = false
	Editor.SpritePlacementMode = false
	Editor.DecalPlacementMode = false
	Editor.VolumePlacementMode = false
	Editor.EntityPlacementMode = false
	Editor.PrefabPlacementMode = false
	Editor.SpawnPlacementMode = false
	Editor.WaypointPlacementMode = false
}

// placeEffectAt creates an effect at pos, selects it, and leaves placement
// mode.
func placeEffectAt(pos mgl32.Vec3) bool {
	Editor.EffectPlacementMode = false
	Editor.TreePlacementMode = false
	lvl := Editor.level
	if lvl == nil {
		return false
	}

	EditorPushUndo()
	kind := world.EffectKind(Editor.NewEffectKind)
	base := Editor.NewEffectID
	if base == "" {
		base = string(kind)
		if base == "" {
			base = "effect"
		}
	}
	e := world.NewEffect(world.UniqueEffectID(lvl, base), kind, pos)
	// An explosion goes off around the point clicked, so a click on the floor
	// would bury half of it. Lifted to sit on the surface instead — which is
	// also where the ground it scorches would be.
	if e.Kind == world.EffectExplosion {
		e.Position = pos.Add(mgl32.Vec3{0, e.Explosion.Radius * 0.5, 0})
	}
	lvl.Effects = append(lvl.Effects, e)
	editorSelectEffect(len(lvl.Effects) - 1)
	editorSetStatus("Placed %s %q", e.Kind, e.ID)
	return true
}

// ---------- duplicate / delete ----------

// duplicateActiveEffect copies the selected effect beside itself.
func duplicateActiveEffect() {
	lvl := Editor.level
	src := activeEffect()
	if lvl == nil || src == nil {
		return
	}
	EditorPushUndo()
	cp := *src
	cp.ScriptData = world.CloneScriptData(src.ScriptData)
	cp.ID = world.UniqueEffectID(lvl, src.ID)
	cp.Position = src.Position.Add(mgl32.Vec3{src.Explosion.Radius * 2, 0, 0})
	cp.OneShot = false
	lvl.Effects = append(lvl.Effects, cp)
	editorSelectEffect(len(lvl.Effects) - 1)
	editorSetStatus("Duplicated effect %q", cp.ID)
}

// deleteActiveEffect removes the selected effect.
func deleteActiveEffect() {
	lvl := Editor.level
	idx := Editor.ActiveEffectIdx
	if lvl == nil || idx < 0 || idx >= len(lvl.Effects) {
		return
	}
	EditorPushUndo()
	id := lvl.Effects[idx].ID
	lvl.Effects = append(lvl.Effects[:idx], lvl.Effects[idx+1:]...)
	editorClearEffectSelection()
	editorSetStatus("Deleted effect %q", id)
}

// detonateActiveEffect sets the selected effect off from the start. Not an
// undoable edit: it changes where the effect is in its run, which is not saved.
func detonateActiveEffect() {
	e := activeEffect()
	if e == nil {
		return
	}
	e.Detonate()
	editorSetStatus("Detonated %q", e.ID)
}

// ---------- picking ----------

// pickEffectRayT returns the distance at which a picking ray crosses the
// effect's marker disc, or +Inf.
func pickEffectRayT(origin, dir mgl32.Vec3, lvl *world.Level, e *world.Effect, camPos mgl32.Vec3) float32 {
	return pickMarkerDiscRayT(origin, dir, e.WorldPosition(lvl), camPos, effectMarkerRadius)
}

// ---------- drawing ----------

// RenderEffectMarkers draws every effect's marker, and the selected one's
// reach.
func RenderEffectMarkers(lvl *world.Level, camPos mgl32.Vec3) {
	if lvl == nil {
		return
	}
	for i := range lvl.Effects {
		e := &lvl.Effects[i]
		col := effectMarkerColor
		if i == Editor.ActiveEffectIdx {
			col = effectSelColor
		}
		if !e.Enabled {
			// Dimmed rather than hidden, on the emitter's rule.
			col = col.Mul(0.45)
		}
		p := e.WorldPosition(lvl)
		drawEffectMarker(p, camPos, col)
		if i == Editor.ActiveEffectIdx && e.Kind == world.EffectExplosion {
			drawEffectReach(p, e.Explosion.Radius, effectReachColor)
		}
	}
}

// drawEffectMarker draws the clickable disc as a burst: a ring with eight
// spokes running out past it.
func drawEffectMarker(c, camPos, col mgl32.Vec3) {
	toCam := camPos.Sub(c)
	if toCam.LenSqr() < 1e-8 {
		return
	}
	n := toCam.Normalize()
	ref := mgl32.Vec3{0, 1, 0}
	if absF32(n.Y()) > 0.999 {
		ref = mgl32.Vec3{1, 0, 0}
	}
	right := ref.Cross(n).Normalize()
	up := n.Cross(right)

	const seg = 16
	at := func(a float64, r float32) mgl32.Vec3 {
		return c.Add(right.Mul(r * float32(math.Cos(a)))).Add(up.Mul(r * float32(math.Sin(a))))
	}
	inner := float32(effectMarkerRadius * 0.55)
	prev := at(0, inner)
	for i := 1; i <= seg; i++ {
		p := at(float64(i)/seg*2*math.Pi, inner)
		gizmo.GizmoLine(prev, p, col)
		prev = p
	}
	for i := 0; i < 8; i++ {
		a := float64(i) / 8 * 2 * math.Pi
		outer := float32(effectMarkerRadius)
		if i%2 == 1 {
			outer *= 0.75
		}
		gizmo.GizmoLine(at(a, inner), at(a, outer), col)
	}
}

// drawEffectReach outlines a sphere of radius r as its three great circles.
func drawEffectReach(c mgl32.Vec3, r float32, col mgl32.Vec3) {
	if r <= 0 {
		return
	}
	const seg = 48
	for axis := 0; axis < 3; axis++ {
		point := func(a float64) mgl32.Vec3 {
			u, v := r*float32(math.Cos(a)), r*float32(math.Sin(a))
			switch axis {
			case 0:
				return c.Add(mgl32.Vec3{u, 0, v})
			case 1:
				return c.Add(mgl32.Vec3{u, v, 0})
			}
			return c.Add(mgl32.Vec3{0, u, v})
		}
		prev := point(0)
		for i := 1; i <= seg; i++ {
			p := point(float64(i) / seg * 2 * math.Pi)
			gizmo.GizmoLine(prev, p, col)
			prev = p
		}
	}
}
