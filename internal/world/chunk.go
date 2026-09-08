package world

import (
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"log"

	"github.com/go-gl/mathgl/mgl32"
)

// Chunk is a top-level spatial grouping of sectors.
//
// Points, MinBound/MaxBound, Sector bounds, BiomeSector points, and every
// other per-chunk coordinate are chunk-LOCAL, always — authored around the
// chunk's own origin, the same way a frame-owned chunk's geometry always has
// been. Origin/Rotation/Scale is this chunk's own live placement, a
// counterpart to a frame's Xform: ModelMatrix() is the one place that carries
// local coordinates into world space (or into an owning frame's space, for a
// frame-owned chunk — see FrameOwner below). Nothing else needs to know
// whether a chunk has moved.
//
// PointNames is index-aligned with Points and carries each point's YAML name so
// SaveLevel can round-trip it. It must be maintained by every mutation of the
// point pool — use AddPoint/RemovePoint rather than touching Points directly.
// An empty string means "not yet named"; SaveLevel mints a stable unique name
// for those slots. Binding names to indices rather than to coordinates is what
// lets a point keep its identity when the editor drags it.
type Chunk struct {
	ID           string
	ScriptData                // tags, attributes and the attached script
	Points       []mgl32.Vec2 // shared point pool, chunk-local; sectors reference by index
	Sectors      []Sector
	MinBound     mgl32.Vec3 // chunk-local; see WorldBounds for the world-space AABB
	MaxBound     mgl32.Vec3
	Origin       mgl32.Vec3    // this chunk's live translation; see ModelMatrix
	Rotation     float32       // this chunk's live Y-axis rotation, in radians; see ModelMatrix
	Scale        float32       // this chunk's live uniform scale (0 = 1.0); see ModelMatrix
	PointNames   []string      // index-aligned with Points; "" = unnamed
	BiomeSectors []BiomeSector // user-drawn biome-paint polygons, owned by this chunk

	// Velocity is chunk units per second, added to Origin every tick of play
	// (AdvanceChunks, chunk_motion.go) — a lift that rises, a barge that
	// drifts, a wall that closes in, with nothing writing the origin every
	// frame to do it. Authored state the engine carries forward, exactly like
	// an entity's own Velocity: nothing damps it and nothing stops it, because
	// what should slow a platform down is a rule the game has rather than one
	// the engine invents.
	//
	// It is in the same space as Origin — world for an ordinary chunk, the
	// carrier's for a frame-owned one — so a moving chunk inside a moving ship
	// moves within the ship.
	//
	// A body standing on it is not carried just by colliding with it — that
	// still leaves it behind, exactly as before. An entity can be pinned to
	// this chunk explicitly instead, via its own AttachedChunk: see
	// CarryAttachedEntities, which every writer of Origin (this one included)
	// is expected to call. An interior meant to be properly ridden — walked
	// around inside, crossed through a portal — belongs to a frame (FrameOwner
	// below) and a vehicle, not this.
	Velocity mgl32.Vec3

	// Layers is the metadata for this chunk's authoring bands — the "levels"
	// sectors are drawn on, which is how one chunk holds geometry that overlaps
	// in XZ. Indexed by Sector.Layer / BiomeSector.Layer.
	//
	// May be shorter than the highest layer index in use, or absent entirely:
	// a sector's Layer is the durable fact and this is metadata layered over
	// it, synthesized on demand for any band that lacks an entry. Read it
	// through LayerAt/LayerCount rather than indexing directly. See layer.go.
	Layers []ChunkLayer

	// Disabled takes the chunk out of the world entirely: nothing draws it,
	// nothing collides with it, and its vertex buffers are released. It is what
	// a streaming script toggles as the player moves between areas — see
	// chunk_enable.go, which owns both this flag and Unloaded.
	//
	// Disabled rather than Enabled so the zero value is "on": every chunk
	// written before this existed, and every one a test or an import builds by
	// hand, means to be part of the level.
	Disabled bool

	// Unloaded is runtime-only (never serialized, never cloned): whether the
	// chunk's geometry has actually been released yet. It trails Disabled by up
	// to a frame, because writing the flag may happen anywhere while freeing
	// buffers is gl.* work — SyncChunkGeometry is what closes the gap.
	Unloaded bool

	// FrameOwner names the entity whose local space this chunk's geometry is
	// authored in — a vehicle's interior, carried around the world by whatever
	// owns it. Empty is the ordinary case: a chunk placed directly in world
	// space by its own Origin/Rotation/Scale, with no carrier.
	//
	// A frame-owned chunk is deliberately kept out of the world spatial hash.
	// Its geometry sits around its own origin, so indexing it by those
	// coordinates would have a player standing at world (0,0) fall into a ship
	// parked a mile away.
	FrameOwner string

	// FromEntity is runtime-only (never serialized, never cloned): the entity
	// whose *definition* brought this chunk into the level, as one of its chunk
	// attachments (entity_chunk.go). Empty for a chunk the level file authored.
	//
	// It is what keeps the two kinds apart at save time. A materialised chunk is
	// the definition's content, not the level's, so LevelToRaw skips it — a
	// level that wrote one out would freeze a copy of the ship's hull into
	// itself and stop tracking the file every other level places.
	//
	// Not copied by Clone: a duplicated chunk is a new piece of level geometry
	// that no entity owns, and carrying the binding over would produce a second
	// chunk claiming to be the same attachment.
	FromEntity string

	// Xform is runtime-only (never serialized, never cloned): where this
	// chunk's frame currently is, refreshed every tick by RefreshFrames.
	// Identity for a chunk with no FrameOwner. ModelMatrix composes this with
	// the chunk's own Origin/Rotation/Scale, so a frame-owned chunk keeps its
	// authored placement within the frame even as the frame itself moves.
	Xform mgl32.Mat4

	// FrameScale is runtime-only alongside Xform (never serialized, never
	// cloned): the scale of the entity whose frame this chunk rides in,
	// republished every tick by RefreshFrames. 0 — what every unframed chunk
	// carries — reads as 1. Clear the two together with ClearFrameTransform.
	//
	// It is a field of its own rather than a factor folded into Xform because
	// the two are read in different spaces. Xform places the frame in the
	// world; this scales the chunk's geometry *within* that frame, which is the
	// space collision resolves in — the resolvers work from
	// LocalPlacementMatrix, having already had Xform divided out by the caller
	// that found the chunk. Folded into Xform, a ship scaled up would draw a
	// bigger hold with the deck a body walks on left exactly where it was.
	//
	// Scaling the carried geometry rather than the frame itself is also why a
	// rider is not scaled: the frame stays a rigid place to stand, so someone
	// boarding a ship scaled 2x keeps their own size and finds a hold twice as
	// big. See FrameLocalMatrix.
	FrameScale float32

	// WaterSurfaces is runtime-only (never serialized, never cloned): the
	// companion water planes auto-managed from Water-kind BiomeSectors (see
	// biome_water.go). They are fully derived from BiomeSectors and rebuilt on
	// load and whenever the owning sector changes.
	WaterSurfaces []WaterSurface
}

// AddPoint appends a point to the shared pool and returns its index, keeping
// PointNames index-aligned. The point starts unnamed; SaveLevel assigns it a
// stable name on the next serialise.
func (ch *Chunk) AddPoint(p mgl32.Vec2) int {
	idx := len(ch.Points)
	ch.Points = append(ch.Points, p)
	for len(ch.PointNames) < idx {
		ch.PointNames = append(ch.PointNames, "")
	}
	ch.PointNames = append(ch.PointNames, "")
	return idx
}

// RemovePoint deletes the point at index pi from the shared pool, keeping
// PointNames index-aligned. Callers remain responsible for fixing up sector
// PointIndices and any other index references.
func (ch *Chunk) RemovePoint(pi int) {
	if pi < 0 || pi >= len(ch.Points) {
		return
	}
	ch.Points = append(ch.Points[:pi], ch.Points[pi+1:]...)
	if pi < len(ch.PointNames) {
		ch.PointNames = append(ch.PointNames[:pi], ch.PointNames[pi+1:]...)
	}
}

// Clone returns a fully-independent deep copy of the chunk. All slices and
// maps get fresh backing arrays so edits to the copy do not affect the source.
// GPU geometry handles are intentionally zeroed — the caller must re-upload
// sector/gap geometry before the clone can be rendered. Sector IDs are
// rewritten with idSuffix so they stay unique within the
// level; in-chunk portal ToSector references are remapped to the new IDs.
func (ch *Chunk) Clone(newID, idSuffix string) Chunk {
	cp := Chunk{
		ID:         newID,
		ScriptData: CloneScriptData(ch.ScriptData),
		MinBound:   ch.MinBound,
		MaxBound:   ch.MaxBound,
		Origin:     ch.Origin,
		Rotation:   ch.Rotation,
		Scale:      ch.Scale,
		// Velocity rides along with the placement it is a rate of change of: a
		// copy of a moving platform is another moving platform, not one that
		// has stopped dead where the original happened to be.
		Velocity: ch.Velocity,
		// FrameOwner rides along with Origin/Rotation/Scale because it is part
		// of the same answer to "where is this chunk". Dropping it turned a
		// copy of a vehicle interior into a world chunk whose geometry — still
		// authored around the ship's local origin — was then read as world
		// geometry at (0,0,0). Xform is deliberately NOT copied: it is
		// runtime-only and RefreshFrames owns it (ModelMatrix treats the zero
		// value as "not yet refreshed" and falls back to the local placement),
		// and FrameScale is left behind with it for the same reason.
		FrameOwner: ch.FrameOwner,
		// Disabled rides along for the same reason FrameOwner does: it is part
		// of the answer to "is this chunk in the world". Unloaded deliberately
		// does NOT — the copy has no buffers of its own yet, and the caller is
		// already required to re-upload its geometry (see this method's doc).
		Disabled: ch.Disabled,
		Layers:   append([]ChunkLayer(nil), ch.Layers...),
	}

	cp.Points = append([]mgl32.Vec2(nil), ch.Points...)

	cp.PointNames = append([]string(nil), ch.PointNames...)

	// Build old-sector-ID → new-sector-ID map for portal remapping.
	sectorIDRemap := make(map[string]string, len(ch.Sectors))
	for i := range ch.Sectors {
		oldID := ch.Sectors[i].ID
		sectorIDRemap[oldID] = oldID + idSuffix
	}

	cp.Sectors = make([]Sector, len(ch.Sectors))
	for si := range ch.Sectors {
		cp.Sectors[si] = cloneSector(&ch.Sectors[si], idSuffix, sectorIDRemap)
	}

	if ch.BiomeSectors != nil {
		cp.BiomeSectors = make([]BiomeSector, len(ch.BiomeSectors))
		for bi := range ch.BiomeSectors {
			src := &ch.BiomeSectors[bi]
			// The drape binding names a sector, and every sector in this clone
			// was just renamed — so it is remapped alongside the portal
			// targets. Left un-remapped it would point at the ORIGINAL
			// chunk's surface, which is a different height in a different
			// place. A binding to something outside this chunk (spilled
			// paint) is not in the map and stays as it is.
			drape := src.DrapeSector
			if to, ok := sectorIDRemap[drape]; ok {
				drape = to
			}
			cp.BiomeSectors[bi] = BiomeSector{
				ID:          src.ID + idSuffix,
				DrapeSector: drape,
				Points:      append([]mgl32.Vec2(nil), src.Points...),
				Brush:       src.Brush,
				Layer:       src.Layer,
				HeightDirty: true,
				GroundDirty: true,
			}
		}
	}
	// WaterSurfaces is intentionally left empty — it's fully derived from
	// BiomeSectors (marked GroundDirty above) and gets rebuilt the next time
	// SyncBiomeWaterSurfaces runs.

	return cp
}

func cloneSector(s *Sector, idSuffix string, sectorIDRemap map[string]string) Sector {
	out := Sector{
		ID:         s.ID + idSuffix,
		ScriptData: CloneScriptData(s.ScriptData),
		MinBound:   s.MinBound,
		MaxBound:   s.MaxBound,
		Layer:      s.Layer,
	}
	out.PointIndices = append([]int(nil), s.PointIndices...)
	out.EdgeKinds = append([]string(nil), s.EdgeKinds...)
	// Edge IDs are scoped to their sector, so a clone keeps them as they are —
	// unlike the sector ID itself, which is level-scoped and takes the suffix.
	out.EdgeIDs = append([]string(nil), s.EdgeIDs...)

	out.Walls = make([]Wall, len(s.Walls))
	for i := range s.Walls {
		out.Walls[i] = cloneWall(&s.Walls[i])
	}

	out.Portals = make([]Portal, len(s.Portals))
	for i := range s.Portals {
		out.Portals[i] = clonePortal(&s.Portals[i], sectorIDRemap)
	}

	out.Floor = cloneFloor(&s.Floor)
	out.Ceiling = cloneCeiling(&s.Ceiling)
	return out
}

func cloneWall(w *Wall) Wall {
	out := *w
	out.ScriptData = CloneScriptData(w.ScriptData)
	out.Geometry = gfx.Geometry{}
	out.Mesh = w.Mesh.Clone()
	out.SubmeshGeometries = make([]gfx.Geometry, len(w.SubmeshGeometries))
	return out
}

func clonePortal(p *Portal, sectorIDRemap map[string]string) Portal {
	out := *p
	out.ScriptData = CloneScriptData(p.ScriptData)
	out.FloorGap = gfx.Geometry{}
	out.CeilGap = gfx.Geometry{}
	if remapped, ok := sectorIDRemap[p.ToSector]; ok {
		out.ToSector = remapped
	}
	return out
}

func cloneFloor(f *Floor) Floor {
	out := *f
	out.ScriptData = CloneScriptData(f.ScriptData)
	out.Geometry = gfx.Geometry{}
	out.VertexOffsets = append([]float32(nil), f.VertexOffsets...)
	out.Mesh = f.Mesh.Clone()
	out.Openings = CloneOpenings(f.Openings)
	out.SkirtGeometry = gfx.Geometry{} // derived; RebuildSkirts rebuilds it
	out.Slab.Geometry = gfx.Geometry{} // derived; Sector.Load rebuilds it
	out.Slab.RimGeometry = gfx.Geometry{}
	out.Slab.SubmeshGeometries = make([]gfx.Geometry, len(f.Slab.SubmeshGeometries))
	out.Slab.Mesh = f.Slab.Mesh.Clone()
	out.Slab.CollisionPts = append([]mgl32.Vec2(nil), f.Slab.CollisionPts...)
	out.Slab.CollisionDepths = append([]float32(nil), f.Slab.CollisionDepths...)
	out.Slab.CollisionTris = append([]int(nil), f.Slab.CollisionTris...)
	out.CollisionPts = append([]mgl32.Vec2(nil), f.CollisionPts...)
	out.CollisionDepths = append([]float32(nil), f.CollisionDepths...)
	out.CollisionTris = append([]int(nil), f.CollisionTris...)
	out.SubmeshGeometries = make([]gfx.Geometry, len(f.SubmeshGeometries))
	return out
}

func cloneCeiling(c *Ceiling) Ceiling {
	out := *c
	out.ScriptData = CloneScriptData(c.ScriptData)
	out.Geometry = gfx.Geometry{}
	out.VertexOffsets = append([]float32(nil), c.VertexOffsets...)
	out.Mesh = c.Mesh.Clone()
	out.Openings = CloneOpenings(c.Openings)
	out.Slab.Geometry = gfx.Geometry{} // derived; Sector.Load rebuilds it
	out.Slab.RimGeometry = gfx.Geometry{}
	out.Slab.SubmeshGeometries = make([]gfx.Geometry, len(c.Slab.SubmeshGeometries))
	out.Slab.Mesh = c.Slab.Mesh.Clone()
	out.Slab.CollisionPts = append([]mgl32.Vec2(nil), c.Slab.CollisionPts...)
	out.Slab.CollisionDepths = append([]float32(nil), c.Slab.CollisionDepths...)
	out.Slab.CollisionTris = append([]int(nil), c.Slab.CollisionTris...)
	out.CollisionPts = append([]mgl32.Vec2(nil), c.CollisionPts...)
	out.CollisionDepths = append([]float32(nil), c.CollisionDepths...)
	out.CollisionTris = append([]int(nil), c.CollisionTris...)
	out.SubmeshGeometries = make([]gfx.Geometry, len(c.SubmeshGeometries))
	return out
}

func cloneExtrusions(src []Extrusion) []Extrusion {
	if src == nil {
		return nil
	}
	out := make([]Extrusion, len(src))
	for i := range src {
		out[i] = src[i]
		out[i].Outer = append([]ExtrusionPoint(nil), src[i].Outer...)
		out[i].Inner = append([]ExtrusionPoint(nil), src[i].Inner...)
	}
	return out
}

// RecomputeBounds re-derives the chunk's local AABB (MinBound/MaxBound; see
// WorldBounds for the world-space box used to frustum-cull the whole chunk
// before descending into per-sector culling) from its current sectors and
// water surfaces. Sector.Load keeps each sector's own bounds accurate, but
// nothing else rolls that up to the chunk level, so any editor operation that
// adds/removes/moves a sector without going through InferPortalsAndReload
// leaves MinBound/MaxBound stale — geometry added outside the original bounds
// then gets frustum-culled along with the rest of the chunk even when it's on
// screen.
func (ch *Chunk) RecomputeBounds() {
	if len(ch.Sectors) == 0 && len(ch.WaterSurfaces) == 0 {
		return
	}
	first := true
	extend := func(min, max mgl32.Vec3) {
		if first {
			ch.MinBound = min
			ch.MaxBound = max
			first = false
			return
		}
		for i := 0; i < 3; i++ {
			if min[i] < ch.MinBound[i] {
				ch.MinBound[i] = min[i]
			}
			if max[i] > ch.MaxBound[i] {
				ch.MaxBound[i] = max[i]
			}
		}
	}

	for si := range ch.Sectors {
		extend(ch.Sectors[si].LocalAABB())
	}

	for wi := range ch.WaterSurfaces {
		extend(ch.WaterSurfaces[wi].LocalAABB())
	}
}

// InferPortals detects edges shared between two sectors in this chunk and
// converts wall edges into portal connections. An edge is "shared" when sector
// A has edge (P→Q) and sector B has edge (Q→P) — i.e. the same two chunk
// points in opposite winding order. Existing portals are preserved and have
// their ToSector updated; wall edges that match a reverse edge in another
// sector are promoted to portals with DrawGap enabled.
func (ch *Chunk) InferPortals() {
	type edgeKey struct{ a, b int }

	// Map each directed edge to EVERY sector that walks it, not just one.
	//
	// A single owner was enough while a chunk was one storey deep, because only
	// one sector could ever run along a given edge in a given direction. Layers
	// broke that: a building with two floors subdivided the same way has a
	// ground-floor sector and a first-floor sector walking the identical edge,
	// and keeping only the last one seen made a downstairs room's doorway open
	// onto a room upstairs — with the wall between them gone, and the floor
	// query folding in a height from the wrong storey.
	edgeOwners := map[edgeKey][]int{}
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		n := len(s.PointIndices)
		for ei := 0; ei < n; ei++ {
			a := s.PointIndices[ei]
			b := s.PointIndices[(ei+1)%n]
			edgeOwners[edgeKey{a, b}] = append(edgeOwners[edgeKey{a, b}], si)
		}
	}

	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		n := len(s.PointIndices)
		EnsureEdgeIDs(s)

		var newWalls []Wall
		var newPortals []Portal
		newEdgeKinds := make([]string, n)
		newEdgeIDs := make([]string, n)

		wallIdx, portalIdx := 0, 0
		for ei := 0; ei < n; ei++ {
			a := s.PointIndices[ei]
			b := s.PointIndices[(ei+1)%n]

			kind := "wall"
			if ei < len(s.EdgeKinds) {
				kind = s.EdgeKinds[ei]
			}
			// The edge's ID is the one thing that survives whatever this loop
			// decides the edge should become.
			edgeID := s.EdgeIDs[ei]
			newEdgeIDs[ei] = edgeID

			revOwner, isShared := pickPortalNeighbour(ch, si, edgeOwners[edgeKey{b, a}])

			if kind == "wall" && wallIdx < len(s.Walls) {
				if isShared && !s.Walls[wallIdx].Pinned {
					// Promote wall → portal. The ID carries across: this is the
					// same edge, and a script addressing it must not lose it
					// because the engine changed the edge's kind underneath.
					w := s.Walls[wallIdx]
					newEdgeKinds[ei] = "portal"
					newPortals = append(newPortals, Portal{
						ID:          edgeID,
						StartPoint:  w.StartPoint,
						EndPoint:    w.EndPoint,
						ToSector:    ch.Sectors[revOwner].ID,
						DrawGap:     true,
						Visible:     w.Visible,
						DoubleSided: w.DoubleSided,
						// Opaque, the same default the loader gives a portal
						// whose file says nothing. Leaving it zero made the
						// gap fully transparent the moment ReloadSector
						// propagated it (sector.go), which reads as a gap that
						// simply stopped being drawn.
						Alpha: 1.0,
					})
				} else {
					w := s.Walls[wallIdx]
					w.ID = edgeID
					newEdgeKinds[ei] = "wall"
					newWalls = append(newWalls, w)
				}
				wallIdx++
			} else if kind == "portal" && portalIdx < len(s.Portals) {
				p := s.Portals[portalIdx]
				p.ID = edgeID
				if isShared {
					// Update target in case sector IDs shifted
					p.ToSector = ch.Sectors[revOwner].ID
				}
				newEdgeKinds[ei] = "portal"
				newPortals = append(newPortals, p)
				portalIdx++
			} else {
				// Kind/record mismatch. Emit a blank wall rather than nothing:
				// dropping the edge would leave Walls one short of the ring and
				// silently shift every record after it, which is far worse than
				// an untextured wall the author can see and fix.
				log.Printf("InferPortals: sector %q edge %d (%q): kind %q but no matching wall/portal entry; substituting a blank wall",
					s.ID, ei, edgeID, kind)
				newEdgeKinds[ei] = "wall"
				newWalls = append(newWalls, Wall{
					ID:          edgeID,
					StartPoint:  ch.Points[a],
					EndPoint:    ch.Points[b],
					Collision:   true,
					Visible:     true,
					DoubleSided: true,
					Alpha:       1.0,
					UVScale:     mgl32.Vec2{1, 1},
				})
			}
		}

		s.EdgeKinds = newEdgeKinds
		s.EdgeIDs = newEdgeIDs
		s.Walls = newWalls
		s.Portals = newPortals
	}
}

// pickPortalNeighbour chooses which of the sectors walking the reverse of an
// edge is actually on the other side of it.
//
// Sharing an edge in plan is not enough once a chunk has storeys: the ground
// floor and the floor above it are drawn over the same lines from above, and
// two rooms that share a wall on one storey share it on both. What separates a
// doorway from a coincidence is HEIGHT — a portal joins two spaces a body could
// pass between, so the two sectors' vertical extents have to overlap.
//
// Where several candidates still overlap (a mezzanine open to the hall it looks
// over, say), the one sharing the most vertical extent wins, since that is the
// one the edge is most of a doorway into.
func pickPortalNeighbour(ch *Chunk, si int, candidates []int) (int, bool) {
	s := &ch.Sectors[si]
	lo, hi := sectorSpan(s)

	best, bestOverlap := -1, float32(0)
	for _, ci := range candidates {
		if ci == si {
			continue
		}
		clo, chi := sectorSpan(&ch.Sectors[ci])
		overlap := minFloat32(hi, chi) - maxFloat32(lo, clo)
		if overlap <= 0 && !spanCollapsedInside(lo, hi, clo, chi) {
			continue // stacked, not adjacent: no way through
		}
		if best < 0 || overlap > bestOverlap {
			best, bestOverlap = ci, overlap
		}
	}
	return best, best >= 0
}

// spanCollapsedInside reports whether one of two vertical spans has closed to
// nothing — or turned inside out — within the other.
//
// A door can be built as a sector's own moving surface: the dropship's is its
// doorway's ceiling coming down (chunks/dropship_hold.yaml), and the same trick
// works with a floor coming up. Either way the sector collapses to zero height
// as the leaf seats, and inverts if the moving surface travels past the fixed
// one. Its span can then never overlap a neighbour's by more than nothing,
// which the ranking above would read as "stacked, no way through": the doorway
// would stop being anyone's portal neighbour for as long as it was shut.
//
// That is not a cosmetic mis-ranking. InferPortals repairs a portal's ToSector
// only for an edge it believes is shared, so a doorway saved shut came back
// with its target never updated — and RebuildGaps skips a portal whose target
// names nothing, which is a door whose leaf is not drawn at all, in a level
// that reloads that way every time.
//
// A sector is not a different room because it is shut. What separates the two
// cases is whether the collapsed span sits INSIDE the neighbour's, rather than
// at or beyond its end — a storey stacked on another still meets it exactly at
// the slab, and still has no way through.
func spanCollapsedInside(lo, hi, clo, chi float32) bool {
	inside := func(lo, hi, x float32) bool { return x > lo && x < hi }
	if chi <= clo {
		return inside(lo, hi, minFloat32(clo, chi)) || inside(lo, hi, maxFloat32(clo, chi))
	}
	if hi <= lo {
		return inside(clo, chi, minFloat32(lo, hi)) || inside(clo, chi, maxFloat32(lo, hi))
	}
	return false
}

// sectorSpan is the vertical extent a body could occupy in a sector: its floor
// up to whatever caps it. A sector with no ceiling is capped by its wall tops
// rather than treated as infinite, so an open-air sector does not overlap
// everything stacked above it.
func sectorSpan(s *Sector) (lo, hi float32) {
	return s.Floor.Height, s.wallTopBase()
}

func minFloat32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func maxFloat32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

// InferPortalsAndReload runs InferPortals, then reloads geometry for every
// sector and rebuilds gap walls. Use this after editor operations that add
// or modify sectors.
func (ch *Chunk) InferPortalsAndReload() {
	ch.InferPortals()
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		_ = ReloadSector(ch, s)
	}
	ch.RebuildGaps()
	ch.RecomputeBounds()
}

// RebuildGaps regenerates portal gap geometry for all sectors in this chunk.
// Call after changing DrawGap, GapTexture, or floor/ceiling heights.
func (ch *Chunk) RebuildGaps() {
	// Skirts are the other half of the same job — cross-sector geometry no
	// single sector's own surfaces cover — so they are rebuilt here rather than
	// needing every one of this method's callers taught about them separately.
	defer ch.RebuildSkirts()

	sectorByID := map[string]*Sector{}
	for i := range ch.Sectors {
		sectorByID[ch.Sectors[i].ID] = &ch.Sectors[i]
	}
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		for pi := range s.Portals {
			p := &s.Portals[pi]
			// Clear existing gap geometry. Unload rather than dropping the
			// vertices on the floor: a gap that stops existing — the floor a
			// script is animating passing its neighbour's — would otherwise
			// leave its VAO and VBO allocated with nothing left holding the
			// handles. Load re-uploads either way.
			p.FloorGap.Unload()
			p.CeilGap.Unload()

			if !p.DrawGap {
				continue
			}
			adj, ok := sectorByID[p.ToSector]
			if !ok {
				continue
			}
			tex := p.GapTexture
			if tex == "" {
				tex = s.Floor.Texture
			}
			start := p.StartPoint
			end := p.EndPoint
			segLen := mgl32.Vec2{end.X() - start.X(), end.Y() - start.Y()}.Len()
			if segLen < 1e-6 {
				continue // degenerate zero-length edge
			}

			// Floor riser: only drawn from the lower-floor side.
			if s.Floor.Height < adj.Floor.Height {
				lo1, lo2 := edgeHeights(ch, s, s.Floor.VertexHeight, start, end)
				hi1, hi2 := edgeHeights(ch, adj, adj.Floor.VertexHeight, start, end)
				verts, indices := buildGapQuad(start, end, lo1, lo2, hi1, hi2, segLen)
				_ = p.FloorGap.Load(verts, indices, tex, mgl32.Vec3{})
			}

			// Ceiling filler: only drawn from the higher-ceiling side.
			if s.Ceiling.Enabled && adj.Ceiling.Enabled && s.Ceiling.Height > adj.Ceiling.Height {
				lo1, lo2 := edgeHeights(ch, adj, adj.Ceiling.VertexHeight, start, end)
				hi1, hi2 := edgeHeights(ch, s, s.Ceiling.VertexHeight, start, end)
				verts, indices := buildGapQuad(start, end, lo1, lo2, hi1, hi2, segLen)
				_ = p.CeilGap.Load(verts, indices, tex, mgl32.Vec3{})
			}
		}
	}
}

// buildGapQuad returns the vertices and indices for a vertical quad spanning
// from lo to hi between start and end (in XZ), with the given segment length.
// Pure sector/polygon geometry — moved here (from level.go) to sit beside its
// only caller, RebuildGaps, per RESTRUCTURE_PROPOSAL.md §5 Phase A.
func buildGapQuad(start, end mgl32.Vec2, lo1, lo2, hi1, hi2, segLen float32) ([][]float32, []int) {
	// Sloped surfaces can cross within the edge; collapse rather than invert
	// the quad so the face never winds backwards.
	if hi1 < lo1 {
		hi1 = lo1
	}
	if hi2 < lo2 {
		hi2 = lo2
	}
	a := []float32{start.X(), hi1, start.Y(), 0, 0}
	b := []float32{end.X(), hi2, end.Y(), segLen, 0}
	c := []float32{end.X(), lo2, end.Y(), segLen, hi2 - lo2}
	d := []float32{start.X(), lo1, start.Y(), 0, hi1 - lo1}
	return [][]float32{a, b, c, d}, []int{0, 1, 2, 2, 3, 0}
}

// reverseTriangleWinding turns every triangle in an index buffer round in
// place, so the geometry it describes faces the other way.
//
// Buffers are flat triples throughout (Geometry.Load de-indexes them in this
// order), so swapping the first and last of each triple is the whole job. A
// trailing partial triple is left alone rather than dropped: it is already
// malformed, and this is not the place that would notice.
func reverseTriangleWinding(indices []int) {
	for i := 0; i+2 < len(indices); i += 3 {
		indices[i], indices[i+2] = indices[i+2], indices[i]
	}
}

// edgeHeights returns a sector surface's height at the two endpoints of an edge,
// honoring per-vertex offsets. Endpoints that aren't sector vertices fall back
// to the surface's flat height.
func edgeHeights(ch *Chunk, s *Sector, vertexHeight func(int) float32, start, end mgl32.Vec2) (float32, float32) {
	pts := SectorPoints(ch, s)
	return vertexHeight(FindVertexIdx(pts, start)), vertexHeight(FindVertexIdx(pts, end))
}

// SignedArea2D computes the signed area of a 2D polygon (shoelace formula).
// Positive = CCW winding (interior to the left); negative = CW.
func SignedArea2D(pts []mgl32.Vec2) float32 {
	n := len(pts)
	area := float32(0)
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		area += pts[i].X()*pts[j].Y() - pts[j].X()*pts[i].Y()
	}
	return area / 2
}

// InFrame reports whether this chunk's geometry is authored in a moving frame
// rather than in world space.
func (ch *Chunk) InFrame() bool { return ch.FrameOwner != "" }

// chunkLocalMatrix builds the transform a chunk's own Origin/Rotation/Scale
// describe: translate, then rotate about Y, then a uniform scale. Scale of 0
// means "not set" (1.0), the same convention the YAML loader has always used.
//
// The scale is uniform on purpose, not XZ-only: a chunk's local geometry
// (points, and floor/ceiling Height baked straight into vertex Y) is one
// coordinate space, and one matrix carries all of it into the parent space
// together. Scaling Y independently would need Height pulled back out and
// rescaled by hand wherever geometry is built — this way it falls out for
// free from composing a single matrix.
func chunkLocalMatrix(origin mgl32.Vec3, rotation, scale float32) mgl32.Mat4 {
	sc := scale
	if sc == 0 {
		sc = 1
	}
	T := mgl32.Translate3D(origin.X(), origin.Y(), origin.Z())
	R := mgl32.HomogRotate3DY(rotation)
	S := mgl32.Scale3D(sc, sc, sc)
	return T.Mul4(R).Mul4(S)
}

// LocalPlacementMatrix is this chunk's own Origin/Rotation/Scale, without an
// owning frame's transform layered on — the piece of ModelMatrix a chunk
// contributes on its own. FindChunkAndSectorAmong uses exactly this: its
// caller has already carried the query position out of the frame, so only the
// chunk's own placement within that frame remains to undo.
// It carries the owning entity's scale (FrameScale) as well as the chunk's own,
// which is what makes a ship's hold grow with the ship: the geometry a rider
// walks on is expressed in this space, so a factor applied here reaches the
// drawn floor and the collided one together.
func (ch *Chunk) LocalPlacementMatrix() mgl32.Mat4 {
	local := chunkLocalMatrix(ch.Origin, ch.Rotation, ch.Scale)
	k := ch.EffectiveFrameScale()
	if k == 1 {
		return local
	}
	// Outside the chunk's own placement, so the owner's scale carries Origin
	// along with the geometry: an attachment a metre forward of a ship's centre
	// sits two metres forward once the ship is twice the size.
	return mgl32.Scale3D(k, k, k).Mul4(local)
}

// EffectiveScale is the chunk's own authored scale, with the zero value read as
// 1 — a chunk that never mentioned a scale is full size, not a point.
func (ch *Chunk) EffectiveScale() float32 {
	if ch.Scale == 0 {
		return 1
	}
	return ch.Scale
}

// EffectiveFrameScale is the same for the scale its frame owner contributes.
func (ch *Chunk) EffectiveFrameScale() float32 {
	if ch.FrameScale == 0 {
		return 1
	}
	return ch.FrameScale
}

// PlacementScale is the whole factor between this chunk's local units and the
// units of the space it is placed in — its own scale and its frame owner's
// together.
//
// Anything converting a *distance* across that boundary rather than a point —
// a click radius, a scatter range, the 2D map's ruler — has to go through this
// rather than reading Scale directly, or it is off by the owner's factor on
// every chunk an entity carries.
func (ch *Chunk) PlacementScale() float32 {
	return ch.EffectiveScale() * ch.EffectiveFrameScale()
}

// ClearFrameTransform drops the runtime placement a frame published onto this
// chunk, for when the chunk stops being carried. The two fields go together: a
// stale FrameScale left behind would keep resizing geometry no entity owns any
// more, and unlike Xform it has no "not yet refreshed" fallback to hide it.
func (ch *Chunk) ClearFrameTransform() {
	ch.Xform = mgl32.Mat4{}
	ch.FrameScale = 0
}

// ModelMatrix is the transform this chunk's local geometry is drawn/collided
// through: its own Origin/Rotation/Scale, composed under its owning frame's
// live Xform when it has one. Points, MinBound/MaxBound, sector bounds and
// every other per-chunk coordinate are chunk-local; this is the one matrix
// that carries them into world space (or into the frame's space, for a
// frame-owned chunk — RefreshFrames only ever updates Xform, never this).
// Xform is runtime-only and its zero value is the ZERO matrix, not the
// identity — so a frame-owned chunk whose frames have not been refreshed is
// treated as if it were unframed rather than composed against a singular
// matrix. Without that guard such a chunk collapses to a point
// (LocalToWorld returns the origin for every vertex) and, worse,
// WorldToLocal inverts a singular matrix and hands back NaN, which then
// spreads silently through collision and picking. RebuildIndexes leaves
// exactly this state behind whenever BuildFrames fails — a chunk bound to a
// missing owner or a cycle — and three editor call sites ignore that error.
// Falling back to the chunk's own authored placement keeps the level usable.
func (ch *Chunk) ModelMatrix() mgl32.Mat4 {
	local := ch.LocalPlacementMatrix()
	if ch.InFrame() && ch.Xform != (mgl32.Mat4{}) {
		return ch.Xform.Mul4(local)
	}
	return local
}

// LocalToWorld carries a point out of this chunk's local space, through
// ModelMatrix.
func (ch *Chunk) LocalToWorld(p mgl32.Vec3) mgl32.Vec3 {
	m := ch.ModelMatrix()
	if m == mgl32.Ident4() {
		return p
	}
	r := m.Mul4x1(p.Vec4(1))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// WorldToLocal carries a point into this chunk's local space — the inverse of
// LocalToWorld/ModelMatrix. Every collision or spatial-hash lookup that tests
// a world position against chunk-local geometry (Points, MinBound/MaxBound,
// SectorPoints) goes through this first, so it is testing in the same space
// that geometry is actually expressed in.
func (ch *Chunk) WorldToLocal(p mgl32.Vec3) mgl32.Vec3 {
	m := ch.ModelMatrix()
	if m == mgl32.Ident4() {
		return p
	}
	r := m.Inv().Mul4x1(p.Vec4(1))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// LocalToWorldDirection carries a direction — not a point — out of this
// chunk's local space. WorldToLocalDirection's inverse; see its own doc
// comment for why a direction needs its own conversion rather than
// LocalToWorld's.
func (ch *Chunk) LocalToWorldDirection(d mgl32.Vec3) mgl32.Vec3 {
	m := ch.ModelMatrix()
	if m == mgl32.Ident4() {
		return d
	}
	r := m.Mul4x1(d.Vec4(0))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// WorldToLocalDirection carries a direction — not a point — into this chunk's
// local space: the rotation and scale WorldToLocal applies, without the
// translation a direction has no business being offset by (the W=0 in the
// homogeneous multiply is what drops it). Used wherever an offset or a delta,
// rather than a position, needs to cross into a chunk's own space — an
// axis-constrained drag, say, where dragging one world unit along X should
// move a 2×-scaled chunk's own geometry by half a local unit, in whichever
// local direction its own rotation carries world X to.
func (ch *Chunk) WorldToLocalDirection(d mgl32.Vec3) mgl32.Vec3 {
	m := ch.ModelMatrix()
	if m == mgl32.Ident4() {
		return d
	}
	r := m.Inv().Mul4x1(d.Vec4(0))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// WorldBounds is the chunk's AABB in world space: MinBound/MaxBound carried
// through ModelMatrix and re-fitted.
func (ch *Chunk) WorldBounds() (min, max mgl32.Vec3) {
	return transformAABB(ch.ModelMatrix(), ch.MinBound, ch.MaxBound)
}

// transformAABB carries a local axis-aligned box through m and re-fits it.
//
// A rotated box does not stay a box, so the eight corners are transformed and
// re-bounded rather than the two extremes being transformed in place — which
// would produce a box that is not even the right size, let alone conservative.
// m == identity is the common case (an unmoved, unrotated, unscaled chunk),
// short-circuited so most callers never pay for the corner loop.
func transformAABB(m mgl32.Mat4, lo, hi mgl32.Vec3) (min, max mgl32.Vec3) {
	if m == mgl32.Ident4() {
		return lo, hi
	}
	h := hi.Sub(lo).Mul(0.5)
	c := lo.Add(h)
	for i := 0; i < 8; i++ {
		corner := mgl32.Vec3{
			h.X() * CornerSign(i, 0),
			h.Y() * CornerSign(i, 1),
			h.Z() * CornerSign(i, 2),
		}
		r := m.Mul4x1(c.Add(corner).Vec4(1))
		p := mgl32.Vec3{r.X(), r.Y(), r.Z()}
		if i == 0 {
			min, max = p, p
			continue
		}
		min, max = geom.VecMin(min, p), geom.VecMax(max, p)
	}
	return min, max
}

// ---------- who borders whom ----------

// SectorBorder is one sector's view through one of its portal edges: the sector
// on the far side, and the edge itself so a caller can test which way it faces.
type SectorBorder struct {
	To         int // index into ch.Sectors
	Start, End mgl32.Vec2
}

// SectorBorderGraph lists, for every sector, the sectors it can see through and
// the edge it sees through them.
//
// This is deliberately NOT a walk of each sector's Portals. A portal records ONE
// ToSector, and one edge can genuinely border two sectors once a chunk has
// storeys: a stairwell shaft running the full height of a building shares its
// south edge with the ground-floor room at the bottom and the first-floor room
// at the top. InferPortals has to choose one of them — that field is a single
// reference and cannot say "both" — so whichever it picks, the other is left
// out of the visibility graph and gets culled away while plainly in view. That
// is the corner of an upper floor blinking out as you turn on the spot at the
// top of the stairs.
//
// Seeing through an edge does not care which of the two the portal happened to
// name, so this answers geometrically: every sector walking the reverse edge
// whose vertical extent overlaps. Only edges that are portals are followed, so
// a pinned wall — a building's shell, shared with the open ground outside it —
// stays opaque.
//
// Built in one pass over every edge rather than by rescanning the sector list
// per edge: the visibility walk runs once a frame over the player's chunk, and
// the quadratic version was fine for a handful of sectors and would not have
// stayed fine.
func (ch *Chunk) SectorBorderGraph() [][]SectorBorder {
	type edgeKey struct{ a, b int }
	owners := map[edgeKey][]int{}
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		n := len(s.PointIndices)
		for ei := 0; ei < n; ei++ {
			a := s.PointIndices[ei]
			b := s.PointIndices[(ei+1)%n]
			owners[edgeKey{a, b}] = append(owners[edgeKey{a, b}], si)
		}
	}

	out := make([][]SectorBorder, len(ch.Sectors))
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		n := len(s.PointIndices)
		if n < 3 {
			continue
		}
		lo, hi := sectorSpan(s)
		for ei := 0; ei < n; ei++ {
			if ei >= len(s.EdgeKinds) || s.EdgeKinds[ei] != "portal" {
				continue
			}
			// A portal out of this chunk leads into a space moving relative to
			// it; the visibility walk does not cross those, so it is not
			// offered one here either.
			if _, pi := EdgeKindAndIndex(s, ei); pi < len(s.Portals) && s.Portals[pi].ToFrame != "" {
				continue
			}
			a := s.PointIndices[ei]
			b := s.PointIndices[(ei+1)%n]
			for _, ci := range owners[edgeKey{b, a}] {
				if ci == si {
					continue
				}
				clo, chi := sectorSpan(&ch.Sectors[ci])
				if minFloat32(hi, chi)-maxFloat32(lo, clo) <= 0 {
					continue // stacked, not adjacent: no way through
				}
				out[si] = append(out[si], SectorBorder{
					To: ci, Start: ch.Points[a], End: ch.Points[b],
				})
			}
		}
	}
	return out
}
