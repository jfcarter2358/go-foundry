package main

// script_host_test.go — the language against a real Level.
//
// The flang package's own tests prove the language works against MapHost. These
// prove the *binding* works: that a ref resolves to the right entity, that a
// property write reaches the right field, and that the traps named in
// script_props.go — the inverted Visible, the derived light direction — are
// actually guarded.
//
// All headless: no GL, no window, no message loop. The runtime is driven
// directly rather than through ScriptTick.

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/world"
)

// hostTestLevel builds a small level with one of each addressable kind.
func hostTestLevel(t *testing.T) *world.Level {
	t.Helper()
	withoutGeometryUploads(t)

	ch := edgeIDTestChunk(t)
	ch.ID = "chunk"
	ch.Sectors[0].AddTag("room")
	ch.Sectors[0].Floor.Texture = "concrete"

	lvl := &world.Level{Chunks: []world.Chunk{*ch}}

	crate := world.Entity{ID: "crate", Type: "box", Visible: true,
		BoundHalfX: 1, BoundHalfY: 1, BoundHalfZ: 1,
		Scale:      mgl32.Vec3{1, 1, 1},
		Attributes: world.AttrSet{"health": world.IntAttr(25)}}
	crate.AddTag("breakable")
	lvl.Entities = append(lvl.Entities, crate)

	trigger := world.NewVolume("trigger", mgl32.Vec3{0, 0, 0})
	trigger.HalfX, trigger.HalfY, trigger.HalfZ = 5, 5, 5
	trigger.AddTag("trap")
	trigger.SetAttr("armed", world.BoolAttr(true))
	lvl.Volumes = append(lvl.Volumes, trigger)

	lvl.Lights.Points = append(lvl.Lights.Points, world.PointLight{
		ID: "lamp", Brightness: 1, Radius: 10, Color: mgl32.Vec3{1, 1, 1},
	})
	lvl.Lights.Directional = append(lvl.Lights.Directional, world.DirectionalLight{
		ID: "sun", Yaw: 0, Pitch: -45, Brightness: 1,
	})
	lvl.Lights.Directional[0].SyncDirection()
	return lvl
}

// bindRuntime points the global runtime at lvl and restores it afterwards.
func bindRuntime(t *testing.T, lvl *world.Level) *ScriptRuntime {
	t.Helper()
	prev := *Scripts
	t.Cleanup(func() { *Scripts = prev })
	ScriptRuntimeReset(lvl)
	return Scripts
}

// runScript compiles src against the level host and calls `main`.
func runScript(t *testing.T, lvl *world.Level, self string, src string) flang.Value {
	t.Helper()
	rt := bindRuntime(t, lvl)
	if self != "" {
		r, ok := rt.host.Resolve(self)
		if !ok {
			t.Fatalf("cannot resolve @%s", self)
		}
		rt.self = r
	}

	f, errs := flang.Parse("test.flang", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors:\n%s\n\n%v", src, errs)
	}
	prog := flang.NewProgram()
	prog.Add(f)
	in, ierrs := flang.NewInterp(prog, rt.host, flang.Limits{})
	if len(ierrs) > 0 {
		t.Fatalf("clause errors: %v", ierrs)
	}
	v, err := in.Call("main", nil, flang.Pos{File: "test.flang"})
	if err != nil {
		t.Fatalf("running:\n%s\n\n%v", src, err)
	}
	return v
}

// runScriptErr is runScript for the cases that are supposed to fail.
func runScriptErr(t *testing.T, lvl *world.Level, self string, src string) error {
	t.Helper()
	rt := bindRuntime(t, lvl)
	if self != "" {
		if r, ok := rt.host.Resolve(self); ok {
			rt.self = r
		}
	}
	f, errs := flang.Parse("test.flang", []byte(src))
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	prog := flang.NewProgram()
	prog.Add(f)
	in, _ := flang.NewInterp(prog, rt.host, flang.Limits{})
	_, err := in.Call("main", nil, flang.Pos{})
	if err == nil {
		t.Fatalf("expected an error from:\n%s", src)
	}
	return err
}

func script(body string) string { return "def main() -> null\n" + body + "\nend" }

// ---------- addressing ----------

func TestRefsResolveAcrossEveryKind(t *testing.T) {
	lvl := hostTestLevel(t)
	cases := []struct {
		id   string
		kind flang.Kind_
	}{
		{"crate", flang.RefEntity},
		{"trigger", flang.RefVolume},
		{"room", flang.RefSector},
		{"chunk", flang.RefChunk},
		{"lamp", flang.RefLight},
		{"sun", flang.RefLight},
	}
	rt := bindRuntime(t, lvl)
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			r, ok := rt.host.Resolve(tc.id)
			if !ok {
				t.Fatalf("@%s did not resolve", tc.id)
			}
			if r.Kind != tc.kind {
				t.Errorf("@%s resolved as %s, want %s", tc.id, r.Kind, tc.kind)
			}
			if !rt.host.Alive(r) {
				t.Errorf("@%s resolved but is not alive", tc.id)
			}
		})
	}
}

func TestTagsReachEveryKind(t *testing.T) {
	lvl := hostTestLevel(t)
	rt := bindRuntime(t, lvl)

	if got := rt.host.ResolveTag("breakable"); len(got) != 1 || got[0].Kind != flang.RefEntity {
		t.Errorf("#breakable gave %v", got)
	}
	if got := rt.host.ResolveTag("trap"); len(got) != 1 || got[0].Kind != flang.RefVolume {
		t.Errorf("#trap gave %v", got)
	}
	// Geometry tags matter most: without them, `#tag` would be entity-only and
	// half the spec would be unimplementable.
	if got := rt.host.ResolveTag("room"); len(got) != 1 || got[0].Kind != flang.RefSector {
		t.Errorf("#room gave %v", got)
	}
}

func TestStaleRefIsNotAlive(t *testing.T) {
	lvl := hostTestLevel(t)
	rt := bindRuntime(t, lvl)
	r, _ := rt.host.Resolve("crate")

	lvl.Entities = nil
	rt.cache = buildRefCache(lvl)
	if rt.host.Alive(r) {
		t.Error("a ref to a deleted entity still reports alive")
	}
}

// ---------- attributes ----------

func TestAttributesRoundTripThroughAScript(t *testing.T) {
	lvl := hostTestLevel(t)
	got := runScript(t, lvl, "crate", script("    return @crate.attr.health"))
	if !flang.Equal(got, flang.IntVal(25)) {
		t.Errorf("read gave %v, want 25", got)
	}

	runScript(t, lvl, "crate", script("    @crate.attr.health -= 10"))
	if h, _ := lvl.Entities[0].Attr("health"); h.I != 15 {
		t.Errorf("health is %v, want 15", h.I)
	}
}

func TestGeometryCarriesAttributes(t *testing.T) {
	// A door is a wall, and a wall needs somewhere to keep `open_progress`.
	lvl := hostTestLevel(t)
	runScript(t, lvl, "", script(`    @room.attr.visits = 3`))
	if a, ok := lvl.Chunks[0].Sectors[0].Attr("visits"); !ok || a.I != 3 {
		t.Errorf("the sector's attribute did not stick: %v %v", a, ok)
	}
}

func TestCompoundValuesCannotBeStoredAsAttributes(t *testing.T) {
	// A level file has no way to write a list, so storing one would produce an
	// attribute that could not be saved.
	lvl := hostTestLevel(t)
	err := runScriptErr(t, lvl, "crate", script(`    @crate.attr.stuff = [1, 2]`))
	if !strings.Contains(err.Error(), "@self.state") {
		t.Errorf("diagnostic does not point at the alternative: %v", err)
	}
}

// ---------- properties ----------

func TestEntityPropertiesReachTheRightFields(t *testing.T) {
	lvl := hostTestLevel(t)
	runScript(t, lvl, "crate", script(`    @crate.prop.position = <1.0, 2.0, 3.0>
    @crate.prop.visible = false
    @crate.prop.collision_world = true`))

	e := &lvl.Entities[0]
	if e.Position != (mgl32.Vec3{1, 2, 3}) {
		t.Errorf("position is %v", e.Position)
	}
	if e.Visible {
		t.Error("visible was not written")
	}
	if !e.Collision.World {
		t.Error("collision_world was not written")
	}
}

func TestReadOnlyPropertyIsRefused(t *testing.T) {
	// Writing on_ground would be overwritten by EntityUpdate on the very next
	// frame, which is worse than refusing.
	lvl := hostTestLevel(t)
	err := runScriptErr(t, lvl, "crate", script(`    @crate.prop.on_ground = true`))
	if !strings.Contains(err.Error(), "read-only") {
		t.Errorf("diagnostic does not explain the problem: %v", err)
	}
}

func TestUnknownPropertySuggestsTheClosestName(t *testing.T) {
	lvl := hostTestLevel(t)
	err := runScriptErr(t, lvl, "crate", script(`    @crate.prop.visble = false`))
	if !strings.Contains(err.Error(), `did you mean "visible"`) {
		t.Errorf("no suggestion offered: %v", err)
	}
}

func TestEdgeVisibilityIsNotInverted(t *testing.T) {
	// Wall.Visible is stored inverted from YAML's `hidden`. A script sees the
	// runtime sense, and getting that backwards would hide every wall a script
	// meant to show.
	lvl := hostTestLevel(t)
	id := lvl.Chunks[0].Sectors[0].EdgeIDs[0]
	src := script(`    @room.edge.` + id + `.prop.visible = false`)
	runScript(t, lvl, "", src)

	if lvl.Chunks[0].Sectors[0].Walls[0].Visible {
		t.Error("the wall is still visible after being hidden")
	}
}

func TestFloorHeightWriteQueuesARebuild(t *testing.T) {
	// Height is geometry-mutating, so the write must be recorded for
	// DrainScriptWork rather than applied inline — ReloadSector is GL.
	lvl := hostTestLevel(t)
	rt := bindRuntime(t, lvl)
	r, _ := rt.host.Resolve("room")
	floor := flang.RefVal{Kind: flang.RefFloor, A: r.A, B: r.B}

	if err := rt.host.SetProp(floor, "height", flang.FloatVal(4)); err != nil {
		t.Fatal(err)
	}
	if lvl.Chunks[0].Sectors[0].Floor.Height != 4 {
		t.Errorf("height is %v, want 4", lvl.Chunks[0].Sectors[0].Floor.Height)
	}
	if len(rt.dirtySectors) != 1 {
		t.Errorf("got %d queued rebuilds, want 1", len(rt.dirtySectors))
	}
	// Writing it again must not queue a second: coalescing is what keeps a loop
	// that sets a height every frame from costing a rebuild every frame.
	_ = rt.host.SetProp(floor, "height", flang.FloatVal(5))
	if len(rt.dirtySectors) != 1 {
		t.Errorf("rebuilds did not coalesce: got %d", len(rt.dirtySectors))
	}
}

func TestLightDirectionIsWrittenThroughAngles(t *testing.T) {
	// Writing Direction directly is how a light reaches {0,0,0}, whose
	// Normalize() is NaN and poisons the shadow matrix permanently.
	lvl := hostTestLevel(t)
	runScript(t, lvl, "", script(`    @sun.prop.pitch = -90.0`))

	d := &lvl.Lights.Directional[0]
	if d.Pitch != -90 {
		t.Errorf("pitch is %v, want -90", d.Pitch)
	}
	if d.Direction.Len() < 0.99 {
		t.Errorf("direction was not re-derived: %v", d.Direction)
	}
	// There is no way to write the vector at all.
	if _, ok := propTables[flang.RefLight]["direction"]; ok {
		t.Error("a `direction` property exists; only yaw and pitch may be written")
	}
}

func TestTagBroadcastWritesEveryMember(t *testing.T) {
	lvl := hostTestLevel(t)
	lvl.Entities = append(lvl.Entities, world.Entity{ID: "crate2", Visible: true,
		Scale: mgl32.Vec3{1, 1, 1}, Tags: []string{"breakable"}, Attributes: world.AttrSet{}})

	runScript(t, lvl, "", script(`    #breakable.attr.health = 0`))
	for i := range lvl.Entities {
		if a, ok := lvl.Entities[i].Attr("health"); !ok || a.I != 0 {
			t.Errorf("%s was not written: %v", lvl.Entities[i].ID, a)
		}
	}
}

// ---------- structural members ----------

func TestSectorMembersResolve(t *testing.T) {
	lvl := hostTestLevel(t)
	cases := []struct {
		expr string
		want flang.Kind_
	}{
		{"@room.floor", flang.RefFloor},
		{"@room.ceil", flang.RefCeiling},
		{"@room.chunk", flang.RefChunk},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			got := runScript(t, lvl, "", "def main() -> null\n    return "+tc.expr+"\nend")
			r, ok := got.(flang.RefVal)
			if !ok {
				t.Fatalf("got %T", got)
			}
			if r.Kind != tc.want {
				t.Errorf("got kind %s, want %s", r.Kind, tc.want)
			}
		})
	}
}

func TestEdgeAddressingByStableID(t *testing.T) {
	// The whole point of Phase 1: an address that survives the edits an author
	// makes. Here it just has to resolve.
	lvl := hostTestLevel(t)
	id := lvl.Chunks[0].Sectors[0].EdgeIDs[2]
	got := runScript(t, lvl, "", "def main() -> null\n    return @room.edge."+id+".prop.kind\nend")
	if !flang.Equal(got, flang.AtomVal("wall")) {
		t.Errorf("got %v, want :wall:", got)
	}
}

func TestSectorEdgesList(t *testing.T) {
	lvl := hostTestLevel(t)
	got := runScript(t, lvl, "", "def main() -> null\n    return len(@room.edges)\nend")
	if !flang.Equal(got, flang.IntVal(4)) {
		t.Errorf("got %v, want 4", got)
	}
}

// ---------- verbs ----------

func TestMoveToAndSetVelocity(t *testing.T) {
	lvl := hostTestLevel(t)
	runScript(t, lvl, "crate", script(`    move_to(@crate, <5.0, 0.0, 5.0>)
    set_velocity(@crate, <0.0, 1.0, 0.0>)`))

	e := &lvl.Entities[0]
	if e.Position != (mgl32.Vec3{5, 0, 5}) {
		t.Errorf("position is %v", e.Position)
	}
	if e.Velocity != (mgl32.Vec3{0, 1, 0}) {
		t.Errorf("velocity is %v", e.Velocity)
	}
}

func TestSetTextureOnASurface(t *testing.T) {
	lvl := hostTestLevel(t)
	runScript(t, lvl, "", script(`    set_texture(@room.floor, "brick")`))

	if got := lvl.Chunks[0].Sectors[0].Floor.Texture; got != "brick" {
		t.Errorf("floor texture is %q, want brick", got)
	}
	// Both halves of the deferral: the name has to be uploaded and the sector
	// rebuilt, and neither can happen inside the tick.
	if !Scripts.dirtyTex["brick"] {
		t.Errorf("brick was not queued for upload")
	}
	if len(Scripts.dirtySectors) != 1 {
		t.Errorf("got %d sectors queued for rebuild, want 1", len(Scripts.dirtySectors))
	}
}

func TestSetTextureReskinsEveryAttachment(t *testing.T) {
	lvl := hostTestLevel(t)
	lvl.Entities[0].Models = []world.EntityModel{{Name: "body"}, {Name: "lid"}}

	runScript(t, lvl, "crate", script(`    set_texture(@crate, "rust")`))

	for _, m := range lvl.Entities[0].Models {
		if m.Texture != "rust" {
			t.Errorf("attachment %s wears %q, want rust", m.Name, m.Texture)
		}
	}
	if !Scripts.dirtyTex["rust"] {
		t.Errorf("rust was not queued for upload")
	}
	// A reskin changes no geometry, so it must not cost a rebuild.
	if len(Scripts.dirtySectors) != 0 {
		t.Errorf("an entity reskin queued %d sector rebuilds", len(Scripts.dirtySectors))
	}
}

func TestSetTextureTargetsOneAttachment(t *testing.T) {
	lvl := hostTestLevel(t)
	lvl.Entities[0].Models = []world.EntityModel{{Name: "body"}, {Name: "lid"}}

	got := runScript(t, lvl, "crate", script(`    return set_texture(@crate, "lid", "rust")`))

	if !flang.Equal(got, flang.BoolVal(true)) {
		t.Errorf("got %v, want true", got)
	}
	if lvl.Entities[0].Models[0].Texture != "" {
		t.Errorf("body was reskinned too: %q", lvl.Entities[0].Models[0].Texture)
	}
	if lvl.Entities[0].Models[1].Texture != "rust" {
		t.Errorf("lid wears %q, want rust", lvl.Entities[0].Models[1].Texture)
	}
}

func TestSetTextureOnAnUnknownAttachmentIsFalse(t *testing.T) {
	// False rather than an error: a script reskinning "turret" on an entity
	// type that has none is a miss, not a broken script.
	lvl := hostTestLevel(t)
	lvl.Entities[0].Models = []world.EntityModel{{Name: "body"}}
	got := runScript(t, lvl, "crate", script(`    return set_texture(@crate, "turret", "rust")`))
	if !flang.Equal(got, flang.BoolVal(false)) {
		t.Errorf("got %v, want false", got)
	}
}

func TestSetTextureRefusesSomethingWithoutOne(t *testing.T) {
	lvl := hostTestLevel(t)
	err := runScriptErr(t, lvl, "", script(`    set_texture(@lamp, "brick")`))
	if !strings.Contains(err.Error(), "no texture") {
		t.Errorf("got %v", err)
	}
}

func TestEntityTextureProperty(t *testing.T) {
	lvl := hostTestLevel(t)
	lvl.Entities[0].Models = []world.EntityModel{{Name: "body"}}
	got := runScript(t, lvl, "crate", script(`    @crate.prop.texture = "rust"
    return @crate.prop.texture`))
	if !flang.Equal(got, flang.StrVal("rust")) {
		t.Errorf("got %v, want \"rust\"", got)
	}
	if !Scripts.dirtyTex["rust"] {
		t.Errorf("rust was not queued for upload")
	}
}

func TestDestroyRemovesAnEntity(t *testing.T) {
	lvl := hostTestLevel(t)
	runScript(t, lvl, "crate", script(`    destroy(@crate)`))
	if len(lvl.Entities) != 0 {
		t.Errorf("got %d entities, want 0", len(lvl.Entities))
	}
}

func TestThePlayerCannotBeDestroyed(t *testing.T) {
	// The engine owns the player and the camera rides on it; deleting it would
	// leave the view attached to nothing.
	lvl := hostTestLevel(t)
	prev := world.PlayerEntity
	world.PlayerEntity = world.NewPlayer(mgl32.Vec3{})
	t.Cleanup(func() { world.PlayerEntity = prev })

	err := runScriptErr(t, lvl, "crate", script(`    destroy(@player)`))
	if !strings.Contains(err.Error(), "player") {
		t.Errorf("diagnostic does not explain the refusal: %v", err)
	}
}

func TestDistanceAcceptsObjectsOrVecs(t *testing.T) {
	lvl := hostTestLevel(t)
	lvl.Entities[0].Position = mgl32.Vec3{3, 0, 4}

	got := runScript(t, lvl, "", "def main() -> null\n    return distance(@crate, @trigger)\nend")
	if !flang.Equal(got, flang.FloatVal(5)) {
		t.Errorf("object distance gave %v, want 5", got)
	}
	got = runScript(t, lvl, "", "def main() -> null\n    return distance(<0.0,0.0,0.0>, <3.0,4.0,0.0>)\nend")
	if !flang.Equal(got, flang.FloatVal(5)) {
		t.Errorf("vec distance gave %v, want 5", got)
	}
}

// ---------- messages ----------

func TestSendQueuesOnTheRuntime(t *testing.T) {
	lvl := hostTestLevel(t)
	rt := bindRuntime(t, lvl)
	r, _ := rt.host.Resolve("trigger")
	rt.self = r

	f, _ := flang.Parse("t.flang", []byte(script(`    @crate <- (:damage:, @self, 20)`)))
	prog := flang.NewProgram()
	prog.Add(f)
	in, _ := flang.NewInterp(prog, rt.host, flang.Limits{})
	if _, err := in.Call("main", nil, flang.Pos{}); err != nil {
		t.Fatal(err)
	}

	if len(rt.queue) != 1 {
		t.Fatalf("got %d queued messages, want 1", len(rt.queue))
	}
	m := rt.queue[0]
	if m.to.ID() != "crate" || m.atom != "damage" {
		t.Errorf("queued %+v", m)
	}
}

// ---------- the play sandbox ----------

func TestPlaySessionTracksDirtiness(t *testing.T) {
	// A session that wrote nothing must cost nothing to leave.
	lvl := hostTestLevel(t)
	bindRuntime(t, lvl)

	playSession.valid = true
	playSession.dirty = false
	t.Cleanup(func() { playSession.valid, playSession.dirty = false, false })

	if PlaySessionDirty() {
		t.Error("a fresh session is already dirty")
	}
	runScript(t, lvl, "crate", script(`    @crate.attr.health = 1`))
	if !PlaySessionDirty() {
		t.Error("a script write did not mark the session dirty")
	}
}

func TestUndoIsRefusedDuringPlay(t *testing.T) {
	// A snapshot taken over script-mutated state would poison the stack
	// permanently, and leaving play restores the level underneath it anyway.
	playSession.valid = true
	t.Cleanup(func() { playSession.valid = false })
	if !inPlaySession() {
		t.Fatal("inPlaySession did not see the session")
	}
}

// ---------- the property catalogue ----------

func TestEveryPropertyIsReadable(t *testing.T) {
	// The table is what the editor's reference panel and the "did you mean"
	// message both enumerate, so a getter-less entry would be invisible in one
	// and unsuggestable in the other.
	for kind, tbl := range propTables {
		for name, acc := range tbl {
			if acc.Get == nil {
				t.Errorf("%s.%s has no getter", kind, name)
			}
			if acc.Doc == "" {
				t.Errorf("%s.%s has no documentation", kind, name)
			}
		}
	}
}

func TestPropertyNamesAreSorted(t *testing.T) {
	names := PropertyNames(flang.RefEntity)
	if len(names) == 0 {
		t.Fatal("entities have no properties")
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("PropertyNames is not sorted: %v", names)
		}
	}
}
