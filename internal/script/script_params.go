package script

// script_params.go — script parameters: the tunables a .flang file declares and
// the level fills in.
//
// A script that names the object it acts on is a script for exactly one object.
// The whole point of `param door ref` is that the *file* stays generic and the
// *placement* carries the wiring: twenty buttons share one button.flang, and
// each one's panel says which door it opens. Inside the script the binding reads
// as `?door` — its own sigil, because a param is a question put to the level and
// `@door` is an assertion that the level contains an object of that name.
//
// Three pieces meet here:
//
//   - The declaration lives in the script (internal/flang's ParamDecl) and says
//     the name, the type and the fallback.
//   - The binding lives on the object, in ScriptData.ScriptParams or
//     Entity.ScriptParams, as an AttrValue — so it saves, loads, clones and
//     undoes with everything else the object carries, at no extra cost.
//   - The lookup happens per read, in levelHost.Param, against whichever object
//     is currently `@self`.
//
// A `ref` param stores the ID of the object it names, never an index or a
// pointer, for the reason script_ref.go gives: undo rebuilds the level from
// RawLevel, so anything but a name is stale the moment Ctrl+Z lands.

import (
	"sort"

	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/world"
)

// ---------- declarations ----------

// scriptParamDecls returns the parameters a script declares, in source order.
//
// It reads through the runtime's loaded units when it can, so the editor and the
// running game agree on what a script declares. A path the runtime has not
// loaded — the usual case in the editor, where nothing has been attached yet —
// is parsed on demand and cached.
func ScriptParamDecls(path string) []*flang.ParamDecl {
	if path == "" {
		return nil
	}
	if u, ok := Scripts.units[path]; ok && u.prog != nil {
		return u.prog.ParamList()
	}
	return scriptParamCacheGet(path)
}

// scriptParamCache holds declarations for scripts the runtime has not loaded.
// Cleared by ScriptParamsInvalidate whenever a script is saved or the folder is
// rescanned, which is the only way the answer can change.
var scriptParamCache = map[string][]*flang.ParamDecl{}

func scriptParamCacheGet(path string) []*flang.ParamDecl {
	if decls, ok := scriptParamCache[path]; ok {
		return decls
	}
	var decls []*flang.ParamDecl
	if src, err := ReadScriptFile(fileutil.ScriptPath(path)); err == nil {
		// Linked rather than merely parsed, so a param an imported library
		// declares shows up in the panel too — which is the point of libraries.
		// Diagnostics are dropped here: a broken script's errors belong in the
		// log window, not in a panel that is only asking what it can configure.
		if prog, _ := flang.Link(fileutil.ScriptPath(path), src, scriptLoader{}); prog != nil {
			decls = prog.ParamList()
		}
	}
	// Cached even when empty, so a script with no params is not re-read every
	// frame the panel is open.
	scriptParamCache[path] = decls
	return decls
}

// ScriptParamsInvalidate drops the cached declarations for one script, or for
// every script when path is empty. Called when a script is saved and when the
// scripts folder is rescanned.
func ScriptParamsInvalidate(path string) {
	if path == "" {
		scriptParamCache = map[string][]*flang.ParamDecl{}
		return
	}
	delete(scriptParamCache, path)
}

// ---------- bindings ----------

// scriptParamsFor returns the parameter bindings an object carries, ready to be
// written through. Entities keep theirs outside a ScriptData — their params are
// merged from a definition, which EntityToRaw depends on — so they are handled
// separately, exactly as their attributes are.
func (rt *ScriptRuntime) scriptParamsFor(r flang.RefVal) *world.AttrSet {
	if rt.host == nil {
		return nil
	}
	if e := rt.host.entityFor(r); e != nil {
		return &e.ScriptParams
	}
	if d := rt.cache.scriptDataFor(r); d != nil {
		return &d.ScriptParams
	}
	return nil
}

// Param is flang.Host's side of the lookup: what the object running this script
// bound to one of its parameters.
//
// Only the binding is read here. The declaration's default and the str-to-ref
// step are the interpreter's, in paramValue — it holds the declarations, and
// resolving there means one implementation rather than one per host.
func (h *levelHost) Param(name string) (flang.Value, bool) {
	if h.rt == nil {
		return nil, false
	}
	params := h.rt.scriptParamsFor(h.rt.self)
	if params == nil {
		return nil, false
	}
	a, ok := params.Attr(name)
	if !ok {
		return nil, false
	}
	return attrToValue(a), true
}

// ---------- editor support ----------

// scriptParamAttrType maps a declared param type onto the storage that holds it.
// A `ref` is stored as the ID it names, which is a string.
func scriptParamAttrType(declType string) world.AttrType {
	switch declType {
	case "int":
		return world.AttrInt
	case "float":
		return world.AttrFloat
	case "bool":
		return world.AttrBool
	}
	// "str" and "ref" both.
	return world.AttrString
}

// scriptParamDefault converts a declaration's default into the attribute the
// editor should show when nothing has been bound. The bool is false when the
// declaration has no default, which reads as null.
func scriptParamDefault(d *flang.ParamDecl) (world.AttrValue, bool) {
	v, ok := d.DefaultValue()
	if !ok {
		return world.AttrValue{}, false
	}
	a, ok := valueToAttr(v)
	if !ok {
		return world.AttrValue{}, false
	}
	return world.ConvertAttr(a, scriptParamAttrType(d.Type)), true
}

// scriptParamValue returns the value to show for one declared param: what the
// object bound, else the declaration's default, else the type's zero. The bool
// reports whether the object bound it — which is what the panel needs to say
// "(default)" rather than presenting a fallback as a choice.
func ScriptParamValue(params world.AttrSet, d *flang.ParamDecl) (world.AttrValue, bool) {
	want := scriptParamAttrType(d.Type)
	if a, ok := params.Attr(d.Name); ok {
		// Converted rather than trusted: a declaration whose type changed after
		// a level bound a value would otherwise hand the widget the wrong kind.
		return world.ConvertAttr(a, want), true
	}
	if a, ok := scriptParamDefault(d); ok {
		return a, false
	}
	return world.ZeroAttr(want), false
}

// scriptParamsPrune drops bindings for names the script no longer declares,
// reporting how many went.
//
// Not automatic. A param renamed in the script would silently take its level
// wiring with it, and the author is better served by a panel that says "3
// unused" with a button than by one that quietly discards what they wrote.
func ScriptParamsPrune(params *world.AttrSet, path string) int {
	if params == nil || len(*params) == 0 {
		return 0
	}
	declared := map[string]bool{}
	for _, d := range ScriptParamDecls(path) {
		declared[d.Name] = true
	}
	n := 0
	for name := range *params {
		if !declared[name] {
			delete(*params, name)
			n++
		}
	}
	return n
}

// scriptParamsUnused names the bindings an object carries that its script does
// not declare, sorted. Shown in the panel rather than deleted — see above.
func ScriptParamsUnused(params world.AttrSet, path string) []string {
	if len(params) == 0 {
		return nil
	}
	declared := map[string]bool{}
	for _, d := range ScriptParamDecls(path) {
		declared[d.Name] = true
	}
	var out []string
	for name := range params {
		if !declared[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// ---------- ref params ----------

// scriptRefTarget is one object a `ref` param could name: the ID that gets
// stored, and the label the picker shows.
type ScriptRefTarget struct {
	ID    string
	Label string
}

// scriptRefTargets returns every object in the level a `ref` param could name,
// in a stable order: entities, volumes, sprites, emitters, sounds, waypoint
// animations, then chunks with their brushes, then lights — the kinds and order
// CompletionRefs walks, since both are the set refFor resolves.
//
// Grouped by kind rather than sorted alphabetically, because "which one did I
// mean" is a question an author answers by kind first — a level with forty
// sector IDs interleaved among its entities is a list nobody can pick from.
//
// The kind is read off the walk rather than looked up per entry: labelling each
// ID through refFor would rebuild or re-search the index once per row, which is
// quadratic in a list the panel redraws every frame.
func ScriptRefTargets(lvl *world.Level) []ScriptRefTarget {
	if lvl == nil {
		return nil
	}
	var out []ScriptRefTarget
	add := func(id, kind string) {
		if id != "" {
			out = append(out, ScriptRefTarget{ID: id, Label: id + "  (" + kind + ")"})
		}
	}
	for i := range lvl.Entities {
		add(lvl.Entities[i].ID, "entity")
	}
	for i := range lvl.Volumes {
		add(lvl.Volumes[i].ID, "volume")
	}
	for i := range lvl.Sprites {
		add(lvl.Sprites[i].ID, "sprite")
	}
	for i := range lvl.Emitters {
		add(lvl.Emitters[i].ID, "emitter")
	}
	for i := range lvl.Sounds {
		add(lvl.Sounds[i].ID, "sound")
	}
	for i := range lvl.WaypointAnimations {
		add(lvl.WaypointAnimations[i].ID, "waypoints")
	}
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		add(ch.ID, "chunk")
		for bi := range ch.Brushes {
			add(ch.Brushes[bi].ID, "brush")
		}
	}
	for i := range lvl.Lights.Directional {
		add(lvl.Lights.Directional[i].ID, "light")
	}
	for i := range lvl.Lights.Points {
		add(lvl.Lights.Points[i].ID, "light")
	}
	return out
}

// scriptRefLabel describes the currently bound ID, given the list the picker is
// already showing.
//
// An ID nothing in the list matches is shown as missing rather than blanked. A
// param pointing at a deleted object is exactly what an author needs to see, and
// blanking it would look like they had never set one.
func ScriptRefLabel(targets []ScriptRefTarget, id string) string {
	if id == "" {
		return "(none)"
	}
	for _, t := range targets {
		if t.ID == id {
			return t.Label
		}
	}
	return id + "  (missing)"
}
