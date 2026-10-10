package render

// The clustered shadow cull (staticGroup.addCulled) against the brute-force
// per-surface test it replaced: for any frustum, the vertices it hands to the
// draw must be exactly the vertices of the surfaces AABBInFrustum accepts —
// not a superset (a cluster called Inside that was not), and not a subset (a
// cluster called Outside that was not, which is a hole in a shadow).

import (
	"math/rand"
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/testenv"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// scatteredStatic is n small quads strewn through a 200 m box, over a few
// materials, so each group has many clusters.
func scatteredStatic(n int, rng *rand.Rand) *world.StaticGeometry {
	s := &world.StaticGeometry{}
	for i := 0; i < n; i++ {
		c := mgl32.Vec3{rng.Float32()*200 - 100, rng.Float32()*40 - 20, rng.Float32()*200 - 100}
		h := mgl32.Vec3{rng.Float32()*2 + 0.1, rng.Float32()*2 + 0.1, rng.Float32()*2 + 0.1}
		lo, hi := c.Sub(h), c.Add(h)
		verts := []float32{
			lo[0], lo[1], lo[2], 0, 0, hi[0], lo[1], lo[2], 1, 0, hi[0], hi[1], hi[2], 1, 1,
			lo[0], lo[1], lo[2], 0, 0, hi[0], hi[1], hi[2], 1, 1, lo[0], hi[1], hi[2], 0, 1,
		}
		s.Surfaces = append(s.Surfaces, world.StaticSurface{
			Geometry: gfx.Geometry{Vertices: verts, Texture: []string{"a", "b", "c"}[i%3], Alpha: 1},
			Chunk:    "c", Lo: lo, Hi: hi,
		})
	}
	return s
}

// covered is the set of vertex indexes a list of ranges draws.
func covered(first, count []int32) map[int32]bool {
	out := map[int32]bool{}
	for i := range first {
		for v := first[i]; v < first[i]+count[i]; v++ {
			out[v] = true
		}
	}
	return out
}

func TestClusteredShadowCullMatchesPerSurfaceTest(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	rng := rand.New(rand.NewSource(7))
	s := scatteredStatic(5000, rng)
	sb := buildStaticBatches(s)

	var inside, straddle int
	for trial := 0; trial < 200; trial++ {
		// A mix of what the passes actually use: a point light's 90° cube
		// face, and a sun cascade's orthographic box.
		eye := mgl32.Vec3{rng.Float32()*160 - 80, rng.Float32()*20 - 10, rng.Float32()*160 - 80}
		at := eye.Add(mgl32.Vec3{rng.Float32()*2 - 1, rng.Float32()*2 - 1, rng.Float32()*2 - 1})
		var proj mgl32.Mat4
		if trial%2 == 0 {
			proj = mgl32.Perspective(mgl32.DegToRad(90), 1, 0.1, rng.Float32()*60+5)
		} else {
			r := rng.Float32()*60 + 5
			proj = mgl32.Ortho(-r, r, -r, r, -r*2, r*2)
		}
		f := ExtractFrustum(proj.Mul4(mgl32.LookAtV(eye, at, mgl32.Vec3{0, 1, 0.01})))

		for _, g := range sb.groups {
			g.addCulled(s, &f)
			got := covered(g.visFirst, g.visCount)
			g.visFirst, g.visCount = g.visFirst[:0], g.visCount[:0]

			var wf, wc []int32
			for k, si := range g.surfaces {
				if AABBInFrustum(f, s.Surfaces[si].Lo, s.Surfaces[si].Hi) {
					wf, wc = append(wf, g.firsts[k]), append(wc, g.counts[k])
				}
			}
			want := covered(wf, wc)
			if len(got) != len(want) {
				t.Fatalf("trial %d group %s: clustered cull drew %d vertices, per-surface test %d",
					trial, g.key.mat.Texture, len(got), len(want))
			}
			for v := range want {
				if !got[v] {
					t.Fatalf("trial %d: vertex %d passes the per-surface test but was not drawn", trial, v)
				}
			}
			for _, c := range g.clusters {
				switch ClassifyAABB(f, c.lo, c.hi) {
				case FrustumInside:
					inside++
				case FrustumIntersects:
					straddle++
				}
			}
		}
	}
	// The whole point is that most clusters are decided without their
	// surfaces; a test in which none were Inside proved nothing about that path.
	if inside == 0 || straddle == 0 {
		t.Fatalf("fixture never exercised both paths: %d inside, %d straddling", inside, straddle)
	}
}

func TestClustersAreSpatiallyCompact(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	s := scatteredStatic(6000, rand.New(rand.NewSource(3)))
	sb := buildStaticBatches(s)
	// 2000 surfaces per group through a 200 m box: unsorted, a run of 32 spans
	// most of the box. Sorted, a cluster's box should be a small fraction of it
	// (about a sixteenth here: the odd run that straddles a jump in the curve
	// dominates the mean).
	var vol, n float64
	for _, g := range sb.groups {
		for _, c := range g.clusters {
			e := c.hi.Sub(c.lo)
			vol += float64(e[0] * e[1] * e[2])
			n++
		}
	}
	if avg, whole := vol/n, 200.0*40*200; avg > whole/8 {
		t.Fatalf("average cluster box is %.0f m³ of a %.0f m³ level — the sort is not grouping neighbours", avg, whole)
	}
}

// A chunk's brushes are culled against the frustum carried into the chunk
// (Frustum.InSpaceOf) rather than each box carried out. The two must agree on
// every point: a local point is inside the carried frustum exactly when its
// world image is inside the original.
func TestAFrustumCarriedIntoAChunkAgreesPointForPoint(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	inside := func(f Frustum, p mgl32.Vec3) bool {
		for _, pl := range f.Planes {
			if pl[0]*p[0]+pl[1]*p[1]+pl[2]*p[2]+pl[3] < 0 {
				return false
			}
		}
		return true
	}
	var in, out int
	for trial := 0; trial < 100; trial++ {
		eye := mgl32.Vec3{rng.Float32()*40 - 20, rng.Float32() * 10, rng.Float32()*40 - 20}
		f := ExtractFrustum(mgl32.Perspective(mgl32.DegToRad(70), 1.6, 0.1, 50).
			Mul4(mgl32.LookAtV(eye, mgl32.Vec3{}, mgl32.Vec3{0, 1, 0})))
		// What a chunk's model matrix can be: moved, turned about Y, scaled.
		m := mgl32.Translate3D(rng.Float32()*30-15, rng.Float32()*5, rng.Float32()*30-15).
			Mul4(mgl32.HomogRotate3DY(rng.Float32() * 6.28)).
			Mul4(mgl32.Scale3D(1.5, 1.5, 1.5))
		local := f.InSpaceOf(m)
		for i := 0; i < 200; i++ {
			p := mgl32.Vec3{rng.Float32()*60 - 30, rng.Float32()*20 - 10, rng.Float32()*60 - 30}
			w := m.Mul4x1(p.Vec4(1)).Vec3()
			want := inside(f, w)
			// Points within a hair of a plane may round either way.
			if got := inside(local, p); got != want && !nearAPlane(f, w) {
				t.Fatalf("trial %d: local point %v is inside=%v in chunk space but %v in the world", trial, p, got, want)
			}
			if want {
				in++
			} else {
				out++
			}
		}
	}
	if in == 0 || out == 0 {
		t.Fatalf("no coverage: %d inside, %d outside", in, out)
	}
}

func nearAPlane(f Frustum, p mgl32.Vec3) bool {
	for _, pl := range f.Planes {
		n := mgl32.Vec3{pl[0], pl[1], pl[2]}.Len()
		if d := (pl[0]*p[0] + pl[1]*p[1] + pl[2]*p[2] + pl[3]) / n; d > -1e-3 && d < 1e-3 {
			return true
		}
	}
	return false
}

// The camera pass gathers what the PVS handed it through the clusters and a
// bitset rather than walking every surface; what it draws must be exactly the
// visible surfaces that pass the frustum, and each transparent one queued once.
func TestTheCameraPassGathersExactlyTheVisibleSurfaces(t *testing.T) {
	testenv.WithoutGeometryUploads(t)
	rng := rand.New(rand.NewSource(5))
	s := scatteredStatic(4000, rng)
	for i := range s.Surfaces {
		if i%7 == 0 {
			s.Surfaces[i].Geometry.Alpha = 0.5 // a transparent group per texture
		}
	}
	sb := buildStaticBatches(s)

	for trial := 0; trial < 60; trial++ {
		eye := mgl32.Vec3{rng.Float32()*160 - 80, rng.Float32()*20 - 10, rng.Float32()*160 - 80}
		at := eye.Add(mgl32.Vec3{rng.Float32()*2 - 1, rng.Float32()*0.4 - 0.2, rng.Float32()*2 - 1})
		f := ExtractFrustum(mgl32.Perspective(mgl32.DegToRad(70), 1.6, 0.1, rng.Float32()*150+10).
			Mul4(mgl32.LookAtV(eye, at, mgl32.Vec3{0, 1, 0})))
		// A PVS answer: some of the level, in no particular order.
		var visible []int
		for _, i := range rng.Perm(len(s.Surfaces)) {
			if rng.Intn(3) > 0 {
				visible = append(visible, i)
			}
		}
		type key struct {
			b     *gfx.Batch
			first int32
		}
		queued := map[key]int{}
		drawn := sb.gatherVisible(s, visible, f, eye, func(d BatchDraw, _ float32) { queued[key{d.Batch, d.First[0]}]++ })

		inPVS := map[int32]bool{}
		for _, i := range visible {
			inPVS[int32(i)] = true
		}
		want := 0
		for _, g := range sb.groups {
			got := covered(g.visFirst, g.visCount)
			g.visFirst, g.visCount = g.visFirst[:0], g.visCount[:0]
			var wf, wc []int32
			for k, si := range g.surfaces {
				if !inPVS[si] || !AABBInFrustum(f, s.Surfaces[si].Lo, s.Surfaces[si].Hi) {
					continue
				}
				want++
				if g.batch.IsTransparent() {
					if n := queued[key{&g.batch, g.firsts[k]}]; n != 1 {
						t.Fatalf("trial %d: a visible transparent surface was queued %d times", trial, n)
					}
					continue
				}
				wf, wc = append(wf, g.firsts[k]), append(wc, g.counts[k])
			}
			exp := covered(wf, wc)
			if len(got) != len(exp) {
				t.Fatalf("trial %d: drew %d vertices of a group, want %d", trial, len(got), len(exp))
			}
			for v := range exp {
				if !got[v] {
					t.Fatalf("trial %d: vertex %d is visible and was not drawn", trial, v)
				}
			}
			for _, w := range g.visBits {
				if w != 0 {
					t.Fatalf("trial %d: the pass left bits set for the next frame", trial)
				}
			}
		}
		if drawn != want {
			t.Fatalf("trial %d: reported %d drawn, want %d", trial, drawn, want)
		}
	}
}
