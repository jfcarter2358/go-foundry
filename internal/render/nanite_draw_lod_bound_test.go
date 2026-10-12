package render

// nanite_draw_lod_bound_test.go — the draw bound tightened by LOD
// (nanite_draw_lod_bound.go) must never fall below what the cull can pack:
// every tile holding a cluster the shaders' LOD test would take is counted,
// from any eye, at any scale. Checked against the shaders' test, mirrored
// here, on random DAG-shaped clusters.

import (
	"math"
	"math/rand"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// shaderLODWithin is nanite_cull_common.glsl's lodWithin.
func shaderLODWithin(eye, c mgl32.Vec3, r, e, k float32) bool {
	return e*k <= max(c.Sub(eye).Len()-r, lodNear)
}

// shaderSelects is nanite_cluster_common.glsl's lodSelects.
func shaderSelects(cl *gpuCluster, m mgl32.Mat4, s float32, eye mgl32.Vec3, k float32) bool {
	own := shaderLODWithin(eye, m.Mul4x1(mgl32.Vec4{cl.LOD[0], cl.LOD[1], cl.LOD[2], 1}).Vec3(), cl.LOD[3]*s, cl.Errs[0]*s, k)
	parentFine := cl.Errs[1] >= 0 &&
		shaderLODWithin(eye, m.Mul4x1(mgl32.Vec4{cl.Parent[0], cl.Parent[1], cl.Parent[2], 1}).Vec3(), cl.Parent[3]*s, cl.Errs[1]*s, k)
	return own && !parentFine
}

func TestDrawLODBoundCountsEveryTileTheCullCanDraw(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	b := &cullBatch{}
	// Two instances of a 40-cluster "DAG": errors growing with level, LOD
	// spheres within a few metres of the mesh's centre, the top level roots.
	var clusters []gpuCluster
	for c := 0; c < 40; c++ {
		level := float32(c / 8)
		e := 0.01 * float32(math.Pow(3, float64(level)))
		pe := e * 3
		if level == 4 {
			pe = -1
		}
		p := mgl32.Vec3{rng.Float32()*4 - 2, rng.Float32()*4 - 2, rng.Float32()*4 - 2}
		clusters = append(clusters, gpuCluster{
			LOD:    [4]float32{p[0], p[1], p[2], 0.5 + level},
			Parent: [4]float32{p[0] * 0.8, p[1] * 0.8, p[2] * 0.8, 1 + level},
			Errs:   [4]float32{e, pe, level, 0},
		})
	}
	b.cpuClusters = append(clusters, clusters...)
	slot := 0
	var tiles []gpuTile
	for inst := 0; inst < 2; inst++ {
		for first := 0; first < 40; first += 5 {
			tiles = appendRangeTile(tiles, uint32(inst*40+first), 5, uint32(inst), 0, &slot)
		}
	}
	for i := range tiles {
		tiles[i].B[3] = 0
	}
	b.tiles, b.groups, b.slots = tiles, []cullGroup{{0, int32(slot)}}, slot
	b.instSlotsDirty = true
	b.leaves = 1
	in := []bool{true, true}
	for trial := 0; trial < 400; trial++ {
		sc := 0.3 + 3*rng.Float32()
		m0 := mgl32.Translate3D(rng.Float32()*100-50, 0, rng.Float32()*100-50).Mul4(mgl32.Scale3D(sc, sc, sc))
		m1 := mgl32.Translate3D(rng.Float32()*400, 5, -200).Mul4(mgl32.HomogRotate3DY(rng.Float32() * 6))
		b.insts = []gpuInstance{placedInstance(m0, mgl32.Vec3{}, 4), placedInstance(m1, mgl32.Vec3{}, 4)}
		if trial%50 == 0 {
			b.instSlotsDirty = true // the keys are the mesh's: rebuilt, the same
		}
		eye := mgl32.Vec3{rng.Float32()*600 - 300, rng.Float32() * 100, rng.Float32()*600 - 300}
		k := 100 + rng.Float32()*3000
		b.setBoundLOD(naniteLODView{eye: eye, k: k}, true)
		b.boundDraws(in)
		need := int32(0)
		for _, tl := range tiles {
			inst := int(tl.A[2])
			m := mgl32.Mat4(b.insts[inst].Model)
			s := maxScaleOf(&b.insts[inst].Model)
			for ci := tl.A[0]; ci < tl.A[0]+tl.A[1]; ci++ {
				if shaderSelects(&b.cpuClusters[ci], m, s, eye, k) {
					need += rangeSlots
					break
				}
			}
		}
		if b.drawMax[0] < need {
			t.Fatalf("trial %d: bound %d below the %d slots the cut can draw", trial, b.drawMax[0], need)
		}
		if b.drawMax[0] > int32(slot) {
			t.Fatalf("trial %d: bound %d above every slot (%d)", trial, b.drawMax[0], slot)
		}
	}
	// And it does tighten: far away, only the roots' tiles are left.
	b.insts = []gpuInstance{placedInstance(mgl32.Ident4(), mgl32.Vec3{}, 4), placedInstance(mgl32.Ident4(), mgl32.Vec3{}, 4)}
	b.setBoundLOD(naniteLODView{eye: mgl32.Vec3{0, 0, 1e5}, k: 100}, true)
	b.boundDraws(in)
	if roots := int32(2 * 2 * rangeSlots); b.drawMax[0] != roots {
		t.Errorf("from 100 km the bound is %d slots, want the roots' %d", b.drawMax[0], roots)
	}
	b.setBoundLOD(naniteLODView{}, false)
	b.boundDraws(in)
	if b.drawMax[0] != int32(slot) {
		t.Errorf("with the LOD bound off: %d slots, want every one (%d)", b.drawMax[0], slot)
	}
}
