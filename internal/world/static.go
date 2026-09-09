package world

// static.go — a compiled level's drawable geometry.
//
// An authored level is drawn by walking its chunks and their brushes. A
// COMPILED level is not: its surfaces have been clipped free of the brushes
// they came from, cut into fragments, sorted into convex cells and lit once and
// for all. What is left is a flat list of surfaces and a tree that says which
// of them to draw.
//
// # Why the chunks are still here
//
// A compiled level keeps its brushes, and they are the SAME brushes — they are
// just no longer what gets drawn. Collision resolves a body against a brush's
// own convex hull, a raycast wants real triangles, and a script addresses a
// brush by name. None of those has a compiled equivalent worth inventing, and
// all of them would be worse against a soup of clipped fragments than against
// the solids an author actually placed. So Chunk.Compiled marks a chunk whose
// SURFACES are in here, and the draw loop skips it; everything else about it
// works exactly as before.
//
// # Culling
//
// Find the cell the camera is in, take that row of the visibility set, and draw
// the surfaces of every cell the row names. That happens before any frustum
// test and before any draw call — a sealed room you are not in costs nothing at
// all, which is the entire point of having compiled anything.
//
// The one case that has to be got right is the camera being somewhere the
// compiler says nothing can be: clipped into a wall, or outside a level that
// leaked. Then there is no row to trust, and the answer is to draw EVERYTHING.
// Slow and correct beats fast and full of holes.

import (
	"foundry/internal/bsp"
	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// StaticSurface is one compiled face, ready to draw.
//
// It is deliberately shaped like a BrushFace's drawable half and nothing else:
// the renderer's inner loop should not be able to tell which of the two it has.
type StaticSurface struct {
	Geometry    gfx.Geometry
	DoubleSided bool
	// Chunk and Brush name where this surface came from. Not needed to draw
	// it — but a raycast reports what it hit by those names, and a script that
	// asks what is under the crosshair has to get the same answer from a
	// compiled level as from the authored one it was written against.
	Chunk, Brush string
	// Normal is the face's own plane normal, carried across from the compiler
	// so nothing downstream has to re-derive it from the triangles.
	Normal mgl32.Vec3
	Lo, Hi mgl32.Vec3
}

// StaticGeometry is the whole of a compiled level's rendering side.
type StaticGeometry struct {
	Surfaces []StaticSurface
	// Tree and Vis are the compiled partition and its visibility. Vis may be
	// nil for a level compiled without it; VisibleSurfaces then falls back to
	// drawing everything.
	Tree *bsp.Tree
	Vis  *bsp.PVS

	// HiddenChunks names the compiled chunks currently switched off. See
	// Hidden, which is why this exists at all.
	HiddenChunks map[string]bool

	// seen is the dedup scratch VisibleSurfaces fills, kept here so the frame
	// loop allocates nothing. A surface bounds more than one cell whenever a
	// splitting plane cut through the space in front of it, so the same index
	// arrives from several rows.
	seen []bool
	// all is the everything-visible answer, built once.
	all []int
}

// Hidden reports whether a compiled surface should be skipped because the chunk
// it came from has been switched off.
//
// A compiled chunk's surfaces are not its own any more, so releasing its vertex
// buffers — which is what disabling a chunk does — reaches nothing. Without
// this, `@chunk.prop.enabled = false` would take a compiled area out of
// collision and out of the spatial hash and leave it on screen, which is a
// worse state than either being on or off.
//
// The map is empty in every level that never streams, and the guard is checked
// before the lookup, so the ordinary case pays nothing.
func (s *StaticGeometry) Hidden(i int) bool {
	if s == nil || len(s.HiddenChunks) == 0 || i < 0 || i >= len(s.Surfaces) {
		return false
	}
	return s.HiddenChunks[s.Surfaces[i].Chunk]
}

// SetChunkHidden records that a compiled chunk has been switched off, or on.
func (s *StaticGeometry) SetChunkHidden(chunkID string, hidden bool) {
	if s == nil {
		return
	}
	if !hidden {
		delete(s.HiddenChunks, chunkID)
		return
	}
	if s.HiddenChunks == nil {
		s.HiddenChunks = map[string]bool{}
	}
	s.HiddenChunks[chunkID] = true
}

// VisibleSurfaces returns the surface indices to draw from a point, appending
// into out (which the caller reuses across frames).
func (s *StaticGeometry) VisibleSurfaces(eye mgl32.Vec3, out []int) []int {
	out = out[:0]
	if s == nil || len(s.Surfaces) == 0 {
		return out
	}
	if s.Tree == nil || s.Vis == nil {
		return append(out, s.everything()...)
	}
	leaf := s.Tree.FindLeaf(eye)
	if !s.Tree.Interior(leaf) {
		// The camera is in material or in the void. Nothing was computed for
		// there, so draw the level rather than a hole in it.
		return append(out, s.everything()...)
	}

	if len(s.seen) != len(s.Surfaces) {
		s.seen = make([]bool, len(s.Surfaces))
	} else {
		for i := range s.seen {
			s.seen[i] = false
		}
	}
	for other := range s.Tree.Leaves {
		if !s.Vis.Visible(leaf, other) {
			continue
		}
		for _, fi := range s.Tree.Leaves[other].Faces {
			if fi >= 0 && fi < len(s.seen) && !s.seen[fi] && !s.Hidden(fi) {
				s.seen[fi] = true
				out = append(out, fi)
			}
		}
	}
	return out
}

// everything is every surface index that is not hidden, for the fallbacks
// above.
//
// Cached while nothing is hidden, which is every level that does not stream;
// rebuilt each call otherwise, since the fallback is already the slow path.
func (s *StaticGeometry) everything() []int {
	if len(s.HiddenChunks) > 0 {
		out := make([]int, 0, len(s.Surfaces))
		for i := range s.Surfaces {
			if !s.Hidden(i) {
				out = append(out, i)
			}
		}
		return out
	}
	if len(s.all) != len(s.Surfaces) {
		s.all = make([]int, len(s.Surfaces))
		for i := range s.all {
			s.all[i] = i
		}
	}
	return s.all
}

// Release frees every GPU buffer the static geometry owns.
//
// Called before a level is replaced, for the same reason ReleaseLevelGeometry
// is: overwriting the struct drops the handles rather than freeing them.
func (s *StaticGeometry) Release() {
	if s == nil {
		return
	}
	for i := range s.Surfaces {
		s.Surfaces[i].Geometry.Unload()
	}
}
