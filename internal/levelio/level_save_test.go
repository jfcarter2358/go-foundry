package levelio

// level_save_test.go — what the writer refuses to write.
//
// The point-naming tests that used to live here went with the shared point
// pool: a brush owns its verts outright, so there are no names to keep
// aligned and no edge ring to chain head-to-tail. What is left is the
// writer's half of the loader's own checks — a level must never be saved in a
// shape it could not load back.

import (
	"strings"
	"testing"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// squareRawBrush is one box brush in raw form, the shape validateRawChunk
// accepts.
func squareRawBrush() world.RawBrush {
	b := world.NewBoxBrush("room", mgl32.Vec3{5, 2, 5}, mgl32.Vec3{5, 2, 5}, "concrete")
	return world.BrushToRaw(&b)
}

func TestValidateRawChunkAcceptsWellFormed(t *testing.T) {
	rc := world.RawChunk{ID: "main", Brushes: []world.RawBrush{squareRawBrush()}}
	if err := validateRawChunk(&rc); err != nil {
		t.Errorf("a well-formed chunk was rejected: %v", err)
	}
}

func TestValidateRawChunkRejectsTooFewVerts(t *testing.T) {
	// Three verts cannot bound a solid, and a level saved with one would fail
	// to load back — which is exactly what this check is here to stop.
	rc := world.RawChunk{ID: "main", Brushes: []world.RawBrush{{
		ID:    "sliver",
		Verts: []float32{0, 0, 0, 1, 0, 0, 0, 1, 0},
	}}}
	err := validateRawChunk(&rc)
	if err == nil {
		t.Fatal("a brush with three verts was accepted")
	}
	if !strings.Contains(err.Error(), "at least four") {
		t.Errorf("diagnostic does not say what is wrong: %v", err)
	}
}

func TestValidateRawChunkRejectsOutOfRangeFaceIndex(t *testing.T) {
	rb := squareRawBrush()
	rb.Faces[0].Indices = []int{0, 1, 99}
	rc := world.RawChunk{ID: "main", Brushes: []world.RawBrush{rb}}

	err := validateRawChunk(&rc)
	if err == nil {
		t.Fatal("a face naming a vertex the brush does not have was accepted")
	}
	if !strings.Contains(err.Error(), "99") {
		t.Errorf("diagnostic does not name the bad index: %v", err)
	}
}

// The loader makes the same two checks on the way in. A shape one accepts and
// the other refuses would mean a level that saves and then will not open.
func TestLoaderAndWriterAgreeOnWhatIsValid(t *testing.T) {
	bad := []world.RawBrush{
		{ID: "sliver", Verts: []float32{0, 0, 0, 1, 0, 0, 0, 1, 0}},
		func() world.RawBrush {
			rb := squareRawBrush()
			rb.Faces[0].Indices = []int{0, 1, 99}
			return rb
		}(),
	}
	for _, rb := range bad {
		rc := world.RawChunk{ID: "main", Brushes: []world.RawBrush{rb}}
		writerErr := validateRawChunk(&rc)
		_, _, loaderErr := loadRawChunk(rc)
		if (writerErr == nil) != (loaderErr == nil) {
			t.Errorf("brush %q: writer err = %v but loader err = %v", rb.ID, writerErr, loaderErr)
		}
	}
}
