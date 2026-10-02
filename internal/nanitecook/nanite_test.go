package nanitecook

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"foundry/internal/cluster"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/meshcollide"
	"foundry/internal/model"
	"foundry/internal/nanite"
	"foundry/internal/testenv"

	"github.com/go-gl/mathgl/mgl32"
)

// writeSampleGLB writes SampleRock(level) as a .glb under dir.
func writeSampleGLB(t testing.TB, dir string, level int) string {
	t.Helper()
	glb, err := model.EncodeGLB(SampleRock(level, 0.3))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rock.glb")
	if err := os.WriteFile(path, glb, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSampleRockIsOutwardWound(t *testing.T) {
	m := SampleRock(2, 0)
	out := 0
	for i := 0; i < m.TriangleCount(); i++ {
		a, b, c := m.Positions[m.Indices[3*i]], m.Positions[m.Indices[3*i+1]], m.Positions[m.Indices[3*i+2]]
		if b.Sub(a).Cross(c.Sub(a)).Dot(a.Add(b).Add(c)) > 0 {
			out++
		}
	}
	if out < m.TriangleCount()*95/100 {
		t.Fatalf("%d of %d triangles face outward", out, m.TriangleCount())
	}
}

func TestCookGLBProducesAValidClusteredMesh(t *testing.T) {
	dir := t.TempDir()
	src := writeSampleGLB(t, dir, 4) // 5120 triangles
	m, err := Cook(src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if m.TriangleCount() != 5120 {
		t.Fatalf("%d triangles cooked of 5120", m.TriangleCount())
	}
	if len(m.Slots) != 2 || m.Slots[0].Name != "stone" || m.Slots[1].Name != "moss" {
		t.Fatalf("slots %+v", m.Slots)
	}
	// The weld shares vertices: a closed sphere is about two triangles a
	// vertex.
	if m.VertexCount() > m.TriangleCount() {
		t.Fatalf("%d vertices for %d triangles: the weld shared nothing", m.VertexCount(), m.TriangleCount())
	}
	// Clusters within limits, never spanning slots, cones only on the
	// single-sided slot.
	for si, s := range m.Slots {
		for ci := s.First; ci < s.First+s.Count; ci++ {
			c := &m.Clusters[ci]
			if c.TriCount > cluster.DefaultMaxTriangles || c.Vertices > cluster.DefaultMaxVertices {
				t.Fatalf("cluster %d: %d triangles, %d vertices", ci, c.TriCount, c.Vertices)
			}
			if s.DoubleSided && c.ConeCutoff < 1 {
				t.Fatalf("slot %d is double-sided but cluster %d has a cone", si, ci)
			}
		}
	}
	withCone := 0
	for _, c := range m.Clusters {
		if c.ConeCutoff < 1 {
			withCone++
		}
	}
	if withCone == 0 {
		t.Fatal("no cluster of a convex-ish rock has a normal cone")
	}
	// Every vertex inside the bounds and the sphere.
	for v := 0; v < m.VertexCount(); v++ {
		p := mgl32.Vec3{m.Vertices[5*v], m.Vertices[5*v+1], m.Vertices[5*v+2]}
		if p.Sub(m.Center).Len() > m.Radius {
			t.Fatalf("vertex %d outside the bounding sphere", v)
		}
	}
	if m.SDF == nil || m.SDF.N < sdfMinRes {
		t.Fatalf("SDF %+v", m.SDF)
	}
	// Inside the rock reads negative, well outside positive.
	g := m.SDF
	at := func(p mgl32.Vec3) float32 {
		q := p.Sub(g.Lo).Mul(1 / g.Cell)
		x, y, z := int(q[0]+0.5), int(q[1]+0.5), int(q[2]+0.5)
		return g.Data[(z*g.N+y)*g.N+x]
	}
	if at(m.Center) >= 0 {
		t.Fatalf("SDF at the rock's centre is %g", at(m.Center))
	}
	if at(g.Lo.Add(mgl32.Vec3{g.Cell, g.Cell, g.Cell})) <= 0 {
		t.Fatal("SDF at the grid's corner is not outside")
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m, err := Cook(writeSampleGLB(t, dir, 3), Options{})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := m.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := nanite.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got.SourceHash != m.SourceHash || len(got.Indices) != len(m.Indices) || len(got.Clusters) != len(m.Clusters) ||
		got.SDF.N != m.SDF.N || got.Radius != m.Radius {
		t.Fatal("decoded mesh differs")
	}
	for i := range m.Indices {
		if got.Indices[i] != m.Indices[i] {
			t.Fatal("indices differ")
		}
	}
	for i := range m.Clusters {
		if got.Clusters[i] != m.Clusters[i] {
			t.Fatalf("cluster %d differs", i)
		}
	}
	// A damaged or foreign file is refused.
	b := buf.Bytes()
	bad := append([]byte(nil), b...)
	bad[4] = 99
	if _, err := nanite.Decode(bytes.NewReader(bad)); err == nil {
		t.Error("a file of another version decoded")
	}
	bad = append([]byte(nil), b...)
	bad[10] ^= 0xff
	if _, err := nanite.Decode(bytes.NewReader(bad)); err == nil {
		t.Error("a header whose hash disagrees with the body decoded")
	}
	if _, err := nanite.Decode(bytes.NewReader([]byte("FBX binary"))); err == nil {
		t.Error("a foreign file decoded")
	}
}

func TestCookFileSkipsWhatIsUpToDate(t *testing.T) {
	dir := t.TempDir()
	src := writeSampleGLB(t, dir, 2)
	r, err := CookFile(src, "", false, Options{})
	if err != nil || r.Skipped || r.Out != filepath.Join(dir, "rock.fnm") {
		t.Fatalf("first cook: %+v %v", r, err)
	}
	r, err = CookFile(src, "", false, Options{})
	if err != nil || !r.Skipped {
		t.Fatalf("second cook of an unchanged source: %+v %v", r, err)
	}
	if r, err = CookFile(src, "", true, Options{}); err != nil || r.Skipped {
		t.Fatalf("forced cook: %+v %v", r, err)
	}
	// Change the source: re-cooked.
	glb, _ := model.EncodeGLB(SampleRock(2, 0.9))
	if err := os.WriteFile(src, glb, 0o644); err != nil {
		t.Fatal(err)
	}
	if r, err = CookFile(src, "", false, Options{}); err != nil || r.Skipped {
		t.Fatalf("cook of a changed source: %+v %v", r, err)
	}
	// And the cache sees the new file.
	a, err := nanite.Load(r.Out)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := nanite.Load(r.Out)
	if a != b {
		t.Fatal("the cache read an unchanged file twice")
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := CookFile(src, "", true, Options{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	os.Chtimes(r.Out, now, now)
	c, _ := nanite.Load(r.Out)
	if c == a {
		t.Fatal("the cache kept a mesh whose file was rewritten")
	}
}

func TestSDFResolutionFollowsSize(t *testing.T) {
	if n := sdfResolution(0.5, 0); n != sdfMinRes {
		t.Errorf("a half-metre mesh gets %d cells", n)
	}
	if n := sdfResolution(6, 0); n <= sdfMinRes || n >= sdfMaxRes {
		t.Errorf("a six-metre mesh gets %d cells", n)
	}
	if n := sdfResolution(500, 0); n != sdfMaxRes {
		t.Errorf("a cliff gets %d cells", n)
	}
}

// A cooked .fmdl must hold the very triangles a scenery placement of the
// .fmdl draws — positions, UVs and normals bit for bit — in some order.
func TestCookedModelMatchesTheSceneryMesh(t *testing.T) {
	path := filepath.Join(testenv.RepoRoot(t), "resources", "models", "dropship.fmdl")
	m, err := Cook(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := model.LoadModel(path)
	if err != nil {
		t.Fatal(err)
	}
	type corner [8]float32
	type tri [3]corner
	count := map[tri]int{}
	for _, sub := range doc.ModelSubmeshes() {
		g := gfx.Geometry{Smooth: sub.Smooth, SmoothAngle: doc.SmoothAngle, AutoSmooth: true, CPUOnly: true}
		if err := g.Load(sub.Vertices, sub.Indices, sub.Texture, mgl32.Vec3{}); err != nil {
			t.Fatal(err)
		}
		for k := 0; k+2 < len(g.Vertices)/5; k += 3 {
			var tr tri
			for c := 0; c < 3; c++ {
				v := k + c
				copy(tr[c][:5], g.Vertices[5*v:5*v+5])
				copy(tr[c][5:], g.Normals[3*v:3*v+3])
			}
			count[tr]++
		}
	}
	for t3 := 0; t3 < m.TriangleCount(); t3++ {
		var tr tri
		for c := 0; c < 3; c++ {
			v := m.Indices[3*t3+c]
			copy(tr[c][:5], m.Vertices[5*v:5*v+5])
			copy(tr[c][5:], m.Normals[3*v:3*v+3])
		}
		if count[tr] == 0 {
			t.Fatalf("cooked triangle %d is not one the scenery mesh draws", t3)
		}
		count[tr]--
	}
	for _, n := range count {
		if n != 0 {
			t.Fatal("the scenery mesh draws a triangle the cook lost")
		}
	}
	if !m.Slots[0].DoubleSided {
		t.Fatal("a .fmdl slot must be double-sided (models draw their back faces)")
	}
}

func BenchmarkCookRock(b *testing.B) {
	dir := b.TempDir()
	src := writeSampleGLB(b, dir, 6) // 81,920 triangles
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Cook(src, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

// The cook keeps a closed source closed: the weld splits vertices at UV seams
// and slot borders, and the shadow passes' cone test (nanite/closed.go,
// render/nanite_cull.go) is only allowed on a mesh that still reads as a
// solid after it. A rock with a face taken out is not.
func TestACookedClosedRockStaysClosed(t *testing.T) {
	dir := t.TempDir()
	m, err := Cook(writeSampleGLB(t, dir, 3), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !m.Closed() {
		t.Fatal("the cooked sample rock does not read as closed")
	}
	m.Indices = m.Indices[3:]
	if m.Closed() {
		t.Fatal("the rock with a triangle removed still reads as closed")
	}
}

// A cook builds each slot's cluster DAG and the fallback: every level's
// clusters after level 0's in the slot's run, a tree over them, and a fallback
// cut that is closed, far smaller than the full detail, and the source of the
// distance grid. Cuts of both slots together at any threshold stay closed —
// where the stone meets the moss is locked.
func TestCookBuildsTheDAGAndTheFallback(t *testing.T) {
	m, err := Cook(writeSampleGLB(t, t.TempDir(), 5), Options{}) // 20,480 triangles
	if err != nil {
		t.Fatal(err)
	}
	if m.Levels < 4 || len(m.Nodes) == 0 || m.Leaves == 0 {
		t.Fatalf("%d levels, %d nodes, %d leaves", m.Levels, len(m.Nodes), m.Leaves)
	}
	if m.TriangleCount() != 20480 || m.StoredTriangles() <= m.TriangleCount() {
		t.Fatalf("level 0 holds %d, all levels %d", m.TriangleCount(), m.StoredTriangles())
	}
	closed := func(idx []uint32) bool {
		return (&nanite.Mesh{Vertices: m.Vertices, Indices: idx}).Closed()
	}
	if !closed(m.BaseIndices()) {
		t.Fatal("the full detail is not closed")
	}
	var maxErr float32
	for i := range m.Clusters {
		maxErr = max(maxErr, m.Clusters[i].Error)
	}
	for _, f := range []float32{0.01, 0.1, 0.5, 1} {
		var idx []uint32
		for i := range m.Clusters {
			if c := &m.Clusters[i]; c.Selected(f * maxErr) {
				idx = append(idx, m.Indices[3*c.TriFirst:3*(c.TriFirst+c.TriCount)]...)
			}
		}
		if !closed(idx) {
			t.Fatalf("the cut at %g of the largest error is open", f)
		}
	}
	fb := len(m.Fallback) / 3
	if m.FallbackError <= 0 || fb == 0 || fb*4 > m.TriangleCount() {
		t.Fatalf("fallback: %d triangles at error %g", fb, m.FallbackError)
	}
	if !closed(m.Fallback) {
		t.Fatal("the fallback mesh is open")
	}
	for si := range m.Slots {
		if len(m.SlotFallback(si)) == 0 {
			t.Fatalf("slot %d has no fallback triangles", si)
		}
	}
	t.Logf("%d levels, %d clusters, fallback %d triangles at error %.4g", m.Levels, len(m.Clusters), fb, m.FallbackError)
}

// A cook builds the mesh's collision (docs/nanite/PHASE_2b.md): the DAG cut
// at the collision error — fewer triangles than the full detail on a mesh
// whose bumps are under it, and closed, because the cut of a closed DAG is —
// each triangle tagged with its slot, a tree over it, and hulls that hold the
// surface. It survives the file, and its settings are part of the hash.
func TestCookBuildsTheCollision(t *testing.T) {
	dir := t.TempDir()
	src := writeSampleGLB(t, dir, 5) // 20,480 triangles
	m, err := Cook(src, Options{Collision: meshcollide.Settings{Error: 0.08}})
	if err != nil {
		t.Fatal(err)
	}
	c := m.Collision
	if c == nil {
		t.Fatal("no collision")
	}
	if c.TriangleCount() == 0 || c.TriangleCount() >= m.TriangleCount() {
		t.Fatalf("collision keeps %d of %d triangles", c.TriangleCount(), m.TriangleCount())
	}
	if c.OpenEdges != 0 || c.NonManifold != 0 {
		t.Errorf("the collision cut of a closed rock has %d open and %d non-manifold edges", c.OpenEdges, c.NonManifold)
	}
	if len(c.TagNames) != 2 || c.TagNames[0] != "stone" || c.TagNames[1] != "moss" {
		t.Errorf("tag names %v", c.TagNames)
	}
	tagged := map[uint8]int{}
	for _, tg := range c.Tags {
		tagged[tg]++
	}
	if tagged[0] == 0 || tagged[1] == 0 {
		t.Errorf("both slots should tag triangles: %v", tagged)
	}
	if len(c.Hulls) == 0 {
		t.Fatal("no hulls")
	}
	// Every collision vertex is inside some hull, within what the
	// decomposition says it left out: the concavity of a piece it stopped
	// splitting, and how far a capped hull strays.
	r := meshcollide.NewResident(c)
	slack := c.Concavity + c.VertCapError + 1e-4
	for _, p := range r.Pos {
		in := false
		for hi := range c.Hulls {
			h := &c.Hulls[hi]
			inside := true
			for _, pl := range h.Planes {
				if (mgl32.Vec3{pl[0], pl[1], pl[2]}).Dot(p)-pl[3] > slack {
					inside = false
					break
				}
			}
			if inside {
				in = true
				break
			}
		}
		if !in {
			t.Fatalf("collision vertex %v is in no hull", p)
		}
	}

	var buf bytes.Buffer
	if err := m.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := nanite.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got.Collision == nil || got.Collision.TriangleCount() != c.TriangleCount() || len(got.Collision.Hulls) != len(c.Hulls) ||
		len(got.Collision.Nodes) != len(c.Nodes) || got.Collision.Settings != c.Settings {
		t.Fatal("the collision did not survive the file")
	}

	// The settings are in the hash: a cook with others is not up to date.
	if _, err := CookFile(src, "", true, Options{}); err != nil {
		t.Fatal(err)
	}
	out := CookedPath(src)
	if fresh, _ := UpToDateWith(src, out, Options{}); !fresh {
		t.Fatal("a cook is not up to date with its own options")
	}
	if fresh, _ := UpToDateWith(src, out, Options{Collision: meshcollide.Settings{Error: 0.2}}); fresh {
		t.Fatal("a cook with another collision error reads as up to date")
	}
	if fresh, _ := UpToDateWith(src, out, Options{Collision: meshcollide.Settings{Error: meshcollide.DefaultError}}); !fresh {
		t.Fatal("the default error spelled out should hash as the default")
	}
}

// ProjectOptions reads game.yaml's nanite block, and a project without one
// cooks with the defaults.
func TestProjectOptionsReadTheManifest(t *testing.T) {
	dir := t.TempDir()
	prev := fileutil.ResourcesPath
	fileutil.ResourcesPath = dir
	t.Cleanup(func() { fileutil.ResourcesPath = prev })
	if o := ProjectOptions(); o.Collision != (meshcollide.Settings{}) {
		t.Fatalf("no manifest: %+v", o.Collision)
	}
	yml := "title: x\nnanite:\n  collision_error: 0.05\n  collision_max_hulls: 4\n  collision_auto_size: 9\n"
	if err := os.WriteFile(filepath.Join(dir, "game.yaml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	o := ProjectOptions()
	if o.Collision.Error != 0.05 || o.Collision.MaxHulls != 4 || o.Collision.AutoSize != 9 {
		t.Fatalf("read %+v", o.Collision)
	}
}
