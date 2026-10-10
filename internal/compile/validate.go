package compile

// validate.go — the checks a level has to pass before it can be compiled.
//
// The editor deliberately lets a level be temporarily wrong. A free vertex drag
// can make a brush concave, and refusing the drag would mean refusing every
// intermediate state on the way to a shape that is fine — an editor you cannot
// pass through an invalid state in is an editor you cannot shape anything in.
// It warns instead (warnIfSelectionBrokeConvexity).
//
// The compiler is the other end of that bargain. Everything downstream of here
// assumes a brush is convex: CSG's whole test for "inside" is "behind every one
// of its planes", collision's support functions assume it, and the BSP's
// solid/empty labelling follows from face normals that a concave brush does not
// give consistently. So this is where a warning becomes an error, once, at the
// top, rather than each stage quietly producing nonsense.

import (
	"fmt"
	"sort"
	"strings"

	"foundry/internal/world"
)

// ValidationError is everything wrong with a level, reported together.
//
// All of them rather than the first, because the fix is to go and edit the
// brushes and an author would rather see the whole list than rediscover it one
// compile at a time.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return "level cannot be compiled: " + e.Problems[0]
	}
	return fmt.Sprintf("level cannot be compiled (%d problems):\n  %s",
		len(e.Problems), strings.Join(e.Problems, "\n  "))
}

// Validate reports what would stop this level compiling, or nil.
func Validate(lvl *world.Level) error {
	if lvl == nil {
		return nil
	}
	var problems []string
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !Bakes(ch) {
			// Neither takes part in the compile — a disabled chunk is not in
			// the world and a frame-owned one is drawn live — so neither has to
			// satisfy the compiler's rules.
			continue
		}
		for bi := range ch.Brushes {
			b := &ch.Brushes[bi]
			if !b.IsConvex() {
				switch {
				case b.Dynamic:
					// A script moves this brush as one rigid convex hull
					// (world/brush_dynamic.go) and addresses it by exactly
					// one ID (internal/script's brushOwnerFor) — decomposing
					// it into several brushes would break both, so unlike a
					// static brush this one is still a hard requirement.
					problems = append(problems, fmt.Sprintf(
						"dynamic brush %q in chunk %q is concave; a moving brush must stay convex — "+
							"split it by hand into convex pieces placed in one Moves chunk instead "+
							"(which can translate and rotate as a group), or make it non-dynamic and it will split automatically",
						b.ID, ch.ID))
				case b.Solid:
					if _, err := world.DecomposeConvex(b.BrushMesh); err != nil {
						problems = append(problems, fmt.Sprintf(
							"brush %q in chunk %q could not be split into convex pieces: %v",
							b.ID, ch.ID, err))
					}
					// Otherwise fine: CSG and collision both read this
					// brush's ConvexBrushPieces instead of assuming the
					// brush itself is convex — see world.Chunk's own
					// comment on that cache.
				}
			}
			if len(b.Faces) < 4 && b.Solid {
				problems = append(problems, fmt.Sprintf(
					"solid brush %q in chunk %q has %d faces; fewer than four cannot bound a volume",
					b.ID, ch.ID, len(b.Faces)))
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	// Sorted so that the same broken level reports the same way twice, however
	// the chunks happened to be walked.
	sort.Strings(problems)
	return &ValidationError{Problems: problems}
}
