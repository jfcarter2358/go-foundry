package terrain

// path.go — FOOTPATHS laid on the finished ground (docs/terrain-paint/
// PHASE_P4.md; the user's page is docs/guide/36-terrain-paint.md, "Paths").
//
// A path is drawn as a stroke and kept as a spline with a width per point,
// as a river is (river.go), and a ground_edit lays it after its rivers
// (procgen/stages ground_edit_path*.go). Laying a path does four things,
// each a pure function of the heights, the spline and the water already
// there:
//
//  1. FLATTEN (path_lay.go). The ground along the centreline is smoothed
//     along the length (a box filter over Smooth metres, three passes,
//     reading only dry stations so a ford never drags the bed down into the
//     river), and the cross-section is levelled to it: within the half
//     width the ground goes to the target, beyond it the change eases out
//     over Blend metres. On a slope steeper than MaxGrade the target may be
//     stepped (Steps: treads StepRise apart). Nothing is flattened in water
//     or in a river's channel, and the flatten fades out over the last two
//     metres before water, so a path runs down the natural bank to a ford.
//     A style that sets Flatten false (stepping stones, a game trail)
//     leaves the ground alone.
//  2. MASKS (path_lay.go, PathMaskNames). One SURFACE mask per style — the
//     splat a material entry paints the style's texture by — with a worn,
//     irregular edge: the half width wobbles by Edge (a fraction) on an fBm
//     of EdgeSize metres, so a trail's edge is ragged rather than a ruled
//     line. A WEAR mask, 1 along the centreline and easing to 0 over Wear
//     metres past the edge (scaled by the style's wear), thins and shortens
//     grass toward the middle and leaves a soft wear band at the sides. A
//     CLEAR mask keeps large scatter (rocks, logs) off the path and a
//     little beyond. The DIRECTION masks hold the path's tangent (0.5 +
//     0.5·t·strength per axis, strength 1 on the path easing out over the
//     wear band) — what `orient_to` reads where paths lie. No surface, wear
//     or clear is written under standing or running water.
//  3. STONES (path_stones.go): flagstones filling a rock path's width,
//     stepping stones a stride apart down the middle of a stepping-stone
//     path, slightly zig-zagged, and — where any path but a game trail
//     crosses water — a ford: stepping stones whose tops stand above the
//     surface, sitting on the bed. Laid out here (positions, sizes, yaw
//     along the path, no two overlapping); the stage seats each on the
//     final ground as a rock variant, scaled to the size and sunk.
//  4. META: the stations, lengths and crossings, for terrain.yaml and the
//     editor's paths panel.
//
// Paths never carve across a channel: a bridge is out of scope (noted in
// PHASE_P4.md).

import (
	"math"
)

// The path styles.
const (
	PathRock     = "rock_path"
	PathStepping = "stepping_stones"
	PathDirt     = "dirt_trail"
	PathGravel   = "gravel_path"
	PathGame     = "game_trail"
)

// PathStyleNames is every style, in the order the editor offers them.
var PathStyleNames = []string{PathRock, PathStepping, PathDirt, PathGravel, PathGame}

// Stone kinds a style lays.
const (
	StonesNone = ""
	StonesFlag = "flag"
	StonesStep = "step"
	// StonesFord is a stepping stone in water (any style with Ford).
	StonesFord = "ford"
)

// PathStyle is what a style does beyond its shape numbers.
type PathStyle struct {
	// Stones is the stones it lays along its length (StonesFlag,
	// StonesStep or none).
	Stones string
	// Surface is its surface mask's value on the path (the splat's
	// strength there): a stepping-stone path only darkens the grass a
	// little between its stones, a game trail is faint.
	Surface float64
	// Wear is the wear mask's peak: 1 leaves no grass on the centreline.
	Wear float64
	// Clear is the clear mask's peak (large scatter kept off).
	Clear float64
	// Ford lays stepping stones where the path crosses water.
	Ford bool
}

// PathStyles are the styles.
var PathStyles = map[string]PathStyle{
	PathRock:     {Stones: StonesFlag, Surface: 1, Wear: 1, Clear: 1, Ford: true},
	PathStepping: {Stones: StonesStep, Surface: 0.45, Wear: 0.7, Clear: 1, Ford: true},
	PathDirt:     {Surface: 1, Wear: 1, Clear: 1, Ford: true},
	PathGravel:   {Surface: 1, Wear: 1, Clear: 1, Ford: true},
	PathGame:     {Surface: 0.55, Wear: 0.6, Clear: 0.5},
}

// Path mask names. Each style's surface mask is PathSurfaceMask(style).
const (
	MaskPathWear  = "path_wear"
	MaskPathClear = "path_clear"
	MaskPathDirX  = "path_dir_x"
	MaskPathDirZ  = "path_dir_z"
)

// PathSurfaceMask is a style's surface mask: path_<style>.
func PathSurfaceMask(style string) string { return "path_" + style }

// PathMaskNames is every path mask, in the order terrain.yaml lists them:
// written (all of them) whenever a ground_edit lays at least one path.
var PathMaskNames = func() []string {
	var out []string
	for _, s := range PathStyleNames {
		out = append(out, PathSurfaceMask(s))
	}
	return append(out, MaskPathWear, MaskPathClear, MaskPathDirX, MaskPathDirZ)
}()

// PathMaskDocs describe each path mask.
var PathMaskDocs = func() map[string]string {
	m := map[string]string{
		MaskPathWear:  "paths' wear: 1 on the centreline easing to 0 past the edge (thins and shortens grass)",
		MaskPathClear: "paths' keep-clear share for large scatter, 0..1",
		MaskPathDirX:  "paths' direction along X: 0.5 + 0.5 t·strength",
		MaskPathDirZ:  "paths' direction along Z: 0.5 + 0.5 t·strength",
	}
	for _, s := range PathStyleNames {
		m[PathSurfaceMask(s)] = s + " surface (its splat), with a worn edge, 0..1"
	}
	return m
}()

// PathShape is a path's numbers (DefaultPathShape; the stage's `paths`
// item fields).
type PathShape struct {
	// Flatten levels the cross-section to the smoothed centreline.
	Flatten bool
	// Blend is how far past the edge the flatten eases out (m); Smooth the
	// length the centreline is smoothed over (m).
	Blend, Smooth float64
	// Steps cuts treads StepRise apart where the path is steeper than
	// MaxGrade (rise per metre).
	Steps              bool
	StepRise, MaxGrade float64
	// Edge is how much the worn edge wobbles (a fraction of the half
	// width) on a noise of EdgeSize metres.
	Edge, EdgeSize float64
	// Wear is the wear band's width past the edge (m).
	Wear float64
	// Stones is the share of stones kept (1 all, 0 none); StoneSize scales
	// them; Stride is a stepping-stone path's step (m).
	Stones, StoneSize, Stride float64
}

// DefaultPathShape is the shape's defaults.
func DefaultPathShape() PathShape {
	return PathShape{Flatten: true, Blend: 1.2, Smooth: 6, StepRise: 0.18, MaxGrade: 0.3, Edge: 0.3, EdgeSize: 1.6,
		Wear: 1.2, Stones: 1, StoneSize: 1, Stride: 0.7}
}

// PathSpec is one path to lay.
type PathSpec struct {
	Name, Style string
	// Points is the centreline (x, z); Width the path's width at each
	// point (m; missing points repeat the last, none reads 1.5).
	Points [][2]float64
	Width  []float64
	Shape  PathShape
	Seed   uint64
}

// PathStation is a point every half cell along a laid path.
type PathStation struct {
	X, Z float64
	// S is the distance along (m); TX, TZ the unit tangent.
	S, TX, TZ float64
	Width     float64
	// Ground is the ground under the centreline before the path; Target
	// the level the flatten brings it to.
	Ground, Target float64
	// Wet is whether water stands at the centreline (a pond or a river);
	// Level its surface there (NaN dry); WetDist the distance along the
	// path to the nearest wet station (+Inf when none).
	Wet     bool
	Level   float64
	WetDist float64
}

// Path is one laid path.
type Path struct {
	Name, Style string
	Spec        int
	Stations    []PathStation
	Length      float64
	// Crossings is how many separate stretches of it are under water.
	Crossings int
	// Step is the station spacing (m).
	Step float64
}

// at is the station state at distance s along (interpolated).
func (p *Path) at(s float64) PathStation {
	st := p.Stations
	if len(st) == 0 {
		return PathStation{}
	}
	f := math.Max(0, math.Min(s/p.Step, float64(len(st)-1)))
	i := min(int(f), len(st)-2)
	if i < 0 {
		return st[0]
	}
	t := f - float64(i)
	a, b := st[i], st[i+1]
	lerp := func(x, y float64) float64 { return x + (y-x)*t }
	out := a
	out.X, out.Z, out.S = lerp(a.X, b.X), lerp(a.Z, b.Z), s
	out.Width, out.Ground, out.Target = lerp(a.Width, b.Width), lerp(a.Ground, b.Ground), lerp(a.Target, b.Target)
	out.WetDist = math.Min(a.WetDist, b.WetDist) // +Inf on a dry path: no lerp
	tx, tz := lerp(a.TX, b.TX), lerp(a.TZ, b.TZ)
	if l := math.Hypot(tx, tz); l > 1e-9 {
		out.TX, out.TZ = tx/l, tz/l
	}
	if t > 0.5 {
		out.Wet, out.Level = b.Wet, b.Level
	}
	return out
}

// PathResult is what LayPaths did.
type PathResult struct {
	Paths []Path
	// Masks holds every PathMaskNames entry over the grid (unquantised).
	Masks map[string][]float32
	// Changed is how many samples the flatten moved.
	Changed int
}

// pathWater answers whether water stands at grid sample k, its level, and
// whether k is in a river's channel (its bed share above a trace): what a
// path neither flattens nor paints.
type pathWater struct {
	water  *WaterResult
	rivers *RiverResult
}

func (w pathWater) at(k int) (level float64, wet, channel bool) {
	if w.water != nil && k < len(w.water.Body) && w.water.Body[k] >= 0 {
		return float64(w.water.Level[k]), true, true
	}
	if w.rivers != nil {
		if s, ok := w.rivers.Samples[int32(k)]; ok {
			if s.Wet {
				return float64(s.Surface), true, true
			}
			return math.NaN(), false, s.Bed > 0.05
		}
	}
	return math.NaN(), false, false
}

// sampleIndex is the grid sample nearest (x, z), or -1 off the grid.
func sampleIndex(g *Grid, x, z float64) int {
	i := int(math.Round((x - g.MinX) / g.Cell))
	j := int(math.Round((z - g.MinZ) / g.Cell))
	s := g.S()
	if i < 0 || j < 0 || i >= s || j >= s {
		return -1
	}
	return j*s + i
}

// smoothstep is 0 at or below a, 1 at or above b, smooth between.
func smoothstep(a, b, x float64) float64 {
	if b <= a {
		if x < a {
			return 0
		}
		return 1
	}
	t := math.Max(0, math.Min(1, (x-a)/(b-a)))
	return t * t * (3 - 2*t)
}
