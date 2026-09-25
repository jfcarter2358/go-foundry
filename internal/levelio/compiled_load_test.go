package levelio

// compiled_load_test.go — a compiled level, loaded, is a level.
//
// End to end and on purpose: the value of this stage is that everything
// downstream — the draw loop, collision, raycasts, scripts — carries on working
// against an ordinary world.Level. So the assertions are about the level that
// comes out, not about the pieces that made it.

import (
	"path/filepath"
	"testing"

	"foundry/internal/collision"
	"foundry/internal/compile"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// compileToDisk writes a level, compiles it, and returns the .fbsp path.
func compileToDisk(t *testing.T, lvl *world.Level) string {
	t.Helper()
	withResourcesRoot(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "roundtrip"+LevelExt)
	if err := SaveLevel(lvl, src); err != nil {
		t.Fatalf("save: %v", err)
	}
	c, err := CompileLevelFile(src, compile.DefaultOptions())
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	out := CompiledPath(src)
	if err := SaveCompiledLevel(c, out); err != nil {
		t.Fatalf("write: %v", err)
	}
	return out
}

// loadedRoom compiles the shared fixture and loads it back, headlessly.
func loadedRoom(t *testing.T) world.Level {
	t.Helper()
	path := compileToDisk(t, sealedLevel())
	testenv.WithoutGeometryUploads(t)
	lvl, err := LoadCompiledLevel(path)
	if err != nil {
		t.Fatalf("LoadCompiledLevel: %v", err)
	}
	return lvl
}

func TestACompiledLevelLoadsAsAnOrdinaryLevel(t *testing.T) {
	lvl := loadedRoom(t)

	if lvl.Static == nil || len(lvl.Static.Surfaces) == 0 {
		t.Fatal("no compiled surfaces came back")
	}
	if lvl.Spawn == nil {
		t.Error("the spawn did not survive")
	}
	if lvl.Lights.Global.Brightness == 0 {
		t.Error("the lights did not survive")
	}
	if lvl.Spatial == nil {
		t.Error("the spatial index was not built; nothing would find a chunk to collide with")
	}
	// Every surface has a real mesh behind it.
	for i := range lvl.Static.Surfaces {
		if len(lvl.Static.Surfaces[i].Geometry.Vertices) == 0 {
			t.Fatalf("compiled surface %d has no vertices", i)
		}
	}
}

// The division of labour: a compiled chunk keeps its brushes and loses its
// meshes, because collision wants the first and only drawing wanted the second.
func TestACompiledChunkKeepsItsBrushesAndLosesItsMeshes(t *testing.T) {
	lvl := loadedRoom(t)

	var compiled, live int
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if len(ch.Brushes) == 0 {
			t.Fatalf("chunk %q came back with no brushes at all", ch.ID)
		}
		meshed := false
		for bi := range ch.Brushes {
			for fi := range ch.Brushes[bi].Faces {
				if len(ch.Brushes[bi].Faces[fi].Geometry.Vertices) > 0 {
					meshed = true
				}
			}
		}
		if ch.Compiled {
			compiled++
			if meshed {
				t.Errorf("compiled chunk %q built meshes; they would be a second copy of the static surfaces", ch.ID)
			}
			continue
		}
		live++
		if !ch.Disabled && !meshed {
			t.Errorf("chunk %q was not compiled and has no meshes; nothing would draw it", ch.ID)
		}
	}
	if compiled == 0 || live == 0 {
		t.Fatalf("the fixture produced %d compiled and %d live chunks; it proves nothing", compiled, live)
	}
}

// The reason the brushes are kept. A player dropped into the compiled room
// lands on its floor, exactly as they would in the level it was compiled from
// — the surfaces went to the static list, the collision hulls did not move.
func TestAPlayerStillLandsOnACompiledFloor(t *testing.T) {
	lvl := loadedRoom(t)
	p := world.NewPlayer(mgl32.Vec3{0, 3, 0})

	mv := PlayerFallStep()
	var onGround bool
	for i := 0; i < 300; i++ {
		onGround = collision.MovePlayer(p, &lvl, mv).OnGround
	}
	if !onGround {
		t.Fatalf("the player never landed; it is at %v", p.Position)
	}
	// The room's floor is at y=0, and a player stands half their height above
	// whatever they are on.
	if got := p.Position.Y(); got < 0 || got > 2 {
		t.Errorf("the player came to rest at y=%v, nowhere near the room's floor", got)
	}
	if p.Position.X() != 0 || p.Position.Z() != 0 {
		t.Errorf("the player slid to %v while falling straight down", p.Position)
	}
}

// PlayerFallStep is the gameplay tuning a dropped body falls under, matching
// what the running game uses so this is a test against the real numbers.
func PlayerFallStep() collision.PlayerMove {
	return collision.PlayerMove{
		Gravity:      true,
		DT:           1.0 / 60,
		GravityAccel: world.DefaultGravity,
		MaxFallSpeed: world.DefaultMaxFallSpeed,
		JumpVelocity: world.PlayerJumpVelocity,
	}
}

// Visibility is doing something: a sealed room sees only its own surfaces, and
// the fixture has more than that.
func TestTheLoadedLevelCullsWithItsVisibilitySet(t *testing.T) {
	lvl := loadedRoom(t)

	inside := lvl.Static.VisibleSurfaces(mgl32.Vec3{0, 2, 0}, nil)
	if len(inside) == 0 {
		t.Fatal("standing in the room, nothing is visible")
	}
	if len(inside) >= len(lvl.Static.Surfaces) {
		t.Errorf("standing in the room, all %d surfaces are visible; the set is culling nothing", len(inside))
	}
}

// The seam test, at the level it actually matters: two fragments of ONE
// authored face that meet share a corner, and that corner must have the same
// texture coordinate in both. Anything else is a visible break in the texture
// along every cut the compiler made — and it makes them everywhere.
//
// Keyed by the source face as well as the position, because two surfaces on
// different planes meeting at an edge legitimately disagree: they have
// different projections, and a room corner is exactly that.
func TestTheTextureRunsUnbrokenAcrossACut(t *testing.T) {
	lvl := loadedRoom(t)

	type key struct {
		chunk, brush string
		normal       [3]int
		pos          [3]float32
	}
	type uv struct{ u, v float32 }
	at := map[key]uv{}
	checked := 0
	for si := range lvl.Static.Surfaces {
		surf := &lvl.Static.Surfaces[si]
		n := [3]int{
			int(surf.Normal.X() * 100), int(surf.Normal.Y() * 100), int(surf.Normal.Z() * 100),
		}
		verts := surf.Geometry.Vertices
		for i := 0; i+5 <= len(verts); i += 5 {
			k := key{surf.Chunk, surf.Brush, n, [3]float32{verts[i], verts[i+1], verts[i+2]}}
			got := uv{verts[i+3], verts[i+4]}
			was, seen := at[k]
			if !seen {
				at[k] = got
				continue
			}
			checked++
			if !geomNear(was.u, got.u) || !geomNear(was.v, got.v) {
				t.Fatalf("the corner %v of %s/%s has texture coordinates (%v,%v) on one fragment and (%v,%v) on another",
					k.pos, k.chunk, k.brush, was.u, was.v, got.u, got.v)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no corner was shared by two fragments of one face; the test proves nothing")
	}
}

func geomNear(a, b float32) bool {
	d := a - b
	return d < 0.001 && d > -0.001
}
