package stages

// ground_edit_run.go — the ground_edit stage's run (ground_edit.go has the
// stage and why it is a terrain source).
//
//  1. The source terrain (its artifact: fresh this run, or read back),
//     its heights quantised to its own heightmap's step either way.
//  2. HEIGHT: every height layer's offset × its scale added to the samples
//     its stored tiles cover.
//  3. WATER: every water layer carved (terrain.CarveBasins) in list order,
//     each against the ground the earlier ones left; the bodies, their
//     levels and the per-sample water gathered (ground_edit_water.go).
//  4. QUANTISED to what the files hold: the heights on heightmap.png's
//     16 bits over the source's range padded 16 m each way (so no stroke
//     short of a 16 m dig moves the range, and with it every sample), the
//     masks on 8 bits. The artifact is then exactly what Load reads back
//     (ArtifactAsLoaded), and a downstream piece made against either is the
//     same piece.
//  5. MASKS: slope and curvature recomputed where the ground moved (and
//     the reach of their stencils), the rest as the source wrote them; the
//     water mask (the source's, if any, with the bodies' depth over it).
//     Flow, deposition, cliff and strata are kept: a pond's bank does not
//     change where water ran before it was dug.
//  6. FILES: terrain.yaml (the source's, with the new height range, the
//     water bodies and a `ground_edit:` record), the rewritten images, and
//     every other file the source's terrain.yaml names HARD-LINKED (copied
//     where links fail) into this folder, so it is a whole terrain folder
//     Load can read and the source can be rewritten without changing it.
//  7. TILES: every placement of the source superseded and placed again;
//     a tile whose samples (or the ring its normals read) moved is recooked
//     — on the source's tile plan, base and quantisation step, so it meets
//     its untouched neighbours exactly as drift's recooked tiles do — into
//     tile_<i>_<j>_<key>.fnm. The key in the file name makes a recooked
//     tile a NEW model path, so the editor's live splice reloads it rather
//     than keeping the mesh it has under the old name. Incremental: a tile
//     whose key matches last run's keeps its file untouched.
//  8. WATER MESHES per tile and material (ground_edit_water.go).
//  9. RIVERS (phase W3): carved after the basins (step 3b, before the
//     quantisation), they add their wet samples to the water mask, raise
//     the flow mask along their channels and write the river masks
//     (terrain/river_meta.go); their ribbons, rivers.yaml and boulders
//     follow the ponds' meshes (ground_edit_river*.go); phase W4 adds each
//     waterfall's falling sheet, its mist emitters and its pool's foam
//     rings (ground_edit_falls.go).
// 10. PATHS (phase P4): laid after the rivers (step 3c, before the
//     quantisation), they flatten their beds and write the path masks
//     (terrain/path.go); their stones and paths.yaml follow the rivers'
//     (ground_edit_path*.go).

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"foundry/internal/procgen"
	"foundry/internal/procgen/meshout"
	"foundry/internal/procgen/paint"
	"foundry/internal/procgen/terrain"

	"gopkg.in/yaml.v2"
)

// groundTileVersion changes when a recooked tile's mesh changes for the
// same heights.
const groundTileVersion = "ground-tile-1"

// heightPad is how far past the source's height range an edited
// heightmap's range reaches (metres; file comment, step 4).
const heightPad = 16

func runGroundEdit(rc *procgen.RunContext) error {
	p := rc.Params
	t0 := time.Now()
	tid := p.Str("terrain")
	ta, err := rc.UpstreamArtifact(tid)
	if err != nil {
		return err
	}
	src, ok := ta.(*terrain.Terrain)
	if !ok {
		return fmt.Errorf("stage %s is not a terrain stage", tid)
	}
	up := rc.Upstream(tid)
	tm := src.Meta.Tiles
	if tm.Step == 0 || tm.Count == 0 {
		return fmt.Errorf("the terrain stage %s does not cook its tiles", tid)
	}
	srcStage := rc.Doc.StageByID(tid)
	if srcStage == nil || procgen.KindByName(srcStage.Kind) == nil {
		return fmt.Errorf("no terrain stage %q", tid)
	}
	sp := procgen.Resolve(procgen.KindByName(srcStage.Kind).Params, srcStage.Params)

	// The source's heights as its heightmap holds them: a source that ran
	// this run is unquantised, one read back is not, and the edit must be
	// the same either way (so its tiles and every downstream piece are).
	g0 := src.Grid.Clone()
	terrain.QuantizeHeights(g0.H, src.Meta.Height.Lo, src.Meta.Height.Hi)
	g := g0.Clone()
	sz := g.S()

	// 2. Height layers.
	for _, it := range p.Items("height") {
		l := rc.Doc.PaintLayer(it.Str("layer"))
		if l.Empty() {
			continue
		}
		scale := float32(it.Float("scale"))
		i0, j0, i1, j1, ok := layerWindow(g, l)
		if !ok {
			continue
		}
		for j := j0; j <= j1; j++ {
			for i := i0; i <= i1; i++ {
				if v := l.Sample(g.X(i), g.Z(j)); v != 0 {
					g.H[j*sz+i] += scale * float32(v)
				}
			}
		}
	}

	// 3. Water layers.
	ws, err := carveGroundWater(rc, g, src)
	if err != nil {
		return err
	}
	// 3b. Rivers (phase W3, ground_edit_river.go), after the basins they
	// may run into.
	rv, err := carveGroundRivers(rc, g, src, ws)
	if err != nil {
		return err
	}
	// The plunge pools' foam rings (phase W4, ground_edit_falls.go), before
	// the river masks and the ribbons read the foam.
	fallsFoam(g, rv)
	// 3c. Paths (phase P4, ground_edit_path.go), after the water they
	// ford.
	gp := layGroundPaths(rc, g, ws, rv)

	// 4. Quantise, over the source's range padded by heightPad (×4 until
	// the edits fit): a range that followed the edits would move every
	// sample by a quantum whenever a stroke dug a little deeper, and every
	// downstream piece would rebuild.
	slo, shi := src.Meta.Height.Lo, src.Meta.Height.Hi
	elo, ehi := g.Range()
	pad := float32(heightPad)
	for elo < slo-pad || ehi > shi+pad {
		pad *= 4
	}
	lo, hi := slo-pad, shi+pad
	// Moved: what an edit touched, before the final quantisation.
	moved := make([]bool, len(g.H))
	nMoved := 0
	for k := range g.H {
		if g.H[k] != g0.H[k] {
			moved[k] = true
			nMoved++
		}
	}
	terrain.QuantizeHeights(g.H, lo, hi)

	// 5. Masks.
	slope0, err := src.Mask(terrain.MaskSlope)
	if err != nil {
		return err
	}
	curv0, err := src.Mask(terrain.MaskCurvature)
	if err != nil {
		return err
	}
	slope := append([]float32(nil), slope0...)
	curv := append([]float32(nil), curv0...)
	if nMoved > 0 {
		near := dilate(moved, sz, terrain.CurvatureReach(g))
		for k, on := range near {
			if on {
				terrain.SurfaceMasks(g, slope, curv, k%sz, k/sz, k%sz, k/sz)
			}
		}
	}
	terrain.QuantizeUnit(slope)
	terrain.QuantizeUnit(curv)
	var water []float32
	if wm, err := src.Mask(terrain.MaskWater); err == nil {
		water = append([]float32(nil), wm...)
	}
	if ws.wet() {
		if water == nil {
			water = make([]float32, len(g.H))
		}
		for k, b := range ws.water.Body {
			if b >= 0 {
				water[k] = max(water[k], min((ws.water.Level[k]-g.H[k])/float32(terrain.WaterMaskDepth), 1))
			}
		}
	}
	if rv.any() {
		if water == nil {
			water = make([]float32, len(g.H))
		}
		for k, s := range rv.res.Samples {
			if s.Wet {
				water[k] = max(water[k], min((s.Surface-g.H[k])/float32(terrain.WaterMaskDepth), 1))
			}
		}
	}
	if water != nil {
		terrain.QuantizeUnit(water)
	}
	var pmasks map[string][]float32
	if gp.any() {
		pmasks = gp.masks()
	}
	// The rivers' masks, and the flow mask raised along their channels.
	var rmasks map[string][]float32
	var flow []float32
	if rv.any() {
		rmasks = riverMasks(g, rv)
		if f0, err := src.Mask(terrain.MaskFlow); err == nil && f0 != nil {
			flow = append([]float32(nil), f0...)
			bed := rmasks[terrain.MaskRiverBed]
			for k, b := range bed {
				flow[k] = max(flow[k], 0.9*b)
			}
			terrain.QuantizeUnit(flow)
		}
	}

	// 6. Files.
	meta := src.Meta
	meta.Masks = append([]terrain.MaskMeta(nil), src.Meta.Masks...)
	meta.Height.Lo, meta.Height.Hi = lo, hi
	elo, ehi = g.Range()
	meta.Bounds.MinY, meta.Bounds.MaxY = elo, ehi
	if water != nil && !hasMask(meta.Masks, terrain.MaskWater) {
		meta.Masks = append(meta.Masks, terrain.MaskMeta{Name: terrain.MaskWater, File: terrain.MaskFile(terrain.MaskWater), Encoding: "unit",
			Doc: fmt.Sprintf("standing water's depth over %g m, 0..1 (0: dry)", terrain.WaterMaskDepth)})
	}
	meta.Water = groundWaterMeta(src.Meta.Water, ws)
	meta.Rivers = riversMeta(rv)
	for _, name := range terrain.RiverMaskNames {
		if rmasks != nil && !hasMask(meta.Masks, name) {
			meta.Masks = append(meta.Masks, terrain.MaskMeta{Name: name, File: terrain.MaskFile(name), Encoding: "unit",
				Doc: terrain.RiverMaskDocs[name]})
		}
	}
	for _, name := range terrain.PathMaskNames {
		if pmasks != nil && !hasMask(meta.Masks, name) {
			meta.Masks = append(meta.Masks, terrain.MaskMeta{Name: name, File: terrain.MaskFile(name), Encoding: "unit",
				Doc: terrain.PathMaskDocs[name]})
		}
	}
	rewritten := map[string][]byte{}
	if rewritten[meta.Height.File], err = terrain.EncodeGray16(sz, sz, g.H, lo, hi); err != nil {
		return err
	}
	if rewritten[terrain.MaskFile(terrain.MaskSlope)], err = terrain.EncodeGray8(sz, sz, slope); err != nil {
		return err
	}
	if rewritten[terrain.MaskFile(terrain.MaskCurvature)], err = terrain.EncodeGray8(sz, sz, curv); err != nil {
		return err
	}
	if water != nil {
		if rewritten[terrain.MaskFile(terrain.MaskWater)], err = terrain.EncodeGray8(sz, sz, water); err != nil {
			return err
		}
	}
	for _, name := range terrain.RiverMaskNames {
		if m := rmasks[name]; m != nil {
			if rewritten[terrain.MaskFile(name)], err = terrain.EncodeGray8(sz, sz, m); err != nil {
				return err
			}
		}
	}
	if flow != nil {
		if rewritten[terrain.MaskFile(terrain.MaskFlow)], err = terrain.EncodeGray8(sz, sz, flow); err != nil {
			return err
		}
	}
	for _, name := range terrain.PathMaskNames {
		if m := pmasks[name]; m != nil {
			if rewritten[terrain.MaskFile(name)], err = terrain.EncodeGray8(sz, sz, m); err != nil {
				return err
			}
		}
	}
	if err := linkTerrainFiles(rc, src, rewritten); err != nil {
		return err
	}
	for _, name := range sortedKeys(rewritten) {
		if err := rc.WriteFile(name, rewritten[name]); err != nil {
			return err
		}
	}

	// 7. Tiles.
	tp := tilePlan{size: tm.Size, count: tm.Count, step: max(sp.Int("mesh_step"), 1), base: tm.Base, quant: tm.Step}
	tp.per, _ = whole(tm.Size, g.Cell)
	recooked, err := groundEditTiles(rc, g, tp, sp, moved, up)
	if err != nil {
		return err
	}
	meta.GroundEdit = &terrain.GroundEditMeta{Source: tid, Samples: nMoved, Tiles: recooked}

	// 8. Water meshes.
	// The rivers' cells first: where a river runs into a pond the ribbon
	// takes the pond's under-bank skirt, so neither leaves a dry gap nor
	// doubles the other (ground_edit_river_mesh.go).
	var rcells map[int32]int32
	if rv.any() {
		rcells = riverCells(g, rv, pondCoreCells(g, ws))
	}
	if err := writeGroundWater(rc, g, tp, sp, ws, rcells); err != nil {
		return err
	}
	if err := writeBodiesFile(rc, ws); err != nil {
		return err
	}
	// 9. Rivers: the ribbons, rivers.yaml, the boulders.
	if err := writeGroundRivers(rc, g, tp, rv, rcells); err != nil {
		return err
	}
	if len(p.Items("rivers")) > 0 || p.Bool("drainage") {
		if err := writeRiversFile(rc, rv); err != nil {
			return err
		}
	}
	if err := placeBoulders(rc, rv); err != nil {
		return err
	}
	// The waterfalls' sheets and mist (phase W4, ground_edit_falls.go).
	if err := writeGroundFalls(rc, g, rv); err != nil {
		return err
	}
	// The water's sounds (phase W5, ground_edit_audio.go).
	placeWaterSounds(rc, g, rv, ws)
	// 10. Paths: the stones, paths.yaml.
	note, err := placePathStones(rc, g, gp)
	if err != nil {
		return err
	}
	if len(p.Items("paths")) > 0 {
		if err := writePathsFile(rc, gp, note); err != nil {
			return err
		}
	}
	meta.Footpaths = gp.meta()
	// The rocks this stage placed, for the placement stages to keep out of
	// (phase W4, ground_obstacles.go).
	if rv != nil {
		meta.Obstacles = append(meta.Obstacles, rv.obstacles...)
	}
	if gp != nil {
		meta.Obstacles = append(meta.Obstacles, gp.obstacles...)
	}
	data, err := yaml.Marshal(meta)
	if err != nil {
		return err
	}
	if err := rc.WriteFile(terrain.MetaFile, data); err != nil {
		return err
	}
	masks := map[string][]float32{terrain.MaskSlope: slope, terrain.MaskCurvature: curv}
	if water != nil {
		masks[terrain.MaskWater] = water
	}
	for name, m := range rmasks {
		masks[name] = m
	}
	if flow != nil {
		masks[terrain.MaskFlow] = flow
	}
	for name, m := range pmasks {
		masks[name] = m
	}
	rc.Artifact = terrain.FromResult(&terrain.Result{Grid: g, Masks: masks}, meta, rc.Dir)
	rc.ArtifactAsLoaded = true
	rc.Logf("%d samples moved, %d of %d tiles moved, %d water bodies, in %s", nMoved, recooked, tp.count*tp.count,
		len(ws.bodies), time.Since(t0).Round(time.Millisecond))
	return nil
}

// layerWindow is the grid samples a layer's stored tiles cover (plus one
// texel for the bilinear reach), clamped to the grid; false when none.
func layerWindow(g *terrain.Grid, l *paint.Layer) (i0, j0, i1, j1 int, ok bool) {
	r, ok := layerRect(l)
	if !ok {
		return 0, 0, 0, 0, false
	}
	sz := g.S()
	i0 = max(int(math.Floor((r[0]-g.MinX)/g.Cell)), 0)
	j0 = max(int(math.Floor((r[1]-g.MinZ)/g.Cell)), 0)
	i1 = min(int(math.Ceil((r[2]-g.MinX)/g.Cell)), sz-1)
	j1 = min(int(math.Ceil((r[3]-g.MinZ)/g.Cell)), sz-1)
	return i0, j0, i1, j1, i1 >= i0 && j1 >= j0
}

// layerRect is the world rectangle of a layer's stored tiles, one texel
// wider each way; false for an empty layer.
func layerRect(l *paint.Layer) ([4]float64, bool) {
	keys := l.Keys()
	if len(keys) == 0 {
		return [4]float64{}, false
	}
	r := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, k := range keys {
		kr := k.Rect()
		r = [4]float64{math.Min(r[0], kr[0]), math.Min(r[1], kr[1]), math.Max(r[2], kr[2]), math.Max(r[3], kr[3])}
	}
	t := l.Def.Res()
	return [4]float64{r[0] - t, r[1] - t, r[2] + t, r[3] + t}, true
}

// dilate is on grown by r samples (a square), separably.
func dilate(on []bool, sz, r int) []bool {
	tmp := make([]bool, len(on))
	for j := 0; j < sz; j++ {
		last := -1 << 30
		for i := 0; i < sz; i++ {
			if on[j*sz+i] {
				last = i
			}
			if i-last <= r {
				tmp[j*sz+i] = true
			}
		}
		last = 1 << 30
		for i := sz - 1; i >= 0; i-- {
			if on[j*sz+i] {
				last = i
			}
			if last-i <= r {
				tmp[j*sz+i] = true
			}
		}
	}
	out := make([]bool, len(on))
	for i := 0; i < sz; i++ {
		last := -1 << 30
		for j := 0; j < sz; j++ {
			if tmp[j*sz+i] {
				last = j
			}
			if j-last <= r {
				out[j*sz+i] = true
			}
		}
		last = 1 << 30
		for j := sz - 1; j >= 0; j-- {
			if tmp[j*sz+i] {
				last = j
			}
			if last-j <= r {
				out[j*sz+i] = true
			}
		}
	}
	return out
}

func hasMask(ms []terrain.MaskMeta, name string) bool {
	for _, m := range ms {
		if m.Name == name {
			return true
		}
	}
	return false
}

// linkTerrainFiles puts every file the source's terrain.yaml names, except
// the ones being rewritten, into this stage's folder: a hard link, or a
// copy where the filesystem will not link.
func linkTerrainFiles(rc *procgen.RunContext, src *terrain.Terrain, skip map[string][]byte) error {
	m := src.Meta
	names := []string{m.Height.File, m.Albedo, m.Relief}
	for _, mm := range m.Masks {
		names = append(names, mm.File)
	}
	if m.Biomes != nil {
		names = append(names, m.Biomes.Overlay)
	}
	if m.Far != nil {
		names = append(names, m.Far.Height.File)
	}
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" || seen[name] || skip[name] != nil {
			continue
		}
		seen[name] = true
		from := filepath.Join(src.Dir, filepath.FromSlash(name))
		if _, err := os.Stat(from); err != nil {
			continue // a source held only in memory names files it never wrote
		}
		to := rc.Path(name)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if linkedSame(from, to) {
			continue
		}
		os.Remove(to)
		if err := os.Link(from, to); err == nil {
			continue
		}
		if err := copyFile(from, to); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// linkedSame reports whether a and b are the same file already.
func linkedSame(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	return err == nil && os.SameFile(fa, fb)
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := to + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, to)
}

// groundTileKey is a recooked tile's piece key: the stage's base hash and
// lineage, the tile plan, the mesh settings and the tile's samples with the
// ring its normals read.
func groundTileKey(rc *procgen.RunContext, g *terrain.Grid, tp tilePlan, sp procgen.Params, ti, tj int, meshOnly bool) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d,%d|%d|%d|%d|%g|%g|%g|%g|%g|%g|%d|%v|%s|%s|%g|%s\n", ti, tj, tp.count, tp.per, tp.step, tp.size,
		tp.base, tp.quant, g.Cell, g.MinX, g.MinZ, g.N, meshOnly, sp.Str("texture"), sp.Str("uv_mode"), sp.Float("uv_scale"),
		sp.Str("collision"))
	i0, i1 := max(ti*tp.per-tp.step, 0), min((ti+1)*tp.per+tp.step, g.N)
	j0, j1 := max(tj*tp.per-tp.step, 0), min((tj+1)*tp.per+tp.step, g.N)
	var b [4]byte
	for j := j0; j <= j1; j++ {
		for i := i0; i <= i1; i++ {
			binary.LittleEndian.PutUint32(b[:], math.Float32bits(g.At(i, j)))
			h.Write(b[:])
		}
	}
	return groundPieceKey(rc, groundTileVersion, fmt.Sprintf("%x", h.Sum(nil)))
}

// groundPieceKey is a piece key of the stage's base hash, a version and
// parts — without the upstream lineage Env.PieceKey folds in: the edit
// requantises its source to the source's own heightmap first, so what it
// makes is the same whether the source ran this time or was read back.
func groundPieceKey(rc *procgen.RunContext, version string, parts ...string) string {
	h := sha256.New()
	fmt.Fprintf(h, "ground\x00%s\x00%s\x00", rc.BaseHash, version)
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// tileMoved reports whether any sample of tile (ti, tj), or of the ring its
// normals read, moved.
func tileMoved(moved []bool, g *terrain.Grid, tp tilePlan, ti, tj int) bool {
	sz := g.S()
	i0, i1 := max(ti*tp.per-tp.step, 0), min((ti+1)*tp.per+tp.step, g.N)
	j0, j1 := max(tj*tp.per-tp.step, 0), min((tj+1)*tp.per+tp.step, g.N)
	for j := j0; j <= j1; j++ {
		for i := i0; i <= i1; i++ {
			if moved[j*sz+i] {
				return true
			}
		}
	}
	return false
}

// groundEditTiles supersedes every placement of the source and places it
// again, recooking the tiles that moved (file comment, step 7). It returns
// how many moved (recooked this run, or reused from the last).
func groundEditTiles(rc *procgen.RunContext, g *terrain.Grid, tp tilePlan, sp procgen.Params, moved []bool,
	up *procgen.StageResult) (int, error) {
	tid := rc.Params.Str("terrain")
	collision := sp.Str("collision")
	meshOnly := collision == "mesh" || collision == "auto"
	type job struct {
		pl     procgen.Placement
		ti, tj int
	}
	var jobs []job
	for _, pl := range up.Placements {
		var ti, tj int
		if _, err := fmt.Sscanf(pl.ID, "tile_%d_%d", &ti, &tj); err == nil && tileName(ti, tj) == pl.ID &&
			ti >= 0 && tj >= 0 && ti < tp.count && tj < tp.count && tileMoved(moved, g, tp, ti, tj) {
			jobs = append(jobs, job{pl, ti, tj})
			continue
		}
		rc.Supersede(tid, pl.ID)
		rc.Place(pl)
	}
	if len(jobs) == 0 {
		return 0, nil
	}
	tris := tileTriangles(tp)
	conc := cookConcurrency(tris, rc.Params.Int("cook_memory"), rc.Workers, meshOnly)
	sem := make(chan struct{}, conc)
	uv := float32(sp.Float("uv_scale"))
	tex := sp.Str("texture")
	var cooked, reused atomic.Int64
	err := rc.Parallel(len(jobs), func(n int) (ferr error) {
		sem <- struct{}{}
		defer func() { <-sem }()
		if err := rc.Err(); err != nil {
			return err
		}
		jb := jobs[n]
		key := groundTileKey(rc, g, tp, sp, jb.ti, jb.tj, meshOnly)
		name := jb.pl.ID
		file := fmt.Sprintf("%s_%s.fnm", name, key[:10])
		pl := jb.pl
		pl.Model = rc.AssetPath(file)
		rc.Supersede(tid, pl.ID)
		if rc.ReuseTile(name, key, file) {
			reused.Add(1)
			rc.Place(pl)
			return nil
		}
		rc.BeginTile(name)
		defer func() {
			if ferr == nil {
				rc.SetTile(name, key)
			}
		}()
		hg, _ := tileGridAt(g, tp, jb.ti, jb.tj, uv, sp.Str("uv_mode") == "terrain")
		var b meshout.Builder
		b.Slot("ground", tex)
		hg.Build(&b)
		rect := hg.Rect()
		if _, err := meshout.Cook(b.Mesh(), rc.Path(file), meshout.CookOptions{LockRect: &rect, PositionStep: tp.quant,
			CollisionMesh: meshOnly}); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		cooked.Add(1)
		rc.Place(pl)
		return nil
	})
	if err != nil {
		return 0, err
	}
	rc.Logf("tiles: %d recooked, %d reused from the last run (cooking %d at a time)", cooked.Load(), reused.Load(), conc)
	return len(jobs), nil
}
