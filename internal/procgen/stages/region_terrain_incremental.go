package stages

// region_terrain_incremental.go — region_terrain's incremental runs
// (docs/regions/SPEC.md, "Incremental regeneration"; the engine side is
// procgen/incremental.go).
//
// region_terrain is an Incremental kind: when an edit makes it stale, the
// engine keeps last run's files and hands it last run's manifest entry. Two
// things are then reused, each keyed by a hash of EXACTLY its own inputs:
//
//   - EROSION WINDOWS (tiled erosion only). terrain.Union.Cache is
//     erosionCache, which keeps each window's eroded heights as
//     erosion/w_<i>_<j>.bin with the window's input hash in the header
//     (terrain/tiled_cache.go). A window none of whose inputs changed — the
//     pre-erosion ground over its square, the regions owning it and their
//     shares, the providers' settings, its knobs and seed — is read back
//     instead of eroded. The providers, weighing and blending still run over
//     the whole union (they are cheap beside erosion and the cook, and the
//     window keys are computed from what they produce).
//   - TILES. Each tile_<i>_<j>'s hash (tileInputHash) covers its heights
//     with the ring of samples its normals read, the shared base and
//     quantisation step, the mesh and cook settings, its place and the
//     kind's version. The hash goes into manifest.yaml (`tiles:`); a tile
//     whose hash matches last run's and whose file is still there is
//     neither meshed nor cooked — its .fnm is not touched — and only its
//     placement is written again.
//
// Content hashes rather than "which regions' bands touch this tile": the
// region data a tile depends on reaches it through the weights, and the
// nearest-region fill can carry a region's share far past its band
// (docs/regions/PHASE_2.md), so a geometric rule would either miss edits or
// be so conservative it reused nothing. Hashing what the generator actually
// read is exact by construction; the cost is that the providers and the
// blend always run.
//
// What makes far edits leave near tiles alone is TILED erosion: a window
// eroded on its own, with a fixed seed, depends only on the ground its square
// holds. With GLOBAL erosion every sample depends on every region (the
// drainage routes the whole union, the droplets' RNG blocks and the lead's
// knobs are global), so any region edit changes every tile's heights and
// every tile is recooked: global mode stays whole-stage in practice. The
// tile hashes still pay off there for edits that do not touch the heights
// (a cook_memory change, a rerun after a cancelled cook).
//
// One more thing has to hold still: the tiles' shared BASE (the closed
// bottom) and quantisation step are global — from the union's height range —
// so a far edit that lowers the lowest point by a metre would change every
// tile's bytes. With tiled erosion the base is snapped down to a multiple of
// tileBaseSnap (64 m) and the step follows from it, so they move only when the
// range crosses a snap (stableTileBase).

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

// tileBaseSnap is the tiled-erosion tile base's step, metres.
const tileBaseSnap = 64

// tileHashVersion changes when a tile's mesh or cook changes for the same
// inputs (with the kind's Version).
const tileHashVersion = "region-tile-1"

// erosionCacheMagic heads an erosion window file.
var erosionCacheMagic = []byte("FGERWIN1")

// erosionCache keeps tiled-erosion windows in the stage's folder.
type erosionCache struct{ rc *procgen.RunContext }

func erosionWindowFile(name string) string { return "erosion/" + name + ".bin" }

func (c erosionCache) Load(name string, key [32]byte) ([]byte, bool) {
	rel := erosionWindowFile(name)
	data, err := os.ReadFile(filepath.Join(c.rc.Dir, filepath.FromSlash(rel)))
	if err != nil || len(data) < len(erosionCacheMagic)+32 || !bytes.Equal(data[:len(erosionCacheMagic)], erosionCacheMagic) ||
		!bytes.Equal(data[len(erosionCacheMagic):len(erosionCacheMagic)+32], key[:]) {
		return nil, false
	}
	c.rc.Path(rel) // still one of the stage's outputs
	return data[len(erosionCacheMagic)+32:], true
}

func (c erosionCache) Store(name string, key [32]byte, payload []byte) error {
	data := make([]byte, 0, len(erosionCacheMagic)+32+len(payload))
	data = append(append(append(data, erosionCacheMagic...), key[:]...), payload...)
	return c.rc.WriteFile(erosionWindowFile(name), data)
}

// providerKey fingerprints what a region's provider's material compiles
// from, for the window cache: its preset terrain stage's resolved params and
// seed (StageHash), and where the union puts it.
func providerKey(tk *procgen.StageKind, seed uint64, pt *presetTerrain, pv *terrain.Provider) string {
	return fmt.Sprintf("%s|%v|%v|%g|%g|%g", procgen.StageHash(tk, seed, pt.params, nil), pv.Anchor, pv.Measure, pv.Extend,
		pv.Settings.Cell, pv.Settings.Size)
}

// stableTileBase snaps a tile plan's base down to a multiple of tileBaseSnap
// (at least 4 m under the lowest point) and recomputes the quantisation step
// from it, as planTiles does.
func stableTileBase(tp *tilePlan, g *terrain.Grid) {
	lo, hi := g.Range()
	tp.base = float32(math.Floor((float64(lo)-4)/tileBaseSnap) * tileBaseSnap)
	ext := max(float64(tp.size), float64(hi-tp.base))
	tp.quant = float32(math.Ldexp(1, int(math.Floor(math.Log2(ext)))-18))
}

// tileCentre is tile (ti, tj)'s centre in the world (what tileGridAt says).
func tileCentre(g *terrain.Grid, tp tilePlan, ti, tj int) procgen.Point3 {
	ts := float32(tp.size)
	return procgen.Point3{X: float32(g.MinX) + ts*(float32(ti)+0.5), Y: 0, Z: float32(g.MinZ) + ts*(float32(tj)+0.5)}
}

// tileInputHash is everything tile (ti, tj)'s file is made from (file
// comment).
func tileInputHash(rc *procgen.RunContext, g *terrain.Grid, tp tilePlan, ti, tj int, cook, meshOnly bool) string {
	p := rc.Params
	h := sha256.New()
	version := 0
	if rc.Kind != nil {
		version = rc.Kind.Version
	}
	fmt.Fprintf(h, "%s|v%d|%d,%d|%d|%d|%d|%g|%g|%g|%g|%g|%g|%d|%v|%v|%s|%s|%g\n", tileHashVersion, version, ti, tj,
		tp.count, tp.per, tp.step, tp.size, tp.base, tp.quant, g.Cell, g.MinX, g.MinZ, g.N, cook, meshOnly,
		p.Str("texture"), p.Str("uv_mode"), p.Float("uv_scale"))
	// The tile's samples and the ring its normals read (step samples each
	// way, clamped at the union's edge).
	i0, i1 := max(ti*tp.per-tp.step, 0), min((ti+1)*tp.per+tp.step, g.N)
	j0, j1 := max(tj*tp.per-tp.step, 0), min((tj+1)*tp.per+tp.step, g.N)
	var b [4]byte
	for j := j0; j <= j1; j++ {
		for i := i0; i <= i1; i++ {
			binary.LittleEndian.PutUint32(b[:], math.Float32bits(g.At(i, j)))
			h.Write(b[:])
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
