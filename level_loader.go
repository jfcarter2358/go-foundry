package main

import (
	"fmt"
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
func loadRawChunk(rc RawChunk) (Chunk, map[string]bool, error) {
	texNeeded := map[string]bool{}

	sc := float32(1.0)
	if rc.Scale != 0 {
		sc = rc.Scale
	}
	origin := mgl32.Vec3{rc.Origin.X, rc.Origin.Y, rc.Origin.Z}
	chunkScript, err := scriptDataFromRaw(rc.RawScriptData, "chunk "+rc.ID)
	if err != nil {
		return Chunk{}, nil, err
	}
	chunk := Chunk{
		ID:         rc.ID,
		ScriptData: chunkScript,
		Origin:     origin,
		Rotation:   rc.Rotation,
		Scale:      sc,
	}

	cos32 := float32(math.Cos(float64(rc.Rotation)))
	sin32 := float32(math.Sin(float64(rc.Rotation)))

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
		x := rp.X * sc
		y := rp.Y * sc
		tx := cos32*x - sin32*y + origin.X()
		tz := sin32*x + cos32*y + origin.Z()
		pointNameToIdx[name] = len(chunk.Points)
		chunk.Points = append(chunk.Points, mgl32.Vec2{tx, tz})
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
		loadExtrusions := func(raws []RawExtrusion, transformXZ bool) []Extrusion {
			var out []Extrusion
			for _, re := range raws {
				extUVScale := mgl32.Vec2{re.UVScale.X, re.UVScale.Y}
				if extUVScale == (mgl32.Vec2{}) {
					extUVScale = mgl32.Vec2{1, 1}
				}
				ext := Extrusion{
					Depth:       re.Depth,
					Texture:     re.Texture,
					Visible:     boolDefault(re.Visible, true),
					BackVisible: boolDefault(re.BackVisible, true),
					UVOffset:    mgl32.Vec2{re.UVOffset.X, re.UVOffset.Y},
					UVScale:     extUVScale,
					UVRotation:  re.UVRotation,
					Alpha:       float32Default(re.Alpha, 1.0),
				}
				if re.Texture != "" {
					texNeeded[re.Texture] = true
				}
				for _, rp := range re.Outer {
					if transformXZ {
						tp := transformPoint2D(mgl32.Vec2{rp.U, rp.V}, origin, rc.Rotation, sc)
						ext.Outer = append(ext.Outer, ExtrusionPoint{U: tp.X(), V: tp.Y()})
					} else {
						ext.Outer = append(ext.Outer, ExtrusionPoint{U: rp.U, V: rp.V})
					}
				}
				for _, rp := range re.Inner {
					if transformXZ {
						tp := transformPoint2D(mgl32.Vec2{rp.U, rp.V}, origin, rc.Rotation, sc)
						ext.Inner = append(ext.Inner, ExtrusionPoint{U: tp.X(), V: tp.Y()})
					} else {
						ext.Inner = append(ext.Inner, ExtrusionPoint{U: rp.U, V: rp.V})
					}
				}
				out = append(out, ext)
			}
			return out
		}
		loadTerrainPts := func(raws []RawTerrainPoint, transformXZ bool) []TerrainPoint {
			var out []TerrainPoint
			for _, rtp := range raws {
				if transformXZ {
					tp := transformPoint2D(mgl32.Vec2{rtp.U, rtp.V}, origin, rc.Rotation, sc)
					out = append(out, TerrainPoint{U: tp.X(), V: tp.Y(), Depth: rtp.Depth})
				} else {
					out = append(out, TerrainPoint{U: rtp.U, V: rtp.V, Depth: rtp.Depth})
				}
			}
			return out
		}

		floorUVScale := mgl32.Vec2{rs.Floor.UVScale.X, rs.Floor.UVScale.Y}
		if floorUVScale == (mgl32.Vec2{}) {
			floorUVScale = mgl32.Vec2{1, 1}
		}
		ceilUVScale := mgl32.Vec2{rs.Ceiling.UVScale.X, rs.Ceiling.UVScale.Y}
		if ceilUVScale == (mgl32.Vec2{}) {
			ceilUVScale = mgl32.Vec2{1, 1}
		}
		sectorScript, err := scriptDataFromRaw(rs.RawScriptData, "sector "+rs.ID)
		if err != nil {
			return Chunk{}, nil, err
		}
		floorScript, err := scriptDataFromRaw(rs.Floor.RawScriptData, "sector "+rs.ID+" floor")
		if err != nil {
			return Chunk{}, nil, err
		}
		ceilScript, err := scriptDataFromRaw(rs.Ceiling.RawScriptData, "sector "+rs.ID+" ceiling")
		if err != nil {
			return Chunk{}, nil, err
		}

		sector := Sector{
			ID:         rs.ID,
			ScriptData: sectorScript,
			Floor: Floor{
				ScriptData:    floorScript,
				Enabled:       rs.Floor.Enabled,
				Hidden:        rs.Floor.Hidden,
				Height:        rs.Floor.Height,
				Texture:       rs.Floor.Texture,
				Terrain:       rs.Floor.Terrain,
				DoubleSided:   boolDefault(rs.Floor.DoubleSided, true),
				Alpha:         float32Default(rs.Floor.Alpha, 1.0),
				Extrusions:    loadExtrusions(rs.Floor.Extrusions, true),
				TerrainPoints: loadTerrainPts(rs.Floor.TerrainPoints, true),
				UVOffset:      mgl32.Vec2{rs.Floor.UVOffset.X, rs.Floor.UVOffset.Y},
				UVScale:       floorUVScale,
				UVRotation:    rs.Floor.UVRotation,
				Detail:        detailFromRaw(rs.Floor.Detail),
				Smooth:        rs.Floor.Smooth,
				SmoothAngle:   rs.Floor.SmoothAngle,
			},
			Ceiling: Ceiling{
				ScriptData:    ceilScript,
				Enabled:       rs.Ceiling.Enabled,
				Height:        rs.Ceiling.Height,
				Texture:       rs.Ceiling.Texture,
				Terrain:       rs.Ceiling.Terrain,
				DoubleSided:   boolDefault(rs.Ceiling.DoubleSided, true),
				Alpha:         float32Default(rs.Ceiling.Alpha, 1.0),
				Extrusions:    loadExtrusions(rs.Ceiling.Extrusions, true),
				TerrainPoints: loadTerrainPts(rs.Ceiling.TerrainPoints, true),
				UVOffset:      mgl32.Vec2{rs.Ceiling.UVOffset.X, rs.Ceiling.UVOffset.Y},
				UVScale:       ceilUVScale,
				UVRotation:    rs.Ceiling.UVRotation,
				Detail:        detailFromRaw(rs.Ceiling.Detail),
				Smooth:        rs.Ceiling.Smooth,
				SmoothAngle:   rs.Ceiling.SmoothAngle,
			},
		}

		if rs.Floor.Texture != "" {
			texNeeded[rs.Floor.Texture] = true
		}
		if rs.Ceiling.Texture != "" {
			texNeeded[rs.Ceiling.Texture] = true
		}

		// A sector's edges must form a closed ring visiting each point exactly
		// once. Silently de-duplicating a repeated name (as this used to do)
		// produces a sector with fewer vertices than edges, which desynchronises
		// Walls/Portals from EdgeKinds and yields degenerate, overlapping
		// geometry. Reject the file instead so the damage is visible.
		pointSeen := make(map[string]bool, len(rs.Edges))
		for ei, e := range rs.Edges {
			if pointSeen[e.From] {
				return Chunk{}, nil, fmt.Errorf(
					"sector %q: point %q appears more than once in the edge ring", rs.ID, e.From)
			}
			pointSeen[e.From] = true

			next := rs.Edges[(ei+1)%len(rs.Edges)]
			if e.To != next.From {
				return Chunk{}, nil, fmt.Errorf(
					"sector %q: edge %d ends at %q but edge %d starts at %q — edges must form a closed ring",
					rs.ID, ei, e.To, (ei+1)%len(rs.Edges), next.From)
			}

			idx, ok := pointNameToIdx[e.From]
			if !ok {
				return Chunk{}, nil, fmt.Errorf(
					"sector %q: edge references unknown point %q", rs.ID, e.From)
			}
			sector.PointIndices = append(sector.PointIndices, idx)
		}

		for _, edge := range rs.Edges {
			fromIdx, okFrom := pointNameToIdx[edge.From]
			toIdx, okTo := pointNameToIdx[edge.To]
			if !okFrom || !okTo {
				return Chunk{}, nil, fmt.Errorf("sector %q: edge references unknown point: %s->%s",
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
				edgeID = migrateEdgeID(edge.From, edge.To)
			}
			edgeScript, err := scriptDataFromRaw(edge.RawScriptData, "sector "+rs.ID+" edge "+edgeID)
			if err != nil {
				return Chunk{}, nil, err
			}

			switch edge.Kind {
			case "wall":
				if edge.Texture != "" {
					texNeeded[edge.Texture] = true
				}
				wallUVScale := mgl32.Vec2{edge.UVScale.X, edge.UVScale.Y}
				if wallUVScale == (mgl32.Vec2{}) {
					wallUVScale = mgl32.Vec2{1, 1}
				}
				sector.Walls = append(sector.Walls, Wall{
					ID:            edgeID,
					ScriptData:    edgeScript,
					StartPoint:    start,
					EndPoint:      end,
					Texture:       edge.Texture,
					Collision:     edge.Collision,
					Visible:       !edge.Hidden,
					Pinned:        edge.Pinned,
					DoubleSided:   boolDefault(edge.DoubleSided, true),
					Terrain:       edge.WallTerrain,
					Alpha:         float32Default(edge.Alpha, 1.0),
					Extrusions:    loadExtrusions(edge.Extrusions, false),
					TerrainPoints: loadTerrainPts(edge.TerrainPoints, false),
					UVOffset:      mgl32.Vec2{edge.UVOffset.X, edge.UVOffset.Y},
					UVScale:       wallUVScale,
					UVRotation:    edge.UVRotation,
					Detail:        detailFromRaw(edge.Detail),
					Smooth:        edge.Smooth,
					SmoothAngle:   edge.SmoothAngle,
				})
				sector.EdgeKinds = append(sector.EdgeKinds, "wall")
				sector.EdgeIDs = append(sector.EdgeIDs, edgeID)
			case "portal":
				sector.Portals = append(sector.Portals, Portal{
					ID:          edgeID,
					ScriptData:  edgeScript,
					StartPoint:  start,
					EndPoint:    end,
					ToSector:    edge.ToSector,
					DrawGap:     edge.DrawGap,
					GapTexture:  edge.GapTexture,
					Visible:     !edge.Hidden,
					DoubleSided: boolDefault(edge.DoubleSided, true),
					Alpha:       float32Default(edge.Alpha, 1.0),
				})
				sector.EdgeKinds = append(sector.EdgeKinds, "portal")
				sector.EdgeIDs = append(sector.EdgeIDs, edgeID)
			}
		}

		// A file can carry duplicate IDs (hand-edited, or two edges migrating to
		// the same point pair in a malformed ring). Resolve them here so the
		// invariant holds from the moment the sector exists.
		dedupeSectorEdgeIDs(&sector)

		sectorPts := SectorPoints(&chunk, &sector)

		reversed := signedArea2D(sectorPts) < 0
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
				wall   Wall
				portal Portal
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
			newWalls := make([]Wall, 0, len(sector.Walls))
			newPortals := make([]Portal, 0, len(sector.Portals))
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
			sectorPts = SectorPoints(&chunk, &sector)
		}
		syncEdgeRecordIDs(&sector)

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
		for _, tp := range sector.Floor.TerrainPoints {
			if h := sector.Floor.Height + tp.Depth; h < minFloor {
				minFloor = h
			}
		}
		for _, ext := range sector.Floor.Extrusions {
			if h := sector.Floor.Height - ext.Depth; h < minFloor {
				minFloor = h
			}
		}
		maxCeil := sector.Ceiling.Height
		for _, off := range rs.Ceiling.VertexOffsets {
			if h := sector.Ceiling.Height + off; h > maxCeil {
				maxCeil = h
			}
		}
		for _, tp := range sector.Ceiling.TerrainPoints {
			if h := sector.Ceiling.Height + tp.Depth; h > maxCeil {
				maxCeil = h
			}
		}
		for _, ext := range sector.Ceiling.Extrusions {
			if h := sector.Ceiling.Height + ext.Depth; h > maxCeil {
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

	for si := range chunk.Sectors {
		s := &chunk.Sectors[si]
		sectorPts := SectorPoints(&chunk, s)
		if err := s.Load(sectorPts); err != nil {
			return Chunk{}, nil, fmt.Errorf("sector %q load: %w", s.ID, err)
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
	chunk.RebuildGaps()

	// Bounds so far come only from the chunk's points; roll up the sectors' own
	// bounds so geometry reaching past the point pool isn't frustum-culled with
	// it. Water surfaces are folded in later, by SyncBiomeWaterSurfaces.
	chunk.RecomputeBounds()

	// Biome sector polygons — points are chunk-local, re-derived to world
	// space using the same transform already applied to structural points.
	for _, rb := range rc.BiomeSectors {
		bs := BiomeSector{
			ID:          rb.ID,
			Points:      make([]mgl32.Vec2, len(rb.Points)),
			HeightDirty: true,
			GroundDirty: true,
			Brush: BiomeBrush{
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
				Water:        waterParamsFromRaw(rb.Brush.Water),

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
			bs.Points[i] = transformPoint2D(mgl32.Vec2{rp.X, rp.Y}, origin, rc.Rotation, sc)
		}
		chunk.BiomeSectors = append(chunk.BiomeSectors, bs)
	}

	return chunk, texNeeded, nil
}

// LoadLevel reads a YAML level file, converts it to runtime types, and uploads
// all geometry to the GPU. Texture loading is driven from this function via the
// global textureNames map and executablePath variable.
func LoadLevel(levelPath string) (Level, error) {
	raw, err := readRawLevelFile(levelPath)
	if err != nil {
		return Level{}, err
	}
	return rawToLevel(raw)
}

// readRawLevelFile parses a level file into its Raw form without touching the
// GPU. Split from LoadLevel so anything that only wants to inspect a level's
// authored shape — validation, migration checks, tooling — can do so headlessly.
func readRawLevelFile(levelPath string) (RawLevel, error) {
	data, err := os.ReadFile(levelPath)
	if err != nil {
		return RawLevel{}, err
	}
	var raw RawLevel
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return RawLevel{}, err
	}
	return raw, nil
}

// blankRawLevel returns a minimal valid RawLevel: a single four-walled room
// lit by soft ambient light. Used to write a starter file from the editor's
// "New Level" dialog.
func blankRawLevel() RawLevel {
	return RawLevel{
		Chunks: []RawChunk{{
			ID: "default",
			Points: map[string]Point{
				"A": {X: -4, Y: -4},
				"B": {X: 4, Y: -4},
				"C": {X: 4, Y: 4},
				"D": {X: -4, Y: 4},
			},
			Sectors: []RawSector{{
				ID: "room",
				Floor: RawFloor{
					Enabled: true,
					Height:  0,
					Texture: "default",
				},
				Ceiling: RawCeiling{
					Enabled: true,
					Height:  4,
					Texture: "default",
				},
				Edges: []RawEdge{
					{From: "A", To: "B", Kind: "wall", Texture: "default", Collision: true},
					{From: "B", To: "C", Kind: "wall", Texture: "default", Collision: true},
					{From: "C", To: "D", Kind: "wall", Texture: "default", Collision: true},
					{From: "D", To: "A", Kind: "wall", Texture: "default", Collision: true},
				},
			}},
		}},
		Lights: RawLights{
			Global: RawGlobalLight{Color: RawColor{R: 1, G: 1, B: 1}, Brightness: 0.3},
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
func NewBlankLevel() (Level, error) {
	return rawToLevel(RawLevel{
		Lights: RawLights{
			Global: RawGlobalLight{Color: RawColor{R: 1, G: 1, B: 1}, Brightness: 0.3},
		},
	})
}

// rawToLightSet converts parsed YAML lights into runtime lights. Split out of
// rawToLevel because it touches no GPU state.
func rawToLightSet(rl RawLights) LightSet {
	var ls LightSet
	ls.Global = GlobalLight{
		Color:      mgl32.Vec3{rl.Global.Color.R, rl.Global.Color.G, rl.Global.Color.B},
		Brightness: rl.Global.Brightness,
	}
	for _, rd := range rl.Directional {
		ss := rd.ShadowStrength
		if ss == 0 && rd.CastShadows {
			ss = 0.5 // default to half-strength shadows
		}
		dlScript, _ := scriptDataFromRaw(rd.RawScriptData, "directional light "+rd.ID)
		dl := DirectionalLight{
			ID:             rd.ID,
			ScriptData:     dlScript,
			Color:          mgl32.Vec3{rd.Color.R, rd.Color.G, rd.Color.B},
			Brightness:     rd.Brightness,
			CastShadows:    rd.CastShadows,
			ShadowStrength: ss,
		}
		// Angles are authoritative when present; otherwise derive them from the
		// direction vector, which is all pre-rotation levels stored.
		if rd.Yaw != nil && rd.Pitch != nil {
			dl.Yaw, dl.Pitch = *rd.Yaw, *rd.Pitch
		} else {
			dl.Yaw, dl.Pitch = DirLightYawPitch(mgl32.Vec3{rd.Direction.X, rd.Direction.Y, rd.Direction.Z})
		}
		dl.SyncDirection()
		ls.Directional = append(ls.Directional, dl)
	}
	for _, rp := range rl.Point {
		ss := rp.ShadowStrength
		if ss == 0 && rp.CastShadows {
			ss = 0.5
		}
		plScript, _ := scriptDataFromRaw(rp.RawScriptData, "point light "+rp.ID)
		ls.Points = append(ls.Points, PointLight{
			ID:             rp.ID,
			ScriptData:     plScript,
			Position:       mgl32.Vec3{rp.Position.X, rp.Position.Y, rp.Position.Z},
			Color:          mgl32.Vec3{rp.Color.R, rp.Color.G, rp.Color.B},
			Brightness:     rp.Brightness,
			Radius:         rp.Radius,
			CastShadows:    rp.CastShadows,
			ShadowStrength: ss,
		})
	}
	// Lights predate having identity, so most levels arrive with none. Mint them
	// here rather than at every use site, so a light is addressable the moment it
	// exists.
	assignLightIDs(&ls)
	return ls
}

// rawToLevel converts a parsed RawLevel into runtime types and uploads all
// referenced geometry/textures to the GPU.
func rawToLevel(raw RawLevel) (Level, error) {
	var level Level
	texNeeded := map[string]bool{}

	for _, rc := range raw.Chunks {
		chunk, chunkTex, err := loadRawChunk(rc)
		if err != nil {
			return Level{}, err
		}
		for t := range chunkTex {
			texNeeded[t] = true
		}
		level.Chunks = append(level.Chunks, chunk)
	}

	// Chunk and sector IDs are how a script addresses geometry, so they have to
	// name exactly one thing. Nothing enforced that before scripting existed, and
	// the editor could mint duplicates, so resolve them here — renaming with a
	// warning rather than rejecting, since a level authored under the old rules
	// should still open.
	level.Scripts = append([]string(nil), raw.Scripts...)

	resolveLevelIDCollisions(level.Chunks)
	resolveBiomeSectorIDCollisions(&level)

	// The player spawn: one optional marker per level, not a placed object.
	if raw.Spawn != nil {
		spawn := &SpawnPoint{
			Position: mgl32.Vec3{raw.Spawn.Position.X, raw.Spawn.Position.Y, raw.Spawn.Position.Z},
			Rotation: mgl32.Vec3{raw.Spawn.Rotation.X, raw.Spawn.Rotation.Y, raw.Spawn.Rotation.Z},
		}
		if err := spawn.Load(); err != nil {
			return Level{}, fmt.Errorf("spawn point load: %w", err)
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
		e, err := entityFromRaw(re, LoadEntityDef)
		if err != nil {
			return Level{}, err
		}
		// IDs address entities from scripts, so a level with two of the same
		// gets the duplicate renamed rather than made unreachable.
		e.ID = uniqueEntityID(&level, e.ID)
		if err := e.Load(); err != nil {
			return Level{}, fmt.Errorf("entity %q load: %w", e.ID, err)
		}
		level.Entities = append(level.Entities, e)
	}

	// Volumes: scriptable regions, with the same ID guarantee entities get.
	for _, rv := range raw.Volumes {
		v, err := volumeFromRaw(rv)
		if err != nil {
			return Level{}, err
		}
		v.ID = uniqueVolumeID(&level, v.ID)
		if err := v.Load(); err != nil {
			// A cage that would not build is a missing authoring aid, not a
			// broken level — the volume still works.
			log.Printf("volume %q: marker load: %v", v.ID, err)
		}
		level.Volumes = append(level.Volumes, v)
	}

	// Convert lights
	level.Lights = rawToLightSet(raw.Lights)

	// Load all textures referenced by this level
	for name := range texNeeded {
		if _, already := Textures[name]; already {
			continue
		}
		path, ok := textureNames[name]
		if !ok {
			return Level{}, fmt.Errorf("unknown texture %q — ensure a matching .png exists in textures/", name)
		}
		if err := LoadTexture(name, filepath.Join(executablePath, path)); err != nil {
			return Level{}, err
		}
	}

	level.Spatial = BuildSpatialHash(level.Chunks, 8.0)

	return level, nil
}

// float32Default returns the value of a *float32, defaulting to def when nil.
func float32Default(p *float32, def float32) float32 {
	if p != nil {
		return *p
	}
	return def
}
