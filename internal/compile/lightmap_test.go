package compile

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// lightmapTexelAt is the texel the floor's lightmap holds at world point p:
// rgb and the sun's visibility, decoded from its half floats.
func lightmapTexelAt(t *testing.T, tr *BSPTree, p mgl32.Vec3) (vis float32, ok bool) {
	t.Helper()
	for fi := range tr.Faces {
		f := &tr.Faces[fi]
		if f.Lightmap.Page < 0 || f.Plane.N.Y() < 0.9 || !inside2(polyOn(f), p.X(), p.Z()) {
			continue
		}
		u, v := f.Lightmap.UV(p)
		x, y := int(u*lightmapPage), int(v*lightmapPage)
		page := tr.Lightmaps[f.Lightmap.Page].Texels
		return halfToFloat(page[(y*lightmapPage+x)*4+3]), true
	}
	return 0, false
}

// polyOn is a floor face's polygon in (x, z).
func polyOn(f *Face) [][2]float32 {
	out := make([][2]float32, len(f.Points))
	for i, p := range f.Points {
		out[i] = [2]float32{p.X(), p.Z()}
	}
	return out
}

func halfToFloat(h uint16) float32 {
	sign := float32(1)
	if h&0x8000 != 0 {
		sign = -1
	}
	exp := int((h >> 10) & 0x1f)
	mant := float64(h & 0x3ff)
	switch exp {
	case 0:
		return sign * float32(mant*math.Pow(2, -24))
	case 31:
		return sign * float32(math.Inf(1))
	}
	return sign * float32((1+mant/1024)*math.Pow(2, float64(exp-15)))
}

func TestHalfBitsRoundTrips(t *testing.T) {
	for _, f := range []float32{0, 1, 0.5, 0.25, 0.1, 2.75, 100, -3.5, 1e-5, 65504} {
		got := halfToFloat(halfBits(f))
		if d := math.Abs(float64(got - f)); d > math.Abs(float64(f))*1e-3+1e-6 {
			t.Errorf("halfBits(%v) reads back %v", f, got)
		}
	}
}

// A box on a lit floor bakes its shadow into the floor's lightmap, sharp at
// the texel's size, while the vertices keep only the coarse grid.
func TestALightmapHoldsAShadowSharperThanTheGrid(t *testing.T) {
	floor := boxFromTo("floor", mgl32.Vec3{-8, -1, -8}, mgl32.Vec3{8, 0, 8})
	crate := boxFromTo("crate", mgl32.Vec3{-1, 0, -1}, mgl32.Vec3{1, 2, 1})
	lvl := levelOf(floor, crate)
	lvl.Lights = sunFrom(mgl32.Vec3{0, -1, 0}) // straight down: the shadow is the crate's footprint
	tr, _, _ := compiled(t, lvl)
	opt := DefaultLightOptions()
	opt.Lightmap, opt.LightmapTexel = true, 0.25
	BakeLight(tr, lvl, opt)

	if len(tr.Lightmaps) == 0 {
		t.Fatal("a lightmapped bake made no pages")
	}
	mapped := 0
	for fi := range tr.Faces {
		f := &tr.Faces[fi]
		if f.Lightmap.Page < 0 {
			continue
		}
		mapped++
		for _, p := range f.Points {
			u, v := f.Lightmap.UV(p)
			if u < 0 || u > 1 || v < 0 || v > 1 {
				t.Fatalf("face %d's corner %v maps off its page: (%v, %v)", fi, p, u, v)
			}
		}
	}
	if mapped == 0 {
		t.Fatal("no face was given a chart")
	}

	// Next to the crate (0.5 m out) in full sun, and under where it would be
	// lit were it not for the crate — the floor under the crate itself is
	// cut away by CSG, so the shadow cannot be read there; a sun leaning a
	// little shows one instead.
	if vis, ok := lightmapTexelAt(t, tr, mgl32.Vec3{1.6, 0, 0}); !ok || vis < 0.95 {
		t.Errorf("0.6 m out from the crate in full sun: visibility %v (found %v)", vis, ok)
	}

	lvl.Lights = sunFrom(mgl32.Vec3{1, -1, 0}) // from the west-up: the shadow falls east
	tr, _, _ = compiled(t, lvl)
	BakeLight(tr, lvl, opt)
	if vis, ok := lightmapTexelAt(t, tr, mgl32.Vec3{1.5, 0, 0}); !ok || vis > 0.05 {
		t.Errorf("half a metre into the crate's shadow: visibility %v (found %v)", vis, ok)
	}
	if vis, ok := lightmapTexelAt(t, tr, mgl32.Vec3{3.6, 0, 0}); !ok || vis < 0.95 {
		t.Errorf("past the shadow's far edge: visibility %v (found %v)", vis, ok)
	}
}
