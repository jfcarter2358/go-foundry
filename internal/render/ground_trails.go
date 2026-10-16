package render

// ground_trails.go — the trail map: footprints pressed into sand and snow
// (docs/trails/SPEC.md).
//
// A walker's steps on soft ground queue world.TrailStamps (sim/footsteps.go).
// Once a frame, before anything draws, this stamps them into the trail map —
// a TrailSize² RG16F texture over TrailSpan metres of ground around the
// player (world.TrailFocus), wrapped toroidally so the window follows the
// player without copying (ground_trail_comp.glsl says how) — and decays what
// is there: the red channel fills back in over TrailFastSeconds, the green
// over TrailSlowSeconds.
//
// The splat shader reads it (surface_fx.glsl, fxTrail): gfx.GroundTrail is
// the frame's map and window, which gfx/material_fx.go binds on
// FxUnitTrail for any splat with a soft layer and turns into the FX_TRAIL
// bit. A pixel takes the trail's press times its soft layers' depth, mixed
// between the channels by their fill, and shows it as a dent: parallax, the
// dent's own normal and a darkening in it.
//
// Nothing here is saved: a session's tracks are a picture. A new level, or
// the player jumping further than the window in one frame, clears it.

import (
	"log"
	"math"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// The trail map's size: TrailSize texels over TrailSpan metres (2.3 cm a
// texel, a boot print ~12 texels by 5 — at 4.7 cm a print was a blurred
// bump with no floor), 16 MB.
const (
	TrailSize = 2048
	TrailSpan = 48.0
	// TrailFastSeconds and TrailSlowSeconds are the channels' time
	// constants: a sand print is mostly gone in a couple of minutes, a snow
	// print in an hour.
	TrailFastSeconds = 45.0
	TrailSlowSeconds = 1800.0
	// trailIdleEvery is how often the map is updated when nothing is
	// stamping; trailFastEvery and trailSlowEvery how often each channel
	// decays (a factor of about 0.995 and 0.99).
	trailIdleEvery = 0.25
	trailFastEvery = 0.25
	trailSlowEvery = 20.0
	// trailRecentre is how far the focus may wander from the window's
	// centre before it moves (metres); moving it clears the strip that
	// scrolls in, so it moves in steps rather than every texel.
	trailRecentre  = 4.0
	trailMaxStamps = 32
)

// GroundTrails switches the trail map (FOUNDRY_TRAILS=0 turns it off; the
// footsteps' sounds and puffs go on).
var GroundTrails = gfx.EnvFlag("FOUNDRY_TRAILS", true)

// groundTrailsFill (FOUNDRY_TRAILS_FILL=1, for measuring only) centres the
// map on the camera and presses a field of footprints — a pair every half
// metre over 30 m — around it whenever the map is cleared, so a bench with a pinned camera
// measures the splat's worst case: prints over all the ground in view
// (docs/trails/SPEC.md, "Budgets").
var groundTrailsFill = gfx.EnvFlag("FOUNDRY_TRAILS_FILL", false)

// fillStamps is that field around f.
func fillStamps(f [2]float32) []world.TrailStamp {
	var out []world.TrailStamp
	for z := float32(-15); z < 15; z += 0.5 {
		for x := float32(-15); x < 15; x += 0.5 {
			for _, side := range []float32{-0.11, 0.11} {
				c := mgl32.Vec2{f[0] + x + side, f[1] + z}
				out = append(out, world.TrailStamp{A: c.Sub(mgl32.Vec2{0, 0.075}), B: c.Add(mgl32.Vec2{0, 0.075}),
					Radius: 0.06, Strength: 1, Rim: 0.35})
			}
		}
	}
	return out
}

type groundTrailPass struct {
	tried, ok bool
	prog      progUniforms
	tex       uint32
	lvl       *world.Level
	// origin is the window's first world cell; valid once an update ran.
	origin, prev [2]int32
	valid        bool
	// since, sinceFast and sinceSlow are the time not yet decayed (s): an
	// update's, and each channel's own. A channel decays only once its
	// factor is one RG16F can hold — near 1 a half float's step is 1/2048,
	// so a factor of 0.9999 a frame rounds to a whole step DOWN and the
	// slow channel would fade thirty times too fast — so each banks its
	// time until a decay is a percent or so (fast every trailFastEvery,
	// slow every trailSlowEvery). last is the frame clock they are
	// measured from.
	since, sinceFast, sinceSlow float64
	last                        float64
	// stamps is the frame's drained stamps (reused).
	stamps []world.TrailStamp
}

var groundTrail groundTrailPass

func (g *groundTrailPass) ready() bool {
	if g.tried {
		return g.ok
	}
	g.tried = true
	p, err := giComputeProgram(giShaderSource("ground_trail_comp.glsl", ""))
	if err != nil {
		log.Printf("trails: ground trails unavailable: %v", err)
		return false
	}
	g.prog = p
	gl.GenTextures(1, &g.tex)
	gl.BindTexture(gl.TEXTURE_2D, g.tex)
	gl.TexStorage2D(gl.TEXTURE_2D, 1, gl.RG16F, TrailSize, TrailSize)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.REPEAT)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.REPEAT)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	g.ok = true
	return true
}

// trailTexel is metres a texel.
const trailTexel = TrailSpan / TrailSize

// groundTrailsUpdate stamps the frame's footprints and decays the map,
// and publishes it for the splat (gfx.GroundTrail). now is the frame clock.
func groundTrailsUpdate(lvl *world.Level, now float64) {
	g := &groundTrail
	stamps := world.DrainTrailStamps(g.stamps[:0])
	g.stamps = stamps
	if !GroundTrails || lvl == nil || !g.ready() {
		gfx.GroundTrail.On = false
		return
	}
	clearAll := !g.valid || g.lvl != lvl
	if clearAll {
		g.lvl = lvl
		g.since, g.sinceFast, g.sinceSlow, g.last = 0, 0, 0, now
	}
	dt := now - g.last
	if dt < 0 || dt > 1 {
		dt = 0 // a clock reset, or a frame after a long stall: no decay jump
	}
	g.last = now
	g.since += dt
	g.sinceFast += dt
	g.sinceSlow += dt

	// The window: recentred on the focus once it strays.
	f := world.TrailFocus
	if groundTrailsFill {
		// A bench pins the camera, not the player: the prints go where it
		// looks.
		f = gfx.Cam.Position
	}
	half := float32(TrailSize / 2)
	cx := float32(g.origin[0]) + half
	cz := float32(g.origin[1]) + half
	fx, fz := f.X()/trailTexel, f.Z()/trailTexel
	moved := false
	if clearAll || absf(fx-cx)*trailTexel > trailRecentre || absf(fz-cz)*trailTexel > trailRecentre {
		next := [2]int32{int32(math.Floor(float64(fx - half))), int32(math.Floor(float64(fz - half)))}
		// A jump further than the window keeps nothing.
		if !clearAll && (absi(next[0]-g.origin[0]) >= TrailSize || absi(next[1]-g.origin[1]) >= TrailSize) {
			clearAll = true
		}
		g.prev = g.origin
		if clearAll {
			g.prev = next
		}
		g.origin = next
		moved = true
	} else {
		g.prev = g.origin
	}

	if clearAll && groundTrailsFill {
		stamps = append(stamps, fillStamps([2]float32{f.X(), f.Z()})...)
	}
	full := clearAll || moved || g.since >= trailIdleEvery
	if full || len(stamps) > 0 {
		ProfileBegin("ground trails")
		p := &g.prog
		gl.UseProgram(p.prog)
		gl.Uniform2i(p.U("origin"), g.origin[0], g.origin[1])
		gl.Uniform1i(p.U("size"), TrailSize)
		gl.Uniform1f(p.U("texel"), trailTexel)
		gl.BindImageTexture(0, g.tex, 0, false, 0, gl.READ_WRITE, gl.RG16F)
		if full {
			// The whole window: what scrolled in is cleared, both channels
			// decayed, and the first batch of stamps pressed.
			decay := [2]float32{1, 1}
			if g.sinceFast >= trailFastEvery {
				decay[0] = float32(math.Exp(-g.sinceFast / TrailFastSeconds))
				g.sinceFast = 0
			}
			if g.sinceSlow >= trailSlowEvery {
				decay[1] = float32(math.Exp(-g.sinceSlow / TrailSlowSeconds))
				g.sinceSlow = 0
			}
			g.since = 0
			n := min(len(stamps), trailMaxStamps)
			g.dispatch(g.origin, [2]int32{TrailSize, TrailSize}, g.prev, decay, clearAll, stamps[:n])
			stamps = stamps[n:]
		}
		// The rest of the stamps (all of them on a frame that only stamps):
		// each batch over only the cells it reaches.
		for len(stamps) > 0 {
			n := min(len(stamps), trailMaxStamps)
			base, ext, ok := g.stampRect(stamps[:n])
			if ok {
				gl.MemoryBarrier(gl.SHADER_IMAGE_ACCESS_BARRIER_BIT)
				g.dispatch(base, ext, g.origin, [2]float32{1, 1}, false, stamps[:n])
			}
			stamps = stamps[n:]
		}
		gl.MemoryBarrier(gl.TEXTURE_FETCH_BARRIER_BIT | gl.SHADER_IMAGE_ACCESS_BARRIER_BIT)
		gl.UseProgram(gfx.Program)
		ProfileEnd()
	}
	g.valid = true

	gfx.GroundTrail.On = true
	gfx.GroundTrail.Tex = g.tex
	gfx.GroundTrail.Min = [2]float32{float32(g.origin[0]) * trailTexel, float32(g.origin[1]) * trailTexel}
	gfx.GroundTrail.Span = TrailSpan
}

// dispatch runs the update over the cells [base, base+ext) of the window,
// with prev the last update's origin (the window's own for a part), the
// channels' decay and the stamps.
func (g *groundTrailPass) dispatch(base, ext, prev [2]int32, decay [2]float32, clearAll bool, stamps []world.TrailStamp) {
	p := &g.prog
	var ab, sp [4 * trailMaxStamps]float32
	for i, s := range stamps {
		ab[4*i], ab[4*i+1], ab[4*i+2], ab[4*i+3] = s.A.X(), s.A.Y(), s.B.X(), s.B.Y()
		sp[4*i], sp[4*i+1], sp[4*i+2] = max(s.Radius, 0.01), s.Strength, s.Rim
	}
	gl.Uniform2i(p.U("prevOrigin"), prev[0], prev[1])
	gl.Uniform2f(p.U("decay"), decay[0], decay[1])
	gl.Uniform1i(p.U("clearAll"), boolToInt32(clearAll))
	gl.Uniform2i(p.U("cellBase"), base[0], base[1])
	gl.Uniform2i(p.U("extent"), ext[0], ext[1])
	gl.Uniform1i(p.U("stampCount"), int32(len(stamps)))
	if len(stamps) > 0 {
		gl.Uniform4fv(p.U("stampAB"), int32(len(stamps)), &ab[0])
		gl.Uniform4fv(p.U("stampP"), int32(len(stamps)), &sp[0])
	}
	gl.DispatchCompute(uint32(ext[0]+7)/8, uint32(ext[1]+7)/8, 1)
}

// stampRect is the cells stamps reach (their capsules and rims), clipped to
// the window; ok false when that is nothing.
func (g *groundTrailPass) stampRect(stamps []world.TrailStamp) (base, ext [2]int32, ok bool) {
	lo := [2]float32{math.MaxFloat32, math.MaxFloat32}
	hi := [2]float32{-math.MaxFloat32, -math.MaxFloat32}
	for _, s := range stamps {
		r := max(s.Radius, 0.01) + 0.1 // the rim's reach
		lo[0], lo[1] = min(lo[0], s.A.X()-r, s.B.X()-r), min(lo[1], s.A.Y()-r, s.B.Y()-r)
		hi[0], hi[1] = max(hi[0], s.A.X()+r, s.B.X()+r), max(hi[1], s.A.Y()+r, s.B.Y()+r)
	}
	for k := 0; k < 2; k++ {
		a := max(int32(math.Floor(float64(lo[k]/trailTexel))), g.origin[k])
		b := min(int32(math.Floor(float64(hi[k]/trailTexel)))+1, g.origin[k]+TrailSize)
		if b <= a {
			return base, ext, false
		}
		base[k], ext[k] = a, b-a
	}
	return base, ext, true
}

func absf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func absi(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
