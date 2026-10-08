package render

// foliage_volumes.go — `kind: biome` scatter volumes, drawn as foliage.
//
// A lattice scatter volume is generated in its own vertex shader and never
// reaches the foliage renderer. A biome volume does: world.GrowVolumePlants
// places its species once, on the CPU, over the ground the volume's column
// finds, and the instances go into cells exactly as placed trees' do
// (foliage_placed.go). From the draw list on, nothing knows the difference —
// trees in a volume are Nanite foliage with wind bones, shadow proxies and
// GI like any other.
//
// # When a volume regrows
//
// Its key is world.VolumePlantsKey (where it is, what biome, how much)
// folded with the scatter height bake's own fingerprint of the geometry
// under it (scatterGeometry), so editing the floor regrows it and a ship
// flying past does not. Growing is thousands of ground casts, so a key that
// keeps moving — a volume being dragged in the editor — waits until it holds
// still for a frame. A volume that has never grown grows at once, so a
// level's first frame (and a headless shot) has its plants.
//
// A volume riding a frame grows in the frame's coordinates and draws through
// the frame's matrix; one riding an entity grows nothing, as with the
// lattice kinds (forEachScatterVolume).

import (
	"foundry/internal/biome"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// foliageVolume is one biome volume's cells.
type foliageVolume struct {
	foliagePlaced
	// pending is a key seen last frame that has not been grown yet.
	pending uint64
}

// foliageVolumes is every biome volume's cells, by volume ID.
type foliageVolumes struct {
	byID map[string]*foliageVolume
}

// sync regrows the volumes whose key settled on a new value, and puts what
// is in range on this frame's draw list.
func (fv *foliageVolumes) sync(lvl *world.Level, frame uint64, reach float32) {
	if fv.byID == nil {
		fv.byID = map[string]*foliageVolume{}
	}
	seen := map[string]bool{}
	for vi := range lvl.Volumes {
		v := &lvl.Volumes[vi]
		if !v.HasPlants() || v.Attach.Entity != "" || !v.HasBounds() {
			continue
		}
		seen[v.ID] = true
		p := fv.byID[v.ID]
		if p == nil {
			p = &foliageVolume{}
			fv.byID[v.ID] = p
		}
		if !v.Enabled || !v.Scatter.Enabled {
			// Kept, so a script switching it back on does not regrow it.
			continue
		}
		key := world.VolumePlantsKey(v) ^ scatterGeometry(lvl, v).full*1099511628211
		if key != p.fp && (p.fp == 0 || key == p.pending) {
			p.rebuild(growVolumePlants(lvl, v))
			p.fp = key
		}
		p.pending = key

		p.model = mgl32.Ident4()
		if f := lvl.Frames.Frame(v.Frame()); !f.IsWorld() {
			p.model = f.WorldMatrix()
		}
		p.draw(frame, reach)
	}
	for id, p := range fv.byID {
		if !seen[id] {
			p.release()
			delete(fv.byID, id)
		}
	}
}

// release gives every volume's instances back.
func (fv *foliageVolumes) release() {
	for id, p := range fv.byID {
		p.release()
		delete(fv.byID, id)
	}
}

// growVolumePlants places v's biome on the ground its column finds — the
// same column, in the same frame, the lattice kinds' height bake casts
// (scatterColumn, bakeScatterHeightRect).
func growVolumePlants(lvl *world.Level, v *world.Volume) []world.FoliageBatch {
	// A terrain rebuilds its surface lazily when cast against; do it here,
	// on this thread, before the casts spread over several.
	for ci := range lvl.Chunks {
		if tr := lvl.Chunks[ci].Terrain; tr != nil {
			tr.Sections()
		}
	}
	q := biome.NewGroundQuery(lvl, v.Frame())
	topY, drop := scatterColumn(v)
	return world.GrowVolumePlants(v, func(x, z float32) (mgl32.Vec3, mgl32.Vec3, bool) {
		s := q.SurfaceUnder(x, topY, z, drop)
		return mgl32.Vec3{x, s.Height, z}, s.Normal, s.Found
	})
}
