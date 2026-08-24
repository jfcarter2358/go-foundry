package main

import (
	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// Portal is a transparent, non-collidable edge connecting two sectors.
// When DrawGap is true, gap-fill walls are generated at load time to cover
// any height difference between this sector and the adjacent one.
type Portal struct {
	// ID is the portal's stable identity, shared with the Wall it would become
	// if converted back — identity belongs to the edge, not to the record
	// currently occupying it. See sector_edge_id.go.
	ID          string
	ScriptData  // tags, attributes and the attached script
	StartPoint  mgl32.Vec2
	EndPoint    mgl32.Vec2
	ToSector    string
	DrawGap     bool         // fill height-difference gaps with wall geometry
	GapTexture  string       // texture for gap walls (falls back to sector floor texture)
	Visible     bool         // if false, gap-fill geometry is not drawn (default true)
	DoubleSided bool         // if true, gap geometry is rendered from both sides (default true)
	Alpha       float32      // 0.0 = fully transparent, 1.0 = fully opaque
	FloorGap    gfx.Geometry // riser wall from lower floor up to higher floor
	CeilGap     gfx.Geometry // filler wall from lower ceiling up to higher ceiling
}
