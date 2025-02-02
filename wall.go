package main

import (
	"github.com/go-gl/gl/v3.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

type Wall struct {
	Texture                    string
	TextureUniform             int32
	TextureCoordinateAttribute uint32
	Vertices                   []float32
	VertexAttribute            uint32
	Model                      mgl32.Mat4
	ModelUniform               int32
	Vao                        uint32
	Vbo                        uint32
}

// vertices is a nested array of points in the format X, Y, Z, U, V
// this should correspond to the four points starting from the top right and working around clockwise
func LoadWall(vertices [][]float32, textureName string) {
	// 3 ---- 0
	// |   /  |
	// |  /   |
	// 2 ---- 1
	// Triangles: [0, 1, 2], [2, 3, 0]

	verts := []float32{}
	idxs := []int{0, 1, 2, 2, 3, 0}
	for _, idx := range idxs {
		verts = append(verts, vertices[idx]...)
	}

	wall := Wall{
		Texture:  textureName,
		Vertices: verts,
	}

	wall.Model = mgl32.Ident4()
	wall.ModelUniform = gl.GetUniformLocation(Program, gl.Str("model\x00"))
	gl.UniformMatrix4fv(wall.ModelUniform, 1, false, &wall.Model[0])

	wall.TextureUniform = gl.GetUniformLocation(Program, gl.Str("tex\x00"))
	gl.Uniform1i(wall.TextureUniform, 0)

	// Configure the vertex data
	gl.GenVertexArrays(1, &wall.Vao)
	gl.BindVertexArray(wall.Vao)

	gl.GenBuffers(1, &wall.Vbo)
	gl.BindBuffer(gl.ARRAY_BUFFER, wall.Vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(wall.Vertices)*4, gl.Ptr(wall.Vertices), gl.STATIC_DRAW)

	wall.VertexAttribute = uint32(gl.GetAttribLocation(Program, gl.Str("vert\x00")))
	gl.EnableVertexAttribArray(wall.VertexAttribute)
	gl.VertexAttribPointerWithOffset(wall.VertexAttribute, 3, gl.FLOAT, false, 5*4, 0)

	wall.TextureCoordinateAttribute = uint32(gl.GetAttribLocation(Program, gl.Str("vertTexCoord\x00")))
	gl.EnableVertexAttribArray(wall.TextureCoordinateAttribute)
	gl.VertexAttribPointerWithOffset(wall.TextureCoordinateAttribute, 2, gl.FLOAT, false, 5*4, 3*4)

	Walls = append(Walls, wall)
}

func DrawWall(wall Wall) {
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)

	// angle := 45.0
	// model = mgl32.HomogRotate3D(float32(angle), mgl32.Vec3{0, 1, 0})

	// Render
	gl.UseProgram(Program)
	gl.UniformMatrix4fv(wall.ModelUniform, 1, false, &wall.Model[0])

	gl.BindVertexArray(wall.Vao)

	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, Textures[wall.Texture])

	gl.DrawArrays(gl.TRIANGLES, 0, 6*2*3)
}
