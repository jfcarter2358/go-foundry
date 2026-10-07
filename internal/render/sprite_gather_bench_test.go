package render

// sprite_gather_bench_test.go — what a frame's sprite gathering costs on the
// CPU: the shadow cascades' caster gathers and the camera's, over a level of
// particle effects of every blend mode. No GL: gathering is pure CPU.

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/particle"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// spriteBenchLevel is a level of n emitters — alpha smoke, additive sparks and
// cutout debris in turn — each with per live particles, and as many authored
// sprites as emitters.
func spriteBenchLevel(n, per int) *world.Level {
	lvl := &world.Level{}
	blends := []gfx.SpriteBlend{gfx.SpriteBlendAlpha, gfx.SpriteBlendAdditive, gfx.SpriteBlendCutout}
	rng := uint32(12345)
	next := func() float32 {
		rng ^= rng << 13
		rng ^= rng >> 17
		rng ^= rng << 5
		return float32(rng&0xffff) / 65536
	}
	for i := 0; i < n; i++ {
		l := particle.NewLayer("l")
		l.Blend = blends[i%len(blends)]
		l.Texture = []string{"smoke", "spark", "debris", "fire"}[i%4]
		l.CastShadows = true
		s := particle.NewSystem(l, 0, uint32(i+1))
		centre := mgl32.Vec3{float32(i%10) * 6, 1, float32(i/10) * 6}
		for k := 0; k < per; k++ {
			s.Live = append(s.Live, particle.Particle{
				Pos:  centre.Add(mgl32.Vec3{next()*2 - 1, next() * 3, next()*2 - 1}),
				Size: 0.3 + next()*0.2, Life: 2, Age: next() * 2,
			})
		}
		e := world.NewEmitter("e", centre)
		e.Doc = &particle.Doc{Layers: []particle.Layer{l}}
		e.Systems = []*particle.System{s}
		lvl.Emitters = append(lvl.Emitters, e)

		sp := world.NewSprite("s", centre.Add(mgl32.Vec3{0, 2, 0}))
		sp.Texture = []string{"sign", "lamp"}[i%2]
		lvl.Sprites = append(lvl.Sprites, sp)
	}
	return lvl
}

// benchSpriteFrame is one frame's gathers: four cascades' casters, then the
// camera's for the cutout pass and again for the blended pass — as DrawWorld
// makes them, inside a frame.
func benchSpriteFrame(lvl *world.Level, cascades []Frustum, cam Frustum, eye mgl32.Vec3) int {
	drawFrameNo++
	drawFrameOpen = true
	defer func() { drawFrameOpen = false }()
	n := 0
	for i := range cascades {
		n += len(SpriteBatches(lvl, &cascades[i], eye, true))
	}
	n += len(cameraSpriteBatches(lvl, &cam, eye))
	n += len(cameraSpriteBatches(lvl, &cam, eye))
	return n
}

func spriteBenchViews() ([]Frustum, Frustum, mgl32.Vec3) {
	eye := mgl32.Vec3{27, 3, -10}
	proj := mgl32.Perspective(mgl32.DegToRad(70), 16.0/9, 0.1, 500)
	view := mgl32.LookAtV(eye, mgl32.Vec3{27, 1, 30}, mgl32.Vec3{0, 1, 0})
	cam := ExtractFrustum(proj.Mul4(view))
	var cascades []Frustum
	for i := 0; i < 4; i++ {
		r := float32(int(10) << i)
		o := mgl32.Ortho(-r, r, -r, r, -200, 200)
		lv := mgl32.LookAtV(mgl32.Vec3{27, 50, 27}, mgl32.Vec3{27, 0, 28}, mgl32.Vec3{0, 0, 1})
		cascades = append(cascades, ExtractFrustum(o.Mul4(lv)))
	}
	return cascades, cam, eye
}

func BenchmarkSpriteFrameGather(b *testing.B) {
	lvl := spriteBenchLevel(60, 150)
	cascades, cam, eye := spriteBenchViews()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSpriteFrame(lvl, cascades, cam, eye)
	}
}

// copyBatches deep-copies a gather, whose storage the next one reuses.
func copyBatches(bs []spriteBatch) []spriteBatch {
	out := make([]spriteBatch, len(bs))
	for i, b := range bs {
		out[i] = b
		out[i].Instances = append([]SpriteInstance(nil), b.Instances...)
	}
	return out
}

func sameBatches(t *testing.T, what string, got, want []spriteBatch) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d batches, want %d", what, len(got), len(want))
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.Texture != w.Texture || g.Blend != w.Blend || g.Billboard != w.Billboard || g.Lit != w.Lit ||
			g.FadingCutout != w.FadingCutout || g.FixedRight != w.FixedRight || g.FixedUp != w.FixedUp {
			t.Fatalf("%s: batch %d keyed %+v, want %+v", what, i, g, w)
		}
		if g.Depth != w.Depth {
			t.Errorf("%s: batch %d depth %v, want %v", what, i, g.Depth, w.Depth)
		}
		if len(g.Instances) != len(w.Instances) {
			t.Fatalf("%s: batch %d holds %d instances, want %d", what, i, len(g.Instances), len(w.Instances))
		}
		for k := range g.Instances {
			if g.Instances[k] != w.Instances[k] {
				t.Fatalf("%s: batch %d instance %d is %+v, want %+v", what, i, k, g.Instances[k], w.Instances[k])
			}
		}
	}
}

// A frame's shared gathering — one fill and one sort a layer, one gather for
// the camera's two passes — hands every pass exactly what gathering from
// scratch does, order and all.
func TestSpriteFrameSharingGathersTheSame(t *testing.T) {
	lvl := spriteBenchLevel(30, 80)
	cascades, cam, eye := spriteBenchViews()
	run := func(shared bool) (casters [][]spriteBatch, camera []spriteBatch, sprites, particles int) {
		prev := spriteFrameCaching
		spriteFrameCaching = shared
		defer func() { spriteFrameCaching = prev }()
		drawFrameNo++
		drawFrameOpen = true
		defer func() { drawFrameOpen = false }()
		for i := range cascades {
			casters = append(casters, copyBatches(SpriteBatches(lvl, &cascades[i], eye, true)))
		}
		// The cutout pass's gather, then the blended pass's.
		_ = cameraSpriteBatches(lvl, &cam, eye)
		camera = copyBatches(cameraSpriteBatches(lvl, &cam, eye))
		return casters, camera, DebugStats.Sprites, DebugStats.Particles
	}
	wantCasters, wantCamera, wantS, wantP := run(false)
	gotCasters, gotCamera, gotS, gotP := run(true)
	for i := range wantCasters {
		sameBatches(t, "cascade", gotCasters[i], wantCasters[i])
	}
	sameBatches(t, "camera", gotCamera, wantCamera)
	if gotS != wantS || gotP != wantP {
		t.Errorf("shared gathering counted %d sprites and %d particles, want %d and %d", gotS, gotP, wantS, wantP)
	}
	// And a second frame, its particles moved, is gathered afresh.
	for ei := range lvl.Emitters {
		for _, s := range lvl.Emitters[ei].Systems {
			for k := range s.Live {
				s.Live[k].Pos[1] += 0.5
			}
		}
	}
	_, wantCamera, _, _ = run(false)
	_, gotCamera, _, _ = run(true)
	sameBatches(t, "the next frame's camera", gotCamera, wantCamera)
}

// The camera's alpha layers are sorted as particle.System.Fill sorts them.
func TestSpriteLayerSortMatchesFill(t *testing.T) {
	lvl := spriteBenchLevel(3, 500)
	sys := lvl.Emitters[0].Systems[0]
	eye := mgl32.Vec3{1, 2, 3}
	// Ties: several particles at one place.
	for k := 0; k < 40; k++ {
		sys.Live[k].Pos = mgl32.Vec3{4, 4, 4}
		sys.Live[k].Size = 0.25 + float32(k)*0.001
	}
	want := make([]particle.Instance, sys.Count())
	want = want[:sys.Fill(want, eye, true)]
	got := make([]particle.Instance, sys.Count())
	got = got[:sys.Fill(got, eye, false)]
	sortInstancesBackToFront(got, make([]float32, len(got)), eye)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("instance %d: sorted %+v, Fill sorted %+v", i, got[i], want[i])
		}
	}
}

// The layers prefilled side by side on goroutines are the layers the gather
// fills itself, one after another.
func TestPrefilledSpriteLayersGatherTheSame(t *testing.T) {
	lvl := spriteBenchLevel(40, 90)
	_, cam, eye := spriteBenchViews()
	gather := func(prefill bool) []spriteBatch {
		drawFrameNo++
		drawFrameOpen = true
		defer func() { drawFrameOpen = false }()
		if prefill {
			prefillSpriteLayers(lvl, &cam, eye)
		}
		return copyBatches(cameraSpriteBatches(lvl, &cam, eye))
	}
	want := gather(false)
	got := gather(true)
	sameBatches(t, "prefilled", got, want)
}
