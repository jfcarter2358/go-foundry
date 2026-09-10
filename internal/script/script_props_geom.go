package script

// script_props_geom.go — properties of the world's geometry and lights.
//
// Split from script_props.go purely by length: the entity and volume tables
// there and these are the same idea. The traps named in that file's header all
// live here — the inverted Visible, the derived light Direction, and the
// zero-Alpha default — so each is guarded at the point it applies.

import (
	"fmt"

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

// brushProps is what a script can ask a brush about itself. Its textures, UVs
// and shading live on its individual faces, and faces have no address of their
// own yet (RefFace is not here), so what is left is the brush as an object:
// whether it is solid, and — for one marked dynamic — where it is.
//
// The placement properties are gated on Dynamic rather than allowed on any
// brush. A static brush's corners are baked into the compiled level's BSP and
// its faces have been clipped against their neighbours, so moving one at run
// time would move a shape the compiled world no longer agrees exists. The
// toggle is what tells the compiler to leave a brush out of that bake, so it is
// also what makes writing a position meaningful; writing one without it is an
// error the author can see rather than a move that silently works in the editor
// and does nothing in the shipped game. See world/brush_dynamic.go.
func brushProps() map[string]propAccess {
	// A brush's verts are chunk-local. What a script reads and writes is that
	// carried out into the space the chunk is PLACED in — which for a level's
	// own geometry is the world, the space @player.prop.position and every
	// volume's centre are in, and for a brush an ENTITY carries is that
	// entity's own.
	//
	// The entity case is the one that had to be chosen deliberately, and local
	// is the only reading that works. A door on a ship is authored, drawn and
	// collided relative to the ship; a script closing it means "slide two
	// metres to port", not "slide two metres along world −X", and the two stop
	// agreeing the moment the ship turns. Local also composes the way an author
	// expects — the ship's own motion carries the door along, and the velocity
	// written here is on top of it — which is exactly what makes it possible to
	// write a lift inside a moving hull at all.
	//
	// For every chunk that is not frame-owned the two spaces are the same
	// matrix, so nothing about a level's own brushes changed. See
	// Chunk.LocalToPlacement.
	getOwner := func(fn func(*world.Chunk, *world.Brush) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			ch, b := c.brushOwnerFor(r)
			if b == nil {
				return nil, false
			}
			return fn(ch, b), true
		}
	}
	// setDynamic resolves the brush, refuses the write unless it is dynamic,
	// and hands the setter a level it can re-bucket through — the same
	// arrangement chunkProps makes for `origin`, and for the same reason: a
	// brush that moved is in different spatial-hash cells than it was.
	setDynamic := func(name string, fn func(c *refCache, ch *world.Chunk, b *world.Brush, v flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
		return func(c *refCache, r flang.RefVal, v flang.Value) error {
			ch, b := c.brushOwnerFor(r)
			if b == nil {
				return &scriptMissingError{r.String()}
			}
			if !b.Dynamic {
				return fmt.Errorf("%s: cannot write %s on a brush that is not dynamic; "+
					"tick Dynamic in its properties panel first", r.String(), name)
			}
			return fn(c, ch, b, v)
		}
	}
	// rebucket is what every placement write ends with. A moved brush grows or
	// shrinks its chunk's bounds, and the spatial hash keys off those, so a
	// brush that slid out of its chunk's old footprint would stop being found
	// by every world-space lookup — collision included, which would make a
	// closing door pass straight through the player it was meant to crush.
	rebucket := func(c *refCache, ch *world.Chunk) {
		ch.RecomputeBounds()
		world.RebuildSpatialHash(c.lvl)
	}
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
		"dynamic": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				b := c.brushFor(r)
				if b == nil {
					return nil, false
				}
				return flang.BoolVal(b.Dynamic), true
			},
			Doc: "read-only: whether this brush can be moved by a script (authored, not switchable)",
		},
		"position": {
			Get: getOwner(func(ch *world.Chunk, b *world.Brush) flang.Value {
				return vecValue(ch.LocalToPlacement(b.Position()))
			}),
			Set: setDynamic("position", func(c *refCache, ch *world.Chunk, b *world.Brush, v flang.Value) error {
				p, ok := toVec3(v)
				if !ok {
					return propErr("position", "vec", v)
				}
				want := ch.PlacementToLocal(p)
				if want == b.Position() {
					// A script animating one door writes this every frame; one
					// holding it still should not pay for a rehash.
					return nil
				}
				b.MoveTo(want)
				rebucket(c, ch)
				return nil
			}),
			Dirty: true,
			Doc:   "the brush's centre, in the space its chunk sits in — world space for a level's own geometry, the entity's own for a brush an entity carries; writing it moves the brush and its collision together",
		},
		"rotation": {
			Get: getOwner(func(_ *world.Chunk, b *world.Brush) flang.Value {
				return vecValue(b.Rotation)
			}),
			Set: setDynamic("rotation", func(c *refCache, ch *world.Chunk, b *world.Brush, v flang.Value) error {
				rot, ok := toVec3(v)
				if !ok {
					return propErr("rotation", "vec", v)
				}
				if rot == b.Rotation {
					return nil
				}
				b.Rotation = rot
				rebucket(c, ch)
				return nil
			}),
			Dirty: true,
			Doc:   "euler angles in radians, turning the brush about its own centre",
		},
		"velocity": {
			Get: getOwner(func(ch *world.Chunk, b *world.Brush) flang.Value {
				return vecValue(ch.LocalToPlacementDirection(b.Velocity))
			}),
			// No re-bucketing here, unlike `position`: writing a rate moves
			// nothing this instant. The move it produces happens in
			// world.AdvanceBrushes at the top of the next tick, which
			// re-buckets the hash itself once every mover has been integrated.
			Set: setDynamic("velocity", func(c *refCache, ch *world.Chunk, b *world.Brush, v flang.Value) error {
				vel, ok := toVec3(v)
				if !ok {
					return propErr("velocity", "vec", v)
				}
				b.Velocity = ch.PlacementToLocalDirection(vel)
				return nil
			}),
			Dirty: true,
			Doc:   "units per second added to this brush's position during play, in the same space `position` is in; the engine keeps it moving",
		},
		"angular_velocity": {
			// Read and written raw, with no chunk conversion — unlike
			// `velocity`, which is world-space because a script aiming a move
			// thinks in world directions. This is the rate of change of
			// `rotation`, and `rotation` is the brush's own Euler angles about
			// its own pivot, so the two have to be in the same basis or a
			// script could not integrate one into the other by hand.
			Get: getOwner(func(_ *world.Chunk, b *world.Brush) flang.Value {
				return vecValue(b.AngularVelocity)
			}),
			// No re-bucketing, for the same reason `velocity` does none: a
			// rate turns nothing this instant. world.AdvanceBrushes applies it
			// at the top of the next tick and re-buckets the hash itself.
			Set: setDynamic("angular_velocity", func(_ *refCache, _ *world.Chunk, b *world.Brush, v flang.Value) error {
				av, ok := toVec3(v)
				if !ok {
					return propErr("angular_velocity", "vec", v)
				}
				b.AngularVelocity = av
				return nil
			}),
			Dirty: true,
			Doc:   "radians per second added to this brush's rotation during play; the engine keeps it turning",
		},
		"scale": {
			// Raw, like `rotation`: a multiplier on the brush's own authored
			// size about its own centre, so there is no space to convert into.
			Get: getOwner(func(_ *world.Chunk, b *world.Brush) flang.Value {
				return vecValue(b.EffectiveScale())
			}),
			Set: setDynamic("scale", func(c *refCache, ch *world.Chunk, b *world.Brush, v flang.Value) error {
				s, ok := toVec3(v)
				if !ok {
					return propErr("scale", "vec", v)
				}
				// Refused rather than clamped: a zero axis collapses the solid
				// and a negative one turns it inside out, and neither is a
				// value an author means to get a slightly different version of.
				if s.X() <= 0 || s.Y() <= 0 || s.Z() <= 0 {
					return fmt.Errorf("scale: every axis must be greater than zero, got <%g, %g, %g>", s.X(), s.Y(), s.Z())
				}
				s = world.ClampBrushScale(s)
				if s == b.EffectiveScale() {
					return nil
				}
				b.Scale = s
				rebucket(c, ch)
				return nil
			}),
			Dirty: true,
			Doc:   "per-axis size multiplier on the brush as authored, growing about its own centre; <1, 1, 1> is its authored size",
		},
		"waypoints": waypointsProp(),
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
		"waypoints": waypointsProp(),
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
