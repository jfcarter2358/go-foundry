package world

// brush_texture_override_test.go — a face drawn with a texture other than its
// unwrapped one, and the UV map maths the UV gizmo drags it with.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// An override draws its own texture projected under its own transform, and
// leaves the painted texture and its unwrap where they were.
func TestTextureOverrideReplacesThePaintingWithoutForgettingIt(t *testing.T) {
	b := NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "atlas")
	f := &b.Faces[uvBoxTop]
	stated := []mgl32.Vec2{{0.1, 0.2}, {0.3, 0.2}, {0.3, 0.4}, {0.1, 0.4}}
	f.UVs = append([]mgl32.Vec2(nil), stated...)
	f.TextureOverride = true
	f.OverrideTexture = "tiles"
	f.OverrideUVOffset = mgl32.Vec2{0.5, 0.25}
	f.OverrideUVScale = mgl32.Vec2{2, 2}

	if got := f.DrawTexture(); got != "tiles" {
		t.Errorf("drawn texture = %q, want the override", got)
	}
	if f.DrawsCornerUVs() {
		t.Error("an overridden face still draws its unwrap's UVs")
	}

	// The same face with no unwrap, wearing the override's transform as its
	// own, is what the overridden face must draw.
	plain := NewBoxBrush("p", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "tiles")
	pf := &plain.Faces[uvBoxTop]
	pf.UVOffset, pf.UVScale = f.OverrideUVOffset, f.OverrideUVScale
	got, ok := b.DrawnUVMap(f)
	want, ok2 := plain.DrawnUVMap(pf)
	if !ok || !ok2 {
		t.Fatal("DrawnUVMap refused a box face")
	}
	for _, vi := range f.Indices {
		if p := b.Verts[vi]; !uvNear(got.At(p), want.At(p)) {
			t.Errorf("corner %v draws at %v, want the projection's %v", p, got.At(p), want.At(p))
		}
	}

	f.TextureOverride = false
	if f.DrawTexture() != "atlas" || !f.DrawsCornerUVs() {
		t.Error("switching the override off did not bring the painting back")
	}
}

// The override saves whole, including while it is off, so toggling it back on
// after a reload returns the placement it had.
func TestTextureOverrideSurvivesSaveLoad(t *testing.T) {
	b := NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "atlas")
	f := &b.Faces[0]
	f.OverrideTexture = "tiles"
	f.OverrideUVOffset = mgl32.Vec2{0.5, 0.25}
	f.OverrideUVScale = mgl32.Vec2{2, 3}
	f.OverrideUVRotation = 0.7

	for _, on := range []bool{true, false} {
		f.TextureOverride = on
		back, err := BrushFromRaw(BrushToRaw(&b))
		if err != nil {
			t.Fatalf("BrushFromRaw: %v", err)
		}
		g := &back.Faces[0]
		if g.TextureOverride != on || g.OverrideTexture != f.OverrideTexture ||
			g.OverrideUVOffset != f.OverrideUVOffset || g.OverrideUVScale != f.OverrideUVScale ||
			g.OverrideUVRotation != f.OverrideUVRotation {
			t.Errorf("override (on=%v) loaded as %+v", on, g)
		}
	}
	if raw := BrushToRaw(&b); raw.Faces[1].OverrideTexture != "" || raw.Faces[1].TextureOverride {
		t.Error("a face with no override saved one")
	}
}

// Sliding a texture by d must put the texel that was at p at p+d — under a
// rotation and a non-uniform scale, where the projection's axes and the
// texture's part ways.
func TestUVMapShiftCarriesTheTexelWithTheDisplacement(t *testing.T) {
	b := NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "")
	f := &b.Faces[uvBoxPosZ]
	f.UVScale = mgl32.Vec2{2, 0.5}
	f.UVRotation = 0.6
	f.UVOffset = mgl32.Vec2{0.1, -0.3}

	before, ok := b.DrawnUVMap(f)
	if !ok {
		t.Fatal("DrawnUVMap refused a box face")
	}
	p := mgl32.Vec3{0.2, -0.4, 1}
	d := mgl32.Vec3{0.3, 0.5, 0}
	f.UVOffset = f.UVOffset.Add(before.Shift(d))
	after, _ := b.DrawnUVMap(f)

	if !uvNear(after.At(p.Add(d)), before.At(p)) {
		t.Errorf("the texel at %v went to %v; %v now samples %v",
			before.At(p), after.At(p.Add(d)), p.Add(d), after.At(p.Add(d)))
	}
}

// Steps are the texture's own axes on the face: along du, u climbs by one and v
// holds, and the other way round for dv.
func TestUVMapStepsFollowTheTexturesAxes(t *testing.T) {
	b := NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "")
	f := &b.Faces[uvBoxTop]
	f.UVScale = mgl32.Vec2{3, 1}
	f.UVRotation = 1.1

	m, ok := b.DrawnUVMap(f)
	if !ok {
		t.Fatal("DrawnUVMap refused a box face")
	}
	du, dv, ok := m.Steps(mgl32.Vec3{0, 1, 0})
	if !ok {
		t.Fatal("Steps refused a textured face")
	}
	p := mgl32.Vec3{0.1, 1, 0.2}
	if got, want := m.At(p.Add(du)).Sub(m.At(p)), (mgl32.Vec2{1, 0}); !uvNear(got, want) {
		t.Errorf("a step along du changes the UV by %v, want %v", got, want)
	}
	if got, want := m.At(p.Add(dv)).Sub(m.At(p)), (mgl32.Vec2{0, 1}); !uvNear(got, want) {
		t.Errorf("a step along dv changes the UV by %v, want %v", got, want)
	}
	if du.Y() != 0 || dv.Y() != 0 {
		t.Errorf("steps %v, %v leave the face's plane", du, dv)
	}
}

// Pinning a point's UV moves only the drawn offset, and lands the texel.
func TestPinDrawnUVMovesOnlyTheOffset(t *testing.T) {
	b := NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "")
	f := &b.Faces[uvBoxTop]
	f.TextureOverride = true
	f.OverrideUVScale = mgl32.Vec2{2, 0.5}
	f.OverrideUVRotation = 0.3
	pivot := mgl32.Vec3{0.3, 1, -0.2}
	want := mgl32.Vec2{4, -1}

	if !b.PinDrawnUV(f, pivot, want) {
		t.Fatal("PinDrawnUV refused a box face")
	}
	m, _ := b.DrawnUVMap(f)
	if got := m.At(pivot); !uvNear(got, want) {
		t.Errorf("the pivot samples %v, want %v", got, want)
	}
	if f.OverrideUVScale != (mgl32.Vec2{2, 0.5}) || f.OverrideUVRotation != 0.3 || f.UVOffset != (mgl32.Vec2{}) {
		t.Error("pinning changed more than the override's offset")
	}
}

// A mirrored unwrap reads as the other hand, so a turn of its UVs turns the
// texture the other way.
func TestUVMapHandednessSeesAMirror(t *testing.T) {
	b := NewBoxBrush("b", mgl32.Vec3{}, mgl32.Vec3{1, 1, 1}, "")
	f := &b.Faces[uvBoxTop]
	n := mgl32.Vec3{0, 1, 0}
	plain, _ := b.DrawnUVMap(f)
	f.UVScale = mgl32.Vec2{-1, 1}
	mirrored, _ := b.DrawnUVMap(f)
	if plain.Handedness(n)*mirrored.Handedness(n) >= 0 {
		t.Errorf("handedness %v and mirrored %v agree", plain.Handedness(n), mirrored.Handedness(n))
	}
	base, _ := b.BaseUVMap(f)
	if base.Handedness(n) != plain.Handedness(n) {
		t.Error("the base map kept the face's scale")
	}
}
