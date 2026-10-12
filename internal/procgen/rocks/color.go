package rocks

// color.go — the vertex colour bake.
//
// A rock's look at a distance is its colour, and most of a weathered rock's
// colour is not its mineral but its HISTORY: where water ran (varnish
// streaks), where dust settles (the tops), where nothing reaches (the
// crevices, darker for the light they lose and the dirt they keep), where it
// was knocked (fresh, paler rock on the edges). So the bake reads, at each
// vertex, the field around it (sdf's AO and Laplacian) and the rock's own
// structure (its beds and blocks), and writes:
//
//   - RGB, the albedo multiplier: the lithology's palette; for sandstone, a
//     colour per bed (soft beds darker and redder, every bed its own mix of
//     the base and band colours) with the laminations as faint lines; for
//     granite, a salt-and-pepper mottle of feldspar, quartz and dark
//     minerals; a tint per joint block; a little grain; then the cavity
//     darkening (concave), the edge lightening (convex), desert varnish
//     (dark streaks down steep faces, heavier under overhangs) and dust
//     (on the up-facing faces);
//   - A, the ambient occlusion with the cavity folded in, which
//     fragment.glsl takes off the ambient light and a share of the direct.
//
// The palettes are named for the formations they are drawn from; "auto"
// picks Entrada for sandstone and grey for granite, "custom" uses the
// settings' two colours.

import (
	"context"
	"math"

	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/sdf"
)

// palette is a lithology's colours (display values, 0..1).
type palette struct {
	base, band, soft, varnish, dust, speck sdf.Vec3
}

// Palettes is the palette names, for the schema.
var Palettes = []string{"auto", "entrada", "navajo", "wingate", "kayenta", "granite_grey", "granite_pink", "custom"}

var palettes = map[string]palette{
	// Entrada: Arches' red-orange slickrock, buff bands.
	"entrada": {base: sdf.Vec3{0.70, 0.37, 0.23}, band: sdf.Vec3{0.84, 0.62, 0.45}, soft: sdf.Vec3{0.60, 0.30, 0.21},
		varnish: sdf.Vec3{0.20, 0.12, 0.09}, dust: sdf.Vec3{0.78, 0.56, 0.41}},
	// Navajo: Zion's and the Wave's cream to salmon cross-bedded dunes.
	"navajo": {base: sdf.Vec3{0.85, 0.64, 0.46}, band: sdf.Vec3{0.92, 0.83, 0.70}, soft: sdf.Vec3{0.78, 0.52, 0.37},
		varnish: sdf.Vec3{0.24, 0.16, 0.11}, dust: sdf.Vec3{0.82, 0.66, 0.50}},
	// Wingate: Canyonlands' deep red-brown walls, heavily varnished.
	"wingate": {base: sdf.Vec3{0.58, 0.31, 0.20}, band: sdf.Vec3{0.70, 0.43, 0.29}, soft: sdf.Vec3{0.50, 0.27, 0.19},
		varnish: sdf.Vec3{0.14, 0.09, 0.07}, dust: sdf.Vec3{0.72, 0.51, 0.38}},
	// Kayenta: the purplish-red siltstone ledges between them.
	"kayenta": {base: sdf.Vec3{0.56, 0.34, 0.31}, band: sdf.Vec3{0.67, 0.47, 0.41}, soft: sdf.Vec3{0.48, 0.30, 0.29},
		varnish: sdf.Vec3{0.19, 0.13, 0.11}, dust: sdf.Vec3{0.70, 0.53, 0.43}},
	// Granite, grey (Sierra) and pink (Joshua Tree's monzogranite).
	"granite_grey": {base: sdf.Vec3{0.60, 0.58, 0.55}, band: sdf.Vec3{0.74, 0.71, 0.67}, soft: sdf.Vec3{0.55, 0.52, 0.49},
		varnish: sdf.Vec3{0.30, 0.25, 0.20}, dust: sdf.Vec3{0.70, 0.61, 0.50}, speck: sdf.Vec3{0.17, 0.17, 0.18}},
	"granite_pink": {base: sdf.Vec3{0.68, 0.57, 0.51}, band: sdf.Vec3{0.78, 0.68, 0.62}, soft: sdf.Vec3{0.62, 0.51, 0.46},
		varnish: sdf.Vec3{0.32, 0.24, 0.19}, dust: sdf.Vec3{0.74, 0.62, 0.51}, speck: sdf.Vec3{0.19, 0.17, 0.17}},
}

// paletteFor is the palette the settings ask for.
func paletteFor(s Settings, name string) palette {
	switch name {
	case "", "auto":
		if s.Lithology == "granite" {
			return palettes["granite_grey"]
		}
		return palettes["entrada"]
	case "custom":
		c, b := sdf.Vec3(s.Color), sdf.Vec3(s.BandColor)
		return palette{base: c, band: b, soft: c.Mul(0.88), varnish: sdf.Vec3{0.2, 0.13, 0.1},
			dust: c.Add(sdf.Vec3{0.8, 0.65, 0.5}).Mul(0.5), speck: sdf.Vec3{0.17, 0.17, 0.18}}
	}
	if p, ok := palettes[name]; ok {
		return p
	}
	return palettes["entrada"]
}

func mixV(a, b sdf.Vec3, t float64) sdf.Vec3 { return a.Add(b.Sub(a).Mul(t)) }

// unitHash is a hash as a number in [0, 1).
func unitHash(words ...uint64) float64 { return float64(noise.Hash64(words...)>>11) / (1 << 53) }

// bake is the colours of m's vertices, from the shape's field (see the file
// comment), on workers goroutines.
func bake(b *build, shape sdf.Shape, m *sdf.Mesh, ctx context.Context, workers int) ([][4]uint8, error) {
	s := b.s
	pal := paletteFor(s, s.Palette)
	size := b.size
	fine := math.Max(0.006*size, 0.0015)
	broad := 0.05 * size
	grain := sdf.FBM(noise.Hash64(b.seed, 1), 22/size, 3, 0.55)
	mottle := sdf.FBM(noise.Hash64(b.seed, 2), 2.5/size, 3, 0.5)
	streak := noise.Fractal{Seed: noise.Hash64(b.seed, 3), Octaves: 3, Frequency: 2.2 / size, Gain: 0.55}
	speckle := noise.Hash64(b.seed, 4)
	out := make([][4]uint8, len(m.Positions))
	const chunk = 256
	nChunks := (len(m.Positions) + chunk - 1) / chunk
	err := parallelFor(ctx, workers, nChunks, func(ci int) {
		for i := ci * chunk; i < min((ci+1)*chunk, len(m.Positions)); i++ {
			p, n := m.Positions[i], m.Normals[i]
			ao := shape.AO(p, n, s.AORadius, 5)
			lapF := shape.Laplacian(p, fine)
			lapB := shape.Laplacian(p, broad)
			// The broad hollows count most: a fine cavity at every step's
			// foot outlines it like ink.
			cavity := sdf.Clamp(-lapF*1.3, 0, 1)*0.4 + sdf.Clamp(-lapB*2.0, 0, 1)*0.6
			edge := sdf.Clamp(lapF*1.4, 0, 1)

			// The mineral and the beds.
			q := b.bedFrame.Apply(p)
			col := pal.base
			if s.Lithology == "granite" {
				m := mottle.F(p)*0.5 + 0.5
				col = mixV(pal.base, pal.band, sdf.SmoothStep(0.3, 0.8, m)*s.ColorVariation)
				// Dark minerals, a few percent of the grains.
				g := noise.Cellular3(speckle, q[0]*60/size, q[1]*60/size, q[2]*60/size, 1, noise.MetricEuclidean)
				if unitHash(g.ID) < 0.18 {
					col = mixV(col, pal.speck, 0.55*sdf.SmoothStep(0.5, 0.2, g.F1))
				}
			} else if st := b.strata; st != nil {
				c := st.Coord(q)
				idx, rep, t, _ := st.LayerAt(c)
				// Each bed's colour, blended into the next over the outer
				// fifth of each (a bed boundary crossing a flat top is a
				// gradation, not a painted line).
				col = bedColor(b, st, pal, idx, rep)
				n := len(st.Layers)
				if t > 0.8 {
					ni, nr := idx+1, rep
					if ni == n {
						ni, nr = 0, rep+1
					}
					col = mixV(col, bedColor(b, st, pal, ni, nr), 0.5*sdf.SmoothStep(0.8, 1, t))
				} else if t < 0.2 {
					pi, pr := idx-1, rep
					if pi < 0 {
						pi, pr = n-1, rep-1
					}
					col = mixV(col, bedColor(b, st, pal, pi, pr), 0.5*sdf.SmoothStep(0.2, 0, t))
				}
				u := unitHash(b.seed, uint64(idx), uint64(int64(rep)+1<<20))
				// Bleached and iron-stained patches across the beds.
				mo := mottle.F(q)
				col = mixV(col, pal.band, sdf.SmoothStep(0.15, 0.6, mo)*0.45*s.ColorVariation)
				col = col.Mul(1 + 0.18*s.ColorVariation*sdf.Clamp(mo, -1, 1))
				// A thin dark line at a bed's base (the parting), and the
				// laminations.
				col = col.Mul(1 - 0.06*sdf.SmoothStep(0.06, 0, t))
				if st.LamPeriod > 0 && b.lamShow > 0 {
					col = col.Mul(1 + 0.03*b.lamShow*math.Sin(2*math.Pi*c/st.LamPeriod+u*6))
				}
			}
			// Each joint block its own shade, faint and blended across the
			// joints.
			if b.blocks != nil {
				v := b.blocks.BlockValue(p, 0.15*b.blocks.Spacing.MinComp(), func(id uint64) float64 { return unitHash(id) })
				col = col.Mul(1 + 0.05*s.ColorVariation*(v-0.5))
			}
			col = col.Mul(1 + 0.07*grain.F(p))

			// Weathering.
			col = col.Mul(1 - 0.38*s.Cavity*cavity)
			col = mixV(col, col.Mul(1.18), s.Cavity*edge)
			if varnish := s.Varnish * b.pr.varnish; varnish > 0 {
				// Down steep faces, in streaks narrow and wide, heavier
				// where little sky is seen (under an overhang's lip).
				steep := sdf.SmoothStep(0.6, 0.15, math.Abs(n[1]))
				st := streak.At3(q[0]*2.2, q[1]*0.12, q[2]*2.2)*0.55 + streak.At3(q[0]*7+31, q[1]*0.3, q[2]*7-17)*0.3 +
					streak.At3(q[0]*0.3+5, q[1]*0.6, q[2]*0.3)*0.35
				st = st*0.5 + 0.5
				v := varnish * steep * sdf.SmoothStep(0.55, 0.75, st+0.3*(1-ao)) * (1 - 0.6*edge)
				col = mixV(col, pal.varnish, sdf.Clamp(v, 0, 0.9))
			}
			if s.Dust > 0 {
				up := sdf.SmoothStep(0.45, 0.95, n[1])
				col = mixV(col, pal.dust, s.Dust*up*(0.6+0.4*ao))
			}
			// Never to black: in a canyon a crevice's floor still sees the
			// warm rock round it.
			a := 0.22 + 0.78*(1-s.AO*(1-ao))*(1-0.45*s.Cavity*cavity)
			out[i] = [4]uint8{unitByte(col[0]), unitByte(col[1]), unitByte(col[2]), unitByte(a)}
		}
	})
	return out, err
}

// bedColor is bed idx of repetition rep's own colour: the palette's base
// mixed toward its band colour by the bed's own amount, softer beds toward
// the soft colour, each a little lighter or darker.
func bedColor(b *build, st *sdf.Strata, pal palette, idx, rep int) sdf.Vec3 {
	s := b.s
	hard := st.Layers[idx].Hardness
	u := unitHash(b.seed, uint64(idx), uint64(int64(rep)+1<<20))
	col := mixV(pal.base, pal.band, math.Pow(u, 1.5)*s.ColorVariation)
	col = mixV(col, pal.soft, (1-hard)*0.6)
	return col.Mul(1 + 0.18*s.ColorVariation*(unitHash(b.seed, 7, uint64(idx), uint64(int64(rep)+1<<20))-0.5))
}

func unitByte(v float64) uint8 { return uint8(sdf.Clamp(math.Round(v*255), 0, 255)) }
