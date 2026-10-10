package world

// seal.go — the box that seals a level.
//
// The level compiler finds the inside of a level by flooding in from far
// outside it, and everything the flood reaches is void (compile/outside.go). A
// level open to the sky — a hangar door onto space, a courtyard, a canyon — is
// reachable all the way in, so all of it is void. Before this box existed the
// only way to stop that was to build a shell of brushes around the whole level
// by hand.
//
// The seal is that shell as one object: an axis-aligned box the compiler wraps
// in solid slabs of its own, which never become brushes. Nothing collides with
// it and nothing draws it in play; the editor draws it as a wireframe, because
// somebody has to size it. Every level has one: a level file without one gets
// one fitted around its contents when it loads (levelio), and saves it from
// then on.

import "github.com/go-gl/mathgl/mgl32"

// SealMinHalf is the smallest half-extent a seal takes on any axis. A box with
// no depth on an axis seals nothing, and a scale drag through zero would turn
// it inside out.
const SealMinHalf = float32(0.5)

// Seal is a level's sealing box.
type Seal struct {
	Position mgl32.Vec3 // the centre
	Half     mgl32.Vec3 // half the size on each axis
	// Disabled leaves the level to seal itself with its own walls, the way it
	// had to before the box existed — and so to report every gap in them as a
	// leak. For a level meant to be closed that wants to be told when it is not.
	Disabled bool
}

// RawSeal is the YAML form of a Seal.
type RawSeal struct {
	Position RawVec3 `yaml:"position"`
	Half     RawVec3 `yaml:"half"`
	Disabled bool    `yaml:"disabled,omitempty"`
}

// Active reports whether the seal seals anything: there, and switched on.
func (s *Seal) Active() bool { return s != nil && !s.Disabled }

// Bounds is the box's two corners.
func (s *Seal) Bounds() (lo, hi mgl32.Vec3) {
	return s.Position.Sub(s.Half), s.Position.Add(s.Half)
}

// Contains reports whether p is inside the box. On a face counts as inside.
func (s *Seal) Contains(p mgl32.Vec3) bool {
	lo, hi := s.Bounds()
	for k := 0; k < 3; k++ {
		if p[k] < lo[k] || p[k] > hi[k] {
			return false
		}
	}
	return true
}

// ClampSealHalf keeps every half-extent at SealMinHalf or more.
func ClampSealHalf(h mgl32.Vec3) mgl32.Vec3 {
	for k := 0; k < 3; k++ {
		if !(h[k] >= SealMinHalf) { // written this way round so NaN clamps too
			h[k] = SealMinHalf
		}
	}
	return h
}

// SealFromRaw converts a seal from its YAML form.
func SealFromRaw(r *RawSeal) *Seal {
	if r == nil {
		return nil
	}
	return &Seal{
		Position: mgl32.Vec3{r.Position.X, r.Position.Y, r.Position.Z},
		Half:     ClampSealHalf(mgl32.Vec3{r.Half.X, r.Half.Y, r.Half.Z}),
		Disabled: r.Disabled,
	}
}

// SealToRaw converts a seal to its YAML form.
func SealToRaw(s *Seal) *RawSeal {
	if s == nil {
		return nil
	}
	return &RawSeal{
		Position: RawVec3{X: s.Position.X(), Y: s.Position.Y(), Z: s.Position.Z()},
		Half:     RawVec3{X: s.Half.X(), Y: s.Half.Y(), Z: s.Half.Z()},
		Disabled: s.Disabled,
	}
}
