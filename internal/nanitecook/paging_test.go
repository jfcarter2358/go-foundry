package nanitecook

// Phase 3's cooked format and residency (docs/nanite/PHASE_3.md): the pages
// a cook writes, reading them back one at a time from the file, and the cut
// any set of resident pages allows.

import (
	"bytes"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"foundry/internal/cluster"
	"foundry/internal/meshcollide"
	"foundry/internal/nanite"

	"github.com/go-gl/mathgl/mgl32"
)

// cookPagedRock cooks the two-slot sample rock at level into a file and
// returns the cook's mesh and the file's path.
func cookPagedRock(t *testing.T, level int) (*nanite.Mesh, string) {
	t.Helper()
	dir := t.TempDir()
	res, err := CookFile(writeSampleGLB(t, dir, level), "", true, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return res.Mesh, res.Out
}

// The pages: within the limits, clusters in order, never splitting a leaf of
// the tree (a range tile of the cull) or a group's input, root clusters
// alone on root pages, and each page's parents holding what its groups made.
func TestPagesFollowTheDAG(t *testing.T) {
	m, _ := cookPagedRock(t, 5)
	if len(m.Pages) < 4 {
		t.Fatalf("only %d pages", len(m.Pages))
	}
	pageOf := make([]int32, len(m.Clusters))
	for pi, p := range m.Pages {
		if p.Vertices > nanite.PageMaxVertices || p.Triangles > nanite.PageMaxTriangles {
			t.Fatalf("page %d: %d vertices, %d triangles", pi, p.Vertices, p.Triangles)
		}
		for c := p.ClusterFirst; c < p.ClusterFirst+p.ClusterCount; c++ {
			pageOf[c] = int32(pi)
			if p.Root != m.Clusters[c].IsRoot() {
				t.Fatalf("page %d (root %v) holds cluster %d (root %v)", pi, p.Root, c, m.Clusters[c].IsRoot())
			}
		}
		if m.PageOf(p.ClusterFirst) != int32(pi) || m.PageOf(p.ClusterFirst+p.ClusterCount-1) != int32(pi) {
			t.Fatalf("PageOf disagrees about page %d", pi)
		}
	}
	for _, l := range cluster.LeafRanges(m.Nodes) {
		if pageOf[l[0]] != pageOf[l[0]+l[1]-1] {
			t.Fatalf("leaf at cluster %d straddles pages %d and %d", l[0], pageOf[l[0]], pageOf[l[0]+l[1]-1])
		}
	}
	// The renderer's windows (cluster.RangeWindows per slot) never straddle
	// two pages, but where a run of root leaves starts (the cook keeps roots
	// on pages of their own): that is a handful a mesh, against the 12% more
	// windows that packing by groups alone cost.
	straddle, windows := 0, 0
	for _, sl := range m.Slots {
		var in [][2]int32
		for _, l := range cluster.LeafRanges(m.Nodes) {
			if l[0] >= sl.First && l[0] < sl.First+sl.Count {
				in = append(in, l)
			}
		}
		for _, w := range cluster.RangeWindows(in, nil) {
			windows++
			if pageOf[w[0]] != pageOf[w[0]+w[1]-1] {
				straddle++
				if m.Clusters[w[0]].IsRoot() == m.Clusters[w[0]+w[1]-1].IsRoot() {
					t.Fatalf("window at cluster %d straddles pages %d and %d", w[0], pageOf[w[0]], pageOf[w[0]+w[1]-1])
				}
			}
		}
	}
	if straddle > int(m.Levels)*len(m.Slots) {
		t.Fatalf("%d of %d windows straddle a page", straddle, windows)
	}
	// A group's input is split only where a page ends: across two
	// neighbouring pages, at most once a page. The page that holds it lists
	// the pages of what the group made as parents.
	type key struct{ slot, g int32 }
	slotOf := func(c int) int32 {
		for si, s := range m.Slots {
			if int32(c) >= s.First && int32(c) < s.First+s.Count {
				return int32(si)
			}
		}
		return -1
	}
	inputPages := map[key][]int32{}
	madePages := map[key][]int32{}
	for ci := range m.Clusters {
		c := &m.Clusters[ci]
		if c.Parent >= 0 {
			k := key{slotOf(ci), c.Parent}
			if !slices.Contains(inputPages[k], pageOf[ci]) {
				inputPages[k] = append(inputPages[k], pageOf[ci])
			}
		}
		if c.Group >= 0 {
			k := key{slotOf(ci), c.Group}
			madePages[k] = append(madePages[k], pageOf[ci])
		}
	}
	split := 0
	for k, ps := range inputPages {
		if len(ps) > 1 {
			split++
		}
		for _, p := range ps {
			for _, mp := range madePages[k] {
				if !slices.Contains(ps, mp) && !slices.Contains(m.Pages[p].Parents, mp) {
					t.Fatalf("page %d holds group %v's input but not its output page %d as a parent", p, k, mp)
				}
			}
		}
	}
	if split >= len(m.Pages) {
		t.Fatalf("%d groups' inputs are split over pages, %d pages", split, len(m.Pages))
	}
	t.Logf("%d of %d windows straddle a page (at root runs); %d of %d groups' inputs split", straddle, windows, split, len(inputPages))
	roots := m.RootPages()
	if roots == 0 || roots*4 > len(m.Pages) {
		t.Fatalf("%d root pages of %d", roots, len(m.Pages))
	}
	raw := m.StoredTriangles()*3*4 + m.VertexCount()*32
	t.Logf("%d pages (%d root), %d KB packed for %d KB of floats (%.1f%%), %d vertices for %d triangles",
		len(m.Pages), roots, m.PageBytes()/1024, raw/1024, 100*float64(m.PageBytes())/float64(raw), m.VertexCount(), m.StoredTriangles())
}

// A mesh read from its file has no geometry until asked: each page read on
// its own decodes to exactly the cook's pool for its clusters, and Flat is
// the whole of it. A file re-cooked under a loaded mesh is refused rather
// than read as the old one, and a damaged page is an error, not a panic.
func TestPagesReadBackFromTheFile(t *testing.T) {
	m, path := cookPagedRock(t, 4)
	got, err := nanite.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.HasFlat() || got.VertexCount() != m.VertexCount() {
		t.Fatalf("read mesh: flat %v, %d vertices of %d", got.HasFlat(), got.VertexCount(), m.VertexCount())
	}
	if got.Collision == nil || got.Collision.MeshResident() {
		t.Fatal("the collision mesh came with the metadata")
	}
	vbase := 0
	for pi := range got.Pages {
		d, err := got.LoadPage(pi)
		if err != nil {
			t.Fatal(err)
		}
		p := &got.Pages[pi]
		first := m.Clusters[p.ClusterFirst].TriFirst
		for k, ix := range d.Indices {
			want := m.Indices[3*int(first)+k]
			if int(ix)+vbase != int(want) {
				t.Fatalf("page %d index %d is %d, the cook's %d", pi, k, int(ix)+vbase, want)
			}
		}
		for k, f := range d.Vertices {
			if math.Float32bits(f) != math.Float32bits(m.Vertices[5*vbase+k]) {
				t.Fatalf("page %d vertex float %d differs", pi, k)
			}
		}
		for k, f := range d.Normals {
			if math.Float32bits(f) != math.Float32bits(m.Normals[3*vbase+k]) {
				t.Fatalf("page %d normal float %d differs", pi, k)
			}
		}
		vbase += len(d.Vertices) / 5
	}
	if err := got.Flat(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Indices, m.Indices) || len(got.Vertices) != len(m.Vertices) {
		t.Fatal("Flat differs from the cook's pool")
	}
	if _, err := got.Collision.WithMesh(); err != nil {
		t.Fatal(err)
	}

	// Damaged bytes: flip bytes in a page and read it from memory.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(4))
	for i := 0; i < 200; i++ {
		bad := append([]byte(nil), data...)
		dm, err := nanite.Decode(bytesReader(bad))
		if err != nil {
			t.Fatal(err)
		}
		// Somewhere in the data section, past the metadata.
		end := len(bad)
		start := end - int(dm.PageBytes()) - int(dm.CollisionSize)
		for k := 0; k < 4; k++ {
			bad[start+rng.Intn(int(dm.PageBytes()))] ^= byte(1 + rng.Intn(255))
		}
		for pi := range dm.Pages {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("a damaged page panicked: %v", r)
					}
				}()
				_, _ = dm.LoadPage(pi)
			}()
		}
	}

	// Re-cooked under it: a different source, so a different hash.
	other, err := Cook(writeSampleGLB(t, t.TempDir(), 3), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Write(path); err != nil {
		t.Fatal(err)
	}
	if _, err := got.LoadPage(0); err == nil {
		t.Fatal("a page was read from a file re-cooked since the mesh was loaded")
	}
}

// effectiveCut is the clusters a world-space threshold selects with the
// residency's errors, as the GPU's test does with the eye's.
func effectiveCut(r *nanite.Residency, m *nanite.Mesh, th float32) []int32 {
	var out []int32
	for ci := range m.Clusters {
		own, parent := r.Effective(int32(ci))
		if own <= th && (parent < 0 || parent > th) {
			out = append(out, int32(ci))
		}
	}
	return out
}

func cutIndices(m *nanite.Mesh, cut []int32) []uint32 {
	var idx []uint32
	for _, ci := range cut {
		c := &m.Clusters[ci]
		idx = append(idx, m.Indices[3*c.TriFirst:3*(c.TriFirst+c.TriCount)]...)
	}
	return idx
}

// Whatever pages are resident — the roots alone, all of them, or any
// random set, closed under parents or not — the cut the effective errors
// choose is whole (a closed rock stays closed: no crack, no surface twice)
// and every cluster in it is on a resident page. With everything resident it
// is exactly the cooked cut; with the roots alone it is the roots.
func TestAnyResidencyGivesAWholeCut(t *testing.T) {
	m, _ := cookPagedRock(t, 5)
	closed := func(idx []uint32) bool { return (&nanite.Mesh{Vertices: m.Vertices, Indices: idx}).Closed() }
	var maxErr float32
	for i := range m.Clusters {
		maxErr = max(maxErr, m.Clusters[i].Error)
	}
	ths := []float32{0, 0.002 * maxErr, 0.05 * maxErr, 0.3 * maxErr, maxErr}

	r := nanite.NewResidency(m)
	for _, th := range ths {
		cut := effectiveCut(r, m, th)
		for _, ci := range cut {
			if !m.Clusters[ci].IsRoot() {
				t.Fatalf("roots only: cluster %d (level %d) is in the cut", ci, m.Clusters[ci].Level)
			}
		}
		if !closed(cutIndices(m, cut)) {
			t.Fatalf("roots only: the cut at %g is open", th)
		}
	}
	for pi := range r.Resident {
		r.Resident[pi] = true
	}
	r.Update()
	for _, th := range ths {
		var want []int32
		for ci := range m.Clusters {
			if m.Clusters[ci].Selected(th) {
				want = append(want, int32(ci))
			}
		}
		if got := effectiveCut(r, m, th); !slices.Equal(got, want) {
			t.Fatalf("all resident: the cut at %g differs from the cooked one (%d vs %d clusters)", th, len(got), len(want))
		}
	}
	if ref, all := r.RefinableGroups(); ref != all {
		t.Fatalf("all resident but %d of %d groups refinable", ref, all)
	}

	rng := rand.New(rand.NewSource(5))
	for trial := 0; trial < 60; trial++ {
		frac := rng.Float64()
		for pi := range r.Resident {
			r.Resident[pi] = m.Pages[pi].Root || rng.Float64() < frac
		}
		r.Update()
		for _, th := range ths {
			cut := effectiveCut(r, m, th)
			for _, ci := range cut {
				if !r.Resident[m.PageOf(ci)] || !r.Drawable(ci) {
					t.Fatalf("trial %d: cluster %d is in the cut on a page not resident", trial, ci)
				}
			}
			if !closed(cutIndices(m, cut)) {
				t.Fatalf("trial %d (%.0f%% resident): the cut at %g is open", trial, 100*frac, th)
			}
		}
	}
}

// The tree the GPU walks, refreshed for a residency, finds exactly the
// clusters the flat per-cluster test does from any eye; refreshed with
// everything resident it is the cooked tree.
func TestEffectiveTreeWalkMatchesTheFlatTest(t *testing.T) {
	m, _ := cookPagedRock(t, 5)
	r := nanite.NewResidency(m)
	for pi := range r.Resident {
		r.Resident[pi] = true
	}
	r.Update()
	nodes := r.EffectiveNodes(nil)
	for i := range nodes {
		if nodes[i].MinError != m.Nodes[i].MinError || nodes[i].MaxParentError != m.Nodes[i].MaxParentError {
			t.Fatalf("node %d's refreshed errors %g/%g, cooked %g/%g", i, nodes[i].MinError, nodes[i].MaxParentError,
				m.Nodes[i].MinError, m.Nodes[i].MaxParentError)
		}
	}
	rng := rand.New(rand.NewSource(6))
	for trial := 0; trial < 20; trial++ {
		for pi := range r.Resident {
			r.Resident[pi] = m.Pages[pi].Root || rng.Float64() < 0.6
		}
		r.Update()
		nodes = r.EffectiveNodes(nodes)
		eff := append([]cluster.LODCluster(nil), m.Clusters...)
		for ci := range eff {
			eff[ci].Error, eff[ci].ParentError = r.Effective(int32(ci))
		}
		for e := 0; e < 5; e++ {
			eye := mgl32.Vec3{float32(rng.NormFloat64()), float32(rng.NormFloat64()), float32(rng.NormFloat64())}.Mul(0.2 + 6*rng.Float32())
			k := float32(400 + rng.Intn(2000))
			var flat []int
			for ci := range eff {
				if eff[ci].SelectedFrom(eye, k) {
					flat = append(flat, ci)
				}
			}
			if walked := cluster.SelectThroughTree(nodes, eff, eye, k); !slices.Equal(walked, flat) {
				t.Fatalf("trial %d eye %v: the walk found %d clusters, the flat test %d", trial, eye, len(walked), len(flat))
			}
		}
	}
}

// Streaming in converges: starting from the roots, loading what the cut's
// too-coarse clusters ask for (RequestPage's group, with its pages and their
// parents), a cut at a fine threshold reaches the cooked cut, every step a
// whole mesh.
func TestRequestsStreamTheCutIn(t *testing.T) {
	m, _ := cookPagedRock(t, 5)
	closed := func(idx []uint32) bool { return (&nanite.Mesh{Vertices: m.Vertices, Indices: idx}).Closed() }
	var maxErr float32
	for i := range m.Clusters {
		maxErr = max(maxErr, m.Clusters[i].Error)
	}
	th := 0.01 * maxErr
	r := nanite.NewResidency(m)
	var want []int32
	for ci := range m.Clusters {
		if m.Clusters[ci].Selected(th) {
			want = append(want, int32(ci))
		}
	}
	var load func(p int32)
	load = func(p int32) {
		if r.Resident[p] {
			return
		}
		r.Resident[p] = true
		for _, q := range m.Pages[p].Parents {
			load(q)
		}
	}
	for step := 0; ; step++ {
		cut := effectiveCut(r, m, th)
		if !closed(cutIndices(m, cut)) {
			t.Fatalf("step %d: open cut", step)
		}
		if slices.Equal(cut, want) {
			t.Logf("reached the cooked cut in %d steps, %d of %d pages resident", step, countTrue(r.Resident), len(r.Resident))
			break
		}
		if step > 64 {
			t.Fatal("streaming never reaches the cooked cut")
		}
		asked := 0
		for _, ci := range cut {
			if m.Clusters[ci].Error > th {
				for _, p := range r.GroupPages(ci) {
					for _, q := range r.PagesNeededWith(p) {
						if !r.Resident[q] {
							asked++
						}
						load(q)
					}
				}
			}
		}
		if asked == 0 {
			t.Fatalf("step %d: the cut is not the cooked one but nothing asks for a page", step)
		}
		r.Update()
	}
}

func countTrue(b []bool) int {
	n := 0
	for _, x := range b {
		if x {
			n++
		}
	}
	return n
}

// The collision's mesh part stays in the file until asked for.
func TestCollisionMeshIsPagedOut(t *testing.T) {
	_, path := cookPagedRock(t, 4)
	m, err := nanite.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	c := m.Collision
	if c.MeshResident() || c.TriangleCount() == 0 {
		t.Fatalf("resident %v, %d triangles", c.MeshResident(), c.TriangleCount())
	}
	if c.Resolve(meshcollide.ModeMesh, 1) != meshcollide.ModeMesh {
		t.Fatal("a paged-out collision mesh no longer offers the mesh mode")
	}
	full, err := c.WithMesh()
	if err != nil {
		t.Fatal(err)
	}
	if full == c || len(full.Tris)/3 != c.TriangleCount() || c.MeshResident() {
		t.Fatal("WithMesh did not hand back a separate whole copy")
	}
	// What stays loaded is the hulls; the mesh part is most of it.
	if c.Bytes()*2 > full.Bytes() {
		t.Fatalf("%d bytes held of %d", c.Bytes(), full.Bytes())
	}
	t.Logf("held with the metadata: %d KB; the mesh part adds %d KB", c.Bytes()/1024, (full.Bytes()-c.Bytes())/1024)
	if filepath.Ext(path) != nanite.Ext {
		t.Fatal(path)
	}
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
