package terrain

// edit.go — what a stage that EDITS a finished terrain's heights needs to
// hand on a terrain downstream stages can read as if a terrain stage had
// made it (the `ground_edit` stage, stages/ground_edit*.go;
// docs/terrain-paint/PHASE_W2.md):
//
//   - QuantizeHeights / QuantizeUnit: the values exactly as the files they
//     are written to give them back (heightmap.png's u16 over lo..hi, a
//     mask's 8 bits), so the in-memory terrain IS the one Load rebuilds.
//     A downstream piece made against either is the same piece, which is
//     what lets a stroke rerun only the pieces it reached.
//   - SurfaceMasks: the slope and curvature masks recomputed over a window
//     of samples, by computeMasks' own formulas, so the rest of the mask
//     stays bit for bit what the terrain stage wrote.
//   - GroundEditMeta (terrain.yaml's `ground_edit:`) and RectKey: an edited
//     terrain answers, for any rectangle, a key of the heights in it, which
//     the run engine folds into a downstream piece's paint key
//     (procgen.RectKeyer). A terrain no stage edited answers "" — and a
//     terrain.yaml without the section reads exactly as before.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
)

// GroundEditMeta is an edited terrain's record in terrain.yaml.
type GroundEditMeta struct {
	// Source is the stage whose terrain was edited; Samples how many
	// samples the edits moved; Tiles the tiles recooked.
	Source  string `yaml:"source"`
	Samples int    `yaml:"samples"`
	Tiles   int    `yaml:"tiles"`
}

// QuantizeHeights rounds h in place to what EncodeGray16 over lo..hi and
// Load's decode give back.
func QuantizeHeights(h []float32, lo, hi float32) {
	span := hi - lo
	if span == 0 {
		span = 1
	}
	dspan := hi - lo
	for k, v := range h {
		t := min(max((v-lo)/span, 0), 1)
		u := uint16(float64(t)*65535 + 0.5)
		h[k] = lo + (float32(u)/65535)*dspan
	}
}

// QuantizeUnit rounds 0..1 values in place to what EncodeGray8 and a mask
// read give back.
func QuantizeUnit(v []float32) {
	for k, x := range v {
		v[k] = float32(uint8(min(max(x, 0), 1)*255+0.5)) / 255
	}
}

// SurfaceMasks recomputes the slope and curvature masks of g over the
// samples [i0, i1] × [j0, j1] (clamped), exactly as the terrain stage's
// computeMasks does.
func SurfaceMasks(g *Grid, slope, curv []float32, i0, j0, i1, j1 int) {
	sz := g.S()
	i0, j0, i1, j1 = max(i0, 0), max(j0, 0), min(i1, sz-1), min(j1, sz-1)
	cell := float32(g.Cell)
	cr := max(int(math.Round(2/g.Cell)), 1)
	cspan := float32(cr) * cell
	for j := j0; j <= j1; j++ {
		for i := i0; i <= i1; i++ {
			k := j*sz + i
			il, ir := max(i-1, 0), min(i+1, sz-1)
			jl, jr := max(j-1, 0), min(j+1, sz-1)
			gx := (g.H[j*sz+ir] - g.H[j*sz+il]) / (cell * float32(ir-il))
			gz := (g.H[jr*sz+i] - g.H[jl*sz+i]) / (cell * float32(jr-jl))
			ang := math.Atan(math.Hypot(float64(gx), float64(gz))) * 180 / math.Pi
			slope[k] = float32(ang / 90)
			lap := (g.At(i+cr, j) + g.At(i-cr, j) + g.At(i, j+cr) + g.At(i, j-cr) - 4*g.H[k]) / (cspan * cspan)
			curv[k] = float32(0.5 + 0.5*math.Tanh(float64(lap)*3))
		}
	}
}

// CurvatureReach is how many samples beyond a moved one SurfaceMasks must
// cover for the curvature (the Laplacian's arm) and the slope (one).
func CurvatureReach(g *Grid) int { return max(int(math.Round(2/g.Cell)), 1) + 1 }

// RectKey is a key of what an edit can change within the world rectangle,
// or "" for a terrain no stage edited: what a downstream piece reading this
// terrain adds to its paint key (procgen.RectKeyer). It covers the heights
// and the water mask (a pinned level moves the water without always moving
// the ground) out to the curvature stencil's reach and a sample beyond, so
// the slope and curvature masks there, which an edit recomputes from those
// heights, are covered too; the rest of the masks an edit never changes —
// except that one with rivers rewrites the flow mask and adds the river
// masks (the flow map among them), and one with paths adds the path masks,
// which are then covered as well.
func (t *Terrain) RectKey(minX, minZ, maxX, maxZ float64) string {
	if t == nil || t.Meta.GroundEdit == nil || t.Grid == nil {
		return ""
	}
	g := t.Grid
	sz := g.S()
	m := 2*CurvatureReach(g) + 1
	i0 := max(int(math.Floor((minX-g.MinX)/g.Cell))-m, 0)
	j0 := max(int(math.Floor((minZ-g.MinZ)/g.Cell))-m, 0)
	i1 := min(int(math.Ceil((maxX-g.MinX)/g.Cell))+m, sz-1)
	j1 := min(int(math.Ceil((maxZ-g.MinZ)/g.Cell))+m, sz-1)
	water, _ := t.Mask(MaskWater)
	// A ground_edit with rivers also rewrote the flow mask and wrote the
	// river masks (river_meta.go): a downstream piece reading any of them
	// is keyed by them too. Without rivers the key is what it was.
	var extra [][]float32
	if t.Meta.Rivers != nil {
		for _, name := range append([]string{MaskFlow}, RiverMaskNames...) {
			if m, err := t.Mask(name); err == nil && m != nil {
				extra = append(extra, m)
			}
		}
	}
	// So with paths (phase P4): their surface, wear, clear and direction
	// masks (path.go).
	if t.Meta.Footpaths != nil {
		for _, name := range PathMaskNames {
			if m, err := t.Mask(name); err == nil && m != nil {
				extra = append(extra, m)
			}
		}
	}
	h := sha256.New()
	var b [4]byte
	for j := j0; j <= j1; j++ {
		for i := i0; i <= i1; i++ {
			k := j*sz + i
			binary.LittleEndian.PutUint32(b[:], math.Float32bits(g.H[k]))
			h.Write(b[:])
			if water != nil {
				binary.LittleEndian.PutUint32(b[:], math.Float32bits(water[k]))
				h.Write(b[:])
			}
			for _, m := range extra {
				binary.LittleEndian.PutUint32(b[:], math.Float32bits(m[k]))
				h.Write(b[:])
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}
