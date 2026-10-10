package render

import (
	"math/bits"
	"testing"
	"time"

	"foundry/internal/cluster"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

var nfTestAssets *nfAssetData

// testAssets builds every variant once per test binary, with the real atlas.
func testAssets(t *testing.T) *nfAssetData {
	t.Helper()
	if nfTestAssets == nil {
		start := time.Now()
		a := buildNaniteFoliageAssets(nfAtlas{chain: buildFoliageAtlas()})
		t.Logf("built %d variants in %v: %d clusters, %d parts, %d bricks, %d voxels, %d bones",
			len(a.buckets), time.Since(start), len(a.clusters), len(a.parts), len(a.bricks), len(a.voxels), len(a.bones))
		nfTestAssets = &a
	}
	return nfTestAssets
}

// Everything the packed asset points at is inside it, and every voxel a
// brick's mask claims is there.
func TestTheFoliageAssetIsConsistent(t *testing.T) {
	a := testAssets(t)
	nv := len(a.verts) / nfVertexFloats
	if len(a.buckets) != nfBucketCount() {
		t.Fatalf("%d buckets for %d variants", len(a.buckets), nfBucketCount())
	}
	for bi, b := range a.buckets {
		roots := 0
		for ci := b.firstCluster; ci < b.firstCluster+b.clusters; ci++ {
			c := &a.clusters[ci]
			if c.bucket != bi {
				t.Fatalf("cluster %d is bucket %d's, in bucket %d", ci, c.bucket, bi)
			}
			if c.perr < 0 && c.kind != nfKindProxy {
				roots++
			}
			if c.perr >= 0 && c.perr < c.err {
				t.Fatalf("cluster %d errs %v under its parent's %v", ci, c.err, c.perr)
			}
			switch c.kind {
			case nfKindTriangles, nfKindParts, nfKindProxy:
				if c.firstIndex+c.indexCount > len(a.idx) || c.indexCount == 0 {
					t.Fatalf("cluster %d's indices run past the buffer", ci)
				}
				for _, i := range a.idx[c.firstIndex : c.firstIndex+c.indexCount] {
					if c.baseVertex+int(i) >= nv {
						t.Fatalf("cluster %d indexes vertex %d of %d", ci, c.baseVertex+int(i), nv)
					}
				}
				if c.kind == nfKindParts && (c.parts == 0 || c.parts > nfPartsPerCluster || c.firstPart+c.parts > len(a.parts) || c.mult != c.parts || c.level != 0) {
					t.Fatalf("part cluster %d: %d parts from %d", ci, c.parts, c.firstPart)
				}
			case nfKindVoxels:
				if c.bricks == 0 || c.bricks > cluster.MaxClusterBricks || c.firstBrick+c.bricks > len(a.bricks) || c.mult != c.bricks {
					t.Fatalf("voxel cluster %d: %d bricks", ci, c.bricks)
				}
				for k := c.firstBrick; k < c.firstBrick+c.bricks; k++ {
					br := &a.bricks[k]
					n := bits.OnesCount32(br.Mask[0]) + bits.OnesCount32(br.Mask[1])
					if n == 0 || int(br.First)+n > len(a.voxels) {
						t.Fatalf("brick %d claims %d voxels from %d of %d", k, n, br.First, len(a.voxels))
					}
					if bone := int(br.Bind & 0xffff); bone >= b.bones {
						t.Fatalf("brick %d rides bone %d of %d", k, bone, b.bones)
					}
					if br.Corner[3] != c.voxelSize {
						t.Fatalf("brick %d's voxels are %v, its cluster's %v", k, br.Corner[3], c.voxelSize)
					}
				}
			}
		}
		if roots != 1 {
			t.Fatalf("bucket %d has %d roots", bi, roots)
		}
	}
	for i, p := range a.parts {
		if bone := int(p.base[3]); bone < 0 || bone >= a.maxBones {
			t.Fatalf("part %d rides bone %d", i, bone)
		}
	}
}

// A tree is parts and triangles up close and voxels from a distance, and
// its root is a brick or two: the far tier that replaces the impostor.
func TestATreeEndsInVoxels(t *testing.T) {
	a := testAssets(t)
	for _, s := range []world.FoliageShape{world.FoliageBroadleaf, world.FoliageConifer, world.FoliagePalm, world.FoliageAcacia} {
		b := a.buckets[nfBucketOf(s, 0)]
		var root *nfCluster
		for ci := b.firstCluster; ci < b.firstCluster+b.clusters; ci++ {
			if a.clusters[ci].perr < 0 && a.clusters[ci].kind != nfKindProxy {
				root = &a.clusters[ci]
			}
		}
		if b.voxClusters == 0 || root.kind != nfKindVoxels || root.bricks > 2 {
			t.Errorf("%v: %d voxel clusters, root kind %d of %d bricks", s, b.voxClusters, root.kind, root.bricks)
		}
		if (s == world.FoliageBroadleaf || s == world.FoliageConifer) && b.partClusters == 0 {
			t.Errorf("%v has no part clusters", s)
		}
		t.Logf("%v: %d levels, %d triangle, %d part, %d voxel clusters, radius %.2f",
			s, b.levels, b.triClusters, b.partClusters, b.voxClusters, b.radius)
	}
}

// nfSelect is the cut an eye at eye picks for bucket b at unit scale (k as
// cluster.LODWithin's).
func nfSelect(a *nfAssetData, b nfBucket, eye mgl32.Vec3, k float32) []int {
	var out []int
	for ci := b.firstCluster; ci < b.firstCluster+b.clusters; ci++ {
		c := &a.clusters[ci]
		if c.kind == nfKindProxy {
			continue
		}
		if !cluster.LODWithin(c.err, c.lod.Vec3(), c.lod[3], eye, k) {
			continue
		}
		if c.perr >= 0 && cluster.LODWithin(c.perr, c.parent.Vec3(), c.parent[3], eye, k) {
			continue
		}
		out = append(out, ci)
	}
	return out
}

// Pulled away, a tree's cut costs ever less: all of it up close, voxels
// further off, one brick at the horizon.
func TestATreeCostsByItsDistance(t *testing.T) {
	a := testAssets(t)
	b := a.buckets[nfBucketOf(world.FoliageBroadleaf, 1)]
	// k for a 900-pixel-high view, 60° tall, one pixel.
	k := float32(900 / (2 * 0.57735))
	prevBricks := 1 << 30
	sawTri, sawVox := false, false
	for _, d := range []float32{5, 20, 50, 100, 200, 400, 800, 1600, 3200} {
		sel := nfSelect(a, b, mgl32.Vec3{0, 4, d}, k)
		if len(sel) == 0 {
			t.Fatalf("nothing selected at %v", d)
		}
		bricks, tris := 0, 0
		for _, ci := range sel {
			c := &a.clusters[ci]
			if c.kind == nfKindVoxels {
				bricks += c.bricks
			} else {
				tris += c.indexCount / 3 * c.mult
			}
		}
		if tris > 0 {
			sawTri = true
		}
		if tris == 0 {
			sawVox = true
			if bricks > prevBricks {
				t.Fatalf("at %v the cut is %d bricks, more than nearer (%d)", d, bricks, prevBricks)
			}
			prevBricks = bricks
		}
		t.Logf("at %4.0f: %d triangles, %d bricks", d, tris, bricks)
	}
	if !sawTri || !sawVox || prevBricks > 2 {
		t.Fatalf("triangles seen %v, voxels %v, %d bricks at the horizon", sawTri, sawVox, prevBricks)
	}
}

// TestNaniteFoliageFlushesOnlyOnD3D12: the glFlush that keeps Mesa's D3D12
// driver from hanging (nfFlushD3D12) is that driver's alone unless forced.
func TestNaniteFoliageFlushesOnlyOnD3D12(t *testing.T) {
	t.Setenv("FOUNDRY_NANITE_FOLIAGE_FLUSH", "")
	if !nfFlushD3D12("D3D12 (NVIDIA GeForce GTX 1070)") {
		t.Error("no flush on Mesa's D3D12 driver")
	}
	if nfFlushD3D12("NVIDIA GeForce GTX 1070/PCIe/SSE2") {
		t.Error("a flush on a native driver")
	}
	t.Setenv("FOUNDRY_NANITE_FOLIAGE_FLUSH", "0")
	if nfFlushD3D12("D3D12 (NVIDIA GeForce GTX 1070)") {
		t.Error("FOUNDRY_NANITE_FOLIAGE_FLUSH=0 did not turn it off")
	}
	t.Setenv("FOUNDRY_NANITE_FOLIAGE_FLUSH", "1")
	if !nfFlushD3D12("NVIDIA GeForce GTX 1070/PCIe/SSE2") {
		t.Error("FOUNDRY_NANITE_FOLIAGE_FLUSH=1 did not turn it on")
	}
}
