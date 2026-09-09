package editor

import (
	"foundry/internal/model"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// A texture name becomes a file stem under textures/, so anything that could
// steer a write out of that directory has to be stripped.
func TestSanitizeTextureNameRejectsPathEscapes(t *testing.T) {
	cases := map[string]string{
		"crate":            "crate",
		"  crate  ":        "crate",
		"my crate":         "my_crate",
		"../../etc/passwd": "etcpasswd",
		"a/b":              "ab",
		`c:\win`:           "cwin",
		"weird!@#$%":       "weird",
		"":                 "",
		"...":              "",
		"keep-_09":         "keep-_09",
	}
	for in, want := range cases {
		if got := sanitizeTextureName(in); got != want {
			t.Errorf("sanitizeTextureName(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"../../etc/passwd", "a/b", `c:\win`} {
		got := sanitizeTextureName(in)
		if strings.ContainsAny(got, `/\.`) {
			t.Errorf("sanitizeTextureName(%q) = %q still carries a path character", in, got)
		}
	}
}

// The suggested name has to be usable as a file stem even when the model's ID
// is not, and must never come back empty.
func TestModelUnwrapSuggestedNameIsAlwaysUsable(t *testing.T) {
	prev := modelDoc
	defer func() { modelDoc = prev }()

	for _, id := range []string{"crate", "my model", "!!!", ""} {
		modelDoc = model.NewCubeDoc()
		modelDoc.ID = id
		got := modelUnwrapSuggestedName()
		if got == "" {
			t.Errorf("ID %q gave an empty name", id)
		}
		if got != sanitizeTextureName(got) {
			t.Errorf("ID %q gave unsanitised name %q", id, got)
		}
	}
}

// The overlay is what tells a painter which texels belong to which face, so it
// has to report the model's real UVs — and stay silent about a texture this
// model does not wear, or it would draw someone else's layout over an
// unrelated image.
func TestModelUVLayoutOnlyForTheModelsOwnTexture(t *testing.T) {
	prev := modelDoc
	defer func() { modelDoc = prev }()

	modelDoc = model.NewCubeDoc()
	modelDoc.SetTexture("crate_atlas")

	if got := modelUVLayoutFor("crate_atlas"); len(got) != 6 {
		t.Errorf("layout loops = %d, want 6", len(got))
	}
	if got := modelUVLayoutFor("something_else"); got != nil {
		t.Errorf("layout returned %d loops for a texture the model does not wear", len(got))
	}
	if got := modelUVLayoutFor(""); got != nil {
		t.Error("layout returned loops for an unbound document")
	}

	// An untextured model wears nothing, so no texture in the editor gets its
	// layout drawn over it.
	modelDoc = model.NewCubeDoc()
	if got := modelUVLayoutFor("crate_atlas"); got != nil {
		t.Errorf("layout returned %d loops for a model wearing no texture", len(got))
	}
}

// The loops must be the document's actual UVs, not a re-derivation — that is
// what makes the overlay follow a vertex the moment it moves.
func TestModelUVLayoutReflectsLiveUVs(t *testing.T) {
	prev := modelDoc
	defer func() { modelDoc = prev }()

	modelDoc = model.NewCubeDoc()
	modelDoc.SetTexture("crate_atlas")
	modelDoc.Faces[0].UVs[0] = mgl32.Vec2{0.125, 0.875}

	loops := modelUVLayoutFor("crate_atlas")
	if len(loops) == 0 {
		t.Fatal("no loops")
	}
	if loops[0][0] != (mgl32.Vec2{0.125, 0.875}) {
		t.Errorf("loop corner = %v, want the document's own UV", loops[0][0])
	}
}

// A degenerate face has no outline to draw and would render as a stray line.
func TestModelUVLayoutSkipsDegenerateFaces(t *testing.T) {
	prev := modelDoc
	defer func() { modelDoc = prev }()

	modelDoc = model.NewCubeDoc()
	modelDoc.SetTexture("crate_atlas")
	modelDoc.Faces = append(modelDoc.Faces, model.ModelFace{
		Verts: []int{0, 1},
		UVs:   make([]mgl32.Vec2, 2),
	})
	modelDoc.Invalidate()

	if got := modelUVLayoutFor("crate_atlas"); len(got) != 6 {
		t.Errorf("layout loops = %d, want 6 (the two-vertex face must be skipped)", len(got))
	}
}

// A nil document must not panic the texture editor's canvas draw.
func TestModelUVLayoutHandlesNoDocument(t *testing.T) {
	prev := modelDoc
	defer func() { modelDoc = prev }()

	modelDoc = nil
	if got := modelUVLayoutFor("anything"); got != nil {
		t.Error("layout returned loops with no document open")
	}
}
