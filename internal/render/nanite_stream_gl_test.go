//go:build !nogl

package render

// GL tests for Phase 3's page pool (nanite_stream.go; docs/nanite/PHASE_3.md):
// with everything resident it draws the Phase 2 pool's picture exactly;
// started from the root pages alone it streams the rest in from the GPU's own
// feedback, a whole mesh every frame, until the picture is the full one; and
// under a budget cap smaller than the level, it evicts and refills as the
// camera moves, never holding more than the cap, never uploading more than a
// frame's share, never drawing a crack — and the view it settles on is the
// unbounded one wherever what that view needs fits.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/model"
	"foundry/internal/nanitecook"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// cookRockSeed is cookRock with a seed, so a level can hold distinct meshes.
func cookRockSeed(t *testing.T, level int, seed float32) string {
	t.Helper()
	dir := t.TempDir()
	glb, err := model.EncodeGLB(nanitecook.SampleRock(level, seed))
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "rock.glb")
	if err := os.WriteFile(src, glb, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := nanitecook.CookFile(src, "", true, nanitecook.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return res.Out
}

// freshNanitePool makes the next prepare build the pool anew (a switch the
// pool reads at build time changed).
func freshNanitePool(t *testing.T) {
	t.Helper()
	r := &naniteScenery
	r.poolList, r.meshes, r.lvl = nil, nil, nil
	t.Cleanup(func() { r.poolList, r.meshes, r.lvl = nil, nil, nil })
}

// streamFrame draws one frame of the level's cooked placements: a new
// streaming frame, the camera's two passes, the picture.
func streamFrame(t *testing.T, lvl *world.Level) []uint8 {
	t.Helper()
	naniteStreamFrame++
	px, _ := drawNanite(t, lvl, true)
	return px
}

// checkStreamRecords reads the pool's cluster records back from the GPU and
// checks them against the streamer's own state, as the frame just drawn saw
// them: a record that draws points at the slot holding its own page, with
// the triangles that page puts there; a cluster whose page is not resident
// draws nothing and can never be in the cut (its parent error reads 0, or
// it is a root); and the slots hold no page twice.
func checkStreamRecords(t *testing.T, when string) {
	t.Helper()
	r := &naniteScenery
	s := r.stream
	if s == nil {
		t.Fatalf("%s: no page pool", when)
	}
	n := 0
	for _, sm := range s.meshes {
		n += len(sm.m.Clusters)
	}
	recs := make([]gpuCluster, n)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, r.clusters)
	glext.GetBufferSubData(gl.SHADER_STORAGE_BUFFER, 0, n*int(unsafe.Sizeof(gpuCluster{})), unsafe.Pointer(&recs[0]))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	seen := map[int32]bool{}
	for slot, gp := range s.slotPage {
		if gp < 0 {
			continue
		}
		if seen[gp] {
			t.Fatalf("%s: page %d is in two slots", when, gp)
		}
		seen[gp] = true
		if s.pages[gp].slot != int32(slot) {
			t.Fatalf("%s: slot %d holds page %d, which says it is in slot %d", when, slot, gp, s.pages[gp].slot)
		}
	}
	for mi, sm := range s.meshes {
		for pi := range sm.m.Pages {
			p := &sm.m.Pages[pi]
			gp := sm.pageBase + int32(pi)
			for k := int32(0); k < p.ClusterCount; k++ {
				ci := p.ClusterFirst + k
				rec := recs[sm.clusterBase+uint32(ci)]
				c := &sm.m.Clusters[ci]
				if rec.Draw[1] == 0 {
					if s.pages[gp].slot >= 0 {
						t.Fatalf("%s: mesh %d cluster %d is resident but its record draws nothing", when, mi, ci)
					}
					if !c.IsRoot() && rec.Errs[1] != 0 {
						t.Fatalf("%s: mesh %d cluster %d is not resident but could be in the cut (parent error %g)", when, mi, ci, rec.Errs[1])
					}
					continue
				}
				slot := int32(rec.Draw[2] / streamSlotVerts)
				if s.slotPage[slot] != gp {
					t.Fatalf("%s: mesh %d cluster %d (page %d) draws from slot %d, which holds page %d", when, mi, ci, gp, slot, s.slotPage[slot])
				}
				if rec.Draw[0] != uint32(slot)*streamSlotIndices+3*sm.offsets[pi][k] || rec.Draw[1] != 3*c.TriCount {
					t.Fatalf("%s: mesh %d cluster %d's draw range is wrong", when, mi, ci)
				}
			}
		}
	}
}

// pixelsDiffer counts pixels that are not the same.
func pixelsDiffer(a, b []uint8) int {
	n := 0
	for i := 0; i < len(a); i += 4 {
		if a[i] != b[i] || a[i+1] != b[i+1] || a[i+2] != b[i+2] {
			n++
		}
	}
	return n
}

// With every page resident, the page pool draws exactly what the Phase 2
// pool does — the same clusters, the same triangles, from other buffers.
func TestNaniteStreamPoolMatchesTheFlatPool(t *testing.T) {
	lodProbe(t)
	rock := cookRockSeed(t, 5, 0.4)
	poses := []mgl32.Vec3{{0, 0, 0}, {0.9, 0.3, -1.5}, {-1.2, -0.4, -4}, {1.5, 0.2, -9}}
	lvl := naniteSceneryLevel(t, rock, true, poses, 0.6)
	gfx.Cam.Position = mgl32.Vec3{0.1, 0.05, 2.4}
	setLODThreshold(t, 1)

	NaniteStream = false
	freshNanitePool(t)
	flat := streamFrame(t, lvl)
	flat2 := streamFrame(t, lvl)
	if naniteScenery.stream != nil {
		t.Fatal("FOUNDRY_NANITE_STREAM=0 still built a page pool")
	}
	NaniteStream = true
	freshNanitePool(t)
	for frame := 0; frame < 3; frame++ {
		got := streamFrame(t, lvl)
		if naniteScenery.stream == nil {
			t.Fatal("no page pool")
		}
		st := DebugNaniteStream()
		if st.Resident != st.Pages {
			t.Fatalf("%d of %d pages resident with the budget unbounded", st.Resident, st.Pages)
		}
		ref := flat
		if frame > 0 {
			ref = flat2
		}
		if n := pixelsDiffer(ref, got); n > 0 {
			t.Errorf("frame %d: %d pixels differ from the flat pool", frame, n)
		}
	}
	if holes := cracksOf(t, lvl); holes > 0 {
		t.Errorf("%d pixels show a back face through a hole", holes)
	}
}

// Started from the root pages alone, the pool streams in what the camera's
// passes ask for: every frame on the way is a whole mesh (no back face
// through a hole), no frame takes in more than streamPagesPerFrame pages,
// and it settles on exactly the fully resident picture. Both upload paths:
// the persistently mapped ring, and the 4.3 one.
func TestNaniteStreamStreamsInFromTheRoots(t *testing.T) {
	lodProbe(t)
	rock := cookRockSeed(t, 6, 0.4) // 81,920 triangles, 47 pages
	lvl := naniteSceneryLevel(t, rock, true, []mgl32.Vec3{{0, 0, 0}, {0.8, 0.2, -1.2}}, 0.7)
	gfx.Cam.Position = mgl32.Vec3{0.1, 0.05, 1.8}
	setLODThreshold(t, 1)
	freshNanitePool(t)
	full := streamFrame(t, lvl)
	full = streamFrame(t, lvl)

	t.Cleanup(glext.Reinit)
	for _, persistent := range []bool{true, false} {
		NanitePersistent = persistent
		if !persistent {
			glext.DisableBufferStorage()
		}
		NaniteStreamPreload = false
		freshNanitePool(t)
		first := streamFrame(t, lvl)
		st := DebugNaniteStream()
		if !persistent && st.Persistent {
			t.Fatal("the 4.3 path was asked for, the ring is persistent")
		}
		if persistent && !st.Persistent {
			t.Log("this driver has no buffer storage: both runs take the 4.3 path")
		}
		roots := st.Resident
		if roots == 0 || roots >= st.Pages {
			t.Fatalf("started with %d of %d pages", roots, st.Pages)
		}
		if pixelsDiffer(first, full) == 0 {
			t.Fatal("the roots alone draw the full picture: nothing to stream")
		}
		settled := -1
		for frame := 1; frame < 60; frame++ {
			got := streamFrame(t, lvl)
			st = DebugNaniteStream()
			if st.Uploaded > streamPagesPerFrame {
				t.Fatalf("frame %d took in %d pages (most %d)", frame, st.Uploaded, streamPagesPerFrame)
			}
			checkStreamRecords(t, fmt.Sprintf("frame %d", frame))
			if st.DirectWrites > 0 {
				t.Fatalf("frame %d: %d record writes missed the staging ring", frame, st.DirectWrites)
			}
			if holes := cracksOf(t, lvl); holes > 0 {
				t.Fatalf("frame %d (%d of %d pages): %d pixels show a back face through a hole", frame, st.Resident, st.Pages, holes)
			}
			if pixelsDiffer(got, full) == 0 && st.Queued == 0 && st.InFlight == 0 {
				settled = frame
				break
			}
		}
		if settled < 0 {
			t.Fatalf("persistent %v: never settled on the full picture: %d of %d pages, %s", persistent, st.Resident, st.Pages, st)
		}
		t.Logf("persistent %v: from %d root pages to the full picture in %d frames, %d of %d pages, %d uploaded",
			persistent, roots, settled, st.Resident, st.Pages, st.TotalUploaded)
		NaniteStreamPreload = true
		glext.Reinit()
	}
}

// A level bigger than its budget: four distinct rocks, a pool capped well
// under their pages. The camera goes to each rock in turn and stays a few
// frames: the pool never holds more than its slots, uploads at most a
// frame's share, never shows a crack, evicts what the last rock no longer
// needs, and — since one rock's detail and the others' roots fit — settles
// at each stop on exactly the picture of an unbounded pool.
func TestNaniteStreamUnderABudgetCap(t *testing.T) {
	lodProbe(t)
	var rocks []string
	for i := 0; i < 4; i++ {
		rocks = append(rocks, cookRockSeed(t, 5, 0.3+0.1*float32(i)))
	}
	lvl := &world.Level{}
	spots := []mgl32.Vec3{{-6, 0, -2}, {-2, 0, -2}, {2, 0, -2}, {6, 0, -2}}
	for i, r := range rocks {
		l := naniteSceneryLevel(t, r, true, []mgl32.Vec3{spots[i]}, 0.7)
		lvl.Scenery = append(lvl.Scenery, l.Scenery...)
	}
	views := []mgl32.Vec3{{-6, 0, -0.6}, {6, 0, -0.6}, {-2, 0, -0.6}, {2, 0, -0.6}, {-6, 0, -0.6}}

	// The unbounded pictures.
	freshNanitePool(t)
	want := make([][]uint8, len(views))
	for vi, v := range views {
		gfx.Cam.Position = v
		setLODThreshold(t, 1)
		streamFrame(t, lvl)
		want[vi] = streamFrame(t, lvl)
	}
	total := DebugNaniteStream().Pages

	prev := gfx.NaniteStreamPoolMB
	t.Cleanup(func() { gfx.NaniteStreamPoolMB = prev })
	gfx.NaniteStreamPoolMB = 4
	freshNanitePool(t)
	evicted, starved := 0, 0
	for vi, v := range views {
		gfx.Cam.Position = v
		setLODThreshold(t, 1)
		settled := -1
		var st NaniteStreamStats
		for frame := 0; frame < 60; frame++ {
			got := streamFrame(t, lvl)
			st = DebugNaniteStream()
			if st.Slots >= total {
				t.Fatalf("the pool has %d slots for %d pages: no cap", st.Slots, total)
			}
			if st.Resident > st.Slots {
				t.Fatalf("%d pages resident in %d slots", st.Resident, st.Slots)
			}
			if st.Uploaded > streamPagesPerFrame {
				t.Fatalf("a frame took in %d pages", st.Uploaded)
			}
			checkStreamRecords(t, fmt.Sprintf("view %d frame %d", vi, frame))
			if st.DirectWrites > 0 {
				t.Fatalf("view %d frame %d: %d record writes missed the staging ring", vi, frame, st.DirectWrites)
			}
			if holes := cracksOf(t, lvl); holes > 0 {
				t.Fatalf("view %d frame %d: %d pixels show a back face through a hole", vi, frame, holes)
			}
			if pixelsDiffer(got, want[vi]) == 0 && st.Queued == 0 && st.InFlight == 0 {
				settled = frame
				break
			}
		}
		evicted += st.TotalEvicted
		starved += st.TotalStarved
		if settled < 0 {
			t.Fatalf("view %d never settled on the unbounded picture (%d pixels off): %s", vi,
				pixelsDiffer(streamFrame(t, lvl), want[vi]), st)
		}
		t.Logf("view %d: settled in %d frames; %s, longest update %.2f ms", vi, settled, st,
			float64(st.MaxUpdateTime.Microseconds())/1000)
	}
	st := DebugNaniteStream()
	if st.TotalEvicted == 0 {
		t.Fatal("the capped pool never evicted a page")
	}
	t.Logf("%d slots for %d pages: %d uploaded, %d evicted, %d starved in all", st.Slots, total, st.TotalUploaded, st.TotalEvicted, st.TotalStarved)
}

// One mesh placed many times, near and far, in a pool too small for it: the
// load fills the pool coarsest first, so every page in it is in use by some
// placement, and least-recently-used finds nothing to evict. The asks of
// the near rock — many pixels of error — must still win the slots of pages
// whose use is worth less (Nanite's priority trade): pages come in, the near
// rock sharpens toward the unbounded picture, and nothing cracks.
func TestNaniteStreamTradesPagesByWorth(t *testing.T) {
	lodProbe(t)
	rock := cookRockSeed(t, 6, 0.5)
	poses := []mgl32.Vec3{{0, 0, 0}}
	for i := 0; i < 30; i++ {
		poses = append(poses, mgl32.Vec3{float32(i%6-3) * 3, float32(i/6-2) * 2, -25 - float32(i%5)*6})
	}
	lvl := naniteSceneryLevel(t, rock, true, poses, 0.6)
	gfx.Cam.Position = mgl32.Vec3{0.1, 0.05, 1.6}
	setLODThreshold(t, 1)
	freshNanitePool(t)
	streamFrame(t, lvl)
	want := streamFrame(t, lvl)
	total := DebugNaniteStream().Pages

	prev := gfx.NaniteStreamPoolMB
	t.Cleanup(func() { gfx.NaniteStreamPoolMB = prev })
	gfx.NaniteStreamPoolMB = 3
	freshNanitePool(t)
	first := streamFrame(t, lvl)
	st := DebugNaniteStream()
	if st.Slots >= total || st.Resident != st.Slots {
		t.Fatalf("%d slots, %d resident, of %d pages: not a full, capped pool", st.Slots, st.Resident, total)
	}
	offFirst, covered := lodCompare(want, first)
	var got []uint8
	for frame := 0; frame < 40; frame++ {
		got = streamFrame(t, lvl)
		checkStreamRecords(t, fmt.Sprintf("frame %d", frame))
		if holes := cracksOf(t, lvl); holes > 0 {
			t.Fatalf("frame %d: %d pixels show a back face through a hole", frame, holes)
		}
	}
	st = DebugNaniteStream()
	offLast, _ := lodCompare(want, got)
	t.Logf("%d slots for %d pages: %d uploaded, %d evicted; blocks off the unbounded picture %d -> %d of %d",
		st.Slots, total, st.TotalUploaded, st.TotalEvicted, offFirst, offLast, covered)
	if st.TotalUploaded == 0 || st.TotalEvicted == 0 {
		t.Fatal("a full pool of pages in use took nothing in for the near rock")
	}
	if offLast >= offFirst {
		t.Fatalf("the near rock did not sharpen: %d blocks off at first, %d after", offFirst, offLast)
	}
}
