package editor

// editor_material_render.go — the material editor's 3D preview.
//
// The swatch is drawn through the engine's own world shader, not through a
// second implementation of it. gfx.CompileWorldProgram links another copy of
// vertex.glsl + fragment.glsl — the same text, the same injections — and
// render.PrepareWorldProgramForPreview gives it a lookdev light rig and, more
// importantly, binds every shadow sampler it has: a sampler2DArrayShadow left
// unassigned falls back to texture unit 0, which holds the albedo sampler2D,
// and that type collision makes every draw through the program
// GL_INVALID_OPERATION with nothing rasterised and nothing said.
//
// Why a copy rather than gfx.Program itself, which is shared and would work:
// the preview's camera, lights and fog would be left sitting in the program
// the level window draws with. Its next frame sets them again, but not before
// its own passes have begun.
//
// GL object ownership: the program and the textures are shared objects and are
// made wherever they are first needed. The preview MESHES carry VAOs, and no
// VAO is ever shared, so they are built in this window's context on first use
// and released in onMaterialEditorClose.

import (
	"log"
	"math"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/material"
	"foundry/internal/render"
	"foundry/internal/uiwin"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// The shapes the swatch can be shown on. A plane reads a tiling and a scroll,
// a sphere reads the lighting and a normal map, and a cube reads both plus how
// the material meets an edge — which is why there is a choice rather than one
// canonical blob.
const (
	materialShapeSphere = iota
	materialShapeCube
	materialShapePlane
)

// materialShapeNames is the UI spelling of each, indexed by the shape.
var materialShapeNames = []string{"Sphere", "Cube", "Plane"}

var (
	materialGridColor = mgl32.Vec3{0.26, 0.26, 0.30}
	materialAxisX     = mgl32.Vec3{0.50, 0.24, 0.24}
	materialAxisZ     = mgl32.Vec3{0.24, 0.24, 0.50}
)

// materialPreview holds this window's GL objects.
var materialPreview struct {
	prog   uint32
	meshes [3]gfx.Geometry // indexed by the materialShape* constants
	built  bool
	// failed latches a compile or upload failure so the log is not filled at
	// sixty lines a second by a window nobody has noticed is broken.
	failed bool
}

// materialEnsurePreview compiles the program and builds the meshes, once.
// Called from Draw, so a GL context is current.
func materialEnsurePreview() bool {
	if materialPreview.failed {
		return false
	}
	if materialPreview.built {
		return true
	}
	if materialPreview.prog == 0 {
		p, err := gfx.CompileWorldProgram(
			fileutil.ShaderPath("vertex.glsl"),
			fileutil.ShaderPath("fragment.glsl"),
		)
		if err != nil {
			log.Printf("material editor: preview shader: %v", err)
			materialPreview.failed = true
			return false
		}
		gfx.ForgetUniformLocations(p)
		materialPreview.prog = p
	}
	shapes := [3]func() ([][]float32, []int){
		materialSphereMesh, materialCubeMesh, materialPlaneMesh,
	}
	for i, build := range shapes {
		verts, indices := build()
		g := gfx.Geometry{Alpha: 1, AutoSmooth: true, Smooth: i != materialShapePlane}
		if err := g.Load(verts, indices, "", mgl32.Vec3{}); err != nil {
			log.Printf("material editor: preview mesh %d: %v", i, err)
			materialPreview.failed = true
			return false
		}
		materialPreview.meshes[i] = g
	}
	materialPreview.built = true
	return true
}

// materialFreePreviewMeshes releases the meshes' buffers. Must run with the
// material editor window's context current — see the file comment. The program
// is shared and is left alone, so re-opening the window costs no compile.
func materialFreePreviewMeshes() {
	for i := range materialPreview.meshes {
		materialPreview.meshes[i].Unload()
	}
	materialPreview.built = false
}

// materialDrawPreview draws the grid and the swatch.
func materialDrawPreview(w *uiwin.UIWindow) {
	view := materialCam.view()
	proj := materialCam.proj(w.W, w.H)
	projView := proj.Mul4(view)

	gl.Enable(gl.DEPTH_TEST)
	if materialShowGrid {
		materialDrawGrid()
	}
	gizmo.GizmoFlush(projView)

	if materialDoc != nil && materialDoc.Domain == material.DomainSurface {
		materialDrawSwatch(view, proj)
	}
	gl.Disable(gl.DEPTH_TEST)
}

// materialDrawGrid is the scale reference, with the two axes picked out.
func materialDrawGrid() {
	const half = 4
	for i := -half; i <= half; i++ {
		f := float32(i)
		xCol, zCol := materialGridColor, materialGridColor
		if i == 0 {
			xCol, zCol = materialAxisZ, materialAxisX
		}
		gizmo.GizmoLine(mgl32.Vec3{f, -1, -half}, mgl32.Vec3{f, -1, half}, xCol)
		gizmo.GizmoLine(mgl32.Vec3{-half, -1, f}, mgl32.Vec3{half, -1, f}, zCol)
	}
}

// materialDrawSwatch draws the chosen shape wearing the document.
//
// The document is published into the library under a preview name before the
// draw and the mesh points at that name, so the swatch goes through
// material.ResolveSurface exactly as a wall does — rather than through some
// preview-only path that could disagree with it.
func materialDrawSwatch(view, proj mgl32.Mat4) {
	if !materialEnsurePreview() || materialDoc == nil {
		return
	}
	material.SetPreview(materialPreviewDoc())
	g := &materialPreview.meshes[materialShape]
	g.MaterialName = materialPreviewName

	camPos := materialCam.position()
	render.PrepareWorldProgramForPreview(materialPreview.prog, view, proj, camPos, gfx.MaterialScrollTime)

	model := mgl32.HomogRotate3DY(materialSpinAngle)
	if materialShape == materialShapePlane {
		// The plane is authored lying down; stand it up so a tiling is read
		// face-on rather than edge-on from the default camera.
		model = model.Mul4(mgl32.HomogRotate3DX(-mgl32.DegToRad(60)))
	}

	// A material whose alpha or unlit sends it to the blended pass is drawn
	// blended here too, or a glass material would preview as opaque.
	st := g.SurfaceState()
	if st.IsTransparent() {
		gl.Enable(gl.BLEND)
		gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
		gl.DepthMask(false)
		defer func() {
			gl.DepthMask(true)
			gl.Disable(gl.BLEND)
		}()
	}
	g.DrawThroughProgram(materialPreview.prog, model)
}

// materialPreviewName is the library name the swatch is published under. A name
// no file can have — a material is named after its file, and ".preview" is not
// a legal basename here — so it can never collide with a real one or be picked
// up by a level.
const materialPreviewName = "\x00preview"

// materialPreviewDoc is the document as the library should see it while it is
// being edited: a copy under the preview name, so resolving it neither
// overwrites the saved material nor makes an unsaved one reachable by name from
// a level. material.SetPreview holds it in a slot of its own for exactly that
// reason, and only invalidates the engine's caches when it actually changes.
func materialPreviewDoc() *material.Doc {
	d := materialDoc.Clone()
	d.Name = materialPreviewName
	if !materialPreviewLit {
		// The "Lit" toggle is a VIEW control: the albedo with no shading, for
		// checking art rather than lighting. It is applied by adding the
		// engine's own unlit effect to the preview copy rather than by writing
		// the uniform afterwards — which would not work, since the draw sets
		// every one of its uniforms from the resolved material.
		d.AddEffect(material.KindUnlit)
	}
	return d
}

// ---------- preview meshes ----------
//
// Built here rather than loaded from models/ so the editor has no content
// dependency: a project with an empty models/ still gets a swatch. The vertex
// record is gfx.Geometry's own — [X,Y,Z,U,V] — and the indices are triangles.

// materialPlaneMesh is a 2x2 quad on the XZ plane, UV 0..2 so a tiling of one
// repeats twice across it and a scale is legible.
func materialPlaneMesh() ([][]float32, []int) {
	// Counter-clockwise seen from ABOVE, so the face the camera starts on is
	// the front one. Wound the other way it lights from underneath.
	verts := [][]float32{
		{-1, 0, -1, 0, 0},
		{-1, 0, 1, 0, 2},
		{1, 0, 1, 2, 2},
		{1, 0, -1, 2, 0},
	}
	return verts, []int{0, 1, 2, 0, 2, 3}
}

// materialCubeMesh is a unit cube, each face with its own 0..1 UV square so
// the art reads once per face rather than wrapping round the solid.
func materialCubeMesh() ([][]float32, []int) {
	// Each face as four corners in CCW order seen from outside, with its UVs.
	faces := [6][4][3]float32{
		{{-1, -1, 1}, {1, -1, 1}, {1, 1, 1}, {-1, 1, 1}},     // +Z
		{{1, -1, -1}, {-1, -1, -1}, {-1, 1, -1}, {1, 1, -1}}, // -Z
		{{1, -1, 1}, {1, -1, -1}, {1, 1, -1}, {1, 1, 1}},     // +X
		{{-1, -1, -1}, {-1, -1, 1}, {-1, 1, 1}, {-1, 1, -1}}, // -X
		{{-1, 1, 1}, {1, 1, 1}, {1, 1, -1}, {-1, 1, -1}},     // +Y
		{{-1, -1, -1}, {1, -1, -1}, {1, -1, 1}, {-1, -1, 1}}, // -Y
	}
	uv := [4][2]float32{{0, 1}, {1, 1}, {1, 0}, {0, 0}}
	var verts [][]float32
	var indices []int
	for _, f := range faces {
		base := len(verts)
		for i, p := range f {
			verts = append(verts, []float32{p[0] * 0.8, p[1] * 0.8, p[2] * 0.8, uv[i][0], uv[i][1]})
		}
		indices = append(indices, base, base+1, base+2, base, base+2, base+3)
	}
	return verts, indices
}

// materialSphereMesh is a UV sphere. Enough segments that a normal map reads as
// relief rather than as faceting, and few enough that the whole thing is a few
// thousand triangles.
func materialSphereMesh() ([][]float32, []int) {
	const (
		rings    = 32
		sectors  = 48
		radius   = 1.0
		uRepeats = 2 // so a tiling material shows its repeat round the equator
	)
	var verts [][]float32
	for r := 0; r <= rings; r++ {
		// Latitude from the north pole down.
		phi := float64(r) / rings * math.Pi
		y := float32(math.Cos(phi))
		ry := float32(math.Sin(phi))
		for s := 0; s <= sectors; s++ {
			// All the way round. Half a turn here is the whole difference
			// between a sphere and a bowl, and the bowl spins to look like a
			// sphere from one side, which is how it survived review.
			theta := float64(s) / sectors * 2 * math.Pi
			x := ry * float32(math.Cos(theta))
			z := ry * float32(math.Sin(theta))
			u := float32(s) / sectors * uRepeats
			v := float32(r) / rings
			verts = append(verts, []float32{x * radius, y * radius, z * radius, u, v})
		}
	}
	var indices []int
	stride := sectors + 1
	for r := 0; r < rings; r++ {
		for s := 0; s < sectors; s++ {
			a := r*stride + s
			b := a + stride
			// a -> a+1 -> b+1 -> b is the quad in counter-clockwise order seen
			// from OUTSIDE, which is the winding the one-sided lighting model
			// needs: fragment.glsl orients its facet normal by the winding, and
			// Geometry.Load welds its vertex normals from it.
			indices = append(indices, a, a+1, b+1, a, b+1, b)
		}
	}
	return verts, indices
}
