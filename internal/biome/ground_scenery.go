package biome

// ground_scenery.go — the ground under a column, when the ground is a mesh.
//
// Brushes and block terrain were the only ground until the procedural
// generators: their terrain is cooked Nanite tiles placed as scenery
// (procgen's `terrain` stage), and a scatter volume or a biome volume over
// it found nothing to stand on. So GroundQuery also casts against every
// scenery placement that collides as its MESH — the collision cut the
// physics already resolves bodies against (meshcollide, a BVH over the DAG
// cut at a few centimetres) — which is the drawn surface to within that
// error and is exactly what a body would stand on.
//
// Only `collision: mesh` placements: a box or a set of hulls is a stand-in
// volume, and grass growing on the top of a rock's bounding box would float.
// Only placements in the world frame (scenery is world-space; a column in a
// ship's frame does not meet it). Only placements in the world at all: one
// whose chunk is switched off is not there to stand on.
//
// The collision mesh is decoded (meshcollide.NewResident) on the first
// column that reaches a placement's box, once per mesh shared by every
// placement of it, and dropped with the query. A column that misses the
// box never pays for it, so a query over one corner of a kilometre of tiles
// decodes the few tiles under it. The decode is guarded per mesh, so the
// columns of a parallel bake may race to it safely.

import (
	"sync"

	"foundry/internal/meshcollide"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// groundScenery is one mesh-collision placement.
type groundScenery struct {
	lo, hi  mgl32.Vec3 // world box
	inv     mgl32.Mat4 // world -> mesh
	normalM mgl32.Mat3 // mesh normals -> world (inverse transpose)
	mesh    *groundMesh
}

// groundMesh is a collision mesh decoded on demand.
type groundMesh struct {
	data *meshcollide.Data
	once sync.Once
	res  *meshcollide.Resident
}

func (m *groundMesh) resident() *meshcollide.Resident {
	m.once.Do(func() {
		d, err := m.data.WithMesh()
		if err != nil || d.TriangleCount() == 0 || len(d.Nodes) == 0 {
			return
		}
		m.res = meshcollide.NewResident(d)
	})
	return m.res
}

// addScenery gathers lvl's mesh-collision placements for a world-frame query.
func (q *GroundQuery) addScenery(lvl *world.Level) {
	meshes := map[*meshcollide.Data]*groundMesh{}
	for i := range lvl.Scenery {
		s := &lvl.Scenery[i]
		if !s.InWorld() || s.CollisionMode() != meshcollide.ModeMesh {
			continue
		}
		d := s.CollisionData()
		lo, hi, ok := s.CollisionAABB()
		if d == nil || !ok {
			continue
		}
		model := s.ModelMatrix()
		inv := model.Inv()
		if inv == (mgl32.Mat4{}) {
			continue
		}
		gm := meshes[d]
		if gm == nil {
			gm = &groundMesh{data: d}
			meshes[d] = gm
		}
		q.scenery = append(q.scenery, groundScenery{lo: lo, hi: hi, inv: inv, normalM: inv.Mat3().Transpose(), mesh: gm})
	}
}

// sceneryUnder is the nearest upward-facing mesh-collision surface the
// column from origin meets within maxDrop: its distance and world normal.
func (q *GroundQuery) sceneryUnder(origin mgl32.Vec3, maxDrop float32) (float32, mgl32.Vec3, bool) {
	best, bestN, found := maxDrop, mgl32.Vec3{}, false
	down := mgl32.Vec3{0, -1, 0}
	for i := range q.scenery {
		gs := &q.scenery[i]
		if origin.X() < gs.lo.X() || origin.X() > gs.hi.X() || origin.Z() < gs.lo.Z() || origin.Z() > gs.hi.Z() ||
			gs.lo.Y() > origin.Y() || gs.hi.Y() < origin.Y()-best {
			continue
		}
		r := gs.mesh.resident()
		if r == nil {
			continue
		}
		// Into the mesh's space: an affine inverse keeps the ray parameter,
		// so t below is world distance (meshcollide/bvh.go).
		o4 := gs.inv.Mul4x1(origin.Vec4(1))
		d4 := gs.inv.Mul4x1(down.Vec4(0))
		o, d := o4.Vec3(), d4.Vec3()
		gone := float32(0)
		for k := 0; k < 8; k++ {
			t, tri, ok := r.Raycast(o, d, best-gone)
			if !ok {
				break
			}
			n := gs.normalM.Mul3x1(r.Normals[tri])
			if n.Y() > 0 {
				best, bestN, found = gone+t, n.Normalize(), true
				break
			}
			// An overhang's underside: carry on to the floor below it.
			const past = 1e-3
			gone += t + past
			o = o.Add(d.Mul(t + past))
		}
	}
	return best, bestN, found
}
