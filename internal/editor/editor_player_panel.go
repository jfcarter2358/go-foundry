package editor

// editor_player_panel.go — the level's player tuning, in the properties panel.
//
// Level-scoped and selection-free, for the environment panel's reason: the
// player is engine-owned, so there is nothing in the viewport to click that
// would open this. It is drawn beside Sky and Fog because it answers the same
// shape of question — something that is true of the whole level and belongs to
// no object in it.
//
// Only one setting so far, and it is the one the spawn marker cannot express:
// how tall a riser the player walks up rather than being stopped by. See
// world.PlayerSettings and, for what actually enforces it, ResolveStepEntry in
// the collision package.

import (
	"fmt"
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorPlayerSection draws the level's `player:` block.
func editorPlayerSection(lvl *world.Level) {
	if lvl == nil {
		return
	}
	imgui.Separator()
	if !imgui.CollapsingHeader("Player##player") {
		return
	}
	editorPropGroup("Movement##playermovegrp", 0, func() { editorPlayerStepHeight(lvl) })
}

// editorPlayerStepHeight draws the step-height field, and the checkbox that
// decides whether the level says anything about it at all.
//
// The checkbox is the same gesture the Sky and Fog groups open with: unticked
// writes no value, which is what keeps a level that never cared about this
// following the engine's default rather than freezing today's number into the
// file. Zero is not a separate authored state — a body that could not step at
// all would be stopped by the seam between two floors that differ by a rounding
// error — so unticking is a return to the default, not a way to say "none".
func editorPlayerStepHeight(lvl *world.Level) {
	def := world.DefaultStepHeight()

	has := lvl.Player.StepHeight > 0
	if imgui.Checkbox("This level sets a step height##stepon", &has) {
		EditorPushUndo()
		if has {
			lvl.Player.StepHeight = world.SuggestedStepHeight
			editorSetStatus("Player step height set to %.2f", lvl.Player.StepHeight)
		} else {
			lvl.Player.StepHeight = 0
			editorSetStatus("Player step height back to the default (%.2f)", def)
		}
	}

	if lvl.Player.StepHeight <= 0 {
		editorTextMutedWrapped(fmt.Sprintf(
			"Following the default: the player's own height, %.2f units. Anything taller than that is a wall it is stopped by; anything shorter is a step it walks up.", def))
		return
	}

	editorDragFloatStep("Step height##stepheight",
		&lvl.Player.StepHeight, 0.01, world.MaxStepHeight, "%.2f", 0.05)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The tallest riser the player walks up rather than being\n" +
			"stopped by it. Jumping still reaches higher ledges: the\n" +
			"allowance is measured from the feet as well as from the\n" +
			"floor being left.")
	}
	editorTextMutedWrapped(fmt.Sprintf(
		"World units, measured from the floor the player is leaving. The engine's default without this block is %.2f, the player's own height.", def))
}
