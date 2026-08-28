package particle

// particle_file_test.go — the .fpar document through the YAML boundary.
//
// The second test is the one that matters. Most of a layer's numbers have a
// meaningful zero — a Rate of 0 is a burst-only layer, a Gravity of 0 floats —
// so the schema mixes plain omitempty fields with pointers, and the rule for
// which is which is easy to get wrong in a way nothing else catches: a default
// written out explicitly turns a three-line effect into a forty-line one, and a
// pointer that should have been plain silently rewrites an authored zero into a
// default on the next load.

import (
	"foundry/internal/gfx"
	"reflect"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

func TestDocRoundTripsEveryField(t *testing.T) {
	want := &Doc{
		ID:      "torch",
		Seed:    4242,
		Gravity: 3.5,
		Layers: []Layer{{
			Name:           "flame",
			Muted:          true,
			Rate:           60,
			Duration:       2.5,
			Loop:           false,
			Prewarm:        true,
			MaxParticles:   250,
			Bursts:         []Burst{{Time: 0, Count: 30}, {Time: 1.5, Count: 5}},
			Shape:          ShapeCone,
			Radius:         0.15,
			Angle:          18,
			BoxHalf:        mgl32.Vec3{1, 2, 3},
			Thickness:      0.4,
			Dir:            DirFixed,
			Direction:      mgl32.Vec3{0, 0, 1},
			Spread:         12,
			LifeMin:        0.8,
			LifeMax:        1.4,
			SpeedMin:       1.2,
			SpeedMax:       2,
			SizeMin:        0.3,
			SizeMax:        0.5,
			RotMin:         -1,
			RotMax:         1,
			RotVelMin:      -2,
			RotVelMax:      2,
			Gravity:        -0.2,
			Drag:           0.4,
			Radial:         0.5,
			Tangential:     0.25,
			TurbulenceAmp:  0.6,
			TurbulenceFreq: 1.5,
			LocalSpace:     true,
			Texture:        "flame_sheet",
			SheetCols:      4,
			SheetRows:      4,
			FrameMode:      FrameRandom,
			Blend:          gfx.SpriteBlendAdditive,
			Billboard:      gfx.BillboardYAxis,
			Lit:            true,
			CastShadows:    true,
			Tint:           mgl32.Vec3{1, 0.5, 0.25},
			Collision:      CollideBounce,
			CollideRadius:  0.05,
			Bounce:         0.4,
			Friction:       0.2,
			SizeRamp:       Ramp{{T: 0, V: 1}, {T: 1, V: 0.2}},
			AlphaRamp:      Ramp{{T: 0, V: 0}, {T: 0.2, V: 1}, {T: 1, V: 0}},
			ColorRamp:      Gradient{{T: 0, C: mgl32.Vec3{1, 1, 0.8}}, {T: 1, C: mgl32.Vec3{0.6, 0, 0}}},
		}},
	}

	got := rawToDoc(docToRaw(want))
	// Path and the dirty/rev pair are runtime state, not document state.
	got.Path = want.Path
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("document changed across a round trip:\n got %+v\nwant %+v", got.Layers[0], want.Layers[0])
	}
}

// TestDefaultLayerWritesNoRedundantKeys is the omitempty contract.
func TestDefaultLayerWritesNoRedundantKeys(t *testing.T) {
	// A layer at the schema's own defaults, not NewLayer's — NewLayer
	// deliberately picks non-default values so a fresh effect is visible.
	d := &Doc{
		ID:      "e",
		Gravity: DefaultGravity,
		Layers:  []Layer{{Name: "l", Duration: 1, Loop: true, MaxParticles: 500, BoxHalf: mgl32.Vec3{0.5, 0.5, 0.5}, Direction: mgl32.Vec3{0, 1, 0}, Tint: mgl32.Vec3{1, 1, 1}}},
	}
	out, err := yaml.Marshal(docToRaw(d))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(out)
	for _, key := range []string{
		"gravity:", "duration:", "loop:", "max:", "shape:", "direction_mode:",
		"box_half:", "direction:", "frames:", "blend:", "billboard:", "tint:",
		"collision:", "bursts:", "size_ramp:", "alpha_ramp:", "color_ramp:",
		"muted:", "prewarm:", "lit:", "cast_shadows:", "local_space:",
	} {
		if strings.Contains(text, key) {
			t.Errorf("a default layer wrote %q; it should be omitted:\n%s", key, text)
		}
	}
}

// TestAuthoredZeroSurvives is the other half of that contract, and the failure
// mode a pointer-where-a-plain-field-belongs produces: a rate of zero is a
// burst-only layer, and it must not come back as the default on reload.
func TestAuthoredZeroSurvives(t *testing.T) {
	d := &Doc{ID: "e", Gravity: DefaultGravity, Layers: []Layer{{
		Name: "burst-only", Rate: 0, Gravity: 0, Duration: 1, Loop: true,
		MaxParticles: 500, Bursts: []Burst{{Time: 0, Count: 10}},
	}}}
	back := rawToDoc(docToRaw(d))
	if back.Layers[0].Rate != 0 {
		t.Errorf("an authored rate of 0 came back as %v", back.Layers[0].Rate)
	}
	if back.Layers[0].Gravity != 0 {
		t.Errorf("an authored gravity of 0 came back as %v", back.Layers[0].Gravity)
	}
}

// TestValidationRejectsWhatWouldMisbehave — and only that. Anything clampable
// is clamped on load instead, so a document written by a newer build opens.
func TestValidation(t *testing.T) {
	if err := validateRawParticle(RawParticle{ID: "e"}); err == nil {
		t.Error("an effect with no layers was accepted")
	}
	if err := validateRawParticle(RawParticle{ID: "e", Layers: []RawLayer{{}}}); err == nil {
		t.Error("a layer with no name was accepted")
	}
	dup := RawParticle{ID: "e", Layers: []RawLayer{{Name: "a"}, {Name: "a"}}}
	if err := validateRawParticle(dup); err == nil {
		t.Error("two layers with the same name were accepted; one would be unreachable")
	}
	huge := MaxPoolSize + 1
	over := RawParticle{ID: "e", Layers: []RawLayer{{Name: "a", MaxParticles: &huge}}}
	if err := validateRawParticle(over); err == nil {
		t.Error("a pool over the hard cap was accepted")
	}
	ok := RawParticle{ID: "e", Layers: []RawLayer{{Name: "a"}, {Name: "b"}}}
	if err := validateRawParticle(ok); err != nil {
		t.Errorf("a valid effect was rejected: %v", err)
	}
}

// TestUnknownNamesLoadAsDefaults — a document written by a newer build, naming
// a shape this build has never heard of, still opens.
func TestUnknownNamesLoadAsDefaults(t *testing.T) {
	l := rawToLayer(RawLayer{
		Name: "l", Shape: "dodecahedron", Blend: "screen",
		Billboard: "sideways", FrameMode: "backwards", Collision: "explode",
		Dir: "sideways",
	})
	if l.Shape != ShapePoint {
		t.Errorf("unknown shape loaded as %v, want the default", l.Shape)
	}
	if l.Blend != gfx.SpriteBlendCutout {
		t.Errorf("unknown blend loaded as %v, want the default", l.Blend)
	}
	if l.Billboard != gfx.BillboardFull {
		t.Errorf("unknown billboard loaded as %v, want the default", l.Billboard)
	}
	if l.FrameMode != FrameOverLife || l.Collision != CollideNone || l.Dir != DirShape {
		t.Error("an unknown mode name did not fall back to its default")
	}
}

// TestCloneIsDeep — the undo stack depends on it. A shallow copy would share
// ramps with the live document and record nothing.
func TestCloneIsDeep(t *testing.T) {
	d := NewDoc("e")
	d.Layers[0].Bursts = []Burst{{Time: 0, Count: 1}}
	cp := d.Clone()

	cp.Layers[0].AlphaRamp[0].V = 0.5
	cp.Layers[0].Bursts[0].Count = 99
	cp.Layers[0].Name = "changed"

	if d.Layers[0].AlphaRamp[0].V == 0.5 {
		t.Error("the clone shares its alpha ramp with the original")
	}
	if d.Layers[0].Bursts[0].Count == 99 {
		t.Error("the clone shares its bursts with the original")
	}
	if d.Layers[0].Name == "changed" {
		t.Error("the clone shares its layer slice with the original")
	}
}

// TestLayerNamesStayUnique — names address layers from scripts and from the
// editor's list, so the document mints unique ones.
func TestLayerNamesStayUnique(t *testing.T) {
	d := NewDoc("e") // one layer, "layer"
	d.AddLayer("layer")
	d.DuplicateLayer(0)
	seen := map[string]bool{}
	for _, l := range d.Layers {
		if seen[l.Name] {
			t.Fatalf("duplicate layer name %q in %v", l.Name, d.Layers)
		}
		seen[l.Name] = true
	}
	if len(d.Layers) != 3 {
		t.Errorf("got %d layers, want 3", len(d.Layers))
	}
}
