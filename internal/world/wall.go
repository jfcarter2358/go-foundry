package world

import (
	"foundry/internal/gfx"
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// ApplyUVTransform transforms the UV coordinates of a vertex slice in-place.
// Vertices are [X,Y,Z,U,V]. Applies scale around (0,0), then rotation, then offset.
func ApplyUVTransform(vertices [][]float32, offset mgl32.Vec2, scale mgl32.Vec2, rotation float32) {
	if scale == (mgl32.Vec2{}) {
		scale = mgl32.Vec2{1, 1}
	}
	if offset == (mgl32.Vec2{}) && scale == (mgl32.Vec2{1, 1}) && rotation == 0 {
		return
	}
	cos := float32(math.Cos(float64(rotation)))
	sin := float32(math.Sin(float64(rotation)))
	for i := range vertices {
		u := vertices[i][3] * scale.X()
		v := vertices[i][4] * scale.Y()
		ru := u*cos - v*sin
		rv := u*sin + v*cos
		vertices[i][3] = ru + offset.X()
		vertices[i][4] = rv + offset.Y()
	}
}

// Wall is a rendered, optionally-collidable edge between two 2D points.
type Wall struct {
	// ID is the wall's stable identity — how a script addresses it
	// (`@sector.edge.<id>`). It belongs to the polygon *edge* rather than to
	// this record: converting the edge to a portal carries the ID across, so
	// the address survives the conversion. See sector_edge_id.go.
	ID                  string
	ScriptData          // tags, attributes and the attached script
	StartPoint          mgl32.Vec2
	EndPoint            mgl32.Vec2
	Texture             string
	BlendTexture        string // optional second texture, mixed in by BlendMask's luminance
	BlendMask           string // grayscale mask texture; 0 = Texture, 1 = BlendTexture
	Collision           bool
	Visible             bool    // if false, wall geometry is not drawn (default true)
	Pinned              bool    // if true, InferPortals will not convert this wall to a portal
	DoubleSided         bool    // if true, wall is rendered from both sides (default true)
	Terrain             bool    // if true, use terrain displacement mesh
	Alpha               float32 // 0.0 = fully transparent, 1.0 = fully opaque
	Extrusions          []Extrusion
	TerrainPoints       []TerrainPoint
	UVOffset            mgl32.Vec2 // UV translation
	UVScale             mgl32.Vec2 // UV scale; zero = {1,1}
	UVRotation          float32    // UV rotation in radians
	Detail              DetailSpec // per-face procedural detail (rocks); disabled by default
	Smooth              bool       // shade from interpolated vertex normals (see smooth.go)
	SmoothAngle         float32    // crease threshold in degrees; 0 = DefaultSmoothAngle
	Geometry            gfx.Geometry
	ExtrusionGeometries []gfx.Geometry
}
