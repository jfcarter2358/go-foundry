package stages

// ground_edit_audio.go — the sound of the water a ground_edit makes
// (docs/terrain-paint/PHASE_W5.md, item 5).
//
// With `water_sounds` on, ground_edit places positional looping sounds
// (procgen.Sound → the level's `sounds:`, under the generator's prefix like
// the W4 mist emitters, replaced by every splice):
//
//   - a ROAR at the foot of every waterfall (`roar_<river>_<n>`, the
//     water_falls loop), louder and carrying further the higher the drop and
//     the wider the sheet — placed first, so nothing else crowds it;
//   - along every river, a sound every `water_sound_spacing` metres of its
//     length (`flow_<river>_<k>`): water_rapids where the station is a
//     riffle crest, fast or white (riffles, rapids, a cascade's run), the
//     gentler water_river elsewhere, its gain from the river's width and
//     speed;
//   - at every confluence a rapids sound at the mouth (`join_<n>`), the
//     mixing water there being the loudest of a river's reach;
//   - along every pond, lake and marsh shore, gentle lapping
//     (`lap_<body>_<k>`, water_lap) at points of the shoreline spaced
//     `water_sound_spacing` apart, a few per body at most.
//
// SPACING. A new sound is dropped when one already placed is within 0.7 of
// the spacing (a fall's roar keeps a wider berth: its own reach), so
// sounds never stack where rivers meet or bend back. Each carries little
// further than the spacing (max distance ~1.2–1.5× it), so walking a river
// you hear two or three at a time; world.SoundUpdate gives a loop's voice
// back past its max distance (the mixer keeps only eight loops), so a long
// river costs nothing beyond earshot. Pitches vary a little per sound
// (hashed from its name, deterministic), so neighbours do not phase.
//
// The clips ship as generated loops (audio/audio_water_sounds.go): no CC0
// recordings were at hand. Any clip of the same name under a project's
// sounds/ replaces them.
//
// Off by default: a document from before W5 splices no sounds until its
// author turns them on (the river, stream, pond, lake and marsh brush
// presets do).

import (
	"fmt"
	"hash/fnv"
	"math"
	"sort"

	"foundry/internal/procgen"
	"foundry/internal/procgen/terrain"
)

// Water sound clips (resources/sounds).
const (
	waterClipRiver  = "water_river"
	waterClipRapids = "water_rapids"
	waterClipFalls  = "water_falls"
	waterClipLap    = "water_lap"
)

// waterSoundsMaxPerBody caps the lapping sounds on one shore.
const waterSoundsMaxPerBody = 8

// waterSoundParamDefs are ground_edit's audio params (appended to the
// river params).
func waterSoundParamDefs() []procgen.ParamDef {
	return []procgen.ParamDef{
		{Name: "water_sounds", Added: true, RectCovered: true, Type: procgen.ParamBool, Label: "Water sounds",
			Default: procgen.Bool(false),
			Doc: "Place looping positional sounds: the flow along every river (rapids at riffles), a roar at each waterfall, " +
				"the rush at confluences and lapping along pond and lake shores (water_river, water_rapids, water_falls, " +
				"water_lap under sounds/)."},
		{Name: "water_sound_spacing", Added: true, RectCovered: true, Type: procgen.ParamFloat, Label: "Sound spacing (m)",
			Default: procgen.Float(24), Min: 6, Max: 200, Step: 1,
			Doc: "How far apart the water sounds are placed along rivers and shores; each carries a little further than this."},
	}
}

// waterSoundPlan collects the sounds with the spacing rule.
type waterSoundPlan struct {
	out []procgen.Sound
	// keep is each placed sound's berth (x, z, radius).
	keep [][3]float64
}

// free reports whether a sound at (x, z) keeps clear of every placed one
// by r (and of their own berths).
func (p *waterSoundPlan) free(x, z, r float64) bool {
	for _, k := range p.keep {
		if math.Hypot(x-k[0], z-k[1]) < max(r, k[2]) {
			return false
		}
	}
	return true
}

// add places a sound with a berth.
func (p *waterSoundPlan) add(s procgen.Sound, berth float64) {
	p.out = append(p.out, s)
	p.keep = append(p.keep, [3]float64{float64(s.Position.X), float64(s.Position.Z), berth})
}

// soundPitch is a name's small deterministic detune, 0.92..1.08.
func soundPitch(id string) float32 {
	h := fnv.New32a()
	h.Write([]byte(id))
	return float32(round2(0.92 + 0.16*float64(h.Sum32()%1000)/999))
}

// placeWaterSounds places the water sounds (file comment).
func placeWaterSounds(rc *procgen.RunContext, g *terrain.Grid, rv *groundRivers, ws *groundWater) {
	if !rc.Params.Bool("water_sounds") {
		return
	}
	sp := rc.Params.Float("water_sound_spacing")
	if sp <= 0 {
		sp = 24
	}
	plan := planWaterSounds(g, rv, ws, sp)
	rc.PlaceSound(plan...)
	n := map[string]int{}
	for _, s := range plan {
		n[s.Clip]++
	}
	rc.Logf("water sounds: %d (%d flow, %d rapids, %d falls, %d lapping)", len(plan),
		n[waterClipRiver], n[waterClipRapids], n[waterClipFalls], n[waterClipLap])
}

// planWaterSounds is the sounds for rivers rv and standing water ws at
// spacing sp (pure: the test calls it).
func planWaterSounds(g *terrain.Grid, rv *groundRivers, ws *groundWater, sp float64) []procgen.Sound {
	p := &waterSoundPlan{}
	pt := func(x, y, z float64) procgen.Point3 {
		return procgen.Point3{X: float32(round2(x)), Y: float32(round2(y)), Z: float32(round2(z))}
	}
	if rv.any() {
		// 1. The falls' roar.
		names := fallsNames(rv.res.Falls)
		for i, f := range rv.res.Falls {
			drop := f.Lip - f.Foot
			reach := noiseClamp(sp*1.4+8*drop+2*f.Width, sp, 140)
			id := "roar_" + names[i][len("falls_"):]
			s := procgen.Sound{ID: id, Clip: waterClipFalls,
				Position:    pt(f.X+f.DirX*1.5, f.Foot+0.5, f.Z+f.DirZ*1.5),
				Gain:        float32(round2(noiseClamp(0.45+0.1*drop+0.05*f.Width, 0.45, 1.4))),
				Pitch:       soundPitch(id) * float32(round2(noiseClamp(1.1-0.02*drop, 0.85, 1.1))),
				RefDistance: float32(round2(noiseClamp(2+0.5*drop+0.3*f.Width, 2, 12))),
				MaxDistance: float32(round2(reach))}
			if f.Cascade && !p.free(f.X, f.Z, sp*0.5) {
				continue // a cascade's steps share one roar
			}
			p.add(s, max(sp*0.7, reach*0.35))
		}
		// 2. Confluences.
		for i, c := range rv.res.Confluences {
			if !p.free(c.X, c.Z, sp*0.7) {
				continue
			}
			id := fmt.Sprintf("join_%d", i+1)
			p.add(procgen.Sound{ID: id, Clip: waterClipRapids, Position: pt(c.X, c.Level+0.3, c.Z),
				Gain: 0.7, Pitch: soundPitch(id), RefDistance: 3, MaxDistance: float32(round2(sp * 1.3))}, sp*0.7)
		}
		// 3. Along each river.
		for _, r := range rv.res.Rivers {
			if len(r.Stations) == 0 {
				continue
			}
			k := 0
			next := sp * 0.5
			for si := range r.Stations {
				st := &r.Stations[si]
				if st.S < next || !p.free(st.X, st.Z, sp*0.7) {
					// Not yet, or crowded (a fall's roar, a confluence, a
					// river doubling back): try the stations on from here.
					continue
				}
				next = st.S + sp
				// The loudest station within half a spacing decides
				// the clip: a riffle a few metres on is heard here.
				rapid := 0.0
				for sj := si; sj < len(r.Stations) && r.Stations[sj].S < st.S+sp*0.5; sj++ {
					q := &r.Stations[sj]
					rapid = max(rapid, q.Riffle*0.6+q.Foam*0.8+noiseClamp((q.Speed-1)/1.5, 0, 1)*0.6)
				}
				clip := waterClipRiver
				if rapid > 0.85 {
					clip = waterClipRapids
				}
				k++
				id := fmt.Sprintf("flow_%s_%d", safeName(r.Name, "river"), k)
				gain := noiseClamp(0.3+0.06*st.Width+0.25*rapid, 0.3, 1.1)
				p.add(procgen.Sound{ID: id, Clip: clip, Position: pt(st.X, st.Surface+0.2, st.Z),
					Gain: float32(round2(gain)), Pitch: soundPitch(id),
					RefDistance: float32(round2(noiseClamp(1.5+0.4*st.Width, 1.5, 8))),
					MaxDistance: float32(round2(sp * 1.25))}, sp*0.7)
			}
		}
	}
	// 4. Shores.
	if ws != nil && ws.wet() {
		for bi, pts := range shorePoints(g, ws.water) {
			body := ws.water.Bodies[bi]
			kept := 0
			for _, q := range pts {
				if kept >= waterSoundsMaxPerBody {
					break
				}
				if !p.free(q[0], q[1], sp*0.7) {
					continue
				}
				kept++
				id := fmt.Sprintf("lap_%d_%d", bi+1, kept)
				p.add(procgen.Sound{ID: id, Clip: waterClipLap, Position: pt(q[0], body.Level+0.15, q[1]),
					Gain: 0.45, Pitch: soundPitch(id), RefDistance: 2, MaxDistance: float32(round2(sp * 1.1))}, sp*0.7)
			}
		}
	}
	return p.out
}

// shorePoints is each standing body's shoreline — wet samples with a dry
// neighbour — as world (x, z) points ordered round the body (by angle from
// its centre), so a greedy pick spaces them along it.
func shorePoints(g *terrain.Grid, w *terrain.WaterResult) [][][2]float64 {
	out := make([][][2]float64, len(w.Bodies))
	S := g.S()
	sum := make([][3]float64, len(w.Bodies))
	for j := 1; j < S-1; j++ {
		for i := 1; i < S-1; i++ {
			k := j*S + i
			b := w.Body[k]
			if b < 0 || int(b) >= len(w.Bodies) {
				continue
			}
			if w.Body[k-1] >= 0 && w.Body[k+1] >= 0 && w.Body[k-S] >= 0 && w.Body[k+S] >= 0 {
				continue
			}
			x, z := g.MinX+float64(i)*g.Cell, g.MinZ+float64(j)*g.Cell
			out[b] = append(out[b], [2]float64{x, z})
			sum[b][0] += x
			sum[b][1] += z
			sum[b][2]++
		}
	}
	for b := range out {
		if sum[b][2] == 0 {
			continue
		}
		cx, cz := sum[b][0]/sum[b][2], sum[b][1]/sum[b][2]
		pts := out[b]
		sort.SliceStable(pts, func(a, c int) bool {
			return math.Atan2(pts[a][1]-cz, pts[a][0]-cx) < math.Atan2(pts[c][1]-cz, pts[c][0]-cx)
		})
	}
	return out
}

// noiseClamp clamps v to [lo, hi].
func noiseClamp(v, lo, hi float64) float64 { return math.Min(math.Max(v, lo), hi) }
