package stages

// ground_obstacles.go — what ground_edit placed on the ground, as footprints
// the placement stages keep out of (docs/terrain-paint/PHASE_W4.md).
//
// ground_edit places some rocks itself: W3's river boulders (dropped in
// the riffles, where their foam is known when the ribbon is meshed) and
// P4's path stones (flagstones, stepping stones, fords). They are not any
// scatter's footprints, so a scatter or a grass field over the edited
// ground used to drop a bush through a boulder, or a blade through a
// stepping stone's edge. Now ground_edit lists every rock it placed as a
// disc (x, z, radius) in terrain.yaml's `obstacles:` (terrain.Meta), and
// every scatter and grass_field whose terrain is that ground_edit adds
// them to its avoid list — whatever its own `avoid` stage, since there is
// no case for growing through a placed rock.
//
// A tiled (paint-reading) scatter's and every grass field's piece keys
// fold in the obstacles that reach the piece (obstacleKey), so a river
// edit that moves a boulder rebuilds the pieces round it. A terrain with
// no obstacles (every terrain before W4, and any without boulders or
// stones) adds nothing to a key: those pieces keep the keys they had.

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"

	"foundry/internal/procgen/place"
	"foundry/internal/procgen/terrain"
)

// terrainObstacles is the rocks a ground_edit terrain placed, as discs
// (nil for any other terrain).
func terrainObstacles(t *terrain.Terrain) []place.Disc {
	if t == nil || len(t.Meta.Obstacles) == 0 {
		return nil
	}
	out := make([]place.Disc, len(t.Meta.Obstacles))
	for i, o := range t.Meta.Obstacles {
		out[i] = place.Disc{X: o[0], Z: o[1], R: o[2]}
	}
	return out
}

// obstacleKey hashes the discs that reach rect grown by pad: "" when none
// do, so a piece far from every obstacle keeps its key.
func obstacleKey(ds []place.Disc, rect [4]float64, pad float64) string {
	h := sha256.New()
	n := 0
	var b [8]byte
	for _, d := range ds {
		r := d.R + pad
		if d.X+r < rect[0] || d.X-r > rect[2] || d.Z+r < rect[1] || d.Z-r > rect[3] {
			continue
		}
		for _, v := range [3]float64{d.X, d.Z, d.R} {
			binary.LittleEndian.PutUint64(b[:], math.Float64bits(v))
			h.Write(b[:])
		}
		n++
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("obstacles:%x", h.Sum(nil)[:12])
}

// obstacleDisc is one placed rock's footprint, rounded as terrain.yaml
// holds it.
func obstacleDisc(x, z, r float64) [3]float64 { return [3]float64{round2(x), round2(z), round2(r)} }
