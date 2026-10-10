package script

// script_aitree_test.go — a decision tree on a real entity in a real level:
// that it hears the engine's events, decides through the same host the script
// does, and reaches the script's own functions.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/aitree"
	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// writeTree puts a .fai in the temp resources folder and returns the bare name
// an entity would carry.
func writeTree(t *testing.T, name string, d *aitree.Doc) string {
	t.Helper()
	full := aitree.AIPath(name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(aitree.ToRaw(d))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return name
}

func treeCond(id, verb string, fields map[string]string, then, els string) *aitree.Node {
	return &aitree.Node{ID: id, Kind: aitree.NodeCondition, Then: then, Else: els,
		Check: aitree.Call{Verb: verb, Fields: fields}}
}

func treeAct(id, verb string, fields map[string]string, next string, args ...string) *aitree.Node {
	return &aitree.Node{ID: id, Kind: aitree.NodeAction, Next: next,
		Do: aitree.Call{Verb: verb, Fields: fields, Args: args}}
}

// treeEntity is a guard carrying a tree, a script, or both.
func treeEntity(id, script, tree string) world.Entity {
	e := world.NewBlankEntity(id)
	e.Position = mgl32.Vec3{4, 0, 4}
	e.Script, e.AITree = script, tree
	e.Attributes = world.AttrSet{"health": world.IntAttr(100)}
	return e
}

func TestATreeHearsAnEngineEventAndDecidesOnIt(t *testing.T) {
	withTempScriptDir(t)
	tree := writeTree(t, "guard", &aitree.Doc{ID: "guard", Trees: []*aitree.Tree{{
		Event: "spawn", Root: "n1", Nodes: []*aitree.Node{
			treeCond("n1", "attr", map[string]string{"attr": "health", "op": ">", "value": "50"}, "n2", "n3"),
			treeAct("n2", "set_attr", map[string]string{"attr": "verdict", "value": "\"fit\""}, ""),
			treeAct("n3", "set_attr", map[string]string{"attr": "verdict", "value": "\"hurt\""}, ""),
		},
	}}})

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, treeEntity("guard", "", tree))
	runTicks(t, lvl, 2)

	a, ok := lvl.Entities[0].Attr("verdict")
	if !ok || a.S != "fit" {
		t.Fatalf("verdict = %v (%v); :spawn: never reached the tree, or it took the wrong branch", a, ok)
	}
}

func TestATreeAndItsScriptShareTheAgentsState(t *testing.T) {
	root := withTempScriptDir(t)
	script := writeScript(t, root, "guard.flang", `
def on_tick(:tick:, dt float) when @self.state["armed"] == true -> null
    @self.attr.saw_it = 1
on_tick(:tick:, dt float)
    return null
end
`)
	tree := writeTree(t, "guard", &aitree.Doc{ID: "guard", Trees: []*aitree.Tree{{
		Event: "spawn", Root: "n1", Nodes: []*aitree.Node{
			treeAct("n1", "set", map[string]string{"var": "armed", "value": "true"}, ""),
		},
	}}})

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, treeEntity("guard", script, tree))
	runTicks(t, lvl, 3)

	if a, _ := lvl.Entities[0].Attr("saw_it"); a.I != 1 {
		t.Fatal("the script did not see the blackboard key the tree wrote; they are not sharing one state")
	}
}

func TestATreeCallsItsEntitysScript(t *testing.T) {
	root := withTempScriptDir(t)
	script := writeScript(t, root, "guard.flang", `
def double(n int) -> int
    return n * 2
end
`)
	tree := writeTree(t, "guard", &aitree.Doc{ID: "guard", Trees: []*aitree.Tree{{
		Event: "spawn", Root: "n1", Nodes: []*aitree.Node{
			treeAct("n1", "call", map[string]string{"function": "double", "into": "answer"}, "n2", "21"),
			treeAct("n2", "set_attr", map[string]string{"attr": "answer", "value": "@self.state[\"answer\"]"}, ""),
		},
	}}})

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, treeEntity("guard", script, tree))
	runTicks(t, lvl, 2)

	if a, _ := lvl.Entities[0].Attr("answer"); a.I != 42 {
		t.Fatalf("answer = %v; the tree did not reach its script's function", a)
	}
}

func TestATreesParameterIsSetByTheEntitysParamsBlock(t *testing.T) {
	withTempScriptDir(t)
	tree := writeTree(t, "guard", &aitree.Doc{ID: "guard",
		Params: []aitree.Param{{Name: "damage", Type: "int", Value: "12"}},
		Trees: []*aitree.Tree{{Event: "spawn", Root: "n1", Nodes: []*aitree.Node{
			treeAct("n1", "set_attr", map[string]string{"attr": "dmg", "value": "?damage"}, ""),
		}}}})

	lvl := tickTestLevel(t)
	e := treeEntity("guard", "", tree)
	lvl.Entities = append(lvl.Entities, e)
	runTicks(t, lvl, 2)
	if a, _ := lvl.Entities[0].Attr("dmg"); a.I != 12 {
		t.Fatalf("the declared default did not reach ?damage: %v", a)
	}

	lvl2 := tickTestLevel(t)
	e2 := treeEntity("guard", "", tree)
	e2.ScriptParams = world.AttrSet{"damage": world.IntAttr(30)}
	lvl2.Entities = append(lvl2.Entities, e2)
	runTicks(t, lvl2, 2)
	if a, _ := lvl2.Entities[0].Attr("dmg"); a.I != 30 {
		t.Fatalf("the placement's params: block did not override the tree's default: %v", a)
	}
}

func TestATreeSendsAMessageThatArrivesLikeAnyOther(t *testing.T) {
	root := withTempScriptDir(t)
	script := writeScript(t, root, "target.flang", `
def on_poke(:poke:, n int) -> null
    @self.attr.poked = n
end
`)
	tree := writeTree(t, "guard", &aitree.Doc{ID: "guard", Trees: []*aitree.Tree{{
		Event: "spawn", Root: "n1", Nodes: []*aitree.Node{
			treeAct("n1", "send", map[string]string{"to": "@crate", "event": "poke"}, "", "7"),
		},
	}}})

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities,
		treeEntity("guard", "", tree),
		func() world.Entity { e := world.NewBlankEntity("crate"); e.Script = script; return e }())
	runTicks(t, lvl, 3)

	if a, _ := lvl.Entities[1].Attr("poked"); a.I != 7 {
		t.Fatalf("the crate was not poked: %v", a)
	}
}

func TestATreeAndAScriptBothAnswerTheSameEventWithTheTreeFirst(t *testing.T) {
	root := withTempScriptDir(t)
	script := writeScript(t, root, "guard.flang", `
def on_spawn(:spawn:) -> null
    @self.attr.order = @self.attr.order + "script"
end
`)
	tree := writeTree(t, "guard", &aitree.Doc{ID: "guard", Trees: []*aitree.Tree{{
		Event: "spawn", Root: "n1", Nodes: []*aitree.Node{
			treeAct("n1", "set_attr", map[string]string{"attr": "order", "value": "\"tree \""}, ""),
		},
	}}})

	lvl := tickTestLevel(t)
	e := treeEntity("guard", script, tree)
	e.Attributes["order"] = world.StringAttr("")
	lvl.Entities = append(lvl.Entities, e)
	runTicks(t, lvl, 2)

	if a, _ := lvl.Entities[0].Attr("order"); a.S != "tree script" {
		t.Fatalf("order = %q; deciding comes before acting", a.S)
	}
}

func TestAnEntityWithATreeAndNoScriptStillGetsSpawnAndTick(t *testing.T) {
	withTempScriptDir(t)
	tree := writeTree(t, "guard", &aitree.Doc{ID: "guard", Trees: []*aitree.Tree{{
		Event: "tick", Args: []string{"dt"}, Root: "n1", Nodes: []*aitree.Node{
			treeAct("n1", "set_attr", map[string]string{"attr": "ticks", "op": "+=", "value": "1"}, ""),
		},
	}}})

	lvl := tickTestLevel(t)
	e := treeEntity("guard", "", tree)
	e.Attributes["ticks"] = world.IntAttr(0)
	lvl.Entities = append(lvl.Entities, e)
	runTicks(t, lvl, 4)

	if a, _ := lvl.Entities[0].Attr("ticks"); a.I != 4 {
		t.Fatalf("ticks = %v, wanted 4; an entity with only a tree is still scripted", a)
	}
}

func TestADocumentThatWillNotLoadIsReportedAndTheGameCarriesOn(t *testing.T) {
	withTempScriptDir(t)
	full := aitree.AIPath("broken")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("id: broken\ntrees: [not a list of maps]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, treeEntity("guard", "", "broken"))
	runTicks(t, lvl, 2)
	if AITreeLoaded("broken") {
		t.Fatal("a document that would not parse should be disabled")
	}
}

func TestAMissingDocumentIsAnErrorRatherThanASilentNothing(t *testing.T) {
	withTempScriptDir(t)
	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, treeEntity("guard", "", "nowhere"))
	runTicks(t, lvl, 2)
	found := false
	for _, e := range ScriptLog.Snapshot() {
		if strings.Contains(e.Source, "nowhere") {
			found = true
		}
	}
	if !found {
		t.Fatal("nothing was logged about a tree that is not there")
	}
}

func TestAITreeUsageNamesThePlacementsThatCarryADocument(t *testing.T) {
	lvl := &world.Level{Entities: []world.Entity{
		treeEntity("a", "", "soldier"),
		treeEntity("b", "", "soldier"),
		treeEntity("c", "", "other"),
	}}
	got := AITreeUsage(lvl, "soldier")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v", got)
	}
}

func TestScriptFunctionNamesAreOfferedForACallNode(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "guard.flang", `
def alpha() -> null
    return null
end
def beta(n int) -> int
    return n
end
`)
	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, treeEntity("guard", path, ""))
	runTicks(t, lvl, 1)
	got := ScriptFunctionNames(path)
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("got %v", got)
	}
}

// ---------- a tree may not be given two of the same parameter ----------

func TestAParameterDeclaredByBothTheTreeAndItsScriptIsReported(t *testing.T) {
	root := withTempScriptDir(t)
	script := writeScript(t, root, "guard.flang", "param damage int = 5\n")
	tree := writeTree(t, "guard", &aitree.Doc{ID: "guard",
		Params: []aitree.Param{{Name: "damage", Type: "int", Value: "12"}},
		Trees: []*aitree.Tree{{Event: "spawn", Root: "n1", Nodes: []*aitree.Node{
			treeAct("n1", "stop", nil, ""),
		}}}})

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities, treeEntity("guard", script, tree))
	runTicks(t, lvl, 1)

	found := false
	for _, d := range ScriptDiagnostics(script) {
		if strings.Contains(d.Msg, "damage") && strings.Contains(d.Msg, "already declared") {
			found = true
		}
	}
	if !found {
		t.Fatalf("wanted a duplicate-parameter diagnostic, got %v", ScriptDiagnostics(script))
	}
}

var _ = flang.NullVal{}
var _ = fileutil.ResourcesPath
