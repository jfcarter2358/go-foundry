package editor

// editor_seal.go — the level's sealing box (world/seal.go) in the editor:
// selecting it, sizing it, fitting it, and its properties.
//
// It is drawn as a wireframe and nothing else, and only here — in play it does
// not exist. Move and scale act on it as they act on a volume, scale driving
// its half-extents. It has no rotation (the compiler's slabs are axis-aligned)
// and cannot be deleted: every level has one, and a level without one would
// only get a new one fitted the next time it loaded. Switching it off is what
// its Properties checkbox is for.

import (
	"math"

	"foundry/internal/compile"
	"foundry/internal/gizmo"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

var (
	sealBoxColor    = mgl32.Vec3{0.25, 0.6, 0.65}
	sealBoxOffColor = mgl32.Vec3{0.35, 0.35, 0.35}
)

// ---------- selection ----------

// activeSeal is the level's seal when it is the selection, or nil.
func activeSeal() *world.Seal {
	if !Editor.SealSelected || Editor.level == nil {
		return nil
	}
	return Editor.level.Seal
}

// activeGizmoSeal is activeSeal, gated on the gizmo being attached to it.
func activeGizmoSeal() *world.Seal {
	if Editor.GizmoKind != gizmoKindSeal {
		return nil
	}
	return activeSeal()
}

// editorSelectSeal makes the seal the selection, which gives it the move and
// scale gizmos and opens its properties.
func editorSelectSeal() {
	if Editor.level == nil || Editor.level.Seal == nil {
		return
	}
	editorClearObjectSelections()
	Editor.SealSelected = true
	Editor.GizmoKind = gizmoKindSeal
	Editor.PanelFocus = panelFocusSeal
}

// editorClearSealSelection drops the seal from the selection, and the gizmo
// with it.
func editorClearSealSelection() {
	Editor.SealSelected = false
	if Editor.GizmoKind == gizmoKindSeal {
		Editor.GizmoKind = gizmoKindNone
	}
}

// pickSealRayT is where a picking ray enters the seal box, or MaxFloat32.
//
// Only from outside the box. Inside it — which is where an author almost always
// is — the box surrounds every click, and a hit on its far side would select it
// in place of whatever the click was aimed at. pick3D goes further and only
// considers the box when a click hit nothing else, since from outside it sits in
// front of the whole level.
func pickSealRayT(origin, dir mgl32.Vec3, s *world.Seal) float32 {
	miss := float32(math.MaxFloat32)
	if s == nil || s.Contains(origin) {
		return miss
	}
	lo, hi := s.Bounds()
	near, far := float32(-math.MaxFloat32), float32(math.MaxFloat32)
	for k := 0; k < 3; k++ {
		if math.Abs(float64(dir[k])) < 1e-9 {
			if origin[k] < lo[k] || origin[k] > hi[k] {
				return miss
			}
			continue
		}
		t1, t2 := (lo[k]-origin[k])/dir[k], (hi[k]-origin[k])/dir[k]
		if t1 > t2 {
			t1, t2 = t2, t1
		}
		near, far = max(near, t1), min(far, t2)
		if near > far {
			return miss
		}
	}
	if near <= 0 {
		return miss
	}
	return near
}

// ---------- editing ----------

// editorFitSealToLevel refits the seal around what the level holds now,
// keeping whether it is switched on.
func editorFitSealToLevel() {
	lvl := Editor.level
	if lvl == nil {
		return
	}
	EditorPushUndo()
	fitted := compile.FitSeal(lvl)
	if lvl.Seal != nil {
		fitted.Disabled = lvl.Seal.Disabled
	}
	lvl.Seal = fitted
}

// renderSealBox draws the seal's wireframe: the selection colour when it is
// selected, grey when it is switched off.
func renderSealBox(lvl *world.Level, selected mgl32.Vec3) {
	s := lvl.Seal
	if s == nil {
		return
	}
	col := sealBoxColor
	if s.Disabled {
		col = sealBoxOffColor
	}
	if Editor.SealSelected {
		col = selected
	}
	lo, hi := s.Bounds()
	gizmo.GizmoAABB(lo, hi, col)
}

// ---------- panels ----------

// editorExplorerSealRow is the seal's row in the Explorer, which is how it is
// reached from inside the level, where it cannot be clicked.
func editorExplorerSealRow(lvl *world.Level) {
	if lvl.Seal == nil {
		return
	}
	label := "Seal Box"
	if lvl.Seal.Disabled {
		label += "  (off)"
	}
	if editorExplorerLeaf(label+"##sealrow", Editor.SealSelected, panelFocusSeal) {
		editorSelectSeal()
	}
	editorExplorerMenu("sealctx", func() {
		if imgui.MenuItem("Fit To Level") {
			editorFitSealToLevel()
		}
	})
}

// editorSealSection draws the seal's properties.
func editorSealSection(lvl *world.Level) {
	s := lvl.Seal
	if s == nil || !Editor.SealSelected {
		return
	}

	imgui.Separator()
	editorFocusOpen(panelFocusSeal)
	if !imgui.CollapsingHeaderV("Seal Box##sealhdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusSeal)
	editorTextMuted("Invisible in play. Compiling treats everything inside it as the level.")

	on := !s.Disabled
	if imgui.Checkbox("Seals the level##sealon", &on) {
		EditorPushUndo()
		s.Disabled = !on
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Off: the level has to seal itself with its own walls, and\n" +
			"compiling reports every gap in them as a leak.")
	}

	pos := [3]float32{s.Position.X(), s.Position.Y(), s.Position.Z()}
	if editorDragFloat3("Position##sealpos", &pos, 0, 0, "%.2f") {
		s.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
	}
	half := [3]float32{s.Half.X(), s.Half.Y(), s.Half.Z()}
	if editorDragFloat3Step("Half-extents##sealhalf", &half, world.SealMinHalf, 1e5, "%.2f", 0.5) {
		s.Half = world.ClampSealHalf(mgl32.Vec3{half[0], half[1], half[2]})
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Half the box's size on each axis. The scale gizmo drags these too.")
	}

	if imgui.Button("Fit To Level##seal") {
		editorFitSealToLevel()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Refit the box around every brush that compiles, the spawn and\nthe entities, with a margin.")
	}
}
