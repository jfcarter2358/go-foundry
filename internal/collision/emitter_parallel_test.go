package collision

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"foundry/internal/particle"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// sparkLevel is crowdLevel's yard with emitters scattered over it: fountains
// throwing sparks that land on the floor and the steps, half bouncing, half
// stopping, some one-shot bursts, some attached to walkers.
func sparkLevel(n int) *world.Level {
	lvl := crowdLevel(30)
	for i := 0; i < n; i++ {
		doc := particle.NewDoc(fmt.Sprintf("sparks%d", i))
		doc.Seed = uint32(17 + i)
		l := doc.Layers[0]
		l.Rate = 150
		l.SpeedMin, l.SpeedMax = 3, 7
		l.Gravity = 1
		l.Collision = []particle.CollideMode{particle.CollideBounce, particle.CollideStop, particle.CollideKill}[i%3]
		doc.Layers = []particle.Layer{l, l}
		doc.Layers[1].Name = "second"
		e := world.Emitter{
			ID:        fmt.Sprintf("em%d", i),
			Position:  mgl32.Vec3{float32(i%9)*4 - 16, 1 + float32(i%4), float32(i/9)*4 - 16},
			Enabled:   true,
			Emitting:  true,
			RateScale: 1,
			OneShot:   i%7 == 3,
		}
		if i%5 == 0 {
			e.Attach = world.Attach{Entity: lvl.Entities[1+i%30].ID}
		}
		e.SetDoc(doc)
		lvl.Emitters = append(lvl.Emitters, e)
	}
	return lvl
}

// TestParallelEmittersMatchSerial steps the same sparks serially and in
// parallel, colliding against the level, and holds every particle to bit
// equality frame by frame. Run with -race.
func TestParallelEmittersMatchSerial(t *testing.T) {
	defer func(was bool, c func(mgl32.Vec3, mgl32.Vec3, float32) (mgl32.Vec3, mgl32.Vec3, bool)) {
		world.ParallelEmitters, particle.Collide = was, c
	}(world.ParallelEmitters, particle.Collide)
	const frames = 150
	run := func(parallel bool) [][]string {
		world.ParallelEmitters = parallel
		lvl := sparkLevel(40)
		particle.Collide = func(from, to mgl32.Vec3, r float32) (mgl32.Vec3, mgl32.Vec3, bool) {
			return ParticleCollide(lvl, from, to, r)
		}
		var out [][]string
		for f := 0; f < frames; f++ {
			EntityUpdate(lvl, 1.0/60, true, nil)
			world.RefreshFrames(lvl, lvl.Frames)
			world.EmitterUpdate(lvl, 1.0/60, true)
			var st []string
			for i := range lvl.Emitters {
				em := &lvl.Emitters[i]
				s := em.ID
				for _, sys := range em.Systems {
					for _, p := range sys.Live {
						s += fmt.Sprintf(" %x,%x,%x/%x,%x,%x/%x", math.Float32bits(p.Pos[0]), math.Float32bits(p.Pos[1]), math.Float32bits(p.Pos[2]),
							math.Float32bits(p.Vel[0]), math.Float32bits(p.Vel[1]), math.Float32bits(p.Vel[2]), math.Float32bits(p.Age))
					}
					s += " |"
				}
				st = append(st, s)
			}
			out = append(out, st)
		}
		return out
	}
	serial := run(false)
	par := run(true)
	parts := 0
	for _, s := range serial[len(serial)-1] {
		parts += strings.Count(s, "/") / 2
	}
	if parts < 2000 {
		t.Fatalf("only %d particles alive at the end; the comparison is not exercising the step", parts)
	}
	for f := range serial {
		if len(serial[f]) != len(par[f]) {
			t.Fatalf("frame %d: %d emitters serial, %d parallel", f, len(serial[f]), len(par[f]))
		}
		for i := range serial[f] {
			if serial[f][i] != par[f][i] {
				t.Fatalf("frame %d emitter %d differs:\nserial   %.300s\nparallel %.300s", f, i, serial[f][i], par[f][i])
			}
		}
	}
}

func BenchmarkEmitterUpdate(b *testing.B) {
	defer func(was bool, c func(mgl32.Vec3, mgl32.Vec3, float32) (mgl32.Vec3, mgl32.Vec3, bool)) {
		world.ParallelEmitters, particle.Collide = was, c
	}(world.ParallelEmitters, particle.Collide)
	for _, par := range []bool{false, true} {
		b.Run(fmt.Sprintf("parallel=%v", par), func(b *testing.B) {
			world.ParallelEmitters = par
			lvl := sparkLevel(60)
			particle.Collide = func(from, to mgl32.Vec3, r float32) (mgl32.Vec3, mgl32.Vec3, bool) {
				return ParticleCollide(lvl, from, to, r)
			}
			for i := 0; i < 120; i++ {
				world.EmitterUpdate(lvl, 1.0/60, true)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				world.EmitterUpdate(lvl, 1.0/60, true)
			}
		})
	}
}
