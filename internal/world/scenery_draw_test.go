package world

import (
	"testing"

	"foundry/internal/nanite"

	"github.com/go-gl/mathgl/mgl32"
)

func TestSceneryWindowsWithoutDistancesAreOpen(t *testing.T) {
	s := NewScenery("rock", "models/rock.fnm", mgl32.Vec3{1, 2, 3})
	if !s.MeshWindow().Open() || !s.MeshShadowWindow().Open() || !s.ImpostorWindow().None {
		t.Errorf("a placement with no distances: mesh %+v shadow %+v impostor %+v; want open, open, none",
			s.MeshWindow(), s.MeshShadowWindow(), s.ImpostorWindow())
	}
	if !s.DrawnAt(mgl32.Vec3{1e6, 0, 0}, false) || !s.DrawnAt(mgl32.Vec3{1e6, 0, 0}, true) {
		t.Error("a placement with no distances is not drawn a thousand kilometres away")
	}
}

func TestSceneryWindowsWithAnImpostor(t *testing.T) {
	s := NewScenery("fir", "models/fir.fnm", mgl32.Vec3{})
	s.DrawDistance, s.ShadowDistance, s.Impostor, s.ImpostorDistance = 1200, 700, "models/fir_imp.fnm", 300
	// Not loaded (or not drawn by the GPU cull): the mesh goes all the way.
	if w := s.MeshWindow(); w != (DrawWindow{To: 1200}) || !s.ImpostorWindow().None {
		t.Errorf("no impostor loaded: mesh %+v impostor %+v", w, s.ImpostorWindow())
	}
	s.ImpostorMesh, s.NaniteGPU = &nanite.Mesh{}, true
	if w := s.MeshWindow(); w != (DrawWindow{To: 300}) {
		t.Errorf("mesh window %+v, want [0, 300)", w)
	}
	if w := s.ImpostorWindow(); w != (DrawWindow{From: 300, To: 1200}) {
		t.Errorf("impostor window %+v, want [300, 1200)", w)
	}
	if w := s.ImpostorShadowWindow(); w != (DrawWindow{From: 300, To: 700}) {
		t.Errorf("impostor shadow window %+v, want [300, 700)", w)
	}
	if w := s.MeshShadowWindow(); w != (DrawWindow{To: 300}) {
		t.Errorf("mesh shadow window %+v, want [0, 300)", w)
	}
	// Shadows shorter than the hand-over: the impostor casts nothing.
	s.ShadowDistance = 200
	if !s.ImpostorShadowWindow().None || s.MeshShadowWindow() != (DrawWindow{To: 200}) {
		t.Errorf("shadow 200: mesh %+v impostor %+v", s.MeshShadowWindow(), s.ImpostorShadowWindow())
	}
	// A hand-over past the draw distance: no far tier.
	s.ImpostorDistance = 2000
	if s.HasImpostor() || s.MeshWindow() != (DrawWindow{To: 1200}) {
		t.Errorf("hand-over beyond the draw distance: impostor %v, mesh %+v", s.HasImpostor(), s.MeshWindow())
	}
}

func TestSceneryCutsHandOverAtOneDistance(t *testing.T) {
	// The per-instance cut: the mesh stops exactly where its impostor starts,
	// for every hash, at every distance through the band.
	s := NewScenery("fir", "models/fir.fnm", mgl32.Vec3{})
	s.DrawDistance, s.Impostor, s.ImpostorDistance = 1000, "x", 200
	s.ImpostorMesh, s.NaniteGPU = &nanite.Mesh{}, true
	mesh, imp := s.MeshWindow(), s.ImpostorWindow()
	for h := float32(0); h < 1; h += 0.0625 {
		for d := float32(150); d < 220; d += 0.25 {
			if mesh.Drawn(d, h) == imp.Drawn(d, h) {
				t.Fatalf("hash %.3f, %g m: mesh drawn %v, impostor drawn %v — want exactly one", h, d, mesh.Drawn(d, h), imp.Drawn(d, h))
			}
		}
	}
	// The per-pixel fades are complementary: the mesh's hi is the
	// impostor's lo at every distance.
	for d := float32(150); d < 220; d += 0.5 {
		_, hi := mesh.DrawFade(d)
		lo, _ := imp.DrawFade(d)
		if hi != lo {
			t.Fatalf("%g m: the mesh keeps noise below %g, the impostor from %g", d, hi, lo)
		}
	}
	// Each instance drops at its own distance in the band.
	cut := func(h float32) float32 {
		for d := float32(800); d <= 1000; d += 0.5 {
			if !imp.Drawn(d, h) {
				return d
			}
		}
		return 1001
	}
	if a, b := cut(0.1), cut(0.9); !(a >= 900 && b <= 1000 && a < b) {
		t.Errorf("cuts at hash 0.1: %g m, 0.9: %g m — want both in [900, 1000), apart", a, b)
	}
}

func TestSceneryDrawHashIsStableAndSpread(t *testing.T) {
	a, b := DrawHashAt(mgl32.Vec3{10, 2, -3}), DrawHashAt(mgl32.Vec3{10, 2, -3})
	if a != b || a < 0 || a >= 1 {
		t.Errorf("hash %g, %g", a, b)
	}
	// Spread: 1000 placements on a line cover the unit interval.
	var lo, hi float32 = 1, 0
	var sum float64
	for i := 0; i < 1000; i++ {
		h := DrawHashAt(mgl32.Vec3{float32(i) * 1.7, 0, float32(i) * 0.3})
		lo, hi = min(lo, h), max(hi, h)
		sum += float64(h)
	}
	if lo > 0.02 || hi < 0.98 || sum/1000 < 0.45 || sum/1000 > 0.55 {
		t.Errorf("hashes span [%g, %g], mean %g: not spread", lo, hi, sum/1000)
	}
}

func TestSceneryDrawFieldsRoundTrip(t *testing.T) {
	r := RawScenery{ID: "fir", Model: "models/fir.fnm", DrawDistance: 1200, ShadowDistance: 700, Impostor: "models/fir_imp.fnm", ImpostorDistance: 300}
	s := SceneryFromRaw(r)
	if s.DrawDistance != 1200 || s.ShadowDistance != 700 || s.Impostor != "models/fir_imp.fnm" || s.ImpostorDistance != 300 {
		t.Errorf("from raw: %+v", s)
	}
	back := SceneryToRaw(&s)
	if back.DrawDistance != 1200 || back.ShadowDistance != 700 || back.Impostor != r.Impostor || back.ImpostorDistance != 300 {
		t.Errorf("to raw: %+v", back)
	}
	// None of it written when unset: an existing level saves as it was.
	plain := SceneryToRaw(&Scenery{ID: "rock", Model: "models/rock.fnm"})
	if plain.DrawDistance != 0 || plain.ShadowDistance != 0 || plain.Impostor != "" || plain.ImpostorDistance != 0 {
		t.Errorf("an unset placement wrote %+v", plain)
	}
}
