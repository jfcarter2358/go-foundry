package particle

import (
	"path/filepath"
	"testing"

	"foundry/internal/testenv"

	"github.com/go-gl/mathgl/mgl32"
)

// The phase W5 splashes load and burst, and the mist drifts downwind.
func TestShippedWaterSplashesBurst(t *testing.T) {
	root := testenv.RepoRoot(t)
	for _, id := range []string{"water_step", "water_splash", "water_impact"} {
		d, err := LoadParticle(filepath.Join(root, "resources", "particles", id+".fpar"))
		if err != nil {
			t.Fatal(err)
		}
		total := 0
		for i := range d.Layers {
			s := NewSystem(d.Layers[i], d.Gravity, d.Seed+uint32(i))
			for f := 0; f < 10; f++ {
				s.Step(1.0/60.0, mgl32.Ident4())
			}
			total += s.Count()
		}
		if total == 0 {
			t.Errorf("%s: nothing burst", id)
		}
	}
}

func TestWindCarriesALayerWithWind(t *testing.T) {
	t.Cleanup(func() { SetWind(mgl32.Vec3{}) })
	SetWind(mgl32.Vec3{2, 0, 0})
	mean := func(wind float32) float32 {
		l := Layer{Name: "w", Rate: 30, Duration: 1, Loop: true, LifeMin: 4, LifeMax: 4, SizeMin: 0.1, SizeMax: 0.1, Wind: wind}
		s := NewSystem(l, 0, 3)
		for f := 0; f < 180; f++ {
			s.Step(1.0/60.0, mgl32.Ident4())
		}
		x := float32(0)
		for _, p := range s.Live {
			x += p.Pos.X()
		}
		return x / float32(max(len(s.Live), 1))
	}
	if still, carried := mean(0), mean(1); carried < still+1 {
		t.Errorf("mean x %.2f with wind 1 vs %.2f without: want carried downwind", carried, still)
	}
}
