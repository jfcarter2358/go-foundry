package script

// script_props_geom.go — properties of the world's geometry and lights.
//
// Split from script_props.go purely by length: the entity and volume tables
// there and these are the same idea. The traps named in that file's header all
// live here — the inverted Visible, the derived light Direction, and the
// zero-Alpha default — so each is guarded at the point it applies.

import (
	"foundry/internal/flang"
	"foundry/internal/world"
)

func init() {
	propTables = map[flang.Kind_]map[string]propAccess{
		flang.RefEntity:  entityProps(),
		flang.RefVolume:  volumeProps(),
		flang.RefBrush:   brushProps(),
		flang.RefChunk:   chunkProps(),
		flang.RefLight:   lightProps(),
		flang.RefSprite:  spriteProps(),
		flang.RefLevel:   levelProps(),
		flang.RefEmitter: emitterProps(),
		flang.RefScreen:  screenProps(),
		flang.RefWidget:  widgetProps(),
		flang.RefSound:   soundProps(),
		flang.RefModel:   modelProps(),
	}
}

// ---------- brushes ----------

// brushProps is thin on purpose: a brush's textures, UVs and shading live on
// its individual faces, and faces have no address of their own yet (see
// SCRIPTING.md's brush section once Phase 8 gives them one — RefFace is not
// here). All a script can currently ask a brush itself is whether it is
// solid.
func brushProps() map[string]propAccess {
	return map[string]propAccess{
		"solid": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				b := c.brushFor(r)
				if b == nil {
					return nil, false
				}
				return flang.BoolVal(b.Solid), true
			},
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				bo, ok := toBool(v)
				if !ok {
					return propErr("solid", "bool", v)
				}
				b := c.brushFor(r)
				if b == nil {
					return &scriptMissingError{r.String()}
				}
				b.Solid = bo
				return nil
			},
			Dirty: true,
			Doc:   "whether the brush blocks movement",
		},
	}
}

func chunkProps() map[string]propAccess {
	get := func(fn func(*world.Chunk) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			ch := c.chunkFor(r)
			if ch == nil {
				return nil, false
			}
			return fn(ch), true
		}
	}
	// The cache is handed to the setter as well as the chunk: a write that
	// moves a chunk has to re-bucket the level's spatial hash, and the cache is
	// where the setter can reach the level from.
	set := func(fn func(c *refCache, ch *world.Chunk, v flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
		return func(c *refCache, r flang.RefVal, v flang.Value) error {
			ch := c.chunkFor(r)
			if ch == nil {
				return &scriptMissingError{r.String()}
			}
			return fn(c, ch, v)
		}
	}
	return map[string]propAccess{
		"enabled": {
			Get: get(func(ch *world.Chunk) flang.Value { return flang.BoolVal(ch.IsEnabled()) }),
			// Only the flag is written here. Releasing or rebuilding the
			// chunk's buffers is gl.* work, which a script tick is not allowed
			// to do — the frame loop's SyncChunkGeometry picks the change up
			// between frames and rebuilds the spatial hash with it. See
			// world/chunk_enable.go.
			Set: set(func(_ *refCache, ch *world.Chunk, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("enabled", "bool", v)
				}
				ch.Disabled = !b
				return nil
			}),
			Doc: "a disabled chunk is not drawn, not collided with, and its geometry is unloaded",
		},
		"origin": {
			Get: get(func(ch *world.Chunk) flang.Value { return vecValue(ch.Origin) }),
			// Writing it is the whole of moving a chunk. Points, sector
			// heights, biome paint and every other per-chunk coordinate are
			// chunk-local and never baked with Origin (see world.Chunk), so
			// they ride along through ModelMatrix with no geometry to rebuild
			// — which is exactly what the editor's own move does. Any entity
			// with AttachedChunk naming this chunk rides along too, by the
			// same distance (world.CarryAttachedEntities).
			//
			// What does not ride along is the spatial hash: its keys are world
			// cells derived from ModelMatrix, so a moved chunk drops out of
			// every world-space lookup until it is re-bucketed. Done here
			// rather than deferred like `enabled` is, because re-bucketing is
			// pure CPU (~40us on a 56-sector level, the same call the editor
			// makes on every frame of a drag) where releasing vertex buffers is
			// gl.* work. Deferring it would leave the rest of this tick's
			// location verbs answering from the chunk's old footprint.
			Set: set(func(c *refCache, ch *world.Chunk, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("origin", "vec", v)
				}
				if p == ch.Origin {
					// A script animating one chunk writes this every frame;
					// one holding it still should not pay for a rehash.
					return nil
				}
				delta := p.Sub(ch.Origin)
				ch.Origin = p
				world.CarryAttachedEntities(c.lvl, ch, delta)
				world.RebuildSpatialHash(c.lvl)
				return nil
			}),
			Doc: "this chunk's placement: writing it moves the chunk and everything authored in it",
		},
		"velocity": {
			Get: get(func(ch *world.Chunk) flang.Value { return vecValue(ch.Velocity) }),
			// No re-bucketing here, unlike `origin`: writing a rate moves
			// nothing this instant. The move it produces happens in
			// world.AdvanceChunks at the top of the next tick, which re-buckets
			// the hash itself once every mover has been integrated.
			Set: set(func(_ *refCache, ch *world.Chunk, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("velocity", "vec", v)
				}
				ch.Velocity = p
				return nil
			}),
			Doc: "units per second added to this chunk's origin during play; the engine keeps it moving",
		},
		"rotation": {
			Get: get(func(ch *world.Chunk) flang.Value { return flang.FloatVal(ch.Rotation) }),
			Doc: "read-only: this chunk's Y rotation, in radians",
		},
		"scale": {
			Get: get(func(ch *world.Chunk) flang.Value { return flang.FloatVal(ch.Scale) }),
			Doc: "read-only: this chunk's uniform scale",
		},
	}
}

// ---------- lights ----------

func lightProps() map[string]propAccess {
	return map[string]propAccess{
		"color": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				d, p := c.lightFor(r)
				switch {
				case d != nil:
					return vecValue(d.Color), true
				case p != nil:
					return vecValue(p.Color), true
				}
				return nil, false
			},
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				col, ok := toVec3(v)
				if !ok {
					return propErr("color", "vec", v)
				}
				d, p := c.lightFor(r)
				switch {
				case d != nil:
					d.Color = col
				case p != nil:
					p.Color = col
				default:
					return &scriptMissingError{r.String()}
				}
				return nil
			},
			Doc: "RGB, as a vec",
		},
		"brightness": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				d, p := c.lightFor(r)
				switch {
				case d != nil:
					return flang.FloatVal(d.Brightness), true
				case p != nil:
					return flang.FloatVal(p.Brightness), true
				}
				return nil, false
			},
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("brightness", "float", v)
				}
				d, p := c.lightFor(r)
				switch {
				case d != nil:
					d.Brightness = f
				case p != nil:
					p.Brightness = f
				default:
					return &scriptMissingError{r.String()}
				}
				return nil
			},
			Doc: "intensity",
		},
		"position": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				_, p := c.lightFor(r)
				if p == nil {
					return nil, false
				}
				return vecValue(p.Position), true
			},
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				_, p := c.lightFor(r)
				if p == nil {
					return &scriptMissingError{r.String() + " is not a point light"}
				}
				pos, ok := toVec3(v)
				if !ok {
					return propErr("position", "vec", v)
				}
				p.Position = pos
				return nil
			},
			Doc: "point lights only",
		},
		"radius": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				_, p := c.lightFor(r)
				if p == nil {
					return nil, false
				}
				return flang.FloatVal(p.Radius), true
			},
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				_, p := c.lightFor(r)
				if p == nil {
					return &scriptMissingError{r.String() + " is not a point light"}
				}
				f, ok := toFloat32(v)
				if !ok {
					return propErr("radius", "float", v)
				}
				p.Radius = f
				return nil
			},
			Doc: "point lights only: falloff radius",
		},
		// Yaw and pitch, never Direction. Writing the vector directly is how a
		// light reaches {0,0,0}, whose Normalize() is NaN — which poisons the
		// direction uniform and the shadow matrix permanently (light.go).
		"yaw": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				d, _ := c.lightFor(r)
				if d == nil {
					return nil, false
				}
				return flang.FloatVal(d.Yaw), true
			},
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				d, _ := c.lightFor(r)
				if d == nil {
					return &scriptMissingError{r.String() + " is not a directional light"}
				}
				f, ok := toFloat32(v)
				if !ok {
					return propErr("yaw", "float", v)
				}
				d.Yaw = f
				d.SyncDirection()
				return nil
			},
			Doc: "directional lights only: degrees about +Y",
		},
		"pitch": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				d, _ := c.lightFor(r)
				if d == nil {
					return nil, false
				}
				return flang.FloatVal(d.Pitch), true
			},
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				d, _ := c.lightFor(r)
				if d == nil {
					return &scriptMissingError{r.String() + " is not a directional light"}
				}
				f, ok := toFloat32(v)
				if !ok {
					return propErr("pitch", "float", v)
				}
				d.Pitch = f
				d.SyncDirection()
				return nil
			},
			Doc: "directional lights only: degrees of elevation",
		},
		"cast_shadows": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				d, p := c.lightFor(r)
				switch {
				case d != nil:
					return flang.BoolVal(d.CastShadows), true
				case p != nil:
					return flang.BoolVal(p.CastShadows), true
				}
				return nil, false
			},
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("cast_shadows", "bool", v)
				}
				d, p := c.lightFor(r)
				switch {
				case d != nil:
					d.CastShadows = b
				case p != nil:
					p.CastShadows = b
				default:
					return &scriptMissingError{r.String()}
				}
				return nil
			},
			Doc: "whether this light casts shadows",
		},
	}
}
