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
		onGround = collision.MovePlayer(p, &lvl, mv)
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
		DTScale:      1.0,
		GravityAccel: 0.010,
		MaxFallSpeed: 0.50,
		JumpVelocity: 0.20,
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
