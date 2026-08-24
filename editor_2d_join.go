package main

// editor_2d_join.go — turning a selection of points into edges.
//
// Drawing a sector used to be a modal gesture: ctrl+click a point to anchor,
// ctrl+click the next to draw an edge to it, and keep going until the outline
// closed. It worked, but it spent the one modifier every other editor uses for
// "add this to the selection" — and it left no way to see what you had picked
// before committing to it, since each click drew an edge immediately.
//
// So the two halves are separated. Ctrl+click builds an ordered selection of
// points, exactly as it does in the 3D view; Join then connects them, in the
// order they were picked. Nothing is drawn until you ask for it, which means a
// half-built outline can be re-picked rather than unwound.
//
// Two forms, because both are wanted and neither is the other:
//
//	Join          the chain only, n-1 edges — a cut across a sector, a corridor
//	              wall, an outline you mean to finish later
//	Join & close  the chain plus the edge back to the first point, which is what
//	              completes a loop and creates a sector from it
//
// The work per pair is what the old ctrl+click did: two points already on one
// sector's boundary split that sector in two, and anything else becomes a
// pending edge that turns into a sector as soon as the outline closes a loop
// (closePendingLoop).
//
// A pair that is already a wall draws nothing — but it is still a side of the
// outline, and a room traced against an existing one shares one. Those are
// carried along as links so the loop can close through them; without that the
// pending edges are forever a chain, and the sector never gets made.

import "fmt"

// editorSelectedChunkPoints returns the chunk points the selection names, in
// selection order and without repeats.
//
// The selection is a list of handles, not of points: a corner shared by two
// sectors is two handles on one point, and a corner's floor and ceiling handles
// are another two. Joining works in points, so those collapse — the first
// handle naming a point fixes where in the order it sits.
//
// Handles outside the chunk the map is showing are dropped. The 2D view edits
// one chunk and edges cannot span two, so a selection carried over from the 3D
// view (which ranges over the whole level) contributes only its local part.
func editorSelectedChunkPoints() []int {
	ch := activeChunk()
	if ch == nil {
		return nil
	}
	seen := map[int]bool{}
	var out []int
	for _, sel := range Editor.SelectedVertices {
		if sel.chunkIdx != Editor.ActiveChunkIdx {
			continue
		}
		if sel.pointIdx < 0 || sel.pointIdx >= len(ch.Points) || seen[sel.pointIdx] {
			continue
		}
		seen[sel.pointIdx] = true
		out = append(out, sel.pointIdx)
	}
	return out
}

// planPointJoin returns the pairs of chunk points a join would connect, in
// order: consecutive picks, plus the closing pair back to the first when
// closeLoop is set.
//
// Pure index arithmetic, so what a join is *about* to do can be checked without
// a level to do it to. Whether each pair splits a sector or becomes a pending
// edge is decided at apply time (joinPointPair), because applying one pair
// changes the answer for the next.
func planPointJoin(pts []int, closeLoop bool) [][2]int {
	if len(pts) < 2 {
		return nil
	}
	pairs := make([][2]int, 0, len(pts))
	for i := 0; i+1 < len(pts); i++ {
		pairs = append(pairs, [2]int{pts[i], pts[i+1]})
	}
	// Two points have one edge between them either way; closing them would ask
	// for the same edge a second time.
	if closeLoop && len(pts) > 2 {
		pairs = append(pairs, [2]int{pts[len(pts)-1], pts[0]})
	}
	return pairs
}

// editorCanJoinPoints reports whether there is anything for a join to do —
// which is what greys the menu row and the panel button out.
func editorCanJoinPoints() bool {
	return len(editorSelectedChunkPoints()) >= 2
}

// pointsAdjacentInAnySector reports whether some sector already walks straight
// from a to b, meaning there is a wall or portal between them already.
func pointsAdjacentInAnySector(ch *Chunk, a, b int) bool {
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		posA := sectorVertexPos(s, a)
		posB := sectorVertexPos(s, b)
		if posA >= 0 && posB >= 0 && adjacentInSector(s, posA, posB) {
			return true
		}
	}
	return false
}

// pendingEdgeExists reports whether an unjoined edge between a and b is already
// waiting, in either direction.
func pendingEdgeExists(a, b int) bool {
	for _, e := range Editor.PendingEdges {
		if (e[0] == a && e[1] == b) || (e[0] == b && e[1] == a) {
			return true
		}
	}
	return false
}

// joinPointPair connects two chunk points and reports whether it changed
// anything.
//
// Pairs that are already connected are skipped rather than doubled: selecting a
// sector's whole ring and closing it should be a no-op, not a second set of
// walls laid over the first. (The old ctrl+click had no such check, because a
// click chain never revisited an edge it had just drawn.)
func joinPointPair(ch *Chunk, a, b int) bool {
	if !joinPairPending(ch, a, b) {
		return false
	}

	// Both points on one sector's boundary, not already neighbours: the edge
	// runs across that sector, so it cuts the sector in two.
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		posA := sectorVertexPos(s, a)
		posB := sectorVertexPos(s, b)
		if posA >= 0 && posB >= 0 {
			splitSector(ch, si, posA, posB)
			return true
		}
	}

	// Otherwise it is a new edge in open space. It stays pending until the
	// pending set closes a loop, at which point it becomes a sector.
	Editor.PendingEdges = append(Editor.PendingEdges, [2]int{a, b})
	tryCreateSectorFromPendingEdges(ch)
	return true
}

// joinPairPending reports whether a pair still has an edge to draw. Used both
// to decide whether a join is worth an undo entry and, inside joinPointPair, to
// skip pairs that are already connected.
func joinPairPending(ch *Chunk, a, b int) bool {
	return a != b && !pointsAdjacentInAnySector(ch, a, b) && !pendingEdgeExists(a, b)
}

// EditorJoinSelectedPoints connects the selected points, optionally closing the
// loop, and reports how many pairs it actually joined.
//
// One undo entry for the whole run: a join is one action to the person who
// asked for it, however many edges it works out to. The entry is only pushed
// once something is known to change — applying a pair can only ever *add*
// connections, so if no pair is outstanding now none will become so partway
// through, and an undo entry for a no-op is noise in the history.
func EditorJoinSelectedPoints(closeLoop bool) int {
	ch := activeChunk()
	if ch == nil {
		return 0
	}
	pairs := planPointJoin(editorSelectedChunkPoints(), closeLoop)
	if len(pairs) == 0 {
		editorSetStatus("Select two or more points to join")
		return 0
	}

	// Sides of the outline that are already walls draw nothing, but they are
	// still sides: a sector traced against an existing one shares one, and the
	// pending set alone could never close around it. Noted before anything is
	// applied, since a split partway through the run would make later pairs
	// look pre-connected when they were not.
	var links [][2]int
	any := false
	for _, pair := range pairs {
		if joinPairPending(ch, pair[0], pair[1]) {
			any = true
		} else if pair[0] != pair[1] && pointsAdjacentInAnySector(ch, pair[0], pair[1]) {
			links = append(links, pair)
		}
	}
	if !any {
		editorSetStatus("Those points are already connected")
		return 0
	}

	EditorPushUndo()
	sectorsBefore := len(ch.Sectors)
	joined := 0
	for _, pair := range pairs {
		if joinPointPair(ch, pair[0], pair[1]) {
			joined++
		}
	}
	// One more attempt with the shared sides in hand. It cannot go inside
	// joinPointPair: an outline whose last pair is a wall it already had adds
	// no pending edge, so nothing there would ever ask again.
	closePendingLoop(ch, links)

	if made := len(ch.Sectors) - sectorsBefore; made > 0 {
		editorSetStatus("Joined %d edge(s), created %d sector(s)", joined, made)
	} else {
		editorSetStatus("Joined %d edge(s)", joined)
	}
	return joined
}

// editorJoinLabel is a join action's label, carrying the edge count so the
// button says what it is about to do rather than merely that it can.
func editorJoinLabel(closeLoop bool) string {
	base := "Join Points"
	if closeLoop {
		base = "Join & Close Loop"
	}
	switch n := len(planPointJoin(editorSelectedChunkPoints(), closeLoop)); {
	case n <= 0:
		return base
	case n == 1:
		return base + " (1 edge)"
	default:
		return fmt.Sprintf("%s (%d edges)", base, n)
	}
}

// EditorDiscardPendingEdges throws away the edges that have been drawn but have
// not closed into a sector yet — the way out of a half-built outline that is
// going nowhere. The points themselves survive, so it can be redrawn without
// being replaced.
func EditorDiscardPendingEdges() {
	if len(Editor.PendingEdges) == 0 {
		return
	}
	EditorPushUndo()
	n := len(Editor.PendingEdges)
	Editor.PendingEdges = nil
	editorSetStatus("Discarded %d unjoined edge(s)", n)
}
