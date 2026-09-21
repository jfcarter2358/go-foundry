package script

// script_save_test.go — checkpoints, and the script runtime's half of a save.

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/hooks"
	"foundry/internal/savefile"
	"foundry/internal/world"
)

// takeCheckpoints drains the save requests and returns the checkpoints among
// them.
func takeCheckpoints() []string {
	var out []string
	for _, r := range hooks.TakeSaveRequests() {
		if r.Kind == hooks.Checkpoint {
			out = append(out, r.Name)
		}
	}
	return out
}

func withNoSaveRequests(t *testing.T) {
	t.Helper()
	hooks.TakeSaveRequests()
	t.Cleanup(func() { hooks.TakeSaveRequests() })
}

// A checkpoint volume is reached by the player walking in — nothing else, and
// once. Walking out and back is not a second checkpoint, and neither is the
// same checkpoint after a restore that remembers reaching it.
func TestACheckpointVolumeIsReachedOnceByThePlayerAlone(t *testing.T) {
	withNoSaveRequests(t)
	lvl := tickTestLevel(t)
	lvl.Name = "test"
	v := world.NewVolume("ck_gate", mgl32.Vec3{0, 0, 0})
	v.HalfX, v.HalfY, v.HalfZ = 2, 2, 2
	v.Checkpoint = true
	lvl.Volumes = append(lvl.Volumes, v)
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "walker", Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Position: mgl32.Vec3{10, 0, 0},
	})
	player := withPlayerAt(t, mgl32.Vec3{10, 0, 0})

	prevRT, prevEv := *Scripts, scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv })
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)

	lvl.Entities[0].Position = mgl32.Vec3{}
	ScriptTick(lvl, 1.0/60.0, true)
	if got := takeCheckpoints(); len(got) != 0 {
		t.Fatalf("an entity walking in reached %v; only the player may", got)
	}

	player.Position = mgl32.Vec3{}
	ScriptTick(lvl, 1.0/60.0, true)
	if got := takeCheckpoints(); !reflect.DeepEqual(got, []string{"ck_gate"}) {
		t.Fatalf("the player walking in reached %v, want [ck_gate]", got)
	}

	player.Position = mgl32.Vec3{10, 0, 0}
	ScriptTick(lvl, 1.0/60.0, true)
	player.Position = mgl32.Vec3{}
	ScriptTick(lvl, 1.0/60.0, true)
	if got := takeCheckpoints(); len(got) != 0 {
		t.Errorf("walking out and back in reached %v again", got)
	}

	// A fresh play-through that restores the state saved here does not reach
	// it either — which is what keeps retrying from a checkpoint from saving
	// that checkpoint again.
	saved := CaptureScriptState(nil)
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)
	RestoreScriptState(saved)
	player.Position = mgl32.Vec3{10, 0, 0}
	ScriptTick(lvl, 1.0/60.0, true)
	player.Position = mgl32.Vec3{}
	ScriptTick(lvl, 1.0/60.0, true)
	if got := takeCheckpoints(); len(got) != 0 {
		t.Errorf("after restoring, walking in reached %v again", got)
	}
}

// The verb and the volume share "once": a script reaching a name the volume
// already reached is refused.
func TestTheCheckpointVerbIsOncePerName(t *testing.T) {
	withNoSaveRequests(t)
	root := withTempScriptDir(t)
	path := writeScript(t, root, "ck.flang", `def on_spawn(:spawn:) -> null
    @self.attr.first = checkpoint("lz")
    @self.attr.second = checkpoint("lz")
end`)
	lvl := tickTestLevel(t)
	lvl.Name = "test"
	lvl.Entities = append(lvl.Entities, world.Entity{
		ID: "director", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Attributes: world.AttrSet{},
	})
	runTicks(t, lvl, 1)

	first, _ := lvl.Entities[0].Attr("first")
	second, _ := lvl.Entities[0].Attr("second")
	if !first.B || second.B {
		t.Errorf("checkpoint returned %v then %v, want true then false", first.B, second.B)
	}
	if got := takeCheckpoints(); !reflect.DeepEqual(got, []string{"lz"}) {
		t.Errorf("requested %v, want [lz]", got)
	}
}

// Every kind of value a `.state` dict can hold comes back as itself.
func TestValuesSurviveEncoding(t *testing.T) {
	inner := flang.NewDict()
	inner.M["n"] = flang.IntVal(3)
	d := flang.NewDict()
	d.M["null"] = flang.NullVal{}
	d.M["int"] = flang.IntVal(-7)
	d.M["float"] = flang.FloatVal(2.5)
	d.M["str"] = flang.StrVal("hello")
	d.M["bool"] = flang.BoolVal(true)
	d.M["atom"] = flang.AtomVal("open")
	d.M["vec"] = flang.VecVal{1, 2, 3}
	d.M["ref"] = flang.RefVal{Kind: flang.RefEntity, A: "grenade_2"}
	d.M["tuple"] = flang.TupleVal{Elems: []flang.Value{flang.AtomVal("hit"), flang.IntVal(1)}}
	d.M["list"] = &flang.ListVal{Elems: []flang.Value{flang.StrVal("a"), inner}}
	d.M["dict"] = inner

	got := DecodeValue(EncodeValue(d))
	if !reflect.DeepEqual(got, d) {
		t.Errorf("round trip changed the value:\n got  %v\n want %v", got, d)
	}
}

// A handler's scratch, its pending timer and its "already spawned" survive a
// save: after restoring, the timer comes due when it would have — not early,
// not late — and `:spawn:` is not heard a second time.
func TestScriptStateSurvivesASave(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "fuse.flang", `def on_spawn(:spawn:) -> null
    @self.state["spawns"] = get(@self.state, "spawns", 0) + 1
    @self.state["armed"] = {"by": @player_marker, "at": <1.0, 2.0, 3.0>}
    @self <- after(500.0, :boom:)
end
def on_boom(:boom:) -> null
    @self.attr.boomed = true
end`)
	build := func() *world.Level {
		lvl := tickTestLevel(t)
		lvl.Entities = append(lvl.Entities,
			world.Entity{ID: "charge", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
				Attributes: world.AttrSet{"boomed": world.BoolAttr(false)}},
			world.Entity{ID: "player_marker", Visible: true, Scale: mgl32.Vec3{1, 1, 1}})
		return lvl
	}

	lvl := build()
	prevRT, prevEv := *Scripts, scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv })
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)
	for i := 0; i < 12; i++ { // 0.2 s: the fuse has 0.3 s left
		ScriptTick(lvl, 1.0/60.0, true)
	}

	// Through the file format, as a save really goes.
	data, err := savefile.Encode(&savefile.Snapshot{Script: CaptureScriptState(nil)})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := savefile.Decode(data)
	if err != nil {
		t.Fatal(err)
	}

	fresh := build()
	ScriptEventsReset()
	ScriptRuntimeLoad(fresh)
	RestoreScriptState(snap.Script)

	st := Scripts.state[flang.RefVal{Kind: flang.RefEntity, A: "charge"}]
	if st == nil {
		t.Fatal("the charge's state did not come back")
	}
	armed, _ := st.M["armed"].(*flang.DictVal)
	if armed == nil || armed.M["by"] != (flang.RefVal{Kind: flang.RefEntity, A: "player_marker"}) ||
		armed.M["at"] != (flang.VecVal{1, 2, 3}) {
		t.Errorf("state came back as %v", st)
	}

	for i := 0; i < 15; i++ { // 0.25 s: still 0.05 s short
		ScriptTick(fresh, 1.0/60.0, true)
	}
	if a, _ := fresh.Entities[0].Attr("boomed"); a.B {
		t.Fatal("the restored timer went off early")
	}
	for i := 0; i < 6; i++ {
		ScriptTick(fresh, 1.0/60.0, true)
	}
	if a, _ := fresh.Entities[0].Attr("boomed"); !a.B {
		t.Error("the restored timer never went off")
	}
	if n := Scripts.state[flang.RefVal{Kind: flang.RefEntity, A: "charge"}].M["spawns"]; n != flang.IntVal(1) {
		t.Errorf(":spawn: ran %v times across the save, want 1", n)
	}
}
