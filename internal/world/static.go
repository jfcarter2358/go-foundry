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
	Geometry gfx.Geometry
	// SubmeshGeometries is the scattered detail this surface carries, exactly
	// as a BrushFace's does.
	SubmeshGeometries []gfx.Geometry
	Detail            DetailSpec
	DoubleSided       bool
	// Normal is the face's own plane normal, for the detail pass, which needs
	// it to build a displacement basis without re-deriving it per frame.
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

	// seen is the dedup scratch VisibleSurfaces fills, kept here so the frame
	// loop allocates nothing. A surface bounds more than one cell whenever a
	// splitting plane cut through the space in front of it, so the same index
	// arrives from several rows.
	seen []bool
	// all is the everything-visible answer, built once.
	all []int
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
			if fi >= 0 && fi < len(s.seen) && !s.seen[fi] {
				s.seen[fi] = true
				out = append(out, fi)
			}
		}
	}
	return out
}

// everything is every surface index, for the fallbacks above.
func (s *StaticGeometry) everything() []int {
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
		for j := range s.Surfaces[i].SubmeshGeometries {
			s.Surfaces[i].SubmeshGeometries[j].Unload()
		}
		s.Surfaces[i].SubmeshGeometries = nil
	}
}
