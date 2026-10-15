package stages

// region_material_palette.go — a region terrain's ground PALETTE: more than
// eight distinct ground layers in one world (docs/regions/PRESETS.md,
// "Engine work", item 1; docs/regions/PRESETS_1.md).
//
// One splat mixes at most eight layers (two weights maps, eight unrolled
// layer calls in the shader, the material pass's sampler budget), so a
// world of eleven environments cannot wear one union material. It does not
// need to: every TILE sees only the few environments within reach of it.
// When the deduped layers number more than eight (up to
// material.SplatAtlasMax, 32), region_material:
//
//   - keeps them as a PALETTE in the union's order, and packs them into one
//     shared atlas: every tile material names all of them as its
//     `atlas1..N`, so gfx builds ONE 8-across atlas (512 a layer: 32 layers
//     are the eight-layer 1k pair's 4352×2176, docs: gfx
//     material_fx_textures.go) and every tile samples the same texels;
//   - per-tile materials are then MANDATORY (whatever `per_tile` says): a
//     tile's LAYER SET is every palette layer whose weight byte is 2 or more
//     in its window — the tile, a texel round it and `tile_margin` metres
//     more, the same rule as region_material_tiles.go — and must hold at
//     most eight. A tile that needs more is an ERROR naming the tile and
//     its layers (the layout or the presets must share layers);
//   - each raw set joins the largest set already chosen that holds it (a set
//     of four or fewer only a set of four or fewer, so a tile deep in one
//     region stays four-layer), and
//     each chosen set gets a material of its layers in palette order: four
//     or fewer, a four-layer splat; five to eight, an eight-layer one —
//     regionMaterialDoc, the union's own builder, with the union's
//     per-layer settings and the lead's material knobs;
//   - its weights are the palette's bytes for its layers, over a sub-rectangle
//     of the union's texel grid: the bounding box of the windows of the tiles
//     wearing it, snapped OUT to multiples of paletteAlign texels from the
//     union's first texel. So every tile reads, at mip 0 and the next
//     log2(paletteAlign) mips, the very bytes and box averages its
//     neighbour's material reads for a shared layer: two tiles of different
//     sets meet with no seam in their common layers, and a layer one of them
//     lacks weighs nothing near that edge (it is under the cut, or it would
//     be in that tile's set);
//   - region_terrain's tiles and stitch strips (and the backdrop's ring
//     tiles, over the far weights by the same rules) are all re-dressed in
//     their set's material and superseded, as the per-tile pass does. There
//     is no union material file: a placement still wearing its name would
//     draw untextured, so any region_terrain placement it cannot re-dress is
//     an error.
//
// A world of eight layers or fewer never comes here: its output is what it
// always was (the union material, per_tile as asked).

import (
	"fmt"
	"math"
	"math/bits"
	"sort"
	"strconv"
	"strings"

	"foundry/internal/material"
	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

// paletteAlign is the texel alignment of a palette tile material's weights
// rectangle (see the file comment).
const paletteAlign = 64

// paletteMask is a set of palette layers, bit i layer i.
type paletteMask uint32

func (m paletteMask) layers() []int {
	var out []int
	for i := 0; i < material.SplatAtlasMax; i++ {
		if m&(1<<i) != 0 {
			out = append(out, i)
		}
	}
	return out
}

func (m paletteMask) count() int { return bits.OnesCount32(uint32(m)) }

// tag is its name part: "p" and its 1-based layers joined by "_".
func (m paletteMask) tag() string {
	var parts []string
	for _, i := range m.layers() {
		parts = append(parts, strconv.Itoa(i+1))
	}
	return "p" + strings.Join(parts, "_")
}

// palettePlanes is the nl-per-sample weights as one byte plane per layer
// (the bytes splitWeights would write).
func palettePlanes(w []float32, nl, n int) [][]uint8 {
	out := make([][]uint8, nl)
	for i := range out {
		out[i] = make([]uint8, n)
	}
	for k := 0; k < n; k++ {
		for i := 0; i < nl; i++ {
			out[i][k] = uint8(clampUnit(float64(w[k*nl+i]))*255 + 0.5)
		}
	}
	return out
}

// palettePresence is the layers whose byte is 2 or more anywhere in win of
// the side×side planes.
func palettePresence(planes [][]uint8, side int, win texelWindow) paletteMask {
	var m paletteMask
	i0, i1 := max(win.i0, 0), min(win.i1, side-1)
	j0, j1 := max(win.j0, 0), min(win.j1, side-1)
	for l, pl := range planes {
		if m&(1<<l) != 0 {
			continue
		}
	rows:
		for j := j0; j <= j1; j++ {
			row := pl[j*side : (j+1)*side]
			for i := i0; i <= i1; i++ {
				if row[i] >= 2 {
					m |= 1 << l
					break rows
				}
			}
		}
	}
	return m
}

// choosePaletteSets maps each raw set (each at most eight) to the set whose
// material wears it: the largest chosen set holding it — of four layers or
// fewer when it is — or itself (sets in order of size, then value).
func choosePaletteSets(raw []paletteMask) map[paletteMask]paletteMask {
	uniq := map[paletteMask]bool{}
	var list []paletteMask
	for _, m := range raw {
		if !uniq[m] {
			uniq[m] = true
			list = append(list, m)
		}
	}
	sort.Slice(list, func(a, b int) bool {
		if list[a].count() != list[b].count() {
			return list[a].count() > list[b].count()
		}
		return list[a] < list[b]
	})
	out := map[paletteMask]paletteMask{}
	var chosen []paletteMask
	for _, m := range list {
		home := m
		for _, c := range chosen {
			// A set of four or fewer joins only a four-layer set: an
			// eight-layer tile costs the GPU about half again a four-layer
			// one (docs/regions/PHASE_6.md), so a tile deep in one region
			// keeps its four.
			if m&^c == 0 && (m.count() > material.SplatLayers || c.count() <= material.SplatLayers) {
				home = c
				break
			}
		}
		if home == m {
			chosen = append(chosen, m)
		}
		out[m] = home
	}
	return out
}

// paletteDress is one placement to dress: its window and sets.
type paletteDress struct {
	pl       procgen.Placement
	win      texelWindow
	raw, set paletteMask
}

// describePaletteLayers names layers for an error.
func describePaletteLayers(m paletteMask, layers []*regionLayer) string {
	var parts []string
	for _, i := range m.layers() {
		l := layers[i]
		parts = append(parts, fmt.Sprintf("%d %s %s (%s)", i+1, strings.Join(l.envs, "+"), splatRoleNames[l.role], l.layer.ID))
	}
	return strings.Join(parts, ", ")
}

// alignWindow snaps win out to multiples of paletteAlign and clamps it into
// a side-texel grid.
func alignWindow(win texelWindow, side int) texelWindow {
	lo := func(v int) int { return max(int(math.Floor(float64(v)/paletteAlign))*paletteAlign, 0) }
	hi := func(v int) int { return min(int(math.Ceil(float64(v+1)/paletteAlign))*paletteAlign-1, side-1) }
	return texelWindow{lo(win.i0), hi(win.i1), lo(win.j0), hi(win.j1)}
}

// paletteWeights is the RGBA weights map(s) of set's layers (palette order;
// layers 5–8 in the second) over the window win of the side-texel planes.
func paletteWeights(planes [][]uint8, side int, set paletteMask, win texelWindow) (w1, w2 []uint8, ww, wh int) {
	ww, wh = win.i1-win.i0+1, win.j1-win.j0+1
	ls := set.layers()
	w1 = make([]uint8, 4*ww*wh)
	if len(ls) > material.SplatLayers {
		w2 = make([]uint8, 4*ww*wh)
	}
	for c, l := range ls {
		dst, ch := w1, c
		if c >= material.SplatLayers {
			dst, ch = w2, c-material.SplatLayers
		}
		pl := planes[l]
		for j := 0; j < wh; j++ {
			src := pl[(win.j0+j)*side+win.i0:]
			for i := 0; i < ww; i++ {
				dst[4*(j*ww+i)+ch] = src[i]
			}
		}
	}
	return w1, w2, ww, wh
}

// paletteGroup is one group of placements dressed over one weights grid:
// the near tiles and strips over the union's, or the ring over the far
// lattice's.
type paletteGroup struct {
	far    bool
	ds     []*paletteDress
	planes [][]uint8
	side   int
	// min and cell: the grid's first texel's corner (world) and texel size.
	min  [2]float64
	cell float64
}

// writeRegionPalette dresses every tile, strip and ring tile of a region
// terrain whose material has a palette of more than eight layers (see the
// file comment). w is the union's nl weights a sample; farW the backdrop's
// (nil: none), fn its side.
func writeRegionPalette(rc *procgen.RunContext, t *terrain.Terrain, a *TerrainMaterialArtifact, layers []*regionLayer,
	leadP procgen.Params, lead string, w []float32, farW []float32, fn int) error {
	p := rc.Params
	nl := len(layers)
	terrainID := p.Str("terrain")
	up := rc.Upstream(terrainID)
	if up == nil {
		return fmt.Errorf("palette: no upstream stage %q", terrainID)
	}
	g := t.Grid
	sz := g.S()
	margin := p.Float("tile_margin")
	near := &paletteGroup{planes: palettePlanes(w, nl, sz*sz), side: sz, min: a.Min, cell: g.Cell}
	var ring *paletteGroup
	var segX, segZ []float64
	farM := 0
	if farW != nil && t.Meta.Far != nil {
		fm := t.Meta.Far
		segX, segZ = regionFarSegments(rc, g, fm)
		farM = int(math.Ceil(margin / fm.Cell))
		ring = &paletteGroup{far: true, planes: palettePlanes(farW, nl, fn*fn), side: fn,
			min: [2]float64{fm.Bounds.MinX - fm.Cell/2, fm.Bounds.MinZ - fm.Cell/2}, cell: fm.Cell}
	}
	nearM := int(math.Ceil(margin / g.Cell))
	var stray []string
	for _, pl := range up.Placements {
		switch {
		case pl.Material == a.Material:
			win, ok := tilePlacementWindow(pl.ID, g, &t.Meta.Tiles, nearM)
			if !ok {
				stray = append(stray, pl.ID)
				continue
			}
			near.ds = append(near.ds, &paletteDress{pl: pl, win: win, raw: palettePresence(near.planes, sz, win)})
		case a.FarMaterial != "" && pl.Material == a.FarMaterial:
			if ring == nil {
				stray = append(stray, pl.ID)
				continue
			}
			win, ok := farPlacementWindow(pl.ID, t.Meta.Far, segX, segZ, farM)
			if !ok {
				stray = append(stray, pl.ID)
				continue
			}
			ring.ds = append(ring.ds, &paletteDress{pl: pl, win: win, raw: palettePresence(ring.planes, fn, win)})
		}
	}
	if len(stray) > 0 {
		sort.Strings(stray)
		return fmt.Errorf("a ground palette of %d layers has no union material, and %d of %s's placements wearing it are not "+
			"its tiles, strips or ring tiles: %s", nl, len(stray), terrainID, strings.Join(stray[:min(len(stray), 6)], ", "))
	}
	// Every tile must fit in eight.
	var over []string
	for _, grp := range []*paletteGroup{near, ring} {
		if grp == nil {
			continue
		}
		for _, d := range grp.ds {
			if d.raw.count() > material.SplatLayersMax {
				over = append(over, fmt.Sprintf("%s needs %d (%s)", d.pl.ID, d.raw.count(), describePaletteLayers(d.raw, layers)))
			}
		}
	}
	if len(over) > 0 {
		sort.Strings(over)
		more := ""
		if len(over) > 4 {
			more = fmt.Sprintf("; and %d more", len(over)-4)
		}
		return fmt.Errorf("%d ground layers in the palette, and %d tiles need more than %d of them within %g m (a tile's material "+
			"mixes at most %d): %s%s. Share layers between the environments' presets (the same Poly Haven id at the same scale "+
			"is one layer), move regions that meet apart, or lower tile_margin",
			nl, len(over), material.SplatLayersMax, margin, material.SplatLayersMax, strings.Join(over[:min(len(over), 4)], "; "), more)
	}
	var mats, texels int
	for _, grp := range []*paletteGroup{near, ring} {
		if grp == nil {
			continue
		}
		m, tx, err := dressPaletteGroup(rc, grp, terrainID, a, layers, leadP, lead)
		if err != nil {
			return err
		}
		mats += m
		texels += tx
	}
	a.Palette = nl
	n4 := 0
	for _, tm := range a.Tiles {
		if len(tm.Layers) <= material.SplatLayers {
			n4 += tm.Placements
		}
	}
	nr := 0
	if ring != nil {
		nr = len(ring.ds)
	}
	rc.Logf("ground palette: %d layers in one shared atlas; %d tiles and strips, %d ring tiles in %d materials (%d placements four-layer); "+
		"their weights maps %.1f M texels (%.0f MB RGBA8 with mips)", nl, len(near.ds), nr, mats, n4, float64(texels)/1e6,
		float64(texels)*4*4/3/1e6)
	return nil
}

// dressPaletteGroup chooses the group's sets, writes each set's weights
// and material, and re-places the group's placements in them. It returns
// the materials written and the weights texels.
func dressPaletteGroup(rc *procgen.RunContext, grp *paletteGroup, terrainID string, a *TerrainMaterialArtifact,
	layers []*regionLayer, leadP procgen.Params, lead string) (int, int, error) {
	raw := make([]paletteMask, len(grp.ds))
	for i, d := range grp.ds {
		raw[i] = d.raw
	}
	choice := choosePaletteSets(raw)
	wins := map[paletteMask]texelWindow{}
	count := map[paletteMask]int{}
	for _, d := range grp.ds {
		d.set = choice[d.raw]
		if d.set == 0 {
			// A tile no layer weighs anything on (it cannot be: weights sum
			// to one) — give it the palette's first layer.
			d.set = 1
		}
		if w, ok := wins[d.set]; ok {
			wins[d.set] = texelWindow{min(w.i0, d.win.i0), max(w.i1, d.win.i1), min(w.j0, d.win.j0), max(w.j1, d.win.j1)}
		} else {
			wins[d.set] = d.win
		}
		count[d.set]++
	}
	var sets []paletteMask
	for s := range count {
		sets = append(sets, s)
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i] < sets[j] })
	names := map[paletteMask]string{}
	texels := 0
	for _, set := range sets {
		win := alignWindow(wins[set], grp.side)
		w1, w2, ww, wh := paletteWeights(grp.planes, grp.side, set, win)
		texels += ww * wh * (1 + btoi(w2 != nil))
		part := set.tag()
		if grp.far {
			part = "far_" + part
		}
		sa := *a
		sa.Material = genMaterialName(rc, part)
		sa.Min = [2]float64{grp.min[0] + float64(win.i0)*grp.cell, grp.min[1] + float64(win.j0)*grp.cell}
		sa.Max = [2]float64{grp.min[0] + float64(win.i1+1)*grp.cell, grp.min[1] + float64(win.j1+1)*grp.cell}
		sa.Weights, sa.Weights2 = "", ""
		for k, pix := range [][]uint8{w1, w2} {
			if pix == nil {
				continue
			}
			file := "weights_" + part + ".png"
			if k == 1 {
				file = "weights2_" + part + ".png"
			}
			data, err := terrain.EncodeRGBA(ww, wh, pix)
			if err != nil {
				return 0, 0, err
			}
			if err := rc.WriteFile(file, data); err != nil {
				return 0, 0, err
			}
			if k == 0 {
				sa.Weights = mapRef(rc, file)
			} else {
				sa.Weights2 = mapRef(rc, file)
			}
		}
		var sub []*regionLayer
		for _, l := range set.layers() {
			sub = append(sub, layers[l])
		}
		d := regionMaterialDoc(&sa, leadP, sub, lead)
		e := d.Find(material.KindSplat)
		for i, l := range layers {
			e.Set(material.SplatAtlasParam(i), material.TextureValue(l.layer.Albedo))
		}
		tag := strings.ReplaceAll(strings.TrimPrefix(set.tag(), "p"), "_", ", ")
		if grp.far {
			farMaterialTweaks(d, leadP)
			d.Doc = "Generated region terrain backdrop, per tile: palette layers " + tag + " (the palette's shared atlas)"
		} else {
			d.Doc = "Generated region terrain, per tile: palette layers " + tag + " of " + strconv.Itoa(len(layers)) +
				" (the palette's shared atlas), for the tiles that weigh no other"
		}
		if err := writeMaterial(rc, d); err != nil {
			return 0, 0, err
		}
		names[set] = sa.Material
		var ls []int
		for _, l := range set.layers() {
			ls = append(ls, l+1)
		}
		a.Tiles = append(a.Tiles, RegionTileMaterial{Material: sa.Material, Layers: ls, Weights: sa.Weights, Far: grp.far,
			Placements: count[set]})
	}
	for _, d := range grp.ds {
		q := d.pl
		q.Material = names[d.set]
		rc.Supersede(terrainID, d.pl.ID)
		rc.Place(q)
	}
	return len(sets), texels, nil
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
