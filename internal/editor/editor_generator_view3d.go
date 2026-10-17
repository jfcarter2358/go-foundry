package editor

// editor_generator_view3d.go — the Generator editor's 3D view.
//
// The preview's meshes (procgen.PreviewMesh) drawn through a copy of the
// engine's world program, set up by render.PrepareWorldProgramForPreview with
// its lookdev light rig and every shadow sampler bound — the material
// editor's route (editor_material_render.go), for the same reasons: what is
// shown is shaded by fragment.glsl, and the level window's program is left
// alone. With no texture chosen a mesh draws in its normal colours (the
// engine's untextured look), which reads a terrain's shape well; a mesh that
// names one of the preview's image layers (PreviewMesh.Image — the terrain
// stage's albedo hint) is painted with it; the overlay's texture picker puts
// a lookdev texture on every mesh instead.
//
// A mesh with vertex colours (PreviewMesh.Colors — a rock's bake) draws with
// them, white underneath when it has no texture, through the same
// vertexColors path a cooked rock takes in the level. A TURNTABLE mesh (a rock
// variant) is framed on its own and spun about its vertical axis while the
// view's Spin box is ticked, so its whole form can be judged without
// dragging.
//
// A mesh with INSTANCES (PreviewMesh.Instances — a scatter's rocks, a cliff
// wall's chunks) is drawn once per instance in one instanced draw, each
// placed by its own matrix in a per-instance buffer at the world program's
// instance attributes (gfx.AttachInstanceMatrices, the instanced scenery
// path's), so ten thousand rocks are one draw of one decimated proxy.
//
// The meshes are rebuilt as gfx.Geometry whenever a new preview lands, in
// this window's context (VAOs are never shared), and released when the
// window closes. The camera is an orbit camera framed on the meshes' bounds,
// driven from the window's raw input like the material editor's.

import (
	"fmt"
	"image"
	"log"
	"math"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/procgen"
	"foundry/internal/render"
	"foundry/internal/uiwin"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
	"github.com/veandco/go-sdl2/sdl"
)

// genCam is the 3D view's orbit camera. Its range is a terrain's, not a
// swatch's: a kilometre of canyon has to fit.
type genCam struct {
	Target     mgl32.Vec3
	Dist       float32
	Yaw, Pitch float32
}

func (c genCam) forward() mgl32.Vec3 { return gfx.DirectionFromAngles(c.Yaw, c.Pitch) }
func (c genCam) position() mgl32.Vec3 {
	return c.Target.Sub(c.forward().Mul(c.Dist))
}
func (c genCam) view() mgl32.Mat4 { return mgl32.LookAtV(c.position(), c.Target, mgl32.Vec3{0, 1, 0}) }
func (c genCam) proj(w, h int32) mgl32.Mat4 {
	if h <= 0 {
		h = 1
	}
	near := max(c.Dist*0.002, min(0.05, c.Dist*0.05))
	return mgl32.Perspective(mgl32.DegToRad(50), float32(w)/float32(h), near, c.Dist*20+100)
}

func (c *genCam) orbit(dx, dy int32) {
	c.Yaw += float32(dx) * 0.005
	c.Pitch -= float32(dy) * 0.005
	const limit = float32(math.Pi/2) - 0.01
	c.Pitch = min(max(c.Pitch, -limit), limit)
}

func (c *genCam) pan(dx, dy int32) {
	right := c.forward().Cross(mgl32.Vec3{0, 1, 0}).Normalize()
	up := right.Cross(c.forward()).Normalize()
	k := c.Dist * 0.0018
	c.Target = c.Target.Sub(right.Mul(float32(dx) * k)).Add(up.Mul(float32(dy) * k))
}

func (c *genCam) zoom(steps int32) {
	c.Dist *= float32(math.Pow(0.9, float64(steps)))
	c.Dist = min(max(c.Dist, 0.02), 50000)
}

// genV3 is the 3D view's state and GL objects.
var genV3 = struct {
	prog   uint32
	failed bool
	data   *procgen.PreviewData // what meshes were built from
	meshes []gfx.Geometry
	// instBuf and instN are each mesh's instance matrix buffer and count
	// (0, 0: drawn once).
	instBuf []uint32
	instN   []int
	lo, hi  mgl32.Vec3
	cam     genCam
	framed  bool
	texture string
	builtTx string
	grid    bool
	// image is the preview whose mesh image is uploaded, if any.
	image *procgen.PreviewData
	// turntable: the preview's meshes ask to be spun; spin is the Spin box,
	// angle the current turn and last when it was advanced; centre is the
	// axis the turn is about.
	turntable, spin bool
	angle           float32
	last            time.Time
	centre          mgl32.Vec3

	orbiting, panning bool
}{cam: genCam{Dist: 300, Yaw: mgl32.DegToRad(35), Pitch: mgl32.DegToRad(-35)}, grid: true, spin: true}

// genEnsureView3D compiles the program once and rebuilds the meshes when the
// preview (or the lookdev texture) changed. Runs with this window's context
// current.
func genEnsureView3D(p *procgen.PreviewData) bool {
	if genV3.failed {
		return false
	}
	if genV3.prog == 0 {
		prog, err := gfx.CompileWorldProgram(fileutil.ShaderPath("vertex.glsl"), fileutil.ShaderPath("fragment.glsl"))
		if err != nil {
			log.Printf("generator editor: preview shader: %v", err)
			genV3.failed = true
			return false
		}
		gfx.ForgetUniformLocations(prog)
		genV3.prog = prog
	}
	if p == genV3.data && genV3.texture == genV3.builtTx {
		return true
	}
	genFreeView3D()
	genV3.data, genV3.builtTx = p, genV3.texture
	if p == nil {
		return true
	}
	first := true
	turntable := false
	for _, m := range p.Meshes {
		turntable = turntable || m.Turntable
		verts, idx := genMeshRecords(m)
		tex := genV3.texture
		if tex == "" {
			tex = genPreviewImageTexture(p, m, verts)
		}
		if m.Image == "" && len(m.UVs) == len(m.Positions) {
			// The mesh's own UVs (a rock's box mapping), for a lookdev
			// texture to sit on as it would in the level.
			for i, uv := range m.UVs {
				verts[i][3], verts[i][4] = uv[0], uv[1]
			}
		}
		if len(idx) == 0 {
			continue
		}
		grow := func(v mgl32.Vec3) {
			if first {
				genV3.lo, genV3.hi, first = v, v, false
			}
			for k := 0; k < 3; k++ {
				genV3.lo[k] = min(genV3.lo[k], v[k])
				genV3.hi[k] = max(genV3.hi[k], v[k])
			}
		}
		if lo, hi, ok := m.InstanceBounds(); ok {
			grow(mgl32.Vec3(lo))
			grow(mgl32.Vec3(hi))
		} else {
			for _, q := range m.Positions {
				grow(mgl32.Vec3(q))
			}
		}
		g := gfx.Geometry{Alpha: 1, AutoSmooth: true, Smooth: true}
		if len(m.Normals) == len(m.Positions) {
			g.VertexNormals = make([][]float32, len(m.Normals))
			for i, n := range m.Normals {
				g.VertexNormals[i] = []float32{n[0], n[1], n[2]}
			}
		}
		if len(m.Colors) == len(m.Positions) {
			g.VertexColors = make([][]float32, len(m.Colors))
			for i, c := range m.Colors {
				g.VertexColors[i] = []float32{float32(c[0]) / 255, float32(c[1]) / 255, float32(c[2]) / 255, float32(c[3]) / 255}
			}
		}
		if genV3.texture != "" {
			_ = gfx.EnsureTextureLoaded(genV3.texture)
		}
		if err := g.Load(verts, idx, tex, mgl32.Vec3{}); err != nil {
			log.Printf("generator editor: preview mesh %s: %v", m.Name, err)
			continue
		}
		buf, n := genUploadInstances(g.Vao, m.Instances)
		genV3.meshes = append(genV3.meshes, g)
		genV3.instBuf = append(genV3.instBuf, buf)
		genV3.instN = append(genV3.instN, n)
	}
	genV3.turntable = turntable
	genV3.centre = genV3.lo.Add(genV3.hi).Mul(0.5)
	if (!genV3.framed || turntable) && len(genV3.meshes) > 0 {
		genView3DFrame()
	}
	// Let the uploads land before the first draw reads them. On WSLg's
	// Mesa d3d12 a terrain preview's buffers and mip chain still in flight
	// when it was drawn and read back deadlocked the driver in glReadPixels
	// (seen in the GL tests from the second such preview on). Once per new
	// preview, so the cost is nothing.
	gl.Finish()
	return true
}

// genUploadInstances puts a mesh's instance matrices in a buffer of their
// own and points vao's instance attributes at it. 0, 0 for a mesh drawn
// once.
func genUploadInstances(vao uint32, inst [][16]float32) (uint32, int) {
	if len(inst) == 0 || vao == 0 {
		return 0, 0
	}
	var buf uint32
	gl.GenBuffers(1, &buf)
	gl.BindBuffer(gl.ARRAY_BUFFER, buf)
	gl.BufferData(gl.ARRAY_BUFFER, len(inst)*64, gl.Ptr(&inst[0][0]), gl.STATIC_DRAW)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	gfx.AttachInstanceMatrices(vao, buf)
	return buf, len(inst)
}

// genMeshRecords is a preview mesh as gfx.Geometry's [X,Y,Z,U,V] records,
// with planar UVs (a metre-scale tiling for a lookdev texture).
func genMeshRecords(m procgen.PreviewMesh) ([][]float32, []int) {
	verts := make([][]float32, len(m.Positions))
	for i, p := range m.Positions {
		verts[i] = []float32{p[0], p[1], p[2], p[0] / 8, p[2] / 8}
	}
	idx := make([]int, 0, len(m.Indices))
	for _, ix := range m.Indices {
		if int(ix) < len(verts) {
			idx = append(idx, int(ix))
		}
	}
	return verts, idx[:len(idx)/3*3]
}

// genPreviewImageName is the GL texture a preview mesh's image is uploaded
// as: one name, re-specified for each new preview (UpdateTextureFromRGBA
// reuses the handle), released with the meshes.
const genPreviewImageName = "__generator_preview_image"

// genPreviewImageTexture uploads the image layer m names, rewrites verts'
// UVs to m's, and returns the texture to draw with — or "" when m has no
// image (normal colours).
func genPreviewImageTexture(p *procgen.PreviewData, m procgen.PreviewMesh, verts [][]float32) string {
	if m.Image == "" || len(m.UVs) != len(m.Positions) {
		return ""
	}
	for _, im := range p.Images {
		if im.Name != m.Image || im.W <= 0 || im.H <= 0 || len(im.RGBA) != im.W*im.H*4 {
			continue
		}
		if genV3.image != p {
			img := image.NewRGBA(image.Rect(0, 0, im.W, im.H))
			copy(img.Pix, im.RGBA)
			if err := gfx.UpdateTextureFromRGBA(genPreviewImageName, img); err != nil {
				log.Printf("generator editor: preview image %s: %v", im.Name, err)
				return ""
			}
			genV3.image = p
		}
		for i, uv := range m.UVs {
			verts[i][3], verts[i][4] = uv[0], uv[1]
		}
		return genPreviewImageName
	}
	return ""
}

// genFreeView3D releases the meshes. This window's context must be current.
func genFreeView3D() {
	for i := range genV3.meshes {
		genV3.meshes[i].Unload()
	}
	for _, b := range genV3.instBuf {
		if b != 0 {
			gl.DeleteBuffers(1, &b)
		}
	}
	genV3.meshes, genV3.data = nil, nil
	genV3.instBuf, genV3.instN = nil, nil
	if genV3.image != nil {
		gfx.ReleaseTexture(genPreviewImageName)
		genV3.image = nil
	}
}

// genView3DFrame aims the camera at the meshes.
func genView3DFrame() {
	if len(genV3.meshes) == 0 {
		return
	}
	genV3.cam.Target = genV3.lo.Add(genV3.hi).Mul(0.5)
	// Down to a pebble's few centimetres: a rock variant is framed on its
	// own.
	genV3.cam.Dist = max(genV3.hi.Sub(genV3.lo).Len()*0.9, 0.05)
	genV3.framed = true
}

// genDrawView3D draws the meshes under the UI.
func genDrawView3D(w *uiwin.UIWindow) {
	p := genCurrentPreview()
	if !genEnsureView3D(p) {
		return
	}
	view := genV3.cam.view()
	proj := genV3.cam.proj(w.W, w.H)
	gl.Enable(gl.DEPTH_TEST)
	if genV3.grid && len(genV3.meshes) > 0 {
		genDrawGround3D()
		gizmo.GizmoFlush(proj.Mul4(view))
	}
	render.PrepareWorldProgramForPreview(genV3.prog, view, proj, genV3.cam.position(), gfx.MaterialScrollTime)
	model := genTurntableMatrix()
	for i := range genV3.meshes {
		if i < len(genV3.instN) && genV3.instN[i] > 0 {
			genV3.meshes[i].DrawInstancedThroughProgram(genV3.prog, 0, genV3.instN[i])
			continue
		}
		genV3.meshes[i].DrawThroughProgram(genV3.prog, model)
	}
	gl.Disable(gl.DEPTH_TEST)
}

// genTurntableMatrix is the meshes' model matrix: the identity, or, for a
// turntable preview with Spin on, a turn about the vertical through the
// meshes' centre, advanced by the time since the last frame.
func genTurntableMatrix() mgl32.Mat4 {
	now := time.Now()
	if genV3.turntable && genV3.spin {
		if !genV3.last.IsZero() {
			dt := min(float32(now.Sub(genV3.last).Seconds()), 0.1)
			genV3.angle = float32(math.Mod(float64(genV3.angle+dt*0.45), 2*math.Pi))
		}
	}
	genV3.last = now
	if !genV3.turntable || genV3.angle == 0 {
		return mgl32.Ident4()
	}
	c := genV3.centre
	return mgl32.Translate3D(c[0], c[1], c[2]).Mul4(mgl32.HomogRotate3DY(genV3.angle)).Mul4(mgl32.Translate3D(-c[0], -c[1], -c[2]))
}

// genDrawGround3D is a grid under the meshes, ten lines a side.
func genDrawGround3D() {
	lo, hi := genV3.lo, genV3.hi
	col := mgl32.Vec3{0.26, 0.26, 0.30}
	y := lo[1] - 0.01
	for i := 0; i <= 10; i++ {
		t := float32(i) / 10
		x := lo[0] + (hi[0]-lo[0])*t
		z := lo[2] + (hi[2]-lo[2])*t
		gizmo.GizmoLine(mgl32.Vec3{x, y, lo[2]}, mgl32.Vec3{x, y, hi[2]}, col)
		gizmo.GizmoLine(mgl32.Vec3{lo[0], y, z}, mgl32.Vec3{hi[0], y, z}, col)
	}
}

// genView3DOverlay is the little window over the 3D view.
func genView3DOverlay(x, y float32) {
	imgui.SetNextWindowPos(imgui.Vec2{X: x + 8, Y: y + 8})
	imgui.BeginV("##genview3d", nil, genPanelFlags|imgui.WindowFlagsAlwaysAutoResize)
	n, tris, inst := 0, 0, 0
	if p := genCurrentPreview(); p != nil {
		n = len(p.Meshes)
		for _, m := range p.Meshes {
			k := max(len(m.Instances), 1)
			if len(m.Instances) > 0 {
				inst += len(m.Instances)
			}
			tris += len(m.Indices) / 3 * k
		}
	}
	if genV3.failed {
		editorTextWrappedColored(genWarnColor, "The preview shader did not compile; see the log.")
	}
	if inst > 0 {
		editorTextMuted(fmt.Sprintf("%d meshes, %d instances, %d triangles", n, inst, tris))
	} else {
		editorTextMuted(fmt.Sprintf("%d meshes, %d triangles", n, tris))
	}
	if n == 0 {
		editorTextMuted("This stage's preview has no meshes.")
	}
	tex := genV3.texture
	imgui.SetNextItemWidth(150)
	if textureCombo("Texture##gen3dtex", &tex, true) {
		genV3.texture = tex
	}
	editorMenuTooltip("A lookdev texture for the preview meshes. None draws them in their normal colours.")
	imgui.Checkbox("Grid##gen3dgrid", &genV3.grid)
	imgui.SameLine()
	if imgui.Button("Frame##gen3dframe") {
		genView3DFrame()
	}
	if genV3.turntable {
		imgui.SameLine()
		imgui.Checkbox("Spin##gen3dspin", &genV3.spin)
		editorMenuTooltip("Turn the variant about its vertical axis.")
	}
	imgui.End()
}

// genView3DHandleEvent orbits, pans and zooms.
func genView3DHandleEvent(event sdl.Event) {
	io := imgui.CurrentIO()
	switch t := event.(type) {
	case sdl.MouseButtonEvent:
		if t.Type == sdl.MOUSEBUTTONDOWN {
			if io.WantCaptureMouse() {
				return
			}
			switch t.Button {
			case sdl.ButtonLeft, sdl.ButtonRight:
				genV3.orbiting = true
			case sdl.ButtonMiddle:
				genV3.panning = true
			}
			return
		}
		switch t.Button {
		case sdl.ButtonLeft, sdl.ButtonRight:
			genV3.orbiting = false
		case sdl.ButtonMiddle:
			genV3.panning = false
		}
	case sdl.MouseMotionEvent:
		switch {
		case genV3.orbiting:
			genV3.cam.orbit(t.XRel, t.YRel)
		case genV3.panning:
			genV3.cam.pan(t.XRel, t.YRel)
		}
	case sdl.MouseWheelEvent:
		if io.WantCaptureMouse() {
			return
		}
		genV3.cam.zoom(t.Y)
	}
}
