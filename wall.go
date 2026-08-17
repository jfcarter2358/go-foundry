package main

import (
	"fmt"
	"math"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// Geometry holds GPU-side vertex data and associated handles.
// Vertex format: 5 floats per vertex [X, Y, Z, U, V].
// All geometry is stored in world space; model matrix is always identity.
//
// Smooth surfaces additionally carry Normals — one per vertex, in a buffer of
// their own rather than interleaved, so Vertices stays the 5-float record that
// picking, the shadow passes and the detail renderer all index directly.
type Geometry struct {
	Texture                    string
	TextureUniform             int32
	TextureCoordinateAttribute uint32
	Vertices                   []float32
	VertexAttribute            uint32
	Smooth                     bool    // shade from interpolated vertex normals instead of flat facets
	SmoothAngle                float32 // crease threshold in degrees; 0 = defaultSmoothAngle
	Normals                    []float32
	NormalAttribute            uint32
	Model                      mgl32.Mat4
	ModelUniform               int32
	Normal                     mgl32.Vec3
	Position                   mgl32.Vec3
	Yaw                        float32
	Alpha                      float32 // 0.0 = fully transparent, 1.0 = fully opaque
	Water                      WaterParams
	Vao                        uint32
	Vbo                        uint32
	Nbo                        uint32 // normal buffer; 0 when the surface is flat-shaded
}

// smoothFlag returns the value of the shader's `smoothShading` uniform for this
// geometry: 1 only when smoothing is on *and* normals were actually generated,
// so a surface whose mesh produced none still falls back to the derivative
// normal rather than shading off an empty attribute.
func (g *Geometry) smoothFlag() int32 {
	if g.Smooth && len(g.Normals) > 0 {
		return 1
	}
	return 0
}

// applyWaterUniforms pushes this geometry's water settings to the shader.
// Every draw call sets them, including the disabled case, so a water plane
// never leaks its wave/fresnel state onto the geometry drawn after it.
func (g *Geometry) applyWaterUniforms() {
	if !g.Water.Enabled {
		gl.Uniform1i(IsWaterUniform, 0)
		return
	}
	gl.Uniform1i(IsWaterUniform, 1)
	gl.Uniform1f(WaveAmplitudeUniform, g.Water.WaveAmplitude)
	gl.Uniform1f(WaveLengthUniform, g.Water.WaveLength)
	gl.Uniform1f(WaveSpeedUniform, g.Water.WaveSpeed)
	gl.Uniform1f(ReflectivityUniform, g.Water.Reflectivity)
	gl.Uniform3f(ReflectColorUniform,
		g.Water.ReflectColor.X(), g.Water.ReflectColor.Y(), g.Water.ReflectColor.Z())
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
	if g.Alpha == 0 {
		g.Alpha = 1.0
	}

	// Normals are derived from the indexed mesh, before de-indexing loses which
	// triangles shared a vertex — smooth.go welds by position anyway, but the
	// index list is what defines the triangles in the first place.
	g.Normals = nil
	if g.Smooth {
		g.Normals = computeSmoothNormals(vertices, indices, g.SmoothAngle)
	}

	if len(g.Vertices) == 0 {
		return nil
	}

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

	// Smooth surfaces get a second buffer in the same VAO. Reloading a surface
	// runs Load again, so release the previous one first.
	if g.Nbo != 0 {
		gl.DeleteBuffers(1, &g.Nbo)
		g.Nbo = 0
	}
	if len(g.Normals) > 0 {
		if loc := gl.GetAttribLocation(Program, gl.Str("vertNormal\x00")); loc >= 0 {
			g.NormalAttribute = uint32(loc)
			gl.GenBuffers(1, &g.Nbo)
			gl.BindBuffer(gl.ARRAY_BUFFER, g.Nbo)
			gl.BufferData(gl.ARRAY_BUFFER, len(g.Normals)*4, gl.Ptr(g.Normals), gl.STATIC_DRAW)
			gl.EnableVertexAttribArray(g.NormalAttribute)
			gl.VertexAttribPointerWithOffset(g.NormalAttribute, 3, gl.FLOAT, false, 3*4, 0)
		} else {
			g.Normals = nil // shader has no normal input; stay flat-shaded
		}
	}

	return nil
}

// Delete releases the GPU resources (VAO and VBO) held by this geometry.
func (g *Geometry) Delete() {
	if g.Vao != 0 {
		gl.DeleteVertexArrays(1, &g.Vao)
		g.Vao = 0
	}
	if g.Vbo != 0 {
		gl.DeleteBuffers(1, &g.Vbo)
		g.Vbo = 0
	}
	if g.Nbo != 0 {
		gl.DeleteBuffers(1, &g.Nbo)
		g.Nbo = 0
	}
}

// IsTransparent returns true if this geometry has a non-opaque alpha value,
// or its texture has any semi-transparent texel (see texture_alpha.go). Both
// route the surface into the sorted, back-to-front blended pass instead of
// the opaque one.
func (g *Geometry) IsTransparent() bool {
	return (g.Alpha > 0 && g.Alpha < 1.0) || textureHasAlpha[g.Texture]
}

// Draw renders the geometry using the current OpenGL state.
func (g Geometry) Draw() {
	if len(g.Vertices) == 0 {
		return
	}
	model := mgl32.Ident4()

	gl.UseProgram(Program)
	gl.UniformMatrix4fv(g.ModelUniform, 1, false, &model[0])
	gl.Uniform1f(AlphaUniform, g.Alpha)
	gl.Uniform1i(SmoothUniform, g.smoothFlag())
	g.applyWaterUniforms()

	gl.BindVertexArray(g.Vao)

	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, Textures[g.Texture])

	gl.DrawArrays(gl.TRIANGLES, 0, int32(len(g.Vertices)/5))
}

// applyUVTransform transforms the UV coordinates of a vertex slice in-place.
// Vertices are [X,Y,Z,U,V]. Applies scale around (0,0), then rotation, then offset.
func applyUVTransform(vertices [][]float32, offset mgl32.Vec2, scale mgl32.Vec2, rotation float32) {
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
	UVOffset    mgl32.Vec2       // UV translation for side/back faces
	UVScale     mgl32.Vec2       // UV scale for side/back faces; zero = {1,1}
	UVRotation  float32          // UV rotation in radians for side/back faces
	Alpha       float32          // per-extrusion alpha; only affects custom-texture back faces
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
	Alpha         float32 // 0.0 = fully transparent, 1.0 = fully opaque
	Extrusions    []Extrusion
	TerrainPoints []TerrainPoint
	UVOffset      mgl32.Vec2 // UV translation
	UVScale       mgl32.Vec2 // UV scale; zero = {1,1}
	UVRotation    float32    // UV rotation in radians
	Detail        DetailSpec // per-face procedural detail (rocks); disabled by default
	Smooth        bool       // shade from interpolated vertex normals (see smooth.go)
	SmoothAngle   float32    // crease threshold in degrees; 0 = defaultSmoothAngle
	Geometry            Geometry
	ExtrusionGeometries []Geometry
}

// DrawWithTransform renders the geometry using a caller-supplied model matrix.
func (g Geometry) DrawWithTransform(model mgl32.Mat4) {
	if len(g.Vertices) == 0 {
		return
	}
	gl.UseProgram(Program)
	gl.UniformMatrix4fv(g.ModelUniform, 1, false, &model[0])
	gl.Uniform1f(AlphaUniform, g.Alpha)
	gl.Uniform1i(SmoothUniform, g.smoothFlag())
	g.applyWaterUniforms()
	gl.BindVertexArray(g.Vao)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, Textures[g.Texture])
	gl.DrawArrays(gl.TRIANGLES, 0, int32(len(g.Vertices)/5))
}
