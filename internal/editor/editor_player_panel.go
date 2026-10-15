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
	"foundry/internal/render"
	"foundry/internal/world"

	"foundry/internal/imguibackend"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorPlayerSection draws the level's `player:` block.
func editorPlayerSection(lvl *world.Level) {
	if lvl == nil {
		render.EditorPreviewActive = false
		return
	}
	imgui.Separator()
	if !imgui.CollapsingHeader("Player##player") {
		render.EditorPreviewActive = false
		return
	}
	editorPropGroup("Movement##playermovegrp", 0, func() {
		editorPlayerStepHeight(lvl)
		imgui.Separator()
		editorPlayerGravity(lvl)
	})

	// The player's behaviour, drawn with the very widgets a placed entity's
	// panel uses. They edit the level's authored copy; world.ApplyPlayerScript
	// hands it to the player as each play session starts.
	editorScriptUI(&lvl.Player.Script, &lvl.Player.ScriptParams, "player", "The player")
	editorAttributesUI(&lvl.Player.Attributes, "player", "Attributes", "the player")

	// render.EditorPreviewActive tracks whether this group is open on THIS
	// frame, not whether it was ever opened: editorPropGroup's body only runs
	// while its own tree node is expanded, so closing the group (or the
	// Player header above it, or leaving this panel's frame out entirely)
	// naturally lets the flag fall back to false again next frame.
	viewmodelOpen := false
	editorPropGroup("Viewmodel##playermodelsgrp", 0, func() {
		viewmodelOpen = true
		editorViewmodelSection(lvl)
	})
	render.EditorPreviewActive = viewmodelOpen

	editorPropGroup("Camera##playercameragrp", 0, func() {
		editorPlayerCamera(lvl)
	})
	editorPropGroup("Body (third person)##playerbodygrp", 0, func() {
		editorPlayerBody(lvl)
	})
}

// ---------- camera ----------

// editorPlayerCamera edits the `player: camera:` block. Every field writes the
// resolved camera back whole, so touching one key leaves the others at their
// defaults — and world.PlayerCameraToRaw writes only what differs from them.
func editorPlayerCamera(lvl *world.Level) {
	c := lvl.Player.Camera.Effective()
	before := c
	third := c.ThirdPerson()
	if imgui.Checkbox("Third person##plcamthird", &third) {
		if third {
			c.Mode = world.CameraThirdPerson
		} else {
			c.Mode = world.CameraFirstPerson
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("First person: the camera is the player's eye and the\n" +
			"viewmodel is drawn in front of it. Third person: a boom\n" +
			"behind and over the shoulder looks at the Body below,\n" +
			"pulled in by walls and eased back out.")
	}
	if !c.ThirdPerson() {
		editorTextMutedWrapped("First person: the camera is the player's eye. The boom settings below apply once third person is ticked.")
	}
	editorDragFloatStep("Distance##plcamdist", &c.Distance, 0, 30, "%.2f", 0.1)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Boom length behind the pivot, in world units, before\nanything pulls it in.")
	}
	editorDragFloatStep("Pivot height##plcamheight", &c.Height, -2, 4, "%.2f", 0.05)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The pivot's height above the body's centre. The\nfirst-person eye is 1.7 above it.")
	}
	editorDragFloatStep("Shoulder##plcamshoulder", &c.Shoulder, -3, 3, "%.2f", 0.05)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Slides the pivot to the camera's right (negative: the\nleft shoulder; 0 centres the body on the reticle).")
	}
	editorDragFloatStep("Pitch min (deg)##plcampmin", &c.PitchMin, -89, 89, "%.0f", 1)
	editorDragFloatStep("Pitch max (deg)##plcampmax", &c.PitchMax, -89, 89, "%.0f", 1)
	editorDragFloatStep("Collision radius##plcamradius", &c.Radius, 0, 2, "%.2f", 0.01)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How far the camera keeps from a wall that pulls it in.")
	}
	editorDragFloatStep("Ease-out rate##plcamsmooth", &c.Smoothing, 0, 30, "%.1f", 0.5)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How fast the boom grows back once a wall stops pulling it\n" +
			"in (fraction of the gap closed per second). Pulling in is\nalways immediate.")
	}
	editorDragFloatStep("Hide body under##plcamhide", &c.HideDistance, 0, 5, "%.2f", 0.05)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The boom length below which the body is hidden, so a camera\n" +
			"squeezed against the player's back does not see inside the head.")
	}
	if c != before {
		EditorPushUndo()
		lvl.Player.Camera = c
	}
}

// ---------- body ----------

// editorPlayerBody edits the `player: body:` block: the third-person body's
// models (the same widgets as a placed entity's, on the loaded body as the
// viewmodel's are on the loaded player) and its turn rate and clips.
func editorPlayerBody(lvl *world.Level) {
	if world.PlayerBodyEntity == nil {
		world.PlayerBodyEntity = world.NewPlayerBody()
	}
	editorTextMutedWrapped("Drawn in the world, with shadows, while the camera is third person. The body's origin is the player's feet, facing -Z: a model facing +Z (the human rig) mounts with rotation y = 3.1416.")
	tmp := &world.Entity{ID: world.PlayerBodyID, Models: world.PlayerBodyEntity.Models}
	editorEntityModels(tmp)
	world.PlayerBodyEntity.Models = tmp.Models
	lvl.Player.Body.Models = playerAuthoredModels(world.PlayerBodyEntity.Models)

	imgui.Spacing()
	turn := lvl.Player.Body.TurnRate
	if turn <= 0 {
		turn = world.DefaultBodyTurnRate
	}
	if editorDragFloatStep("Turn rate (deg/s)##plbodyturn", &turn, 30, 3600, "%.0f", 10) {
		EditorPushUndo()
		lvl.Player.Body.TurnRate = turn
	}

	imgui.Spacing()
	imgui.Text("Locomotion clips")
	editorTextMutedWrapped("Animation names on the body's models, or clip names in the model. Empty follows the human rig's default (shown greyed).")
	l := &lvl.Player.Body.Locomotion
	d := world.DefaultLocomotion()
	before := *l
	clipField := func(label, id string, v *string, def string) {
		imgui.PushItemWidth(-1)
		imgui.Text(label)
		imgui.InputTextWithHint("##plloco"+id, def, v)
		imgui.PopItemWidth()
	}
	clipField("Idle", "idle", &l.Idle, d.Idle)
	clipField("Walk", "walk", &l.Walk, d.Walk)
	clipField("Run", "run", &l.Run, d.Run)
	clipField("Sprint", "sprint", &l.Sprint, d.Sprint)
	clipField("Jump", "jump", &l.Jump, d.Jump)
	clipField("Jump at a run", "jumprun", &l.JumpRun, d.JumpRun)
	clipField("Fall", "fall", &l.Fall, d.Fall)
	clipField("Crouch idle", "cidle", &l.CrouchIdle, d.CrouchIdle)
	clipField("Crouch walk", "cwalk", &l.CrouchWalk, d.CrouchWalk)
	ws, rs := l.Effective().WalkSpeed, l.Effective().RunSpeed
	if editorDragFloatStep("Walk clip speed##pllocows", &ws, 0.1, 20, "%.2f", 0.05) {
		l.WalkSpeed = ws
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The ground speed, units a second, the walk clip was authored\nat; playback is scaled to the real speed so the feet do not skate.")
	}
	if editorDragFloatStep("Run clip speed##pllocors", &rs, 0.1, 30, "%.2f", 0.05) {
		l.RunSpeed = rs
	}
	if *l != before {
		after := *l
		*l = before
		EditorPushUndo()
		*l = after
	}
}

// ---------- viewmodel ----------

// editorViewmodelSection is the Player panel's model-attachment editor: the
// player's own `models:` list by default, or — via the combo — any AI
// entity's, reusing editorEntityModels exactly as a placed entity's own
// properties panel already does when it is clicked in the viewport. This is
// the panel that lets it be reached without hunting for one in the viewport.
func editorViewmodelSection(lvl *world.Level) {
	agents := world.FindAIAgents(lvl)

	if imguibackend.BeginCombo("Edit##viewmodeltarget", editorViewmodelTargetLabel(agents)) {
		if imguibackend.SelectableV("Player", Editor.ViewmodelTarget == "", 0, imgui.Vec2{}) {
			Editor.ViewmodelTarget = ""
		}
		for _, a := range agents {
			if imguibackend.SelectableV(a.ID, Editor.ViewmodelTarget == a.ID, 0, imgui.Vec2{}) {
				Editor.ViewmodelTarget = a.ID
			}
		}
		imguibackend.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Every entity with an AI controller, plus the player.\n" +
			"Picking one here is the same panel that entity's own\n" +
			"properties show when it is clicked in the viewport.")
	}

	if Editor.ViewmodelTarget == "" {
		editorPlayerViewmodel(lvl)
		return
	}
	for _, a := range agents {
		if a.ID == Editor.ViewmodelTarget {
			editorEntityModels(a)
			return
		}
	}
	// The chosen entity is gone — deleted, or an undo/redo swapped the level
	// out from under this selection, which names entities by ID rather than
	// by index for exactly this reason. Fall back rather than draw nothing.
	Editor.ViewmodelTarget = ""
	editorTextMutedWrapped("That entity is no longer in the level; showing the player instead.")
	editorPlayerViewmodel(lvl)
}

// editorViewmodelTargetLabel is the combo's own closed-state label.
func editorViewmodelTargetLabel(agents []*world.Entity) string {
	if Editor.ViewmodelTarget == "" {
		return "Player"
	}
	for _, a := range agents {
		if a.ID == Editor.ViewmodelTarget {
			return a.ID
		}
	}
	return "Player"
}

// editorPlayerViewmodel edits the player's own `models:` list.
//
// world.PlayerEntity.Models is the loaded, GPU-backed copy the level load
// already built (level_loader.go); lvl.Player.Models is the plain-data copy
// that gets saved and undone. tmp aliases the loaded copy rather than the
// plain one, so editorEntityModels' mesh-status messages ("nothing uploaded")
// read the real state and a Path/Texture edit's own .Load() call — already
// part of that generic panel — lands on the very thing RenderViewmodel draws.
// That would not be true the other way around: starting from lvl.Player.Models
// would mean nothing is ever "uploaded" until something is edited, and the
// live preview below would show nothing for a level that already works.
//
// lvl.Player.Models is brought into step afterward, through
// playerAuthoredModels rather than a raw slice assignment, so it never ends up
// holding the GPU state a level file has no way to save and an undo snapshot
// has no business duplicating.
func editorPlayerViewmodel(lvl *world.Level) {
	if world.PlayerEntity == nil {
		editorTextMutedWrapped("No player object yet — the engine has not finished starting up.")
		return
	}
	tmp := &world.Entity{ID: "player", Models: world.PlayerEntity.Models}
	editorEntityModels(tmp)
	world.PlayerEntity.Models = tmp.Models
	lvl.Player.Models = playerAuthoredModels(world.PlayerEntity.Models)
}

// playerAuthoredModels copies only the fields a level file can hold, dropping
// the runtime ones (Submeshes, Anim, Doc, AnimBlocked) that a GPU-backed
// world.EntityModel also carries — the same set entityModelToRaw picks out,
// reimplemented here since that conversion is unexported to the world package.
func playerAuthoredModels(models []world.EntityModel) []world.EntityModel {
	if len(models) == 0 {
		return nil
	}
	out := make([]world.EntityModel, len(models))
	for i, m := range models {
		out[i] = world.EntityModel{
			Name:       m.Name,
			Path:       m.Path,
			Texture:    m.Texture,
			Parent:     m.Parent,
			Bone:       m.Bone,
			Offset:     m.Offset,
			Rotation:   m.Rotation,
			Scale:      m.Scale,
			Alpha:      m.Alpha,
			Visible:    m.Visible,
			Animations: m.Animations,
		}
	}
	return out
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
			lvl.Player.StepHeight = def
			editorSetStatus("Player step height set to %.2f", lvl.Player.StepHeight)
		} else {
			lvl.Player.StepHeight = 0
			editorSetStatus("Player step height back to the default (%.2f)", def)
		}
	}

	if lvl.Player.StepHeight <= 0 {
		editorTextMutedWrapped(fmt.Sprintf(
			"Following the default: %.2f units, about a stair riser. Anything taller than that is a wall it is stopped by; anything shorter is a step it walks up.", def))
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
		"World units, measured from the floor the player is leaving. The engine's default without this block is %.2f.", def))
}

// editorPlayerGravity draws the level's own gravity and the player's two knobs
// on top of it.
//
// The level's number is here rather than in the Environment panel because this
// is where an author is already thinking about how the body moves, and because
// the three fields only make sense read together: the level sets the pull, the
// player scales it, and the terminal speed caps what the scale can build up to.
// The volume panel carries the fourth (a region that overrides the level's).
func editorPlayerGravity(lvl *world.Level) {
	// The level's own, with the same "does this level say anything" checkbox
	// the step height opens with — and for a sharper reason here: 0 is a
	// perfectly good gravity (a station with the plating off), so an unticked
	// box has to mean "no value written" rather than "zero".
	has := lvl.Gravity != nil
	if imgui.Checkbox("This level sets a gravity##gravon", &has) {
		EditorPushUndo()
		if has {
			g := world.DefaultGravity
			lvl.Gravity = &g
			editorSetStatus("Level gravity set to %.2f", g)
		} else {
			lvl.Gravity = nil
			editorSetStatus("Level gravity back to the default (%.2f)", world.DefaultGravity)
		}
	}
	if lvl.Gravity == nil {
		editorTextMutedWrapped(fmt.Sprintf(
			"Following the default: %.1f units a second squared, downward. A volume can still override it in one region.", world.DefaultGravity))
	} else {
		editorDragFloatStep("Gravity##levelgravity", lvl.Gravity, -200, 200, "%.2f", 0.5)
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Downward acceleration everywhere in the level, in units\n" +
				"a second squared. 36 is the engine's default and about\n" +
				"Earth at this scale; 6 reads as a moon, 0 as no pull at\n" +
				"all, and a negative number falls upward.")
		}
	}

	imgui.Spacing()
	// The player's own two. Zero means "the engine's", so these are plain
	// fields with no checkbox: the value at rest already says "not set".
	scale := lvl.Player.GravityScale
	if editorDragFloatStep("Gravity scale##plgravscale", &scale, -10, 10, "%.2f", 0.05) {
		EditorPushUndo()
		lvl.Player.GravityScale = scale
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Multiplies whatever gravity is in force where the player\n" +
			"is standing — the level's, or a volume's. 0 leaves it at 1.\n" +
			"To stop the player falling at all, use the spawn point's\n" +
			"gravity switch or @level.prop.player_gravity.")
	}
	fall := lvl.Player.MaxFallSpeed
	if editorDragFloatStep("Max fall speed##plmaxfall", &fall, 0, 500, "%.1f", 1) {
		EditorPushUndo()
		lvl.Player.MaxFallSpeed = fall
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip(fmt.Sprintf("Terminal speed of the player's fall, in units a second.\n"+
			"0 leaves it at the engine's %.0f.", world.DefaultMaxFallSpeed))
	}
}
