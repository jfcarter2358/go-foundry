package compile

// compile.go — the pipeline, in order, and what it has to say about the level
// it just built.
//
// Each stage is useful on its own and tested on its own; this is the one place
// that knows they go together, and in which order. The order is not a choice
// among several that would work:
//
//   1. Validate, because every stage below assumes convex brushes.
//   2. CSG, which throws away the surfaces nothing can see, so that everything
//      after it has fewer faces to partition, light and draw.
//   3. BSP, which turns the remaining soup of polygons into convex cells.
//   4. Visibility, which is a question about cells and portals between them,
//      and needs the void around the level filled in first.
//   5. Light, LAST, because it subdivides faces to get enough samples out of
//      per-vertex lighting and so renumbers them — which would invalidate the
//      cell face lists visibility had been building if it ran earlier.

import (
	"fmt"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/world"
)

// Options tunes a compile. The zero value is a valid compile with no light
// bake at all, which is what a test that only cares about geometry wants;
// DefaultOptions is what shipping a level wants.
type Options struct {
	// Light controls the bake. A zero GridStep means faces are lit at their
	// existing corners, which for a level built out of big slabs means almost
	// no lighting detail at all — see light.go.
	Light LightOptions
	// SkipLight leaves every face's Light nil, for a compile that only wants
	// the geometry and visibility.
	SkipLight bool
}

// DefaultOptions is the compile a level ships with.
func DefaultOptions() Options { return Options{Light: DefaultLightOptions()} }

// Stats is what the compile has to say about the level, for the editor to show
// and for a build step to print.
type Stats struct {
	SourceFaces    int // faces the level authored, before anything was removed
	Faces          int // faces in the compiled output
	Nodes          int
	Leaves         int
	SolidLeaves    int
	OutsideLeaves  int
	InteriorLeaves int
	Portals        int
	// AvgVisibleLeaves is the mean row weight of the PVS over interior cells.
	// The number to watch: close to the interior cell count means the level is
	// one big open space and visibility is buying nothing.
	AvgVisibleLeaves float64
	// Leaks are places the level says something should be — the spawn, an
	// entity — that ended up in the void outside it. Each one means a hole in
	// the shell. See outside.go.
	Leaks []Leak
}

// Leak is one thing found outside the level.
type Leak struct {
	What     string
	Position mgl32.Vec3
}

// Result is a compiled level's geometry.
type Result struct {
	Tree    *BSPTree
	Portals []Portal
	Vis     *PVS
	Stats   Stats
}

// Compile runs every stage over a level.
//
// A leak does NOT fail the compile. The output is still correct — the void is
// filled and visibility is computed over what is left — it is just that a level
// with a hole in its shell has most of itself outside. Reporting it and letting
// the author look is more use than refusing, and refusing would make the
// editor's compile button useless on a level halfway through being built.
func Compile(lvl *world.Level, opt Options) (*Result, error) {
	faces, err := CSG(lvl)
	if err != nil {
		return nil, err
	}
	tree := BuildBSP(faces)
	portals, vis := ComputeVisibility(tree)
	if !opt.SkipLight {
		BakeLight(tree, lvl, opt.Light)
	}

	r := &Result{Tree: tree, Portals: portals, Vis: vis}
	r.Stats = gatherStats(lvl, r, sourceFaceCount(lvl))
	return r, nil
}

func sourceFaceCount(lvl *world.Level) int {
	if lvl == nil {
		return 0
	}
	n := 0
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !ch.IsEnabled() || ch.FrameOwner != "" {
			continue
		}
		for bi := range ch.Brushes {
			n += len(ch.Brushes[bi].Faces)
		}
	}
	return n
}

func gatherStats(lvl *world.Level, r *Result, source int) Stats {
	s := Stats{
		SourceFaces: source,
		Faces:       len(r.Tree.Faces),
		Nodes:       len(r.Tree.Nodes),
		Leaves:      len(r.Tree.Leaves),
		Portals:     len(r.Portals),
	}
	total, counted := 0, 0
	for i := range r.Tree.Leaves {
		l := &r.Tree.Leaves[i]
		switch {
		case l.Solid:
			s.SolidLeaves++
		case l.Outside:
			s.OutsideLeaves++
		default:
			s.InteriorLeaves++
			total += r.Vis.VisibleCount(i)
			counted++
		}
	}
	if counted > 0 {
		s.AvgVisibleLeaves = float64(total) / float64(counted)
	}
	s.Leaks = findLeaks(lvl, r.Tree)
	return s
}

// findLeaks asks the tree about every place the level says something is.
//
// The spawn and the entities rather than an exhaustive sweep: a leak puts
// nearly everything outside, so one thing landing in the void is enough to
// report it, and these are the two an author will recognise immediately.
func findLeaks(lvl *world.Level, t *BSPTree) []Leak {
	if lvl == nil {
		return nil
	}
	var out []Leak
	if lvl.Spawn != nil && t.LeaksAt(lvl.Spawn.Position) {
		out = append(out, Leak{What: "the player spawn", Position: lvl.Spawn.Position})
	}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if t.LeaksAt(e.Position) {
			out = append(out, Leak{What: "entity " + e.ID, Position: e.Position})
		}
	}
	return out
}

// Summary is the one-line-per-fact report a build step or the editor prints.
func (s Stats) Summary() string {
	return fmt.Sprintf(
		"faces %d -> %d, %d nodes, %d cells (%d solid, %d void, %d interior), %d portals, %.1f cells visible on average",
		s.SourceFaces, s.Faces, s.Nodes, s.Leaves,
		s.SolidLeaves, s.OutsideLeaves, s.InteriorLeaves, s.Portals, s.AvgVisibleLeaves)
}
