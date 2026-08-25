package levelio

// level_save.go — serialise a runtime Level back to YAML.
//
// SaveLevel reconstructs the RawLevel representation from the live runtime
// state and writes it to disk. Point names come from chunk.PointNames, which is
// index-aligned with chunk.Points; points the editor added are unnamed there and
// get auto-names "E0", "E1", … chosen to avoid names already in use.
//
// Names are bound to point indices, never to coordinates. Matching by
// coordinate meant a point the editor had moved no longer matched its recorded
// name, so it was issued a fresh one — which collided with an existing point and
// silently corrupted the level on the next load.

import (
	"fmt"
	"foundry/internal/collision"
	"foundry/internal/fileutil"
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/render"
	"foundry/internal/script"
	"foundry/internal/world"
	"math"
	"os"
	"path/filepath"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// ScanLevelFiles returns sorted filenames of .yaml files in dir.
func ScanLevelFiles(dir string) []string {
	return fileutil.ScanFilesByExt(dir, ".yaml")
}

// WriteBlankLevel writes a minimal valid level YAML to path.
func WriteBlankLevel(path string) error {
	raw := blankRawLevel()
	data, err := yaml.Marshal(&raw)
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data)
}

// SaveLevel serialises level to YAML and writes it to path. The level is
// validated before anything touches the filesystem: a level that would not load
// back correctly is never written, so a bad save cannot destroy a good file.
func SaveLevel(level *world.Level, path string) error {
	raw, err := LevelToRaw(level)
	if err != nil {
		return err
	}
	if err := validateRawLevel(&raw); err != nil {
		return fmt.Errorf("refusing to save malformed level: %w", err)
	}
	data, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("yaml marshal: %w", err)
	}
	return fileutil.WriteFileAtomic(path, data)
}

// levelToRaw converts the runtime Level into its serialisable form.
func LevelToRaw(level *world.Level) (world.RawLevel, error) {
	var raw world.RawLevel

	for ci := range level.Chunks {
		ch := &level.Chunks[ci]
		rc, err := chunkToRaw(ch)
		if err != nil {
			return world.RawLevel{}, fmt.Errorf("chunk %q: %w", ch.ID, err)
		}
		raw.Chunks = append(raw.Chunks, rc)
	}

	if level.Spawn != nil {
		raw.Spawn = spawnToRaw(level.Spawn)
	}

	for i := range level.Entities {
		raw.Entities = append(raw.Entities, world.EntityToRaw(&level.Entities[i], entityDefFor(&level.Entities[i])))
	}

	for i := range level.Volumes {
		raw.Volumes = append(raw.Volumes, world.VolumeToRaw(&level.Volumes[i]))
	}

	raw.Scripts = append([]string(nil), level.Scripts...)
	raw.Lights = lightsToRaw(level.Lights)
	return raw, nil
}

// entityDefFor returns the definition an entity was instanced from, or nil when
// it cannot be read. Saving falls back to writing the entity in full in that
// case (see EntityToRaw), so a definition that has been deleted or renamed out
// from under a level costs a verbose entry rather than a lost one.
func entityDefFor(e *world.Entity) *world.EntityDef {
	if e.Type == "" {
		return nil
	}
	def, err := world.LoadEntityDef(e.Type)
	if err != nil {
		return nil
	}
	return def
}

// chunkToRaw serialises one chunk. It reverse-transforms points by removing
// the chunk's origin/rotation/scale so the YAML stores chunk-local coords.
func chunkToRaw(ch *world.Chunk) (world.RawChunk, error) {
	rc := world.RawChunk{
		ID:            ch.ID,
		RawScriptData: world.ScriptDataToRaw(ch.ScriptData),
		Points:        make(map[string]geom.Point),
		Origin:        world.RawChunkOrigin{X: ch.Origin.X(), Y: ch.Origin.Y(), Z: ch.Origin.Z()},
		Rotation:      ch.Rotation,
		Scale:         ch.Scale,
		Frame:         ch.FrameOwner,
	}
	if rc.Scale == 0 {
		rc.Scale = 1
	}

	// Name every point in the pool by index, then emit the live pool. Names are
	// bound to point indices, never to coordinates, so a point the editor moved
	// keeps its identity and cannot collide with an unrelated point.
	names := chunkPointNames(ch)
	for i, name := range names {
		local := reverseTransformPoint2D(ch.Points[i], ch.Origin, ch.Rotation, ch.Scale)
		rc.Points[name] = geom.Point{X: local.X(), Y: local.Y()}
	}

	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		// Only save non-zero VertexOffsets to keep YAML clean
		floorOffsets := trimTrailingZeros(s.Floor.VertexOffsets)
		ceilOffsets := trimTrailingZeros(s.Ceiling.VertexOffsets)
		rs := world.RawSector{
			ID:            s.ID,
			RawScriptData: world.ScriptDataToRaw(s.ScriptData),
			Floor: world.RawFloor{
				RawScriptData: world.ScriptDataToRaw(s.Floor.ScriptData),
				Enabled:       s.Floor.Enabled,
				Hidden:        s.Floor.Hidden,
				Height:        s.Floor.Height,
				VertexOffsets: floorOffsets,
				Texture:       s.Floor.Texture,
				BlendTexture:  s.Floor.BlendTexture,
				BlendMask:     s.Floor.BlendMask,
				Terrain:       s.Floor.Terrain,
				DoubleSided:   world.BoolPtrIfFalse(s.Floor.DoubleSided),
				Alpha:         float32PtrIfNotOne(s.Floor.Alpha),
				Extrusions:    saveExtrusions(s.Floor.Extrusions, ch, true),
				TerrainPoints: saveTerrainPts(s.Floor.TerrainPoints, ch, true),
				UVOffset:      geom.Point{X: s.Floor.UVOffset.X(), Y: s.Floor.UVOffset.Y()},
				UVScale:       geom.Point{X: s.Floor.UVScale.X(), Y: s.Floor.UVScale.Y()},
				UVRotation:    s.Floor.UVRotation,
				Detail:        world.DetailToRaw(s.Floor.Detail),
				Smooth:        s.Floor.Smooth,
				SmoothAngle:   s.Floor.SmoothAngle,
			},
			Ceiling: world.RawCeiling{
				RawScriptData: world.ScriptDataToRaw(s.Ceiling.ScriptData),
				Enabled:       s.Ceiling.Enabled,
				Height:        s.Ceiling.Height,
				VertexOffsets: ceilOffsets,
				Texture:       s.Ceiling.Texture,
				BlendTexture:  s.Ceiling.BlendTexture,
				BlendMask:     s.Ceiling.BlendMask,
				Terrain:       s.Ceiling.Terrain,
				DoubleSided:   world.BoolPtrIfFalse(s.Ceiling.DoubleSided),
				Alpha:         float32PtrIfNotOne(s.Ceiling.Alpha),
				Extrusions:    saveExtrusions(s.Ceiling.Extrusions, ch, true),
				TerrainPoints: saveTerrainPts(s.Ceiling.TerrainPoints, ch, true),
				UVOffset:      geom.Point{X: s.Ceiling.UVOffset.X(), Y: s.Ceiling.UVOffset.Y()},
				UVScale:       geom.Point{X: s.Ceiling.UVScale.X(), Y: s.Ceiling.UVScale.Y()},
				UVRotation:    s.Ceiling.UVRotation,
				Detail:        world.DetailToRaw(s.Ceiling.Detail),
				Smooth:        s.Ceiling.Smooth,
				SmoothAngle:   s.Ceiling.SmoothAngle,
			},
		}

		// Reconstruct edges from Walls and Portals in polygon order,
		// using EdgeKinds index-based matching (robust against float drift).
		nPts := len(s.PointIndices)
		wallIdx, portalIdx := 0, 0
		for ei := 0; ei < nPts; ei++ {
			fromIdx := s.PointIndices[ei]
			toIdx := s.PointIndices[(ei+1)%nPts]
			if fromIdx < 0 || fromIdx >= len(names) || toIdx < 0 || toIdx >= len(names) {
				return world.RawChunk{}, fmt.Errorf(
					"sector %q: edge %d references point index out of range (pool has %d points)",
					s.ID, ei, len(names))
			}
			fromName := names[fromIdx]
			toName := names[toIdx]

			kind := "wall"
			if ei < len(s.EdgeKinds) {
				kind = s.EdgeKinds[ei]
			}
			edgeID := world.EdgeIDAt(s, ei)

			if kind == "wall" && wallIdx < len(s.Walls) {
				w := &s.Walls[wallIdx]
				rs.Edges = append(rs.Edges, world.RawEdge{
					ID:            edgeID,
					RawScriptData: world.ScriptDataToRaw(w.ScriptData),
					From:          fromName,
					To:            toName,
					Kind:          "wall",
					Texture:       w.Texture,
					BlendTexture:  w.BlendTexture,
					BlendMask:     w.BlendMask,
					Collision:     w.Collision,
					Hidden:        !w.Visible,
					Pinned:        w.Pinned,
					DoubleSided:   world.BoolPtrIfFalse(w.DoubleSided),
					Alpha:         float32PtrIfNotOne(w.Alpha),
					Extrusions:    saveExtrusions(w.Extrusions, ch, false),
					WallTerrain:   w.Terrain,
					TerrainPoints: saveTerrainPts(w.TerrainPoints, ch, false),
					UVOffset:      geom.Point{X: w.UVOffset.X(), Y: w.UVOffset.Y()},
					UVScale:       geom.Point{X: w.UVScale.X(), Y: w.UVScale.Y()},
					UVRotation:    w.UVRotation,
					Detail:        world.DetailToRaw(w.Detail),
					Smooth:        w.Smooth,
					SmoothAngle:   w.SmoothAngle,
				})
				wallIdx++
			} else if kind == "portal" && portalIdx < len(s.Portals) {
				p := &s.Portals[portalIdx]
				rs.Edges = append(rs.Edges, world.RawEdge{
					ID:            edgeID,
					RawScriptData: world.ScriptDataToRaw(p.ScriptData),
					From:          fromName,
					To:            toName,
					Kind:          "portal",
					ToSector:      p.ToSector,
					ToFrame:       p.ToFrame,
					DrawGap:       p.DrawGap,
					GapTexture:    p.GapTexture,
					Hidden:        !p.Visible,
					DoubleSided:   world.BoolPtrIfFalse(p.DoubleSided),
					Alpha:         float32PtrIfNotOne(p.Alpha),
				})
				portalIdx++
			} else {
				// Fallback: emit a bare wall edge so data is not lost
				rs.Edges = append(rs.Edges, world.RawEdge{
					ID:   edgeID,
					From: fromName,
					To:   toName,
					Kind: "wall",
				})
			}
		}

		rc.Sectors = append(rc.Sectors, rs)
	}

	// Serialize biome sectors with reverse-transformed (chunk-local) points.
	for bi := range ch.BiomeSectors {
		bs := &ch.BiomeSectors[bi]
		rb := world.RawBiomeSector{
			ID:     bs.ID,
			Points: make([]geom.Point, len(bs.Points)),
			Brush: world.RawBiomeBrush{
				Kind:         bs.Brush.Kind,
				Density:      bs.Brush.Density,
				Range:        bs.Brush.Range,
				Color:        world.RawColor{R: bs.Brush.Color.X(), G: bs.Brush.Color.Y(), B: bs.Brush.Color.Z()},
				GrassHeight:  bs.Brush.GrassHeight,
				GrassWidth:   bs.Brush.GrassWidth,
				HeadScale:    bs.Brush.HeadScale,
				GrassClump:   bs.Brush.GrassClump,
				LOD1Distance: bs.Brush.LOD1Distance,
				LOD2Distance: bs.Brush.LOD2Distance,
				RockSides:    bs.Brush.RockSides,
				RockScale:    bs.Brush.RockScale,
				SandHeight:   bs.Brush.SandHeight,
				WaterDepth:   bs.Brush.WaterDepth,

				MossScatter:   bs.Brush.MossScatter,
				MinSlopeAngle: bs.Brush.MinSlopeAngle,
				MaxSlopeAngle: bs.Brush.MaxSlopeAngle,

				BloomSpike:      bs.Brush.BloomSpike,
				BloomCenter:     bs.Brush.BloomCenter,
				BloomSolidColor: bs.Brush.BloomSolidColor,
				CenterColor: world.RawColor{
					R: bs.Brush.CenterColor.X(), G: bs.Brush.CenterColor.Y(), B: bs.Brush.CenterColor.Z(),
				},
				StemLeaves: bs.Brush.StemLeaves,
				LeafScale:  bs.Brush.LeafScale,

				GroundTexture:   bs.Brush.GroundTexture,
				GroundAmplitude: bs.Brush.GroundAmplitude,
				GroundFrequency: bs.Brush.GroundFrequency,
				GroundOctaves:   bs.Brush.GroundOctaves,
				GroundUVScale:   geom.Point{X: bs.Brush.GroundUVScale.X(), Y: bs.Brush.GroundUVScale.Y()},

				RangeFadeFraction: bs.Brush.RangeFadeFraction,
				EdgeFadeDistance:  bs.Brush.EdgeFadeDistance,
				BoundaryJitter:    bs.Brush.BoundaryJitter,
			},
		}
		if bs.Brush.Kind == world.BiomeWater {
			water := bs.Brush.Water
			rb.Brush.Water = &world.RawWater{
				WaveAmplitude: water.WaveAmplitude,
				WaveLength:    water.WaveLength,
				WaveSpeed:     water.WaveSpeed,
				Reflectivity:  water.Reflectivity,
				ReflectColor: world.RawColor3{
					R: water.ReflectColor.X(), G: water.ReflectColor.Y(), B: water.ReflectColor.Z(),
				},
			}
		}
		for i, wp := range bs.Points {
			local := reverseTransformPoint2D(wp, ch.Origin, ch.Rotation, ch.Scale)
			rb.Points[i] = geom.Point{X: local.X(), Y: local.Y()}
		}
		rc.BiomeSectors = append(rc.BiomeSectors, rb)
	}

	return rc, nil
}

// chunkPointNames returns a YAML point name for every entry in ch.Points,
// index-aligned with that slice. Names recorded in ch.PointNames are preserved;
// unnamed slots (points the editor added) get a fresh "E<n>" chosen so it cannot
// collide with a name already in use.
//
// The collision check is the point of this function. Names used to be minted
// from a counter that started at zero on every save while ch.PointNames already
// held E0, E1, … from the previous save, so re-saving an edited level silently
// overwrote unrelated points and scrambled the geometry on the next load.
func chunkPointNames(ch *world.Chunk) []string {
	names := make([]string, len(ch.Points))
	used := make(map[string]bool, len(ch.Points))

	for i := range ch.Points {
		if i >= len(ch.PointNames) {
			break
		}
		if n := ch.PointNames[i]; n != "" && !used[n] {
			names[i] = n
			used[n] = true
		}
	}

	ctr := 0
	for i := range names {
		for names[i] == "" {
			n := fmt.Sprintf("E%d", ctr)
			ctr++
			if !used[n] {
				names[i] = n
				used[n] = true
			}
		}
	}
	return names
}

// validateRawChunk checks the invariants loadRawChunk depends on: every edge
// references a defined point, and each sector's edges form a closed ring that
// visits each point exactly once. Callers abort the write on error, so a
// serialisation bug can never overwrite a good level file with a broken one.
func validateRawChunk(rc *world.RawChunk) error {
	for si := range rc.Sectors {
		s := &rc.Sectors[si]
		if len(s.Edges) == 0 {
			continue
		}
		seen := make(map[string]bool, len(s.Edges))
		for ei, e := range s.Edges {
			if _, ok := rc.Points[e.From]; !ok {
				return fmt.Errorf("chunk %q sector %q: edge %d references undefined point %q",
					rc.ID, s.ID, ei, e.From)
			}
			if _, ok := rc.Points[e.To]; !ok {
				return fmt.Errorf("chunk %q sector %q: edge %d references undefined point %q",
					rc.ID, s.ID, ei, e.To)
			}
			if seen[e.From] {
				return fmt.Errorf("chunk %q sector %q: point %q appears more than once in the edge ring",
					rc.ID, s.ID, e.From)
			}
			seen[e.From] = true

			nextIdx := (ei + 1) % len(s.Edges)
			if next := s.Edges[nextIdx]; e.To != next.From {
				return fmt.Errorf(
					"chunk %q sector %q: edge %d ends at %q but edge %d starts at %q — edges must form a closed ring",
					rc.ID, s.ID, ei, e.To, nextIdx, next.From)
			}
		}
	}
	return nil
}

// validateRawLevel runs validateRawChunk over every chunk in a level.
func validateRawLevel(raw *world.RawLevel) error {
	for ci := range raw.Chunks {
		if err := validateRawChunk(&raw.Chunks[ci]); err != nil {
			return err
		}
	}
	return nil
}

// spawnToRaw converts the level's spawn point to its raw YAML form. Position
// stays in world space: the spawn belongs to the level, not to a chunk, so
// there is no chunk transform to be local to.
func spawnToRaw(s *world.SpawnPoint) *world.RawSpawn {
	return &world.RawSpawn{
		Position: world.RawVec3{X: s.Position.X(), Y: s.Position.Y(), Z: s.Position.Z()},
		Rotation: world.RawVec3{X: s.Rotation.X(), Y: s.Rotation.Y(), Z: s.Rotation.Z()},
		Frame:    s.Frame,
		Seat:     s.Seat,
	}
}

// lightsToRaw converts the LightSet to its raw YAML form.
func lightsToRaw(ls world.LightSet) world.RawLights {
	rl := world.RawLights{
		Global: world.RawGlobalLight{
			Color:      world.RawColor{R: ls.Global.Color.X(), G: ls.Global.Color.Y(), B: ls.Global.Color.Z()},
			Brightness: ls.Global.Brightness,
			Disabled:   ls.Global.Disabled,
		},
	}
	for _, dl := range ls.Directional {
		// Re-derive so a stale/NaN Direction from an older in-memory level can
		// never be written back out.
		dl.SyncDirection()
		yaw, pitch := dl.Yaw, dl.Pitch
		rl.Directional = append(rl.Directional, world.RawDirLight{
			ID:            dl.ID,
			RawScriptData: world.ScriptDataToRaw(dl.ScriptData),
			Direction: struct {
				X float32 `yaml:"x"`
				Y float32 `yaml:"y"`
				Z float32 `yaml:"z"`
			}{dl.Direction.X(), dl.Direction.Y(), dl.Direction.Z()},
			Yaw:            &yaw,
			Pitch:          &pitch,
			Color:          world.RawColor{R: dl.Color.X(), G: dl.Color.Y(), B: dl.Color.Z()},
			Brightness:     dl.Brightness,
			CastShadows:    dl.CastShadows,
			ShadowStrength: dl.ShadowStrength,
			Disabled:       dl.Disabled,
		})
	}
	for _, pl := range ls.Points {
		rl.Point = append(rl.Point, world.RawPointLight{
			ID:            pl.ID,
			RawScriptData: world.ScriptDataToRaw(pl.ScriptData),
			Position: struct {
				X float32 `yaml:"x"`
				Y float32 `yaml:"y"`
				Z float32 `yaml:"z"`
			}{pl.Position.X(), pl.Position.Y(), pl.Position.Z()},
			Color:          world.RawColor{R: pl.Color.X(), G: pl.Color.Y(), B: pl.Color.Z()},
			Brightness:     pl.Brightness,
			Radius:         pl.Radius,
			CastShadows:    pl.CastShadows,
			ShadowStrength: pl.ShadowStrength,
			Disabled:       pl.Disabled,
		})
	}
	return rl
}

// ---------- extrusion/terrain save helpers ----------

// saveExtrusions converts runtime extrusions to raw YAML form.
// If transformXZ is true, reverse-transforms U/V as world XZ → chunk-local.
func saveExtrusions(exts []world.Extrusion, ch *world.Chunk, transformXZ bool) []world.RawExtrusion {
	if len(exts) == 0 {
		return nil
	}
	var out []world.RawExtrusion
	for _, ext := range exts {
		re := world.RawExtrusion{
			Depth:       ext.Depth,
			Texture:     ext.Texture,
			Visible:     world.BoolPtrIfFalse(ext.Visible),
			BackVisible: world.BoolPtrIfFalse(ext.BackVisible),
			UVOffset:    geom.Point{X: ext.UVOffset.X(), Y: ext.UVOffset.Y()},
			UVScale:     geom.Point{X: ext.UVScale.X(), Y: ext.UVScale.Y()},
			UVRotation:  ext.UVRotation,
			Alpha:       float32PtrIfNotOne(ext.Alpha),
		}
		for _, p := range ext.Outer {
			if transformXZ {
				local := reverseTransformPoint2D(mgl32.Vec2{p.U, p.V}, ch.Origin, ch.Rotation, ch.Scale)
				re.Outer = append(re.Outer, world.RawExtrusionPoint{U: local.X(), V: local.Y()})
			} else {
				re.Outer = append(re.Outer, world.RawExtrusionPoint{U: p.U, V: p.V})
			}
		}
		for _, p := range ext.Inner {
			if transformXZ {
				local := reverseTransformPoint2D(mgl32.Vec2{p.U, p.V}, ch.Origin, ch.Rotation, ch.Scale)
				re.Inner = append(re.Inner, world.RawExtrusionPoint{U: local.X(), V: local.Y()})
			} else {
				re.Inner = append(re.Inner, world.RawExtrusionPoint{U: p.U, V: p.V})
			}
		}
		out = append(out, re)
	}
	return out
}

// saveTerrainPts converts runtime terrain points to raw YAML form.
func saveTerrainPts(pts []world.TerrainPoint, ch *world.Chunk, transformXZ bool) []world.RawTerrainPoint {
	if len(pts) == 0 {
		return nil
	}
	var out []world.RawTerrainPoint
	for _, tp := range pts {
		if transformXZ {
			local := reverseTransformPoint2D(mgl32.Vec2{tp.U, tp.V}, ch.Origin, ch.Rotation, ch.Scale)
			out = append(out, world.RawTerrainPoint{U: local.X(), V: local.Y(), Depth: tp.Depth})
		} else {
			out = append(out, world.RawTerrainPoint{U: tp.U, V: tp.V, Depth: tp.Depth})
		}
	}
	return out
}

// ---------- coordinate helpers ----------

// transformPoint2D applies scale → Y-rotation → origin offset to a 2D (XZ) point.
func transformPoint2D(pt mgl32.Vec2, origin mgl32.Vec3, rotation, scale float32) mgl32.Vec2 {
	if scale == 0 {
		scale = 1
	}
	x := pt.X() * scale
	y := pt.Y() * scale
	cos := float32(math.Cos(float64(rotation)))
	sin := float32(math.Sin(float64(rotation)))
	return mgl32.Vec2{
		cos*x - sin*y + origin.X(),
		sin*x + cos*y + origin.Z(),
	}
}

// reverseTransformPoint2D inverts the chunk transform to recover local coords.
func reverseTransformPoint2D(world mgl32.Vec2, origin mgl32.Vec3, rotation, scale float32) mgl32.Vec2 {
	if scale == 0 {
		scale = 1
	}
	// Undo origin
	x := world.X() - origin.X()
	y := world.Y() - origin.Z()
	// Undo rotation (rotate by -rotation)
	cos := float32(math.Cos(float64(-rotation)))
	sin := float32(math.Sin(float64(-rotation)))
	rx := cos*x - sin*y
	ry := sin*x + cos*y
	// Undo scale
	return mgl32.Vec2{rx / scale, ry / scale}
}

// float32PtrIfNotOne returns a *float32 pointer only when v differs from 1.0.
// Returns nil when v == 1.0 so the field is omitted from YAML via omitempty.
func float32PtrIfNotOne(v float32) *float32 {
	if v != 1.0 {
		return &v
	}
	return nil
}

// trimTrailingZeros returns a copy of s with trailing zero values removed.
// Returns nil if all values are zero (avoids writing empty arrays to YAML).
func trimTrailingZeros(s []float32) []float32 {
	last := -1
	for i, v := range s {
		if v != 0 {
			last = i
		}
	}
	if last < 0 {
		return nil
	}
	out := make([]float32, last+1)
	copy(out, s[:last+1])
	return out
}

// SaveChunk serialises a single chunk to a standalone YAML file.
func SaveChunk(ch *world.Chunk, path string) error {
	rc, err := chunkToRaw(ch)
	if err != nil {
		return err
	}
	if err := validateRawChunk(&rc); err != nil {
		return fmt.Errorf("refusing to save malformed chunk: %w", err)
	}
	data, err := yaml.Marshal(rc)
	if err != nil {
		return fmt.Errorf("yaml marshal: %w", err)
	}
	return fileutil.WriteFileAtomic(path, data)
}

// LoadChunkFromFile reads a standalone chunk YAML file.
func LoadChunkFromFile(path string) (world.RawChunk, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return world.RawChunk{}, err
	}
	var rc world.RawChunk
	if err := yaml.Unmarshal(data, &rc); err != nil {
		return world.RawChunk{}, err
	}
	return rc, nil
}

// ImportChunkIntoLevel loads a chunk from a YAML file and appends it to the level.
// It resolves ID conflicts by appending numeric suffixes.
func ImportChunkIntoLevel(lvl *world.Level, path string) (string, error) {
	rc, err := LoadChunkFromFile(path)
	if err != nil {
		return "", err
	}

	existingIDs := map[string]bool{}
	existingSectorIDs := map[string]bool{}
	for i := range lvl.Chunks {
		existingIDs[lvl.Chunks[i].ID] = true
		for j := range lvl.Chunks[i].Sectors {
			existingSectorIDs[lvl.Chunks[i].Sectors[j].ID] = true
		}
	}

	suffix := ""
	if existingIDs[rc.ID] {
		for n := 1; ; n++ {
			suffix = fmt.Sprintf("_%d", n)
			if !existingIDs[rc.ID+suffix] {
				break
			}
		}
		rc.ID = rc.ID + suffix
	}

	if suffix != "" {
		for si := range rc.Sectors {
			oldID := rc.Sectors[si].ID
			rc.Sectors[si].ID = oldID + suffix
			for ei := range rc.Sectors[si].Edges {
				if rc.Sectors[si].Edges[ei].ToSector == oldID {
					rc.Sectors[si].Edges[ei].ToSector = oldID + suffix
				}
			}
		}
		// Remap portal ToSector references that point to other sectors in the chunk
		sectorOldIDs := map[string]bool{}
		for si := range rc.Sectors {
			sectorOldIDs[rc.Sectors[si].ID] = true
		}
		for si := range rc.Sectors {
			for ei := range rc.Sectors[si].Edges {
				e := &rc.Sectors[si].Edges[ei]
				if e.Kind == "portal" && !sectorOldIDs[e.ToSector] {
					candidate := e.ToSector + suffix
					if sectorOldIDs[candidate] {
						e.ToSector = candidate
					}
				}
			}
		}
	}

	chunk, texNeeded, err := loadRawChunk(rc)
	if err != nil {
		return "", fmt.Errorf("load chunk %q: %w", rc.ID, err)
	}

	for name := range texNeeded {
		if _, already := gfx.Textures[name]; already {
			continue
		}
		tpath, ok := gfx.TextureNames[name]
		if !ok {
			return "", fmt.Errorf("unknown texture %q — ensure a matching .png exists in textures/", name)
		}
		if err := gfx.LoadTexture(name, filepath.Join(fileutil.ExecutablePath, tpath)); err != nil {
			return "", err
		}
	}

	lvl.Chunks = append(lvl.Chunks, chunk)
	if err := world.RebuildIndexes(lvl); err != nil {
		return "", err
	}

	return rc.ID, nil
}

// applyRawLevel rebuilds lvl from a raw snapshot, in place.
//
// Split out of restoreLevelSnapshot because three different things need exactly
// this and nothing else: undo, redo, and the end of a play session
// (script_play.go). The parts they do *not* share — resetting the editor's
// selection, and moving the player to the spawn — are the caller's to decide.
func ApplyRawLevel(raw world.RawLevel, lvl *world.Level) error {
	newLevel, err := rawToLevel(raw)
	if err != nil {
		return err
	}
	// Built before the swap so a failed rebuild leaves the current level intact,
	// released after it so the outgoing entities' GPU buffers do not leak — see
	// EditorLoadLevel's note.
	collision.DeleteEntities(lvl)
	render.DeleteVolumes(lvl)
	*lvl = newLevel

	world.UploadLights(lvl.Lights)
	render.LoadVolumes(lvl)

	// Every ref a script holds now points at an object that has been
	// re-instantiated, so anything the runtime remembered about them is stale.
	script.ScriptRuntimeLoad(lvl)
	return nil
}
