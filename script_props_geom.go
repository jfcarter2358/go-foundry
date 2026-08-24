package main

// script_props_geom.go — properties of the world's geometry and lights.
//
// Split from script_props.go purely by length: the entity and volume tables
// there and these are the same idea. The traps named in that file's header all
// live here — the inverted Visible, the derived light Direction, and the
// zero-Alpha default — so each is guarded at the point it applies.

import (
	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
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
	}
}

// ---------- edges ----------

// edgeAt resolves an edge ref to whichever record currently occupies it. Both
// are returned so a caller can handle the wall-only and portal-only properties
// without resolving twice.
func edgeAt(c *refCache, r flang.RefVal) (*Wall, *Portal) {
	_, s := c.sectorFor(r)
	if s == nil {
		return nil, nil
	}
	return wallByEdgeID(s, r.C), portalByEdgeID(s, r.C)
}

func edgeProps() map[string]propAccess {
	// Most edge properties exist on a wall and, in a different form or not at
	// all, on a portal. These helpers keep each entry to the part that differs.
	wallGet := func(fn func(w *Wall) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			w, _ := edgeAt(c, r)
			if w == nil {
				return nil, false
			}
			return fn(w), true
		}
	}
	wallSet := func(fn func(w *Wall, v flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
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
			Get: wallGet(func(w *Wall) flang.Value { return flang.BoolVal(w.Collision) }),
			Set: wallSet(func(w *Wall, v flang.Value) error {
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
			Get: wallGet(func(w *Wall) flang.Value { return flang.StrVal(w.Texture) }),
			Set: wallSet(func(w *Wall, v flang.Value) error {
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
		"double_sided": {
			Get: wallGet(func(w *Wall) flang.Value { return flang.BoolVal(w.DoubleSided) }),
			Set: wallSet(func(w *Wall, v flang.Value) error {
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
			Get: wallGet(func(w *Wall) flang.Value { return flang.BoolVal(w.Pinned) }),
			Set: wallSet(func(w *Wall, v flang.Value) error {
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
			Get: wallGet(func(w *Wall) flang.Value { return flang.BoolVal(w.Smooth) }),
			Set: wallSet(func(w *Wall, v flang.Value) error {
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
		f *Floor
		c *Ceiling
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

	getF := func(ff func(*Floor) flang.Value, cf func(*Ceiling) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
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
	setF := func(ff func(*Floor, flang.Value) error, cf func(*Ceiling, flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
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
				func(f *Floor) flang.Value { return flang.FloatVal(f.Height) },
				func(c *Ceiling) flang.Value { return flang.FloatVal(c.Height) }),
			Set: setF(
				func(f *Floor, v flang.Value) error {
					h, ok := toFloat32(v)
					if !ok {
						return propErr("height", "float", v)
					}
					f.Height = h
					return nil
				},
				func(c *Ceiling, v flang.Value) error {
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
		"texture": {
			Get: getF(
				func(f *Floor) flang.Value { return flang.StrVal(f.Texture) },
				func(c *Ceiling) flang.Value { return flang.StrVal(c.Texture) }),
			Set: setF(
				func(f *Floor, v flang.Value) error {
					s, ok := toStr(v)
					if !ok {
						return propErr("texture", "str", v)
					}
					f.Texture = s
					return nil
				},
				func(c *Ceiling, v flang.Value) error {
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
		"enabled": {
			Get: getF(
				func(f *Floor) flang.Value { return flang.BoolVal(f.Enabled) },
				func(c *Ceiling) flang.Value { return flang.BoolVal(c.Enabled) }),
			Set: setF(
				func(f *Floor, v flang.Value) error {
					b, ok := toBool(v)
					if !ok {
						return propErr("enabled", "bool", v)
					}
					f.Enabled = b
					return nil
				},
				func(c *Ceiling, v flang.Value) error {
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
				func(f *Floor) flang.Value { return flang.FloatVal(f.Alpha) },
				func(c *Ceiling) flang.Value { return flang.FloatVal(c.Alpha) }),
			Set: setF(
				func(f *Floor, v flang.Value) error {
					a, ok := toFloat32(v)
					if !ok {
						return propErr("alpha", "float", v)
					}
					f.Alpha = a
					return nil
				},
				func(c *Ceiling, v flang.Value) error {
					a, ok := toFloat32(v)
					if !ok {
						return propErr("alpha", "float", v)
					}
					c.Alpha = a
					return nil
				}),
			Doc: "0.0 fully transparent, 1.0 opaque",
		},
		"terrain": {
			Get: getF(
				func(f *Floor) flang.Value { return flang.BoolVal(f.Terrain) },
				func(c *Ceiling) flang.Value { return flang.BoolVal(c.Terrain) }),
			Set: setF(
				func(f *Floor, v flang.Value) error {
					b, ok := toBool(v)
					if !ok {
						return propErr("terrain", "bool", v)
					}
					f.Terrain = b
					return nil
				},
				func(c *Ceiling, v flang.Value) error {
					b, ok := toBool(v)
					if !ok {
						return propErr("terrain", "bool", v)
					}
					c.Terrain = b
					return nil
				}),
			Dirty: true,
			Doc:   "use the terrain displacement mesh",
		},
		"smooth": {
			Get: getF(
				func(f *Floor) flang.Value { return flang.BoolVal(f.Smooth) },
				func(c *Ceiling) flang.Value { return flang.BoolVal(c.Smooth) }),
			Set: setF(
				func(f *Floor, v flang.Value) error {
					b, ok := toBool(v)
					if !ok {
						return propErr("smooth", "bool", v)
					}
					f.Smooth = b
					return nil
				},
				func(c *Ceiling, v flang.Value) error {
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
	get := func(fn func(*Chunk) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			ch := c.chunkFor(r)
			if ch == nil {
				return nil, false
			}
			return fn(ch), true
		}
	}
	return map[string]propAccess{
		"origin": {
			Get: get(func(ch *Chunk) flang.Value { return vecValue(ch.Origin) }),
			Doc: "read-only: the offset baked into this chunk's points at load",
		},
		"rotation": {
			Get: get(func(ch *Chunk) flang.Value { return flang.FloatVal(ch.Rotation) }),
			Doc: "read-only: Y rotation baked in at load",
		},
		"scale": {
			Get: get(func(ch *Chunk) flang.Value { return flang.FloatVal(ch.Scale) }),
			Doc: "read-only: uniform XZ scale baked in at load",
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
	get := func(fn func(*BiomeSector) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
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
			Get: get(func(b *BiomeSector) flang.Value { return flang.FloatVal(b.Brush.Density) }),
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
			Get: get(func(b *BiomeSector) flang.Value { return vecValue(mgl32.Vec3(b.Brush.Color)) }),
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
			Get: get(func(b *BiomeSector) flang.Value { return flang.StrVal(b.Brush.GroundTexture) }),
			Doc: "read-only: the ground patch's texture",
		},
	}
}
