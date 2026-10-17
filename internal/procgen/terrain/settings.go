package terrain

// settings.go — every number Generate reads, grouped the way the pipeline
// runs (terrain.go), with the defaults the `terrain` stage starts from.
//
// The stage maps its schema onto this one to one; nothing here knows about
// .fgen documents. Lengths are metres and angles degrees throughout, so a
// change of Cell (resolution) changes the detail and not the landforms:
// every per-cell quantity (droplet steps, brush radius, drainage grid,
// thermal reach) is derived from a metre length here.

import (
	"math"
)

// Settings is a generation.
type Settings struct {
	// Size is the square's side and Cell the sample spacing, in metres.
	Size, Cell float64
	Seed       uint64

	Base   BaseShape
	Mesa   Mesa
	Strata Strata
	// Canyons are carved in order.
	Canyons  []Canyon
	Drainage Drainage
	Droplets Droplets
	Thermal  Thermal
	Detail   Detail
	// CliffAngle is the cliff mask's threshold, degrees.
	CliffAngle float64
	// SandColor is the albedo hint's colour of loose deposits, 0..1 RGB.
	SandColor [3]float64
	// Landform is the provider's other landforms (landform.go): ridged
	// mountains, dunes, a coast, volcanoes, hummocks and ponds. The zero
	// value is none, and the base shape is the classic one.
	Landform Landform
	// Edge fades a region's relief toward a common level at its borders
	// with other regions (edge.go); the zero value never fades.
	Edge Edge
}

// BaseShape is the lowland: domain-warped FBM.
type BaseShape struct {
	// Height is the lowland's mean elevation.
	Height float64
	// Relief is the FBM's half range.
	Relief float64
	// Frequency is the first octave's, cycles per metre.
	Frequency float64
	Octaves   int
	Gain      float64
	// Warp is the domain warp's reach, metres.
	Warp float64
}

// Mesa is the plateau field: thresholded low-frequency noise in tiers.
type Mesa struct {
	// Height is each tier's rise, metres; 0 is no mesas.
	Height float64
	// Coverage is the share of the map the first tier covers, 0..1.
	Coverage float64
	// Scale is the plateaus' typical size, metres.
	Scale float64
	// Tiers stacks plateaus on plateaus: 2 puts buttes on the mesas.
	Tiers int
	// Rim is the rim's horizontal width, metres: smaller is steeper.
	Rim float64
	// Roughness ragged-edges the rims, 0..1.
	Roughness float64
	// TopRelief is the undulation left on the flat tops, metres.
	TopRelief float64
	// Joints is how strongly the rims follow a fracture network, 0..1, and
	// JointSpacing the network's spacing, metres.
	Joints, JointSpacing float64
}

// Detail is the fine surface texture added after erosion.
type Detail struct {
	// Amplitude is the texture's height, metres; 0 is none.
	Amplitude float64
	// JointSpacing is the slickrock crack network's spacing, metres.
	JointSpacing float64
}

// Layer is one stratum.
type Layer struct {
	Thickness float64
	// Hardness is 0 (shale, slope-former) to 1 (sandstone, cliff-former).
	Hardness float64
	// Color is its rock colour for the albedo hint, 0..1 RGB.
	Color [3]float64
}

// Strata is the layer stack.
type Strata struct {
	// Layers stack upward from Base, repeating the list as often as the
	// heights need (and downward below Base the same way).
	Layers []Layer
	Base   float64
	// Terrace is how strongly the surface steps through the layers, 0..1.
	Terrace float64
	// SlopeLo and SlopeHi are the slopes (degrees) the stepping fades in
	// over: gentler ground stays smooth (see terrace).
	SlopeLo, SlopeHi float64
	// Warp is the noise displacement of the layer heights, metres.
	Warp float64
	// Dip tilts the layers: metres of rise per 100 m along X and Z.
	Dip [2]float64
	// Resistance is how much hardness slows erosion, 0..1: at 1 a layer of
	// hardness 1 does not erode at all.
	Resistance float64
}

// Profile is a canyon's cross-section shape.
type Profile int

const (
	ProfileV Profile = iota
	ProfileU
	ProfileSlot
	ProfileStepped
	ProfileCustom
)

// ProfileNames are the UI spellings, indexed by Profile.
var ProfileNames = []string{"v", "u", "slot", "stepped", "custom"}

// ProfileFromName parses a ProfileNames entry; unknown is V.
func ProfileFromName(s string) Profile {
	for i, n := range ProfileNames {
		if n == s {
			return Profile(i)
		}
	}
	return ProfileV
}

// PathPoint is one control point of a canyon path.
type PathPoint struct {
	X, Z float64
	// Width is the canyon's rim-to-rim width and Depth its depth below the
	// surrounding ground, metres, at this point.
	Width, Depth float64
}

// Canyon is one carved path.
type Canyon struct {
	// Path runs DOWNSTREAM: the floor never rises along it.
	Path    []PathPoint
	Profile Profile
	// Custom is the custom profile: depth fraction (0 rim .. 1 floor) by
	// distance from the centreline as a fraction of the half width (0
	// centre .. 1 rim). Used when Profile is ProfileCustom.
	Custom func(u float64) float64
	// Floor is the flat floor's share of the width, 0..1.
	Floor float64
	// Steps is the stepped profile's benches.
	Steps int
	// Meander is the sideways wander's amplitude and MeanderLength its
	// wavelength, metres.
	Meander, MeanderLength float64
	// Tributaries branch off the path; TributaryLength is their reach.
	Tributaries     int
	TributaryLength float64
	// Seed varies the meander and the tributaries.
	Seed uint64
	// Hold is 0..1: how much of the carved shape survives the erosion
	// within the canyon and HoldMargin metres past its rims (stamps.go) —
	// a slot canyon's sheer walls and open floor, which talus would
	// otherwise fill.
	Hold, HoldMargin float64
}

// Drainage is the coarse stream-power erosion.
type Drainage struct {
	// Strength scales the incision, 0..1; 0 skips the phase.
	Strength float64
	// Iterations of route-and-incise.
	Iterations int
	// Cell is the coarse grid's spacing, metres.
	Cell float64
	// Exponent is the drainage area's in the stream-power law (m): higher
	// concentrates erosion in the big channels.
	Exponent float64
	// Fill is how far a pit fills toward its spill height each iteration
	// (flat playas and sand-filled washes), 0..1.
	Fill float64
	// Diffusion is the hillslope creep per iteration, 0..0.24.
	Diffusion float64
}

// Droplets is the particle hydraulic erosion.
type Droplets struct {
	// Density is droplets per square metre; 0 skips the phase.
	Density float64
	// Lifetime is how far a droplet runs, metres.
	Lifetime float64
	// Inertia keeps a droplet's direction, 0..1.
	Inertia float64
	// Capacity is the sediment (m³) a droplet can carry per unit of
	// slope, speed and water.
	Capacity float64
	// MinSlope is the capacity's floor slope, so flats still carry.
	MinSlope float64
	// Erosion and Deposition are the pick-up and drop rates per metre run,
	// as shares of the gap to capacity.
	Erosion, Deposition float64
	// Evaporation is the share of water lost per metre run, 0..1.
	Evaporation float64
	// Radius is the erosion brush's, metres.
	Radius float64
	// Gravity accelerates a droplet down a slope.
	Gravity float64
	// Passes splits the droplets into rounds, each over the whole map.
	Passes int
}

// Thermal is the talus erosion.
type Thermal struct {
	// Iterations of slide; 0 skips the phase.
	Iterations int
	// TalusAngle is a soft layer's angle of repose and CliffAngle the
	// steepest a hard layer stands at, degrees; hardness blends between.
	TalusAngle, CliffAngle float64
	// Rate is the share of the excess moved per iteration, 0..1.
	Rate float64
}

// DefaultLayers is a red-rock stack, bottom up, on the pattern of the
// Canyonlands section: a soft shale slope, a thin pale sandstone that holds a
// wide bench, mudstone ledges and slopes, a massive cliff-forming sandstone,
// and a ledgy cap. Few, contrasting layers: a stack of many similar thin
// ones terraces every slope into contour lines.
func DefaultLayers() []Layer {
	return []Layer{
		{Thickness: 30, Hardness: 0.1, Color: [3]float64{0.62, 0.36, 0.27}},  // slope-forming shale
		{Thickness: 8, Hardness: 0.85, Color: [3]float64{0.84, 0.74, 0.58}},  // a pale bench-forming sandstone
		{Thickness: 25, Hardness: 0.2, Color: [3]float64{0.58, 0.33, 0.25}},  // ledgy mudstone
		{Thickness: 35, Hardness: 0.3, Color: [3]float64{0.60, 0.45, 0.42}},  // a variegated slope
		{Thickness: 60, Hardness: 0.95, Color: [3]float64{0.76, 0.43, 0.27}}, // the great cliff-former
		{Thickness: 15, Hardness: 0.6, Color: [3]float64{0.72, 0.50, 0.36}},  // ledgy caprock
	}
}

// Defaults is the stage's starting point: a kilometre of mesa country at a
// metre a sample.
func Defaults() Settings {
	return Settings{
		Size: 1024, Cell: 1,
		Base:     BaseShape{Height: 0, Relief: 35, Frequency: 0.0035, Octaves: 6, Gain: 0.5, Warp: 90},
		Mesa:     Mesa{Height: 95, Coverage: 0.6, Scale: 520, Tiers: 2, Rim: 40, Roughness: 0.55, TopRelief: 3, Joints: 0, JointSpacing: 70},
		Strata:   Strata{Layers: DefaultLayers(), Base: -20, Terrace: 0.85, SlopeLo: 8, SlopeHi: 30, Warp: 3, Resistance: 0.6},
		Drainage: Drainage{Strength: 2, Iterations: 100, Cell: 4, Exponent: 0.5, Fill: 0.5, Diffusion: 0.1},
		Droplets: Droplets{Density: 1.5, Lifetime: 48, Inertia: 0.08, Capacity: 0.25, MinSlope: 0.02,
			Erosion: 0.1, Deposition: 0.1, Evaporation: 0.02, Radius: 1.5, Gravity: 4, Passes: 4},
		Thermal:    Thermal{Iterations: 60, TalusAngle: 34, CliffAngle: 80, Rate: 0.5},
		Detail:     Detail{Amplitude: 0.5, JointSpacing: 8},
		CliffAngle: 50,
		SandColor:  [3]float64{0.80, 0.62, 0.44},
	}
}

// Cells is the cells a side.
func (s *Settings) Cells() int { return int(math.Round(s.Size / s.Cell)) }

// Check reports settings that cannot run.
func (s *Settings) Check() error {
	if s.Cell <= 0 || s.Size <= 0 {
		return errorf("size and resolution must be positive")
	}
	n := s.Cells()
	if n < 4 {
		return errorf("%.4g m at %.4g m is fewer than 4 cells", s.Size, s.Cell)
	}
	if n > 16384 {
		return errorf("%d cells a side is more than 16384", n)
	}
	if math.Abs(float64(n)*s.Cell-s.Size) > 1e-6*s.Size {
		return errorf("size %.4g m is not a whole number of %.4g m cells", s.Size, s.Cell)
	}
	return nil
}

// Scaled is s for a preview at cells a side at most: the same landforms on a
// coarser grid, with the per-sample work (droplets, thermal iterations)
// reduced to match — the "fast mode". Lengths stay metres, so the shape
// holds; the fine erosion detail is what a preview gives up.
func (s Settings) Scaled(maxCells int) Settings {
	n := s.Cells()
	if n <= maxCells {
		return s
	}
	f := float64(n) / float64(maxCells)
	s.Cell *= f
	// The coarse drainage grid: twice the preview's cell, so its routing
	// costs a quarter of the field's.
	s.Drainage.Cell = max(s.Drainage.Cell, 2*s.Cell)
	// Fewer, cheaper droplets: a preview wants the gullies' layout, not
	// their detail.
	s.Droplets.Passes = min(s.Droplets.Passes, 2)
	s.Drainage.Iterations = min(s.Drainage.Iterations, 25)
	// Thermal reach is in cells per iteration: at f times the cell, f times
	// fewer iterations reach as far.
	s.Thermal.Iterations = max(int(math.Ceil(float64(s.Thermal.Iterations)/f)), min(s.Thermal.Iterations, 8))
	return s
}
