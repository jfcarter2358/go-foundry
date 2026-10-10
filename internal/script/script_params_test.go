package script

// script_params_test.go — script parameters against a real Level.
//
// internal/flang's param_test.go proves the language half: what `@name` reads
// and in what order. These prove the binding half — that a param bound on one
// object is not visible from another, that it survives a save and a load, and
// that an entity's comes from its definition unless the instance says otherwise.

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/testenv"
	"foundry/internal/world"
)

// paramTestLevel builds two volumes running one script and two doors for them to
// point at — the arrangement the feature exists for.
func paramTestLevel(t *testing.T) *world.Level {
	t.Helper()
	testenv.WithoutGeometryUploads(t)

	lvl := &world.Level{}
	for _, id := range []string{"button_a", "button_b"} {
		v := world.NewVolume(id, mgl32.Vec3{0, 0, 0})
		v.HalfX, v.HalfY, v.HalfZ = 1, 1, 1
		v.Script = "scripts/button.flang"
		lvl.Volumes = append(lvl.Volumes, v)
	}
	lvl.Entities = append(lvl.Entities,
		world.Entity{ID: "door_north", Visible: true, Scale: mgl32.Vec3{1, 1, 1}},
		world.Entity{ID: "door_south", Visible: true, Scale: mgl32.Vec3{1, 1, 1}})
	return lvl
}

// Two objects running one script see their own bindings. This is the whole
// feature: without it, `button.flang` would be a script for one door.
func TestParamIsPerObject(t *testing.T) {
	lvl := paramTestLevel(t)
	lvl.Volumes[0].SetScriptParam("target", world.StringAttr("door_north"))
	lvl.Volumes[1].SetScriptParam("target", world.StringAttr("door_south"))

	src := `
param target ref

def main() -> str
    if ?target == null
        return "none"
    end
    return ?target.id
end
`
	if got := runScript(t, lvl, "button_a", src); got != flang.StrVal("door_north") {
		t.Errorf("button_a saw %v, want door_north", got)
	}
	if got := runScript(t, lvl, "button_b", src); got != flang.StrVal("door_south") {
		t.Errorf("button_b saw %v, want door_south", got)
	}
}

// An object that bound nothing gets the script's default, not the previous
// object's binding — the lookup is against `self`, not a runtime-wide table.
func TestParamUnboundFallsBackToDefault(t *testing.T) {
	lvl := paramTestLevel(t)
	lvl.Volumes[0].SetScriptParam("drop", world.FloatAttr(9.0))

	src := `
param drop float = 3.0

def main() -> float
    return ?drop
end
`
	if got := runScript(t, lvl, "button_a", src); got != flang.FloatVal(9.0) {
		t.Errorf("button_a saw %v, want its bound 9.0", got)
	}
	if got := runScript(t, lvl, "button_b", src); got != flang.FloatVal(3.0) {
		t.Errorf("button_b saw %v, want the declared 3.0", got)
	}
}

// A param may address anything a level can name, not just entities.
func TestRefParamReachesGeometry(t *testing.T) {
	lvl := hostTestLevel(t)
	brushID := lvl.Chunks[0].Brushes[0].ID
	lvl.Volumes[0].SetScriptParam("room", world.StringAttr(brushID))

	got := runScript(t, lvl, "trigger", `
param room ref

def main() -> bool
    return ?room.prop.solid
end
`)
	want := flang.BoolVal(lvl.Chunks[0].Brushes[0].Solid)
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

// A binding naming an object that has since been deleted reads as null rather
// than throwing, so the guard an author writes is the one that runs.
func TestRefParamToMissingObjectIsNull(t *testing.T) {
	lvl := paramTestLevel(t)
	lvl.Volumes[0].SetScriptParam("target", world.StringAttr("door_that_left"))

	got := runScript(t, lvl, "button_a", `
param target ref

def main() -> bool
    return ?target == null
end
`)
	if got != flang.BoolVal(true) {
		t.Errorf("got %v, want true", got)
	}
}

// A script writing its own param would be arguing with the level.
func TestParamWriteFromScriptFails(t *testing.T) {
	lvl := paramTestLevel(t)
	err := runScriptErr(t, lvl, "button_a", `
param drop float = 3.0

def main() -> null
    ?drop = 8.0
end
`)
	if !strings.Contains(err.Error(), "script parameter") {
		t.Errorf("error %q should say what ?drop is", err)
	}
}

// ---------- storage ----------

func TestScriptParamsRoundTripThroughYAML(t *testing.T) {
	d := world.ScriptData{
		Script:       "scripts/button.flang",
		ScriptParams: world.AttrSet{"target": world.StringAttr("door_north"), "drop": world.FloatAttr(2.5)},
	}
	back, err := world.ScriptDataFromRaw(world.ScriptDataToRaw(d), "a wall")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := back.ScriptParam("target"); !ok || v != world.StringAttr("door_north") {
		t.Errorf("target = %v (ok=%v)", v, ok)
	}
	if v, ok := back.ScriptParam("drop"); !ok || v != world.FloatAttr(2.5) {
		t.Errorf("drop = %v (ok=%v)", v, ok)
	}
}

// Params live under their own key, not among the attributes: an attribute is
// state the script writes, a param is configuration the level owns, and merging
// the two would let a script overwrite its own wiring.
func TestScriptParamsAreNotAttributes(t *testing.T) {
	d := world.ScriptData{ScriptParams: world.AttrSet{"target": world.StringAttr("door_north")}}
	raw := world.ScriptDataToRaw(d)
	if len(raw.Attributes) != 0 {
		t.Errorf("params leaked into attributes: %v", raw.Attributes)
	}
	if _, ok := raw.ScriptParams["target"]; !ok {
		t.Errorf("params key missing: %+v", raw)
	}
}

// A cloned chunk's bindings are its own, so retargeting the copy does not
// retarget the original.
func TestCloneScriptDataCopiesParams(t *testing.T) {
	d := world.ScriptData{ScriptParams: world.AttrSet{"target": world.StringAttr("door_north")}}
	c := world.CloneScriptData(d)
	c.SetScriptParam("target", world.StringAttr("door_south"))
	if v, _ := d.ScriptParam("target"); v != world.StringAttr("door_north") {
		t.Errorf("original changed to %v", v)
	}
}

// ---------- entities ----------

// An entity's params come from its definition and are overlaid per instance, the
// same way its attributes are — so a type can ship a scripted behaviour whose
// wiring each placed copy fills in.
func TestEntityParamsMergeFromDefinition(t *testing.T) {
	def := &world.EntityDef{
		Name:    "button",
		Script:  "scripts/button.flang",
		Visible: true,
		Params:  world.AttrSet{"drop": world.FloatAttr(3.0), "latching": world.BoolAttr(true)},
	}
	e, err := world.EntityFromRaw(world.RawEntity{
		Type:   "button",
		ID:     "button_a",
		Params: map[string]world.RawAttribute{"drop": {Type: "float", Value: "9.5"}},
	}, func(string) (*world.EntityDef, error) { return def, nil })
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := e.ScriptParams.Attr("drop"); v != world.FloatAttr(9.5) {
		t.Errorf("drop = %v, want the instance's 9.5", v)
	}
	if v, _ := e.ScriptParams.Attr("latching"); v != world.BoolAttr(true) {
		t.Errorf("latching = %v, want the definition's true", v)
	}

	// Only the difference is written back, so a definition that retunes its
	// default retunes every instance that never overrode it.
	raw := world.EntityToRaw(&e, def)
	if _, ok := raw.Params["latching"]; ok {
		t.Errorf("an inherited param was written back: %v", raw.Params)
	}
	if _, ok := raw.Params["drop"]; !ok {
		t.Errorf("the overridden param was not written back: %v", raw.Params)
	}
}

// ---------- editor helpers ----------

func declFor(t *testing.T, src string) []*flang.ParamDecl {
	t.Helper()
	f, errs := flang.Parse("t.flang", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	prog := flang.NewProgram()
	prog.Add(f)
	return prog.ParamList()
}

// The panel shows the bound value when there is one and the declared default
// when there is not — and says which, because a widget alone cannot.
func TestScriptParamValuePrefersBinding(t *testing.T) {
	decls := declFor(t, "param drop float = 3.0\n\ndef main() -> null\nend\n")
	if len(decls) != 1 {
		t.Fatalf("got %d decls", len(decls))
	}
	v, bound := ScriptParamValue(nil, decls[0])
	if bound || v != world.FloatAttr(3.0) {
		t.Errorf("unbound = %v (bound=%v), want the default 3.0", v, bound)
	}
	v, bound = ScriptParamValue(world.AttrSet{"drop": world.FloatAttr(1.5)}, decls[0])
	if !bound || v != world.FloatAttr(1.5) {
		t.Errorf("bound = %v (bound=%v), want 1.5", v, bound)
	}
}

// A param with no default still needs a value to draw, and the type's zero is
// the honest one.
func TestScriptParamValueZeroWithoutDefault(t *testing.T) {
	decls := declFor(t, "param count int\n\ndef main() -> null\nend\n")
	v, bound := ScriptParamValue(nil, decls[0])
	if bound || v != world.IntAttr(0) {
		t.Errorf("got %v (bound=%v), want 0", v, bound)
	}
}

// A ref param is stored as a string, whatever the level file happened to say, so
// the picker always has something to show.
func TestScriptParamValueCoercesToDeclaredType(t *testing.T) {
	decls := declFor(t, "param target ref\n\ndef main() -> null\nend\n")
	v, bound := ScriptParamValue(world.AttrSet{"target": world.IntAttr(7)}, decls[0])
	if !bound || v.Type != world.AttrString {
		t.Errorf("got %v (type %v), want a str", v, v.Type)
	}
}

// A binding whose declaration went away is reported, not silently dropped: a
// renamed param would otherwise take its level wiring with it.
func TestScriptParamsUnusedAndPrune(t *testing.T) {
	prev := scriptParamCache
	t.Cleanup(func() { scriptParamCache = prev })
	scriptParamCache = map[string][]*flang.ParamDecl{
		"scripts/x.flang": declFor(t, "param target ref\n\ndef main() -> null\nend\n"),
	}

	params := world.AttrSet{"target": world.StringAttr("door_north"), "old_name": world.IntAttr(1)}
	unused := ScriptParamsUnused(params, "scripts/x.flang")
	if len(unused) != 1 || unused[0] != "old_name" {
		t.Fatalf("unused = %v, want [old_name]", unused)
	}
	if n := ScriptParamsPrune(&params, "scripts/x.flang"); n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}
	if _, ok := params["target"]; !ok {
		t.Errorf("prune removed a declared param")
	}
	if _, ok := params["old_name"]; ok {
		t.Errorf("prune left the undeclared param")
	}
}

// The picker lists every kind a ref can name, grouped, and labels each with what
// it is — two objects that read alike still have to be tellable apart.
func TestScriptRefTargets(t *testing.T) {
	lvl := hostTestLevel(t)
	lvl.Sprites = append(lvl.Sprites, world.Sprite{ID: "glow", Width: 1, Height: 1, Visible: true})
	lvl.Emitters = append(lvl.Emitters, world.Emitter{ID: "sparks"})
	lvl.Sounds = append(lvl.Sounds, world.Sound{ID: "hum"})
	lvl.WaypointAnimations = append(lvl.WaypointAnimations, world.WaypointAnimation{ID: "rise"})
	targets := ScriptRefTargets(lvl)

	var ids []string
	for _, tg := range targets {
		ids = append(ids, tg.ID)
	}
	for _, want := range []string{"crate", "trigger", "glow", "sparks", "hum", "rise", "chunk", "room", "lamp", "sun"} {
		if !containsString(ids, want) {
			t.Errorf("%q missing from %v", want, ids)
		}
	}
	if ids[0] != "crate" {
		t.Errorf("entities should come first, got %v", ids)
	}
	if got := ScriptRefLabel(targets, "rise"); !strings.Contains(got, "waypoints") {
		t.Errorf("label %q should name the kind", got)
	}

	// The picker and `@name` completion both offer the set refFor resolves. The
	// picker once walked a shorter list, and sprites, emitters, sounds and
	// waypoints could be addressed in a script but never bound to a param.
	// Screens and widgets are left out: a param names a level object.
	for _, r := range CompletionRefs(lvl) {
		if r.Kind == flang.RefScreen || r.Kind == flang.RefWidget {
			continue
		}
		if !containsString(ids, r.ID) {
			t.Errorf("%q (%v) completes as @name but is missing from the ref picker", r.ID, r.Kind)
		}
	}
	if got := ScriptRefLabel(targets, "crate"); !strings.Contains(got, "entity") {
		t.Errorf("label %q should name the kind", got)
	}
	if got := ScriptRefLabel(targets, "gone"); !strings.Contains(got, "missing") {
		t.Errorf("label %q should flag a dangling binding", got)
	}
	if got := ScriptRefLabel(targets, ""); got != "(none)" {
		t.Errorf("empty label = %q", got)
	}
}

// TODO(brush migration): the shipped resources/scripts/button.flang example
// raises and lowers a sector floor (`@target.floor.prop.height`), a property
// brushes do not have — a brush's shape is its verts, not a height field. The
// example and the test that ran it both need rewriting once brushes grow a
// way to be moved from a script.
