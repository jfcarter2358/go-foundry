package script

// script_wiring_test.go — the checks that turn a silent no-op into a sentence.
//
// Two halves that have to agree with each other: the verbs warning at runtime
// (script_actions.go) and the linter warning before it (script_lint.go), both
// reading the same reasons out of entity_diagnose.go. The tests are together in
// one file for that reason — if the two ever disagree about why a call would do
// nothing, it should be visible here.

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/model"
	"foundry/internal/modeltest"
	"foundry/internal/world"
)

// riggedDoc is a model with an armature and a "press" clip, but no skin weights
// — the half-built rig that NewEntityAnim refuses.
func riggedDoc(t *testing.T) *model.ModelDoc {
	t.Helper()
	m, idx := modeltest.BoneChain(t)
	ci := m.AddClip("press")
	c := m.Clip(ci)
	c.SetKey(idx[0], 0, model.IdentBonePose())
	c.SetKey(idx[0], 1, model.IdentBonePose())
	return m
}

// buttonEntity is the shape the whole feature exists for: an entity with one
// attachment whose model has a clip, and which declares whichever animations
// the caller asks for.
func buttonEntity(t *testing.T, declared ...world.EntityAnimation) world.Entity {
	t.Helper()
	doc := riggedDoc(t)
	e := world.Entity{ID: "button", Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		BoundHalfX: 1, BoundHalfY: 1, BoundHalfZ: 1}
	e.Models = []world.EntityModel{{
		Name:       "button",
		Animations: declared,
		// Built directly rather than through Load: uploading needs GL, and what
		// is under test is the mapping from names to clips, which is not.
		Anim: &world.EntityAnim{Doc: doc, Clip: -1, Speed: 1},
	}}
	return e
}

// ---------- why a rig will not animate ----------

func TestAnimUnavailableNamesTheMissingStep(t *testing.T) {
	cases := []struct {
		name string
		doc  func(t *testing.T) *model.ModelDoc
		want string
	}{
		{"no document", func(*testing.T) *model.ModelDoc { return nil }, "did not load"},
		{"no armature", func(*testing.T) *model.ModelDoc { return model.NewCubeDoc() }, "no armature"},
		{"no skin weights", riggedDoc, "weighted to a bone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			why := world.AnimUnavailable(tc.doc(t))
			if !strings.Contains(why, tc.want) {
				t.Errorf("AnimUnavailable = %q, want it to mention %q", why, tc.want)
			}
			// Whatever the reason, the outcome is the same and the two must not
			// disagree: a reason means no playback state.
			if world.NewEntityAnim(tc.doc(t)) != nil {
				t.Error("NewEntityAnim returned playback state for a model it cannot animate")
			}
		})
	}
}

func TestAnimUnavailableIsEmptyForAWorkingRig(t *testing.T) {
	// A cube skinned to a bone chain, with a keyed clip: the complete case.
	m := riggedDoc(t)
	all := make([]int, len(m.Verts))
	for i := range all {
		all[i] = i
	}
	if n := m.AssignVertsToBone(all, 0, 1); n != len(all) {
		t.Fatalf("bound %d of %d vertices", n, len(all))
	}
	if why := world.AnimUnavailable(m); why != "" {
		t.Fatalf("a rigged, skinned, keyed model reported %q", why)
	}
	if world.NewEntityAnim(m) == nil {
		t.Fatal("NewEntityAnim refused a complete rig")
	}
}

// ---------- the four ways play_anim comes back false ----------

func TestAnimationProblemDistinguishesTheFourFailures(t *testing.T) {
	declared := world.EntityAnimation{Name: "button_press", Clip: "press"}
	cases := []struct {
		name           string
		entity         func(t *testing.T) world.Entity
		model, anim    string
		wantSubstrings []string
	}{
		{
			name:           "no attachment by that name",
			entity:         func(t *testing.T) world.Entity { return buttonEntity(t, declared) },
			model:          "buton",
			anim:           "button_press",
			wantSubstrings: []string{"no model attachment called \"buton\"", "it has \"button\""},
		},
		{
			name: "attachment cannot animate at all",
			entity: func(t *testing.T) world.Entity {
				e := buttonEntity(t)
				e.Models[0].Anim = nil
				e.Models[0].AnimBlocked = "no vertex in the model is weighted to a bone"
				return e
			},
			model:          "button",
			anim:           "button_press",
			wantSubstrings: []string{"cannot animate", "weighted to a bone"},
		},
		{
			name:   "clip exists but the entity never declared it",
			entity: func(t *testing.T) world.Entity { return buttonEntity(t) },
			model:  "button",
			anim:   "button_press",
			// The one that started all this. It has to say both that the entity
			// declares nothing and that the model does have clips, because that
			// pair is what identifies the mistake.
			wantSubstrings: []string{"has no animation called \"button_press\"",
				"Add Animation", "declares none", "clips are \"press\""},
		},
		{
			name: "declared, but pointing at a clip the model lacks",
			entity: func(t *testing.T) world.Entity {
				return buttonEntity(t, world.EntityAnimation{Name: "button_press", Clip: "shove"})
			},
			model:          "button",
			anim:           "button_press",
			wantSubstrings: []string{"clip \"shove\"", "does not have"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := tc.entity(t)
			why := e.AnimationProblem(tc.model, tc.anim)
			if why == "" {
				t.Fatal("AnimationProblem found nothing wrong")
			}
			for _, want := range tc.wantSubstrings {
				if !strings.Contains(why, want) {
					t.Errorf("reason %q\n  does not mention %q", why, want)
				}
			}
			// Whatever the reason, the call itself must actually fail — a
			// message about a problem that is not there would be worse than none.
			if e.PlayAnimation(tc.model, tc.anim) {
				t.Error("PlayAnimation succeeded on a case AnimationProblem rejected")
			}
		})
	}
}

func TestAnimationProblemIsSilentWhenItWouldPlay(t *testing.T) {
	e := buttonEntity(t, world.EntityAnimation{Name: "button_press", Clip: "press"})
	if why := e.AnimationProblem("button", "button_press"); why != "" {
		t.Fatalf("a correctly wired animation reported %q", why)
	}
	if !e.PlayAnimation("button", "button_press") {
		t.Fatal("PlayAnimation failed on the case AnimationProblem accepted")
	}
}

// ---------- the verbs logging what they did not do ----------

// warningsFrom runs one statement against lvl and returns the warnings it logged.
func warningsFrom(t *testing.T, lvl *world.Level, self, body string) []string {
	t.Helper()
	ring := withScriptLog(t, 64)
	runScript(t, lvl, self, script(body))
	var out []string
	for _, e := range ring.Snapshot() {
		if e.Level == LogWarn {
			out = append(out, e.Text)
		}
	}
	return out
}

// wantOneWarning asserts a single warning mentioning each substring.
func wantOneWarning(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("got %d warnings, want 1: %q", len(got), got)
	}
	for _, w := range want {
		if !strings.Contains(got[0], w) {
			t.Errorf("warning %q\n  does not mention %q", got[0], w)
		}
	}
}

func TestPlayAnimWarnsWhenItDoesNothing(t *testing.T) {
	lvl := hostTestLevel(t)
	lvl.Entities = append(lvl.Entities, buttonEntity(t))
	got := warningsFrom(t, lvl, "", `    play_anim(@button, "button", "button_press")`)
	wantOneWarning(t, got, "play_anim did nothing", "has no animation called \"button_press\"")
}

func TestSetTextureWarnsOnAnAttachmentThatIsNotThere(t *testing.T) {
	lvl := hostTestLevel(t)
	lvl.Entities = append(lvl.Entities, buttonEntity(t))
	got := warningsFrom(t, lvl, "", `    set_texture(@button, "lid", "rust")`)
	wantOneWarning(t, got, "set_texture did nothing", "no model attachment called \"lid\"")
}

func TestDestroyWarnsWhenTheRefIsNotDestroyable(t *testing.T) {
	lvl := hostTestLevel(t)
	got := warningsFrom(t, lvl, "", `    destroy(@room.floor)`)
	wantOneWarning(t, got, "destroy did nothing", "only entities and volumes")
}

func TestFindWarnsOnATagNothingCarries(t *testing.T) {
	lvl := hostTestLevel(t)
	got := warningsFrom(t, lvl, "", `    find("breakible")`)
	wantOneWarning(t, got, "matched nothing", `did you mean #breakable?`)
}

// A `:tick:` handler calling a broken verb every frame must not fill the log:
// the point of the warning is that it can be read.
func TestWarningsAreLoggedOnce(t *testing.T) {
	lvl := hostTestLevel(t)
	lvl.Entities = append(lvl.Entities, buttonEntity(t))
	got := warningsFrom(t, lvl, "", `
    for i in [1, 2, 3, 4, 5]
        play_anim(@button, "button", "button_press")
    end`)
	wantOneWarning(t, got, "play_anim did nothing")
}

// ...but two different problems on one line are two different warnings, which is
// what makes the dedupe key the message rather than the position.
func TestDifferentProblemsOnOneLineAreBothLogged(t *testing.T) {
	lvl := hostTestLevel(t)
	rt := bindRuntime(t, lvl)
	ring := withScriptLog(t, 64)
	pos := flang.Pos{File: "a.flang", Line: 3, Col: 1}
	rt.warnAt(pos, "first problem")
	rt.warnAt(pos, "first problem")
	rt.warnAt(pos, "second problem")

	var n int
	for _, e := range ring.Snapshot() {
		if e.Level == LogWarn {
			n++
			if e.Pos != pos {
				t.Errorf("warning lost its position: %+v", e.Pos)
			}
		}
	}
	if n != 2 {
		t.Fatalf("logged %d warnings, want 2 (one per distinct message)", n)
	}
}

// ---------- the linter ----------

// lintSource parses src and lints it against lvl.
func lintSource(t *testing.T, lvl *world.Level, src string) []ScriptIssue {
	t.Helper()
	f, errs := flang.Parse("test.flang", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors in the test source: %v", errs)
	}
	scope := map[string]bool{}
	for _, g := range f.Funcs {
		scope[g.Name] = true
	}
	return LintScript(lvl, f, scope, nil)
}

// wantOneIssue asserts a single issue mentioning each substring.
func wantOneIssue(t *testing.T, got []ScriptIssue, want ...string) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("got %d issues, want 1: %v", len(got), got)
	}
	for _, w := range want {
		if !strings.Contains(got[0].Msg, w) {
			t.Errorf("issue %q\n  does not mention %q", got[0].Msg, w)
		}
	}
}

func TestLintFlagsANameTheLevelDoesNotHave(t *testing.T) {
	lvl := hostTestLevel(t)
	got := lintSource(t, lvl, script(`    move_to(@crat, <0, 0, 0>)`))
	wantOneIssue(t, got, `nothing in this level is called "crat"`, "did you mean @crate?")
}

func TestLintFlagsATagNothingCarries(t *testing.T) {
	lvl := hostTestLevel(t)
	got := lintSource(t, lvl, script(`    for x in #breakible`+"\n        log(x)\n    end"))
	wantOneIssue(t, got, `nothing in this level carries the tag "breakible"`, "did you mean #breakable?")
}

func TestLintFlagsAnUndeclaredAnimationBeforeItRuns(t *testing.T) {
	lvl := hostTestLevel(t)
	lvl.Entities = append(lvl.Entities, buttonEntity(t))
	got := lintSource(t, lvl, script(`    play_anim(@button, "button", "button_press")`))
	wantOneIssue(t, got, "play_anim would do nothing", "Add Animation")
}

func TestLintFlagsAnUnknownProperty(t *testing.T) {
	lvl := hostTestLevel(t)
	got := lintSource(t, lvl, script(`    @crate.prop.visble = false`))
	wantOneIssue(t, got, `no property called "visble"`, `did you mean "visible"?`)
}

func TestLintFlagsAnUnknownFunction(t *testing.T) {
	lvl := hostTestLevel(t)
	got := lintSource(t, lvl, script(`    move_too(@crate, <0, 0, 0>)`))
	wantOneIssue(t, got, `nothing defines a function called "move_too"`, `did you mean "move_to"?`)
}

func TestLintFlagsATimerNothingHandles(t *testing.T) {
	lvl := hostTestLevel(t)
	got := lintSource(t, lvl, script(`    after(500, :open_done:)`))
	wantOneIssue(t, got, "sends :open_done: to itself", "defines no clause for it")
}

func TestLintFlagsAHandlerForANearMissEvent(t *testing.T) {
	lvl := hostTestLevel(t)
	got := lintSource(t, lvl, "def on_use(:intract:, src ref) -> null\n    log(\"hi\")\nend")
	wantOneIssue(t, got, ":intract:", "did you mean :interact:?")
}

// A custom atom is ordinary and needs no declaration, so it must not be flagged
// — otherwise every message-passing script in the game lights up.
func TestLintLeavesCustomAtomsAlone(t *testing.T) {
	lvl := hostTestLevel(t)
	src := "def on_unlock(:unlock:, by ref) -> null\n    log(\"hi\")\nend"
	if got := lintSource(t, lvl, src); len(got) != 0 {
		t.Fatalf("a custom atom was flagged: %v", got)
	}
}

func TestLintIsSilentOnACorrectScript(t *testing.T) {
	lvl := hostTestLevel(t)
	lvl.Entities = append(lvl.Entities, buttonEntity(t,
		world.EntityAnimation{Name: "button_press", Clip: "press"}))
	src := `def on_interact(:interact:, src ref) -> null
    play_anim(@button, "button", "button_press")
    @crate.prop.visible = false
    for x in #breakable
        log(x)
    end
end`
	if got := lintSource(t, lvl, src); len(got) != 0 {
		t.Fatalf("a correct script produced issues: %v", got)
	}
}

// Without a level there is nothing to check names against, and guessing would
// flag every script opened before a level loads.
func TestLintSkipsLevelChecksWithoutALevel(t *testing.T) {
	if got := lintSource(t, nil, script(`    move_to(@anything, <0, 0, 0>)`)); len(got) != 0 {
		t.Fatalf("linting with no level produced issues: %v", got)
	}
}

// The linter and the runtime must give the same reason for the same mistake.
func TestLintAndRuntimeAgreeOnTheReason(t *testing.T) {
	lvl := hostTestLevel(t)
	lvl.Entities = append(lvl.Entities, buttonEntity(t))
	const body = `    play_anim(@button, "button", "button_press")`

	issues := lintSource(t, lvl, script(body))
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1: %v", len(issues), issues)
	}
	warnings := warningsFrom(t, lvl, "", body)
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want 1: %q", len(warnings), warnings)
	}
	// Both are prefixed by the verb and then say the same thing, so the two are
	// compared on the reason rather than on the whole line.
	reason := lvl.Entities[len(lvl.Entities)-1].AnimationProblem("button", "button_press")
	if !strings.HasSuffix(issues[0].Msg, reason) {
		t.Errorf("the linter's issue does not end in the shared reason:\n  %q\n  %q", issues[0].Msg, reason)
	}
	if !strings.HasSuffix(warnings[0], reason) {
		t.Errorf("the runtime's warning does not end in the shared reason:\n  %q\n  %q", warnings[0], reason)
	}
}

// ---------- a send nothing handles ----------

// tickWarnings runs n ticks and returns the warnings logged, minus the ones the
// lint-at-load pass produced — those are checked separately, and mixing them in
// would make these tests fail whenever the linter learned something new.
func tickWarnings(t *testing.T, lvl *world.Level, n int) []string {
	t.Helper()
	ring := withScriptLog(t, 256)
	runTicks(t, lvl, n)
	var out []string
	for _, e := range ring.Snapshot() {
		if e.Level == LogWarn && !strings.Contains(e.Text, "nothing in this level") {
			out = append(out, e.Text)
		}
	}
	return out
}

func TestSendNothingHandlesIsWarnedAbout(t *testing.T) {
	root := withTempScriptDir(t)
	sender := writeScript(t, root, "sender.flang", `def on_spawn(:spawn:) -> null
    @door <- (:opne:,)
end`)
	door := writeScript(t, root, "door.flang", `def on_open(:open:) -> null
    @self.attr.opened = 1
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities,
		world.Entity{ID: "sender", Script: sender, Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{}},
		world.Entity{ID: "door", Script: door, Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{}},
	)

	got := tickWarnings(t, lvl, 1)
	var found string
	for _, w := range got {
		if strings.Contains(w, "nothing handled :opne:") {
			found = w
		}
	}
	if found == "" {
		t.Fatalf("a send nothing handled went unreported; warnings were %q", got)
	}
	if !strings.Contains(found, "door.flang") {
		t.Errorf("the warning does not name the receiving script: %q", found)
	}
}

// The fix is on the sender's line, so the warning has to be filed under the
// sender's script — by delivery time the runtime's `self` is the receiver, which
// is the easy thing to attribute it to and the wrong one.
func TestSendWarningIsFiledAgainstTheSender(t *testing.T) {
	root := withTempScriptDir(t)
	sender := writeScript(t, root, "sender.flang", `def on_spawn(:spawn:) -> null
    @door <- (:opne:,)
end`)
	door := writeScript(t, root, "door.flang", `def on_open(:open:) -> null
    @self.attr.opened = 1
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities,
		world.Entity{ID: "sender", Script: sender, Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{}},
		world.Entity{ID: "door", Script: door, Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{}},
	)

	ring := withScriptLog(t, 64)
	runTicks(t, lvl, 1)
	for _, e := range ring.Snapshot() {
		if strings.Contains(e.Text, "nothing handled :opne:") {
			if e.Source != sender {
				t.Fatalf("warning filed under %q, want the sender %q", e.Source, sender)
			}
			return
		}
	}
	t.Fatal("no warning was logged")
}

// Broadcasting to a tag and having some members ignore it is the documented way
// to address a group, not a mistake — see SCRIPTING.md's Messages section.
func TestBroadcastToATagIsNotWarnedAbout(t *testing.T) {
	root := withTempScriptDir(t)
	sender := writeScript(t, root, "caller.flang", `def on_spawn(:spawn:) -> null
    #light <- (:dim:,)
end`)
	listens := writeScript(t, root, "dimmer.flang", `def on_dim(:dim:) -> null
    @self.attr.dimmed = 1
end`)
	ignores := writeScript(t, root, "inert.flang", `def on_spawn(:spawn:) -> null
    @self.attr.up = 1
end`)

	lvl := tickTestLevel(t)
	a := world.Entity{ID: "lamp_a", Script: listens, Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{}}
	b := world.Entity{ID: "lamp_b", Script: ignores, Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{}}
	a.AddTag("light")
	b.AddTag("light")
	lvl.Entities = append(lvl.Entities,
		world.Entity{ID: "caller", Script: sender, Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{}},
		a, b)

	for _, w := range tickWarnings(t, lvl, 1) {
		if strings.Contains(w, "nothing handled") {
			t.Errorf("a broadcast was reported as a mistake: %q", w)
		}
	}
	// The half that does listen still has to have heard it, or the test would
	// pass on a broadcast that reached nobody at all.
	if v, _ := world.FindEntity(lvl, "lamp_a").Attr("dimmed"); v.I != 1 {
		t.Error("the broadcast never reached the object that handles it")
	}
}

// Engine events reach every scripted object and are handled by almost none, so
// they must never produce this warning.
func TestEngineEventsAreNotWarnedAbout(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "quiet.flang", `def on_spawn(:spawn:) -> null
    @self.attr.up = 1
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities,
		world.Entity{ID: "quiet", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{}})

	// Several ticks, so `:tick:` — which this script has no clause for — is
	// posted repeatedly.
	for _, w := range tickWarnings(t, lvl, 5) {
		if strings.Contains(w, "nothing handled") {
			t.Errorf("an engine event was reported as unhandled: %q", w)
		}
	}
}

// The lint pass runs when the level's scripts load, so a wiring problem is in
// the log before anyone presses anything.
func TestLoadingAScriptLogsItsIssues(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "button.flang", `def on_interact(:interact:, src ref) -> null
    move_to(@nothing_called_this, <0, 0, 0>)
end`)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities,
		world.Entity{ID: "b", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Attributes: world.AttrSet{}})

	ring := withScriptLog(t, 64)
	runTicks(t, lvl, 1)

	for _, e := range ring.Snapshot() {
		if e.Level == LogWarn && strings.Contains(e.Text, "nothing_called_this") {
			if e.Pos.Line != 2 {
				t.Errorf("the issue was logged at line %d, want 2", e.Pos.Line)
			}
			return
		}
	}
	t.Fatal("loading a script with a bad name logged nothing")
}
