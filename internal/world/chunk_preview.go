package world

// chunk_preview.go — every drawable piece of a chunk, in one list.
//
// The engine's own chunk pass (app.go) walks a chunk sector by sector, culling
// against the frustum, skipping displaced faces that the tessellation pass will
// draw instead, and testing wall facings against the camera. That is the right
// shape for a level being played and the wrong shape for a *preview* — the
// entity editor draws one chunk, in its own window, under a headlight shader
// that has no tessellation stage and no facing rules.
//
// So this is the flat answer to "what would I draw": no culling, no detail
// substitution, no portal graph. A displaced face is drawn as its undisplaced
// mesh, which is the same thing the 3D editor's wireframe shows and is honest
// about where the surface is.
//
// It lives here rather than in the editor because it is a fact about a Chunk,
// and because internal/render already has three passes that each need to agree
// with the main one — a fourth caller reaching into Sector's geometry fields by
// hand is one more place for them to drift.

import "foundry/internal/gfx"

// ChunkDrawGeometries returns every uploaded piece of ch's geometry: floors,
// ceilings, their surface-mesh submeshes and slabs, skirts, walls and portal
// gap fills.
//
// Pointers into the chunk, not copies — a Geometry carries live GL handles, and
// a copy of one is a second struct pointing at buffers it does not own.
func ChunkDrawGeometries(ch *Chunk) []*gfx.Geometry {
	if ch == nil {
		return nil
	}
	var out []*gfx.Geometry
	add := func(g *gfx.Geometry) {
		if g != nil && len(g.Vertices) > 0 {
			out = append(out, g)
		}
	}

	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if SkirtDrawable(s) {
			add(&s.Floor.SkirtGeometry)
		}
		if s.Floor.Enabled && !s.Floor.Hidden {
			add(&s.Floor.Geometry)
			for i := range s.Floor.SubmeshGeometries {
				add(&s.Floor.SubmeshGeometries[i])
			}
		}
		if s.Ceiling.Enabled {
			add(&s.Ceiling.Geometry)
			for i := range s.Ceiling.SubmeshGeometries {
				add(&s.Ceiling.SubmeshGeometries[i])
			}
		}
		addSlab(add, &s.Floor.Slab)
		addSlab(add, &s.Ceiling.Slab)
		for wi := range s.Walls {
			w := &s.Walls[wi]
			if !w.Visible {
				continue
			}
			add(&w.Geometry)
			for i := range w.SubmeshGeometries {
				add(&w.SubmeshGeometries[i])
			}
		}
		for pi := range s.Portals {
			p := &s.Portals[pi]
			if !p.Visible || !p.DrawGap {
				continue
			}
			add(&p.FloorGap)
			add(&p.CeilGap)
		}
	}
	for wi := range ch.WaterSurfaces {
		add(&ch.WaterSurfaces[wi].Geometry)
	}
	return out
}

// addSlab feeds one surface's slab through the collector: the far face and its
// per-texture submeshes unless the slab is Hidden, and the rim either way —
// which is the same split the draw pass makes (see slab.go).
func addSlab(add func(*gfx.Geometry), sl *Slab) {
	if !sl.Hidden {
		add(&sl.Geometry)
		for i := range sl.SubmeshGeometries {
			add(&sl.SubmeshGeometries[i])
		}
	}
	add(&sl.RimGeometry)
}
