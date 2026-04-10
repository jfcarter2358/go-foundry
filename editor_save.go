package main

// editor_save.go — serialise a runtime Level back to YAML.
//
// SaveLevel reconstructs the RawLevel representation from the live runtime
// state and writes it to disk. Original point names are preserved via
// chunk.PointNames; new points added by the editor get auto-names "E0", "E1", …

import (
	"fmt"
	"math"
	"os"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// SaveLevel serialises level to YAML and writes it to path.
func SaveLevel(level *Level, path string) error {
	raw, err := levelToRaw(level)
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("yaml marshal: %w", err)
	}
	return os.WriteFile(path, data, 0644)
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

	// Build a canonical set of unique points across all sectors.
	// Key: "x,z" (rounded to 4 dp) → point name.
	pointKey := func(v mgl32.Vec2) string {
		return fmt.Sprintf("%.4f,%.4f", v.X(), v.Y())
	}
	ptNames := make(map[string]string) // key → name
	ptCounter := 0

	// Prefer to re-use original names from PointNames
	if ch.PointNames != nil {
		for name, orig := range ch.PointNames {
			// Transform forward to get the world coord, then use as key
			tp := transformPoint2D(orig, ch.Origin, ch.Rotation, ch.Scale)
			ptNames[pointKey(tp)] = name
			rc.Points[name] = Point{X: orig.X(), Y: orig.Y()}
		}
	}

	// For any sector point not already in PointNames, assign a new name.
	assignPoint := func(worldPt mgl32.Vec2) string {
		key := pointKey(worldPt)
		if name, ok := ptNames[key]; ok {
			return name
		}
		// Reverse-transform to local coords
		local := reverseTransformPoint2D(worldPt, ch.Origin, ch.Rotation, ch.Scale)
		name := fmt.Sprintf("E%d", ptCounter)
		ptCounter++
		ptNames[key] = name
		rc.Points[name] = Point{X: local.X(), Y: local.Y()}
		return name
	}

	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		sectorPts := SectorPoints(ch, s)
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
				Extrusions:    saveExtrusions(s.Floor.Extrusions, ch, true),
				TerrainPoints: saveTerrainPts(s.Floor.TerrainPoints, ch, true),
				UVOffset:      Point{X: s.Floor.UVOffset.X(), Y: s.Floor.UVOffset.Y()},
				UVScale:       Point{X: s.Floor.UVScale.X(), Y: s.Floor.UVScale.Y()},
				UVRotation:    s.Floor.UVRotation,
			},
			Ceiling: RawCeiling{
				Enabled:       s.Ceiling.Enabled,
				Height:        s.Ceiling.Height,
				VertexOffsets: ceilOffsets,
				Texture:       s.Ceiling.Texture,
				Terrain:       s.Ceiling.Terrain,
				DoubleSided:   boolPtrIfFalse(s.Ceiling.DoubleSided),
				Extrusions:    saveExtrusions(s.Ceiling.Extrusions, ch, true),
				TerrainPoints: saveTerrainPts(s.Ceiling.TerrainPoints, ch, true),
				UVOffset:      Point{X: s.Ceiling.UVOffset.X(), Y: s.Ceiling.UVOffset.Y()},
				UVScale:       Point{X: s.Ceiling.UVScale.X(), Y: s.Ceiling.UVScale.Y()},
				UVRotation:    s.Ceiling.UVRotation,
			},
		}

		// Reconstruct edges from Walls and Portals in polygon order,
		// using EdgeKinds index-based matching (robust against float drift).
		nPts := len(sectorPts)
		wallIdx, portalIdx := 0, 0
		for ei := 0; ei < nPts; ei++ {
			a := sectorPts[ei]
			b := sectorPts[(ei+1)%nPts]
			fromName := assignPoint(a)
			toName := assignPoint(b)

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
					Extrusions:    saveExtrusions(w.Extrusions, ch, false),
					WallTerrain:   w.Terrain,
					TerrainPoints: saveTerrainPts(w.TerrainPoints, ch, false),
					UVOffset:      Point{X: w.UVOffset.X(), Y: w.UVOffset.Y()},
					UVScale:       Point{X: w.UVScale.X(), Y: w.UVScale.Y()},
					UVRotation:    w.UVRotation,
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

	return rc, nil
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
		UVOffset:   Point{X: p.UVOffset.X(), Y: p.UVOffset.Y()},
		UVScale:    Point{X: uvSc.X(), Y: uvSc.Y()},
		UVRotation: p.UVRotation,
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

// boolPtrIfFalse returns a *bool pointer only when v is false (non-default).
// When v is true (the default for DoubleSided), returns nil so the field is
// omitted from YAML via omitempty.
func boolPtrIfFalse(v bool) *bool {
	if !v {
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
