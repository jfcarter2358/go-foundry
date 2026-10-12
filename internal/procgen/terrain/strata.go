package terrain

// strata.go — the layer stack, and the terracing that turns a smooth slope
// into ledges.
//
// Canyon country is stepped because its rock is: hard sandstones stand as
// cliffs, the soft shales between them weather back to slopes, and the top
// of every hard layer is a bench where the soft rock above it has been
// stripped. So the strata are a list of (thickness, hardness) layers stacked
// upward from Base and repeated as far as the heights need, and three phases
// read them:
//
//   - terrace remaps the surface layer by layer: inside a layer of hardness
//     k the height's position u (0 at the layer's bottom, 1 at its top) goes
//     through g(u) = lerp(u, stepped(u), Terrace·k): stepped climbs the
//     layer's band over the outer 1/(1.5+7k) of it (the cliff face), turns
//     at a sharp lip and holds (the bench); a soft layer stays the slope it
//     was. g is monotonic
//     and fixes both ends, so the surface stays continuous and no layer
//     swaps order;
//   - erosion (drainage and droplets) scales its rate by the hardness of the
//     layer at the eroding sample's height, so hard layers survive the cuts
//     that strip the soft ones;
//   - thermal takes its angle of repose from the hardness.
//
// The layers are not flat: a strata OFFSET per sample (warp noise plus a
// dip) is added to the height before the lookup, so the ledges wander and
// tilt the way real bedding does. A sample's "strata coordinate" is h + off.

import (
	"math"

	"foundry/internal/procgen/noise"
)

// strataTableStep is the hardness table's resolution, metres.
const strataTableStep = 0.05

// compiledStrata is Strata ready for lookups.
type compiledStrata struct {
	layers  []Layer
	base    float64
	tops    []float64 // cumulative top of each layer above base, one period
	period  float64
	terrace float64
	resist  float64
	// hard is the hardness over one period every strataTableStep, softened
	// across each boundary so erosion does not chatter on it.
	hard []float32
}

func (st Strata) compile() *compiledStrata {
	c := &compiledStrata{base: st.Base, terrace: st.Terrace, resist: st.Resistance}
	for _, l := range st.Layers {
		if l.Thickness > 0 {
			c.layers = append(c.layers, l)
		}
	}
	if len(c.layers) == 0 {
		c.layers = []Layer{{Thickness: 1e6, Hardness: 0.5, Color: [3]float64{0.7, 0.5, 0.35}}}
	}
	for _, l := range c.layers {
		c.period += l.Thickness
		c.tops = append(c.tops, c.period)
	}
	n := int(math.Ceil(c.period/strataTableStep)) + 1
	if n > 1<<22 {
		n = 1 << 22
	}
	c.hard = make([]float32, n)
	raw := make([]float32, n)
	for k := range raw {
		_, _, hd := c.layerAtLocal(float64(k) * c.period / float64(n-1))
		raw[k] = float32(hd)
	}
	// Soften over ±0.5 m (a box blur, wrapping: the stack repeats).
	r := max(int(0.5/strataTableStep), 1)
	for k := range c.hard {
		sum := float32(0)
		for d := -r; d <= r; d++ {
			sum += raw[((k+d)%n+n)%n]
		}
		c.hard[k] = sum / float32(2*r+1)
	}
	return c
}

// local is a strata coordinate's position within one period, [0, period).
func (c *compiledStrata) local(y float64) float64 {
	t := math.Mod(y-c.base, c.period)
	if t < 0 {
		t += c.period
	}
	return t
}

// layerAtLocal is the layer holding period position t, its bottom and the
// hardness: (index, bottom, hardness).
func (c *compiledStrata) layerAtLocal(t float64) (int, float64, float64) {
	bottom := 0.0
	for i, top := range c.tops {
		if t < top || i == len(c.tops)-1 {
			return i, bottom, c.layers[i].Hardness
		}
		bottom = top
	}
	return 0, 0, c.layers[0].Hardness
}

// layer is the layer at strata coordinate y and the position within it, 0..1.
func (c *compiledStrata) layer(y float64) (idx int, u float64) {
	t := c.local(y)
	i, bottom, _ := c.layerAtLocal(t)
	return i, min(max((t-bottom)/c.layers[i].Thickness, 0), 1)
}

// hardness is the softened hardness at strata coordinate y.
func (c *compiledStrata) hardness(y float64) float32 {
	t := c.local(y)
	k := int(t / c.period * float64(len(c.hard)-1))
	return c.hard[min(max(k, 0), len(c.hard)-1)]
}

// erodibility is the erosion rate's factor at strata coordinate y.
func (c *compiledStrata) erodibility(y float64) float32 {
	return 1 - float32(c.resist)*c.hardness(y)
}

// terraceHeight remaps strata coordinate y through its layer (file comment).
func (c *compiledStrata) terraceHeight(y float64) float64 {
	if c.terrace <= 0 {
		return y
	}
	t := c.local(y)
	i, bottom, k := c.layerAtLocal(t)
	th := c.layers[i].Thickness
	u := min(max((t-bottom)/th, 0), 1)
	// The hard layer's face takes the outer share cf of the rise (the cliff,
	// nearly straight), then a sharp lip and the bench: flat but for a
	// twelfth of the slope it had. 1-(1-u)^p would round the lip over, which
	// reads as melted wax rather than rock.
	cf := 1 / (1.5 + 7*k)
	cliff := math.Pow(min(u/cf, 1), 0.85)
	stepped := 0.92*cliff + 0.08*u
	g := u + (stepped-u)*c.terrace*k
	return y + (g-u)*th
}

// strataOffsets fills off with each sample's strata offset: warp noise plus
// the dip.
func strataOffsets(s *Settings, g *Grid, off []float32, pool Pool) error {
	st := s.Strata
	f := noise.Fractal{Seed: noise.Hash64(s.Seed, 0x5354_5241), Octaves: 4, Frequency: 1.0 / 260, Gain: 0.5}
	return rows(pool, g.S(), func(j int) {
		z := g.Z(j)
		for i := 0; i < g.S(); i++ {
			x := g.X(i)
			v := st.Dip[0]*x/100 + st.Dip[1]*z/100
			if st.Warp > 0 {
				v += st.Warp * f.At2(x, z)
			}
			off[j*g.S()+i] = float32(v)
		}
	})
}

// terrace steps the surface through the strata, in proportion to how steep
// the ground is: erosion makes cliffs where it cuts down through the layers,
// so an escarpment steps and a plateau top or a gentle lowland stays smooth.
// The slope is measured over a few metres on the unterraced surface; the
// stepping fades in between TerraceSlopeLo and TerraceSlopeHi degrees.
func terrace(s *Settings, g *Grid, c *compiledStrata, off []float32, pool Pool) error {
	if c.terrace <= 0 {
		return nil
	}
	src := append([]float32(nil), g.H...)
	sz := g.S()
	r := max(int(math.Round(3/g.Cell)), 1)
	span := float32(2 * float64(r) * g.Cell)
	lo := math.Tan(s.Strata.SlopeLo * math.Pi / 180)
	hi := math.Tan(max(s.Strata.SlopeHi, s.Strata.SlopeLo+1) * math.Pi / 180)
	return rows(pool, sz, func(j int) {
		for i := 0; i < sz; i++ {
			k := j*sz + i
			il, ir := max(i-r, 0), min(i+r, sz-1)
			jl, jr := max(j-r, 0), min(j+r, sz-1)
			gx := (src[j*sz+ir] - src[j*sz+il]) / span
			gz := (src[jr*sz+i] - src[jl*sz+i]) / span
			w := noise.SmoothStep(lo, hi, math.Hypot(float64(gx), float64(gz)))
			if w <= 0 {
				continue
			}
			y := float64(src[k]) + float64(off[k])
			g.H[k] = src[k] + float32((c.terraceHeight(y)-y)*w)
		}
	})
}
