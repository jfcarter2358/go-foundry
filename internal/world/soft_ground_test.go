package world

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/material"

	"github.com/go-gl/mathgl/mgl32"
)

// softTestLevel is a level of one 100 m tile over [0, 100]² wearing a splat
// whose weights put sand (layer 1) on the west half, rock (layer 4) on the
// east, and snow (layer 2) in the north-west corner.
func softTestLevel(t *testing.T) *Level {
	t.Helper()
	dir := t.TempDir()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for z := 0; z < 4; z++ {
		for x := 0; x < 4; x++ {
			c := color.NRGBA{A: 255} // rock
			if x < 2 {
				c = color.NRGBA{R: 255} // sand
				if z >= 3 {
					c = color.NRGBA{G: 255} // snow
				}
			}
			img.SetNRGBA(x, z, c)
		}
	}
	f, err := os.Create(filepath.Join(dir, "w.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()

	d := &material.Doc{Name: "soft_test_ground", Domain: material.DomainSurface}
	e := d.AddEffect(material.KindSplat)
	e.Set("weights", material.TextureValue(filepath.Join(dir, "w")))
	e.Set("bounds_min", material.Vec2Value(mgl32.Vec2{0, 0}))
	e.Set("bounds_max", material.Vec2Value(mgl32.Vec2{100, 100}))
	e.Set(material.SplatLayerParam(0, ""), material.TextureValue("sand_tex"))
	e.Set(material.SplatLayerParam(0, "soft"), material.EnumValue(material.SoftSand))
	e.Set(material.SplatLayerParam(1, ""), material.TextureValue("snow_tex"))
	e.Set(material.SplatLayerParam(1, "soft"), material.EnumValue(material.SoftSnow))
	e.Set(material.SplatLayerParam(1, "soft_depth"), material.FloatValue(0.2))
	e.Set(material.SplatLayerParam(3, ""), material.TextureValue("rock_tex"))
	material.Put(d)
	t.Cleanup(func() { material.Remove(d.Name) })

	lvl := &Level{}
	lvl.Scenery = append(lvl.Scenery, Scenery{ID: "tile", Material: d.Name,
		LocalMin: mgl32.Vec3{0, 0, 0}, LocalMax: mgl32.Vec3{100, 10, 100}})
	return lvl
}

func TestSoftGroundAtReadsTheSplatWeights(t *testing.T) {
	lvl := softTestLevel(t)

	sand := SoftGroundAt(lvl, 12, 5, 12)
	if !sand.Soft() || sand.Kind != material.SoftSand || sand.Share < 0.99 {
		t.Fatalf("on the sand: %+v, want all sand", sand)
	}
	if k := SoftGroundKinds[material.SoftSand]; sand.Depth != k.Depth || sand.Drag != k.Drag {
		t.Errorf("sand's depth and drag %v, %v; want the kind's own %v, %v", sand.Depth, sand.Drag, k.Depth, k.Drag)
	}

	if rock := SoftGroundAt(lvl, 88, 5, 12); rock.Soft() {
		t.Errorf("on the rock: %+v, want firm ground", rock)
	}

	snow := SoftGroundAt(lvl, 12, 5, 95)
	if snow.Kind != material.SoftSnow {
		t.Fatalf("in the snow corner: %+v, want snow", snow)
	}
	if snow.Depth < 0.19 {
		t.Errorf("the snow layer's soft_depth 0.2 is ignored: depth %v", snow.Depth)
	}

	// Halfway between sand and rock: half soft, and the profile scaled.
	mid := SoftGroundAt(lvl, 50, 5, 12)
	if mid.Share < 0.4 || mid.Share > 0.6 {
		t.Errorf("at the sand/rock seam the share is %v, want about a half", mid.Share)
	}

	// Off the tile, and far below it (a cave under the ground): nothing.
	if g := SoftGroundAt(lvl, 150, 5, 12); g.Soft() {
		t.Errorf("off the tile: %+v", g)
	}
	if g := SoftGroundAt(lvl, 12, -30, 12); g.Soft() {
		t.Errorf("30 m under the tile: %+v", g)
	}
}

func TestTrailStampsQueueAndDrain(t *testing.T) {
	DrainTrailStamps(nil)
	for i := 0; i < maxQueuedStamps+10; i++ {
		PushTrailStamp(TrailStamp{Radius: float32(i)})
	}
	got := DrainTrailStamps(nil)
	if len(got) != maxQueuedStamps {
		t.Fatalf("queued %d, want the cap %d", len(got), maxQueuedStamps)
	}
	if got[len(got)-1].Radius != float32(maxQueuedStamps+9) {
		t.Errorf("the newest stamp was dropped rather than the oldest")
	}
	if again := DrainTrailStamps(nil); len(again) != 0 {
		t.Errorf("drain left %d behind", len(again))
	}
}
