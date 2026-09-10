package model

// model_uv_atlas.go — flattening a whole model into one texture's worth of UVs.
//
// ModelPlanarUnwrap (model_uv.go) is a per-face repair tool: every face gets
// its own projection, and "fit to 0..1" makes every face cover the entire
// image. That is the right answer for re-aiming a tiling material at one face
// and exactly the wrong one for *painting* a model, because no part of the
// image belongs to any particular face — every face is everywhere, so a brush
// stroke lands on all of them at once.
//
// An atlas unwrap gives each face its own region of one shared image instead.
// The cutting, flattening and packing are geom.AtlasUnwrap (geom/uv_atlas.go),
// which a level's brushes use as well — see there for the properties the layout
// keeps and why. What is left here is the part that is about a model: which of
// its faces take part, and writing the answer back onto them.

import "foundry/internal/geom"

// AtlasUnwrapOptions tunes how the mesh is cut and packed. The zero value is
// usable: every field falls back to a default.
type AtlasUnwrapOptions = geom.AtlasOptions

// AtlasUnwrap rewrites the given faces' UVs so that together they tile one
// texture without overlapping, and returns the number of islands the mesh was
// cut into.
//
// Faces with fewer than three vertices are skipped: they have no plane to
// project onto and no area to give texels to. If that leaves nothing, the
// document is untouched and an error is returned — a silent no-op here would
// look exactly like a successful unwrap onto a blank texture.
func (m *ModelDoc) AtlasUnwrap(faces []int, opts AtlasUnwrapOptions) (int, error) {
	// Sorted face indices, so the layout is the same whatever order the
	// selection was built in.
	list := sortedKeys(m.faceSet(faces))
	loops := make([][]int, len(list))
	for i, fi := range list {
		loops[i] = m.Faces[fi].Verts
	}

	layout, err := geom.AtlasUnwrap(m.Verts, loops, opts)
	if err != nil {
		return 0, err
	}
	for i, fi := range list {
		if layout.UVs[i] != nil {
			m.Faces[fi].UVs = layout.UVs[i]
		}
	}
	m.Touch()
	return layout.Islands, nil
}
