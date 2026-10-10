package script

// script_hitscan_test.go — the verb, from a script's side of the boundary.
//
// The ray itself is tested in internal/collision. What is tested here is the
// language boundary: the options dict in, the atom and the refs out, and the
// two mistakes that should be caught before anything runs.

import (
	"math"
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

// ---------- spread ----------

// The cone's geometry, independent of any level: a scattered shot stays inside
// the half-angle it was given, never leaves the forward hemisphere, and stays
// a unit vector however the draws come out.
func TestScatterStaysInsideItsCone(t *testing.T) {
	dir := mgl32.Vec3{0, 0, -1}
	if got := scatter(dir, 0, func() float64 { return 0.9 }); got != dir {
		t.Errorf("a spread of zero moved the aim to %v", got)
	}

	const spread = 12.0
	half := float64(spread) * math.Pi / 360
	// A draw sequence that walks the whole square, so the corners — the widest
	// angle and every spin around it — are all tried.
	draws := []float64{0, 0.25, 0.5, 0.75, 0.999999}
	for _, aim := range []mgl32.Vec3{{0, 0, -1}, {1, 0, 0}, {0, 1, 0}, {0.3, -0.9, 0.2}} {
		for _, u1 := range draws {
			for _, u2 := range draws {
				next := []float64{u1, u2}
				i := 0
				got := scatter(aim, spread, func() float64 { v := next[i]; i++; return v })
				if d := math.Abs(float64(got.Len()) - 1); d > 1e-5 {
					t.Errorf("aim %v draws %v %v: length %v, want a unit vector", aim, u1, u2, got.Len())
				}
				cos := float64(got.Normalize().Dot(aim.Normalize()))
				if cos < math.Cos(half)-1e-5 {
					t.Errorf("aim %v draws %v %v: tilted %.3f degrees, outside the %g-degree cone",
						aim, u1, u2, math.Acos(cos)*180/math.Pi, spread/2)
				}
			}
		}
	}
}

// Drawn over the cone's disc rather than over its angle: half the shots land
// inside the middle half of the cone's radius, not inside the middle half of
// its angle. Tested against the median because that is the one number the two
// distributions disagree about by a wide enough margin to assert on.
func TestScatterFillsTheConeEvenly(t *testing.T) {
	const spread = 20.0
	half := float64(spread) * math.Pi / 360
	aim := mgl32.Vec3{0, 0, -1}

	// A fixed sweep of the first draw, which is the one that sets the angle.
	inner := 0
	const n = 1000
	for i := 0; i < n; i++ {
		u1 := (float64(i) + 0.5) / n
		next := []float64{u1, 0.3}
		j := 0
		got := scatter(aim, spread, func() float64 { v := next[j]; j++; return v })
		if math.Acos(float64(got.Dot(aim))) <= half/2 {
			inner++
		}
	}
	// Uniform over the disc puts a quarter of the shots inside half the radius;
	// uniform over the angle would put half of them there.
	if inner < n/5 || inner > n*3/10 {
		t.Errorf("%d of %d shots landed in the inner half of the cone, want about a quarter — "+
			"the draw is not spread over the cone's area", inner, n)
	}
}

// And the same through the language: a spread cast is aimed off the reticle, so
// a run of them lands in different places, all of them inside the cone.
func TestHitscanSpreadScattersWhereShotsLand(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "gun.flang", `def on_spawn(:spawn:) -> null
    @self.attr.x = 0.0
    n = 0
    while n < 64
        hitscan(:shot:, {"spread": 10.0, "entities": false})
        n += 1
    end
end
def on_shot(:shot:, hit, at vec) -> null
    @self.attr.n += 1
    @self.attr.x = max(@self.attr.x, abs(at.z - 5.0))
end`)

	lvl := aimingLevel(t) // camera at (1,2,5) looking +X at a wall 9 units away
	lvl.Entities = []world.Entity{caster(path)}
	runTicks(t, lvl, 2)

	gun := &lvl.Entities[0]
	if n, _ := gun.Attr("n"); n.I != 64 {
		t.Fatalf("%d shots answered, want all 64", n.I)
	}
	// Every shot is inside a 10-degree cone, so none of them lands further off
	// the centre of the wall than the cone is wide there — and at least one of
	// them lands well off it, or the spread did nothing.
	off, _ := gun.Attr("x")
	if limit := 9 * math.Tan(5*math.Pi/180); off.F > limit+0.05 {
		t.Errorf("a shot landed %v off centre, outside the %v the cone allows", off.F, limit)
	}
	if off.F < 0.1 {
		t.Errorf("the widest shot was %v off centre: the cast did not scatter at all", off.F)
	}
}

// A cast with "from" and "direction" is someone else's shot — an enemy firing
// from its muzzle — so it goes where it is told rather than down the reticle,
// and the player's body, which is in no entity list, is something it can hit.
func TestHitscanFromAMuzzleCanHitThePlayer(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "gun.flang", `def on_spawn(:spawn:) -> null
    hitscan(:shot:, {"from": <8.0, 0.5, 5.0>, "direction": <0.0 - 1.0, 0.0, 0.0>, "ignore": @self})
end
def on_shot(:shot:, hit, at vec) -> null
    if hit == @player
        @self.attr.hit = "player"
    elif hit != null
        @self.attr.hit = hit.id
    end
end`)

	lvl := aimingLevel(t)
	lvl.Entities = []world.Entity{caster(path)}
	runTicks(t, lvl, 2)
	if got := attrStr(t, &lvl.Entities[0], "hit"); got != "player" {
		t.Errorf("hit = %q, want the player standing in the line of fire", got)
	}
}

// The reticle's own cast starts inside the player, and must not report them.
func TestHitscanFromTheReticleNeverHitsThePlayer(t *testing.T) {
	root := withTempScriptDir(t)
	path := writeScript(t, root, "gun.flang", `def on_spawn(:spawn:) -> null
    hitscan(:shot:, {"direction": <0.0, -1.0, 0.0>})
end
def on_shot(:shot:, hit, at vec) -> null
    if hit != null
        @self.attr.hit = hit.kind
    end
end`)

	lvl := aimingLevel(t)
	world.PlayerEntity.Position = gfx.Cam.Position
	lvl.Entities = []world.Entity{caster(path)}
	lvl.Entities[0].Position = mgl32.Vec3{5, 2, 5}
	runTicks(t, lvl, 2)
	if got := attrStr(t, &lvl.Entities[0], "hit"); got != "brush" {
		t.Errorf("hit = %q, want the floor under the player's feet, not the player", got)
	}
}
