package stages

// paint_weight_cache.go — the material stages' per-stroke reruns
// (docs/terrain-paint/PHASE_P2.md; the engine side is procgen/incremental.go
// and procgen/paint_incremental.go).
//
// terrain_material and region_material work out a splat weight per terrain
// sample in two steps: the RULES (bands, noise, covers, the regions' blend —
// the expensive part, noise at every sample) and then their PAINT list
// (terrain_material_paint.go), which is pointwise: a sample's painted weights
// are a function of its unpainted weights and the paint at that sample, and
// of nothing else. So a stroke changes nothing the rules made, and the only
// work it needs is the paint step and the maps after it.
//
// When the stage's own params name a layer, the unpainted weights are kept
// in the stage's folder as prepaint.bin (float32s exactly as the rules left
// them, with the sample and channel counts), keyed by the stage's base hash
// and the whole hash of any layer only an upstream stage reads (an upstream
// stage's output may follow paint anywhere — W2's ground_edit will). A rerun
// whose key matches reads them back instead of running the rules, then
// applies the paint and writes the maps as a run from nothing does, from the
// same floats: the output is identical by construction.
//
// What still runs whole after a stroke: the paint step (pointwise, cheap
// beside the rules), the weights and ground maps and their PNG encodes and,
// for region_material, the far weights and per-tile materials. The height
// map and strata ramp do not follow paint: when the unpainted weights were
// reused they are kept as last run wrote them (keepUnpainted). The weights are
// ONE texture over the terrain, so "rewriting a tile's weights" in the file
// is rewriting the file; the editor re-uploads the texture whole.
//
// A stage that reads no paint never writes the cache, so its outputs are the
// files they always were.
//
// A painted material stage also hands its readers (a scatter's ground) the
// artifact as its manifest file reads back (artifactAsLoaded), so a stroke
// that reruns it does not make them rebuild every piece (procgen's
// upstreamLineage).

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"

	"foundry/internal/procgen"

	"gopkg.in/yaml.v2"
)

// prepaintFile is the cache's file, and its tile name in the manifest.
const (
	prepaintFile    = "prepaint.bin"
	prepaintTile    = "prepaint"
	prepaintVersion = "prepaint-1"
)

var prepaintMagic = []byte("FPPW1\x00")

// cachedWeights is build()'s weights (n samples × nc channels), read from
// the cache when the key matches (reused true) and written to it otherwise;
// build alone when the stage reads no paint of its own.
func cachedWeights(rc *procgen.RunContext, build func() ([]float32, int, error)) (w []float32, nc int, reused bool, err error) {
	if !rc.ReadsOwnPaint() {
		w, nc, err = build()
		return w, nc, false, err
	}
	key := rc.PieceKey(prepaintVersion, rc.UpstreamPaintHash())
	if prev, ok := rc.PreviousTile(prepaintTile); ok && prev == key {
		if data, err := os.ReadFile(filepath.Join(rc.Dir, prepaintFile)); err == nil {
			if w, nc, err := decodePrepaint(data); err == nil && rc.ReuseTile(prepaintTile, key, prepaintFile) {
				rc.Logf("unpainted weights reused (only the paint changed)")
				return w, nc, true, nil
			}
		}
	}
	if _, had := rc.PreviousTile(prepaintTile); had {
		rc.Logf("unpainted weights rebuilt (something besides the stage's own paint changed)")
	}
	rc.BeginTile(prepaintTile)
	if w, nc, err = build(); err != nil {
		return nil, 0, false, err
	}
	if err := rc.WriteFile(prepaintFile, encodePrepaint(w, nc)); err != nil {
		return nil, 0, false, err
	}
	rc.SetTile(prepaintTile, key)
	return w, nc, false, nil
}

// keepUnpainted reports whether file, one of the maps that do not follow
// paint (the height map, the strata ramp), can be kept as last run wrote
// it: the unpainted weights were reused, so everything it is made from is
// what it was, and the file is still there. It records the file as this
// run's output when so.
func keepUnpainted(rc *procgen.RunContext, reused bool, file string) bool {
	if !reused {
		return false
	}
	if _, err := os.Stat(filepath.Join(rc.Dir, file)); err != nil {
		return false
	}
	rc.Path(file)
	return true
}

func encodePrepaint(w []float32, nc int) []byte {
	out := make([]byte, 0, len(prepaintMagic)+8+4*len(w))
	out = append(out, prepaintMagic...)
	out = binary.LittleEndian.AppendUint32(out, uint32(nc))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(w)))
	for _, v := range w {
		out = binary.LittleEndian.AppendUint32(out, math.Float32bits(v))
	}
	return out
}

func decodePrepaint(data []byte) ([]float32, int, error) {
	if !bytes.HasPrefix(data, prepaintMagic) || len(data) < len(prepaintMagic)+8 {
		return nil, 0, errors.New("not a prepaint cache")
	}
	data = data[len(prepaintMagic):]
	nc := int(binary.LittleEndian.Uint32(data))
	n := int(binary.LittleEndian.Uint32(data[4:]))
	data = data[8:]
	if nc <= 0 || len(data) != 4*n {
		return nil, 0, errors.New("prepaint cache: wrong size")
	}
	w := make([]float32, n)
	for i := range w {
		w[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[4*i:]))
	}
	return w, nc, nil
}

// artifactAsLoaded replaces a material stage's artifact with data (the
// manifest it just wrote) read back, and says so, when the stage reads its
// own paint; nothing otherwise, so an unpainted stage's readers see what
// they always did.
func artifactAsLoaded(rc *procgen.RunContext, data []byte) {
	if !rc.ReadsOwnPaint() {
		return
	}
	var a TerrainMaterialArtifact
	if yaml.UnmarshalStrict(data, &a) == nil {
		rc.Artifact, rc.ArtifactAsLoaded = &a, true
	}
}
