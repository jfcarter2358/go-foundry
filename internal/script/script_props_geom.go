package script

// script_props_geom.go — properties of the world's geometry and lights.
//
// Split from script_props.go purely by length: the entity and volume tables
// there and these are the same idea. The traps named in that file's header all
// live here — the inverted Visible, the derived light Direction, and the
// zero-Alpha default — so each is guarded at the point it applies.

import (
	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/world"
)

func init() {
	propTables = map[flang.Kind_]map[string]propAccess{
		flang.RefEntity:  entityProps(),
		flang.RefVolume:  volumeProps(),
		flang.RefEdge:    edgeProps(),
		flang.RefFloor:   surfaceProps(true),
		flang.RefCeiling: surfaceProps(false),
		flang.RefSector:  sectorProps(),
		flang.RefChunk:   chunkProps(),
		flang.RefLight:   lightProps(),
		flang.RefBiome:   biomeProps(),
		flang.RefSprite:  spriteProps(),
		flang.RefLevel:   levelProps(),
		flang.RefEmitter: emitterProps(),
		flang.RefScreen:  screenProps(),
		flang.RefWidget:  widgetProps(),
		flang.RefSound:   soundProps(),
		flang.RefModel:   modelProps(),
	}
}

// ---------- edges ----------

// edgeAt resolves an edge ref to whichever record currently occupies it. Both
// are returned so a caller can handle the wall-only and portal-only properties
// without resolving twice.
func edgeAt(c *refCache, r flang.RefVal) (*world.Wall, *world.Portal) {
	_, s := c.sectorFor(r)
	if s == nil {
		return nil, nil
	}
	return world.WallByEdgeID(s, r.C), world.PortalByEdgeID(s, r.C)
}

func edgeProps() map[string]propAccess {
	// Most edge properties exist on a wall and, in a different form or not at
	// all, on a portal. These helpers keep each entry to the part that differs.
	wallGet := func(fn func(w *world.Wall) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			w, _ := edgeAt(c, r)
			if w == nil {
				return nil, false
			}
			return fn(w), true
		}
	}
	wallSet := func(fn func(w *world.Wall, v flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
		return func(c *refCache, r flang.RefVal, v flang.Value) error {
			w, _ := edgeAt(c, r)
			if w == nil {
				return &scriptMissingError{r.String() + " is not currently a wall"}
			}
			return fn(w, v)
		}
	}

	return map[string]propAccess{
		"kind": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				w, p := edgeAt(c, r)
				switch {
				case w != nil:
					return flang.AtomVal("wall"), true
				case p != nil:
					return flang.AtomVal("portal"), true
				}
				return nil, false
			},
			Doc: "read-only: wall or portal. Converting an edge is an editor operation.",
		},
		"visible": {
			// Visible is stored inverted from YAML's `hidden` (level_loader.go).
			// A script sees the runtime sense, which is the one that reads right.
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				w, p := edgeAt(c, r)
				switch {
				case w != nil:
					return flang.BoolVal(w.Visible), true
				case p != nil:
					return flang.BoolVal(p.Visible), true
				}
				return nil, false
			},
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("visible", "bool", v)
				}
				w, p := edgeAt(c, r)
				switch {
				case w != nil:
					w.Visible = b
				case p != nil:
					p.Visible = b
				default:
					return &scriptMissingError{r.String()}
				}
				return nil
			},
			Dirty: true,
			Doc:   "whether the edge draws",
		},
		"alpha": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				w, p := edgeAt(c, r)
				switch {
				case w != nil:
					return flang.FloatVal(w.Alpha), true
				case p != nil:
					return flang.FloatVal(p.Alpha), true
				}
				return nil, false
			},
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("alpha", "float", v)
				}
				// 0 means fully transparent, not "unset" — the YAML loader's
				// float32Default only applies at load.
				w, p := edgeAt(c, r)
				switch {
				case w != nil:
					w.Alpha = f
				case p != nil:
					p.Alpha = f
				default:
					return &scriptMissingError{r.String()}
				}
				return nil
			},
			Doc: "0.0 fully transparent, 1.0 opaque",
		},
		"collision": {
			Get: wallGet(func(w *world.Wall) flang.Value { return flang.BoolVal(w.Collision) }),
			Set: wallSet(func(w *world.Wall, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("collision", "bool", v)
				}
				w.Collision = b
				return nil
			}),
			Dirty: true,
			Doc:   "whether the wall stops anything",
		},
		"texture": {
			Get: wallGet(func(w *world.Wall) flang.Value { return flang.StrVal(w.Texture) }),
			Set: wallSet(func(w *world.Wall, v flang.Value) error {
				s, ok := toStr(v)
				if !ok {
					return propErr("texture", "str", v)
				}
				w.Texture = s
				return nil
			}),
			Dirty: true,
			Tex:   true,
			Doc:   "texture name, resolved against textures/",
		},
		"blend_texture": {
			Get: wallGet(func(w *world.Wall) flang.Value { return flang.StrVal(w.BlendTexture) }),
			Set: wallSet(func(w *world.Wall, v flang.Value) error {
				s, ok := toStr(v)
				if !ok {
					return propErr("blend_texture", "str", v)
				}
				w.BlendTexture = s
				return nil
			}),
			Dirty: true,
			Tex:   true,
			Doc:   "second texture mixed in by blend_mask's luminance; \"\" disables blending",
		},
		"blend_mask": {
			Get: wallGet(func(w *world.Wall) flang.Value { return flang.StrVal(w.BlendMask) }),
			Set: wallSet(func(w *world.Wall, v flang.Value) error {
				s, ok := toStr(v)
				if !ok {
					return propErr("blend_mask", "str", v)
				}
				w.BlendMask = s
				return nil
			}),
			Dirty: true,
			Tex:   true,
			Doc:   "grayscale mask texture: 0 = texture, 1 = blend_texture",
		},
		"double_sided": {
			Get: wallGet(func(w *world.Wall) flang.Value { return flang.BoolVal(w.DoubleSided) }),
			Set: wallSet(func(w *world.Wall, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("double_sided", "bool", v)
				}
				w.DoubleSided = b
				return nil
			}),
			Dirty: true,
			Doc:   "render from both sides",
		},
		"pinned": {
			Get: wallGet(func(w *world.Wall) flang.Value { return flang.BoolVal(w.Pinned) }),
			Set: wallSet(func(w *world.Wall, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("pinned", "bool", v)
				}
				w.Pinned = b
				return nil
			}),
			Doc: "keep this a wall even where it is shared with another sector",
		},
		"smooth": {
			Get: wallGet(func(w *world.Wall) flang.Value { return flang.BoolVal(w.Smooth) }),
			Set: wallSet(func(w *world.Wall, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("smooth", "bool", v)
				}
				w.Smooth = b
				return nil
			}),
			Dirty: true,
			Doc:   "shade from interpolated vertex normals",
		},
		"to_sector": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				_, p := edgeAt(c, r)
				if p == nil {
					return nil, false
				}
				return flang.StrVal(p.ToSector), true
			},
			Doc: "read-only: the sector a portal connects to",
		},
	}
}

// ---------- floors and ceilings ----------

// surfaceProps builds the table for a floor or a ceiling. They differ in exactly
// one property — a floor has `hidden`, a ceiling does not — so one builder with
// a flag beats two near-identical tables.
func surfaceProps(isFloor bool) map[string]propAccess {
	// A floor and a ceiling are different types with the same field names, so
	// each accessor takes both and picks.
	type surface struct {
		f *world.Floor
		c *world.Ceiling
	}
	at := func(cache *refCache, r flang.RefVal) (surface, bool) {
		_, s := cache.sectorFor(r)
		if s == nil {
			return surface{}, false
		}
		if isFloor {
			return surface{f: &s.Floor}, true
		}
		return surface{c: &s.Ceiling}, true
	}

	getF := func(ff func(*world.Floor) flang.Value, cf func(*world.Ceiling) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(cache *refCache, r flang.RefVal) (flang.Value, bool) {
			s, ok := at(cache, r)
			if !ok {
				return nil, false
			}
			if s.f != nil {
				return ff(s.f), true
			}
			return cf(s.c), true
		}
	}
	setF := func(ff func(*world.Floor, flang.Value) error, cf func(*world.Ceiling, flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
		return func(cache *refCache, r flang.RefVal, v flang.Value) error {
			s, ok := at(cache, r)
			if !ok {
				return &scriptMissingError{r.String()}
			}
			if s.f != nil {
				return ff(s.f, v)
			}
			return cf(s.c, v)
		}
	}

	tbl := map[string]propAccess{
		"height": {
			Get: getF(
				func(f *world.Floor) flang.Value { return flang.FloatVal(f.Height) },
				func(c *world.Ceiling) flang.Value { return flang.FloatVal(c.Height) }),
			Set: setF(
				func(f *world.Floor, v flang.Value) error {
					h, ok := toFloat32(v)
					if !ok {
						return propErr("height", "float", v)
					}
					f.Height = h
					return nil
				},
				func(c *world.Ceiling, v flang.Value) error {
					h, ok := toFloat32(v)
					if !ok {
						return propErr("height", "float", v)
					}
					c.Height = h
					return nil
				}),
			Dirty: true,
			Doc:   "the surface's base Y",
		},
		"world_height": {
			// `height` is chunk-local — the authored number, and the one that
			// has to stay writable, or `height = height + 1` on a placed chunk
			// would stop round-tripping. This is its world-space companion, for
			// the comparison an author actually wants to write: a surface's Y
			// against a player's or an entity's, both of which are world-space.
			//
			// Carried through the chunk's ModelMatrix rather than by adding
			// Origin.Y, so a scaled chunk (Height is baked into vertex Y and
			// scaled with everything else) and a frame-owned one riding a
			// vehicle both answer correctly. Rotation is about Y and so does
			// not affect it — which is exactly why a single scalar is still a
			// meaningful answer here.
			Get: func(cache *refCache, r flang.RefVal) (flang.Value, bool) {
				ch, s := cache.sectorFor(r)
				if ch == nil || s == nil {
					return nil, false
				}
				h := s.Floor.Height
				if !isFloor {
					h = s.Ceiling.Height
				}
				return flang.FloatVal(ch.LocalToWorld(mgl32.Vec3{0, h, 0}).Y()), true
			},
			// No Set, which is what makes SetProp report it read-only. Two
			// writable heights would let a script set them against each other,
			// and only one of them is what the level file stores.
			Doc: "read-only: `height` carried into world space by the chunk's placement",
		},
		"texture": {
			Get: getF(
				func(f *world.Floor) flang.Value { return flang.StrVal(f.Texture) },
				func(c *world.Ceiling) flang.Value { return flang.StrVal(c.Texture) }),
			Set: setF(
				func(f *world.Floor, v flang.Value) error {
					s, ok := toStr(v)
					if !ok {
						return propErr("texture", "str", v)
					}
					f.Texture = s
					return nil
				},
				func(c *world.Ceiling, v flang.Value) error {
					s, ok := toStr(v)
					if !ok {
						return propErr("texture", "str", v)
					}
					c.Texture = s
					return nil
				}),
			Dirty: true,
			Tex:   true,
			Doc:   "texture name",
		},
		"blend_texture": {
			Get: getF(
				func(f *world.Floor) flang.Value { return flang.StrVal(f.BlendTexture) },
				func(c *world.Ceiling) flang.Value { return flang.StrVal(c.BlendTexture) }),
			Set: setF(
				func(f *world.Floor, v flang.Value) error {
					s, ok := toStr(v)
					if !ok {
						return propErr("blend_texture", "str", v)
					}
					f.BlendTexture = s
					return nil
				},
				func(c *world.Ceiling, v flang.Value) error {
					s, ok := toStr(v)
					if !ok {
						return propErr("blend_texture", "str", v)
					}
					c.BlendTexture = s
					return nil
				}),
			Dirty: true,
			Tex:   true,
			Doc:   "second texture mixed in by blend_mask's luminance; \"\" disables blending",
		},
		"blend_mask": {
			Get: getF(
				func(f *world.Floor) flang.Value { return flang.StrVal(f.BlendMask) },
				func(c *world.Ceiling) flang.Value { return flang.StrVal(c.BlendMask) }),
			Set: setF(
				func(f *world.Floor, v flang.Value) error {
					s, ok := toStr(v)
					if !ok {
						return propErr("blend_mask", "str", v)
					}
					f.BlendMask = s
					return nil
				},
				func(c *world.Ceiling, v flang.Value) error {
					s, ok := toStr(v)
					if !ok {
						return propErr("blend_mask", "str", v)
					}
					c.BlendMask = s
					return nil
				}),
			Dirty: true,
			Tex:   true,
			Doc:   "grayscale mask texture: 0 = texture, 1 = blend_texture",
		},
		"enabled": {
			Get: getF(
				func(f *world.Floor) flang.Value { return flang.BoolVal(f.Enabled) },
				func(c *world.Ceiling) flang.Value { return flang.BoolVal(c.Enabled) }),
			Set: setF(
				func(f *world.Floor, v flang.Value) error {
					b, ok := toBool(v)
					if !ok {
						return propErr("enabled", "bool", v)
					}
					f.Enabled = b
					return nil
				},
				func(c *world.Ceiling, v flang.Value) error {
					b, ok := toBool(v)
					if !ok {
						return propErr("enabled", "bool", v)
					}
					c.Enabled = b
					return nil
				}),
			Dirty: true,
			Doc:   "whether the surface exists at all",
		},
		"alpha": {
			Get: getF(
				func(f *world.Floor) flang.Value { return flang.FloatVal(f.Alpha) },
				func(c *world.Ceiling) flang.Value { return flang.FloatVal(c.Alpha) }),
			Set: setF(
				func(f *world.Floor, v flang.Value) error {
					a, ok := toFloat32(v)
					if !ok {
						return propErr("alpha", "float", v)
					}
					f.Alpha = a
					return nil
				},
				func(c *world.Ceiling, v flang.Value) error {
					a, ok := toFloat32(v)
					if !ok {
						return propErr("alpha", "float", v)
					}
					c.Alpha = a
					return nil
				}),
			Doc: "0.0 fully transparent, 1.0 opaque",
		},
		// "shaped" replaces "terrain", which used to switch a surface between a
		// flat plane and a Delaunay displacement mesh. There is only one
		// representation now — a SurfaceMesh, which a flat surface simply does
		// not carry — so there is no mode left to set, only a fact to read.
		"shaped": {
			Get: getF(
				func(f *world.Floor) flang.Value { return flang.BoolVal(!f.Mesh.IsEmpty()) },
				func(c *world.Ceiling) flang.Value { return flang.BoolVal(!c.Mesh.IsEmpty()) }),
			Doc: "read-only: true when the surface has been shaped in the editor",
		},
		"smooth": {
			Get: getF(
				func(f *world.Floor) flang.Value { return flang.BoolVal(f.Smooth) },
				func(c *world.Ceiling) flang.Value { return flang.BoolVal(c.Smooth) }),
			Set: setF(
				func(f *world.Floor, v flang.Value) error {
					b, ok := toBool(v)
					if !ok {
						return propErr("smooth", "bool", v)
					}
					f.Smooth = b
					return nil
				},
				func(c *world.Ceiling, v flang.Value) error {
					b, ok := toBool(v)
					if !ok {
						return propErr("smooth", "bool", v)
					}
					c.Smooth = b
					return nil
				}),
			Dirty: true,
			Doc:   "shade from interpolated vertex normals",
		},
	}

	// The slab: how thick the surface is and what its far side is textured
	// with (world/slab.go). Reached through one accessor rather than the
	// getF/setF pair above, because a Slab is the *same type* on a floor and on
	// a ceiling — the only part of these two structs that is.
	slabAt := func(cache *refCache, r flang.RefVal) (*world.Slab, bool) {
		sf, ok := at(cache, r)
		if !ok {
			return nil, false
		}
		if sf.f != nil {
			return &sf.f.Slab, true
		}
		return &sf.c.Slab, true
	}
	slabGet := func(fn func(*world.Slab) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(cache *refCache, r flang.RefVal) (flang.Value, bool) {
			sl, ok := slabAt(cache, r)
			if !ok {
				return nil, false
			}
			return fn(sl), true
		}
	}
	slabSet := func(fn func(*world.Slab, flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
		return func(cache *refCache, r flang.RefVal, v flang.Value) error {
			sl, ok := slabAt(cache, r)
			if !ok {
				return &scriptMissingError{r.String()}
			}
			return fn(sl, v)
		}
	}

	tbl["thickness"] = propAccess{
		Get: slabGet(func(sl *world.Slab) flang.Value { return flang.FloatVal(sl.Thickness) }),
		Set: slabSet(func(sl *world.Slab, v flang.Value) error {
			t, ok := toFloat32(v)
			if !ok {
				return propErr("thickness", "float", v)
			}
			// Clamped rather than rejected, for the same reason the panel's
			// control is: a negative thickness is a solid turned inside out,
			// and a script computing one off a moving value should end up with
			// no slab rather than with an error mid-frame.
			if t < 0 {
				t = 0
			}
			sl.Thickness = t
			return nil
		}),
		Dirty: true,
		Doc:   "how far the surface's far side sits from it: down from a floor, up from a ceiling. 0 = no slab",
	}
	tbl["slab_texture"] = propAccess{
		Get: slabGet(func(sl *world.Slab) flang.Value { return flang.StrVal(sl.Texture) }),
		Set: slabSet(func(sl *world.Slab, v flang.Value) error {
			t, ok := toStr(v)
			if !ok {
				return propErr("slab_texture", "str", v)
			}
			sl.Texture = t
			return nil
		}),
		Dirty: true,
		Tex:   true,
		Doc:   "the far side's texture; \"\" takes the surface's own",
	}
	tbl["slab_rim_texture"] = propAccess{
		Get: slabGet(func(sl *world.Slab) flang.Value { return flang.StrVal(sl.RimTexture) }),
		Set: slabSet(func(sl *world.Slab, v flang.Value) error {
			t, ok := toStr(v)
			if !ok {
				return propErr("slab_rim_texture", "str", v)
			}
			sl.RimTexture = t
			return nil
		}),
		Dirty: true,
		Tex:   true,
		Doc:   "the slab's edge band texture; \"\" takes slab_texture",
	}
	tbl["slab_shaped"] = propAccess{
		Get: slabGet(func(sl *world.Slab) flang.Value { return flang.BoolVal(sl.Shaped()) }),
		// No Set, like `shaped`: a mesh is not something a script can author,
		// and "false" would have to mean "throw the author's shape away".
		Doc: "read-only: true when the far side has been shaped in the editor, and so no longer follows the surface",
	}
	tbl["slab_hidden"] = propAccess{
		Get: slabGet(func(sl *world.Slab) flang.Value { return flang.BoolVal(sl.Hidden) }),
		Set: slabSet(func(sl *world.Slab, v flang.Value) error {
			b, ok := toBool(v)
			if !ok {
				return propErr("slab_hidden", "bool", v)
			}
			sl.Hidden = b
			return nil
		}),
		Dirty: true,
		Doc:   "stop drawing the far side, keeping the rim",
	}
	tbl["slab_rim_across_portals"] = propAccess{
		Get: slabGet(func(sl *world.Slab) flang.Value { return flang.BoolVal(sl.RimAcrossPortals) }),
		Set: slabSet(func(sl *world.Slab, v flang.Value) error {
			b, ok := toBool(v)
			if !ok {
				return propErr("slab_rim_across_portals", "bool", v)
			}
			sl.RimAcrossPortals = b
			return nil
		}),
		Dirty: true,
		Doc:   "build the rim over portal edges too, instead of skipping them",
	}

	if isFloor {
		// A floor's Hidden suppresses its texture without disabling it —
		// collision and terrain are unaffected. A ceiling has no equivalent.
		tbl["hidden"] = propAccess{
			Get: func(cache *refCache, r flang.RefVal) (flang.Value, bool) {
				_, s := cache.sectorFor(r)
				if s == nil {
					return nil, false
				}
				return flang.BoolVal(s.Floor.Hidden), true
			},
			Set: func(cache *refCache, r flang.RefVal, v flang.Value) error {
				_, s := cache.sectorFor(r)
				if s == nil {
					return &scriptMissingError{r.String()}
				}
				b, ok := toBool(v)
				if !ok {
					return propErr("hidden", "bool", v)
				}
				s.Floor.Hidden = b
				return nil
			},
			Dirty: true,
			Doc:   "hide the texture without disabling the floor",
		}
	}
	return tbl
}

// ---------- sectors and chunks ----------

// sectorProps is thin on purpose: a sector holds no flags of its own, and
// everything a script wants is on its floor, ceiling or edges.
func sectorProps() map[string]propAccess {
	return map[string]propAccess{}
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

// ---------- biome sectors ----------

func biomeProps() map[string]propAccess {
	get := func(fn func(*world.BiomeSector) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			b := c.biomeFor(r)
			if b == nil {
				return nil, false
			}
			return fn(b), true
		}
	}
	return map[string]propAccess{
		"density": {
			Get: get(func(b *world.BiomeSector) flang.Value { return flang.FloatVal(b.Brush.Density) }),
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				b := c.biomeFor(r)
				if b == nil {
					return &scriptMissingError{r.String()}
				}
				f, ok := toFloat32(v)
				if !ok {
					return propErr("density", "float", v)
				}
				b.Brush.Density = f
				b.HeightDirty, b.GroundDirty = true, true
				return nil
			},
			Doc: "scatter density",
		},
		"color": {
			Get: get(func(b *world.BiomeSector) flang.Value { return vecValue(mgl32.Vec3(b.Brush.Color)) }),
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				b := c.biomeFor(r)
				if b == nil {
					return &scriptMissingError{r.String()}
				}
				col, ok := toVec3(v)
				if !ok {
					return propErr("color", "vec", v)
				}
				b.Brush.Color = col
				return nil
			},
			Doc: "scatter tint",
		},
		"ground_texture": {
			Get: get(func(b *world.BiomeSector) flang.Value { return flang.StrVal(b.Brush.GroundTexture) }),
			Doc: "read-only: the ground patch's texture",
		},
		"water_depth": {
			Get: get(func(b *world.BiomeSector) flang.Value {
				// The resolved value, not the raw field: a sector that never
				// had a depth written stores 0, and reporting that back would
				// have `water_depth = water_depth + 1` move the surface to
				// 1 below the floor rather than 1 below where it is.
				return flang.FloatVal(world.ResolvedWaterDepth(b.Brush.WaterDepth))
			}),
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				b := c.biomeFor(r)
				if b == nil {
					return &scriptMissingError{r.String()}
				}
				f, ok := toFloat32(v)
				if !ok {
					return propErr("water_depth", "float", v)
				}
				b.Brush.WaterDepth = world.NormalizeWaterDepth(f)
				// The surface's height is its mesh's placement, so the plane
				// has to be moved — SyncBiomeWaterSurfaces watches this flag
				// and runs every frame. Not GroundDirty: a script raising a
				// flood writes this on every one of those frames, and none of
				// them reshapes the polygon or its lakebed.
				b.WaterLevelDirty = true
				return nil
			},
			Doc: "Water only: how far below the floor under the sector the surface sits (negative lifts it above)",
		},
		"water_level": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				ch, b := c.biomeOwnerFor(r)
				w := waterSurfaceFor(ch, b)
				if w == nil {
					return nil, false
				}
				return flang.FloatVal(ch.LocalToWorld(w.Position).Y()), true
			},
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				ch, b := c.biomeOwnerFor(r)
				w := waterSurfaceFor(ch, b)
				if w == nil {
					return &scriptMissingError{r.String() + " has no water surface"}
				}
				f, ok := toFloat32(v)
				if !ok {
					return propErr("water_level", "float", v)
				}
				// The floor the depth is measured from, recovered from the
				// plane the last rebuild produced rather than re-sampled: the
				// two agree by construction (Position.Y = floor - depth), and
				// this keeps the property from needing the biome package's
				// floor lookup.
				floorY := w.Position.Y() + world.ResolvedWaterDepth(b.Brush.WaterDepth)
				// Through the chunk's placement, so the level written is the
				// world one the caller asked for. The plane's own X/Z go along
				// for the ride so a rotated or scaled chunk converts exactly.
				want := ch.LocalToWorld(w.Position)
				want[1] = f
				b.Brush.WaterDepth = world.NormalizeWaterDepth(floorY - ch.WorldToLocal(want).Y())
				b.WaterLevelDirty = true
				return nil
			},
			Doc: "Water only: the surface's height in world space; unavailable until the plane has been built",
		},
		"lakebed": {
			Get: get(func(b *world.BiomeSector) flang.Value { return flang.BoolVal(!b.Brush.HideLakebed) }),
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				b := c.biomeFor(r)
				if b == nil {
					return &scriptMissingError{r.String()}
				}
				on, ok := toBool(v)
				if !ok {
					return propErr("lakebed", "bool", v)
				}
				// Stored inverted so the zero value is "shown" (see
				// BiomeBrush.HideLakebed); no rebuild — RenderBiomeGround
				// reads the flag per frame and drops the patch's draw.
				b.Brush.HideLakebed = !on
				return nil
			},
			Doc: "Water only: whether the ground patch under the surface is drawn",
		},
	}
}

// waterSurfaceFor returns the companion water plane b's sector produced, or
// nil — before the first SyncBiomeWaterSurfaces after the sector was painted,
// for a sector of any other kind, and for a chunk switched off (which releases
// its planes; see world/chunk_enable.go).
func waterSurfaceFor(ch *world.Chunk, b *world.BiomeSector) *world.WaterSurface {
	if ch == nil || b == nil || b.Brush.Kind != world.BiomeWater {
		return nil
	}
	for i := range ch.WaterSurfaces {
		if ch.WaterSurfaces[i].ID == b.ID {
			return &ch.WaterSurfaces[i]
		}
	}
	return nil
}
