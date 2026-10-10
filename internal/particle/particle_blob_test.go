package particle

// particle_blob_test.go — a volume layer's particles as the volumetric pass
// reads them.
//
// Each ramp means something different on a volume layer than on a sprite one —
// size is a radius, alpha is a density, colour is emitted light — and every one
// of those mappings is a single multiplication that is easy to get subtly
// wrong: a diameter passed as a radius doubles every fireball.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestFillBlobsMapsTheRamps(t *testing.T) {
	l := NewLayer("fire")
	l.Render = RenderVolume
	l.SizeRamp = Ramp{{T: 0, V: 2}, {T: 1, V: 2}}
	l.AlphaRamp = Ramp{{T: 0, V: 0.5}, {T: 1, V: 0.5}}
	l.ColorRamp = Gradient{{T: 0, C: mgl32.Vec3{1, 0.5, 0}}, {T: 1, C: mgl32.Vec3{1, 0.5, 0}}}
	l.Tint = mgl32.Vec3{1, 1, 0.5}
	l.Volume.Density = 4
	l.Volume.Emission = 3
	l.Volume.EmissionRamp = Ramp{{T: 0, V: 0.5}, {T: 1, V: 0.5}}
	l.Volume.Erosion = 0.8
	l.Volume.ErosionRamp = Ramp{{T: 0, V: 2}, {T: 1, V: 2}}

	s := NewSystem(l, 0, 1)
	s.Live = append(s.Live, Particle{Pos: mgl32.Vec3{0, 0, 5}, Size: 3, Life: 1, Seed: 0xFFFF})

	buf := make([]Blob, 4)
	if n := s.FillBlobs(buf, mgl32.Vec3{}); n != 1 {
		t.Fatalf("wrote %d blobs, want 1", n)
	}
	b := buf[0]
	if b.Radius != 3 { // size 3, ramped x2, halved
		t.Errorf("radius %v, want 3", b.Radius)
	}
	if b.Density != 2 { // 4 * 0.5
		t.Errorf("density %v, want 2", b.Density)
	}
	if want := (mgl32.Vec3{1.5, 0.75, 0}); b.Emission != want { // colour * tint * 3 * 0.5
		t.Errorf("emission %v, want %v", b.Emission, want)
	}
	if b.Erosion != 1 { // 0.8 * 2, clamped
		t.Errorf("erosion %v, want 1", b.Erosion)
	}
	if b.Seed != 1 {
		t.Errorf("seed %v, want 1", b.Seed)
	}
}

// Far first, so groups composite in order; and a particle whose size or density
// has ramped to nothing is gone rather than a zero-sized ball costing a march.
func TestFillBlobsSortsAndSkips(t *testing.T) {
	l := NewLayer("smoke")
	l.SizeRamp, l.AlphaRamp = nil, nil
	s := NewSystem(l, 0, 1)
	s.Live = append(s.Live,
		Particle{Pos: mgl32.Vec3{0, 0, 2}, Size: 1, Life: 1},
		Particle{Pos: mgl32.Vec3{0, 0, 9}, Size: 1, Life: 1},
		Particle{Pos: mgl32.Vec3{0, 0, 5}, Size: 0, Life: 1},
		Particle{Pos: mgl32.Vec3{0, 0, 6}, Size: 1, Life: 1},
	)
	buf := make([]Blob, 8)
	n := s.FillBlobs(buf, mgl32.Vec3{})
	if n != 3 {
		t.Fatalf("wrote %d blobs, want 3 (the zero-sized one skipped)", n)
	}
	for i, z := range []float32{9, 6, 2} {
		if buf[i].Pos.Z() != z {
			t.Errorf("blob %d at z=%v, want %v — not sorted far to near", i, buf[i].Pos.Z(), z)
		}
	}

	if n := s.FillBlobs(buf[:1], mgl32.Vec3{}); n != 1 {
		t.Errorf("wrote %d into a buffer of 1", n)
	}
}

func TestVolumeLookDefaultWritesNoBlock(t *testing.T) {
	if volumeLookToRaw(DefaultVolumeLook()) != nil {
		t.Error("a default look wrote a volume block")
	}
	v := DefaultVolumeLook()
	v.EmissionRamp = Ramp{{T: 0, V: 1}}
	if volumeLookToRaw(v) == nil {
		t.Error("a look with an emission ramp wrote no block")
	}
}
