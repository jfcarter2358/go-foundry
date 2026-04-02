package main

import (
	"fmt"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// Geometry holds GPU-side vertex data and associated handles.
// Vertex format: 5 floats per vertex [X, Y, Z, U, V].
// All geometry is stored in world space; model matrix is always identity.
type Geometry struct {
	Texture                    string
	TextureUniform             int32
	TextureCoordinateAttribute uint32
	Vertices                   []float32
	VertexAttribute            uint32
	Model                      mgl32.Mat4
	ModelUniform               int32
	Normal                     mgl32.Vec3
	Position                   mgl32.Vec3
	Yaw                        float32
	Vao                        uint32
	Vbo                        uint32
}

// Load uploads vertices (indexed by indices) to the GPU.
// vertices: slice of [X,Y,Z,U,V] vertex definitions.
// indices: triangle list referencing vertices.
func (g *Geometry) Load(vertices [][]float32, indices []int, texture string, position mgl32.Vec3) error {
	verts := []float32{}
	for _, idx := range indices {
		if idx < 0 || idx >= len(vertices) {
			return fmt.Errorf("Geometry.Load: index %d out of bounds (len=%d)", idx, len(vertices))
		}
		verts = append(verts, vertices[idx]...)
	}

	// Phase 0 fix: assign verts before using it in BufferData
	g.Vertices = verts
	g.Texture = texture
	g.Position = position

	g.Model = mgl32.Ident4()
	g.ModelUniform = gl.GetUniformLocation(Program, gl.Str("model\x00"))
	gl.UniformMatrix4fv(g.ModelUniform, 1, false, &g.Model[0])

	g.TextureUniform = gl.GetUniformLocation(Program, gl.Str("tex\x00"))
	gl.Uniform1i(g.TextureUniform, 0)

	gl.GenVertexArrays(1, &g.Vao)
	gl.BindVertexArray(g.Vao)

	gl.GenBuffers(1, &g.Vbo)
	gl.BindBuffer(gl.ARRAY_BUFFER, g.Vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(g.Vertices)*4, gl.Ptr(g.Vertices), gl.STATIC_DRAW)

	g.VertexAttribute = uint32(gl.GetAttribLocation(Program, gl.Str("vert\x00")))
	gl.EnableVertexAttribArray(g.VertexAttribute)
	gl.VertexAttribPointerWithOffset(g.VertexAttribute, 3, gl.FLOAT, false, 5*4, 0)

	g.TextureCoordinateAttribute = uint32(gl.GetAttribLocation(Program, gl.Str("vertTexCoord\x00")))
	gl.EnableVertexAttribArray(g.TextureCoordinateAttribute)
	gl.VertexAttribPointerWithOffset(g.TextureCoordinateAttribute, 2, gl.FLOAT, false, 5*4, 3*4)

	return nil
}

// Draw renders the geometry using the current OpenGL state.
// Phase 0 fix: use g. receiver (not w.), identity model matrix, and dynamic vertex count.
func (g Geometry) Draw() {
	if len(g.Vertices) == 0 {
		return
	}
	model := mgl32.Ident4()

	gl.UseProgram(Program)
	gl.UniformMatrix4fv(g.ModelUniform, 1, false, &model[0])

	gl.BindVertexArray(g.Vao)

	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, Textures[g.Texture])

	gl.DrawArrays(gl.TRIANGLES, 0, int32(len(g.Vertices)/5))
}

// ExtrusionPoint is a point in surface-local coordinate space.
//
// For walls:          U = distance along wall, V = distance from ceiling down.
// For floors/ceilings: U = world X, V = world Z.
type ExtrusionPoint struct {
	U float32
	V float32
}

// Extrusion defines a polygonal region on a surface that is displaced along the
// surface normal. Outer and Inner must have the same number of vertices. Outer
// defines the hole cut in the surface; Inner defines the shape at Depth.
// Side faces connect Outer[i]→Inner[i]. When Outer != Inner you get
// sloped/beveled sides.
type Extrusion struct {
	Outer       []ExtrusionPoint // CCW polygon on surface (the hole)
	Inner       []ExtrusionPoint // CCW polygon at extruded depth (same vertex count)
	Depth       float32          // displacement along surface normal (+outward, -inward)
	Texture     string           // optional texture for back face + sides; empty = surface texture
	Visible     bool             // if true, render side faces and back face; if false, only the hole is cut
	BackVisible bool             // if true, render the inner polygon face; if false, it's open
}

// TerrainPoint is an additional point placed on a surface for terrain
// displacement. Coordinates follow the same convention as ExtrusionPoint.
type TerrainPoint struct {
	U     float32
	V     float32
	Depth float32 // displacement along surface normal
}

// Wall is a rendered, optionally-collidable edge between two 2D points.
type Wall struct {
	StartPoint    mgl32.Vec2
	EndPoint      mgl32.Vec2
	Texture       string
	Collision     bool
	Visible       bool // if false, wall geometry is not drawn (default true)
	Pinned        bool // if true, InferPortals will not convert this wall to a portal
	DoubleSided   bool // if true, wall is rendered from both sides (default true)
	Terrain       bool // if true, use terrain displacement mesh
	Extrusions    []Extrusion
	TerrainPoints []TerrainPoint
	Geometry      Geometry
}

// DrawWithTransform renders the geometry using a caller-supplied model matrix.
func (g Geometry) DrawWithTransform(model mgl32.Mat4) {
	if len(g.Vertices) == 0 {
		return
	}
	gl.UseProgram(Program)
	gl.UniformMatrix4fv(g.ModelUniform, 1, false, &model[0])
	gl.BindVertexArray(g.Vao)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, Textures[g.Texture])
	gl.DrawArrays(gl.TRIANGLES, 0, int32(len(g.Vertices)/5))
}
