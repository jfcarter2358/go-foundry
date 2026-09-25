package aitree

import (
	"strings"
	"testing"

	"foundry/internal/flang"
)

// ---------- a world to decide in ----------

// harness is a document compiled and wired to a MapHost, which is everything a
// tree needs and nothing a level would bring.
type harness struct {
	t    *testing.T
	host *flang.MapHost
	run  *Runner
	prog *Program
}

func newHarness(t *testing.T, d *Doc, script string) *harness {
	t.Helper()
	prog := Compile(d)
	if errs := prog.Errors(); len(errs) > 0 {
		t.Fatalf("compile: %v", errs)
	}
	host := flang.NewMapHost()
	guard := host.AddObject("guard", flang.RefEntity, "enemy")
	guard.Attrs = map[string]flang.Value{"health": flang.IntVal(100)}
	guard.Props = map[string]flang.Value{
		"position":  flang.VecVal{0, 0, 0},
		"ai_target": flang.NullVal{},
		"ai_state":  flang.AtomVal("idle"),
	}
	host.SetSelf("guard")

	src := script
	if prog.ParamSource != "" {
		src = prog.ParamSource + "\n" + src
	}
	fprog, errs := flang.Link("test.flang", []byte(src), nil)
	if len(errs) > 0 {
		t.Fatalf("script: %v", errs)
	}
	in, ierrs := flang.NewInterp(fprog, host, flang.Limits{})
	if len(ierrs) > 0 {
		t.Fatalf("interp: %v", ierrs)
	}
	return &harness{t: t, host: host, prog: prog,
		run: &Runner{Prog: prog, Interp: in, Host: host, Self: guard.Ref}}
}

func (h *harness) fire(event string, args ...flang.Value) bool {
	h.t.Helper()
	handled, err := h.run.Fire(event, args)
	if err != nil {
		h.t.Fatalf("fire %s: %v", event, err)
	}
	return handled
}

func (h *harness) state(key string) flang.Value {
	return h.host.State(h.run.Self).Get(key)
}

func (h *harness) obj(id string) *flang.MapObject { return h.host.Objects[id] }

// node builders, so a test reads as the tree it draws.
func cond(id, verb string, fields map[string]string, then, els string) *Node {
	return &Node{ID: id, Kind: NodeCondition, Then: then, Else: els,
		Check: Call{Verb: verb, Fields: fields}}
}

func act(id, verb string, fields map[string]string, next string, args ...string) *Node {
	return &Node{ID: id, Kind: NodeAction, Next: next,
		Do: Call{Verb: verb, Fields: fields, Args: args}}
}

// ---------- the walk ----------

func TestAConditionTakesTheBranchItAnswers(t *testing.T) {
	d := &Doc{ID: "t", Trees: []*Tree{{
		Event: "damage", Args: []string{"src", "amount"}, Root: "n1",
		Nodes: []*Node{
			cond("n1", "attr", map[string]string{"attr": "health", "op": ">", "value": "50"}, "n2", "n3"),
			act("n2", "set", map[string]string{"var": "verdict", "value": "\"fine\""}, ""),
			act("n3", "set", map[string]string{"var": "verdict", "value": "\"hurt\""}, ""),
		},
	}}}
	h := newHarness(t, d, "")
	h.fire("damage", flang.NullVal{}, flang.IntVal(10))
	if got := h.state("verdict").String(); got != "fine" {
		t.Fatalf("with 100 health: %q", got)
	}
	h.obj("guard").Attrs["health"] = flang.IntVal(20)
	h.fire("damage", flang.NullVal{}, flang.IntVal(10))
	if got := h.state("verdict").String(); got != "hurt" {
		t.Fatalf("with 20 health: %q", got)
	}
}

func TestAnEventsArgumentsAreBoundByName(t *testing.T) {
	d := &Doc{ID: "t", Trees: []*Tree{{
		Event: "damage", Args: []string{"src", "amount"}, Root: "n1",
		Nodes: []*Node{
			act("n1", "set_attr", map[string]string{"attr": "health", "op": "-=", "value": "amount"}, ""),
		},
	}}}
	h := newHarness(t, d, "")
	h.fire("damage", flang.NullVal{}, flang.IntVal(30))
	if got := h.obj("guard").Attrs["health"].String(); got != "70" {
		t.Fatalf("health %q; the amount the event carried did not reach the write", got)
	}
}

func TestAnEventWithNoRootIsNotHandled(t *testing.T) {
	d := &Doc{ID: "t", Trees: []*Tree{{Event: "damage", Root: "n1",
		Nodes: []*Node{act("n1", "stop", nil, "")}}}}
	h := newHarness(t, d, "")
	if h.fire("suppressed") {
		t.Fatal("a document that says nothing about :suppressed: reported handling it")
	}
	if !h.fire("damage") {
		t.Fatal("the root it does have did not report handling its event")
	}
}

func TestAChainOfActionsRunsInOrder(t *testing.T) {
	d := &Doc{ID: "t", Trees: []*Tree{{Event: "spawn", Root: "a",
		Nodes: []*Node{
			act("a", "set", map[string]string{"var": "n", "op": "=", "value": "1"}, "b"),
			act("b", "set", map[string]string{"var": "n", "op": "+=", "value": "2"}, "c"),
			act("c", "set", map[string]string{"var": "n", "op": "*=", "value": "10"}, ""),
		},
	}}}
	h := newHarness(t, d, "")
	h.fire("spawn")
	if got := h.state("n").String(); got != "30" {
		t.Fatalf("n = %s, wanted 30", got)
	}
}

func TestACounterOnAKeyNothingHasWrittenStartsAtZero(t *testing.T) {
	d := &Doc{ID: "t", Trees: []*Tree{{Event: "spawn", Root: "a",
		Nodes: []*Node{act("a", "set", map[string]string{"var": "dash", "op": "+=", "value": "1"}, "")}}}}
	h := newHarness(t, d, "")
	h.fire("spawn")
	if got := h.state("dash"); got.String() != "1" || got.Type() != flang.TypeInt {
		t.Fatalf("dash = %v (%s); an unwritten key counts as zero and stays an int", got, got.Type())
	}
}

func TestATreeThatLoopsBackIntoItselfIsStoppedRatherThanHanging(t *testing.T) {
	d := &Doc{ID: "t", Trees: []*Tree{{Event: "spawn", Root: "a",
		Nodes: []*Node{
			act("a", "set", map[string]string{"var": "n", "op": "+=", "value": "1"}, "b"),
			act("b", "set", map[string]string{"var": "m", "op": "+=", "value": "1"}, "a"),
		},
	}}}
	h := newHarness(t, d, "")
	h.run.MaxSteps = 20
	_, err := h.run.Fire("spawn", nil)
	if err == nil || !strings.Contains(err.Error(), "wired back round") {
		t.Fatalf("wanted a loop complaint, got %v", err)
	}
}

// ---------- the vocabulary ----------

func TestBlackboardAndBlankKeys(t *testing.T) {
	d := &Doc{ID: "t", Trees: []*Tree{{Event: "spawn", Root: "n1",
		Nodes: []*Node{
			cond("n1", "var", map[string]string{"var": "dead", "op": "is false"}, "n2", "n3"),
			act("n2", "set", map[string]string{"var": "answer", "value": "\"alive\""}, ""),
			act("n3", "set", map[string]string{"var": "answer", "value": "\"dead\""}, ""),
		},
	}}}
	h := newHarness(t, d, "")
	h.fire("spawn")
	if got := h.state("answer").String(); got != "alive" {
		t.Fatalf("a key nothing has written must read as false, got %q", got)
	}
}

func TestCooldownIsReadyBeforeItHasEverBeenStamped(t *testing.T) {
	d := &Doc{ID: "t", Trees: []*Tree{{Event: "spawn", Root: "n1",
		Nodes: []*Node{
			cond("n1", "cooldown", map[string]string{"var": "cover_at", "seconds": "7.0"}, "n2", "n3"),
			act("n2", "set", map[string]string{"var": "ready", "value": "true"}, ""),
			act("n3", "set", map[string]string{"var": "ready", "value": "false"}, ""),
		},
	}}}
	h := newHarness(t, d, "")
	h.host.Verbs["time"] = func(args []flang.Value, pos flang.Pos) (flang.Value, error) {
		return flang.FloatVal(100), nil
	}
	h.fire("spawn")
	if h.state("ready").String() != "true" {
		t.Fatal("a cooldown nothing has stamped must be ready")
	}
	h.host.State(h.run.Self).M["cover_at"] = flang.FloatVal(99)
	h.fire("spawn")
	if h.state("ready").String() != "false" {
		t.Fatal("one stamped a second ago must not be")
	}
	h.host.State(h.run.Self).M["cover_at"] = flang.FloatVal(90)
	h.fire("spawn")
	if h.state("ready").String() != "true" {
		t.Fatal("one stamped ten seconds ago must be")
	}
}

func TestSendFansOutAcrossATagWithinARadius(t *testing.T) {
	d := &Doc{ID: "t", Trees: []*Tree{{Event: "spawn", Args: []string{"target"}, Root: "n1",
		Nodes: []*Node{
			act("n1", "send", map[string]string{
				"tag": "enemy", "event": "alert", "within": "30.0",
				"where": "it != @self",
			}, "", "target"),
		},
	}}}
	h := newHarness(t, d, "")
	near := h.host.AddObject("near", flang.RefEntity, "enemy")
	near.Props = map[string]flang.Value{"position": flang.VecVal{10, 0, 0}}
	far := h.host.AddObject("far", flang.RefEntity, "enemy")
	far.Props = map[string]flang.Value{"position": flang.VecVal{100, 0, 0}}

	h.fire("spawn", flang.StrVal("player"))
	var to []string
	for _, m := range h.host.Posted {
		to = append(to, m.To.ID())
	}
	if len(to) != 1 || to[0] != "near" {
		t.Fatalf("alerted %v; only the near comrade, and never itself", to)
	}
	if h.host.Posted[0].Atom != "alert" || len(h.host.Posted[0].Args) != 1 {
		t.Fatalf("sent %v", h.host.Posted[0])
	}
}

func TestSendWithAnAfterIsATimer(t *testing.T) {
	d := &Doc{ID: "t", Trees: []*Tree{{Event: "spawn", Root: "n1",
		Nodes: []*Node{act("n1", "send", map[string]string{"event": "grenade_check", "after": "470"}, "")}}}}
	h := newHarness(t, d, "")
	h.fire("spawn")
	if len(h.host.Posted) != 1 || h.host.Posted[0].DelayMS != 470 {
		t.Fatalf("posted %v", h.host.Posted)
	}
	if h.host.Posted[0].To.ID() != "guard" {
		t.Fatalf("a send with no `to` goes to the agent itself, not %s", h.host.Posted[0].To)
	}
}

func TestCallEntersTheEntitysScriptAndKeepsItsAnswer(t *testing.T) {
	d := &Doc{ID: "t", Trees: []*Tree{{Event: "spawn", Root: "n1",
		Nodes: []*Node{act("n1", "call", map[string]string{"function": "aim_point", "into": "spot"}, "", "12.0")}}}}
	h := newHarness(t, d, `
def aim_point(h float) -> vec
    return <0.0, h, 0.0>
end
`)
	h.fire("spawn")
	if got := h.state("spot").String(); got != "<0.0, 12.0, 0.0>" {
		t.Fatalf("spot = %s; the script's answer did not reach the blackboard", got)
	}
}

func TestAHelperTreeAnswersAQuestionAskedFromAnotherTree(t *testing.T) {
	d := &Doc{ID: "t", Trees: []*Tree{
		{Event: "spawn", Args: []string{"who"}, Root: "n1", Nodes: []*Node{
			{ID: "n1", Kind: NodeCondition, Then: "n2", Else: "n3",
				Check: Call{Verb: "tree", Fields: map[string]string{"tree": "is_foe"}, Args: []string{"who"}}},
			act("n2", "set", map[string]string{"var": "verdict", "value": "\"foe\""}, ""),
			act("n3", "set", map[string]string{"var": "verdict", "value": "\"friend\""}, ""),
		}},
		{Name: "is_foe", Args: []string{"x"}, Root: "f1", Nodes: []*Node{
			cond("f1", "tagged", map[string]string{"who": "x", "tag": "friendly"}, "f2", "f3"),
			act("f2", "result", map[string]string{"value": "false"}, ""),
			act("f3", "result", map[string]string{"value": "true"}, ""),
		}},
	}}
	h := newHarness(t, d, "")
	h.host.Verbs["find"] = func(args []flang.Value, pos flang.Pos) (flang.Value, error) {
		tag, _ := args[0].(flang.StrVal)
		var elems []flang.Value
		for _, r := range h.host.ResolveTag(string(tag)) {
			elems = append(elems, r)
		}
		return flang.NewList(elems...), nil
	}
	mate := h.host.AddObject("mate", flang.RefEntity, "friendly")
	h.fire("spawn", mate.Ref)
	if got := h.state("verdict").String(); got != "friend" {
		t.Fatalf("a tagged friendly read as %q", got)
	}
	other := h.host.AddObject("other", flang.RefEntity, "enemy")
	h.fire("spawn", other.Ref)
	if got := h.state("verdict").String(); got != "foe" {
		t.Fatalf("an untagged one read as %q", got)
	}
}

func TestAQuotedFieldTakesABareNameAndAnEqualsEscape(t *testing.T) {
	h := newHarness(t, &Doc{ID: "t", Trees: []*Tree{{Event: "spawn", Root: "n1",
		Nodes: []*Node{
			act("n1", "play_anim", map[string]string{"anim": "aim"}, "n2"),
			act("n2", "play_anim", map[string]string{"anim": "=resume_anim()"}, ""),
		}}}}, `
def resume_anim() -> str
    return "crouch_aim"
end
`)
	h.host.Verbs["play_anim"] = func(args []flang.Value, pos flang.Pos) (flang.Value, error) {
		return flang.NullVal{}, nil
	}
	h.fire("spawn")
	var played []string
	for _, a := range h.host.Actions {
		if a.Name == "play_anim" {
			played = append(played, a.Args[1].String())
		}
	}
	if len(played) != 2 || played[0] != "aim" || played[1] != "crouch_aim" {
		t.Fatalf("played %v; a bare name is the name, and =expr is an expression", played)
	}
}

func TestChanceRollsOnTheScriptsOwnRandomStream(t *testing.T) {
	d := &Doc{ID: "t", Trees: []*Tree{{Event: "spawn", Root: "n1",
		Nodes: []*Node{
			cond("n1", "chance", map[string]string{"chance": "0.0"}, "n2", "n3"),
			act("n2", "set", map[string]string{"var": "rolled", "value": "true"}, ""),
			act("n3", "set", map[string]string{"var": "rolled", "value": "false"}, ""),
		}}}}
	h := newHarness(t, d, "")
	h.fire("spawn")
	if h.state("rolled").String() != "false" {
		t.Fatal("a chance of 0 must never be taken")
	}
	d.Trees[0].Nodes[0].Check.Set("chance", "1.0")
	h2 := newHarness(t, d, "")
	h2.fire("spawn")
	if h2.state("rolled").String() != "true" {
		t.Fatal("a chance of 1 must always be")
	}
}

// ---------- parameters ----------

func TestADeclaredParameterIsReadableAsAScriptOneAndOverridable(t *testing.T) {
	d := &Doc{ID: "t",
		Params: []Param{{Name: "grenade_chance", Type: "float", Value: "0.35"}},
		Trees: []*Tree{{Event: "spawn", Root: "n1", Nodes: []*Node{
			act("n1", "set", map[string]string{"var": "p", "value": "?grenade_chance"}, ""),
		}}}}
	h := newHarness(t, d, "")
	h.fire("spawn")
	if got := h.state("p").String(); got != "0.35" {
		t.Fatalf("the declared default did not reach ?grenade_chance: %q", got)
	}
	h.host.Params["grenade_chance"] = flang.FloatVal(0.9)
	h.fire("spawn")
	if got := h.state("p").String(); got != "0.9" {
		t.Fatalf("a level's override did not win: %q", got)
	}
}

// ---------- what the compiler says ----------

func TestTheCompilerReportsWhatIsWrongWithoutRefusingTheRest(t *testing.T) {
	d := &Doc{ID: "t", Trees: []*Tree{{Event: "spawn", Root: "n1", Nodes: []*Node{
		cond("n1", "nonsense", nil, "n2", ""),
		act("n2", "set", map[string]string{"var": "x", "value": "1 +"}, "n404"),
		act("n3", "stop", nil, ""),
	}}}}
	p := Compile(d)
	want := []string{"nonsense", "n404"}
	for _, w := range want {
		found := false
		for _, e := range p.Errors() {
			if strings.Contains(e.Error(), w) {
				found = true
			}
		}
		if !found {
			t.Fatalf("nothing said about %q in %v", w, p.Errors())
		}
	}
	unreachable := false
	for _, dg := range p.Diags {
		if dg.Node == "n3" && dg.Warning {
			unreachable = true
		}
	}
	if !unreachable {
		t.Fatalf("n3 is not linked to and should have been called out: %v", p.Diags)
	}
	if p.Events["spawn"] == nil {
		t.Fatal("the tree still has to be there; a broken node is not a broken document")
	}
}

func TestABrokenNodeEndsItsBranchRatherThanBeingGuessedAt(t *testing.T) {
	d := &Doc{ID: "t", Trees: []*Tree{{Event: "spawn", Root: "n1", Nodes: []*Node{
		act("n1", "set", map[string]string{"var": "before", "value": "true"}, "n2"),
		act("n2", "set", map[string]string{"var": "bad", "value": "1 +"}, "n3"),
		act("n3", "set", map[string]string{"var": "after", "value": "true"}, ""),
	}}}}
	prog := Compile(d)
	host := flang.NewMapHost()
	g := host.AddObject("guard", flang.RefEntity)
	host.SetSelf("guard")
	fp, _ := flang.Link("t.flang", []byte(""), nil)
	in, _ := flang.NewInterp(fp, host, flang.Limits{})
	r := &Runner{Prog: prog, Interp: in, Host: host, Self: g.Ref}
	if _, err := r.Fire("spawn", nil); err != nil {
		t.Fatalf("a broken node must stop the branch, not fail the run: %v", err)
	}
	st := host.State(g.Ref)
	if st.Get("before").String() != "true" {
		t.Fatal("the node before the broken one should have run")
	}
	if _, ok := st.Get("after").(flang.NullVal); !ok {
		t.Fatal("the node after the broken one should not have")
	}
}

func TestAHelperThatRunsItselfIsRefused(t *testing.T) {
	d := &Doc{ID: "t", Trees: []*Tree{{Name: "loop", Root: "n1", Nodes: []*Node{
		{ID: "n1", Kind: NodeAction, Do: Call{Verb: "run", Fields: map[string]string{"tree": "loop"}}},
	}}}}
	if errs := Compile(d).Errors(); len(errs) == 0 {
		t.Fatal("wanted a complaint about a tree running itself")
	}
}

func TestAHelperCalledWithTheWrongNumberOfArgumentsIsCalledOut(t *testing.T) {
	d := &Doc{ID: "t", Trees: []*Tree{
		{Event: "spawn", Root: "n1", Nodes: []*Node{
			{ID: "n1", Kind: NodeAction, Do: Call{Verb: "run",
				Fields: map[string]string{"tree": "h"}, Args: []string{"1", "2"}}},
		}},
		{Name: "h", Args: []string{"x"}, Root: "f", Nodes: []*Node{
			act("f", "result", map[string]string{"value": "x"}, ""),
		}},
	}}
	found := false
	for _, e := range Compile(d).Errors() {
		if strings.Contains(e.Msg, "takes 1 argument") {
			found = true
		}
	}
	if !found {
		t.Fatal("wanted the arity to be checked")
	}
}

// ---------- the file ----------

func TestADocumentSurvivesARoundTripThroughItsFile(t *testing.T) {
	d := &Doc{ID: "soldier", Description: "a soldier",
		Params: []Param{{Name: "damage", Type: "int", Value: "12", Doc: "per shot"}},
		Trees: []*Tree{
			{Event: "damage", Args: []string{"src", "amount"}, Root: "n1", Nodes: []*Node{
				{ID: "n1", Kind: NodeCondition, X: 40, Y: 80, Label: "already down?",
					Check: Call{Verb: "var", Fields: map[string]string{"var": "dead", "op": "is true"}},
					Then:  "", Else: "n2"},
				{ID: "n2", Kind: NodeAction, X: 40, Y: 220,
					Do:   Call{Verb: "send", Fields: map[string]string{"tag": "enemy", "event": "alert", "after": "470"}, Args: []string{"src", "amount"}},
					Next: ""},
				{ID: "n3", Kind: NodeNote, X: 300, Y: 80, Note: "why"},
			}},
			{Name: "is_foe", Args: []string{"x"}, Root: "f1", Nodes: []*Node{
				act("f1", "result", map[string]string{"value": "true"}, ""),
			}},
		}}
	raw := ToRaw(d)
	back := FromRaw(raw)
	if len(back.Trees) != 2 || len(back.Trees[0].Nodes) != 3 {
		t.Fatalf("shape lost: %#v", back)
	}
	n := back.Trees[0].Nodes[1]
	if n.Do.Verb != "send" || n.Do.Field("after") != "470" || len(n.Do.Args) != 2 {
		t.Fatalf("node 2 came back as %#v", n.Do)
	}
	if back.Trees[0].Nodes[0].Label != "already down?" || back.Trees[0].Nodes[0].X != 40 {
		t.Fatalf("the drawing is part of the document: %#v", back.Trees[0].Nodes[0])
	}
	if back.Trees[1].Name != "is_foe" || back.Trees[1].Args[0] != "x" {
		t.Fatalf("helper lost: %#v", back.Trees[1])
	}
}

func TestTheFileWritesAVerbAndItsFieldsFlat(t *testing.T) {
	d := &Doc{ID: "t", Trees: []*Tree{{Event: "spawn", Root: "n1", Nodes: []*Node{
		act("n1", "send", map[string]string{"event": "grenade_check", "after": "470"}, "", "target"),
	}}}}
	data := mustMarshal(t, d)
	for _, want := range []string{"verb: send", "event: grenade_check", `after: "470"`, "- target"} {
		if !strings.Contains(data, want) {
			t.Fatalf("the file should read with %q in it:\n%s", want, data)
		}
	}
	if strings.Index(data, "verb: send") > strings.Index(data, "event: grenade_check") {
		t.Fatalf("the verb comes first:\n%s", data)
	}
}

func TestAFieldThisBuildDoesNotKnowSurvivesBeingSaved(t *testing.T) {
	c := Call{Verb: "send", Fields: map[string]string{"event": "x", "from_a_later_build": "7"}}
	var back Call
	roundTripYAML(t, c, &back)
	if back.Field("from_a_later_build") != "7" {
		t.Fatalf("dropped: %#v", back)
	}
}
