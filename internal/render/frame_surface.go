package render

// frame_surface.go — a material's surface state, resolved once a frame.
//
// gfx.Material.SurfaceState overlays the material library's document (a
// lookup under the library's lock) on the batch's own fields, and a frame of
// a compiled level asks it of every one of a thousand groups several times
// over: whether the material pass can shade it (prepassable), whether it is
// transparent, what its coverage is in the ID pass, what the material pass
// shades it with, and the same again for the occlusion pass's second draw.
// None of it can change inside a frame — the library and the texture editor
// are both written between frames — so within a DrawWorld it is worked out
// once per material and handed out from here.

import (
	"foundry/internal/gfx"
)

// frameSurfaceState is one material's answers for one frame.
type frameSurfaceState struct {
	frame uint64
	st    gfx.SurfaceState
	// prepass is prepassable's answer; transparent is IsTransparent's.
	prepass, transparent bool
}

var (
	frameSurfaces = map[*gfx.Material]*frameSurfaceState{}
	// frameSurfaceSwept is the frame the cache last dropped what had stopped
	// being asked for: a released batch's material would otherwise stay
	// reachable from here for the life of the process.
	frameSurfaceSwept uint64
	// frameSurfaceScratch serves a call outside a frame, which caches nothing.
	frameSurfaceScratch frameSurfaceState
)

// frameSurfaceSweepEvery is how many frames a material may go unasked before
// the cache lets it go.
const frameSurfaceSweepEvery = 256

// frameSurface is m's surface state for this frame.
func frameSurface(m *gfx.Material) *frameSurfaceState {
	if !drawFrameOpen {
		s := &frameSurfaceScratch
		s.fill(m)
		return s
	}
	if drawFrameNo-frameSurfaceSwept >= frameSurfaceSweepEvery {
		for k, s := range frameSurfaces {
			if drawFrameNo-s.frame >= frameSurfaceSweepEvery {
				delete(frameSurfaces, k)
			}
		}
		frameSurfaceSwept = drawFrameNo
	}
	s := frameSurfaces[m]
	if s == nil {
		s = &frameSurfaceState{}
		frameSurfaces[m] = s
	}
	if s.frame != drawFrameNo {
		s.fill(m)
		s.frame = drawFrameNo
	}
	return s
}

func (s *frameSurfaceState) fill(m *gfx.Material) {
	s.st = m.SurfaceState()
	s.transparent = s.st.IsTransparent()
	// prepassable's rule (nanite_prepass.go), on the state in hand.
	s.prepass = !s.st.Unlit && !s.st.Water.Enabled && s.st.Blend == 0 && !s.transparent
}
