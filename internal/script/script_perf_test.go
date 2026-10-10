package script

// script_perf_test.go — the fast paths agree with the slow ones they replaced.
//
// Each optimisation in the runtime keeps a structure the old code rebuilt or
// re-walked every time: the tag index, the ref cache carried across ticks and
// synced, the per-chunk scripted-brush lists, the mover grid behind the overlap
// events. Each test here drives the fast path and compares it with a straight
// re-derivation written the old way — the order of a broadcast and of an enter
// event is script-visible, so the comparisons are of sequences, not sets.

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/world"
)

// walkRefsWithTag is refsWithTag as it was before the index: one HasTag per
// object, in level order. The reference the index has to agree with.
func walkRefsWithTag(lvl *world.Level, tag string) []flang.RefVal {
	var out []flang.RefVal
	for i := range lvl.Entities {
		if lvl.Entities[i].HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefEntity, A: lvl.Entities[i].ID})
		}
	}
	for i := range lvl.Volumes {
		if lvl.Volumes[i].HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefVolume, A: lvl.Volumes[i].ID})
		}
	}
	for i := range lvl.Sprites {
		if lvl.Sprites[i].HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefSprite, A: lvl.Sprites[i].ID})
		}
	}
	for i := range lvl.Emitters {
		if lvl.Emitters[i].HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefEmitter, A: lvl.Emitters[i].ID})
		}
	}
	for i := range lvl.Sounds {
		if lvl.Sounds[i].HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefSound, A: lvl.Sounds[i].ID})
		}
	}
	for i := range lvl.Effects {
		if lvl.Effects[i].HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefEffect, A: lvl.Effects[i].ID})
		}
	}
	for i := range lvl.Decals {
		if lvl.Decals[i].HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefDecal, A: lvl.Decals[i].ID})
		}
	}
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if ch.HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefChunk, A: ch.ID})
		}
		for bi := range ch.Brushes {
			if ch.Brushes[bi].HasTag(tag) {
				out = append(out, flang.RefVal{Kind: flang.RefBrush, A: ch.Brushes[bi].ID})
			}
		}
	}
	for i := range lvl.Lights.Directional {
		if lvl.Lights.Directional[i].HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefLight, A: lvl.Lights.Directional[i].ID})
		}
	}
	for i := range lvl.Lights.Points {
		if lvl.Lights.Points[i].HasTag(tag) {
			out = append(out, flang.RefVal{Kind: flang.RefLight, A: lvl.Lights.Points[i].ID})
		}
	}
	return out
}

// taggedLevel is a level with something of every kind, tagged from a small
// vocabulary so most tags land on several kinds — and one object carrying the
// same tag twice.
func taggedLevel(rng *rand.Rand) *world.Level {
	vocab := []string{"a", "b", "c", "d", "e"}
	tags := func() []string {
		var out []string
		for _, t := range vocab {
			if rng.Intn(3) == 0 {
				out = append(out, t)
			}
		}
		return out
	}
	sd := func() world.ScriptData { return world.ScriptData{Tags: tags()} }
	lvl := &world.Level{}
	for i := 0; i < 30; i++ {
		lvl.Entities = append(lvl.Entities, world.Entity{ID: fmt.Sprintf("e%d", i), Tags: tags()})
		lvl.Volumes = append(lvl.Volumes, world.Volume{ID: fmt.Sprintf("v%d", i), ScriptData: sd()})
		lvl.Sprites = append(lvl.Sprites, world.Sprite{ID: fmt.Sprintf("s%d", i), ScriptData: sd()})
		lvl.Emitters = append(lvl.Emitters, world.Emitter{ID: fmt.Sprintf("m%d", i), ScriptData: sd()})
		lvl.Sounds = append(lvl.Sounds, world.Sound{ID: fmt.Sprintf("o%d", i), ScriptData: sd()})
		lvl.Effects = append(lvl.Effects, world.Effect{ID: fmt.Sprintf("f%d", i), ScriptData: sd()})
		lvl.Decals = append(lvl.Decals, world.Decal{ID: fmt.Sprintf("d%d", i), ScriptData: sd()})
		lvl.Lights.Points = append(lvl.Lights.Points, world.PointLight{ID: fmt.Sprintf("p%d", i), ScriptData: sd()})
	}
	for c := 0; c < 6; c++ {
		ch := world.Chunk{ID: fmt.Sprintf("c%d", c), ScriptData: sd()}
		for b := 0; b < 20; b++ {
			ch.Brushes = append(ch.Brushes, world.Brush{ID: fmt.Sprintf("c%db%d", c, b), ScriptData: sd()})
		}
		lvl.Chunks = append(lvl.Chunks, ch)
	}
	lvl.Lights.Directional = append(lvl.Lights.Directional, world.DirectionalLight{ID: "sun", ScriptData: sd()})
	lvl.Entities[3].Tags = []string{"a", "b", "a"}
	return lvl
}

func TestTagIndexMatchesAWalkOfTheLevel(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for round := 0; round < 5; round++ {
		lvl := taggedLevel(rng)
		c := buildRefCache(lvl)
		for _, tag := range []string{"a", "b", "c", "d", "e", "nothing", ""} {
			got := c.refsWithTag(tag)
			want := walkRefsWithTag(lvl, tag)
			if tag == "" {
				want = nil
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round %d, #%s:\n got %v\nwant %v", round, tag, got, want)
			}
		}
	}
}

// A caller appending to what refsWithTag returned must not reach into the
// index's own storage.
func TestTagIndexSlicesAreNotWritableThroughAppend(t *testing.T) {
	lvl := &world.Level{Entities: []world.Entity{{ID: "a", Tags: []string{"x"}}, {ID: "b", Tags: []string{"x"}}}}
	c := buildRefCache(lvl)
	got := c.refsWithTag("x")
	_ = append(got, flang.RefVal{Kind: flang.RefEntity, A: "intruder"})
	_ = append(c.refsWithTag("x"), flang.RefVal{Kind: flang.RefEntity, A: "other"})
	if again := c.refsWithTag("x"); len(again) != 2 || again[1].A != "b" {
		t.Fatalf("index changed under an append: %v", again)
	}
}

// cacheMatchesFresh compares a synced cache with one built from nothing.
func cacheMatchesFresh(t *testing.T, label string, c *refCache, lvl *world.Level) {
	t.Helper()
	f := buildRefCache(lvl)
	for k := ixKind(0); k < ixCount; k++ {
		got, exp := c.ix(k).m, f.ix(k).m
		if !reflect.DeepEqual(got, exp) && !(len(got) == 0 && len(exp) == 0) {
			t.Fatalf("%s: index %d\n got %v\nwant %v", label, k, got, exp)
		}
	}
	if gb, fb := c.brushIndex(), f.brushIndex(); !reflect.DeepEqual(gb, fb) && !(len(gb) == 0 && len(fb) == 0) {
		t.Fatalf("%s: brush index\n got %v\nwant %v", label, gb, fb)
	}
	for _, tag := range []string{"crew", "cargo", "spawned", "sight", "nothing"} {
		if got, want := c.refsWithTag(tag), walkRefsWithTag(lvl, tag); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: #%s\n got %v\nwant %v", label, tag, got, want)
		}
	}
}

func TestIDIndexSync(t *testing.T) {
	ids := []string{"a", "b", "c"}
	var x idIndex
	get := func(i int) string { return ids[i] }
	check := func(label string) {
		t.Helper()
		want := map[string]int{}
		for i, id := range ids {
			want[id] = i
		}
		if !reflect.DeepEqual(x.m, want) && !(len(x.m) == 0 && len(want) == 0) {
			t.Fatalf("%s: got %v, want %v", label, x.m, want)
		}
	}
	x.sync(len(ids), get)
	check("first")
	ids = append(ids, "d", "e")
	x.sync(len(ids), get)
	check("appended")
	ids = append(ids[:1], ids[2:]...) // remove b
	x.sync(len(ids), get)
	check("removed")
	ids[0], ids[1] = ids[1], ids[0]
	x.sync(len(ids), get)
	check("swapped")
	ids = append(ids, "z")
	x.appended(len(ids), get)
	check("appended one")
	ids = []string{"q", "q", "r"} // a duplicate maps to its last place
	x.sync(len(ids), get)
	if x.m["q"] != 1 || x.m["r"] != 2 || len(x.m) != 2 {
		t.Fatalf("duplicates: %v", x.m)
	}
	ids = nil
	x.sync(0, get)
	check("emptied")
}

// Spawning and destroying mid-tick — with the volumes and emitters a type
// carries coming and going with each entity — leaves the runtime's synced
// cache exactly as a fresh build would make it, at every point a script could
// look.
func TestCacheStaysInStepThroughSpawnAndDestroyMidTick(t *testing.T) {
	root := withTempScriptDir(t)
	crate := writeScript(t, root, "crate.flang", `def on_tick(:tick:, dt float) -> null
    @self.attr.age += 1
    if @self.attr.age == 2
        destroy(@self)
    end
end`)
	spawner := writeScript(t, root, "spawner.flang", `def on_tick(:tick:, dt float) -> null
    n = @self.attr.n
    if n < 12
        spawn("crate", <n * 1.0, 0.0, 0.0>)
        spawn("crate", <n * 1.0, 0.0, 2.0>)
        @self.attr.n = n + 1
    end
    for c in #cargo
        @self.attr.seen += 1
    end
end`)
	dir := filepath.Join(fileutil.ResourcesPath, world.EntitiesDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	def := "id: crate\nscript: " + crate + "\ntags: [cargo, spawned]\n" +
		"attributes:\n  age: {type: int, value: 0}\n" +
		"volumes:\n  - name: sight\n    half: {x: 1, y: 1, z: 1}\n    tags: [sight]\n"
	if err := os.WriteFile(filepath.Join(dir, "crate.yaml"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	world.ReloadEntityDefs()
	t.Cleanup(world.ReloadEntityDefs)

	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities,
		world.Entity{ID: "spawner", Script: spawner, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
			Attributes: world.AttrSet{"n": world.IntAttr(0), "seen": world.IntAttr(0)}},
		world.Entity{ID: "crew1", Tags: []string{"crew"}, Visible: true, Scale: mgl32.Vec3{1, 1, 1}},
	)
	v := world.NewVolume("dock", mgl32.Vec3{})
	v.Tags = []string{"cargo"}
	lvl.Volumes = append(lvl.Volumes, v)

	prevRT := *Scripts
	prevEv := scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv })
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)

	// Every verb that changes the level's structure runs inside a drain; the
	// spawn and destroy verbs are wrapped so the cache is checked straight
	// after each, mid-tick, as the next line of the script would see it.
	checks := 0
	for _, verb := range []string{"spawn", "destroy"} {
		verb := verb
		orig := scriptVerbs[verb]
		scriptVerbs[verb] = func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
			v, err := orig(rt, args, pos)
			cacheMatchesFresh(t, fmt.Sprintf("after %s #%d", verb, checks), rt.cache, rt.lvl)
			checks++
			return v, err
		}
		t.Cleanup(func() { scriptVerbs[verb] = orig })
	}

	for i := 0; i < 20; i++ {
		ScriptTick(lvl, 1.0/60.0, true)
		cacheMatchesFresh(t, fmt.Sprintf("after tick %d", i), Scripts.cache, lvl)
	}
	if checks < 40 {
		t.Fatalf("only %d spawns and destroys ran; the test is not exercising them", checks)
	}
	if n := len(lvl.Entities); n != 2 {
		t.Errorf("%d entities left after every crate's two ticks of life, want the two placed", n)
	}
}

// The scripted-brush lists come back right after a chunk's brushes change.
func TestScriptedBrushesFollowTheLevel(t *testing.T) {
	rt := &ScriptRuntime{}
	lvl := &world.Level{Chunks: []world.Chunk{
		{ID: "a", Brushes: []world.Brush{{ID: "a0"}, {ID: "a1", ScriptData: world.ScriptData{Script: "x.flang"}}}},
		{ID: "b"},
	}}
	ids := func() []string {
		var out []string
		for ci, cs := range rt.scriptedBrushes(lvl) {
			for _, bi := range cs.idx {
				out = append(out, lvl.Chunks[ci].Brushes[bi].ID)
			}
		}
		return out
	}
	if got := ids(); !reflect.DeepEqual(got, []string{"a1"}) {
		t.Fatalf("got %v", got)
	}
	// A new brush list (a load, a re-stamp) is noticed by its backing array.
	lvl.Chunks[1].Brushes = []world.Brush{{ID: "b0", ScriptData: world.ScriptData{Script: "y.flang"}}}
	if got := ids(); !reflect.DeepEqual(got, []string{"a1", "b0"}) {
		t.Fatalf("got %v", got)
	}
	// A chunk added.
	lvl.Chunks = append(lvl.Chunks, world.Chunk{ID: "c", Brushes: []world.Brush{{ID: "c0", ScriptData: world.ScriptData{Script: "z.flang"}}}})
	if got := ids(); !reflect.DeepEqual(got, []string{"a1", "b0", "c0"}) {
		t.Fatalf("got %v", got)
	}
	// An in-place edit that went through a mesh load bumps the generation.
	lvl.Chunks[0].Brushes[0].Script = "w.flang"
	world.MarkBrushMeshesChanged()
	if got := ids(); !reflect.DeepEqual(got, []string{"a0", "a1", "b0", "c0"}) {
		t.Fatalf("got %v", got)
	}
}

// ---------- :tick: is only posted where it is answered ----------

func TestTickIsPostedOnlyToObjectsThatAnswerIt(t *testing.T) {
	root := withTempScriptDir(t)
	ticker := writeScript(t, root, "ticker.flang", `def on_tick(:tick:, dt float) -> null
    @self.attr.ticks += 1
end`)
	deaf := writeScript(t, root, "deaf.flang", `def on_damage(:damage:, n int) -> null
    @self.attr.hp -= n
end`)
	lvl := tickTestLevel(t)
	lvl.Entities = append(lvl.Entities,
		world.Entity{ID: "t", Script: ticker, Attributes: world.AttrSet{"ticks": world.IntAttr(0)}},
		world.Entity{ID: "d", Script: deaf, Attributes: world.AttrSet{"hp": world.IntAttr(5)}},
	)
	prevRT := *Scripts
	prevEv := scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv })
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)
	ScriptTick(lvl, 1.0/60.0, true)

	Scripts.queue = nil
	postTickEvents(Scripts, scriptedRefs(Scripts, lvl), 1.0/60.0)
	var got []string
	for _, m := range Scripts.queue {
		got = append(got, m.to.A)
	}
	if !reflect.DeepEqual(got, []string{"t"}) {
		t.Fatalf(":tick: went to %v, want only the object that answers it", got)
	}
	Scripts.queue = nil
	if a, _ := lvl.Entities[0].Attr("ticks"); a.I != 1 {
		t.Errorf("ticks = %d, want 1", a.I)
	}
}

// ---------- the overlap grid ----------

// bruteEnters is the enter half of postOverlapEvents as it was: every
// container, in order, asked about every mover, in order, through the level's
// own containment tests. The grid has to produce this sequence exactly.
func bruteEnters(lvl *world.Level) []overlapPair {
	type container struct {
		ref     flang.RefVal
		test    func(mgl32.Vec3) bool
		carrier string
	}
	var cs []container
	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		if v.Script == "" && !v.IsLevelTrigger() && !v.IsCheckpointTrigger() {
			continue
		}
		cs = append(cs, container{
			ref:     flang.RefVal{Kind: flang.RefVolume, A: v.ID},
			test:    func(p mgl32.Vec3) bool { return v.ContainsWorldPoint(lvl, p) },
			carrier: v.Attach.Entity,
		})
	}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if e.Script == "" || !e.HasBounds() {
			continue
		}
		ef := lvl.Frames.FrameFor(e)
		cs = append(cs, container{
			ref:  flang.RefVal{Kind: flang.RefEntity, A: e.ID},
			test: func(p mgl32.Vec3) bool { return e.ContainsPoint(ef.FromWorld(p)) },
		})
	}
	type placed struct {
		ref flang.RefVal
		pos mgl32.Vec3
	}
	var ms []placed
	if world.PlayerEntity != nil {
		ms = append(ms, placed{flang.RefVal{Kind: flang.RefEntity, A: "player"}, lvl.Frames.WorldPosition(world.PlayerEntity)})
	}
	for i := range lvl.Entities {
		ms = append(ms, placed{flang.RefVal{Kind: flang.RefEntity, A: lvl.Entities[i].ID}, lvl.Frames.WorldPosition(&lvl.Entities[i])})
	}
	var out []overlapPair
	for _, c := range cs {
		for _, m := range ms {
			if m.ref == c.ref || (c.carrier != "" && m.ref.A == c.carrier) {
				continue
			}
			if c.test(m.pos) {
				out = append(out, overlapPair{container: c.ref, other: m.ref})
			}
		}
	}
	return out
}

func TestOverlapGridMatchesEveryContainerAskingEveryMover(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	root := withTempScriptDir(t)
	trig := writeScript(t, root, "trig.flang", `def on_enter(:enter:, o ref) -> null
    return null
end`)
	lvl := tickTestLevel(t)
	kinds := []world.BoundingType{world.BoundingAABB, world.BoundingSphere, world.BoundingCylinder}
	for i := 0; i < 150; i++ {
		e := world.Entity{
			ID: fmt.Sprintf("e%03d", i), Visible: true, Scale: mgl32.Vec3{1, 1, 1},
			Position: mgl32.Vec3{rng.Float32()*80 - 40, rng.Float32()*6 - 3, rng.Float32()*80 - 40},
		}
		if i%3 == 0 {
			e.Script = trig
			e.BoundingKind = kinds[i%len(kinds)]
			e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ = 0.5+rng.Float32()*3, 0.5+rng.Float32()*2, 0.5+rng.Float32()*3
			if i%2 == 0 {
				e.Rotation = mgl32.Vec3{0, rng.Float32() * 6, 0}
			}
			if i%5 == 0 {
				e.Scale = mgl32.Vec3{1.5, 0.7, 2}
			}
		}
		lvl.Entities = append(lvl.Entities, e)
	}
	for i := 0; i < 60; i++ {
		v := world.NewVolume(fmt.Sprintf("v%02d", i), mgl32.Vec3{rng.Float32()*80 - 40, 0, rng.Float32()*80 - 40})
		v.HalfX, v.HalfY, v.HalfZ = 0.5+rng.Float32()*6, 0.5+rng.Float32()*3, 0.5+rng.Float32()*6
		v.Script = trig
		switch i % 6 {
		case 1:
			v.Rotation = mgl32.Vec3{rng.Float32(), rng.Float32() * 6, 0}
		case 2:
			// Riding an entity: the carrier's transform moves and stretches it.
			v.Attach = world.Attach{Entity: fmt.Sprintf("e%03d", rng.Intn(150))}
			v.Position = mgl32.Vec3{0, 0, 2}
		case 3:
			v.Enabled = false
		case 4:
			// Huge: wider than the whole grid, so it tests every mover.
			v.HalfX, v.HalfZ = 200, 200
		}
		lvl.Volumes = append(lvl.Volumes, v)
	}
	// A mover far outside everything, and one at a non-finite position.
	lvl.Entities = append(lvl.Entities,
		world.Entity{ID: "far", Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Position: mgl32.Vec3{1e7, 0, -1e7}},
		world.Entity{ID: "nan", Visible: true, Scale: mgl32.Vec3{1, 1, 1}, Position: mgl32.Vec3{float32(math.NaN()), 0, 0}},
	)

	prevRT := *Scripts
	prevEv := scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv })
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)

	for frame := 0; frame < 12; frame++ {
		scriptEvents.overlaps = map[overlapPair]bool{} // every overlap is an enter
		Scripts.queue = Scripts.queue[:0]
		postOverlapEvents(Scripts, lvl)
		var got []overlapPair
		for _, m := range Scripts.queue {
			if m.atom == "enter" {
				got = append(got, overlapPair{container: m.to, other: m.args[0].(flang.RefVal)})
			}
		}
		want := bruteEnters(lvl)
		if len(want) == 0 {
			t.Fatal("no overlaps at all; the test level is not testing anything")
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("frame %d: %d enters from the grid, %d from asking everything\n got %v\nwant %v",
				frame, len(got), len(want), got, want)
		}
		// The set left behind is exactly the overlaps found.
		keys := make([]string, 0, len(scriptEvents.overlaps))
		for p := range scriptEvents.overlaps {
			keys = append(keys, p.container.A+"/"+p.other.A)
		}
		sort.Strings(keys)
		wantKeys := make([]string, 0, len(want))
		for _, p := range want {
			wantKeys = append(wantKeys, p.container.A+"/"+p.other.A)
		}
		sort.Strings(wantKeys)
		if !reflect.DeepEqual(keys, wantKeys) {
			t.Fatalf("frame %d: containment set differs", frame)
		}
		// Move everything a little for the next frame.
		for i := range lvl.Entities {
			lvl.Entities[i].Position = lvl.Entities[i].Position.Add(mgl32.Vec3{rng.Float32()*4 - 2, 0, rng.Float32()*4 - 2})
		}
	}
	Scripts.queue = nil
}

// Exits come out for exactly the pairs that stopped overlapping, across ticks
// where both sets are the runtime's own and swap.
func TestOverlapExitsAcrossSwappedSets(t *testing.T) {
	root := withTempScriptDir(t)
	trig := writeScript(t, root, "trig.flang", `def on_enter(:enter:, o ref) -> null
    @self.attr.in += 1
end
def on_exit(:exit:, o ref) -> null
    @self.attr.out += 1
end`)
	lvl := tickTestLevel(t)
	v := world.NewVolume("box", mgl32.Vec3{})
	v.HalfX, v.HalfY, v.HalfZ = 2, 2, 2
	v.Script = trig
	v.SetAttr("in", world.IntAttr(0))
	v.SetAttr("out", world.IntAttr(0))
	lvl.Volumes = append(lvl.Volumes, v)
	for i := 0; i < 4; i++ {
		lvl.Entities = append(lvl.Entities, world.Entity{ID: fmt.Sprintf("w%d", i), Visible: true,
			Scale: mgl32.Vec3{1, 1, 1}, Position: mgl32.Vec3{10, 0, 0}})
	}
	prevRT := *Scripts
	prevEv := scriptEvents
	t.Cleanup(func() { *Scripts = prevRT; scriptEvents = prevEv })
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)

	// Walk them in one per tick, then out one per tick.
	for i := 0; i < 4; i++ {
		lvl.Entities[i].Position = mgl32.Vec3{}
		ScriptTick(lvl, 1.0/60.0, true)
	}
	for i := 0; i < 4; i++ {
		lvl.Entities[i].Position = mgl32.Vec3{10, 0, 0}
		ScriptTick(lvl, 1.0/60.0, true)
	}
	ScriptTick(lvl, 1.0/60.0, true)
	in, _ := lvl.Volumes[0].Attr("in")
	out, _ := lvl.Volumes[0].Attr("out")
	if in.I != 4 || out.I != 4 {
		t.Fatalf("in %d, out %d; want 4 and 4", in.I, out.I)
	}
}
