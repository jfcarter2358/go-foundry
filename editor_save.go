package main

// editor_save.go — serialise a runtime Level back to YAML.
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
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// ScanLevelFiles returns sorted filenames of .yaml files in dir.
func ScanLevelFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(files)
	return files
}

// WriteBlankLevel writes a minimal valid level YAML to path.
func WriteBlankLevel(path string) error {
	raw := blankRawLevel()
	data, err := yaml.Marshal(&raw)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

// SaveLevel serialises level to YAML and writes it to path. The level is
// validated before anything touches the filesystem: a level that would not load
// back correctly is never written, so a bad save cannot destroy a good file.
func SaveLevel(level *Level, path string) error {
	raw, err := levelToRaw(level)
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
	return writeFileAtomic(path, data)
}

// writeFileAtomic writes data to path via a temporary file in the same
// directory followed by a rename, so an interrupted or failing write can never
// leave a truncated level behind. The previous contents are retained as
// path+".bak".
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		return err
	}

	// Keep one generation of backup so a bad edit is still recoverable.
	if prev, err := os.ReadFile(path); err == nil {
		if err := os.WriteFile(path+".bak", prev, 0644); err != nil {
			return err
		}
	}

	return os.Rename(tmpName, path)
}

// levelToRaw converts the runtime Level into its serialisable form.
func levelToRaw(level *Level) (RawLevel, error) {
	var raw RawLevel

	for ci := range level.Chunks {
		ch := &level.Chunks[ci]
		rc, err := chunkToRaw(ch)
		if err != nil {
			return RawLevel{}, fmt.Errorf("chunk %q: %w", ch.ID, err)
		}
		raw.Chunks = append(raw.Chunks, rc)
	}

	for ei := range level.Entities {
		e := &level.Entities[ei]
		raw.Entities = append(raw.Entities, entityToRaw(e))
	}

	raw.Lights = lightsToRaw(level.Lights)
	return raw, nil
}

// chunkToRaw serialises one chunk. It reverse-transforms points by removing
// the chunk's origin/rotation/scale so the YAML stores chunk-local coords.
func chunkToRaw(ch *Chunk) (RawChunk, error) {
	rc := RawChunk{
		ID:       ch.ID,
		Points:   make(map[string]Point),
		Origin:   RawChunkOrigin{X: ch.Origin.X(), Y: ch.Origin.Y(), Z: ch.Origin.Z()},
		Rotation: ch.Rotation,
		Scale:    ch.Scale,
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
		rc.Points[name] = Point{X: local.X(), Y: local.Y()}
	}

	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		// Only save non-zero VertexOffsets to keep YAML clean
		floorOffsets := trimTrailingZeros(s.Floor.VertexOffsets)
		ceilOffsets := trimTrailingZeros(s.Ceiling.VertexOffsets)
		rs := RawSector{
			ID: s.ID,
			Floor: RawFloor{
				Enabled:       s.Floor.Enabled,
				Height:        s.Floor.Height,
				VertexOffsets: floorOffsets,
				Texture:       s.Floor.Texture,
				Terrain:       s.Floor.Terrain,
				DoubleSided:   boolPtrIfFalse(s.Floor.DoubleSided),
				Alpha:         float32PtrIfNotOne(s.Floor.Alpha),
				Extrusions:    saveExtrusions(s.Floor.Extrusions, ch, true),
				TerrainPoints: saveTerrainPts(s.Floor.TerrainPoints, ch, true),
				UVOffset:      Point{X: s.Floor.UVOffset.X(), Y: s.Floor.UVOffset.Y()},
				UVScale:       Point{X: s.Floor.UVScale.X(), Y: s.Floor.UVScale.Y()},
				UVRotation:    s.Floor.UVRotation,
				Detail:        detailToRaw(s.Floor.Detail),
			},
			Ceiling: RawCeiling{
				Enabled:       s.Ceiling.Enabled,
				Height:        s.Ceiling.Height,
				VertexOffsets: ceilOffsets,
				Texture:       s.Ceiling.Texture,
				Terrain:       s.Ceiling.Terrain,
				DoubleSided:   boolPtrIfFalse(s.Ceiling.DoubleSided),
				Alpha:         float32PtrIfNotOne(s.Ceiling.Alpha),
				Extrusions:    saveExtrusions(s.Ceiling.Extrusions, ch, true),
				TerrainPoints: saveTerrainPts(s.Ceiling.TerrainPoints, ch, true),
				UVOffset:      Point{X: s.Ceiling.UVOffset.X(), Y: s.Ceiling.UVOffset.Y()},
				UVScale:       Point{X: s.Ceiling.UVScale.X(), Y: s.Ceiling.UVScale.Y()},
				UVRotation:    s.Ceiling.UVRotation,
				Detail:        detailToRaw(s.Ceiling.Detail),
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
				return RawChunk{}, fmt.Errorf(
					"sector %q: edge %d references point index out of range (pool has %d points)",
					s.ID, ei, len(names))
			}
			fromName := names[fromIdx]
			toName := names[toIdx]

			kind := "wall"
			if ei < len(s.EdgeKinds) {
				kind = s.EdgeKinds[ei]
			}

			if kind == "wall" && wallIdx < len(s.Walls) {
				w := &s.Walls[wallIdx]
				rs.Edges = append(rs.Edges, RawEdge{
					From:          fromName,
					To:            toName,
					Kind:          "wall",
					Texture:       w.Texture,
					Collision:     w.Collision,
					Hidden:        !w.Visible,
					Pinned:        w.Pinned,
					DoubleSided:   boolPtrIfFalse(w.DoubleSided),
					Alpha:         float32PtrIfNotOne(w.Alpha),
					Extrusions:    saveExtrusions(w.Extrusions, ch, false),
					WallTerrain:   w.Terrain,
					TerrainPoints: saveTerrainPts(w.TerrainPoints, ch, false),
					UVOffset:      Point{X: w.UVOffset.X(), Y: w.UVOffset.Y()},
					UVScale:       Point{X: w.UVScale.X(), Y: w.UVScale.Y()},
					UVRotation:    w.UVRotation,
					Detail:        detailToRaw(w.Detail),
				})
				wallIdx++
			} else if kind == "portal" && portalIdx < len(s.Portals) {
				p := &s.Portals[portalIdx]
				rs.Edges = append(rs.Edges, RawEdge{
					From:        fromName,
					To:          toName,
					Kind:        "portal",
					ToSector:    p.ToSector,
					DrawGap:     p.DrawGap,
					GapTexture:  p.GapTexture,
					Hidden:      !p.Visible,
					DoubleSided: boolPtrIfFalse(p.DoubleSided),
					Alpha:       float32PtrIfNotOne(p.Alpha),
				})
				portalIdx++
			} else {
				// Fallback: emit a bare wall edge so data is not lost
				rs.Edges = append(rs.Edges, RawEdge{
					From: fromName,
					To:   toName,
					Kind: "wall",
				})
			}
		}

		rc.Sectors = append(rc.Sectors, rs)
	}

	// Serialize primitives with reverse-transformed positions
	for pi := range ch.Primitives {
		p := &ch.Primitives[pi]
		rc.Primitives = append(rc.Primitives, primitiveToRaw(p, ch))
	}

	// Serialize biome sectors with reverse-transformed (chunk-local) points.
	for bi := range ch.BiomeSectors {
		bs := &ch.BiomeSectors[bi]
		rb := RawBiomeSector{
			ID:     bs.ID,
			Points: make([]Point, len(bs.Points)),
			Brush: RawBiomeBrush{
				Kind:        bs.Brush.Kind,
				Density:     bs.Brush.Density,
				Range:       bs.Brush.Range,
				Color:       RawColor{R: bs.Brush.Color.X(), G: bs.Brush.Color.Y(), B: bs.Brush.Color.Z()},
				GrassHeight: bs.Brush.GrassHeight,
				GrassWidth:  bs.Brush.GrassWidth,
				RockSides:   bs.Brush.RockSides,
			},
		}
		for i, wp := range bs.Points {
			local := reverseTransformPoint2D(wp, ch.Origin, ch.Rotation, ch.Scale)
			rb.Points[i] = Point{X: local.X(), Y: local.Y()}
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
func chunkPointNames(ch *Chunk) []string {
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
func validateRawChunk(rc *RawChunk) error {
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
func validateRawLevel(raw *RawLevel) error {
	for ci := range raw.Chunks {
		if err := validateRawChunk(&raw.Chunks[ci]); err != nil {
			return err
		}
	}
	return nil
}

// primitiveToRaw converts a runtime Primitive to its raw YAML form,
// reverse-transforming the position back to chunk-local coordinates.
func primitiveToRaw(p *Primitive, ch *Chunk) RawPrimitive {
	// Reverse-transform position from world to chunk-local
	localXZ := reverseTransformPoint2D(
		mgl32.Vec2{p.Position.X(), p.Position.Z()},
		ch.Origin, ch.Rotation, ch.Scale,
	)
	localY := p.Position.Y() - ch.Origin.Y()

	sc := p.Scale
	if sc == (mgl32.Vec3{}) {
		sc = mgl32.Vec3{1, 1, 1}
	}

	var typeStr string
	switch p.Kind {
	case PrimitiveSphere:
		typeStr = "sphere"
	case PrimitiveCylinder:
		typeStr = "cylinder"
	case PrimitivePlane:
		typeStr = "plane"
	case PrimitiveWater:
		typeStr = "water"
	default:
		typeStr = "rect_prism"
	}

	uvSc := p.UVScale
	if uvSc == (mgl32.Vec2{}) {
		uvSc = mgl32.Vec2{1, 1}
	}

	rp := RawPrimitive{
		ID:        p.ID,
		Type:      typeStr,
		Position:  RawEntityPosition{X: localXZ.X(), Y: localY, Z: localXZ.Y()},
		Rotation:  RawEntityPosition{X: p.Rotation.X(), Y: p.Rotation.Y(), Z: p.Rotation.Z()},
		Scale:     RawEntityPosition{X: sc.X(), Y: sc.Y(), Z: sc.Z()},
		Texture:   p.Texture,
		Sides:     p.Sides,
		Collision: p.Collision,
		Alpha:      float32PtrIfNotOne(p.Alpha),
		UVOffset:   Point{X: p.UVOffset.X(), Y: p.UVOffset.Y()},
		UVScale:    Point{X: uvSc.X(), Y: uvSc.Y()},
		UVRotation: p.UVRotation,
		Water:      waterParamsToRaw(p),
	}
	return rp
}

// entityToRaw converts one entity to its raw YAML form.
func entityToRaw(e *Entity) RawEntity {
	sc := e.Scale
	if sc == (mgl32.Vec3{}) {
		sc = mgl32.Vec3{1, 1, 1}
	}
	// Strip executablePath prefix from ModelPath for portability
	modelPath := e.ModelPath
	if len(executablePath) > 0 && len(modelPath) > len(executablePath)+1 {
		rel := modelPath[len(executablePath)+1:]
		modelPath = rel
	}
	bKind := "aabb"
	switch e.BoundingKind {
	case BoundingSphere:
		bKind = "sphere"
	case BoundingCylinder:
		bKind = "cylinder"
	}
	return RawEntity{
		ID:       e.ID,
		Position: RawEntityPosition{X: e.Position.X(), Y: e.Position.Y(), Z: e.Position.Z()},
		Rotation: RawEntityPosition{X: e.Rotation.X(), Y: e.Rotation.Y(), Z: e.Rotation.Z()},
		Scale:    RawEntityPosition{X: sc.X(), Y: sc.Y(), Z: sc.Z()},
		Model:    modelPath,
		Texture:  e.TexturePath,
		Bounding: bKind,
		BoundHalfX: e.BoundHalfX,
		BoundHalfY: e.BoundHalfY,
		BoundHalfZ: e.BoundHalfZ,
		Alpha:      float32PtrIfNotOne(e.Alpha),
	}
}

// lightsToRaw converts the LightSet to its raw YAML form.
func lightsToRaw(ls LightSet) RawLights {
	rl := RawLights{
		Global: RawGlobalLight{
			Color:      RawColor{R: ls.Global.Color.X(), G: ls.Global.Color.Y(), B: ls.Global.Color.Z()},
			Brightness: ls.Global.Brightness,
		},
	}
	for _, dl := range ls.Directional {
		rl.Directional = append(rl.Directional, RawDirLight{
			Direction: struct {
				X float32 `yaml:"x"`
				Y float32 `yaml:"y"`
				Z float32 `yaml:"z"`
			}{dl.Direction.X(), dl.Direction.Y(), dl.Direction.Z()},
			Color:          RawColor{R: dl.Color.X(), G: dl.Color.Y(), B: dl.Color.Z()},
			Brightness:     dl.Brightness,
			CastShadows:    dl.CastShadows,
			ShadowStrength: dl.ShadowStrength,
		})
	}
	for _, pl := range ls.Points {
		rl.Point = append(rl.Point, RawPointLight{
			Position: struct {
				X float32 `yaml:"x"`
				Y float32 `yaml:"y"`
				Z float32 `yaml:"z"`
			}{pl.Position.X(), pl.Position.Y(), pl.Position.Z()},
			Color:          RawColor{R: pl.Color.X(), G: pl.Color.Y(), B: pl.Color.Z()},
			Brightness:     pl.Brightness,
			Radius:         pl.Radius,
			CastShadows:    pl.CastShadows,
			ShadowStrength: pl.ShadowStrength,
		})
	}
	return rl
}

// ---------- extrusion/terrain save helpers ----------

// saveExtrusions converts runtime extrusions to raw YAML form.
// If transformXZ is true, reverse-transforms U/V as world XZ → chunk-local.
func saveExtrusions(exts []Extrusion, ch *Chunk, transformXZ bool) []RawExtrusion {
	if len(exts) == 0 {
		return nil
	}
	var out []RawExtrusion
	for _, ext := range exts {
		re := RawExtrusion{
			Depth:       ext.Depth,
			Texture:     ext.Texture,
			Visible:     boolPtrIfFalse(ext.Visible),
			BackVisible: boolPtrIfFalse(ext.BackVisible),
			UVOffset:    Point{X: ext.UVOffset.X(), Y: ext.UVOffset.Y()},
			UVScale:     Point{X: ext.UVScale.X(), Y: ext.UVScale.Y()},
			UVRotation:  ext.UVRotation,
			Alpha:       float32PtrIfNotOne(ext.Alpha),
		}
		for _, p := range ext.Outer {
			if transformXZ {
				local := reverseTransformPoint2D(mgl32.Vec2{p.U, p.V}, ch.Origin, ch.Rotation, ch.Scale)
				re.Outer = append(re.Outer, RawExtrusionPoint{U: local.X(), V: local.Y()})
			} else {
				re.Outer = append(re.Outer, RawExtrusionPoint{U: p.U, V: p.V})
			}
		}
		for _, p := range ext.Inner {
			if transformXZ {
				local := reverseTransformPoint2D(mgl32.Vec2{p.U, p.V}, ch.Origin, ch.Rotation, ch.Scale)
				re.Inner = append(re.Inner, RawExtrusionPoint{U: local.X(), V: local.Y()})
			} else {
				re.Inner = append(re.Inner, RawExtrusionPoint{U: p.U, V: p.V})
			}
		}
		out = append(out, re)
	}
	return out
}

// saveTerrainPts converts runtime terrain points to raw YAML form.
func saveTerrainPts(pts []TerrainPoint, ch *Chunk, transformXZ bool) []RawTerrainPoint {
	if len(pts) == 0 {
		return nil
	}
	var out []RawTerrainPoint
	for _, tp := range pts {
		if transformXZ {
			local := reverseTransformPoint2D(mgl32.Vec2{tp.U, tp.V}, ch.Origin, ch.Rotation, ch.Scale)
			out = append(out, RawTerrainPoint{U: local.X(), V: local.Y(), Depth: tp.Depth})
		} else {
			out = append(out, RawTerrainPoint{U: tp.U, V: tp.V, Depth: tp.Depth})
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

// detailToRaw converts a face's DetailSpec to an optional RawDetail. A disabled
// spec returns nil so the "detail" key is omitted from YAML entirely.
func detailToRaw(d DetailSpec) *RawDetail {
	if !d.Enabled {
		return nil
	}
	return &RawDetail{
		Preset:         d.Preset,
		Seed:           d.Seed,
		Displace:       d.Displace,
		Amplitude:      d.Amplitude,
		Frequency:      d.Frequency,
		Octaves:        d.Octaves,
		TessLevel:      d.TessLevel,
		EdgeTaper:      d.EdgeTaper,
		Scatter:        d.Scatter,
		ScatterMesh:    d.ScatterMesh,
		ScatterTex:     d.ScatterTex,
		ScatterDensity: d.ScatterDensity,
		ScatterSpacing: d.ScatterSpacing,
		ScatterScale:   d.ScatterScale,
		ScatterJitter:  d.ScatterJitter,
		ScatterRange:   d.ScatterRange,
	}
}

// boolPtrIfFalse returns a *bool pointer only when v is false (non-default).
// When v is true (the default for DoubleSided), returns nil so the field is
// omitted from YAML via omitempty.
func boolPtrIfFalse(v bool) *bool {
	if !v {
		return &v
	}
	return nil
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
func SaveChunk(ch *Chunk, path string) error {
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
	return writeFileAtomic(path, data)
}

// LoadChunkFromFile reads a standalone chunk YAML file.
func LoadChunkFromFile(path string) (RawChunk, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RawChunk{}, err
	}
	var rc RawChunk
	if err := yaml.Unmarshal(data, &rc); err != nil {
		return RawChunk{}, err
	}
	return rc, nil
}

// ImportChunkIntoLevel loads a chunk from a YAML file and appends it to the level.
// It resolves ID conflicts by appending numeric suffixes.
func ImportChunkIntoLevel(lvl *Level, path string) (string, error) {
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
		for pi := range rc.Primitives {
			rc.Primitives[pi].ID = rc.Primitives[pi].ID + suffix
		}
	}

	chunk, texNeeded, err := loadRawChunk(rc)
	if err != nil {
		return "", fmt.Errorf("load chunk %q: %w", rc.ID, err)
	}

	for name := range texNeeded {
		if _, already := Textures[name]; already {
			continue
		}
		tpath, ok := textureNames[name]
		if !ok {
			return "", fmt.Errorf("unknown texture %q — ensure a matching .png exists in textures/", name)
		}
		if err := LoadTexture(name, filepath.Join(executablePath, tpath)); err != nil {
			return "", err
		}
	}

	lvl.Chunks = append(lvl.Chunks, chunk)
	lvl.Spatial = BuildSpatialHash(lvl.Chunks, 8.0)

	return rc.ID, nil
}
