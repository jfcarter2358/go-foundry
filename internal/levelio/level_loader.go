package levelio

import (
	"fmt"
	"foundry/internal/audio"
	"foundry/internal/fileutil"
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/world"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// level_loader.go — converts the YAML-facing Raw* level format
// (level_schema.go) into runtime state (Level, Chunk, Sector, ...), and
// back. loadRawChunk does the bulk of the work per chunk; LoadLevel and
// NewBlankLevel are the top-level entry points.

// ---------- Loader ----------

// loadRawChunk converts a single RawChunk into a runtime Chunk, uploading all
// geometry to the GPU. It returns the chunk and the set of texture names needed.
func loadRawChunk(rc world.RawChunk) (world.Chunk, map[string]bool, error) {
	texNeeded := map[string]bool{}

	sc := float32(1.0)
	if rc.Scale != 0 {
		sc = rc.Scale
	}
	origin := mgl32.Vec3{rc.Origin.X, rc.Origin.Y, rc.Origin.Z}
	chunkScript, err := world.ScriptDataFromRaw(rc.RawScriptData, "chunk "+rc.ID)
	if err != nil {
		return world.Chunk{}, nil, err
	}
	chunk := world.Chunk{
		ID:         rc.ID,
		ScriptData: chunkScript,
		Origin:     origin,
		Rotation:   rc.Rotation,
		Scale:      sc,
		FrameOwner: rc.Frame,
		Disabled:   rc.Disabled,
		// A chunk authored disabled is not built at all — the geometry passes
		// below are skipped and it is marked unloaded, which is exactly the
		// state ReleaseGeometry would have left it in. That is the point of
		// writing `disabled: true` into a level: the area costs nothing until
		// a script switches it on, and world.SetChunkEnabled builds it then.
		Unloaded: rc.Disabled,
		Layers:   loadChunkLayers(rc.Layers),
	}

	// Sort so the point pool is built in a deterministic order — Go map
	// iteration is randomised, which would otherwise shuffle point indices on
	// every load and make saved files churn between runs.
	names := make([]string, 0, len(rc.Points))
	for name := range rc.Points {
		names = append(names, name)
	}
	sort.Strings(names)

	chunk.PointNames = make([]string, 0, len(names))
	pointNameToIdx := make(map[string]int, len(names))
	for _, name := range names {
		rp := rc.Points[name]
		pointNameToIdx[name] = len(chunk.Points)
		chunk.Points = append(chunk.Points, mgl32.Vec2{rp.X, rp.Y})
		chunk.PointNames = append(chunk.PointNames, name)
	}

	for i, pt := range chunk.Points {
		if i == 0 {
			chunk.MinBound = mgl32.Vec3{pt.X(), float32(math.MaxFloat32), pt.Y()}
			chunk.MaxBound = mgl32.Vec3{pt.X(), -float32(math.MaxFloat32), pt.Y()}
		} else {
			if pt.X() < chunk.MinBound.X() {
				chunk.MinBound[0] = pt.X()
			}
			if pt.Y() < chunk.MinBound.Z() {
				chunk.MinBound[2] = pt.Y()
			}
			if pt.X() > chunk.MaxBound.X() {
				chunk.MaxBound[0] = pt.X()
			}
			if pt.Y() > chunk.MaxBound.Z() {
				chunk.MaxBound[2] = pt.Y()
			}
		}
	}

	for _, rs := range rc.Sectors {

		floorUVScale := mgl32.Vec2{rs.Floor.UVScale.X, rs.Floor.UVScale.Y}
		if floorUVScale == (mgl32.Vec2{}) {
			floorUVScale = mgl32.Vec2{1, 1}
		}
		ceilUVScale := mgl32.Vec2{rs.Ceiling.UVScale.X, rs.Ceiling.UVScale.Y}
		if ceilUVScale == (mgl32.Vec2{}) {
			ceilUVScale = mgl32.Vec2{1, 1}
		}
		sectorScript, err := world.ScriptDataFromRaw(rs.RawScriptData, "sector "+rs.ID)
		if err != nil {
			return world.Chunk{}, nil, err
		}
		floorScript, err := world.ScriptDataFromRaw(rs.Floor.RawScriptData, "sector "+rs.ID+" floor")
		if err != nil {
			return world.Chunk{}, nil, err
		}
		ceilScript, err := world.ScriptDataFromRaw(rs.Ceiling.RawScriptData, "sector "+rs.ID+" ceiling")
		if err != nil {
			return world.Chunk{}, nil, err
		}

		sector := world.Sector{
			ID:         rs.ID,
			ScriptData: sectorScript,
			Layer:      rs.Layer,
			Floor: world.Floor{
				ScriptData:   floorScript,
				Enabled:      rs.Floor.Enabled,
				Hidden:       rs.Floor.Hidden,
				Height:       rs.Floor.Height,
				Texture:      rs.Floor.Texture,
				BlendTexture: rs.Floor.BlendTexture,
				BlendMask:    rs.Floor.BlendMask,
				DoubleSided:  world.BoolDefault(rs.Floor.DoubleSided, true),
				Alpha:        float32Default(rs.Floor.Alpha, 1.0),
				Openings:     loadOpenings(rs.Floor.Openings),
				Skirt:        rs.Floor.Skirt,
				SkirtTexture: rs.Floor.SkirtTexture,
				UVOffset:     mgl32.Vec2{rs.Floor.UVOffset.X, rs.Floor.UVOffset.Y},
				UVScale:      floorUVScale,
				UVRotation:   rs.Floor.UVRotation,
				Detail:       world.DetailFromRaw(rs.Floor.Detail),
				Smooth:       rs.Floor.Smooth,
				SmoothAngle:  rs.Floor.SmoothAngle,
			},
			Ceiling: world.Ceiling{
				ScriptData:   ceilScript,
				Enabled:      rs.Ceiling.Enabled,
				Height:       rs.Ceiling.Height,
				Texture:      rs.Ceiling.Texture,
				BlendTexture: rs.Ceiling.BlendTexture,
				BlendMask:    rs.Ceiling.BlendMask,
				DoubleSided:  world.BoolDefault(rs.Ceiling.DoubleSided, true),
				Alpha:        float32Default(rs.Ceiling.Alpha, 1.0),
				Openings:     loadOpenings(rs.Ceiling.Openings),
				UVOffset:     mgl32.Vec2{rs.Ceiling.UVOffset.X, rs.Ceiling.UVOffset.Y},
				UVScale:      ceilUVScale,
				UVRotation:   rs.Ceiling.UVRotation,
				Detail:       world.DetailFromRaw(rs.Ceiling.Detail),
				Smooth:       rs.Ceiling.Smooth,
				SmoothAngle:  rs.Ceiling.SmoothAngle,
			},
		}

		if rs.Floor.Texture != "" {
			texNeeded[rs.Floor.Texture] = true
		}
		if rs.Floor.BlendTexture != "" {
			texNeeded[rs.Floor.BlendTexture] = true
		}
		if rs.Floor.BlendMask != "" {
			texNeeded[rs.Floor.BlendMask] = true
		}
		if rs.Ceiling.Texture != "" {
			texNeeded[rs.Ceiling.Texture] = true
		}
		if rs.Ceiling.BlendTexture != "" {
			texNeeded[rs.Ceiling.BlendTexture] = true
		}
		if rs.Ceiling.BlendMask != "" {
			texNeeded[rs.Ceiling.BlendMask] = true
		}

		// A sector's edges must form a closed ring visiting each point exactly
		// once. Silently de-duplicating a repeated name (as this used to do)
		// produces a sector with fewer vertices than edges, which desynchronises
		// Walls/Portals from EdgeKinds and yields degenerate, overlapping
		// geometry. Reject the file instead so the damage is visible.
		pointSeen := make(map[string]bool, len(rs.Edges))
		for ei, e := range rs.Edges {
			if pointSeen[e.From] {
				return world.Chunk{}, nil, fmt.Errorf(
					"sector %q: point %q appears more than once in the edge ring", rs.ID, e.From)
			}
			pointSeen[e.From] = true

			next := rs.Edges[(ei+1)%len(rs.Edges)]
			if e.To != next.From {
				return world.Chunk{}, nil, fmt.Errorf(
					"sector %q: edge %d ends at %q but edge %d starts at %q — edges must form a closed ring",
					rs.ID, ei, e.To, (ei+1)%len(rs.Edges), next.From)
			}

			idx, ok := pointNameToIdx[e.From]
			if !ok {
				return world.Chunk{}, nil, fmt.Errorf(
					"sector %q: edge references unknown point %q", rs.ID, e.From)
			}
			sector.PointIndices = append(sector.PointIndices, idx)
		}

		for _, edge := range rs.Edges {
			fromIdx, okFrom := pointNameToIdx[edge.From]
			toIdx, okTo := pointNameToIdx[edge.To]
			if !okFrom || !okTo {
				return world.Chunk{}, nil, fmt.Errorf("sector %q: edge references unknown point: %s->%s",
					rs.ID, edge.From, edge.To)
			}
			start := chunk.Points[fromIdx]
			end := chunk.Points[toIdx]

			// The edge's stable ID. Computed here, before the winding-reversal
			// fixup below, because reversal turns edge A→B into B→A — deriving
			// the migration name afterwards would rename every edge of a
			// clockwise-authored sector on its next save.
			edgeID := edge.ID
			if edgeID == "" {
				edgeID = world.MigrateEdgeID(edge.From, edge.To)
			}
			edgeScript, err := world.ScriptDataFromRaw(edge.RawScriptData, "sector "+rs.ID+" edge "+edgeID)
			if err != nil {
				return world.Chunk{}, nil, err
			}

			switch edge.Kind {
			case "wall":
				if edge.Texture != "" {
					texNeeded[edge.Texture] = true
				}
				if edge.BlendTexture != "" {
					texNeeded[edge.BlendTexture] = true
				}
				if edge.BlendMask != "" {
					texNeeded[edge.BlendMask] = true
				}
				wallUVScale := mgl32.Vec2{edge.UVScale.X, edge.UVScale.Y}
				if wallUVScale == (mgl32.Vec2{}) {
					wallUVScale = mgl32.Vec2{1, 1}
				}
				sector.Walls = append(sector.Walls, world.Wall{
					ID:           edgeID,
					ScriptData:   edgeScript,
					StartPoint:   start,
					EndPoint:     end,
					Texture:      edge.Texture,
					BlendTexture: edge.BlendTexture,
					BlendMask:    edge.BlendMask,
					Collision:    edge.Collision,
					Visible:      !edge.Hidden,
					Pinned:       edge.Pinned,
					DoubleSided:  world.BoolDefault(edge.DoubleSided, true),
					Alpha:        float32Default(edge.Alpha, 1.0),
					UVOffset:     mgl32.Vec2{edge.UVOffset.X, edge.UVOffset.Y},
					UVScale:      wallUVScale,
					UVRotation:   edge.UVRotation,
					Detail:       world.DetailFromRaw(edge.Detail),
					Smooth:       edge.Smooth,
					SmoothAngle:  edge.SmoothAngle,
				})
				sector.EdgeKinds = append(sector.EdgeKinds, "wall")
				sector.EdgeIDs = append(sector.EdgeIDs, edgeID)
			case "portal":
				sector.Portals = append(sector.Portals, world.Portal{
					ID:          edgeID,
					ScriptData:  edgeScript,
					StartPoint:  start,
					EndPoint:    end,
					ToSector:    edge.ToSector,
					ToFrame:     edge.ToFrame,
					DrawGap:     edge.DrawGap,
					GapTexture:  edge.GapTexture,
					Visible:     !edge.Hidden,
					DoubleSided: world.BoolDefault(edge.DoubleSided, true),
					Alpha:       float32Default(edge.Alpha, 1.0),
				})
				sector.EdgeKinds = append(sector.EdgeKinds, "portal")
				sector.EdgeIDs = append(sector.EdgeIDs, edgeID)
			}
		}

		// A file can carry duplicate IDs (hand-edited, or two edges migrating to
		// the same point pair in a malformed ring). Resolve them here so the
		// invariant holds from the moment the sector exists.
		world.DedupeSectorEdgeIDs(&sector)

		sectorPts := world.SectorPoints(&chunk, &sector)

		reversed := world.SignedArea2D(sectorPts) < 0
		if reversed {
			// Reversing vertex order shifts which polygon edge is which: new
			// edge i is the reverse of old edge (n-2-i) mod n, not old edge
			// (n-1-i) — a plain array-reverse of EdgeKinds/Walls/Portals
			// misaligns edge kinds against the reversed points. Rebuild the
			// per-edge kind/wall/portal records in the correct new order.
			n := len(sector.PointIndices)
			type edgeRec struct {
				kind   string
				id     string
				wall   world.Wall
				portal world.Portal
			}
			recs := make([]edgeRec, n)
			wi, pi := 0, 0
			for i := 0; i < n; i++ {
				// IDs were computed from the unordered point pair above, so they
				// ride the permutation unchanged — an edge keeps its name even
				// though its direction flips.
				id := ""
				if i < len(sector.EdgeIDs) {
					id = sector.EdgeIDs[i]
				}
				if i < len(sector.EdgeKinds) && sector.EdgeKinds[i] == "portal" {
					recs[i] = edgeRec{kind: "portal", id: id, portal: sector.Portals[pi]}
					pi++
				} else {
					recs[i] = edgeRec{kind: "wall", id: id, wall: sector.Walls[wi]}
					wi++
				}
			}

			newPointIndices := make([]int, n)
			for i := 0; i < n; i++ {
				newPointIndices[i] = sector.PointIndices[n-1-i]
			}
			sector.PointIndices = newPointIndices

			newEdgeKinds := make([]string, n)
			newEdgeIDs := make([]string, n)
			newWalls := make([]world.Wall, 0, len(sector.Walls))
			newPortals := make([]world.Portal, 0, len(sector.Portals))
			for i := 0; i < n; i++ {
				rec := recs[((n-2-i)%n+n)%n]
				newEdgeKinds[i] = rec.kind
				newEdgeIDs[i] = rec.id
				if rec.kind == "portal" {
					rec.portal.StartPoint, rec.portal.EndPoint = rec.portal.EndPoint, rec.portal.StartPoint
					newPortals = append(newPortals, rec.portal)
				} else {
					rec.wall.StartPoint, rec.wall.EndPoint = rec.wall.EndPoint, rec.wall.StartPoint
					newWalls = append(newWalls, rec.wall)
				}
			}
			sector.EdgeKinds = newEdgeKinds
			sector.EdgeIDs = newEdgeIDs
			sector.Walls = newWalls
			sector.Portals = newPortals
			sectorPts = world.SectorPoints(&chunk, &sector)
		}
		world.SyncEdgeRecordIDs(&sector)

		nPts := len(sector.PointIndices)
		sector.Floor.VertexOffsets = make([]float32, nPts)
		for i := 0; i < nPts && i < len(rs.Floor.VertexOffsets); i++ {
			sector.Floor.VertexOffsets[i] = rs.Floor.VertexOffsets[i]
		}
		sector.Ceiling.VertexOffsets = make([]float32, nPts)
		for i := 0; i < nPts && i < len(rs.Ceiling.VertexOffsets); i++ {
			sector.Ceiling.VertexOffsets[i] = rs.Ceiling.VertexOffsets[i]
		}
		if reversed {
			for i, j := 0, len(sector.Floor.VertexOffsets)-1; i < j; i, j = i+1, j-1 {
				sector.Floor.VertexOffsets[i], sector.Floor.VertexOffsets[j] = sector.Floor.VertexOffsets[j], sector.Floor.VertexOffsets[i]
			}
			for i, j := 0, len(sector.Ceiling.VertexOffsets)-1; i < j; i, j = i+1, j-1 {
				sector.Ceiling.VertexOffsets[i], sector.Ceiling.VertexOffsets[j] = sector.Ceiling.VertexOffsets[j], sector.Ceiling.VertexOffsets[i]
			}
		}

		minFloor := sector.Floor.Height
		for _, off := range rs.Floor.VertexOffsets {
			if h := sector.Floor.Height + off; h < minFloor {
				minFloor = h
			}
		}
		if d, ok := rawSurfaceMinDepth(rs.Floor.Mesh, rs.Floor.TerrainPoints, rs.Floor.Extrusions, true); ok {
			if h := sector.Floor.Height + d; h < minFloor {
				minFloor = h
			}
		}
		maxCeil := sector.Ceiling.Height
		for _, off := range rs.Ceiling.VertexOffsets {
			if h := sector.Ceiling.Height + off; h > maxCeil {
				maxCeil = h
			}
		}
		if d, ok := rawSurfaceMaxDepth(rs.Ceiling.Mesh, rs.Ceiling.TerrainPoints, rs.Ceiling.Extrusions, false); ok {
			if h := sector.Ceiling.Height + d; h > maxCeil {
				maxCeil = h
			}
		}
		if minFloor < chunk.MinBound.Y() {
			chunk.MinBound[1] = minFloor
		}
		if maxCeil > chunk.MaxBound.Y() {
			chunk.MaxBound[1] = maxCeil
		}

		chunk.Sectors = append(chunk.Sectors, sector)
	}

	chunk.InferPortals()

	// Surface meshes are built here rather than in the struct literals above
	// because a mesh needs a SurfaceFrame, and a frame needs the sector's
	// points, heights and walls to be final — which they are not until
	// InferPortals has decided which edges are walls at all.
	for si := range chunk.Sectors {
		s := &chunk.Sectors[si]
		if si < len(rc.Sectors) {
			loadSectorSurfaceMeshes(&chunk, s, rawSectorByID(rc.Sectors, s.ID), rc.Points)
		}
		if chunk.Disabled {
			// Surface meshes above are authored data and are read either way;
			// only the upload is skipped. ReloadGeometry runs exactly this for
			// every sector when the chunk is switched on.
			continue
		}
		sectorPts := world.SectorPoints(&chunk, s)
		if err := s.Load(sectorPts); err != nil {
			return world.Chunk{}, nil, fmt.Errorf("sector %q load: %w", s.ID, err)
		}
	}

	// Collect gap textures so they get registered/uploaded; the actual gap
	// geometry is built by RebuildGaps (chunk.go), which every other caller
	// that (re)builds portal gaps also uses — see chunk.go's own doc comment.
	for si := range chunk.Sectors {
		s := &chunk.Sectors[si]
		for pi := range s.Portals {
			p := &s.Portals[pi]
			if !p.DrawGap {
				continue
			}
			tex := p.GapTexture
			if tex == "" {
				tex = s.Floor.Texture
			}
			if tex != "" {
				texNeeded[tex] = true
			}
		}
	}
	if !chunk.Disabled {
		chunk.RebuildGaps()

		// Bounds so far come only from the chunk's points; roll up the sectors'
		// own bounds so geometry reaching past the point pool isn't
		// frustum-culled with it. Water surfaces are folded in later, by
		// SyncBiomeWaterSurfaces.
		chunk.RecomputeBounds()
	}

	// Biome sector polygons — points are chunk-local, same as structural points.
	for _, rb := range rc.BiomeSectors {
		bs := world.BiomeSector{
			ID:          rb.ID,
			DrapeSector: rb.DrapeSector,
			Layer:       rb.Layer,
			Points:      make([]mgl32.Vec2, len(rb.Points)),
			HeightDirty: true,
			GroundDirty: true,
			Brush: world.BiomeBrush{
				Kind:         rb.Brush.Kind,
				Density:      rb.Brush.Density,
				Range:        rb.Brush.Range,
				Color:        mgl32.Vec3{rb.Brush.Color.R, rb.Brush.Color.G, rb.Brush.Color.B},
				GrassHeight:  rb.Brush.GrassHeight,
				GrassWidth:   rb.Brush.GrassWidth,
				HeadScale:    rb.Brush.HeadScale,
				GrassClump:   rb.Brush.GrassClump,
				LOD1Distance: rb.Brush.LOD1Distance,
				LOD2Distance: rb.Brush.LOD2Distance,
				RockSides:    rb.Brush.RockSides,
				RockScale:    rb.Brush.RockScale,
				SandHeight:   rb.Brush.SandHeight,
				WaterDepth:   rb.Brush.WaterDepth,
				Water:        world.WaterParamsFromRaw(rb.Brush.Water),

				MossScatter:   rb.Brush.MossScatter,
				MinSlopeAngle: rb.Brush.MinSlopeAngle,
				MaxSlopeAngle: rb.Brush.MaxSlopeAngle,

				BloomSpike:      rb.Brush.BloomSpike,
				BloomCenter:     rb.Brush.BloomCenter,
				BloomSolidColor: rb.Brush.BloomSolidColor,
				CenterColor: mgl32.Vec3{
					rb.Brush.CenterColor.R, rb.Brush.CenterColor.G, rb.Brush.CenterColor.B,
				},
				StemLeaves: rb.Brush.StemLeaves,
				LeafScale:  rb.Brush.LeafScale,

				GroundTexture:   rb.Brush.GroundTexture,
				GroundAmplitude: rb.Brush.GroundAmplitude,
				GroundFrequency: rb.Brush.GroundFrequency,
				GroundOctaves:   rb.Brush.GroundOctaves,
				GroundUVScale:   mgl32.Vec2{rb.Brush.GroundUVScale.X, rb.Brush.GroundUVScale.Y},

				RangeFadeFraction: rb.Brush.RangeFadeFraction,
				EdgeFadeDistance:  rb.Brush.EdgeFadeDistance,
				BoundaryJitter:    rb.Brush.BoundaryJitter,
			},
		}
		for i, rp := range rb.Points {
			bs.Points[i] = mgl32.Vec2{rp.X, rp.Y}
		}
		chunk.BiomeSectors = append(chunk.BiomeSectors, bs)
	}

	return chunk, texNeeded, nil
}

// LoadLevel reads a YAML level file, converts it to runtime types, and uploads
// all geometry to the GPU. Texture loading is driven from this function via the
// global TextureNames map and ExecutablePath variable.
func LoadLevel(levelPath string) (world.Level, error) {
	raw, err := readRawLevelFile(levelPath)
	if err != nil {
		return world.Level{}, err
	}
	return rawToLevel(raw)
}

// readRawLevelFile parses a level file into its Raw form without touching the
// GPU. Split from LoadLevel so anything that only wants to inspect a level's
// authored shape — validation, migration checks, tooling — can do so headlessly.
func readRawLevelFile(levelPath string) (world.RawLevel, error) {
	data, err := os.ReadFile(levelPath)
	if err != nil {
		return world.RawLevel{}, err
	}
	var raw world.RawLevel
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return world.RawLevel{}, err
	}
	return raw, nil
}

// blankRawLevel returns a minimal valid RawLevel: a single four-walled room
// lit by soft ambient light. Used to write a starter file from the editor's
// "New Level" dialog.
func blankRawLevel() world.RawLevel {
	return world.RawLevel{
		Chunks: []world.RawChunk{{
			ID: "default",
			Points: map[string]geom.Point{
				"A": {X: -4, Y: -4},
				"B": {X: 4, Y: -4},
				"C": {X: 4, Y: 4},
				"D": {X: -4, Y: 4},
			},
			Sectors: []world.RawSector{{
				ID: "room",
				Floor: world.RawFloor{
					Enabled: true,
					Height:  0,
					Texture: "default",
				},
				Ceiling: world.RawCeiling{
					Enabled: true,
					Height:  4,
					Texture: "default",
				},
				Edges: []world.RawEdge{
					{From: "A", To: "B", Kind: "wall", Texture: "default", Collision: true},
					{From: "B", To: "C", Kind: "wall", Texture: "default", Collision: true},
					{From: "C", To: "D", Kind: "wall", Texture: "default", Collision: true},
					{From: "D", To: "A", Kind: "wall", Texture: "default", Collision: true},
				},
			}},
		}},
		Lights: world.RawLights{
			Global: world.RawGlobalLight{Color: world.RawColor{R: 1, G: 1, B: 1}, Brightness: 0.3},
		},
	}
}

// NewBlankLevel builds a completely empty in-memory level (no chunks, and not
// backed by any file on disk) suitable for opening the
// editor to a truly blank scene. It still carries the same default ambient
// light as blankRawLevel — a level with zero lights renders everything pure
// black (see fragment.glsl: lighting starts at globalLightColor *
// globalLightBrightness and only grows from there), so geometry added via
// "New Chunk" would otherwise be invisible until a light is added by hand.
// The caller is responsible for saving it if they want to keep it.
func NewBlankLevel() (world.Level, error) {
	return rawToLevel(world.RawLevel{
		Lights: world.RawLights{
			Global: world.RawGlobalLight{Color: world.RawColor{R: 1, G: 1, B: 1}, Brightness: 0.3},
		},
	})
}

// rawToLightSet converts parsed YAML lights into runtime lights. Split out of
// rawToLevel because it touches no GPU state.
func rawToLightSet(rl world.RawLights) world.LightSet {
	var ls world.LightSet
	ls.Global = world.GlobalLight{
		Color:      mgl32.Vec3{rl.Global.Color.R, rl.Global.Color.G, rl.Global.Color.B},
		Brightness: rl.Global.Brightness,
		Disabled:   rl.Global.Disabled,
	}
	for _, rd := range rl.Directional {
		ss := rd.ShadowStrength
		if ss == 0 && rd.CastShadows {
			ss = 0.5 // default to half-strength shadows
		}
		dlScript, _ := world.ScriptDataFromRaw(rd.RawScriptData, "directional light "+rd.ID)
		dl := world.DirectionalLight{
			ID:             rd.ID,
			ScriptData:     dlScript,
			Color:          mgl32.Vec3{rd.Color.R, rd.Color.G, rd.Color.B},
			Brightness:     rd.Brightness,
			CastShadows:    rd.CastShadows,
			ShadowStrength: ss,
			Disabled:       rd.Disabled,
		}
		// Angles are authoritative when present; otherwise derive them from the
		// direction vector, which is all pre-rotation levels stored.
		if rd.Yaw != nil && rd.Pitch != nil {
			dl.Yaw, dl.Pitch = *rd.Yaw, *rd.Pitch
		} else {
			dl.Yaw, dl.Pitch = world.DirLightYawPitch(mgl32.Vec3{rd.Direction.X, rd.Direction.Y, rd.Direction.Z})
		}
		dl.SyncDirection()
		ls.Directional = append(ls.Directional, dl)
	}
	for _, rp := range rl.Point {
		ss := rp.ShadowStrength
		if ss == 0 && rp.CastShadows {
			ss = 0.5
		}
		plScript, _ := world.ScriptDataFromRaw(rp.RawScriptData, "point light "+rp.ID)
		ls.Points = append(ls.Points, world.PointLight{
			ID:             rp.ID,
			ScriptData:     plScript,
			Position:       mgl32.Vec3{rp.Position.X, rp.Position.Y, rp.Position.Z},
			Color:          mgl32.Vec3{rp.Color.R, rp.Color.G, rp.Color.B},
			Brightness:     rp.Brightness,
			Radius:         rp.Radius,
			CastShadows:    rp.CastShadows,
			ShadowStrength: ss,
			Disabled:       rp.Disabled,
		})
	}
	// Lights predate having identity, so most levels arrive with none. Mint them
	// here rather than at every use site, so a light is addressable the moment it
	// exists.
	world.AssignLightIDs(&ls)
	return ls
}

// rawToLevel converts a parsed RawLevel into runtime types and uploads all
// referenced geometry/textures to the GPU.
// rawToLevel builds a runtime level from its authored form.
//
// The three expensive parts — the chunk loop, the level-scoped objects and the
// texture loads — are separate functions rather than inline, because a level
// load has two drivers: this one, which runs the whole thing in a single call,
// and levelio_load_job.go, which runs it a slice at a time so a loading screen
// can animate over it. Both call exactly these functions in exactly this order,
// which is what makes the stepped load provably the same load.
func rawToLevel(raw world.RawLevel) (world.Level, error) {
	var level world.Level
	texNeeded := map[string]bool{}

	if _, err := loadChunkRange(raw, &level, texNeeded, 0, len(raw.Chunks)); err != nil {
		return world.Level{}, err
	}
	if err := loadLevelObjects(raw, &level, texNeeded); err != nil {
		return world.Level{}, err
	}
	names := textureLoadOrder(texNeeded)
	if _, err := loadTextureRange(names, 0, len(names)); err != nil {
		return world.Level{}, err
	}
	if err := finishLevel(&level); err != nil {
		return world.Level{}, err
	}
	return level, nil
}

// loadChunkRange builds chunks [from, from+count) onto level, and reports where
// the next call should resume.
func loadChunkRange(raw world.RawLevel, level *world.Level, texNeeded map[string]bool, from, count int) (int, error) {
	to := from + count
	if to > len(raw.Chunks) {
		to = len(raw.Chunks)
	}
	for i := from; i < to; i++ {
		rc := raw.Chunks[i]
		chunk, chunkTex, err := loadRawChunk(rc)
		if err != nil {
			return i, err
		}
		for t := range chunkTex {
			texNeeded[t] = true
		}
		level.Chunks = append(level.Chunks, chunk)
	}
	return to, nil
}

// loadLevelObjects builds everything that sits at the level rather than in a
// chunk: the spawn, entities, volumes, sprites, emitters, sounds, music and
// lights. One step rather than several — together they are a fraction of a
// chunk's cost, and splitting them would buy nothing but seams.
func loadLevelObjects(raw world.RawLevel, level *world.Level, texNeeded map[string]bool) error {
	// Chunk and sector IDs are how a script addresses geometry, so they have to
	// name exactly one thing. Nothing enforced that before scripting existed, and
	// the editor could mint duplicates, so resolve them here — renaming with a
	// warning rather than rejecting, since a level authored under the old rules
	// should still open.
	level.Scripts = append([]string(nil), raw.Scripts...)
	level.UI = append([]string(nil), raw.UI...)

	world.ResolveLevelIDCollisions(level.Chunks)
	world.ResolveBiomeSectorIDCollisions(level)

	// The player spawn: one optional marker per level, not a placed object.
	if raw.Spawn != nil {
		spawn := &world.SpawnPoint{
			Position: mgl32.Vec3{raw.Spawn.Position.X, raw.Spawn.Position.Y, raw.Spawn.Position.Z},
			Rotation: mgl32.Vec3{raw.Spawn.Rotation.X, raw.Spawn.Rotation.Y, raw.Spawn.Rotation.Z},
			Frame:    raw.Spawn.Frame,
			Seat:     raw.Spawn.Seat,
		}
		if err := spawn.Load(); err != nil {
			return fmt.Errorf("spawn point load: %w", err)
		}
		level.Spawn = spawn
	}

	// Entities: blank ones authored in the level, and instances of the
	// definitions under resources/entities/.
	//
	// A bad entity fails the load rather than being skipped. An entity that
	// quietly vanished would take its attributes and its ID with it, and the
	// first sign of trouble would be a script failing to find something the
	// level plainly says is there.
	for _, re := range raw.Entities {
		e, err := world.EntityFromRaw(re, world.LoadEntityDef)
		if err != nil {
			return err
		}
		// IDs address entities from scripts, so a level with two of the same
		// gets the duplicate renamed rather than made unreachable.
		e.ID = world.UniqueEntityID(level, e.ID)
		if err := e.Load(); err != nil {
			return fmt.Errorf("entity %q load: %w", e.ID, err)
		}
		level.Entities = append(level.Entities, e)
	}

	// Volumes: scriptable regions, with the same ID guarantee entities get.
	for _, rv := range raw.Volumes {
		v, err := world.VolumeFromRaw(rv)
		if err != nil {
			return err
		}
		v.ID = world.UniqueVolumeID(level, v.ID)
		if err := v.Load(); err != nil {
			// A cage that would not build is a missing authoring aid, not a
			// broken level — the volume still works.
			log.Printf("volume %q: marker load: %v", v.ID, err)
		}
		level.Volumes = append(level.Volumes, v)
	}

	// Sprites: camera-facing quads, with the same ID guarantee.
	//
	// A sprite whose texture is missing falls back to the marker rather than
	// failing the load — the same call the editor's texture picker makes when
	// it shows a name as "(missing)". A quad in the wrong texture is a visible
	// problem an author can fix; a level that will not open is not.
	for _, rs := range raw.Sprites {
		sp, err := world.SpriteFromRaw(rs)
		if err != nil {
			return err
		}
		sp.ID = world.UniqueSpriteID(level, sp.ID)
		if err := sp.EnsureSpriteTexture(); err != nil {
			log.Printf("sprite %q: texture %q: %v", sp.ID, sp.Texture, err)
		}
		level.Sprites = append(level.Sprites, sp)
	}

	// Emitters: placed particle effects, with the same ID guarantee.
	//
	// A missing or malformed .fpar is reported and the emitter kept, for the
	// reason a volume with an unbuildable cage is kept: the placement is real
	// level content and the author needs to see it to fix it. What they get is
	// a marker in the editor and an emitter that emits nothing.
	for _, rem := range raw.Emitters {
		em, err := world.EmitterFromRaw(rem)
		if err != nil {
			return err
		}
		em.ID = world.UniqueEmitterID(level, em.ID)
		if err := em.Load(); err != nil {
			log.Printf("emitter %q: effect %q: %v", em.ID, em.Effect, err)
		} else {
			for i := range em.Doc.Layers {
				texNeeded[em.Doc.Layers[i].Texture] = true
			}
		}
		level.Emitters = append(level.Emitters, em)
	}

	// Sounds: placed sound sources, with the same ID guarantee.
	//
	// A missing or unreadable clip is reported and the sound kept, on exactly
	// the emitter rule above: the placement is real level content and the
	// author needs to see it in order to fix it. What they get is a marker in
	// the editor and a sound that makes none.
	//
	// Preloading here rather than on first play matters because decoding is
	// disk I/O measured in tens of milliseconds, and doing it inside the frame
	// that first triggers the sound is a visible hitch.
	for _, rs := range raw.Sounds {
		sd, err := world.SoundFromRaw(rs)
		if err != nil {
			return err
		}
		sd.ID = world.UniqueSoundID(level, sd.ID)
		if sd.Clip != "" {
			if _, err := audio.ClipFor(sd.Clip); err != nil {
				log.Printf("sound %q: clip %q: %v", sd.ID, sd.Clip, err)
			}
		}
		level.Sounds = append(level.Sounds, sd)
	}

	level.Music = world.MusicFromRaw(raw.Music)
	if level.Music != nil && level.Music.Track != "" {
		if _, err := audio.ClipFor(level.Music.Track); err != nil {
			log.Printf("music %q: %v", level.Music.Track, err)
		}
	}

	// Convert lights
	level.Lights = rawToLightSet(raw.Lights)
	return nil
}

// textureLoadOrder is the textures a level needs and does not already have, in
// a stable order so a stepped load makes the same progress twice running.
func textureLoadOrder(texNeeded map[string]bool) []string {
	out := make([]string, 0, len(texNeeded))
	for name := range texNeeded {
		if _, already := gfx.Textures[name]; already {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// loadTextureRange uploads names [from, from+count), and reports where to
// resume.
func loadTextureRange(names []string, from, count int) (int, error) {
	to := from + count
	if to > len(names) {
		to = len(names)
	}
	for i := from; i < to; i++ {
		name := names[i]
		if _, already := gfx.Textures[name]; already {
			continue
		}
		path, ok := gfx.TextureNames[name]
		if !ok {
			return i, fmt.Errorf("unknown texture %q — ensure a matching .png exists in textures/", name)
		}
		if err := gfx.LoadTexture(name, filepath.Join(fileutil.ExecutablePath, path)); err != nil {
			return i, err
		}
	}
	return to, nil
}

// finishLevel builds the spatial hash and the frame graph, which need every
// object in place.
func finishLevel(level *world.Level) error {
	if err := world.RebuildIndexes(level); err != nil {
		// A malformed frame graph is an authoring mistake with no sensible
		// fallback: the geometry would be drawn somewhere the collision cannot
		// find it. Refuse the level and say why.
		return err
	}
	return nil
}

// loadOpenings converts a surface's holes. Points come across verbatim: an
// opening is authored chunk-local, exactly like the sector outline it is cut
// into, and travels into world space through the chunk's own ModelMatrix along
// with everything else.
func loadOpenings(raw []world.RawOpening) []world.Opening {
	if len(raw) == 0 {
		return nil
	}
	out := make([]world.Opening, len(raw))
	for i, ro := range raw {
		op := world.Opening{ID: ro.ID, Link: ro.Link, Points: make([]mgl32.Vec2, len(ro.Points))}
		for j, rp := range ro.Points {
			op.Points[j] = mgl32.Vec2{rp.X, rp.Y}
		}
		out[i] = op
	}
	return out
}

// loadChunkLayers converts a chunk's authoring-band metadata, or returns nil
// for a chunk that has none — the ordinary case, and every chunk written before
// layers existed. Chunk.LayerAt synthesizes bands for whatever is missing, so
// there is nothing to fill in here.
func loadChunkLayers(raw []world.RawChunkLayer) []world.ChunkLayer {
	if len(raw) == 0 {
		return nil
	}
	out := make([]world.ChunkLayer, len(raw))
	for i, rl := range raw {
		out[i] = world.ChunkLayer{Name: rl.Name, BaseY: rl.BaseY, CeilY: rl.CeilY}
	}
	return out
}

// float32Default returns the value of a *float32, defaulting to def when nil.
func float32Default(p *float32, def float32) float32 {
	if p != nil {
		return *p
	}
	return def
}
