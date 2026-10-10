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
	// CoveredBy names the chunks whose solids bury this surface while they are
	// switched on (compile.Face.CoveredBy): it is drawn only while every one of
	// them is off, and then whatever the PVS says, since it is in no cell.
	// Empty for every ordinary surface.
	CoveredBy []string
}

// StaticGeometry is the whole of a compiled level's rendering side.
type StaticGeometry struct {
	Surfaces []StaticSurface
	// Tree and Vis are the compiled partition and its visibility. Vis may be
	// nil for a level compiled without it; VisibleSurfaces then falls back to
	// drawing everything.
	Tree *bsp.Tree
	Vis  *bsp.PVS

	// Clusters is the compiler's cut of the surfaces' draw groups into
	// clusters (static_clusters.go), for the GPU-driven cull. Nil for a level
	// compiled before clusters existed; the renderer then cuts at load.
	// Install through SetClusters.
	Clusters     []StaticClusterGroup
	clusterIndex map[string]int

	// HiddenChunks names the compiled chunks currently switched off. See
	// Hidden, which is why this exists at all. Write it through SetChunkHidden,
	// which keeps the per-surface answer below in step.
	HiddenChunks map[string]bool

	// hidden is Hidden's answer per surface, rebuilt when a chunk's state
	// changes, and hiddenCount the size of HiddenChunks it was built from. A
	// streamed level asks Hidden of tens of thousands of surfaces a frame —
	// the visibility walk, the raycasts — and a map lookup by chunk name each
	// time was most of a frame's CPU. A map changed behind SetChunkHidden's
	// back shows up as a different size and falls back to the lookup.
	hidden      []bool
	hiddenCount int
	// hiddenVersion counts changes to what is hidden, for the caches below.
	hiddenVersion uint64

	// visible caches VisibleSurfaces' last answer: the cell it was for, the
	// hidden state it was built against, and the list. With coarse visibility
	// a cell sees most of the level, and walking every visible cell's face
	// list again on every frame the camera stays in the same cell bought
	// nothing.
	visibleLeaf    int
	visibleVersion uint64
	visibleCount   int
	visibleValid   bool
	visible        []int
	// visibleSerial changes whenever VisibleSurfaces answers with a different
	// list than last time, so a cache built from the list (the GPU cull's
	// candidate clusters) can tell without comparing it. See VisibleSerial.
	visibleSerial uint64
	lastAnswer    visibleAnswer

	// seen is the dedup scratch VisibleSurfaces fills, kept here so the frame
	// loop allocates nothing. A surface bounds more than one cell whenever a
	// splitting plane cut through the space in front of it, so the same index
	// arrives from several rows.
	seen []bool
	// all is the everything-visible answer, built once; allHidden the same
	// with some chunks hidden, for the hidden state allHiddenVer counts.
	all          []int
	allHidden    []int
	allHiddenVer uint64
	allHiddenN   int
	// covers lists the surfaces with a CoveredBy, which VisibleSurfaces adds
	// past the PVS; coversFor is the surface count it was built for.
	covers    []int
	coversFor int
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
	if s == nil || i < 0 || i >= len(s.Surfaces) {
		return false
	}
	if len(s.HiddenChunks) == 0 {
		// Nothing is switched off, so every cover surface is still buried.
		return len(s.Surfaces[i].CoveredBy) > 0
	}
	if len(s.hidden) == len(s.Surfaces) && s.hiddenCount == len(s.HiddenChunks) {
		return s.hidden[i]
	}
	return s.HiddenFor(s.Surfaces[i].Chunk, s.Surfaces[i].CoveredBy)
}

// HiddenFor is Hidden for a surface from chunk, covered by coveredBy: off when
// its own chunk is off, and — for a cover surface — while any chunk burying it
// is still on.
func (s *StaticGeometry) HiddenFor(chunk string, coveredBy []string) bool {
	if s.HiddenChunks[chunk] {
		return true
	}
	for _, c := range coveredBy {
		if !s.HiddenChunks[c] {
			return true
		}
	}
	return false
}

// HiddenVersion changes whenever SetChunkHidden changes what is hidden, for a
// cache outside this package built from which surfaces draw (the renderer's
// point-shadow cache). A map written behind SetChunkHidden's back does not move
// it; read len(HiddenChunks) alongside, as Hidden itself does.
func (s *StaticGeometry) HiddenVersion() uint64 {
	if s == nil {
		return 0
	}
	return s.hiddenVersion
}

// SetChunkHidden records that a compiled chunk has been switched off, or on.
//
// Called for every compiled chunk every frame (SyncChunkGeometry), so a call
// that changes nothing returns before touching anything.
func (s *StaticGeometry) SetChunkHidden(chunkID string, hidden bool) {
	if s == nil || s.HiddenChunks[chunkID] == hidden {
		return
	}
	if !hidden {
		delete(s.HiddenChunks, chunkID)
	} else {
		if s.HiddenChunks == nil {
			s.HiddenChunks = map[string]bool{}
		}
		s.HiddenChunks[chunkID] = true
	}
	if len(s.hidden) != len(s.Surfaces) {
		s.hidden = make([]bool, len(s.Surfaces))
	}
	for i := range s.Surfaces {
		s.hidden[i] = s.HiddenFor(s.Surfaces[i].Chunk, s.Surfaces[i].CoveredBy)
	}
	s.hiddenCount = len(s.HiddenChunks)
	s.hiddenVersion++
}

// VisibleSurfaces returns the surface indices to draw from a point, appending
// into out (which the caller reuses across frames).
func (s *StaticGeometry) VisibleSurfaces(eye mgl32.Vec3, out []int) []int {
	if s == nil {
		return out[:0]
	}
	return append(out[:0], s.VisibleSurfacesView(eye)...)
}

// VisibleSurfacesView is VisibleSurfaces without the copy: the list it
// returns is the geometry's own cached answer, BORROWED — valid until the next
// call of either, and never to be written to. A level's cell sees tens of
// thousands of surfaces, and the frame loop copied them all every frame only
// to read them.
func (s *StaticGeometry) VisibleSurfacesView(eye mgl32.Vec3) []int {
	if s == nil {
		return nil
	}
	view, answer := s.visibleView(eye)
	key := visibleAnswer{answer: answer, hidden: s.hiddenVersion, count: len(s.HiddenChunks), surfaces: len(s.Surfaces)}
	if key != s.lastAnswer || s.visibleSerial == 0 {
		s.lastAnswer = key
		s.visibleSerial++
	}
	return view
}

// VisibleSerial changes whenever VisibleSurfaces' answer has: a new cell, a
// chunk shown or hidden, a fall back to everything. Two calls with the same
// serial returned the same list.
func (s *StaticGeometry) VisibleSerial() uint64 {
	if s == nil {
		return 0
	}
	return s.visibleSerial
}

// What visibleSurfaces answered with, when it was not a leaf's row.
const (
	answerEverything = -1
	answerNothing    = -2
)

// visibleAnswer identifies one answer of VisibleSurfaces.
type visibleAnswer struct {
	answer          int
	hidden          uint64
	count, surfaces int
}

// visibleView is VisibleSurfacesView, also saying which answer it gave: the
// camera's leaf, or answerEverything / answerNothing.
func (s *StaticGeometry) visibleView(eye mgl32.Vec3) ([]int, int) {
	if len(s.Surfaces) == 0 {
		return nil, answerNothing
	}
	if s.Tree == nil || s.Vis == nil {
		return s.everything(), answerEverything
	}
	leaf := s.Tree.FindLeaf(eye)
	if !s.Tree.Interior(leaf) {
		// The camera is in material or in the void. Nothing was computed for
		// there, so draw the level rather than a hole in it.
		return s.everything(), answerEverything
	}
	if s.visibleValid && s.visibleLeaf == leaf && s.visibleVersion == s.hiddenVersion &&
		s.visibleCount == len(s.HiddenChunks) {
		return s.visible, leaf
	}
	out := s.visible[:0]

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
	// A cover surface is in no cell: the PVS was built with the chunk that
	// buries it switched on. It shows only at the edge of a streamed window,
	// and there it goes past the PVS to the frustum.
	if len(s.HiddenChunks) > 0 {
		for _, fi := range s.coverSurfaces() {
			if !s.seen[fi] && !s.Hidden(fi) {
				s.seen[fi] = true
				out = append(out, fi)
			}
		}
	}
	s.visible = out
	s.visibleLeaf, s.visibleVersion, s.visibleCount, s.visibleValid = leaf, s.hiddenVersion, len(s.HiddenChunks), true
	return out, leaf
}

// everything is every surface index that is not hidden, for the fallbacks
// above.
//
// Cached while nothing is hidden, which is every level that does not stream,
// and for the hidden state it was last built for otherwise. Callers only read
// it: it is handed out as VisibleSurfacesView's borrowed answer.
func (s *StaticGeometry) everything() []int {
	if len(s.HiddenChunks) > 0 {
		if s.allHidden != nil && s.allHiddenVer == s.hiddenVersion && s.allHiddenN == len(s.HiddenChunks) {
			return s.allHidden
		}
		out := make([]int, 0, len(s.Surfaces))
		for i := range s.Surfaces {
			if !s.Hidden(i) {
				out = append(out, i)
			}
		}
		s.allHidden, s.allHiddenVer, s.allHiddenN = out, s.hiddenVersion, len(s.HiddenChunks)
		return out
	}
	if len(s.all) != len(s.Surfaces)-len(s.coverSurfaces()) {
		// Nothing hidden means every cover surface is buried.
		s.all = s.all[:0]
		for i := range s.Surfaces {
			if len(s.Surfaces[i].CoveredBy) == 0 {
				s.all = append(s.all, i)
			}
		}
	}
	return s.all
}

// coverSurfaces is the index of every surface with a CoveredBy.
func (s *StaticGeometry) coverSurfaces() []int {
	if s.coversFor != len(s.Surfaces) {
		s.covers = s.covers[:0]
		for i := range s.Surfaces {
			if len(s.Surfaces[i].CoveredBy) > 0 {
				s.covers = append(s.covers, i)
			}
		}
		s.coversFor = len(s.Surfaces)
	}
	return s.covers
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
