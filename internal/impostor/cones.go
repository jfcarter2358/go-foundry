package impostor

// cones.go — which cards are drawn from where.
//
// Eight cards in a star, all drawn, are three or four silhouettes of the tree
// over one another from any side: a crown far denser than the mesh it stands
// for (the first bake's distant firs read as dark solid cones where the
// scans are wisps). So each card is drawn only from the views near its own:
// its cluster's normal cone — the GPU cull's back-face test
// (nanite_cluster_common.glsl, clusterDraws: culled when
// dot(apex - eye, axis) >= cutoff·|apex - eye|) — is set to cull it unless
// the eye is within SideAngle of its normal (TopAngle for the top card).
// With eight cards 45° apart and SideAngle 35°, one card is drawn from most
// directions and two across a 10° overlap; the top card takes over from
// views 25° or more above the horizon, where the side cards' windows close
// (a side card's window narrows as the eye rises: cos(elevation)·cos(azimuth)
// must reach cos(SideAngle)).
//
// The camera's passes test cones on every placement with an even scale and
// no mirror; shadow passes test them only on closed meshes, which cards are
// not — so every card casts, and the shadow is the union of the silhouettes.

import (
	"math"

	"foundry/internal/nanite"
)

// SetViewCones sets the cooked cards' cluster cones (m, cooked from r.Mesh)
// to the view windows above.
func SetViewCones(m *nanite.Mesh, r *Result) {
	for si := range m.Slots {
		if si >= len(r.cards) {
			break
		}
		cd := r.cards[si]
		half := r.opt.SideAngle
		if cd.n[1] > 0.9 {
			half = r.opt.TopAngle
		}
		centre := cd.origin.Add(cd.right.Mul(0.5)).Add(cd.down.Mul(0.5))
		sl := &m.Slots[si]
		for ci := sl.First; ci < sl.First+sl.Count && int(ci) < len(m.Clusters); ci++ {
			c := &m.Clusters[ci]
			c.ConeAxis = cd.n
			c.ConeApex = centre
			c.ConeCutoff = -float32(math.Cos(float64(half) * math.Pi / 180))
		}
	}
}
