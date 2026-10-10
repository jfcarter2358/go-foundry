package editor

// editor_effect_panel.go — the Effects section of the editor's side panel.
//
// Like the sound's panel rather than the emitter's: an effect has no document
// edited somewhere else, so how it looks is authored right here, and the
// kind's parameter group is the substance of it.
//
// The Detonate button is what Audition is for a sound — the way to see the
// thing again without waiting for its loop.

import (
	"strconv"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorEffectPropertiesSection draws the selected effect's fields.
func editorEffectPropertiesSection() {
	e := activeEffect()
	if e == nil {
		return
	}
	imgui.Separator()
	editorFocusOpen(panelFocusEffect)
	if !imgui.CollapsingHeaderV("Effect: "+e.ID+"##fxhdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusEffect)
	editorEffectProperties(e)
}

// editorEffectAddMenu draws the effect half of the Explorer's Add menu.
func editorEffectAddMenu() {
	imgui.SetNextItemWidth(160)
	imgui.InputTextV("ID##newfx", &Editor.NewEffectID, 0, nil)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Leave empty to name it after its kind.\nMade unique on placement.")
	}
	imgui.Separator()
	for _, k := range world.EffectKinds {
		if imgui.MenuItem(effectKindLabel(k) + "##placefx" + string(k)) {
			Editor.NewEffectKind = string(k)
			editorArmEffectPlacement()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(effectKindTooltip(k))
		}
	}
}

// effectKindLabel is a kind as the menu names it.
func effectKindLabel(k world.EffectKind) string {
	switch k {
	case world.EffectExplosion:
		return "Explosion"
	}
	return string(k)
}

func effectKindTooltip(k world.EffectKind) string {
	switch k {
	case world.EffectExplosion:
		return "A volumetric fireball that radiates outward, cools into smoke\n" +
			"and lights the level while it burns. Placed where you click."
	}
	return ""
}

// editorEffectProperties draws the selected effect's editable state.
func editorEffectProperties(e *world.Effect) {
	editorPropGroupOpen("Identity##fxidgrp", func() { editorEffectIdentity(e) })
	editorPropGroupOpen("Transform##fxtfgrp", func() { editorEffectTransform(e) })
	if e.Kind == world.EffectExplosion {
		editorPropGroupOpen("Explosion##fxexpgrp", func() { editorExplosionFire(&e.Explosion) })
		editorPropGroupOpen("Light##fxlightgrp", func() { editorExplosionLight(&e.Explosion) })
	}
	editorPropGroup("Attach##fxattachgrp", 0, func() { editorEffectAttach(e) })
	editorScriptDataUI(&e.ScriptData, "fx", strconv.Quote(e.ID))
	editorPropGroupOpen("Actions##fxactions", func() { editorEffectActions(e) })
}

// editorEffectIdentity draws the ID, the kind and the timeline switches.
func editorEffectIdentity(e *world.Effect) {
	id := e.ID
	if imgui.InputTextV("ID##fxid", &id, imgui.InputTextFlagsEnterReturnsTrue, nil) && id != e.ID {
		lvl := Editor.level
		if id == "" {
			editorSetStatus("An effect needs an ID for a script to address it")
		} else if world.LevelIDTaken(lvl, id) {
			editorSetStatus("%q is already used by something else in this level", id)
		} else {
			EditorPushUndo()
			e.ID = id
		}
	}
	editorTextMuted("Kind: " + effectKindLabel(e.Kind))

	if imgui.Checkbox("Enabled##fxenabled", &e.Enabled) {
		EditorPushUndo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Off draws and lights nothing.")
	}
	if imgui.Checkbox("Playing##fxplaying", &e.Playing) {
		EditorPushUndo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Whether it goes off when the level starts.\n" +
			"Untick for one a script sets off with play(@id).")
	}
	if imgui.Checkbox("Loop##fxloop", &e.Loop) {
		EditorPushUndo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Go off again after the delay below.\nOff goes off once and stops.")
	}
	if e.Loop {
		editorDragFloatStep("Loop delay (s)##fxloopdelay", &e.LoopDelay, 0, 600, "%.2f", 0.1)
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Seconds between one run ending and the next going off.")
		}
	}

	// Where it is in its run, because a stopped explosion and a finished one
	// look identical — nothing at all.
	switch {
	case !e.Enabled:
		editorTextMutedWrapped("Disabled.")
	case !e.Playing:
		editorTextMutedWrapped("Stopped. Detonate to see it.")
	case e.Live():
		editorTextMuted("Burning: " + strconv.Itoa(int(e.Progress()*100)) + "%")
	default:
		editorTextMuted("Waiting to loop")
	}
}

func editorEffectTransform(e *world.Effect) {
	pos := [3]float32{e.Position.X(), e.Position.Y(), e.Position.Z()}
	if editorDragFloat3("Position##fxpos", &pos, 0, 0, "%.3f") {
		e.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
	}
	if !e.Attach.IsZero() && imgui.IsItemHovered() {
		imgui.SetTooltip("Local to " + e.Attach.Describe() + ", not the world.")
	}

	rot := vec3ToDegrees(e.Rotation)
	if editorDragFloat3Step("Rotation (deg)##fxrot", &rot, -360, 360, "%.1f", 1) {
		e.Rotation = vec3FromDegrees(rot)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Turns the fireball's billows, so two side by side\ndo not look alike.")
	}
}

// editorExplosionFire draws the fireball's size, timing and look.
func editorExplosionFire(x *world.Explosion) {
	editorDragFloatStep("Radius##fxradius", &x.Radius, 0.1, 1000, "%.2f", 0.1)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How far the fireball reaches once grown.\nOutlined in the viewport while selected.")
	}
	editorDragFloatStep("Duration (s)##fxdur", &x.Duration, 0.05, 120, "%.2f", 0.1)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Seconds from detonation to the last of the smoke.")
	}
	editorDragFloatStep("Brightness##fxbright", &x.Brightness, 0, 20, "%.2f", 0.05)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How strongly the fire glows. Its colour comes\nfrom how hot each part of it still is.")
	}
	tint := [3]float32{x.FireTint.X(), x.FireTint.Y(), x.FireTint.Z()}
	if imgui.ColorEdit3("Fire tint##fxtint", &tint) {
		x.FireTint = mgl32.Vec3{tint[0], tint[1], tint[2]}
	}
	smoke := [3]float32{x.SmokeColor.X(), x.SmokeColor.Y(), x.SmokeColor.Z()}
	if imgui.ColorEdit3("Smoke##fxsmoke", &smoke) {
		x.SmokeColor = mgl32.Vec3{smoke[0], smoke[1], smoke[2]}
	}
	editorDragFloatStep("Density##fxdensity", &x.Density, 0, 20, "%.2f", 0.05)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How thick the fire and smoke are. Lower sees into it.")
	}
	editorDragFloatStep("Turbulence##fxturb", &x.Turbulence, 0, 1.5, "%.2f", 0.01)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How far the billows push the surface in and out.\n0 is a smooth ball.")
	}
	editorDragFloatStep("Rise##fxrise", &x.Rise, -100, 100, "%.2f", 0.1)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How far the fireball climbs over its life.")
	}
}

// editorExplosionLight draws the flash it lights the level with.
func editorExplosionLight(x *world.Explosion) {
	col := [3]float32{x.LightColor.X(), x.LightColor.Y(), x.LightColor.Z()}
	if imgui.ColorEdit3("Color##fxlightcol", &col) {
		x.LightColor = mgl32.Vec3{col[0], col[1], col[2]}
	}
	editorDragFloatStep("Brightness##fxlightbright", &x.LightBrightness, 0, 100, "%.2f", 0.1)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The flash at its peak. 0 lights nothing.")
	}
	editorDragFloatStep("Radius##fxlightradius", &x.LightRadius, 0, 1000, "%.1f", 0.5)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How far the flash reaches once the fireball is grown.")
	}
	// A level already at the limit has the flash competing for a slot rather
	// than taking a free one, and says so: an explosion that lit nothing in a
	// room full of lamps would otherwise have no hint why.
	if lvl := Editor.level; lvl != nil && len(lvl.Lights.Points) >= world.MaxPointLights {
		editorTextMutedWrapped("This level already has " + strconv.Itoa(world.MaxPointLights) +
			" point lights, the most there are slots for: the flash lights only when it is" +
			" worth more to the camera than one of them that casts no shadow.")
	}
}

func editorEffectAttach(e *world.Effect) {
	if editorAttachUI(Editor.level, &e.Attach, "fx") {
		EditorPushUndo()
	}
	if e.Attach.IsZero() {
		editorTextMutedWrapped("Unattached: Position is world-space.")
	} else {
		editorTextMutedWrapped("Rides " + e.Attach.Describe() + "; Position is local to it.")
	}
}

func editorEffectActions(e *world.Effect) {
	if imgui.Button("Detonate##fxdetonate") {
		detonateActiveEffect()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Set it off from the start, now.")
	}
	imgui.SameLine()
	if imgui.Button("Duplicate##fxdup") {
		duplicateActiveEffect()
	}
	imgui.SameLine()
	if imgui.Button("Delete##fxdel") {
		deleteActiveEffect()
	}
}
