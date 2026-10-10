package render

// A kept cubemap face is only right if its signature moves whenever anything
// that face draws moves — and it is only worth anything if it stays put when
// nothing the face can see has.

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func shadowCacheLevel() (*world.Level, world.PointLight) {
	tri := []float32{0, 0, 0, 0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1}
	skinned := gfx.Geometry{Vertices: tri, Alpha: 1, BonePalette: []mgl32.Mat4{mgl32.Ident4()}}
	ent := func(id string, at mgl32.Vec3) world.Entity {
		return world.Entity{ID: id, Visible: true, Position: at, BoundHalfX: 0.5, BoundHalfY: 1, BoundHalfZ: 0.5,
			Models: []world.EntityModel{{Visible: true, Submeshes: []gfx.Geometry{skinned}}}}
	}
	door := world.NewBoxBrush("door", mgl32.Vec3{2, 1, 0}, mgl32.Vec3{0.1, 1, 1}, "concrete")
	door.Dynamic = true
	ch := world.Chunk{ID: "room", Scale: 1, Brushes: []world.Brush{door}}
	ch.RecomputeBounds()
	lvl := &world.Level{
		Chunks:   []world.Chunk{ch},
		Entities: []world.Entity{ent("near", mgl32.Vec3{1, 0, 1}), ent("far", mgl32.Vec3{40, 0, 0})},
	}
	// Each entity its own pose, as a real one has.
	for i := range lvl.Entities {
		sm := &lvl.Entities[i].Models[0].Submeshes[0]
		sm.BonePalette = []mgl32.Mat4{mgl32.Ident4()}
	}
	return lvl, world.PointLight{Position: mgl32.Vec3{0, 2, 0}, Radius: 10}
}

type pointShadowSigs struct {
	static uint64
	faces  [6]uint64
}

func TestAPointShadowSignatureMovesWithWhatTheLightCanSee(t *testing.T) {
	lvl, pl := shadowCacheLevel()
	sig := func() pointShadowSigs {
		st, faces, _, ok := pointShadowSignatures(lvl, pl, 7)
		if !ok {
			t.Fatal("nothing in this level should force a render")
		}
		return pointShadowSigs{st, faces}
	}
	base := sig()
	if sig() != base {
		t.Fatal("the same scene hashed two ways")
	}

	same := func(what string) {
		t.Helper()
		if s := sig(); s != base {
			t.Fatalf("%s changed the signature, and cost a re-render for nothing", what)
		}
	}
	// moved: the half that must move, and the static half left alone when only
	// a moving caster did — or the static layer is redrawn for nothing.
	moved := func(what string, staticToo bool) {
		t.Helper()
		s := sig()
		if s == base {
			t.Fatalf("%s did not change the signature: its shadow would freeze", what)
		}
		if staticToo && s.static == base.static {
			t.Fatalf("%s did not change the static layer's signature", what)
		}
		if !staticToo && s.static != base.static {
			t.Fatalf("%s changed the static layer's signature: the room is redrawn for a character", what)
		}
		base = s
	}

	lvl.Entities[1].Position = mgl32.Vec3{41, 0, 0}
	same("an entity walking about outside the light's reach")

	lvl.Entities[0].Position = mgl32.Vec3{1.5, 0, 1}
	moved("an entity moving inside the light's reach", false)

	lvl.Entities[0].Models[0].Submeshes[0].BonePalette[0] = mgl32.Translate3D(0, 0.1, 0)
	moved("an entity in reach changing pose", false)

	lvl.Entities[1].Models[0].Submeshes[0].BonePalette[0] = mgl32.Translate3D(0, 0.1, 0)
	same("an entity out of reach changing pose")

	lvl.Entities[0].Visible = false
	moved("an entity in reach being hidden", false)

	lvl.Entities[1].Position = mgl32.Vec3{3, 0, 0}
	moved("an entity walking into the light's reach", false)

	lvl.Chunks[0].Brushes[0].Offset = mgl32.Vec3{0, 0, 1}
	moved("a door sliding", true)

	world.MarkBrushMeshesChanged()
	moved("a brush mesh edited anywhere", true)

	lvl.Chunks[0].Disabled = true
	moved("the room being switched off", true)

	pl.Position = mgl32.Vec3{0, 2.5, 0}
	moved("the light moving", true)

	if st, _, _, _ := pointShadowSignatures(lvl, pl, 8); st == base.static {
		t.Fatal("a different cubemap took the signature of another's contents")
	}
}

// Only the faces that can see a caster are dirtied by it: a character on the
// floor under a ceiling lamp is nothing to the face looking up.
func TestAMoverDirtiesOnlyTheFacesThatSeeIt(t *testing.T) {
	lvl, pl := shadowCacheLevel()
	// Straight below the light, two metres down: the -Y face's alone, or near
	// enough — its box pokes into the side faces' frusta, but not up.
	lvl.Entities[0].Position = mgl32.Vec3{0, -0.5, 0.2}
	_, mid, _, _ := pointShadowSignatures(lvl, pl, 7)
	lvl.Entities[0].Position = mgl32.Vec3{0, -0.5, 0.3}
	_, after, _, _ := pointShadowSignatures(lvl, pl, 7)
	changed := 0
	for f := range after {
		if after[f] != mid[f] {
			changed++
		}
	}
	const up = 2 // +Y
	if after[up] != mid[up] {
		t.Error("a mover under the light dirtied the face that looks up")
	}
	if mid[3] == after[3] {
		t.Error("a mover under the light did not dirty the face that looks down at it")
	}
	if changed == 6 {
		t.Error("a mover seen by one or two faces dirtied all six")
	}
}

// A posed entity is signed by its animation's pose generation, not bone by
// bone: a new pose steps the generation (world.EntityAnim.setPalette), and the
// generation is what the face has to see change.
func TestAPosedEntityIsSignedByItsPoseGeneration(t *testing.T) {
	lvl, pl := shadowCacheLevel()
	m := &lvl.Entities[0].Models[0]
	m.Anim = &world.EntityAnim{Palette: m.Submeshes[0].BonePalette}
	sig := func() pointShadowSigs {
		st, faces, _, _ := pointShadowSignatures(lvl, pl, 7)
		return pointShadowSigs{st, faces}
	}
	base := sig()
	m.Anim.PoseGen++
	if sig() == base {
		t.Fatal("a new pose generation left the signature alone: the character's shadow would freeze")
	}
}

func TestAPointShadowRedrawsOnlyTheHalfThatChanged(t *testing.T) {
	lvl, pl := shadowCacheLevel()
	pointShadowStates = nil
	t.Cleanup(func() { pointShadowStates = nil })
	// A map whose static layer already exists, so planning makes no GL calls.
	pm := &PointShadowMap{Cubemap: 3, StaticCubemap: 4, StaticFBO: 5}

	plan := func(what string) pointShadowPlan {
		t.Helper()
		return planPointShadow(0, lvl, pl, pm)
	}
	expect := func(what string, want pointShadowPlan) {
		t.Helper()
		if got := plan(what); got != want {
			t.Fatalf("%s: planned %+v, want %+v", what, got, want)
		}
	}
	expect("a map never rendered", pointShadowPlan{faces: allPointShadowFaces, static: true})
	expect("nothing changed", pointShadowPlan{})

	lvl.Entities[0].Position = mgl32.Vec3{0, 0, 2}
	got := plan("a character moved in reach")
	if got.static || got.all || got.keep() {
		t.Fatalf("a character moved in reach: planned %+v, want some faces' moving layer alone", got)
	}
	if got.faces == allPointShadowFaces || got.faces&(1<<2) != 0 {
		t.Fatalf("a character moved below the light: planned faces %06b, want not all, and not +Y", got.faces)
	}
	expect("and then stood still", pointShadowPlan{})

	lvl.Chunks[0].Brushes[0].Offset = mgl32.Vec3{0, 0, 1}
	expect("the door slid", pointShadowPlan{faces: allPointShadowFaces, static: true})

	// With nothing moving in reach there is no static layer to keep apart.
	lvl.Entities[0].Visible, lvl.Entities[1].Visible = false, false
	world.MarkBrushMeshesChanged()
	expect("the room changed with nobody in it", pointShadowPlan{faces: allPointShadowFaces, all: true})

	PointShadowCaching = false
	t.Cleanup(func() { PointShadowCaching = true })
	expect("caching off", pointShadowPlan{faces: allPointShadowFaces, all: true})
	expect("caching off, nothing changed", pointShadowPlan{faces: allPointShadowFaces, all: true})
}
