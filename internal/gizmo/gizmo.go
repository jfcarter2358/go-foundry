package gizmo

// go — flat-color line/wireframe rendering.
//
// Uses GizmoProgram (separate from the textured game shader) so overlays never
// corrupt game rendering state.
//
// This is a renderer, not an editor: the editor is its heaviest caller, but the
// biome scatter, the volume overlay and the multi-window plumbing draw through
// it too, so it sits below all of them rather than inside the editor cluster.
//
// Vertex format: [x, y, z, r, g, b]  (6 floats per vertex, no indices)

import (
	"fmt"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"math"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

var GizmoProgram uint32
var gizmoVAO, gizmoVBO uint32

// GizmoVAOOverride, when non-zero, is the vertex array GizmoFlush binds
// instead of gizmoVAO. VAOs are never shared between GL contexts, so a
// secondary window that draws gizmo lines (the popped-out 2D editor) supplies
// its own for the duration of its frame — see RenderSecondaryWindows
// (window.go). The VBO and the shader program are shared objects and need no
// such treatment; GizmoFlush re-specifies the attribute pointers on every
// call, so a bare VAO works.
var GizmoVAOOverride uint32

// gizmoCurrentVAO returns the vertex array to draw this flush through.
func gizmoCurrentVAO() uint32 {
	if GizmoVAOOverride != 0 {
		return GizmoVAOOverride
	}
	return gizmoVAO
}

// Buf accumulates line endpoints each frame, flushed by GizmoFlush.
// Exported because the editor's picking tests read back what a panel drew
// rather than rendering it — the only way to assert on an immediate-mode
// overlay without a GL context.
var Buf []float32

// GizmoInit compiles the flat-color shader and allocates the scratch VBO.
// Must be called after the OpenGL context is current.
func GizmoInit() {
	var err error
	GizmoProgram, err = gfx.CompileProgramFromFiles(
		fileutil.ShaderPath("gizmo_vert.glsl"),
		fileutil.ShaderPath("gizmo_frag.glsl"),
	)
	if err != nil {
		panic(fmt.Sprintf("gizmo shader: %v", err))
	}

	gl.GenVertexArrays(1, &gizmoVAO)
	gl.GenBuffers(1, &gizmoVBO)

	gl.BindVertexArray(gizmoVAO)
	gl.BindBuffer(gl.ARRAY_BUFFER, gizmoVBO)
	// Allocate a placeholder buffer (resized dynamically via STREAM_DRAW)
	gl.BufferData(gl.ARRAY_BUFFER, 0, nil, gl.STREAM_DRAW)

	stride := int32(6 * 4)
	vertAttr := uint32(gl.GetAttribLocation(GizmoProgram, gl.Str("vert\x00")))
	gl.EnableVertexAttribArray(vertAttr)
	gl.VertexAttribPointerWithOffset(vertAttr, 3, gl.FLOAT, false, stride, 0)

	colAttr := uint32(gl.GetAttribLocation(GizmoProgram, gl.Str("vertColor\x00")))
	gl.EnableVertexAttribArray(colAttr)
	gl.VertexAttribPointerWithOffset(colAttr, 3, gl.FLOAT, false, stride, 3*4)

	gl.BindVertexArray(0)
}

// GizmoLine queues a world-space line segment.
func GizmoLine(a, b mgl32.Vec3, color mgl32.Vec3) {
	Buf = append(Buf,
		a.X(), a.Y(), a.Z(), color.X(), color.Y(), color.Z(),
		b.X(), b.Y(), b.Z(), color.X(), color.Y(), color.Z(),
	)
}

// GizmoAABB queues the 12 edges of an axis-aligned bounding box.
func GizmoAABB(min, max mgl32.Vec3, color mgl32.Vec3) {
	x0, y0, z0 := min.X(), min.Y(), min.Z()
	x1, y1, z1 := max.X(), max.Y(), max.Z()

	// Bottom face
	GizmoLine(mgl32.Vec3{x0, y0, z0}, mgl32.Vec3{x1, y0, z0}, color)
	GizmoLine(mgl32.Vec3{x1, y0, z0}, mgl32.Vec3{x1, y0, z1}, color)
	GizmoLine(mgl32.Vec3{x1, y0, z1}, mgl32.Vec3{x0, y0, z1}, color)
	GizmoLine(mgl32.Vec3{x0, y0, z1}, mgl32.Vec3{x0, y0, z0}, color)
	// Top face
	GizmoLine(mgl32.Vec3{x0, y1, z0}, mgl32.Vec3{x1, y1, z0}, color)
	GizmoLine(mgl32.Vec3{x1, y1, z0}, mgl32.Vec3{x1, y1, z1}, color)
	GizmoLine(mgl32.Vec3{x1, y1, z1}, mgl32.Vec3{x0, y1, z1}, color)
	GizmoLine(mgl32.Vec3{x0, y1, z1}, mgl32.Vec3{x0, y1, z0}, color)
	// Vertical edges
	GizmoLine(mgl32.Vec3{x0, y0, z0}, mgl32.Vec3{x0, y1, z0}, color)
	GizmoLine(mgl32.Vec3{x1, y0, z0}, mgl32.Vec3{x1, y1, z0}, color)
	GizmoLine(mgl32.Vec3{x1, y0, z1}, mgl32.Vec3{x1, y1, z1}, color)
	GizmoLine(mgl32.Vec3{x0, y0, z1}, mgl32.Vec3{x0, y1, z1}, color)
}

// GizmoOBB queues the 12 edges of an oriented box: the box centred on center
// that extends half[i] along each of axes[i], which must be unit length and
// mutually perpendicular. GizmoAABB is the world-axis-aligned special case.
func GizmoOBB(center mgl32.Vec3, axes [3]mgl32.Vec3, half mgl32.Vec3, color mgl32.Vec3) {
	corner := func(sx, sy, sz float32) mgl32.Vec3 {
		return center.
			Add(axes[0].Mul(sx * half[0])).
			Add(axes[1].Mul(sy * half[1])).
			Add(axes[2].Mul(sz * half[2]))
	}
	// The two faces perpendicular to axes[0]...
	for _, sx := range [2]float32{-1, 1} {
		a := corner(sx, -1, -1)
		b := corner(sx, 1, -1)
		c := corner(sx, 1, 1)
		d := corner(sx, -1, 1)
		GizmoLine(a, b, color)
		GizmoLine(b, c, color)
		GizmoLine(c, d, color)
		GizmoLine(d, a, color)
	}
	// ...and the four rails joining them.
	for _, sy := range [2]float32{-1, 1} {
		for _, sz := range [2]float32{-1, 1} {
			GizmoLine(corner(-1, sy, sz), corner(1, sy, sz), color)
		}
	}
}

// GizmoSphere queues three orthogonal rings approximating a sphere.
func GizmoSphere(center mgl32.Vec3, radius float32, color mgl32.Vec3) {
	const segs = 32
	for i := 0; i < segs; i++ {
		a0 := float64(i) / segs * 2 * math.Pi
		a1 := float64(i+1) / segs * 2 * math.Pi
		// XY ring
		GizmoLine(
			center.Add(mgl32.Vec3{float32(math.Cos(a0)) * radius, float32(math.Sin(a0)) * radius, 0}),
			center.Add(mgl32.Vec3{float32(math.Cos(a1)) * radius, float32(math.Sin(a1)) * radius, 0}),
			color)
		// XZ ring
		GizmoLine(
			center.Add(mgl32.Vec3{float32(math.Cos(a0)) * radius, 0, float32(math.Sin(a0)) * radius}),
			center.Add(mgl32.Vec3{float32(math.Cos(a1)) * radius, 0, float32(math.Sin(a1)) * radius}),
			color)
		// YZ ring
		GizmoLine(
			center.Add(mgl32.Vec3{0, float32(math.Cos(a0)) * radius, float32(math.Sin(a0)) * radius}),
			center.Add(mgl32.Vec3{0, float32(math.Cos(a1)) * radius, float32(math.Sin(a1)) * radius}),
			color)
	}
}

// GizmoFlush uploads the accumulated line buffer to the GPU and draws it, then
// clears the buffer. projView is the combined projection×view matrix.
func GizmoFlush(projView mgl32.Mat4) {
	if len(Buf) == 0 {
		return
	}

	prevDepth := gl.IsEnabled(gl.DEPTH_TEST)

	gl.UseProgram(GizmoProgram)

	ident := mgl32.Ident4()
	projLoc := gl.GetUniformLocation(GizmoProgram, gl.Str("projection\x00"))
	viewLoc := gl.GetUniformLocation(GizmoProgram, gl.Str("view\x00"))
	gl.UniformMatrix4fv(projLoc, 1, false, &projView[0])
	gl.UniformMatrix4fv(viewLoc, 1, false, &ident[0])

	gl.BindVertexArray(gizmoCurrentVAO())
	gl.BindBuffer(gl.ARRAY_BUFFER, gizmoVBO)
	gl.BufferData(gl.ARRAY_BUFFER, len(Buf)*4, gl.Ptr(Buf), gl.STREAM_DRAW)

	stride := int32(6 * 4)
	vertAttr := uint32(gl.GetAttribLocation(GizmoProgram, gl.Str("vert\x00")))
	gl.EnableVertexAttribArray(vertAttr)
	gl.VertexAttribPointerWithOffset(vertAttr, 3, gl.FLOAT, false, stride, 0)
	colAttr := uint32(gl.GetAttribLocation(GizmoProgram, gl.Str("vertColor\x00")))
	gl.EnableVertexAttribArray(colAttr)
	gl.VertexAttribPointerWithOffset(colAttr, 3, gl.FLOAT, false, stride, 3*4)

	gl.DrawArrays(gl.LINES, 0, int32(len(Buf)/6))

	gl.BindVertexArray(0)
	gl.UseProgram(gfx.Program)
	if !prevDepth {
		gl.Disable(gl.DEPTH_TEST)
	}

	Buf = Buf[:0]
}
