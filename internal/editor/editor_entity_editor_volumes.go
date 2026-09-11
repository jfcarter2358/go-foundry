package editor

// editor_entity_editor_volumes.go — the regions a type carries, in the entity
// editor.
//
// The document holds templates (world/entity_volume.go): a name, a shape, where
// on the entity it sits, and the tags, attributes and script the placed volume
// gets. This file is the outline's list, the panel that edits one, and the cage
// that shows where it is against the models and brushes beside it.
//
// The cage is drawn through the gizmo line buffer rather than as an uploaded
// wireframe: this window IS the entity's local space, so the transform is the
// template's own (and the bone's, when it names one), and a line list rebuilt
// from CageMesh each frame is a few dozen segments with no GL state to keep in
// step with the document.

import (
	"fmt"
	"strconv"

	"foundry/internal/gizmo"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

var (
	entityVolumeCageColor = mgl32.Vec3{0.35, 0.9, 0.55}
	entityVolumeSelColor  = mgl32.Vec3{1, 1, 0}
)

// defaultVolumeName is what a new carried volume is called.
const defaultVolumeName = "volume"

// ---------- outline ----------

// entityOutlineVolumes lists the regions the type carries.
func entityOutlineVolumes() {
	imgui.Text("Volumes")
	for i := range entityDef.Volumes {
		m := &entityDef.Volumes[i]
		imgui.PushID(fmt.Sprintf("outv%d", i))
		label := m.Name + "  (" + world.VolumeShapeName(m.Shape) + ")"
		if !m.Enabled {
			label += "  (off)"
		}
		if imgui.SelectableV(label, entitySel.Kind == entitySelVolume && entitySel.Idx == i, 0, imgui.Vec2{}) {
			entitySel = entityEditorSelection{Kind: entitySelVolume, Idx: i}
		}
		imgui.PopID()
	}
	if len(entityDef.Volumes) == 0 {
		editorTextMutedWrapped("None. A volume here is a region every placement carries — a sight cone, a pickup radius — with its own script for enter and exit.")
	}
	if imgui.Button("Add Volume##entaddv") {
		entityAddVolume()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Adds a region to the type. Shape it on the right, and\n" +
			"place it with the move, rotate and scale gizmos.")
	}
}

// ---------- structural edits ----------

// entityAddVolume adds a one-unit box to the type, selected.
func entityAddVolume() {
	if entityDef == nil {
		return
	}
	EntityDefPushUndo()
	entityDef.Volumes = append(entityDef.Volumes,
		world.NewEntityVolume(world.UniqueEntityVolumeName(entityDef.Volumes, defaultVolumeName)))
	entitySel = entityEditorSelection{Kind: entitySelVolume, Idx: len(entityDef.Volumes) - 1}
	entityMarkDirty()
}

// entityRemoveVolume drops one carried volume.
func entityRemoveVolume(idx int) {
	if entityDef == nil || idx < 0 || idx >= len(entityDef.Volumes) {
		return
	}
	EntityDefPushUndo()
	entityDef.Volumes = append(entityDef.Volumes[:idx:idx], entityDef.Volumes[idx+1:]...)
	entitySel = entityEditorSelection{}
	entityMarkDirty()
}

// ---------- panel ----------

// entityPanelVolume draws one carried volume: its name, where it hangs, its
// shape, and the scripting the placed volume carries.
func entityPanelVolume(idx int) {
	m := &entityDef.Volumes[idx]
	name := m.Name
	imgui.SetNextItemWidth(-90)
	if imgui.InputTextV("##entvname", &name, imgui.InputTextFlagsEnterReturnsTrue, nil) {
		switch {
		case name == "":
			entityStatus = "A volume needs a name"
		case name != m.Name && entityDef.VolumeNamed(name) != nil:
			entityStatus = fmt.Sprintf("This type already has a volume called %q", name)
		default:
			EntityDefPushUndo()
			m.Name = name
			entityMarkDirty()
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Qualified by the placement in a level, so a script says\n" +
			"@<entity>." + m.Name + " to reach this guard's and not every guard's.")
	}
	imgui.SameLine()
	if imgui.Button("Delete##entvdel") {
		entityRemoveVolume(idx)
		return
	}

	if imgui.Checkbox("Enabled##entven", &m.Enabled) {
		entityBeginEdit("ven")
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A disabled volume contains nothing, so it fires no events.\n" +
			"A type whose region arms on cue ships with this off.")
	}

	entityPanelSocket(&m.Model, &m.Bone, "v", "The volume")

	off := [3]float32{m.Offset.X(), m.Offset.Y(), m.Offset.Z()}
	if editorDragFloat3Step("Offset##entvoff", &off, -1000, 1000, "%.3f", 0.05) {
		entityBeginEdit("voff")
		m.Offset = mgl32.Vec3{off[0], off[1], off[2]}
	}
	if m.Bone != "" && imgui.IsItemHovered() {
		imgui.SetTooltip("Local to the bone, not to the entity.")
	}
	rot := vec3ToDegrees(m.Rotation)
	if editorDragFloat3Step("Rotation (deg)##entvrot", &rot, -360, 360, "%.1f", 1) {
		entityBeginEdit("vrot")
		m.Rotation = vec3FromDegrees(rot)
	}

	if imgui.BeginCombo("Shape##entvshape", world.VolumeShapeName(m.Shape)) {
		for i, n := range world.VolumeShapeNames {
			kind := world.VolumeShape(i)
			if imgui.SelectableV(n, kind == m.Shape, 0, imgui.Vec2{}) && kind != m.Shape {
				EntityDefPushUndo()
				m.Shape = kind
				entityMarkDirty()
			}
		}
		imgui.EndCombo()
	}
	half := [3]float32{m.Half.X(), m.Half.Y(), m.Half.Z()}
	if editorDragFloat3Step("Half-extents##entvhalf", &half, 0.01, 1000, "%.3f", 0.05) {
		entityBeginEdit("vhalf")
		m.Half = mgl32.Vec3{half[0], half[1], half[2]}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Half the region's size on each axis — the box the shape is inscribed in.\n" +
			"The entity's own scale reaches it too, once placed.")
	}

	owner := strconv.Quote(entityDefName() + "." + m.Name)
	editorScriptDataUI(&m.ScriptData, "entv", owner)
	editorTextMutedWrapped("The script gets this volume's enter and exit, with @self the volume. " +
		"@self.prop.carrier is the entity it rides, which never enters its own volume.")
}

// entityPanelSocket draws the model-and-bone picker a carried emitter or volume
// hangs off. what names the thing in the hints ("The effect", "The volume").
//
// The bone list comes from the preview, which is the only place the .fmdl has
// actually been read — the same seam the animation clip list uses. A model with
// no rig therefore offers nothing, which is the honest answer.
func entityPanelSocket(model, bone *string, idSuffix, what string) {
	models := entityEmitterModelNames()
	if len(models) == 0 {
		editorTextMutedWrapped("Hangs off the type's origin. Add a model with a rig to hang it off a bone instead.")
		return
	}
	if entityNameCombo("Model##ent"+idSuffix+"model", model, models, "(the entity itself)") {
		EntityDefPushUndo()
		if *model == "" {
			// A bone is meaningless without the attachment it belongs to, and
			// one left behind would silently do nothing.
			*bone = ""
		}
		entityMarkDirty()
	}
	if *model == "" {
		return
	}
	bones := entityEmitterBoneNames(*model)
	if len(bones) == 0 {
		editorTextMutedWrapped("That attachment has no bones — either it is not rigged, or its mesh has not loaded.")
		return
	}
	if entityNameCombo("Bone##ent"+idSuffix+"bone", bone, bones, "(the attachment's origin)") {
		EntityDefPushUndo()
		entityMarkDirty()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip(what + " rides this bone's posed placement, so it\n" +
			"follows the pose. Its Offset is then local to the bone.")
	}
}

// ---------- the viewport ----------

// entityVolumeOrigin is the transform one carried volume sits in, in the
// entity's own space: the bone it names, then its own offset and rotation —
// entityEmitterOrigin's rule, for the same reason.
func entityVolumeOrigin(i int) mgl32.Mat4 {
	if entityDef == nil || i < 0 || i >= len(entityDef.Volumes) {
		return mgl32.Ident4()
	}
	m := &entityDef.Volumes[i]
	local := m.LocalMatrix()
	if bm, ok := world.EntityBoneMatrix(entityPreview, m.Model, m.Bone); ok {
		return bm.Mul4(local)
	}
	return local
}

// drawEntityVolumeCages traces every carried volume's cage, the selected one in
// the selection colour.
func drawEntityVolumeCages() {
	if entityDef == nil {
		return
	}
	for i := range entityDef.Volumes {
		col := entityVolumeCageColor
		if entitySel.Kind == entitySelVolume && entitySel.Idx == i {
			col = entityVolumeSelColor
		}
		v := world.EntityVolumeInstance("", &entityDef.Volumes[i])
		if !v.HasBounds() {
			continue
		}
		verts, idx := v.CageMesh()
		xf := entityVolumeOrigin(i)
		at := func(k int) mgl32.Vec3 {
			return xf.Mul4x1(mgl32.Vec4{verts[k][0], verts[k][1], verts[k][2], 1}).Vec3()
		}
		for k := 0; k+1 < len(idx); k += 2 {
			a, b := idx[k], idx[k+1]
			if a < 0 || a >= len(verts) || b < 0 || b >= len(verts) {
				continue
			}
			gizmo.GizmoLine(at(a), at(b), col)
		}
	}
}
