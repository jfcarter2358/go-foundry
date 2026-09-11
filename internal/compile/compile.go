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
	"math"
	"strings"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/bsp"
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
	// AvgVisibleLeaves is the mean row weight of the bsp.PVS over interior cells.
	// The number to watch: close to the interior cell count means the level is
	// one big open space and visibility is buying nothing.
	AvgVisibleLeaves float64
	// Leaks are places the level says something should be — the spawn, an
	// entity — that ended up in the void outside it. Each one means a hole in
	// the shell. See outside.go.
	Leaks []Leak
	// BeyondSeal names the baked brushes reaching outside the level's seal box
	// — see BrushesBeyondSeal and SealWarning.
	BeyondSeal []string
}

// SealWarning is what to tell the author about brushes reaching outside the
// seal box, or "" when there are none.
func (s Stats) SealWarning() string {
	n := len(s.BeyondSeal)
	if n == 0 {
		return ""
	}
	names, more := s.BeyondSeal, ""
	if n > 5 {
		names, more = names[:5], fmt.Sprintf(" and %d more", n-5)
	}
	return fmt.Sprintf("%d brush(es) reach outside the seal box, and what is outside it is never drawn from inside the level: %s%s",
		n, strings.Join(names, ", "), more)
}

// Leak is one thing found outside the level.
type Leak struct {
	What     string
	Position mgl32.Vec3
	// Opening is roughly where the void gets in on its way to Position — see
	// BSPTree.leakOpening. Meaningful only when HasOpening.
	Opening    mgl32.Vec3
	HasOpening bool
	// OutsideSeal is a thing placed outside the level's seal box, which is a
	// leak for a different reason and with a different fix.
	OutsideSeal bool
}

// Describe is the leak as a sentence an author can act on.
//
// Not "X is outside the level". The author put X inside the level and can see
// that it is, so a report saying otherwise reads as the compiler being wrong.
// What is true is that the void outside reaches X — and where it gets in is
// the thing to go and fix.
func (l Leak) Describe() string {
	switch {
	case l.OutsideSeal:
		return fmt.Sprintf("%s at %v is outside the level's seal box: move it inside, or make the box bigger",
			l.What, roundVec(l.Position))
	case l.HasOpening:
		return fmt.Sprintf("%s at %v is not sealed in: the void outside the level reaches it through a gap near %v",
			l.What, roundVec(l.Position), roundVec(l.Opening))
	default:
		return fmt.Sprintf("%s at %v is not sealed in: it is open to the void outside the level",
			l.What, roundVec(l.Position))
	}
}

// roundVec rounds to the hundredth, which is as precise as a place to go and
// look needs to be, and keeps a portal's centroid from printing as 2.1600003.
func roundVec(v mgl32.Vec3) mgl32.Vec3 {
	for i := range v {
		v[i] = float32(math.Round(float64(v[i])*100) / 100)
	}
	return v
}

// Result is a compiled level's geometry.
type Result struct {
	Tree    *BSPTree
	Portals []Portal
	Vis     *bsp.PVS
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
		if !Bakes(ch) {
			continue
		}
		for bi := range ch.Brushes {
			if !BakesBrush(&ch.Brushes[bi]) {
				continue // never reached CSG; see BakesBrush
			}
			n += len(ch.Brushes[bi].DrawFaces())
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
	s.BeyondSeal = BrushesBeyondSeal(lvl)
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
	check := func(what string, p mgl32.Vec3) {
		// With a seal, nothing inside the box can leak, and the one way to be
		// outside the level is to be outside the box.
		if lvl.Seal.Active() && !lvl.Seal.Contains(p) {
			out = append(out, Leak{What: what, Position: p, OutsideSeal: true})
			return
		}
		if !t.LeaksAt(p) {
			return
		}
		l := Leak{What: what, Position: p}
		l.Opening, l.HasOpening = t.leakOpening(p)
		out = append(out, l)
	}
	if lvl.Spawn != nil {
		check("the player spawn", lvl.Spawn.Position)
	}
	for i := range lvl.Entities {
		check("entity "+lvl.Entities[i].ID, lvl.Entities[i].Position)
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
