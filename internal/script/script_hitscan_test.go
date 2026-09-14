package script

// script_hitscan_test.go — the verb, from a script's side of the boundary.
//
// The ray itself is tested in internal/collision. What is tested here is the
// language boundary: the options dict in, the atom and the refs out, and the
// two mistakes that should be caught before anything runs.

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/leveltest"
	"foundry/internal/world"
)

// aimingLevel is leveltest's square room with a player standing at one end of
// it looking down +X, geometry built so the far wall is really there.
//
// The camera is what the reticle is, so pointing it is the whole of setting up
// a shot. Both it and the player are engine globals, so both are put back.
func aimingLevel(t *testing.T) *world.Level {
	t.Helper()
	ch := leveltest.RoomChunk(t)
	ch.ID = "chunk"
	leveltest.LoadGeometry(t, ch)
	lvl := &world.Level{Chunks: []world.Chunk{*ch}}

	prevCam, prevPlayer := gfx.Cam, world.PlayerEntity
	t.Cleanup(func() { gfx.Cam, world.PlayerEntity = prevCam, prevPlayer })

	player := world.NewPlayer(mgl32.Vec3{1, 0.3, 5})
	world.PlayerEntity = player
	gfx.Cam.Position = mgl32.Vec3{1, 2, 5}
	gfx.Cam.Direction = mgl32.Vec3{1, 0, 0}
	return lvl
}

// caster returns an entity carrying the script at path, with attributes ready
// for a handler to write what it saw into.
func caster(path string) world.Entity {
	return world.Entity{
		ID: "gun", Script: path, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Position: mgl32.Vec3{1, 2, 5},
		Attributes: world.AttrSet{
			"hit":  world.StringAttr(""),
			"kind": world.StringAttr(""),
			"n":    world.IntAttr(0),
			"x":    world.FloatAttr(0),
		},
	}
}

// target is a 1-unit cube at x, in the ray's path.
func target(id string, x float32, tags ...string) world.Entity {
	e := world.Entity{
		ID: id, Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Position:   mgl32.Vec3{x, 2, 5},
		BoundHalfX: 0.5, BoundHalfY: 0.5, BoundHalfZ: 0.5,
		Attributes: world.AttrSet{},
	}
	e.Tags = tags
	return e
}

func attrStr(t *testing.T, e *world.Entity, name string) string {
	t.Helper()
	a, ok := e.Attr(name)
	if !ok {
		t.Fatalf("entity %s has no attribute %q", e.ID, name)
	}
	return a.S
}

func TestHitscanPostsTheNearestHitToTheCaster(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "gun.flang", `def on_spawn(:spawn:) -> null
    hitscan(:shot:)
end
def on_shot(:shot:, hit, at vec) -> null
    @self.attr.hit = hit.id
    @self.attr.kind = hit.kind
    @self.attr.x = at.x
end`)

	lvl := aimingLevel(t)
	lvl.Entities = []world.Entity{caster(path), target("far", 8), target("near", 4)}

	runTicks(t, lvl, 2)

	gun := &lvl.Entities[0]
	if got := attrStr(t, gun, "hit"); got != "near" {
		t.Errorf("hit = %q, want the nearer of the two entities", got)
	}
	if got := attrStr(t, gun, "kind"); got != "entity" {
		t.Errorf("kind = %q, want entity", got)
	}
	// The `at` vec is where the ray stopped, which is the far wall — not the
	// entity, which the ray passed through.
	a, _ := gun.Attr("x")
	if a.F < 9.99 || a.F > 10.01 {
		t.Errorf("at.x = %v, want the far wall at 10", a.F)
	}
}

func TestHitscanAllReportsTheWholeLineOfFire(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "gun.flang", `def on_spawn(:spawn:) -> null
    hitscan(:shot:, {"all": true})
end
def on_shot(:shot:, hits list, at vec) -> null
    @self.attr.n = len(hits)
    @self.attr.hit = hits[0].id
    @self.attr.kind = hits[len(hits) - 1].kind
end`)

	lvl := aimingLevel(t)
	lvl.Entities = []world.Entity{caster(path), target("far", 8), target("near", 4)}

	runTicks(t, lvl, 2)

	gun := &lvl.Entities[0]
	if n, _ := gun.Attr("n"); n.I != 3 {
		t.Errorf("hits = %d, want both entities and the wall they are in front of", n.I)
	}
	if got := attrStr(t, gun, "hit"); got != "near" {
		t.Errorf("first hit = %q, want nearest first", got)
	}
	if got := attrStr(t, gun, "kind"); got != "brush" {
		t.Errorf("last hit kind = %q, want the brush the ray stopped on", got)
	}
}

func TestHitscanReportsNullWhenItHitsNothing(t *testing.T) {
	// A shot into the sky still answers, so a handler waiting on the atom is
	// never left waiting.
	root := withTempScriptDir(t)
	path := writeScript(t, root, "gun.flang", `def on_spawn(:spawn:) -> null
    hitscan(:shot:, {"geometry": false})
end
def on_shot(:shot:, hit, at vec) -> null
    @self.attr.n = 1
    if hit == null
        @self.attr.hit = "nothing"
    end
end`)

	lvl := aimingLevel(t)
	lvl.Entities = []world.Entity{caster(path)}

	runTicks(t, lvl, 2)

	gun := &lvl.Entities[0]
	if n, _ := gun.Attr("n"); n.I != 1 {
		t.Fatal("the answering message was never posted")
	}
	if got := attrStr(t, gun, "hit"); got != "nothing" {
		t.Errorf("hit = %q, want null for a shot that met nothing", got)
	}
}

func TestHitscanOptionsNarrowWhatIsReported(t *testing.T) {
	src := `def on_spawn(:spawn:) -> null
    hitscan(:shot:, OPTS)
end
def on_shot(:shot:, hit, at vec) -> null
    @self.attr.hit = hit.id
end`

	for _, tc := range []struct {
		name string
		opts string
		want string
	}{
		{"tag reports only what carries it", `{"tag": "enemy"}`, "guard"},
		{"ignore passes straight through", `{"ignore": @crate}`, "guard"},
		{"visible_only skips what is not drawn", `{"visible_only": true}`, "guard"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := withTempScriptDir(t)
			path := writeScript(t, root, "gun.flang", strings.Replace(src, "OPTS", tc.opts, 1))

			lvl := aimingLevel(t)
			crate := target("crate", 3)
			crate.Visible = false
			lvl.Entities = []world.Entity{caster(path), crate, target("guard", 5, "enemy")}

			runTicks(t, lvl, 2)

			if got := attrStr(t, &lvl.Entities[0], "hit"); got != tc.want {
				t.Errorf("hit = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHitscanBrushHitIsTheBrushAScriptAddresses(t *testing.T) {
	// The ref a geometry hit comes back as has to be one a script can then use.
	//
	// TODO(brush migration): this used to write `hit.prop.texture` on the wall
	// it hit. A texture lives on a brush FACE, and faces have no address yet
	// (see script_props_geom.go's brushProps), so the write half of this comes
	// back when they do.
	root := withTempScriptDir(t)
	path := writeScript(t, root, "gun.flang", `def on_spawn(:spawn:) -> null
    hitscan(:shot:)
end
def on_shot(:shot:, hit, at vec) -> null
    @self.attr.kind = hit.kind
    @self.attr.hit = hit.id
end`)

	lvl := aimingLevel(t)
	lvl.Entities = []world.Entity{caster(path)}

	runTicks(t, lvl, 2)

	gun := &lvl.Entities[0]
	if got := attrStr(t, gun, "kind"); got != "brush" {
		t.Fatalf("kind = %q, want brush", got)
	}
	if got := attrStr(t, gun, "hit"); got == "" {
		t.Error("the brush hit reported no id")
	}
}

func TestHitscanWithNoPlayerWarnsAndPostsNothing(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "gun.flang", `def on_spawn(:spawn:) -> null
    hitscan(:shot:)
end
def on_shot(:shot:, hit, at vec) -> null
    @self.attr.n = 1
end`)

	lvl := aimingLevel(t)
	world.PlayerEntity = nil
	lvl.Entities = []world.Entity{caster(path), target("near", 4)}

	runTicks(t, lvl, 2)

	if n, _ := lvl.Entities[0].Attr("n"); n.I != 0 {
		t.Error("a cast with no player to cast from still answered")
	}
	if e := ScriptStatsSnapshot().Errors; e != 0 {
		t.Errorf("%d error(s); a missing player is a warning, not an error", e)
	}
}

func TestHitscanRejectsAnUnknownOption(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "gun.flang", `def on_spawn(:spawn:) -> null
    hitscan(:shot:, {"visable_only": true})
end
def on_shot(:shot:, hit, at vec) -> null
end`)

	lvl := aimingLevel(t)
	lvl.Entities = []world.Entity{caster(path)}

	// One frame, because the stats are per-tick: the cast happens on :spawn:.
	runTicks(t, lvl, 1)

	if ScriptStatsSnapshot().Errors == 0 {
		t.Error("a misspelled option ran silently")
	}
}

func TestHitscanLintCatchesTheMistakesBeforeItRuns(t *testing.T) {
	lvl := aimingLevel(t)
	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{
			"misspelled option",
			`def on_spawn(:spawn:) -> null
    hitscan(:shot:, {"visable_only": true})
end
def on_shot(:shot:, hit, at vec) -> null
end`,
			`did you mean "visible_only"`,
		},
		{
			"answering atom nothing handles",
			`def on_spawn(:spawn:) -> null
    hitscan(:shot:)
end`,
			"defines no clause for it",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var found bool
			for _, iss := range lintSource(t, lvl, tc.src) {
				found = found || strings.Contains(iss.Msg, tc.want)
			}
			if !found {
				t.Errorf("no issue mentioning %q in %v", tc.want, lintSource(t, lvl, tc.src))
			}
		})
	}
}

func TestHitscanOptionNamesAndSummariesAgree(t *testing.T) {
	// The reference panel reads both, so an option with no summary would show
	// as a blank row rather than fail anywhere.
	names := HitscanOptionNames()
	if len(names) != len(hitscanOptions) {
		t.Fatalf("HitscanOptionNames listed %d of %d options", len(names), len(hitscanOptions))
	}
	for _, n := range names {
		if HitscanOptionSummary(n) == "" {
			t.Errorf("option %q has no summary", n)
		}
	}
	if _, ok := hitscanVerbs["hitscan"]; !ok {
		t.Error("the verb table has no hitscan")
	}
	if !flang.IsBuiltin("hitscan") {
		t.Error("the language does not know hitscan is a builtin")
	}
}

// `"points": true` adds where each hit landed. Bodies do not stop the ray, so
// `at` is the wall behind a target — the points are the only way to put an
// effect on the target itself.
func TestHitscanPointsSayWhereEachHitLanded(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "gun.flang", `def on_spawn(:spawn:) -> null
    hitscan(:near_shot:, {"points": true})
    hitscan(:all_shot:, {"all": true, "points": true})
end
def on_near(:near_shot:, hit, at vec, point vec) -> null
    @self.attr.x = point.x
end
def on_all(:all_shot:, hits list, at vec, points list) -> null
    @self.attr.n = len(points)
    @self.attr.hit = format("{}", points[1].x)
end`)

	lvl := aimingLevel(t)
	lvl.Entities = []world.Entity{caster(path), target("far", 8), target("near", 4)}

	runTicks(t, lvl, 2)

	gun := &lvl.Entities[0]
	// The near cube spans x 3.5..4.5; the ray enters it at 3.5.
	if a, _ := gun.Attr("x"); a.F < 3.49 || a.F > 3.51 {
		t.Errorf("nearest point x = %v, want the near cube's face at 3.5", a.F)
	}
	if n, _ := gun.Attr("n"); n.I != 3 {
		t.Errorf("points = %d, want one per hit (two cubes and the wall)", n.I)
	}
	if got := attrStr(t, gun, "hit"); got != "7.5" {
		t.Errorf("second point x = %q, want the far cube's face at 7.5", got)
	}
}

// Without the option the message keeps its two arguments, so a handler
// written before it still matches.
func TestHitscanWithoutPointsKeepsTwoArguments(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "gun.flang", `def on_spawn(:spawn:) -> null
    hitscan(:shot:)
end
def on_shot(:shot:, hit, at vec) -> null
    @self.attr.n = 2
end`)

	lvl := aimingLevel(t)
	lvl.Entities = []world.Entity{caster(path), target("near", 4)}
	runTicks(t, lvl, 2)

	if n, _ := lvl.Entities[0].Attr("n"); n.I != 2 {
		t.Error("a two-argument handler no longer matched")
	}
}

// The player's aim is the camera's, which is what a thrown grenade needs and
// what Entity.Rotation never tracks; anything else looks down its own −Z.
func TestEyePositionAndLookDirection(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "look.flang", `def on_spawn(:spawn:) -> null
    d = @player.prop.look_direction
    e = @player.prop.eye_position
    @self.attr.x = d.x
    @self.attr.hit = format("{} {}", e.y, @near.prop.look_direction.x)
end`)

	lvl := aimingLevel(t) // camera at (1,2,5) looking +X
	gfx.Cam.Direction = mgl32.Vec3{2, 0, 0}
	near := target("near", 4)
	near.Rotation = mgl32.Vec3{0, 3.14159265 / 2, 0} // −Z turned a quarter left is −X
	lvl.Entities = []world.Entity{caster(path), near}
	runTicks(t, lvl, 2)

	gun := &lvl.Entities[0]
	if a, _ := gun.Attr("x"); a.F < 0.999 || a.F > 1.001 {
		t.Errorf("player look_direction.x = %v, want the camera's aim normalised to 1", a.F)
	}
	if got := attrStr(t, gun, "hit"); !strings.HasPrefix(got, "2.0 -1.0") && !strings.HasPrefix(got, "2 -1") {
		t.Errorf("eye y and entity look x = %q, want the camera's height 2 and −X", got)
	}
}
