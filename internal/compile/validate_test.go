package compile

// validate_test.go — what the compiler refuses.
//
// The editor warns about a concave brush and carries on, because refusing an
// invalid intermediate state means refusing to shape anything. This is the
// other half of that: the point where carrying on stops being an option,
// because CSG's "inside is behind every plane" is simply false for a concave
// solid and every stage below it inherits the wrong answer.

import (
	"strings"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// dentedBox is a box with one corner pulled through the opposite face, which is
// the shape a free vertex drag produces and the editor lets you make.
func dentedBox() world.Brush {
	b := world.NewBoxBrush("dented", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "concrete")
	b.Verts[0] = mgl32.Vec3{0.5, 0.5, 0.5}
	return b
}

// A concave STATIC brush no longer stops the compile: CSG and collision both
// read world.Chunk.ConvexBrushPieces instead of assuming a brush is already
// convex, so Validate's job here is done — it only still refuses a DYNAMIC
// one, which is a single script-moved rigid hull and cannot be split (see
// TestADynamicConcaveBrushStopsTheCompile below).
func TestAConcaveStaticBrushCompiles(t *testing.T) {
	dented := dentedBox()
	if dented.IsConvex() {
		t.Fatal("the fixture is still convex; the test proves nothing")
	}
	lvl := levelOf(dented)

	if err := Validate(lvl); err != nil {
		t.Fatalf("Validate refused a concave static brush: %v", err)
	}

	faces, err := CSG(lvl)
	if err != nil {
		t.Fatalf("CSG refused a concave static brush: %v", err)
	}
	if len(faces) == 0 {
		t.Error("CSG produced no faces for a concave brush that should have compiled")
	}
}

// A DYNAMIC concave brush is still refused: a script moves it as one rigid
// convex hull and addresses it by a single ID, and decomposing it into
// several brushes would break both — see brush_dynamic.go.
func TestADynamicConcaveBrushStopsTheCompile(t *testing.T) {
	dented := dentedBox()
	dented.Dynamic = true
	lvl := levelOf(dented)

	if err := Validate(lvl); err == nil {
		t.Fatal("Validate accepted a concave dynamic brush")
	} else if !strings.Contains(err.Error(), "dented") {
		t.Errorf("the error does not name the offending brush: %v", err)
	} else if !strings.Contains(err.Error(), "dynamic") {
		t.Errorf("the error does not explain that Dynamic is what makes this one a hard requirement: %v", err)
	}

	faces, err := CSG(lvl)
	if err == nil {
		t.Fatalf("CSG compiled a concave dynamic brush into %d faces", len(faces))
	}
	if faces != nil {
		t.Error("CSG returned faces alongside the error; a failed compile has no output")
	}
}

// A concave brush in a chunk that does not take part in the compile is not the
// compiler's problem: a frame-owned chunk is drawn live, and a disabled one is
// not in the world at all.
func TestValidationSkipsChunksTheCompileSkips(t *testing.T) {
	lvl := levelOf(dentedBox())
	lvl.Chunks[0].FrameOwner = "ship"
	if err := Validate(lvl); err != nil {
		t.Errorf("a frame-owned chunk was validated: %v", err)
	}

	lvl = levelOf(dentedBox())
	lvl.Chunks[0].Disabled = true
	if err := Validate(lvl); err != nil {
		t.Errorf("a disabled chunk was validated: %v", err)
	}
}

// Every problem at once, in a stable order — an author fixing brushes would
// rather see the whole list than rediscover it one compile at a time.
func TestValidationReportsEveryProblem(t *testing.T) {
	a, b := dentedBox(), dentedBox()
	a.ID, b.ID = "alpha", "beta"
	// Dynamic: a plain concave brush no longer breaks validation (it
	// auto-decomposes), so this needs the one shape that still does.
	a.Dynamic, b.Dynamic = true, true
	err := Validate(levelOf(a, b))
	if err == nil {
		t.Fatal("no error for two broken brushes")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("got %T, want *ValidationError", err)
	}
	if len(ve.Problems) != 2 {
		t.Fatalf("got %d problems, want one per broken brush: %v", len(ve.Problems), ve.Problems)
	}
	if !strings.Contains(ve.Problems[0], "alpha") {
		t.Errorf("problems are not in a stable order: %v", ve.Problems)
	}
}
