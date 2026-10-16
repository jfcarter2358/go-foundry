package stages

// region_material_tiles.go — PER-TILE MATERIALS for a region terrain
// (docs/regions/PHASE_6.md, "Per-tile materials"): region_material's
// `per_tile` switch.
//
// A world of two biomes is one eight-layer splat (region_material.go), and
// every tile of it pays for eight layers — although only the tiles in the
// transition band ever weigh more than four. On the GTX 1070 that was 4–6 ms
// of the material pass over the desert's own four layers, on every tile.
// With `per_tile` on, the stage also writes a FOUR-LAYER material for each
// distinct LAYER SET the terrain's tiles actually use, and re-places every
// tile whose set fits in four wearing it; the band's tiles keep the
// eight-layer union.
//
// WHICH LAYERS A TILE USES. A layer is in a tile's set when its weight byte
// is 2 or more anywhere in the tile's WINDOW: the texels the tile's
// fragments can sample (one texel round its edge, for the bilinear filter),
// plus `tile_margin` metres more for the weights' mipmaps, which a far or
// grazing view reads and which average a neighbour's layers in from up to
// a few mip texels away. Byte 1 does not count, exactly: the shader takes a
// layer part only from a weight of 0.004 (fxSplatLayerOn), and no filtering
// of bytes ≤ 1 (≤ 1/255 = 0.0039) can reach it — so such a layer adds
// nothing on the eight-layer material either.
//
// WHY A FOUR-LAYER TILE DRAWS WHAT THE EIGHT-LAYER ONE WOULD. Within its
// window every layer outside its set weighs below the shader's cut, so on
// the union material those layers are off: not candidates, never sampled,
// never added. The four-layer material is the union's minus exactly those
// layers:
//
//   - its layers are the set's, in the UNION'S ORDER, each with the union's
//     own repeat, projection, tint, anti-tiling, relief and strata switch,
//     and the union's material-level knobs (one regionMaterialDoc builds
//     both) — so the scores, the best, the blend and the order the sums are
//     added in are the union's operation for operation;
//   - its weights are the union's bytes for those layers, over the union's
//     rectangle at the union's resolution (so the same filtering and the
//     same mip chain): for the set of layers 1–4 that IS weights.png, for
//     5–8 weights2.png (both reused, no new texture), and for a mixed set
//     (a few band-edge tiles) a new weights_l<layers>.png of those channels;
//   - its layers sample the union's ATLAS (the splat's `atlas1..8` params,
//     gfx's shared atlas): the very texels, gutters and mips the union's
//     tiles read, and no second atlas pair in video memory.
//
// So a four-layer tile and its eight-layer neighbour meet with no seam: the
// GL test render/material_fx_pertile_gl_test.go draws across such a
// boundary on the forward and visibility-buffer paths and compares it with
// both tiles on the union.
//
// SETS. Each tile's raw set is widened to the fewest materials: a set inside
// layers 1–4 becomes 1–4 and one inside 5–8 becomes 5–8 (a layer at zero
// weight costs nothing and changes nothing), and a mixed set joins a larger
// mixed set that holds it. A set of more than four is the union.
//
// THE BACKDROP is the same over the far weights (far_weights*.png): each
// ring tile's window on the far lattice, materials named <union>_far_l…;
// the STITCH strips round the map wear their edge tile's material (a strip
// reads the union's weights clamped at the edge, which its edge tile's
// window holds).
//
// HOW the tiles wear them: region_terrain places its tiles (it runs first:
// the weights are worked out from its masks), and this stage SUPERSEDES the
// placements it re-dresses (RunContext.Supersede, as drift re-places tiles
// it recooks), re-placing each as it was but for its material. They are
// then the level's gen.<doc>.<this stage>.tile_i_j. Only placements wearing
// the union material (or its far material) are touched: a region terrain
// told to wear another material keeps it.
//
// Off (the default, an `Added` param), nothing here runs and the stage's
// output is what it always was. The canyon_savannah and level_world
// templates switch it on for new documents.

import (
	"fmt"
	"math"
	"math/bits"
	"sort"
	"strings"

	"foundry/internal/material"
	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

// regionTileParams are the stage's per-tile parameters (Added: a document
// from before them keeps its hash and its output).
func regionTileParams() []procgen.ParamDef {
	return []procgen.ParamDef{
		{Name: "per_tile", Added: true, Type: procgen.ParamBool, Label: "Per-tile materials", Default: procgen.Bool(false),
			Doc: "Also write a four-layer material for every set of layers the terrain's tiles use, and dress each tile " +
				"whose layers fit in four in it; only the transition band's tiles keep the eight-layer material. Draws " +
				"the same picture for less: an eight-layer tile costs the GPU about half again a four-layer one. No " +
				"effect on a material of four layers or fewer."},
		{Name: "tile_margin", Added: true, Type: procgen.ParamFloat, Label: "Tile margin (m)", Default: procgen.Float(32),
			Min: 0, Max: 512, Step: 1,
			Doc: "How far past a tile's edge a layer still puts it in that tile's set: the reach of the weights' mipmaps, " +
				"which a distant or grazing view reads. Smaller makes more tiles four-layer; too small can show a faint " +
				"line at a four-layer tile's edge from far off."},
	}
}

// RegionTileMaterial is one per-tile material, as the manifest lists it.
type RegionTileMaterial struct {
	Material string `yaml:"material"`
	// Layers are its layers, 1-based in the union's order.
	Layers []int `yaml:"layers,flow"`
	// Weights is its weights map (the union's own for layers 1–4 or 5–8).
	Weights string `yaml:"weights"`
	// Far: a backdrop material.
	Far bool `yaml:"far,omitempty"`
	// Placements is how many placements wear it.
	Placements int `yaml:"placements"`
}

// layerMask is a set of union layers, bit i layer i (0-based).
type layerMask uint8

// layers is the mask's layers, ascending.
func (m layerMask) layers() []int {
	var out []int
	for i := 0; i < 8; i++ {
		if m&(1<<i) != 0 {
			out = append(out, i)
		}
	}
	return out
}

// tag is its name part: "l" and its 1-based layers, l1234.
func (m layerMask) tag() string {
	var b strings.Builder
	b.WriteByte('l')
	for _, i := range m.layers() {
		b.WriteByte(byte('1' + i))
	}
	return b.String()
}

// texelWindow is a rectangle of weights texels, inclusive.
type texelWindow struct{ i0, i1, j0, j1 int }

// presence is the layers whose byte is 2 or more anywhere in win of the
// n×n weights maps w1 (layers 1–4) and w2 (5–8, nil for none).
func presence(w1, w2 []uint8, n int, win texelWindow) layerMask {
	var m layerMask
	i0, i1 := max(win.i0, 0), min(win.i1, n-1)
	j0, j1 := max(win.j0, 0), min(win.j1, n-1)
	for j := j0; j <= j1; j++ {
		for i := i0; i <= i1; i++ {
			k := 4 * (j*n + i)
			for c := 0; c < 4; c++ {
				if w1[k+c] >= 2 {
					m |= 1 << c
				}
				if w2 != nil && w2[k+c] >= 2 {
					m |= 1 << (4 + c)
				}
			}
		}
	}
	return m
}

// chooseLayerSets maps each raw set to the set whose material wears it
// (0: the union): within layers 1–4 → 1–4, within 5–8 → 5–8 (of nl
// layers), more than four → the union, and a mixed set to the largest
// mixed set holding it (sets in order of size, then value, so the choice
// is deterministic).
func chooseLayerSets(raw []layerMask, nl int) map[layerMask]layerMask {
	lo := layerMask(0x0f)
	hi := layerMask((1<<nl)-1) &^ lo
	out := map[layerMask]layerMask{}
	var mixed []layerMask
	for _, m := range raw {
		if _, ok := out[m]; ok {
			continue
		}
		switch {
		case bits.OnesCount8(uint8(m)) > 4:
			out[m] = 0
		case m&^lo == 0:
			out[m] = lo
		case m&^hi == 0:
			out[m] = hi
		default:
			out[m] = m
			mixed = append(mixed, m)
		}
	}
	sort.Slice(mixed, func(a, b int) bool {
		ca, cb := bits.OnesCount8(uint8(mixed[a])), bits.OnesCount8(uint8(mixed[b]))
		if ca != cb {
			return ca > cb
		}
		return mixed[a] < mixed[b]
	})
	var chosen []layerMask
	for _, m := range mixed {
		home := m
		for _, c := range chosen {
			if m&^c == 0 {
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

// remapWeights is the n×n RGBA weights of set's layers (ascending) from
// the union's maps: channel c of the result is set's c-th layer.
func remapWeights(w1, w2 []uint8, n int, set layerMask) []uint8 {
	out := make([]uint8, 4*n*n)
	for c, l := range set.layers() {
		src, ch := w1, l
		if l >= 4 {
			src, ch = w2, l-4
		}
		for k := 0; k < n*n; k++ {
			out[4*k+c] = src[4*k+ch]
		}
	}
	return out
}

// tilePlacementWindow is the weights window of a near placement the region
// terrain made — tile_<i>_<j>, or strip_<side>_<k> (its edge tile's) — and
// false for any other.
func tilePlacementWindow(id string, g *terrain.Grid, tm *terrain.TilesMeta, margin int) (texelWindow, bool) {
	per := int(math.Round(tm.Size / g.Cell))
	var ti, tj int
	if _, err := fmt.Sscanf(id, "tile_%d_%d", &ti, &tj); err != nil || tileName(ti, tj) != id {
		var side byte
		var k int
		if _, err := fmt.Sscanf(id, "strip_%c_%d", &side, &k); err != nil || fmt.Sprintf("strip_%c_%d", side, k) != id {
			return texelWindow{}, false
		}
		switch side {
		case 's':
			ti, tj = k, 0
		case 'n':
			ti, tj = k, tm.Count-1
		case 'w':
			ti, tj = 0, k
		case 'e':
			ti, tj = tm.Count-1, k
		default:
			return texelWindow{}, false
		}
	}
	if ti < 0 || tj < 0 || ti >= tm.Count || tj >= tm.Count || per <= 0 {
		return texelWindow{}, false
	}
	// One texel round the tile (the bilinear filter at its edge), and the
	// margin.
	m := 1 + margin
	return texelWindow{ti*per - m, (ti+1)*per + m, tj*per - m, (tj+1)*per + m}, true
}

// farPlacementWindow is the far weights window of ring tile far_<a>_<b>
// (segX/segZ its boundaries), and false for any other placement.
func farPlacementWindow(id string, fm *terrain.FarMeta, segX, segZ []float64, margin int) (texelWindow, bool) {
	var a, b int
	if _, err := fmt.Sscanf(id, "far_%d_%d", &a, &b); err != nil || fmt.Sprintf("far_%d_%d", a, b) != id {
		return texelWindow{}, false
	}
	if a < 0 || b < 0 || a+1 >= len(segX) || b+1 >= len(segZ) {
		return texelWindow{}, false
	}
	ix := func(v, lo float64) int { return int(math.Round((v - lo) / fm.Cell)) }
	m := 1 + margin
	return texelWindow{ix(segX[a], fm.Bounds.MinX) - m, ix(segX[a+1], fm.Bounds.MinX) + m,
		ix(segZ[b], fm.Bounds.MinZ) - m, ix(segZ[b+1], fm.Bounds.MinZ) + m}, true
}

// regionFarSegments is the region terrain's ring tile boundaries (as
// writeFarWith cut them), from its grid, its backdrop record and its
// far_tile.
func regionFarSegments(rc *procgen.RunContext, g *terrain.Grid, fm *terrain.FarMeta) (segX, segZ []float64) {
	st := terrainSourceStage(rc.Doc, rc.Params.Str("terrain")) // through a ground_edit
	tile := procgen.Resolve(procgen.KindByName(RegionTerrainKind).Params, st.Params).Float("far_tile")
	half := g.Size() / 2
	cx, cz := g.MinX+half, g.MinZ+half
	o := half + fm.Cell
	r := fm.Bounds.MaxX - cx
	return farSegments(cx, r, o, fm.Cell, tile), farSegments(cz, r, o, fm.Cell, tile)
}

// tileDress is one placement to re-dress: its set, raw then chosen.
type tileDress struct {
	pl       procgen.Placement
	raw, set layerMask
}

// writeRegionTileMaterials is the per-tile pass (see the file comment): a
// no-op unless per_tile is on and the union has more than four layers.
// w1, w2 are the union's weights bytes; far the backdrop's (nil: none).
func writeRegionTileMaterials(rc *procgen.RunContext, t *terrain.Terrain, a *TerrainMaterialArtifact, layers []*regionLayer,
	leadP procgen.Params, lead string, w1, w2 []uint8, far *farWeightMaps) error {
	p := rc.Params
	nl := len(layers)
	if !p.Bool("per_tile") || nl <= 4 {
		if p.Bool("per_tile") {
			rc.Logf("per-tile materials: the material has %d layers, already four or fewer", nl)
		}
		return nil
	}
	terrainID := p.Str("terrain")
	up := rc.Upstream(terrainID)
	if up == nil {
		return fmt.Errorf("per-tile materials: no upstream stage %q", terrainID)
	}
	g := t.Grid
	sz := g.S()
	tm := &t.Meta.Tiles
	margin := p.Float("tile_margin")
	nearM := int(math.Ceil(margin / g.Cell))
	var near, ring []*tileDress
	var segX, segZ []float64
	farM := 0
	if far != nil && t.Meta.Far != nil {
		segX, segZ = regionFarSegments(rc, g, t.Meta.Far)
		farM = int(math.Ceil(margin / t.Meta.Far.Cell))
	}
	for _, pl := range up.Placements {
		switch {
		case pl.Material == a.Material:
			if win, ok := tilePlacementWindow(pl.ID, g, tm, nearM); ok {
				near = append(near, &tileDress{pl: pl, raw: presence(w1, w2, sz, win)})
			}
		case far != nil && a.FarMaterial != "" && pl.Material == a.FarMaterial:
			if win, ok := farPlacementWindow(pl.ID, t.Meta.Far, segX, segZ, farM); ok {
				ring = append(ring, &tileDress{pl: pl, raw: presence(far.w1, far.w2, far.n, win)})
			}
		}
	}
	n4, nf4 := 0, 0
	var err error
	if n4, err = dressTiles(rc, near, terrainID, a, layers, leadP, lead, w1, w2, sz, nil); err != nil {
		return err
	}
	if far != nil {
		if nf4, err = dressTiles(rc, ring, terrainID, a, layers, leadP, lead, far.w1, far.w2, far.n, &far.rect); err != nil {
			return err
		}
	}
	rc.Logf("per-tile materials: %d of %d tiles and strips four-layer, %d of %d ring tiles; %d materials (margin %g m)",
		n4, len(near), nf4, len(ring), len(a.Tiles), margin)
	return nil
}

// dressTiles chooses the sets of one group of placements — near, or the
// ring (farRect then the backdrop weights' rectangle) — writes a
// four-layer material per set, and re-places every placement whose set is
// not the union in its set's material. It returns how many.
func dressTiles(rc *procgen.RunContext, ds []*tileDress, terrainID string, a *TerrainMaterialArtifact, layers []*regionLayer,
	leadP procgen.Params, lead string, w1, w2 []uint8, n int, farRect *[2][2]float64) (int, error) {
	far := farRect != nil
	raw := make([]layerMask, len(ds))
	for i, d := range ds {
		raw[i] = d.raw
	}
	choice := chooseLayerSets(raw, len(layers))
	count := map[layerMask]int{}
	for _, d := range ds {
		d.set = choice[d.raw]
		if d.set != 0 {
			count[d.set]++
		}
	}
	var sets []layerMask
	for s := range count {
		sets = append(sets, s)
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i] < sets[j] })
	names := map[layerMask]string{}
	lo := layerMask(0x0f)
	hi := layerMask((1<<len(layers))-1) &^ lo
	for _, set := range sets {
		sa := *a
		part := set.tag()
		w1Ref, w2Ref := a.Weights, a.Weights2
		if far {
			part = "far_" + part
			w1Ref, w2Ref = a.FarWeights, a.FarWeights2
			sa.Min, sa.Max = farRect[0], farRect[1]
		}
		var ref string
		switch set {
		case lo:
			ref = w1Ref
		case hi:
			ref = w2Ref
		default:
			file := "weights_" + set.tag() + ".png"
			if far {
				file = "far_weights_" + set.tag() + ".png"
			}
			data, err := terrain.EncodeRGBA(n, n, remapWeights(w1, w2, n, set))
			if err != nil {
				return 0, err
			}
			if err := rc.WriteFile(file, data); err != nil {
				return 0, err
			}
			ref = mapRef(rc, file)
		}
		sa.Material = genMaterialName(rc, part)
		sa.Weights, sa.Weights2 = ref, ""
		var sub []*regionLayer
		for _, l := range set.layers() {
			sub = append(sub, layers[l])
		}
		d := regionMaterialDoc(&sa, leadP, sub, lead)
		e := d.Find(material.KindSplat)
		for i, l := range layers {
			e.Set(material.SplatAtlasParam(i), material.TextureValue(l.layer.Albedo))
		}
		if far {
			farMaterialTweaks(d, leadP)
			d.Doc = "Generated region terrain backdrop, per tile: the backdrop's layers " + strings.TrimPrefix(set.tag(), "l") +
				" only (the eight-layer backdrop's atlas, weights and knobs)"
		} else {
			d.Doc = "Generated region terrain, per tile: layers " + strings.TrimPrefix(set.tag(), "l") +
				" of " + a.Material + " only (its atlas, weights and knobs), for tiles that weigh no other"
		}
		if err := writeMaterial(rc, d); err != nil {
			return 0, err
		}
		names[set] = sa.Material
		var ls []int
		for _, l := range set.layers() {
			ls = append(ls, l+1)
		}
		a.Tiles = append(a.Tiles, RegionTileMaterial{Material: sa.Material, Layers: ls, Weights: ref, Far: far, Placements: count[set]})
	}
	moved := 0
	for _, d := range ds {
		if d.set == 0 {
			continue
		}
		q := d.pl
		q.Material = names[d.set]
		rc.Supersede(terrainID, d.pl.ID)
		rc.Place(q)
		moved++
	}
	return moved, nil
}
