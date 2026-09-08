package world

// chunk_preview.go — every drawable piece of a chunk, in one list.
//
// The engine's own chunk pass (app.go) walks a chunk brush by brush, culling
// against the frustum and testing face facings against the camera. That is
// the right shape for a level being played and the wrong shape for a
// *preview* — the entity editor draws one chunk, in its own window, under a
// headlight shader that has no facing rules.
//
// So this is the flat answer to "what would I draw": no culling, no facing
// test.
//
// It lives here rather than in the editor because it is a fact about a
// Chunk, and because internal/render already has passes that each need to
// agree with the main one — a caller reaching into Brush's geometry fields by
// hand is one more place for them to drift.

import "foundry/internal/gfx"

// ChunkDrawGeometries returns every uploaded piece of ch's geometry: every
// visible brush face and its Detail texture-override submeshes.
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

	for bi := range ch.Brushes {
		b := &ch.Brushes[bi]
		for fi := range b.Faces {
			f := &b.Faces[fi]
			if f.NoDraw {
				continue
			}
			add(&f.Geometry)
			for i := range f.SubmeshGeometries {
				add(&f.SubmeshGeometries[i])
			}
		}
	}
	return out
}
