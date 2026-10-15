package world

// volume_region.go — an environment region: where a level generation doc
// grows which environment (docs/regions/SPEC.md).
//
// A level that is generated rather than built carries region volumes: "this
// stretch is desert canyon, that one savannah, blended over 200 m". The
// level generation doc (resources/generators/<level>_world.fgen) copies them
// into its `regions` stage before every run (genlevel.SyncRegions), and one
// generation pass grows every region's environment with a natural band
// between them. The volume is the source of truth; the doc holds a synced
// copy.
//
// Volumes have no `kind:` — a volume is a shape plus role blocks — so the
// design's "kind: region" is a `region:` block, built like volume_context.go:
//
//	volumes:
//	  - id: canyon_west
//	    position: [-300, 40, 0]
//	    half: [300, 50, 300]               # kept fitted to the footprint
//	    region:
//	      environment: desert_canyon
//	      generator: environments/my_canyon.fgen   # optional override
//	      priority: 0
//	      transition: 200
//	      anchor: [0, 0]                   # optional; relative, like footprint
//	      footprint: [[-300, -300], [300, -300], [300, 300], [-300, 300]]
//
// The FOOTPRINT is a closed polygon in the volume's own XZ — relative to its
// position, through its rotation — so the translate gizmo moves the whole
// region with no special case. The box shape is kept FITTED to the
// footprint's bounds (FitRegion: the footprint is recentred on the volume's
// position and the half extents follow), which is what picking, the AABB and
// the plain cage read. A region ignores what it is attached to: it is a
// place in the world, not on a ship.
//
// Region volumes are authoring data only. Nothing renders them at play time
// and nothing collides with them; the editor draws their outline in their
// environment's colour and edits their footprint points in the 3D view.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// DefaultRegionTransition is a region's blend band when the file does not
// say, in metres.
const DefaultRegionTransition = float32(150)

// DefaultRegionHalfY is the half height a new region's box gets: tall enough
// to read as a region from the ground, and irrelevant to generation.
const DefaultRegionHalfY = float32(25)

// VolumeRegion makes a volume an environment region.
type VolumeRegion struct {
	// Environment names the preset (resources/environments/<name>.fgen).
	Environment string
	// Generator, when set, is a .fgen grown instead of the preset.
	Generator string
	// Priority: higher wins where regions overlap.
	Priority int
	// Transition is the blend band's width in metres.
	Transition float32
	// Anchor, when set, is where the preset's own coordinates are measured
	// from, relative to the volume like Footprint; nil is the footprint's
	// centroid.
	Anchor *[2]float32
	// Footprint is the closed polygon in the volume's local XZ.
	Footprint [][2]float32
}

// RawVolumeRegion is the YAML form (see the file comment).
type RawVolumeRegion struct {
	// Environment is the preset the region grows: desert_canyon, savannah,
	// or a project's own resources/environments/<name>.fgen.
	Environment string `yaml:"environment"`
	// Generator is an optional .fgen to grow instead of the environment's
	// preset: a preset name or a path under resources/.
	Generator string `yaml:"generator,omitempty"`
	// Priority decides overlaps: higher wins. Default 0.
	Priority int `yaml:"priority,omitempty"`
	// Transition is the blend band's width in metres, centred on the
	// footprint's edge. Default 150.
	Transition float32 `yaml:"transition,omitempty"`
	// Anchor is where the preset's own coordinates are measured from, XZ
	// relative to the volume's position. Absent: the footprint's centroid.
	Anchor *[2]float32 `yaml:"anchor,omitempty,flow"`
	// Footprint is the region's outline: a closed polygon of [x, z] points
	// relative to the volume's position, at least 3, concave allowed.
	Footprint [][2]float32 `yaml:"footprint,flow"`
}

// VolumeRegionFromRaw converts the YAML form.
func VolumeRegionFromRaw(r *RawVolumeRegion) *VolumeRegion {
	if r == nil {
		return nil
	}
	out := &VolumeRegion{Environment: r.Environment, Generator: r.Generator, Priority: r.Priority,
		Transition: r.Transition, Footprint: append([][2]float32(nil), r.Footprint...)}
	if out.Transition <= 0 {
		out.Transition = DefaultRegionTransition
	}
	if r.Anchor != nil {
		a := *r.Anchor
		out.Anchor = &a
	}
	return out
}

// VolumeRegionToRaw is the inverse.
func VolumeRegionToRaw(g *VolumeRegion) *RawVolumeRegion {
	if g == nil {
		return nil
	}
	out := &RawVolumeRegion{Environment: g.Environment, Generator: g.Generator, Priority: g.Priority,
		Transition: g.Transition, Footprint: append([][2]float32(nil), g.Footprint...)}
	if g.Anchor != nil {
		a := *g.Anchor
		out.Anchor = &a
	}
	return out
}

// Clone is a deep copy: a duplicated region must not share its footprint.
func (g *VolumeRegion) Clone() *VolumeRegion {
	if g == nil {
		return nil
	}
	return VolumeRegionFromRaw(VolumeRegionToRaw(g))
}

// IsRegion reports whether the volume is an environment region.
func (v *Volume) IsRegion() bool { return v != nil && v.Region != nil }

// RegionVolumes is the level's region volumes, in level order.
func RegionVolumes(lvl *Level) []*Volume {
	if lvl == nil {
		return nil
	}
	var out []*Volume
	for i := range lvl.Volumes {
		if lvl.Volumes[i].IsRegion() {
			out = append(out, &lvl.Volumes[i])
		}
	}
	return out
}

// NewVolumeRegion is a region block whose footprint is the rectangle of the
// volume's current box, so a volume turned into a region keeps its size.
func NewVolumeRegion(v *Volume, environment string) *VolumeRegion {
	hx, hz := v.HalfX, v.HalfZ
	if hx <= 0 {
		hx = 1
	}
	if hz <= 0 {
		hz = 1
	}
	return &VolumeRegion{Environment: environment, Transition: DefaultRegionTransition,
		Footprint: [][2]float32{{-hx, -hz}, {hx, -hz}, {hx, hz}, {-hx, hz}}}
}

// RegionLocalToWorld is a footprint point (local XZ) in world XZ, at the
// volume's own height: through the volume's position and rotation, never its
// attachment.
func (v *Volume) RegionLocalToWorld(p [2]float32) mgl32.Vec3 {
	return v.ModelMatrix().Mul4x1(mgl32.Vec4{p[0], 0, p[1], 1}).Vec3()
}

// RegionWorldToLocal is the inverse, for a world point on the volume's plane.
func (v *Volume) RegionWorldToLocal(w mgl32.Vec3) [2]float32 {
	l := v.ModelMatrix().Inv().Mul4x1(w.Vec4(1))
	return [2]float32{l.X(), l.Z()}
}

// RegionWorldFootprint is the footprint in world XZ.
func (v *Volume) RegionWorldFootprint() [][2]float64 {
	if v.Region == nil {
		return nil
	}
	out := make([][2]float64, len(v.Region.Footprint))
	for i, p := range v.Region.Footprint {
		w := v.RegionLocalToWorld(p)
		out[i] = [2]float64{float64(w.X()), float64(w.Z())}
	}
	return out
}

// RegionWorldAnchor is the anchor in world XZ, and whether one is set.
func (v *Volume) RegionWorldAnchor() ([2]float64, bool) {
	if v.Region == nil || v.Region.Anchor == nil {
		return [2]float64{}, false
	}
	w := v.RegionLocalToWorld(*v.Region.Anchor)
	return [2]float64{float64(w.X()), float64(w.Z())}, true
}

// FitRegion recentres the footprint on the volume's position and fits the
// box's half extents to it (Y is left alone, or given DefaultRegionHalfY when
// it has none), so picking, the AABB and the plain cage cover the region. The
// anchor moves with the footprint, so nothing changes in the world. Reports
// whether anything changed. A footprint of fewer than 3 points is left as it
// is.
func (v *Volume) FitRegion() bool {
	g := v.Region
	if g == nil || len(g.Footprint) < 3 {
		return false
	}
	lo, hi := g.Footprint[0], g.Footprint[0]
	for _, p := range g.Footprint[1:] {
		lo = [2]float32{min(lo[0], p[0]), min(lo[1], p[1])}
		hi = [2]float32{max(hi[0], p[0]), max(hi[1], p[1])}
	}
	c := [2]float32{(lo[0] + hi[0]) / 2, (lo[1] + hi[1]) / 2}
	hx := max((hi[0]-lo[0])/2, 0.5)
	hz := max((hi[1]-lo[1])/2, 0.5)
	changed := false
	if c != ([2]float32{}) {
		// The new centre in the world, through the rotation, so the
		// footprint does not move.
		v.Position = v.RegionLocalToWorld(c)
		for i := range g.Footprint {
			g.Footprint[i][0] -= c[0]
			g.Footprint[i][1] -= c[1]
		}
		if g.Anchor != nil {
			g.Anchor[0] -= c[0]
			g.Anchor[1] -= c[1]
		}
		changed = true
	}
	if v.Shape != VolumeBox || v.HalfX != hx || v.HalfZ != hz {
		v.Shape, v.HalfX, v.HalfZ = VolumeBox, hx, hz
		changed = true
	}
	if v.HalfY <= 0 {
		v.HalfY = DefaultRegionHalfY
		changed = true
	}
	return changed
}

// RegionCage is the region's wireframe: the footprint at the top and bottom
// of the box, joined at every corner, in volume-local space.
func RegionCage(footprint [][2]float32, hy float32) ([][]float32, []int) {
	n := len(footprint)
	verts := make([][]float32, 0, 2*n)
	var idx []int
	for _, y := range []float32{-hy, hy} {
		for _, p := range footprint {
			verts = append(verts, []float32{p[0], y, p[1], 0, 0})
		}
	}
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		idx = append(idx, i, j, n+i, n+j, i, n+i)
	}
	return verts, idx
}

// RegionFootprintArea is the footprint's area in square metres (either
// winding), for the inspector's readout.
func RegionFootprintArea(fp [][2]float32) float64 {
	a := 0.0
	for i, j := 0, len(fp)-1; i < len(fp); j, i = i, i+1 {
		a += float64(fp[j][0])*float64(fp[i][1]) - float64(fp[i][0])*float64(fp[j][1])
	}
	return math.Abs(a) / 2
}
