package collision

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strconv"
	"testing"

	"foundry/internal/hooks"
	"foundry/internal/model"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// crowdDoc is stepAnimDoc with a looping walk clip that actually turns the
// bone, so a posed palette is not the identity and a wrong pose shows.
func crowdDoc() *model.ModelDoc {
	d := stepAnimDoc()
	d.Bones = append(d.Bones, model.ModelBone{Name: "arm", Parent: 0, Head: mgl32.Vec3{0, 1, 0}, Tail: mgl32.Vec3{0, 2, 0}})
	d.Bones[0].Tail = mgl32.Vec3{0, 1, 0}
	d.Weights = [][]model.ModelVertexWeight{{{Bone: 1, Weight: 1}}}
	swing := func(a float32) model.ModelBonePose {
		p := model.IdentBonePose()
		p.Rot = mgl32.QuatRotate(a, mgl32.Vec3{0, 0, 1})
		return p
	}
	d.Animations = append(d.Animations, model.ModelClip{
		Name: "walk", Duration: 0.7, FPS: 30, Loop: true,
		Tracks: []model.ModelBoneTrack{{Bone: 1, Keys: []model.ModelBoneKey{
			{Time: 0, ModelBonePose: swing(-0.6)},
			{Time: 0.35, ModelBonePose: swing(0.6)},
			{Time: 0.7, ModelBonePose: swing(-0.6)},
		}}},
	})
	return d
}

// crowdLevel is a walled yard with steps in it, a hut built of an entity's own
// brushes, and n walkers — most animated, some on a looping waypoint path,
// some solid to each other — dropped in at deterministic random places.
func crowdLevel(n int) *world.Level {
	var extra []world.Brush
	for i := 0; i < 6; i++ {
		extra = append(extra, world.NewBoxBrush(fmt.Sprintf("step%d", i),
			mgl32.Vec3{float32(i*5 - 14), 0.15 + float32(i%3)*0.1, float32(i%2*8 - 4)},
			mgl32.Vec3{1.5, 0.15 + float32(i%3)*0.1, 1.5}, "concrete"))
	}
	brushes := world.NewHollowRoom("r", mgl32.Vec3{-20, 0, -20}, mgl32.Vec3{20, 6, 20}, 0.5, "concrete")
	brushes = append(brushes, extra...)
	ch := world.Chunk{ID: "yard", Brushes: brushes}
	ch.RecomputeBounds()
	lvl := &world.Level{Chunks: []world.Chunk{ch}}
	lvl.Player.StepHeight = stepUpAllowance

	hut := hutEntity("hut", mgl32.Vec3{8, 0.25, 8})
	lvl.Entities = append(lvl.Entities, hut)
	hc := world.Chunk{ID: world.EntityGeometryChunkID(hut.ID), Scale: 1, FrameOwner: hut.ID}
	hc.FromEntity = hut.ID
	hc.Brushes = world.CloneEntityBrushes(hut.Brushes)
	hc.RecomputeBounds()
	lvl.Chunks = append(lvl.Chunks, hc)

	lvl.WaypointAnimations = []world.WaypointAnimation{{
		ID: "patrol", Length: 4,
		Points: []world.Waypoint{
			{Position: mgl32.Vec3{-10, 0.6, -10}, Scale: mgl32.Vec3{1, 1, 1}, Time: 0, Events: []string{"start"}},
			{Position: mgl32.Vec3{-4, 0.6, -10}, Scale: mgl32.Vec3{1, 1, 1}, Time: 2, Events: []string{"mid"}},
			{Position: mgl32.Vec3{-4, 0.6, -4}, Scale: mgl32.Vec3{1, 1, 1}, Time: 4, Events: []string{"end"}},
		},
	}}

	r := rand.New(rand.NewSource(3))
	for i := 0; i < n; i++ {
		feet := mgl32.Vec3{r.Float32()*34 - 17, 0.2 + r.Float32()*2, r.Float32()*34 - 17}
		e := stepAnimWalker(fmt.Sprintf("w%d", i), feet)
		if i%5 != 0 {
			e.Models[0].Anim = world.NewEntityAnim(crowdDoc())
			e.Models[0].Animations = append(e.Models[0].Animations, world.EntityAnimation{Name: "walk"})
			e.Models[0].Anim.SetClip("walk", 0)
			e.Models[0].Anim.Time = r.Float32()
		}
		e.Collision.Entities = i%3 == 0
		e.Velocity = mgl32.Vec3{r.Float32()*4 - 2, 0, r.Float32()*4 - 2}
		lvl.Entities = append(lvl.Entities, e)
	}
	if err := world.RebuildIndexes(lvl); err != nil {
		panic(err)
	}
	world.RefreshFrames(lvl, lvl.Frames)
	for i := 1; i < len(lvl.Entities); i += 7 {
		world.PlayWaypoints(lvl, &lvl.Entities[i], &lvl.WaypointAnimations[0])
	}
	return lvl
}

// crowdState is everything a frame changed, floats written as their bits.
func crowdState(lvl *world.Level) []string {
	out := make([]string, 0, len(lvl.Entities))
	var b []byte
	f := func(v float32) { b = strconv.AppendUint(append(b, ' '), uint64(math.Float32bits(v)), 16) }
	v3 := func(v mgl32.Vec3) { f(v[0]); f(v[1]); f(v[2]) }
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		b = append(b[:0], e.ID...)
		v3(e.Position)
		v3(e.Velocity)
		v3(e.Rotation)
		v3(e.Scale)
		f(e.StepOffset)
		b = fmt.Appendf(b, " frame=%q chunk=%q ground=%v", e.Frame, e.CurrentChunk, e.Collision.OnGround)
		for _, m := range e.Models {
			if m.Anim != nil {
				b = fmt.Appendf(b, " [%s %d %v", m.Playing(), m.Anim.Clip, m.Anim.Playing)
				f(m.Anim.Time)
				for _, p := range m.Anim.Palette {
					for _, x := range p {
						f(x)
					}
				}
				b = append(b, ']')
			}
		}
		if e.Waypoints != nil {
			b = fmt.Appendf(b, " wp=%+v", *e.Waypoints)
		}
		out = append(out, string(b))
	}
	return out
}

// TestParallelEntityUpdateMatchesSerial runs one crowd through the serial loop
// and an identical one through the parallel pass, and holds every entity's
// state and every event, in order, to bit equality frame by frame. Run it
// with -race: it is also the parallel pass's race test.
func TestParallelEntityUpdateMatchesSerial(t *testing.T) {
	defer func(was bool) { ParallelEntityUpdate = was }(ParallelEntityUpdate)
	const frames = 240
	const dt = float32(1.0 / 60)
	type frame struct {
		state []string
		anim  []hooks.AnimEvent
		wp    []hooks.WaypointEvent
		pairs [][2]string
	}
	run := func(parallel bool) []frame {
		lvl := crowdLevel(120)
		ParallelEntityUpdate = true
		if _, ok := parallelEntityPass(lvl); !ok {
			t.Fatal("the crowd level does not qualify for the parallel pass; the test would compare serial with serial")
		}
		ParallelEntityUpdate = parallel
		hooks.TakeAnimEvents()
		hooks.TakeWaypointEvents()
		hooks.TakeCollisionPairs()
		var out []frame
		for f := 0; f < frames; f++ {
			// Push the walkers about the way a controller would, so bodies
			// keep stepping, landing and colliding.
			for i := range lvl.Entities {
				e := &lvl.Entities[i]
				if e.AI != nil && f%30 == i%30 {
					a := float64(f*7+i) * 0.37
					e.Velocity[0] = float32(math.Cos(a)) * 2.5
					e.Velocity[2] = float32(math.Sin(a)) * 2.5
				}
			}
			EntityUpdate(lvl, dt, true, nil)
			world.RefreshFrames(lvl, lvl.Frames)
			out = append(out, frame{
				state: crowdState(lvl),
				anim:  append([]hooks.AnimEvent(nil), hooks.TakeAnimEvents()...),
				wp:    append([]hooks.WaypointEvent(nil), hooks.TakeWaypointEvents()...),
				pairs: append([][2]string(nil), hooks.TakeCollisionPairs()...),
			})
		}
		return out
	}
	serial := run(false)
	parallel := run(true)
	events := 0
	for f := range serial {
		s, p := serial[f], parallel[f]
		for i := range s.state {
			if s.state[i] != p.state[i] {
				t.Fatalf("frame %d entity %d differs:\nserial   %s\nparallel %s", f, i, s.state[i], p.state[i])
			}
		}
		if !reflect.DeepEqual(s.anim, p.anim) {
			t.Fatalf("frame %d anim events differ:\nserial   %v\nparallel %v", f, s.anim, p.anim)
		}
		if !reflect.DeepEqual(s.wp, p.wp) {
			t.Fatalf("frame %d waypoint events differ:\nserial   %v\nparallel %v", f, s.wp, p.wp)
		}
		if !reflect.DeepEqual(s.pairs, p.pairs) {
			t.Fatalf("frame %d collision pairs differ", f)
		}
		events += len(s.anim) + len(s.wp)
	}
	if events < 50 {
		t.Fatalf("only %d events in %d frames; the event ordering is not being exercised", events, frames)
	}
}

// BenchmarkEntityUpdateCrowd is one EntityUpdate over the crowd, serial and
// parallel.
func BenchmarkEntityUpdateCrowd(b *testing.B) {
	defer func(was bool) { ParallelEntityUpdate = was }(ParallelEntityUpdate)
	for _, par := range []bool{false, true} {
		b.Run(fmt.Sprintf("parallel=%v", par), func(b *testing.B) {
			ParallelEntityUpdate = par
			lvl := crowdLevel(200)
			for i := 0; i < b.N; i++ {
				EntityUpdate(lvl, 1.0/60, true, nil)
				world.RefreshFrames(lvl, lvl.Frames)
				hooks.TakeAnimEvents()
				hooks.TakeWaypointEvents()
				hooks.TakeCollisionPairs()
			}
		})
	}
}
