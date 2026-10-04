package cluster

import (
	"math"
	"math/rand"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// plainSample is a VoxelOptions.Sample for a mesh of x, y, z, nx, ny, nz
// records: the whole surface there, its interpolated normal, one attribute
// (the height) and binding 0.
func plainSample(m *Mesh) func(v [3]uint32, b [3]float32, size float32, out []float32) VoxelSample {
	return func(v [3]uint32, b [3]float32, _ float32, out []float32) VoxelSample {
		var n mgl32.Vec3
		var y float32
		for k := 0; k < 3; k++ {
			s := int(v[k]) * m.stride()
			n = n.Add(mgl32.Vec3{m.Positions[s+3], m.Positions[s+4], m.Positions[s+5]}.Mul(b[k]))
			y += m.Positions[s+1] * b[k]
		}
		if len(out) > 0 {
			out[0] = y
		}
		return VoxelSample{Weight: 1, Normal: n.Normalize()}
	}
}

// quadMesh is a w by w square at height y, facing up, two triangles.
func quadMesh(w, y float32) Mesh {
	return Mesh{Stride: 6, Positions: []float32{
		0, y, 0, 0, 1, 0,
		w, y, 0, 0, 1, 0,
		w, y, w, 0, 1, 0,
		0, y, w, 0, 1, 0,
	}, Indices: []uint32{0, 2, 1, 0, 3, 2}}
}

func totalArea(cs []VoxelCluster) (area [7]float64, weight float64) {
	for _, c := range cs {
		for _, v := range c.Voxels {
			for a := 0; a < 7; a++ {
				area[a] += float64(v.Area[a])
			}
			weight += float64(v.Weight)
		}
	}
	return
}

// A flat square fills the voxels it crosses wholly from above, its area is
// kept exactly, and its normals agree completely.
func TestVoxelizeAFlatSquareCoversItFromAbove(t *testing.T) {
	m := quadMesh(1, 0.05)
	opt := VoxelOptions{Attrs: 1, Sample: plainSample(&m)}
	const size = 0.125
	cs := Voxelize(m, m.Indices, opt, mgl32.Vec3{}, size)
	if len(cs) != 1 {
		t.Fatalf("%d clusters for a 2x2-brick square", len(cs))
	}
	c := cs[0]
	if len(c.Bricks) != 4 || len(c.Voxels) != 64 {
		t.Fatalf("%d bricks, %d voxels; want 4 and 64 (an 8x8 layer)", len(c.Bricks), len(c.Voxels))
	}
	area, weight := totalArea(cs)
	if math.Abs(area[1]-1) > 1e-4 || math.Abs(weight-1) > 1e-4 || area[0] > 1e-6 || area[2] > 1e-6 {
		t.Fatalf("projected areas %v, weight %v; want 0, 1, 0 and 1", area, weight)
	}
	// Along each diagonal it is the square foreshortened: 1/√3.
	for k := 3; k < 7; k++ {
		if math.Abs(area[k]-0.57735) > 1e-4 {
			t.Fatalf("projected area along diagonal %d is %v, want 1/√3", k, area[k])
		}
	}
	for i := range c.Voxels {
		v := &c.Voxels[i]
		if cv := v.Coverage(1, size); math.Abs(float64(cv)-1) > 1e-3 {
			t.Fatalf("voxel %d covers %v from above, want all of it", i, cv)
		}
		// Seen along a diagonal a third of a ray's way across the voxel
		// meets it: its shadow, 1/√3, over the cube's, √3.
		if cv := v.Coverage(3, size); math.Abs(float64(cv)-1.0/3) > 1e-3 {
			t.Fatalf("voxel %d covers %v along a diagonal, want a third", i, cv)
		}
		if v.Coverage(0, size) != 0 {
			t.Fatalf("voxel %d covers something edge on", i)
		}
		if n := v.MeanNormal(); n.Sub(mgl32.Vec3{0, 1, 0}).Len() > 1e-4 {
			t.Fatalf("voxel %d's mean normal is %v", i, n)
		}
		if y := c.Attrs[i]; math.Abs(float64(y)-0.05) > 1e-5 {
			t.Fatalf("voxel %d's attribute is %v, want the height 0.05", i, y)
		}
	}
}

// Coarsening is exact: every coarse voxel is its eight children summed, so
// the areas and weights of every level agree.
func TestCoarseningKeepsTheAreas(t *testing.T) {
	m := lumpySphereNormals(3)
	opt := VoxelOptions{Attrs: 1, Sample: plainSample(&m)}
	fine := Voxelize(m, m.Indices, opt, mgl32.Vec3{-2, -2, -2}, 1.0/32)
	a0, w0 := totalArea(fine)
	cur := fine
	for size := float32(1.0 / 16); size <= 1; size *= 2 {
		cur = Coarsen(cur, opt, size)
		a, w := totalArea(cur)
		for ax := 0; ax < 7; ax++ {
			if math.Abs(a[ax]-a0[ax]) > 1e-3*a0[ax] {
				t.Fatalf("at %v the area across %d is %v, was %v", size, ax, a[ax], a0[ax])
			}
		}
		if math.Abs(w-w0) > 1e-3*w0 {
			t.Fatalf("at %v the weight is %v, was %v", size, w, w0)
		}
		for _, c := range cur {
			if len(c.Bricks) > MaxClusterBricks {
				t.Fatalf("a cluster of %d bricks", len(c.Bricks))
			}
		}
	}
	// A sphere's surface area is about 4π; projected on each axis about π·2
	// (both sides).
	if math.Abs(w0-4*math.Pi) > 0.6 || math.Abs(a0[1]-2*math.Pi) > 0.4 {
		t.Fatalf("the sphere voxelized to weight %v and area %v", w0, a0)
	}
}

// A voxel whose surface faces every way has a short mean normal: the spread
// the renderer draws from.
func TestNormalsThatDisagreeMakeAShortMean(t *testing.T) {
	m := lumpySphereNormals(2)
	opt := VoxelOptions{Sample: plainSample(&m)}
	cs := Voxelize(m, m.Indices, opt, mgl32.Vec3{-2, -2, -2}, 4)
	if len(cs) != 1 || len(cs[0].Voxels) != 1 {
		t.Fatalf("the whole sphere in one 4-unit voxel gave %d clusters", len(cs))
	}
	if l := cs[0].Voxels[0].MeanNormal().Len(); l > 0.1 {
		t.Fatalf("a whole sphere's normals average to length %v", l)
	}
}

// A brick takes the binding most of its area has.
func TestABrickMovesWithItsLeadingBinding(t *testing.T) {
	m := quadMesh(0.5, 0.01)
	opt := VoxelOptions{Sample: func(v [3]uint32, b [3]float32, _ float32, out []float32) VoxelSample {
		bind := int32(3)
		if v[0] == 0 && v[1] == 2 && v[2] == 1 {
			// The first triangle is bone 5 at weight 0.5, the second bone 3
			// at weight 1 — and it is half the square too, so to break the
			// tie this one weighs less.
			return VoxelSample{Weight: 0.4, Normal: mgl32.Vec3{0, 1, 0}, Bind: 5, BindWeight: 0.5}
		}
		return VoxelSample{Weight: 1, Normal: mgl32.Vec3{0, 1, 0}, Bind: bind, BindWeight: 1}
	}}
	cs := Voxelize(m, m.Indices, opt, mgl32.Vec3{}, 0.25)
	if len(cs) != 1 || len(cs[0].Bricks) != 1 {
		t.Fatalf("want one brick")
	}
	if b := cs[0].Bricks[0]; b.Bind != 3 || b.BindWeight != 1 {
		t.Fatalf("the brick moves with %d at %v, want 3 at 1", b.Bind, b.BindWeight)
	}
}

// lumpySphereNormals is lumpySphere without UVs: x, y, z, nx, ny, nz.
func lumpySphereNormals(level int) Mesh {
	s := lumpySphere(level, false)
	n := s.VertexCount()
	pos := make([]float32, 0, n*6)
	for v := 0; v < n; v++ {
		r := s.Positions[v*s.Stride : v*s.Stride+s.Stride]
		pos = append(pos, r[0], r[1], r[2], r[5], r[6], r[7])
	}
	return Mesh{Positions: pos, Stride: 6, Indices: s.Indices}
}

// leafSoup is n disconnected unit cards scattered through a ball — a crown,
// which simplification can do nothing with.
func leafSoup(n int, seed int64) Mesh {
	r := rand.New(rand.NewSource(seed))
	var pos []float32
	var idx []uint32
	for i := 0; i < n; i++ {
		c := mgl32.Vec3{r.Float32()*4 - 2, r.Float32()*4 - 2, r.Float32()*4 - 2}
		if c.Len() > 2 {
			i--
			continue
		}
		a := mgl32.Vec3{r.Float32() - 0.5, r.Float32() - 0.5, r.Float32() - 0.5}.Normalize().Mul(0.15)
		b := mgl32.Vec3{r.Float32() - 0.5, r.Float32() - 0.5, r.Float32() - 0.5}.Normalize().Mul(0.15)
		nrm := a.Cross(b).Normalize()
		base := uint32(len(pos) / 6)
		for _, p := range []mgl32.Vec3{c.Sub(a).Sub(b), c.Add(a).Sub(b), c.Add(a).Add(b), c.Sub(a).Add(b)} {
			pos = append(pos, p[0], p[1], p[2], nrm[0], nrm[1], nrm[2])
		}
		idx = append(idx, base, base+1, base+2, base, base+2, base+3)
	}
	return Mesh{Positions: pos, Stride: 6, Indices: idx}
}

func voxelDAG(m Mesh) DAG {
	return BuildDAG(m, DAGOptions{Voxels: &VoxelOptions{Attrs: 1, Sample: plainSample(&m), RootBricks: 2}})
}

// checkMonotonic fails unless every node's error is at most its parent's and
// its LOD sphere inside its parent's.
func checkMonotonic(t *testing.T, d *DAG) {
	t.Helper()
	for i := range d.Clusters {
		c := &d.Clusters[i]
		if c.IsRoot() {
			continue
		}
		if c.ParentError < c.Error {
			t.Fatalf("cluster %d (level %d, kind %d): error %v under a parent's %v", i, c.Level, c.Kind, c.Error, c.ParentError)
		}
		if c.LODCenter.Sub(c.ParentCenter).Len()+c.LODRadius > c.ParentRadius*1.001+1e-5 {
			t.Fatalf("cluster %d's LOD sphere pokes out of its parent's", i)
		}
	}
}

// pathsCrossOnce checks a view cut: from a level-0 cluster up to the root,
// exactly one cluster is selected along every path (so the cut covers the
// mesh once, whatever its kinds).
func pathsCrossOnce(t *testing.T, d *DAG, eye mgl32.Vec3, k float32) (tris, bricks int) {
	t.Helper()
	made := map[int32][]int32{}
	for i := range d.Groups {
		made[int32(i)] = d.Groups[i].Made
	}
	sel := make([]bool, len(d.Clusters))
	for i := range d.Clusters {
		sel[i] = d.Clusters[i].SelectedFrom(eye, k)
		if sel[i] {
			if d.Clusters[i].Kind == KindVoxels {
				bricks += len(d.Voxels[d.Clusters[i].Voxel].Bricks)
			} else {
				tris += int(d.Clusters[i].TriCount)
			}
		}
	}
	for i := range d.Clusters {
		if d.Clusters[i].Level != 0 {
			continue
		}
		// Walk up: a cluster's parent group's made clusters all share one
		// test, so the first made one stands for the step.
		n, c := 0, int32(i)
		for {
			if sel[c] {
				n++
			}
			p := d.Clusters[c].Parent
			if p < 0 {
				break
			}
			c = made[p][0]
		}
		if n != 1 {
			t.Fatalf("eye %v: a path from cluster %d crosses the cut %d times", eye, i, n)
		}
	}
	return
}

// A crown of cards has nothing to simplify, so it turns to voxels from the
// first level on; every level errs no less than the one below; the root is a
// couple of bricks; and every view's cut covers the crown exactly once,
// costing fewer bricks the further off it is.
func TestACrownTurnsToVoxels(t *testing.T) {
	m := leafSoup(300, 7)
	d := voxelDAG(m)
	checkMonotonic(t, &d)
	var roots []int
	voxels := 0
	for i := range d.Clusters {
		c := &d.Clusters[i]
		if c.IsRoot() {
			roots = append(roots, i)
		}
		if c.Kind == KindVoxels {
			voxels++
			v := &d.Voxels[c.Voxel]
			if len(v.Bricks) > MaxClusterBricks || len(v.Bricks) == 0 {
				t.Fatalf("voxel cluster %d holds %d bricks", i, len(v.Bricks))
			}
			if c.TriCount != 0 {
				t.Fatalf("voxel cluster %d has triangles", i)
			}
		} else if c.Level > 0 {
			t.Fatalf("cluster %d at level %d is triangles: a card soup should not simplify", i, c.Level)
		}
	}
	if voxels == 0 {
		t.Fatal("no voxel clusters")
	}
	if len(roots) != 1 || d.Clusters[roots[0]].Kind != KindVoxels || len(d.Voxels[d.Clusters[roots[0]].Voxel].Bricks) > 2 {
		t.Fatalf("roots %v: want one voxel root of at most two bricks", roots)
	}
	prevTris, prevBricks := math.MaxInt, math.MaxInt
	for _, dist := range []float32{3, 10, 30, 100, 300, 1000, 3000, 10000} {
		eye := mgl32.Vec3{0, 0, dist}
		tris, bricks := pathsCrossOnce(t, &d, eye, 1000)
		if tris > prevTris || (tris == 0 && bricks > prevBricks) {
			t.Fatalf("at %v the cut is %d triangles and %d bricks, more than nearer (%d, %d)", dist, tris, bricks, prevTris, prevBricks)
		}
		if tris == 0 {
			prevBricks = bricks
		}
		prevTris = tris
	}
	if prevTris != 0 || prevBricks > 2 {
		t.Fatalf("far off the cut is %d triangles and %d bricks", prevTris, prevBricks)
	}
	if tris, _ := pathsCrossOnce(t, &d, mgl32.Vec3{0, 0, 2.5}, 1000); tris != 600 {
		t.Fatalf("up close the cut is %d triangles, want all 600", tris)
	}
}

// A closed lumpy sphere simplifies well, so it stays triangles for its first
// levels and turns to voxels only near the top; the root is voxels either
// way, and the cuts stay complete.
func TestASolidKeepsTrianglesUntilVoxelsErrLess(t *testing.T) {
	m := lumpySphereNormals(4) // 5120 triangles
	d := voxelDAG(m)
	checkMonotonic(t, &d)
	triLevels, firstVox := 0, -1
	for i := range d.Clusters {
		c := &d.Clusters[i]
		if c.Kind == KindTriangles && c.Level > 0 {
			triLevels = max(triLevels, int(c.Level))
		}
		if c.Kind == KindVoxels && (firstVox < 0 || int(c.Level) < firstVox) {
			firstVox = int(c.Level)
		}
		// Whenever voxels replaced a group, they erred less than the
		// triangles would have (or the triangles could not be made) —
		// which the build only checks; here, that the choice is by error
		// at all: no voxel node errs less than a voxel of its size.
		if c.Kind == KindVoxels && c.Error < VoxelError(d.Voxels[c.Voxel].Size)*0.999 {
			t.Fatalf("voxel cluster %d errs %v, under its voxels' %v", i, c.Error, VoxelError(d.Voxels[c.Voxel].Size))
		}
	}
	if triLevels < 2 || firstVox <= 1 {
		t.Fatalf("triangle levels up to %d, voxels from %d: a sphere should simplify first", triLevels, firstVox)
	}
	for _, dist := range []float32{2, 8, 40, 400} {
		pathsCrossOnce(t, &d, mgl32.Vec3{dist, 0, 0}, 1000)
	}
}

// A single small triangle cluster — a grass tuft — still gets a voxel
// level above it.
func TestALoneClusterStillCoarsens(t *testing.T) {
	m := leafSoup(10, 3)
	d := voxelDAG(m)
	checkMonotonic(t, &d)
	if d.Levels < 2 {
		t.Fatalf("%d levels for a lone cluster", d.Levels)
	}
	root := -1
	for i := range d.Clusters {
		if d.Clusters[i].IsRoot() {
			root = i
		}
	}
	if d.Clusters[root].Kind != KindVoxels {
		t.Fatal("the root is not voxels")
	}
}

// Base fixes level 0: each entry is one cluster, named by Base.
func TestDAGBaseIsLevelZero(t *testing.T) {
	m := leafSoup(100, 5)
	var base [][]uint32
	for c := 0; c < 4; c++ {
		var list []uint32
		for tr := c * 50; tr < (c+1)*50; tr++ {
			list = append(list, uint32(tr))
		}
		base = append(base, list)
	}
	d := BuildDAG(m, DAGOptions{Base: base, Voxels: &VoxelOptions{Attrs: 1, Sample: plainSample(&m)}})
	seen := map[int32]bool{}
	for i := range d.Clusters {
		c := &d.Clusters[i]
		if c.Level != 0 {
			if c.Base != -1 {
				t.Fatalf("level %d cluster names base %d", c.Level, c.Base)
			}
			continue
		}
		if c.Base < 0 || c.TriCount != 50 || seen[c.Base] {
			t.Fatalf("level-0 cluster %d: base %d, %d triangles", i, c.Base, c.TriCount)
		}
		seen[c.Base] = true
		for k := uint32(0); k < c.TriCount; k++ {
			if got := d.BaseOrder[c.TriFirst+k]; got != base[c.Base][k] {
				t.Fatalf("cluster %d's triangle %d is %d, want %d", i, k, got, base[c.Base][k])
			}
		}
	}
	if len(seen) != 4 {
		t.Fatalf("%d base clusters", len(seen))
	}
}

// Voxels change nothing for a build that does not ask for them: the DAG of
// a mesh is the same as before this file (the triangle-only tests above
// check its shape; this checks no voxel or base field leaks in).
func TestATriangleDAGHasNoVoxels(t *testing.T) {
	m := lumpySphere(3, true)
	d := BuildDAG(m, DAGOptions{})
	if len(d.Voxels) != 0 || d.VoxelUnit != 0 {
		t.Fatal("a triangle build made voxels")
	}
	for i := range d.Clusters {
		if c := &d.Clusters[i]; c.Kind != KindTriangles || c.Voxel != -1 || c.Base != -1 {
			t.Fatalf("cluster %d: kind %d, voxel %d, base %d", i, c.Kind, c.Voxel, c.Base)
		}
	}
}
