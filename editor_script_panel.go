package main

// editor_script_panel.go — the tags / attributes / script rows, for every kind
// of object that has them.
//
// These used to be part of the entity panel, because an entity was the only
// thing that carried tags and attributes. Scripting gives them to volumes,
// sectors, edges, floors, ceilings, chunks and lights as well, and three copies
// of the same list-with-a-delete-button is three places for the behaviour to
// drift. They take the underlying fields rather than an owner type so the entity
// panel — whose tags and attributes are merged from a definition and so are not
// a ScriptData — can use them on equal terms.
//
// idSuffix disambiguates the imgui widget IDs when more than one of these is on
// screen at once, which happens the moment a sector panel shows its own tags
// above its floor's.

import (
	"fmt"
	"strings"

	imgui "github.com/inkyblackness/imgui-go/v4"

	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/world"
)

// editorTagsUI draws a tag list and its add row, editing tags in place.
//
// Tags are what a script's `#tag` addresses, so the list is kept sorted —
// NormalizeTags does that on every add.
func editorTagsUI(tags *[]string, idSuffix, header string) {
	if !imgui.CollapsingHeaderV(header+"##tags"+idSuffix, imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	for _, t := range *tags {
		imgui.Text("#" + t)
		imgui.SameLine()
		if imgui.Button("x##tag" + idSuffix + t) {
			EditorPushUndo()
			for i, cur := range *tags {
				if cur == t {
					*tags = append((*tags)[:i], (*tags)[i+1:]...)
					break
				}
			}
			// The list being iterated has just been rewritten; leave now and
			// draw the rest of it next frame.
			return
		}
	}
	if len(*tags) == 0 {
		imgui.Text("(none)")
	}

	imgui.InputTextV("##newtag"+idSuffix, &Editor.NewTagText, 0, nil)
	imgui.SameLine()
	if imgui.Button("Add Tag##tagadd"+idSuffix) && Editor.NewTagText != "" {
		EditorPushUndo()
		*tags = world.NormalizeTags(append(*tags, Editor.NewTagText))
		Editor.NewTagText = ""
	}
}

// editorAttributesUI draws an attribute table and its add row, editing the set
// in place. owner names the object in the duplicate-name message.
func editorAttributesUI(attrs *world.AttrSet, idSuffix, header, owner string) {
	if !imgui.CollapsingHeaderV(header+"##attrs"+idSuffix, imgui.TreeNodeFlagsDefaultOpen) {
		return
	}

	for _, name := range attrs.AttrNames() {
		if editorAttributeRow(attrs, idSuffix, name) {
			// The set was rewritten under the iteration; resume next frame.
			return
		}
	}
	if len(*attrs) == 0 {
		imgui.Text("(none)")
	}

	imgui.Separator()
	imgui.InputTextV("Name##newattr"+idSuffix, &Editor.NewAttrName, 0, nil)
	if imgui.BeginCombo("Type##newattr"+idSuffix, string(world.AttrTypeNames[editorNewAttrTypeIdx()])) {
		for i, t := range world.AttrTypeNames {
			if imgui.SelectableV(string(t), int32(i) == Editor.NewAttrType, 0, imgui.Vec2{}) {
				Editor.NewAttrType = int32(i)
			}
		}
		imgui.EndCombo()
	}
	if imgui.Button("Add Attribute##attradd"+idSuffix) && Editor.NewAttrName != "" {
		if _, exists := (*attrs)[Editor.NewAttrName]; exists {
			editorSetStatus("%s already has an attribute called %q", owner, Editor.NewAttrName)
		} else {
			EditorPushUndo()
			if *attrs == nil {
				*attrs = world.AttrSet{}
			}
			(*attrs)[Editor.NewAttrName] = world.ZeroAttr(world.AttrTypeNames[editorNewAttrTypeIdx()])
			Editor.NewAttrName = ""
		}
	}
}

// editorAttributeRow draws one attribute: its type combo, its value editor and
// its delete button. Reports whether the set was structurally changed, which
// ends the caller's iteration for this frame.
func editorAttributeRow(attrs *world.AttrSet, idSuffix, name string) bool {
	v := (*attrs)[name]
	imgui.PushID("attr_" + idSuffix + name)
	defer imgui.PopID()

	imgui.Text(name)
	imgui.SameLine()
	if imgui.Button("x##del") {
		EditorPushUndo()
		delete(*attrs, name)
		return true
	}

	imgui.SetNextItemWidth(90)
	if imgui.BeginCombo("##type", string(v.Type)) {
		for _, t := range world.AttrTypeNames {
			if imgui.SelectableV(string(t), t == v.Type, 0, imgui.Vec2{}) && t != v.Type {
				// Converted rather than reset: picking float after typing 25
				// should not throw the 25 away. See ConvertAttr.
				EditorPushUndo()
				(*attrs)[name] = world.ConvertAttr(v, t)
			}
		}
		imgui.EndCombo()
	}
	imgui.SameLine()
	editorAttributeValue(attrs, name, v)
	return false
}

// editorAttributeValue draws the value editor matching an attribute's type.
func editorAttributeValue(attrs *world.AttrSet, name string, v world.AttrValue) {
	switch v.Type {
	case world.AttrInt:
		n := int32(v.I)
		imgui.SetNextItemWidth(-1)
		if imgui.DragIntV("##val", &n, 1, -1<<30, 1<<30, "%d", 0) {
			(*attrs)[name] = world.IntAttr(int64(n))
		}
	case world.AttrFloat:
		f := float32(v.F)
		imgui.SetNextItemWidth(-1)
		if imgui.DragFloatV("##val", &f, 0.01, -1e9, 1e9, "%.4f", 0) {
			(*attrs)[name] = world.FloatAttr(float64(f))
		}
	case world.AttrBool:
		b := v.B
		if imgui.Checkbox("##val", &b) {
			(*attrs)[name] = world.BoolAttr(b)
		}
	case world.AttrString:
		str := v.S
		imgui.SetNextItemWidth(-1)
		if imgui.InputTextV("##val", &str, 0, nil) {
			(*attrs)[name] = world.StringAttr(str)
		}
	}
}

// editorScriptUI draws the "which .flang file drives this" picker.
//
// A combo over the scripts folder rather than a text field: a mistyped path is a
// script that silently never runs, which is among the harder things to notice.
// The list is rescanned when the section is opened rather than watched, matching
// how the entity catalog and the texture picker work.
func editorScriptUI(script *string, params *world.AttrSet, idSuffix, owner string) {
	if !imgui.CollapsingHeaderV("Script##script"+idSuffix, imgui.TreeNodeFlagsDefaultOpen) {
		return
	}

	if Editor.ScriptFileNames == nil {
		editorRefreshScriptFiles()
	}

	label := *script
	if label == "" {
		label = "(none)"
	}
	if imgui.BeginCombo("File##scriptfile"+idSuffix, label) {
		if imgui.SelectableV("(none)", *script == "", 0, imgui.Vec2{}) && *script != "" {
			EditorPushUndo()
			*script = ""
		}
		for _, name := range Editor.ScriptFileNames {
			path := fileutil.ScriptsDirName + "/" + name
			if imgui.SelectableV(name, *script == path, 0, imgui.Vec2{}) && *script != path {
				EditorPushUndo()
				*script = path
			}
		}
		imgui.EndCombo()
	}
	imgui.SameLine()
	if imgui.Button("Rescan##scriptrescan" + idSuffix) {
		editorRefreshScriptFiles()
	}

	if *script != "" {
		editorTextWrapped(fmt.Sprintf("%s runs %s", owner, *script))
		editorScriptParamsUI(*script, params, idSuffix)
	}
}

// editorScriptParamsUI draws one row per `param` the attached script declares —
// the wiring that makes a shared script specific to this object.
//
// Drawn inside the Script section rather than beside Attributes, because a param
// only means anything in the context of the file that declares it: change the
// script and the whole list changes with it. An object with no script has no
// parameters to show, which is why the caller checks first.
//
// The declarations come from the script, the values from the object, and the two
// can disagree — a param renamed after a level bound it leaves a value nothing
// reads. Those are listed at the bottom rather than deleted; see
// scriptParamsPrune.
func editorScriptParamsUI(script string, params *world.AttrSet, idSuffix string) {
	if params == nil {
		return
	}
	decls := scriptParamDecls(script)
	if len(decls) == 0 && len(*params) == 0 {
		return
	}

	imgui.Separator()
	imgui.Text("Parameters")
	if len(decls) == 0 {
		editorTextMutedWrapped(script + " declares no parameters.")
	}

	// Built once for the whole section rather than per row: every `ref` row
	// shares the list, and rebuilding it per row would walk the level once per
	// parameter, every frame.
	var refTargets []scriptRefTarget
	for _, d := range decls {
		if d.Type == "ref" {
			refTargets = scriptRefTargets(Editor.level)
			break
		}
	}

	for _, d := range decls {
		editorScriptParamRow(d, params, refTargets, idSuffix)
	}

	unused := scriptParamsUnused(*params, script)
	if len(unused) == 0 {
		return
	}
	imgui.Separator()
	editorTextMutedWrapped(fmt.Sprintf("%s does not declare: %s", script, strings.Join(unused, ", ")))
	if imgui.Button("Clear Unused##paramprune" + idSuffix) {
		EditorPushUndo()
		scriptParamsPrune(params, script)
	}
}

// editorScriptParamRow draws one parameter: its name, its value editor, and the
// button that hands it back to the script's default.
func editorScriptParamRow(d *flang.ParamDecl, params *world.AttrSet, refTargets []scriptRefTarget, idSuffix string) {
	imgui.PushID("param_" + idSuffix + d.Name)
	defer imgui.PopID()

	cur, bound := scriptParamValue(*params, d)

	imgui.Text("@" + d.Name)
	imgui.SameLine()
	editorTextMuted(d.Type)
	if !bound {
		// Said out loud, because the widget below shows a value either way and
		// an author has no other way to tell a default from a choice.
		imgui.SameLine()
		editorTextMuted("(default)")
	} else {
		imgui.SameLine()
		if imgui.Button("x##paramclear") {
			EditorPushUndo()
			delete(*params, d.Name)
			return
		}
	}

	var next world.AttrValue
	changed := false
	if d.Type == "ref" {
		next, changed = editorScriptParamRefValue(cur, refTargets)
	} else {
		next, changed = editorScriptParamScalarValue(cur)
	}
	if !changed {
		return
	}
	// Undo is pushed for the edits that land in one step — binding a param for
	// the first time, picking an object, flipping a bool. A drag is deliberately
	// left out, the same way an attribute's is: pushing per frame would fill the
	// stack with the frames of one gesture and leave nothing to undo *to*.
	if !bound || d.Type == "ref" || d.Type == "bool" {
		EditorPushUndo()
	}
	if *params == nil {
		*params = world.AttrSet{}
	}
	(*params)[d.Name] = next
}

// editorScriptParamRefValue draws the object picker for a `ref` parameter.
//
// A combo over the level's IDs rather than a text field, for the same reason the
// script picker is a combo: a mistyped ID is a param that silently reads null,
// and null is indistinguishable from "not wired up yet".
func editorScriptParamRefValue(cur world.AttrValue, targets []scriptRefTarget) (world.AttrValue, bool) {
	id := cur.S
	imgui.SetNextItemWidth(-1)
	if !imgui.BeginCombo("##paramref", scriptRefLabel(targets, id)) {
		return cur, false
	}
	defer imgui.EndCombo()

	changed := false
	out := cur
	if imgui.SelectableV("(none)", id == "", 0, imgui.Vec2{}) && id != "" {
		out, changed = world.StringAttr(""), true
	}
	for _, t := range targets {
		if imgui.SelectableV(t.Label, t.ID == id, 0, imgui.Vec2{}) && t.ID != id {
			out, changed = world.StringAttr(t.ID), true
		}
	}
	return out, changed
}

// editorScriptParamScalarValue draws the value editor for the four scalar
// parameter types. Deliberately the same widgets an attribute gets — a float is
// a float, and two different ways to type one would be two things to learn.
func editorScriptParamScalarValue(cur world.AttrValue) (world.AttrValue, bool) {
	switch cur.Type {
	case world.AttrInt:
		n := int32(cur.I)
		imgui.SetNextItemWidth(-1)
		if imgui.DragIntV("##paramval", &n, 1, -1<<30, 1<<30, "%d", 0) {
			return world.IntAttr(int64(n)), true
		}
	case world.AttrFloat:
		f := float32(cur.F)
		imgui.SetNextItemWidth(-1)
		if imgui.DragFloatV("##paramval", &f, 0.01, -1e9, 1e9, "%.4f", 0) {
			return world.FloatAttr(float64(f)), true
		}
	case world.AttrBool:
		b := cur.B
		if imgui.Checkbox("##paramval", &b) {
			return world.BoolAttr(b), true
		}
	case world.AttrString:
		str := cur.S
		imgui.SetNextItemWidth(-1)
		if imgui.InputTextV("##paramval", &str, 0, nil) {
			return world.StringAttr(str), true
		}
	}
	return cur, false
}

// editorRefreshScriptFiles rescans the scripts folder. Called on first open and
// from the Rescan button — a script written while the editor was running should
// not need a restart to become selectable.
func editorRefreshScriptFiles() {
	Editor.ScriptFileNames = fileutil.ScanScriptFiles()
	// A rescan is also the moment to forget what each file declared: the reason
	// to press it is that the folder changed under the editor.
	ScriptParamsInvalidate("")
	if Editor.ScriptFileNames == nil {
		// Distinguish "scanned, found nothing" from "never scanned", so an empty
		// folder is not rescanned every frame.
		Editor.ScriptFileNames = []string{}
	}
}

// editorScriptDataUI draws all three sections for an object that carries a
// ScriptData — every scriptable thing except an entity, whose tags and
// attributes are merged from its definition and so are edited separately.
func editorScriptDataUI(d *world.ScriptData, idSuffix, owner string) {
	editorTagsUI(&d.Tags, idSuffix, "Tags")
	editorAttributesUI(&d.Attributes, idSuffix, "Attributes", owner)
	editorScriptUI(&d.Script, &d.ScriptParams, idSuffix, owner)
}
