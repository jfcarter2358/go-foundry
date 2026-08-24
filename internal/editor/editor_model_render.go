package editor

// editor_model_render.go — drawing the model editor's mesh.
//
// GL context ownership, which is the opposite of the 2D editor pop-out's:
//
// The 2D window routes every level mutation back through the *main* context
// (withMainGLContext, editor_2d_window.go) because the VAOs it rebuilds have to
// be drawable by the 3D view in the main window, and VAOs are the one class of
// GL object a shared context does not share.
//
// The model editor's mesh is drawn *only* in the model editor's window. So its
// Geometry is built, drawn and deleted entirely in that window's context, and
// nothing here may be wrapped in withMainGLContext — doing so would produce
// VAO names this window cannot draw. RenderSecondaryWindows makes the window's
// context current before calling Draw, and CloseUIWindow makes it current again
// before calling OnClose, so both ends are already correct.

import (
	"fmt"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"log"
	"path/filepath"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// modelProgram is the editor's own shader (model_vert/frag.glsl). Programs are
// shared between contexts, so this is compiled once and reused.
var modelProgram uint32

// modelUniforms caches modelProgram's uniform locations. Like the program, they
// are program state and so are valid in any context.
var modelUniforms struct {
	projection, view, model int32
	tex, camPos, tint       int32
	smoothShading           int32
}

// modelGeoms is the mesh compiled for the GPU: one Geometry per texture, since
// Geometry binds a single texture per draw call. Owned by the model editor
// window's GL context.
var modelGeoms []gfx.Geometry

// modelEditorEnsureProgram compiles the editor shader on first use. Called from
// the window's Draw, so a GL context is current.
func modelEditorEnsureProgram() error {
	if modelProgram != 0 {
		return nil
	}
	p, err := gfx.CompileProgramFromFiles(
		filepath.Join(fileutil.ExecutablePath, "shaders/model_vert.glsl"),
		filepath.Join(fileutil.ExecutablePath, "shaders/model_frag.glsl"),
	)
	if err != nil {
		return fmt.Errorf("model editor shader: %w", err)
	}
	modelProgram = p
	u := func(name string) int32 {
		return gl.GetUniformLocation(modelProgram, gl.Str(name+"\x00"))
	}
	modelUniforms.projection = u("projection")
	modelUniforms.view = u("view")
	modelUniforms.model = u("model")
	modelUniforms.tex = u("tex")
	modelUniforms.camPos = u("camPos")
	modelUniforms.tint = u("tint")
	modelUniforms.smoothShading = u("smoothShading")
	return nil
}

// modelEditorRebuild recompiles the document into GPU geometry. Cheap enough to
// run whenever the document is dirty — but only once per frame, from Draw,
// rather than once per mouse-motion event the way the surface editor rebuilds
// its sector.
func modelEditorRebuild() {
	modelEditorFreeGeometry()
	if modelDoc == nil {
		return
	}
	// The displayed vertices, so pose mode uploads the deformed mesh while the
	// document goes on holding the rest one (editor_model_pose.go).
	for _, sub := range modelDoc.ModelSubmeshesFrom(modelDisplayVerts()) {
		if len(sub.Indices) == 0 {
			continue
		}
		if err := gfx.EnsureTextureLoaded(sub.Texture); err != nil {
			// A missing texture is a content problem, not a reason to drop the
			// geometry — it still draws, just untextured.
			log.Printf("model editor: texture %q: %v", sub.Texture, err)
		}
		g := gfx.Geometry{
			Smooth:      sub.Smooth,
			SmoothAngle: modelDoc.SmoothAngle,
			Alpha:       1,
		}
		if err := g.Load(sub.Vertices, sub.Indices, sub.Texture, mgl32.Vec3{}); err != nil {
			log.Printf("model editor: upload %q: %v", sub.Texture, err)
			continue
		}
		modelGeoms = append(modelGeoms, g)
	}
	modelDoc.ClearDirty()
}

// modelEditorFreeGeometry releases the GPU buffers. Must run with the model
// editor window's context current — see the file comment.
func modelEditorFreeGeometry() {
	for i := range modelGeoms {
		modelGeoms[i].Delete()
	}
	modelGeoms = nil
}

// drawModelMesh renders the compiled submeshes.
//
// Geometry.Draw is not reusable here: it hardcodes gl.UseProgram(Program) and
// resolves its uniform locations against the world shader. The draw loop below
// is the same sequence against modelProgram.
func drawModelMesh(view, proj mgl32.Mat4, camPos mgl32.Vec3) {
	if len(modelGeoms) == 0 {
		return
	}
	gl.UseProgram(modelProgram)
	gl.UniformMatrix4fv(modelUniforms.projection, 1, false, &proj[0])
	gl.UniformMatrix4fv(modelUniforms.view, 1, false, &view[0])
	ident := mgl32.Ident4()
	gl.UniformMatrix4fv(modelUniforms.model, 1, false, &ident[0])
	gl.Uniform1i(modelUniforms.tex, 0)
	gl.Uniform3f(modelUniforms.camPos, camPos.X(), camPos.Y(), camPos.Z())
	gl.Uniform3f(modelUniforms.tint, 1, 1, 1)

	// Push filled triangles a hair away from the viewer so the wireframe drawn
	// on top of them wins the depth test instead of z-fighting: the lines lie
	// exactly in the plane of the faces they outline.
	gl.Enable(gl.POLYGON_OFFSET_FILL)
	gl.PolygonOffset(1, 1)

	for i := range modelGeoms {
		g := &modelGeoms[i]
		if len(g.Vertices) == 0 {
			continue
		}
		gl.Uniform1i(modelUniforms.smoothShading, g.SmoothFlag())
		gl.BindVertexArray(g.Vao)
		gl.ActiveTexture(gl.TEXTURE0)
		gl.BindTexture(gl.TEXTURE_2D, gfx.Textures[g.Texture])
		gl.DrawArrays(gl.TRIANGLES, 0, int32(len(g.Vertices)/5))
	}

	gl.Disable(gl.POLYGON_OFFSET_FILL)
	gl.BindVertexArray(0)
}

// ---------- overlays ----------

var (
	// Two wire colours, because the line is doing two different jobs. Over lit
	// faces it is a dark seam that reads as an edge; with the faces gone it is
	// the only thing on screen and has to carry the shape against the
	// background, which the dark one disappears into.
	modelWireColor     = mgl32.Vec3{0.15, 0.15, 0.18}
	modelWireOnlyColor = mgl32.Vec3{0.62, 0.66, 0.78}
	modelGridColor     = mgl32.Vec3{0.28, 0.28, 0.32}
	modelAxisX         = mgl32.Vec3{0.65, 0.25, 0.25}
	modelAxisZ         = mgl32.Vec3{0.25, 0.35, 0.7}
)

// drawModelWireframe outlines every face edge. Queued into the shared gizmo
// line buffer, flushed by the caller.
func drawModelWireframe() {
	if modelDoc == nil {
		return
	}
	col := modelWireColor
	if modelWireframe {
		col = modelWireOnlyColor
	}
	verts := modelDisplayVerts()
	for _, e := range modelDoc.Edges() {
		if e.A >= len(verts) || e.B >= len(verts) {
			continue
		}
		gizmo.GizmoLine(verts[e.A], verts[e.B], col)
	}
}

// drawModelGrid lays a unit grid on the XZ plane with the two ground axes
// picked out, so the model has a sense of scale and orientation.
func drawModelGrid() {
	const half = 5
	for i := -half; i <= half; i++ {
		f := float32(i)
		xCol, zCol := modelGridColor, modelGridColor
		if i == 0 {
			xCol, zCol = modelAxisZ, modelAxisX
		}
		gizmo.GizmoLine(mgl32.Vec3{f, 0, -half}, mgl32.Vec3{f, 0, half}, xCol)
		gizmo.GizmoLine(mgl32.Vec3{-half, 0, f}, mgl32.Vec3{half, 0, f}, zCol)
	}
}
