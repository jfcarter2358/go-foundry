package editor

// editor_entity_panel.go — the entity side of the editor's properties panel.
//
// Two sections, drawn by both the 3D and the 2D panel:
//
//	editorEntitySection    the catalog and the level's entity list — always up,
//	                       because placing the first entity has to be reachable
//	                       with nothing selected
//	editorEntityProperties the selected entity's own fields
//
// Angles are shown in degrees throughout and stored in radians, the same
// convention the light panel uses. Nobody authors a rotation in radians, and
// nothing downstream wants degrees.

import (
	"fmt"
	"foundry/internal/gfx"
	"foundry/internal/world"
	"strconv"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// ---------- catalog + list ----------

// editorEntitySection draws the definition catalog, the place button, and the
// list of entities already in the level.
func editorEntitySection(lvl *world.Level) {
	imgui.Separator()
	editorFocusOpen(panelFocusEntity)
	if !imgui.CollapsingHeaderV("Entities", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}

	// Scanned on first open rather than at startup: the editor can be launched
	// before any definition exists, and a catalog that was empty once would
	// stay empty for the session.
	if Editor.EntityDefNames == nil {
		editorRefreshEntityDefs()
	}

	editorEntityEngineToggles()
	imgui.Separator()
	editorEntityCatalog()
	imgui.Separator()
	editorEntityList(lvl)
}

// editorEntityEngineToggles draws the two engine-wide collision master
// switches.
//
// They sit here rather than in a physics panel of their own because they are
// the first thing to check when an entity that should be solid is not — having
// them next to the per-entity flags is what makes "is it this entity, or is it
// the engine" a glance rather than a hunt.
func editorEntityEngineToggles() {
	imgui.Checkbox("Entity/world collision  [8]##engworld", &world.EntityWorldCollisionEnabled)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Engine-wide. Each entity still has to opt in;\nthis says whether the engine listens.")
	}
	imgui.Checkbox("Entity/entity collision  [9]##engent", &world.EntityEntityCollisionEnabled)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Engine-wide. Each entity still has to opt in;\nthis says whether the engine listens.")
	}
	editorTextWrapped("Entities only move while the editor is closed.")
}

// entityBlankCatalogLabel names the catalog's first entry: not a definition,
// but the absence of one.
const entityBlankCatalogLabel = "(blank entity)"

// editorEntityCatalog draws the picker and the place button.
//
// The list always starts with the blank entry, and starts *on* it: placing
// something and then deciding what it is, in the panel, is the usual way an
// entity gets made. Definition files under entities/ follow it for the types
// that have earned a file of their own — see entity_def.go on when that is.
func editorEntityCatalog() {
	name := editorSelectedDefName()

	preview := entityBlankCatalogLabel
	if name != "" {
		preview = name
	}
	if imgui.BeginCombo("Type##entdef", preview) {
		if imgui.SelectableV(entityBlankCatalogLabel, name == "", 0, imgui.Vec2{}) {
			Editor.EntityDefIdx = 0
		}
		for i, n := range Editor.EntityDefNames {
			if imgui.SelectableV(n, n == name, 0, imgui.Vec2{}) {
				Editor.EntityDefIdx = int32(i + 1)
			}
		}
		imgui.EndCombo()
	}

	if name == "" {
		editorTextWrapped("No model and no type: a box you place and then " +
			"describe here. The level file carries all of it.")
	} else if def, err := world.LoadEntityDef(name); err == nil {
		// The description is the definition author's one-line note about what
		// the type is for — worth showing, but not worth failing the picker
		// over, so a definition that will not load simply shows why instead.
		if def.Description != "" {
			editorTextWrapped(def.Description)
		}
	} else {
		editorTextWrapped(fmt.Sprintf("Cannot load: %v", err))
	}

	imgui.InputTextV("ID##newent", &Editor.NewEntityID, 0, nil)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Leave blank to name it after its type, or \"" + world.BlankEntityID + "\"\n" +
			"for a blank one. A number is appended if the name is taken.")
	}

	if imgui.Button("Place Entity##entplace") {
		editorArmEntityPlacement(name)
	}
	if Editor.EntityPlacementMode {
		imgui.SameLine()
		imgui.Text("Click to place...")
		imgui.SameLine()
		if imgui.Button("Cancel##entplacecancel") {
			Editor.EntityPlacementMode = false
		}
	}
	imgui.SameLine()
	if imgui.Button("Reload Definitions##entdefs") {
		editorRefreshEntityDefs()
		editorSetStatus("Rescanned %s/", world.EntitiesDirName)
	}
	if len(Editor.EntityDefNames) == 0 && imgui.IsItemHovered() {
		imgui.SetTooltip("Nothing in " + world.EntitiesDirName + "/ yet. A definition is a\n" +
			".yaml file describing one type; entities do not need one.")
	}
}

// editorEntityList draws every entity in the level as a selectable row.
func editorEntityList(lvl *world.Level) {
	if len(lvl.Entities) == 0 {
		imgui.Text("This level has no entities.")
		return
	}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		label := e.ID
		if e.Type != "" && e.Type != e.ID {
			label = fmt.Sprintf("%s  (%s)", e.ID, e.Type)
		}
		if imgui.SelectableV(fmt.Sprintf("%s##ent%d", label, i), i == Editor.ActiveEntityIdx, 0, imgui.Vec2{}) {
			clearVertexSelection()
			editorClearSpawnSelection()
			editorClearVolumeSelection()
			editorClearChunkSelection()
			editorClearLightSelection()
			editorSelectEntity(i)
		}
	}
}

// ---------- selected entity ----------

// editorEntityProperties draws the selected entity's fields. Nothing is drawn
// when no entity is selected, so both panels can call it unconditionally.
func editorEntityProperties(lvl *world.Level) {
	e := activeEntity()
	if e == nil {
		return
	}

	imgui.Separator()
	editorFocusOpen(panelFocusEntity)
	if !imgui.CollapsingHeaderV("Entity: "+e.ID, imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusEntity)

	editorEntityIdentity(lvl, e)
	imgui.Separator()
	editorEntityTransform(e)
	imgui.Separator()
	editorEntityMotion(e)
	imgui.Separator()
	editorEntityBounds(e)
	imgui.Separator()
	editorEntityCollisionSection(e)
	editorEntityVehicleSection(lvl, e)
	imgui.Separator()
	editorEntityTags(e)
	imgui.Separator()
	editorEntityAttributes(e)
	imgui.Separator()
	editorEntityScript(e)
	imgui.Separator()
	editorEntityModels(e)
	imgui.Separator()
	editorEntityActions(e)
}

// editorEntityIdentity draws the ID and the type it was instanced from.
func editorEntityIdentity(lvl *world.Level, e *world.Entity) {
	id := e.ID
	if imgui.InputTextV("ID##entid", &id, imgui.InputTextFlagsEnterReturnsTrue, nil) {
		// Renaming to a name already in use would make `@id` ambiguous, so the
		// rename is refused rather than silently suffixed — the user typed a
		// specific name and should be told it is taken.
		switch {
		case id == "":
			editorSetStatus("An entity needs an ID")
		case id != e.ID && world.FindEntity(lvl, id) != nil:
			editorSetStatus("There is already an entity called %q", id)
		default:
			e.ID = id
		}
	}
	if e.Type != "" {
		imgui.Text("Type: " + e.Type)
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Stamped from " + world.EntitiesDirName + "/" + e.Type + ".yaml.\n" +
				"The level stores only what differs from it.")
		}
	} else {
		imgui.Text("Type: (none)")
		if imgui.IsItemHovered() {
			imgui.SetTooltip("A blank entity: everything below is its own,\nand the level writes all of it.")
		}
	}
	imgui.Checkbox("Visible##entvis", &e.Visible)
	if e.CurrentChunk != "" || e.CurrentSector != "" {
		imgui.Text(fmt.Sprintf("In: chunk %s / sector %s",
			orDash(e.CurrentChunk), orDash(e.CurrentSector)))
	}
}

// orDash renders an empty derived field as a dash rather than as nothing,
// so a blank line does not read as a missing label.
func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// editorEntityTransform draws position, rotation and scale.
func editorEntityTransform(e *world.Entity) {
	pos := [3]float32{e.Position.X(), e.Position.Y(), e.Position.Z()}
	if editorDragFloat3("Position##entpos", &pos, 0, 0, "%.3f") {
		e.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
	}

	rot := vec3ToDegrees(e.Rotation)
	if editorDragFloat3Step("Rotation (deg)##entrot", &rot, -360, 360, "%.1f", 1) {
		e.Rotation = vec3FromDegrees(rot)
	}

	sc := e.EffectiveScale()
	scv := [3]float32{sc.X(), sc.Y(), sc.Z()}
	if editorDragFloat3Step("Scale##entscale", &scv, -100, 100, "%.3f", 0.01) {
		e.Scale = mgl32.Vec3{
			world.EntityScaleClamp(scv[0]),
			world.EntityScaleClamp(scv[1]),
			world.EntityScaleClamp(scv[2]),
		}
	}
}

// editorEntityMotion draws velocity and angular velocity.
func editorEntityMotion(e *world.Entity) {
	if !imgui.CollapsingHeader("Motion##entmotion") {
		return
	}
	editorTextWrapped("Carried forward every frame during play, not in the editor.")

	vel := [3]float32{e.Velocity.X(), e.Velocity.Y(), e.Velocity.Z()}
	if editorDragFloat3Step("Velocity (u/s)##entvel", &vel, -1000, 1000, "%.3f", 0.05) {
		e.Velocity = mgl32.Vec3{vel[0], vel[1], vel[2]}
	}

	av := vec3ToDegrees(e.AngularVelocity)
	if editorDragFloat3Step("Angular velocity (deg/s)##entavel", &av, -3600, 3600, "%.1f", 1) {
		e.AngularVelocity = vec3FromDegrees(av)
	}

	if imgui.Button("Stop##entstopmotion") {
		e.Velocity = mgl32.Vec3{}
		e.AngularVelocity = mgl32.Vec3{}
	}
}

// editorEntityBounds draws the bounding volume: its shape, its size and where
// it sits relative to the entity's origin.
func editorEntityBounds(e *world.Entity) {
	if !imgui.CollapsingHeader("Bounds##entbounds") {
		return
	}

	if imgui.BeginCombo("Shape##entboundkind", world.BoundingTypeName(e.BoundingKind)) {
		for i, n := range world.BoundingTypeNames {
			if imgui.SelectableV(n, world.BoundingType(i) == e.BoundingKind, 0, imgui.Vec2{}) {
				e.BoundingKind = world.BoundingType(i)
			}
		}
		imgui.EndCombo()
	}

	half := [3]float32{e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ}
	if editorDragFloat3Step("Half extents##entbounds", &half, 0, 1000, "%.3f", 0.05) {
		e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ = half[0], half[1], half[2]
	}

	off := [3]float32{e.BoundOffset.X(), e.BoundOffset.Y(), e.BoundOffset.Z()}
	if editorDragFloat3Step("Centre offset##entboundoff", &off, -1000, 1000, "%.3f", 0.05) {
		e.BoundOffset = mgl32.Vec3{off[0], off[1], off[2]}
	}

	if !e.HasBounds() {
		editorTextWrapped("No volume: every extent must be above zero. " +
			"The entity is never culled and never contains a point.")
	}
}

// ---------- collision ----------

// editorEntityCollisionSection draws the four physical-participation flags.
//
// They are per-entity here, but they belong on the *type* — a barrel is solid
// wherever it is placed. The note says so, since the natural next move after
// ticking a box on one instance is to wonder why the next one placed is not
// solid too.
func editorEntityCollisionSection(e *world.Entity) {
	if !imgui.CollapsingHeaderV("Collision##entcol", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}

	imgui.Checkbox("Collide with world##entcolw", &e.Collision.World)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Resolve against walls, floors, ceilings and slopes —\nthe same resolvers the player uses.")
	}
	imgui.Checkbox("Collide with entities##entcole", &e.Collision.Entities)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Push apart from other entities that also have this on,\nand from the player. Both sides have to opt in.")
	}
	imgui.Checkbox("Gravity##entcolg", &e.Collision.Gravity)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Fall. Without world collision it falls forever, which is\nwhat a projectile wants.")
	}
	imgui.Checkbox("Movable##entcolm", &e.Collision.Movable)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Off makes it immovable scenery: it pushes other entities\nand is never pushed itself, and gravity does not touch it.")
	}

	if !e.HasBounds() && (e.Collision.World || e.Collision.Entities) {
		editorTextWrapped("No bounding volume, so nothing to collide with. " +
			"Give every half-extent above a value above zero.")
	}
	if e.Collision.OnGround {
		imgui.Text("Resting on ground")
	}

	if !world.EntityWorldCollisionEnabled && e.Collision.World {
		editorTextWrapped("World collision is switched off engine-wide.")
	}
	if !world.EntityEntityCollisionEnabled && e.Collision.Entities {
		editorTextWrapped("Entity collision is switched off engine-wide.")
	}
	if e.Type != "" {
		editorTextWrapped("These belong on the type. Set them in " +
			world.EntitiesDirName + "/" + e.Type + ".yaml and every one placed gets them.")
	}
}

// ---------- tags ----------

// editorEntityTags and editorEntityAttributes are thin wrappers over the shared
// rows in editor_script_panel.go. An entity's tags and attributes are merged
// from its definition rather than held in a ScriptData, so it cannot use
// editorScriptDataUI wholesale — but the widgets themselves are the same ones a
// volume or a sector gets, and there is no reason for two copies of them.
func editorEntityTags(e *world.Entity) {
	editorTagsUI(&e.Tags, "ent", "Tags")
}

func editorEntityAttributes(e *world.Entity) {
	editorAttributesUI(&e.Attributes, "ent", "Attributes", strconv.Quote(e.ID))
}

// editorEntityScript draws the entity's script picker. An instance's script
// starts as its definition's and may be overridden or detached here; saving
// writes only the difference.
func editorEntityScript(e *world.Entity) {
	editorScriptUI(&e.Script, &e.ScriptParams, "ent", strconv.Quote(e.ID))
}

// editorNewAttrTypeIdx keeps the add row's type index in range, so a value left
// over from a shorter list cannot index past the end.
func editorNewAttrTypeIdx() int {
	i := int(Editor.NewAttrType)
	if i < 0 || i >= len(world.AttrTypeNames) {
		return 0
	}
	return i
}

// ---------- models ----------

// editorEntityModels draws the attachment list: what each one wears, where it
// sits, and what it can play.
func editorEntityModels(e *world.Entity) {
	if !imgui.CollapsingHeaderV("Models##entmodels", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	if len(e.Models) == 0 {
		editorTextWrapped("No models. An entity with none is still a position " +
			"with attributes on it, which is all a trigger or a script anchor needs.")
	}
	for i := range e.Models {
		if editorEntityModelSection(e, i) {
			return // the list was rewritten; resume next frame
		}
	}

	if imgui.Button("Add Model##entmodeladd") {
		editorEntityAddModel(e)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A new attachment with no mesh yet.\nPick one for it below.")
	}
	imgui.SameLine()
	if imgui.Button("Reload Models##entreload") {
		reloadActiveEntityModels()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Re-read every attachment from disk — after editing\nits .fmdl in the model editor, say.")
	}

	if e.Type != "" {
		editorTextWrapped("This entity has a type, so what is changed here is " +
			"stored in the level as an override on " + e.Type + ". Change " +
			world.EntitiesDirName + "/" + e.Type + ".yaml instead and every one placed gets it.")
	}
}

// editorEntityModelSection draws one attachment. Reports whether the list was
// structurally changed, which is the caller's cue to stop walking it.
func editorEntityModelSection(e *world.Entity, idx int) bool {
	m := &e.Models[idx]
	imgui.PushID(fmt.Sprintf("entmodel%d", idx))
	defer imgui.PopID()

	if !imgui.CollapsingHeaderV(m.Name+"##hdr", imgui.TreeNodeFlagsDefaultOpen) {
		return false
	}

	// The attachment's name is how a script asks for an animation on this model
	// rather than another, so it is editable and has to stay unique. The delete
	// button rides beside it, the same shape an animation row has.
	name := m.Name
	imgui.SetNextItemWidth(-90)
	if imgui.InputTextV("##mname", &name, imgui.InputTextFlagsEnterReturnsTrue, nil) {
		switch {
		case name == "":
			editorSetStatus("An attachment needs a name")
		case name != m.Name && e.Model(name) != nil:
			editorSetStatus("This entity already has an attachment called %q", name)
		default:
			EditorPushUndo()
			m.Name = name
		}
	}
	imgui.SameLine()
	if imgui.Button("x##mdel") {
		editorEntityRemoveModel(e, idx)
		return true
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Remove this attachment.")
	}

	// The mesh. Changing it is the point of this section — an entity placed
	// blank has nothing until one is picked here, and one that outgrew its
	// first mesh swaps it without being deleted and placed again.
	prev := m.Path
	if modelFileCombo("Model##mpath", &m.Path) {
		editorEntitySetModelPath(e, m, prev, m.Path)
	}
	if m.Path == "" {
		editorTextWrapped("No mesh: this attachment draws nothing.")
	} else if !m.HasGeometry() {
		editorTextWrapped("Nothing uploaded — the file is missing, empty, or failed to load.")
	}

	imgui.Checkbox("Visible##mvis", &m.Visible)

	// Texture: an override on the model document's own. "(none)" clears it back
	// to whatever the mesh was authored with, which is why allowEmpty is set.
	if textureCombo("Texture##mtex", &m.Texture, true) {
		if err := m.Load(e.ID); err != nil {
			editorSetStatus("Entity %s: %v", e.ID, err)
		}
	}

	off := [3]float32{m.Offset.X(), m.Offset.Y(), m.Offset.Z()}
	if editorDragFloat3Step("Offset##moff", &off, -1000, 1000, "%.3f", 0.05) {
		m.Offset = mgl32.Vec3{off[0], off[1], off[2]}
	}

	rot := vec3ToDegrees(m.Rotation)
	if editorDragFloat3Step("Rotation (deg)##mrot", &rot, -360, 360, "%.1f", 1) {
		m.Rotation = vec3FromDegrees(rot)
	}

	sc := m.EffectiveScale()
	scv := [3]float32{sc.X(), sc.Y(), sc.Z()}
	if editorDragFloat3Step("Scale##mscale", &scv, -100, 100, "%.3f", 0.01) {
		m.Scale = mgl32.Vec3{
			world.EntityScaleClamp(scv[0]),
			world.EntityScaleClamp(scv[1]),
			world.EntityScaleClamp(scv[2]),
		}
	}

	alpha := m.EffectiveAlpha()
	if editorDragFloatStep("Alpha##malpha", &alpha, 0, 1, "%.2f", 0.02) {
		m.SetAlpha(alpha)
	}

	editorEntityAnimations(e, m)
	return false
}

// editorEntityAnimations draws an attachment's named animations, plus the
// playback controls that let a clip be previewed in the editor.
func editorEntityAnimations(e *world.Entity, m *world.EntityModel) {
	if !imgui.CollapsingHeaderV("Animations##manims", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	if m.Anim == nil {
		editorTextWrapped("This model has no armature or no clips, so there is nothing to play.")
		return
	}

	clips := m.Anim.ClipNames()
	playing := m.Playing()

	for i := range m.Animations {
		if editorEntityAnimationRow(m, i, clips, playing) {
			return // the list was rewritten; resume next frame
		}
	}
	if len(m.Animations) == 0 {
		imgui.Text("(none)")
	}

	if imgui.Button("Add Animation##animadd") {
		EditorPushUndo()
		base := "animation"
		if len(clips) > 0 {
			// Default to a clip the model actually has, so a fresh row plays
			// something rather than naming a clip that does not exist.
			base = clips[0]
		}
		m.Animations = append(m.Animations, world.EntityAnimation{
			Name: world.UniqueAnimationName(m, base),
			Clip: base,
		})
	}
	imgui.SameLine()
	if imgui.Button("Stop##animstop") {
		m.Stop()
	}
	if playing != "" {
		imgui.Text("Playing: " + playing)
	}
}

// editorEntityAnimationRow draws one animation: its game-facing name, the clip
// it maps to, and its playback settings. Reports whether the list was
// structurally changed.
func editorEntityAnimationRow(m *world.EntityModel, idx int, clips []string, playing string) bool {
	a := &m.Animations[idx]
	imgui.PushID(fmt.Sprintf("anim%d", idx))
	defer imgui.PopID()

	name := a.Name
	imgui.SetNextItemWidth(-90)
	if imgui.InputTextV("##name", &name, imgui.InputTextFlagsEnterReturnsTrue, nil) && name != "" {
		if m.Animation(name) == nil {
			a.Name = name
		} else {
			editorSetStatus("This model already has an animation called %q", name)
		}
	}
	imgui.SameLine()
	if imgui.Button("x##del") {
		EditorPushUndo()
		m.Animations = append(m.Animations[:idx:idx], m.Animations[idx+1:]...)
		return true
	}

	// The clip is the .fmdl's own name for the motion. Shown even when it is
	// not among the model's clips, so a mapping that has gone stale is visible
	// rather than silently reset to something that happens to exist.
	if imgui.BeginCombo("Clip##clip", a.EffectiveClip()) {
		known := false
		for _, c := range clips {
			if c == a.EffectiveClip() {
				known = true
			}
			if imgui.SelectableV(c, c == a.EffectiveClip(), 0, imgui.Vec2{}) {
				a.Clip = c
			}
		}
		if !known {
			imgui.SelectableV(a.EffectiveClip()+" (missing)", true, 0, imgui.Vec2{})
		}
		imgui.EndCombo()
	}

	speed := a.EffectiveSpeed()
	if editorDragFloatStep("Speed##speed", &speed, 0.01, 100, "%.2f", 0.05) {
		a.Speed = speed
	}

	// Loop is tri-state: follow the clip, force on, force off. A plain checkbox
	// cannot say "follow", and "follow" is the default worth keeping reachable.
	editorEntityLoopCombo(a)
	imgui.Checkbox("Autoplay##autoplay", &a.Autoplay)

	if playing == a.Name {
		imgui.Text("(playing)")
	} else if imgui.Button("Play##play") {
		if !m.Play(a.Name) {
			editorSetStatus("Cannot play %q — clip %q is not in this model", a.Name, a.EffectiveClip())
		}
	}
	imgui.Separator()
	return false
}

// editorEntityLoopCombo draws the tri-state loop override.
func editorEntityLoopCombo(a *world.EntityAnimation) {
	const (
		followClip = "follow clip"
		forceOn    = "loop"
		forceOff   = "once"
	)
	cur := followClip
	if a.Loop != nil {
		if *a.Loop {
			cur = forceOn
		} else {
			cur = forceOff
		}
	}
	if !imgui.BeginCombo("Loop##loop", cur) {
		return
	}
	if imgui.SelectableV(followClip, cur == followClip, 0, imgui.Vec2{}) {
		a.Loop = nil
	}
	if imgui.SelectableV(forceOn, cur == forceOn, 0, imgui.Vec2{}) {
		v := true
		a.Loop = &v
	}
	if imgui.SelectableV(forceOff, cur == forceOff, 0, imgui.Vec2{}) {
		v := false
		a.Loop = &v
	}
	imgui.EndCombo()
}

// ---------- actions ----------

// editorEntityActions draws the buttons that act on the whole entity.
func editorEntityActions(e *world.Entity) {
	if imgui.Button("Duplicate##entdup") {
		duplicateActiveEntity()
	}
	imgui.SameLine()
	if imgui.Button("Move To Camera##entcam") {
		// The camera sits at eye height; an entity's position is its base, the
		// same convention the spawn marker uses.
		e.Position = gfx.Cam.Position.Sub(mgl32.Vec3{0, world.EyeHeight, 0})
	}

	if e.Type != "" {
		if imgui.Button("Reset To Definition##entreset") {
			reapplyEntityDefinition()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Re-stamp from " + e.Type + ", keeping only where it is.\n" +
				"Every override on this instance is discarded.")
		}
	}

	if imgui.Button("Delete Entity##entdel") {
		deleteActiveEntity()
	}
}

// ---------- helpers ----------

// editorTextWrapped draws a paragraph that wraps at the panel's right edge.
// imgui-go v4 exposes the wrap-position stack but no wrapped-text call of its
// own, so this is the two-line idiom the texture editor already uses, named
// once instead of repeated at every explanatory line below.
func editorTextWrapped(s string) {
	imgui.PushTextWrapPos()
	imgui.Text(s)
	imgui.PopTextWrapPos()
}

// vec3ToDegrees / vec3FromDegrees convert a radian Euler triple to the degrees
// the panel shows and back.
func vec3ToDegrees(v mgl32.Vec3) [3]float32 {
	return [3]float32{mgl32.RadToDeg(v.X()), mgl32.RadToDeg(v.Y()), mgl32.RadToDeg(v.Z())}
}

func vec3FromDegrees(d [3]float32) mgl32.Vec3 {
	return mgl32.Vec3{mgl32.DegToRad(d[0]), mgl32.DegToRad(d[1]), mgl32.DegToRad(d[2])}
}

// editorDragFloat3Step is editorDragFloat3 with an explicit drag speed, for the
// fields whose unit is not world distance — degrees, scale factors, per-second
// rates — where the grid step makes dragging either uselessly slow or wild.
func editorDragFloat3Step(label string, values *[3]float32, min, max float32, format string, step float32) bool {
	changed := false
	labels := [3]string{"X", "Y", "Z"}

	imgui.Text(label)
	imgui.PushID(label)
	for i := 0; i < 3; i++ {
		if editorDragFloatStep(fmt.Sprintf("%s##%d", labels[i], i), &values[i], min, max, format, step) {
			changed = true
		}
	}
	imgui.PopID()
	return changed
}
