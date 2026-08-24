package world

// sector_edge_id.go — stable identity for a sector's polygon edges.
//
// A sector's edges are stored in two separately-compacted slices, Walls and
// Portals, neither of which is aligned with the polygon ring. Which slot an edge
// occupies shifts under nearly everything the editor can do to a sector:
// inserting or deleting a vertex, converting a wall to a portal, splitting or
// merging sectors, the implicit InferPortals pass, and even the loader's
// winding-reversal fixup. An index is therefore not an identity — it is not even
// stable across a save and a reload.
//
// Sector.EdgeIDs fixes that by binding a name to each ring position, exactly as
// Chunk.PointNames binds a name to each point slot. The ID belongs to the *edge*
// rather than to the wall or portal record sitting on it, which is what lets
// convertEdge flip an edge between the two kinds while the address a script uses
// keeps working.
//
// Two ways an ID comes into being:
//
//   - MigrateEdgeID, for levels written before `id:` existed. It derives a name
//     from the edge's two point names, canonicalised so the pair is unordered.
//     The loader guarantees a sector's ring visits each point exactly once, so
//     an unordered pair names at most one edge; and being unordered is what makes
//     it survive the winding reversal, which turns edge A→B into B→A and would
//     otherwise rename it on the next save.
//   - mintEdgeID, for edges the editor creates. Opaque `e<n>`, never derived from
//     point names — a name-derived ID would look meaningful and then quietly stop
//     being true the first time someone inserts a vertex next to it.

import (
	"fmt"
	"strings"
)

// MigrateEdgeID derives an edge ID from the names of its two endpoints. Only for
// loading files that predate the `id:` field; new edges use mintEdgeID.
//
// The pair is sorted so the result does not depend on which way round the edge
// is currently wound.
func MigrateEdgeID(fromName, toName string) string {
	if fromName > toName {
		fromName, toName = toName, fromName
	}
	return fromName + "|" + toName
}

// edgeIDTaken reports whether any edge of s already carries id.
func edgeIDTaken(s *Sector, id string) bool {
	for _, e := range s.EdgeIDs {
		if e == id {
			return true
		}
	}
	return false
}

// mintEdgeID returns an opaque ID no edge of s is using.
func mintEdgeID(s *Sector) string {
	for n := len(s.EdgeIDs); ; n++ {
		id := fmt.Sprintf("e%d", n)
		if !edgeIDTaken(s, id) {
			return id
		}
	}
}

// uniqueEdgeID returns base, or base with a numeric suffix that no edge of s is
// using. Used when carrying an ID into a sector that may already hold one by
// that name — a sector split, for instance, where both halves inherit edges.
func uniqueEdgeID(s *Sector, base string) string {
	if base == "" {
		return mintEdgeID(s)
	}
	if !edgeIDTaken(s, base) {
		return base
	}
	for n := 2; ; n++ {
		id := fmt.Sprintf("%s_%d", base, n)
		if !edgeIDTaken(s, id) {
			return id
		}
	}
}

// EnsureEdgeIDs pads s.EdgeIDs out to one entry per polygon edge, minting names
// for any that are missing. A malformed or older sector loads rather than
// failing, the same way EdgeKinds is padded.
func EnsureEdgeIDs(s *Sector) {
	n := len(s.PointIndices)
	for len(s.EdgeIDs) < n {
		s.EdgeIDs = append(s.EdgeIDs, "")
	}
	s.EdgeIDs = s.EdgeIDs[:n]
	for i := range s.EdgeIDs {
		if s.EdgeIDs[i] == "" {
			s.EdgeIDs[i] = mintEdgeID(s)
		}
	}
}

// DedupeSectorEdgeIDs makes every edge ID in s distinct and non-empty, keeping
// the first claim on a name and suffixing later ones. A hand-edited file can
// carry duplicates, and so can migration when a malformed ring visits a point
// twice — either way the invariant has to hold before anything reads it.
func DedupeSectorEdgeIDs(s *Sector) {
	EnsureEdgeIDs(s)
	seen := make(map[string]bool, len(s.EdgeIDs))
	for i, id := range s.EdgeIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			continue
		}
		fresh := id
		if fresh == "" {
			fresh = fmt.Sprintf("e%d", i)
		}
		for n := 2; seen[fresh] || fresh == ""; n++ {
			fresh = fmt.Sprintf("%s_%d", id, n)
		}
		s.EdgeIDs[i] = fresh
		seen[fresh] = true
	}
}

// EdgeIDAt returns the ID of polygon edge ei, or "" if ei is out of range.
func EdgeIDAt(s *Sector, ei int) string {
	if ei < 0 || ei >= len(s.EdgeIDs) {
		return ""
	}
	return s.EdgeIDs[ei]
}

// PolyEdgeIdxByID returns the polygon edge index carrying id, or -1.
func PolyEdgeIdxByID(s *Sector, id string) int {
	for i, e := range s.EdgeIDs {
		if e == id {
			return i
		}
	}
	return -1
}

// EdgeRecordByID resolves an edge ID to the kind of record holding it and that
// record's index within Walls or Portals. This is what a script's
// `@sector.edge.<id>` resolves through.
func EdgeRecordByID(s *Sector, id string) (kind string, idx int, ok bool) {
	ei := PolyEdgeIdxByID(s, id)
	if ei < 0 {
		return "", 0, false
	}
	kind, idx = EdgeKindAndIndex(s, ei)
	if kind == "wall" && idx < len(s.Walls) {
		return kind, idx, true
	}
	if kind == "portal" && idx < len(s.Portals) {
		return kind, idx, true
	}
	return "", 0, false
}

// WallByEdgeID and PortalByEdgeID return the record for an edge ID, or nil when
// the edge is currently the other kind.
func WallByEdgeID(s *Sector, id string) *Wall {
	kind, idx, ok := EdgeRecordByID(s, id)
	if !ok || kind != "wall" {
		return nil
	}
	return &s.Walls[idx]
}

func PortalByEdgeID(s *Sector, id string) *Portal {
	kind, idx, ok := EdgeRecordByID(s, id)
	if !ok || kind != "portal" {
		return nil
	}
	return &s.Portals[idx]
}

// SyncEdgeRecordIDs copies each polygon edge's ID onto the Wall or Portal record
// holding it. The record's own ID field is a convenience for code that already
// has the record in hand; EdgeIDs is the authority, and this is what keeps the
// two from drifting after a rebuild.
func SyncEdgeRecordIDs(s *Sector) {
	EnsureEdgeIDs(s)
	for ei := range s.EdgeIDs {
		kind, idx := EdgeKindAndIndex(s, ei)
		switch {
		case kind == "wall" && idx < len(s.Walls):
			s.Walls[idx].ID = s.EdgeIDs[ei]
		case kind == "portal" && idx < len(s.Portals):
			s.Portals[idx].ID = s.EdgeIDs[ei]
		}
	}
}

// validateSectorEdges asserts the invariant every mutation has to preserve: the
// three ring-aligned slices agree in length, every edge names exactly one record
// of its own kind, and no two edges share an ID.
//
// This is the single check worth running after each editor operation in tests —
// it catches the alignment drift that index-counting reconstruction cannot.
func validateSectorEdges(s *Sector) error {
	n := len(s.PointIndices)
	if len(s.EdgeKinds) != n {
		return fmt.Errorf("sector %q: %d edge kinds for %d points", s.ID, len(s.EdgeKinds), n)
	}
	if len(s.EdgeIDs) != n {
		return fmt.Errorf("sector %q: %d edge IDs for %d points", s.ID, len(s.EdgeIDs), n)
	}
	if len(s.Walls)+len(s.Portals) != n {
		return fmt.Errorf("sector %q: %d walls + %d portals for %d edges",
			s.ID, len(s.Walls), len(s.Portals), n)
	}
	seen := make(map[string]int, n)
	nWall, nPortal := 0, 0
	for ei := 0; ei < n; ei++ {
		id := s.EdgeIDs[ei]
		if id == "" {
			return fmt.Errorf("sector %q: edge %d has no ID", s.ID, ei)
		}
		if prev, dup := seen[id]; dup {
			return fmt.Errorf("sector %q: edges %d and %d share ID %q", s.ID, prev, ei, id)
		}
		seen[id] = ei

		switch s.EdgeKinds[ei] {
		case "wall":
			nWall++
		case "portal":
			nPortal++
		default:
			return fmt.Errorf("sector %q: edge %d has unknown kind %q", s.ID, ei, s.EdgeKinds[ei])
		}
	}
	if nWall != len(s.Walls) {
		return fmt.Errorf("sector %q: %d edges of kind wall but %d wall records", s.ID, nWall, len(s.Walls))
	}
	if nPortal != len(s.Portals) {
		return fmt.Errorf("sector %q: %d edges of kind portal but %d portal records", s.ID, nPortal, len(s.Portals))
	}
	return nil
}

// ValidateChunkEdges runs validateSectorEdges over every sector in ch.
func ValidateChunkEdges(ch *Chunk) error {
	for si := range ch.Sectors {
		if err := validateSectorEdges(&ch.Sectors[si]); err != nil {
			return err
		}
	}
	return nil
}

// InsertEdgeID splices a new ID in after polygon edge ei, keeping EdgeIDs
// aligned with an EdgeKinds insert made at the same position. The edge being
// split keeps its own ID; the new half gets a fresh one.
func InsertEdgeID(s *Sector, ei int) {
	EnsureEdgeIDs(s)
	if ei < 0 || ei >= len(s.EdgeIDs) {
		return
	}
	fresh := mintEdgeID(s)
	out := make([]string, len(s.EdgeIDs)+1)
	copy(out, s.EdgeIDs[:ei+1])
	out[ei+1] = fresh
	copy(out[ei+2:], s.EdgeIDs[ei+1:])
	s.EdgeIDs = out
}

// RemoveEdgeID drops the entry for polygon edge ei.
func RemoveEdgeID(s *Sector, ei int) {
	if ei < 0 || ei >= len(s.EdgeIDs) {
		return
	}
	s.EdgeIDs = append(s.EdgeIDs[:ei], s.EdgeIDs[ei+1:]...)
}

// EdgeIDDisplay renders an ID for the editor's panels. Migrated IDs read as
// "A|B", which is meaningful to a level author; minted ones read as "e3".
func EdgeIDDisplay(id string) string {
	if id == "" {
		return "(none)"
	}
	if strings.Contains(id, "|") {
		return strings.Replace(id, "|", " → ", 1)
	}
	return id
}

// EdgeKindAndIndex returns the edge type and its index within Walls or Portals
// for polygon edge ei, using EdgeKinds for reliable mixed-type lookup.
func EdgeKindAndIndex(s *Sector, ei int) (string, int) {
	if ei < 0 || ei >= len(s.EdgeKinds) {
		return "wall", 0
	}
	kind := s.EdgeKinds[ei]
	idx := 0
	for i := 0; i < ei; i++ {
		if i < len(s.EdgeKinds) && s.EdgeKinds[i] == kind {
			idx++
		}
	}
	return kind, idx
}
