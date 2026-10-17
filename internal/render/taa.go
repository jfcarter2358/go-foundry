package render

// taa.go — temporal anti-aliasing, and the scene-target frame it runs in.
// Phase 4 of the Nanite plan (docs/nanite/PHASE_4.md); the spec's decision is
// that anti-aliasing moves from the window's MSAA to TAA, with MSAA kept as a
// fallback setting (gfx.AntiAliasMode).
//
// # The frame
//
// In a TAA frame DrawWorld draws into the scene target (scene_target.go)
// rather than the caller's: sceneFrameBegin declares it as the frame target,
// so every pass that steps out to a shadow map or a depth copy steps back into
// it. At the end, sceneFrameEnd resolves it into the caller's target. Between
// the two, five things are TAA's:
//
//  1. JITTER (taaBeginJitter). Right after the frame's projection is built,
//     it is moved by a fraction of a pixel — the next point of a Halton(2,3)
//     sequence of eight — and every pass draws through the moved one
//     (gfx.Projection, gfx.ProjectionJitter for the viewmodel's own lens).
//     Over eight frames a still pixel has been sampled at eight places inside
//     itself.
//  2. MOTION (taaMotionPass). Where each pixel's surface was last frame: a
//     moving object's from its own matrices, rasterised over it
//     (taa_object_*.glsl — the entities, the chunks a frame carries, the
//     brushes a script moved, recorded as the opaque pass drew them by
//     taaNote*), and everything else's from the depth and the camera's
//     matrices (taa_velocity_frag.glsl), dilated to the nearest surface of
//     each 3x3 so edges keep their own motion.
//  3. The viewmodel's pixels move with the camera, so their motion is zero
//     (taaViewmodelMotion), drawn over the motion after the hands are.
//  4. RESOLVE (taa_resolve_frag.glsl): reproject the history along the
//     motion, clamp it to this frame's neighbourhood, blend a tenth of the
//     new frame in.
//  5. SHARPEN and output (taa_output_frag.glsl) into the caller's target.
//
// The history is forgotten — the next frame taken as it is — on the first
// frame, a resize, a new level, a jump of the camera (a teleport, a cut) and
// a change of lens.
//
// # Cutouts
//
// Alpha-to-coverage needs samples to spend and the scene target has one a
// pixel, so in a TAA frame the cutout shaders turn coverage into a per-pixel
// chance of being kept instead, a different dither each frame
// (coverageDither; fragment.glsl, foliage and its impostors, cutout sprites,
// the visibility buffer's ID pass). The
// history averages those frames back into the edge's coverage — which is
// alpha-to-coverage spread over time instead of over samples.
//
// # Switches
//
//	gfx.AntiAliasMode (graphics.yaml anti_aliasing, FOUNDRY_AA)  taa | msaa | off
//	AntiAliasEnabled (F10)  in a TAA frame: the jitter and the resolve; off,
//	                        the scene target's frame is shown as drawn
//	gfx.TAASharpen          the output's unsharp mask
//
// The editor's own 3D views always take the forward path (sceneFrameWanted).

import (
	"fmt"
	"log"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// TAAResolve is whether a TAA frame jitters and resolves at all. Off
// (FOUNDRY_TAA_RESOLVE=0) the frame still draws into the scene target — the
// visibility buffer still runs — and is shown as drawn, one sample a pixel:
// the A/B against the forward path that the parity checks compare
// (docs/nanite/PHASE_4.md). F10 (AntiAliasEnabled) does the same at run time.
var TAAResolve = gfx.EnvFlag("FOUNDRY_TAA_RESOLVE", true)

// taaNoObjectMotion is the object-motion attachment's clear value, "no
// object drew here: the camera's motion" (taa_velocity_frag.glsl's
// NO_OBJECT_MOTION). Far outside any real motion in texture units.
const taaNoObjectMotion = 16384

// taaJitterSamples is the length of the jitter sequence.
const taaJitterSamples = 8

// taaBlendCurrent is the new frame's share of a still pixel.
const taaBlendCurrent = 0.1

// taaCutDistance is how far the camera may move in one frame before the
// history is taken for a different place and forgotten.
const taaCutDistance = 4.0

// halton is the radical inverse of i in base b.
func halton(i, b int) float32 {
	f, r := float32(1), float32(0)
	for i > 0 {
		f /= float32(b)
		r += f * float32(i%b)
		i /= b
	}
	return r
}

// taaJitter is frame n's jitter for a w by h target, in normalised device
// units: Halton(2,3) point n mod taaJitterSamples, centred on the pixel.
func taaJitter(n uint32, w, h int32) mgl32.Vec2 {
	return taaJitterN(n, w, h, taaJitterSamples)
}

// taaJitterN is taaJitter over a sequence of samples points: an upsampling
// frame (render scale below 1) spreads more of them, so every output pixel
// is visited by samples near its centre.
func taaJitterN(n uint32, w, h int32, samples int) mgl32.Vec2 {
	i := int(n%uint32(max(samples, 1))) + 1
	px, py := halton(i, 2)-0.5, halton(i, 3)-0.5
	return mgl32.Vec2{2 * px / float32(max(w, 1)), 2 * py / float32(max(h, 1))}
}

// taaState is the frame's TAA: its programs, its history, and what the last
// frame left for this one to reproject against.
type taaState struct {
	tried, ok bool
	vao       uint32
	velocity  progUniforms
	resolve   progUniforms
	output    progUniforms
	object    progUniforms
	objSkin   gfx.SkinUniforms

	w, h int32
	// rw, rh: the size the world is drawn at — w, h but under a render
	// scale (gfx/render_scale.go), when the resolve upsamples into the
	// output-sized history. The motion is at this size.
	rw, rh       int32
	history      [2]uint32
	historyFBO   [2]uint32
	cur          int
	historyValid bool
	// resolvedWithHistory: the last frame's resolve read a history (for
	// the tests).
	resolvedWithHistory bool
	// motion is the frame's final motion vectors (taa_velocity_frag.glsl),
	// in an FBO of its own.
	motion, motionFBO uint32

	frame uint32
	// The frame being drawn: whether it is a scene-target frame at all
	// (active) and whether TAA runs in it (on); the caller's target it ends
	// in; the unjittered projection and view-projections.
	active, on bool
	// editorDepth: the frame is the editor's view, whose overlays are drawn
	// into the caller's target after it and depth-tested there, so the
	// output pass writes the scene's depth into it too.
	editorDepth    bool
	final          FrameTarget
	baseProjection mgl32.Mat4
	jitter         mgl32.Vec2
	viewProj       mgl32.Mat4
	invJitVP       mgl32.Mat4
	prevViewProj   mgl32.Mat4
	prevValid      bool
	prevCam        mgl32.Vec3
	prevProjection mgl32.Mat4
	lvl            *world.Level

	// Moving objects' draws this frame (taaNote*), and each one's matrix last
	// frame.
	draws []taaMotionDraw
	prev  map[any]mgl32.Mat4
	next  map[any]mgl32.Mat4
}

// taaMotionDraw is one moving thing the opaque pass drew.
type taaMotionDraw struct {
	key   any
	model mgl32.Mat4
	geom  *gfx.Geometry
	cb    *ChunkBatch
	batch *gfx.Batch
	first int32
	count int32
}

var taa taaState

func (t *taaState) ready() bool {
	if t.tried {
		return t.ok
	}
	t.tried = true
	if err := t.build(); err != nil {
		log.Printf("taa: unavailable: %v — anti-aliasing falls back to the window's MSAA", err)
		return false
	}
	t.ok = true
	return true
}

func (t *taaState) build() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	vert := mustReadShader(biomeShaderPath("taa_fullscreen_vert.glsl"))
	for _, p := range []struct {
		frag string
		out  *progUniforms
	}{
		{"taa_velocity_frag.glsl", &t.velocity},
		{"taa_resolve_frag.glsl", &t.resolve},
		{"taa_output_frag.glsl", &t.output},
	} {
		prog, perr := gfx.CompileProgramFromSources(vert, mustReadShader(biomeShaderPath(p.frag)))
		if perr != nil {
			return fmt.Errorf("%s: %v", p.frag, perr)
		}
		*p.out = progUniforms{prog: prog, loc: map[string]int32{}}
	}
	ov, err := gfx.InjectSkinning(mustReadShader(biomeShaderPath("taa_object_vert.glsl")))
	if err != nil {
		return err
	}
	prog, err := gfx.CompileProgramFromSources(ov, mustReadShader(biomeShaderPath("taa_object_frag.glsl")))
	if err != nil {
		return fmt.Errorf("taa_object: %v", err)
	}
	t.object = progUniforms{prog: prog, loc: map[string]int32{}}
	gl.GenVertexArrays(1, &t.vao)
	t.prev, t.next = map[any]mgl32.Mat4{}, map[any]mgl32.Mat4{}
	return nil
}

// ensure sizes the history (w by h, the output's size) and the motion (rw
// by rh, the size the world is drawn at), forgetting the history when its
// size changes. A render scale's change keeps it: it is the output's.
func (t *taaState) ensure(w, h, rw, rh int32) {
	if t.history[0] != 0 && t.w == w && t.h == h {
		if t.rw != rw || t.rh != rh {
			if t.motion != 0 {
				gl.DeleteTextures(1, &t.motion)
				gl.DeleteFramebuffers(1, &t.motionFBO)
			}
			t.rw, t.rh = rw, rh
			t.motion, t.motionFBO = colorTarget(gl.RG16F, gl.RG, rw, rh)
			gl.BindTexture(gl.TEXTURE_2D, 0)
		}
		return
	}
	t.release()
	t.w, t.h = w, h
	t.rw, t.rh = rw, rh
	for i := range t.history {
		t.history[i], t.historyFBO[i] = colorTarget(gl.RGBA16F, gl.RGBA, w, h)
		gl.BindTexture(gl.TEXTURE_2D, t.history[i])
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	}
	t.motion, t.motionFBO = colorTarget(gl.RG16F, gl.RG, rw, rh)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	t.historyValid = false
}

// upscaling reports whether this frame's world is drawn smaller than its
// output.
func (t *taaState) upscaling() bool { return t.rw != t.w || t.rh != t.h }

func (t *taaState) release() {
	for i := range t.history {
		if t.history[i] != 0 {
			gl.DeleteTextures(1, &t.history[i])
			gl.DeleteFramebuffers(1, &t.historyFBO[i])
			t.history[i], t.historyFBO[i] = 0, 0
		}
	}
	if t.motion != 0 {
		gl.DeleteTextures(1, &t.motion)
		gl.DeleteFramebuffers(1, &t.motionFBO)
		t.motion, t.motionFBO = 0, 0
	}
}

// TAAShotFrames is how many frames a caller that draws one picture at a time
// (a headless shot) should draw first at the same instant, so the picture is
// TAA's converged one rather than its first frame: twice the jitter sequence
// in a TAA frame, none otherwise.
func TAAShotFrames() int {
	if gfx.AntiAliasMode != gfx.AAModeTAA || !AntiAliasEnabled || !TAAResolve {
		return 0
	}
	return 2 * taaJitterSamples
}

// ForgetTAAHistory makes the next frame start TAA's history afresh: for a
// caller that moved the camera somewhere the last frame says nothing about
// without the move looking like one (a test, a cutscene's cut).
func ForgetTAAHistory() { taa.historyValid = false }

// sceneFrameWanted reports whether this frame draws into the scene target:
// TAA is the mode, the frame is not the editor's own view (whose overlays
// and wireframe draw straight into the window), and the scene target and the
// TAA programs exist.
//
// The editor's filled view takes it too while EditorSceneFrame is on
// (editor_scene_frame.go); its overlays then read the world's depth from
// the window, written there by the output pass (editorDepth).
func sceneFrameWanted(o WorldDraw) bool {
	return gfx.AntiAliasMode == gfx.AAModeTAA && (!o.Editor || EditorSceneFrame) && !o.Wireframe && taa.ready()
}

// sceneFrameBegin makes the scene target the frame's target, after
// beginFrameTarget has declared the caller's. False (and nothing changed)
// when the frame takes the forward path.
func sceneFrameBegin(o WorldDraw) bool {
	taa.active = false
	// HDR shading needs the RGBA16F scene target (gfx/hdr.go): only a frame
	// drawn into it may, and world.PackFog reads this when it fills the
	// scene block for the frame.
	gfx.HDRFrame = false
	if !sceneFrameWanted(o) {
		// A frame drawn the other way (the editor's view between two
		// stretches of play) leaves the history describing nothing.
		taa.historyValid, taa.prevValid = false, false
		return false
	}
	final := FrameTarget{FBO: boundFramebuffer(), Viewport: boundViewport()}
	w, h := final.Viewport[2], final.Viewport[3]
	on := AntiAliasEnabled && TAAResolve
	// The render scale (gfx/render_scale.go, dynres.go): only a frame the
	// resolve runs in can be upsampled, and never the editor's view.
	rw, rh := scaledSize(w, h, frameRenderScale(o, on))
	if !scene.ensure(rw, rh) {
		return false
	}
	taa.ensure(w, h, rw, rh)
	taa.active, taa.final = true, final
	taa.editorDepth = o.Editor
	gfx.HDRFrame = true
	taa.on = on
	dynResBegin()
	drawTarget.set, drawTarget.t = true, FrameTarget{FBO: scene.fbo, Viewport: [4]int32{0, 0, rw, rh}}
	gl.BindFramebuffer(gl.FRAMEBUFFER, scene.fbo)
	gl.Viewport(0, 0, rw, rh)
	drawBuffersColour()
	taa.draws = taa.draws[:0]
	return true
}

// sceneFrameActive reports whether the frame being drawn is a scene-target
// frame.
func sceneFrameActive() bool { return taa.active }

// taaActive reports whether TAA runs in the frame being drawn: its jitter,
// its motion, its resolve — and the cutout shaders' dither.
func taaActive() bool { return taa.active && taa.on }

// sceneFrameClear clears what the frame's own colour-and-depth clear does not
// reach: the scene target's other attachments and its stencil.
func sceneFrameClear() {
	if taa.active {
		scene.clear()
		drawBuffersColour()
	}
}

// taaBeginJitter moves the frame's projection by this frame's jitter and
// remembers what it was, after DrawWorld has built it and before anything
// draws through it. It also decides whether the history still describes
// where the camera is.
func taaBeginJitter(lvl *world.Level) {
	if !taa.active {
		return
	}
	base := gfx.Projection
	taa.baseProjection = base
	taa.viewProj = base.Mul4(gfx.Cam.Matrix)
	cut := !taa.prevValid || lvl != taa.lvl || base != taa.prevProjection ||
		gfx.Cam.Position.Sub(taa.prevCam).Len() > taaCutDistance
	if cut || !taa.on {
		taa.historyValid = false
	}
	if lvl != taa.lvl {
		clear(taa.prev)
	}
	taa.lvl = lvl
	if !taa.on {
		taa.jitter = mgl32.Vec2{}
		taa.invJitVP = taa.viewProj.Inv()
		return
	}
	taa.frame++
	taa.jitter = taaJitterN(taa.frame, taa.rw, taa.rh, taaJitterPhases(taa.rw, taa.w))
	gfx.ProjectionJitter = taa.jitter
	gfx.Projection = gfx.JitterProjection(base, taa.jitter)
	gl.UseProgram(gfx.Program)
	gfx.UploadProjection(gfx.Projection)
	taa.invJitVP = gfx.Projection.Mul4(gfx.Cam.Matrix).Inv()
}

// taaEndJitter puts the unjittered projection back, for whatever draws after
// the frame (the editor's picking reads gfx.Projection, the HUD's world
// anchors project through it).
func taaEndJitter() {
	if taa.jitter == (mgl32.Vec2{}) {
		return
	}
	gfx.Projection = taa.baseProjection
	gfx.ProjectionJitter = mgl32.Vec2{}
	taa.jitter = mgl32.Vec2{}
	gl.UseProgram(gfx.Program)
	gfx.UploadProjection(gfx.Projection)
}

// coverageDither is the cutout shaders' dither switch: 0 off (alpha-to-
// coverage, or a plain cut), else one more than the frame's place in the
// jitter sequence, which picks the dither pattern (see the file comment).
func coverageDither() int32 {
	if !taaActive() {
		return 0
	}
	return int32(taa.frame%taaJitterSamples) + 1
}

// ---------- moving objects ----------

// taaRecording reports whether the opaque pass should note what moves.
func taaRecording() bool { return taaActive() }

// taaNoteGeometry notes an opaque mesh drawn under model (an entity's
// attachment), keyed by the mesh.
func taaNoteGeometry(g *gfx.Geometry, model mgl32.Mat4) {
	if !taaRecording() || g.IsTransparent() {
		return
	}
	taa.draws = append(taa.draws, taaMotionDraw{key: g, model: model, geom: g})
}

// taaNoteChunk notes a chunk's batches drawn under model.
func taaNoteChunk(ch *world.Chunk, cb *ChunkBatch, model mgl32.Mat4) {
	if !taaRecording() {
		return
	}
	taa.draws = append(taa.draws, taaMotionDraw{key: ch, model: model, cb: cb})
}

// taaNoteBrush notes a moved brush's range of batch drawn under model.
func taaNoteBrush(b *world.Brush, batch *gfx.Batch, first, count int32, model mgl32.Mat4) {
	if !taaRecording() {
		return
	}
	taa.draws = append(taa.draws, taaMotionDraw{key: b, model: model, batch: batch, first: first, count: count})
}

// taaMotionPass writes the frame's motion vectors: the moving objects' over
// the scene target's object-motion attachment, then the dilated whole into
// taa.motion. After every pass that writes the world's depth, before the
// viewmodel clears it.
func taaMotionPass() {
	if !taaActive() {
		return
	}
	ProfileBegin("taa: motion")
	defer ProfileEnd()
	t := &taa
	if !t.prevValid {
		t.prevViewProj = t.viewProj
	}
	clear(t.next)
	o := &t.object
	drew := false
	for i := range t.draws {
		d := &t.draws[i]
		t.next[d.key] = d.model
		prev, ok := t.prev[d.key]
		if !ok || prev == d.model {
			// Still, or new this frame: the camera's motion is the whole of
			// it (a new thing has no last frame to come from).
			continue
		}
		if !drew {
			drew = true
			drawBuffersOnly(sceneAttachVelocity)
			gl.DepthMask(false)
			gl.DepthFunc(gl.LEQUAL)
			gl.Enable(gl.POLYGON_OFFSET_FILL)
			gl.PolygonOffset(-1, -1)
			gl.UseProgram(o.prog)
			gl.UniformMatrix4fv(o.U("projection"), 1, false, &gfx.Projection[0])
			gl.UniformMatrix4fv(o.U("camera"), 1, false, &gfx.Cam.Matrix[0])
			gl.UniformMatrix4fv(o.U("viewProj"), 1, false, &t.viewProj[0])
			gl.UniformMatrix4fv(o.U("prevViewProj"), 1, false, &t.prevViewProj[0])
		}
		gl.UniformMatrix4fv(o.U("model"), 1, false, &d.model[0])
		gl.UniformMatrix4fv(o.U("prevModel"), 1, false, &prev[0])
		switch {
		case d.geom != nil:
			gfx.ApplySkinUniforms(&t.objSkin, o.prog, d.geom.BonePalette)
			gl.BindVertexArray(d.geom.Vao)
			d.geom.DrawTriangles()
		case d.cb != nil:
			gfx.ApplySkinUniforms(&t.objSkin, o.prog, nil)
			for _, g := range d.cb.groups {
				if g.batch.Vao == 0 || g.batch.Verts == 0 || g.batch.IsTransparent() {
					continue
				}
				gl.BindVertexArray(g.batch.Vao)
				gl.DrawArrays(gl.TRIANGLES, 0, g.batch.Verts)
			}
		case d.batch != nil:
			gfx.ApplySkinUniforms(&t.objSkin, o.prog, nil)
			gl.BindVertexArray(d.batch.Vao)
			gl.DrawArrays(gl.TRIANGLES, d.first, d.count)
		}
	}
	t.prev, t.next = t.next, t.prev
	if drew {
		gl.Disable(gl.POLYGON_OFFSET_FILL)
		gl.DepthMask(true)
		drawBuffersColour()
	}

	// The camera's motion and the dilation, into taa.motion.
	v := &t.velocity
	gl.BindFramebuffer(gl.FRAMEBUFFER, t.motionFBO)
	gl.Viewport(0, 0, t.rw, t.rh)
	gl.Disable(gl.DEPTH_TEST)
	gl.DepthMask(false)
	gl.UseProgram(v.prog)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, scene.depth)
	gl.ActiveTexture(gl.TEXTURE1)
	gl.BindTexture(gl.TEXTURE_2D, scene.velocity)
	gl.Uniform1i(v.U("sceneDepth"), 0)
	gl.Uniform1i(v.U("objectMotion"), 1)
	gl.UniformMatrix4fv(v.U("invViewProj"), 1, false, &t.invJitVP[0])
	gl.UniformMatrix4fv(v.U("viewProj"), 1, false, &t.viewProj[0])
	gl.UniformMatrix4fv(v.U("prevViewProj"), 1, false, &t.prevViewProj[0])
	gl.Uniform2i(v.U("size"), t.rw, t.rh)
	gl.BindVertexArray(t.vao)
	gl.DrawArrays(gl.TRIANGLES, 0, 3)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	gl.BindVertexArray(0)
	gl.Enable(gl.DEPTH_TEST)
	gl.DepthMask(true)
	gl.BindFramebuffer(gl.FRAMEBUFFER, scene.fbo)
	gl.Viewport(0, 0, t.rw, t.rh)
	gl.UseProgram(gfx.Program)
}

// taaViewmodelMotion zeroes the motion under the viewmodel, which moves with
// the camera: drawn from the same attachments RenderViewmodel draws, at the
// depth it left, into taa.motion. After RenderViewmodel.
func taaViewmodelMotion() {
	e := world.PlayerEntity
	if !taaActive() || e == nil || !e.Visible || len(e.Models) == 0 {
		return
	}
	t := &taa
	o := &t.object
	proj := gfx.ViewmodelProjection()
	base := gfx.Cam.Matrix.Inv()
	// A framebuffer of the motion and the scene's depth, which is the
	// viewmodel's own now.
	gl.BindFramebuffer(gl.FRAMEBUFFER, t.motionFBO)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.DEPTH_STENCIL_ATTACHMENT, gl.TEXTURE_2D, scene.depth, 0)
	gl.DepthMask(false)
	gl.DepthFunc(gl.LEQUAL)
	gl.Enable(gl.POLYGON_OFFSET_FILL)
	gl.PolygonOffset(-1, -1)
	gl.UseProgram(o.prog)
	// The same lens for now and then, and the same matrix: zero.
	gl.UniformMatrix4fv(o.U("projection"), 1, false, &proj[0])
	gl.UniformMatrix4fv(o.U("camera"), 1, false, &gfx.Cam.Matrix[0])
	vp := proj.Mul4(gfx.Cam.Matrix)
	gl.UniformMatrix4fv(o.U("viewProj"), 1, false, &vp[0])
	gl.UniformMatrix4fv(o.U("prevViewProj"), 1, false, &vp[0])
	for mi := range e.Models {
		m := &e.Models[mi]
		if !entityDrawable(e, m) {
			continue
		}
		model := base.Mul4(e.AttachmentMatrix(m))
		gl.UniformMatrix4fv(o.U("model"), 1, false, &model[0])
		gl.UniformMatrix4fv(o.U("prevModel"), 1, false, &model[0])
		for si := range m.Submeshes {
			g := &m.Submeshes[si]
			if len(g.Vertices) == 0 || g.Vao == 0 {
				continue
			}
			gfx.ApplySkinUniforms(&t.objSkin, o.prog, g.BonePalette)
			gl.BindVertexArray(g.Vao)
			g.DrawTriangles()
		}
	}
	gl.BindVertexArray(0)
	gl.Disable(gl.POLYGON_OFFSET_FILL)
	gl.DepthMask(true)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.DEPTH_STENCIL_ATTACHMENT, gl.TEXTURE_2D, 0, 0)
	gl.BindFramebuffer(gl.FRAMEBUFFER, scene.fbo)
	gl.UseProgram(gfx.Program)
}

// sceneFrameEnd resolves the scene target into the caller's target and
// hands the frame back to it: the TAA resolve and the sharpened output, or
// (TAA off) the frame as drawn. Restores the unjittered projection.
func sceneFrameEnd(lvl *world.Level, now float64) {
	if !taa.active {
		return
	}
	t := &taa
	jp := t.jitter // the resolve's, before taaEndJitter forgets it
	taaEndJitter()
	ProfileBegin("taa: resolve")
	gl.Disable(gl.DEPTH_TEST)
	gl.DepthMask(false)
	gl.Disable(gl.BLEND)
	gl.BindVertexArray(t.vao)
	src := scene.colour
	t.resolvedWithHistory = t.on && t.historyValid
	if t.on {
		next := 1 - t.cur
		r := &t.resolve
		gl.BindFramebuffer(gl.FRAMEBUFFER, t.historyFBO[next])
		gl.Viewport(0, 0, t.w, t.h)
		gl.UseProgram(r.prog)
		bindTex := func(unit uint32, tex uint32, name string) {
			gl.ActiveTexture(gl.TEXTURE0 + unit)
			gl.BindTexture(gl.TEXTURE_2D, tex)
			gl.Uniform1i(r.U(name), int32(unit))
		}
		bindTex(0, scene.colour, "current")
		bindTex(1, t.history[t.cur], "history")
		bindTex(2, t.motion, "motion")
		gl.Uniform1i(r.U("historyValid"), boolToInt32(t.historyValid))
		gl.Uniform2f(r.U("size"), float32(t.w), float32(t.h))
		gl.Uniform1f(r.U("blendCurrent"), taaBlendCurrent)
		// The upsampling resolve (taa_resolve_frag.glsl's resolveUpscaled):
		// the world's size and this frame's jitter in its pixels.
		gl.Uniform1i(r.U("upscale"), boolToInt32(t.upscaling()))
		gl.Uniform2f(r.U("renderSize"), float32(t.rw), float32(t.rh))
		gl.Uniform2f(r.U("jitterPx"), jp[0]*float32(t.rw)*0.5, jp[1]*float32(t.rh)*0.5)
		gl.DrawArrays(gl.TRIANGLES, 0, 3)
		t.cur = next
		t.historyValid = true
		src = t.history[next]
	}
	ProfileEnd()
	dynResEnd()

	// The HDR frame's glow and its metering for the eye's adaptation
	// (bloom.go, exposure.go), from the resolved picture before the curve.
	hdr := gfx.Scene.Block.HdrMode != 0
	var bloomTex uint32
	if hdr {
		if gfx.Bloom {
			bloomTex = runBloom(src, t.w, t.h)
		}
		ProfileBegin("exposure")
		meterExposure(src, lvl, now)
		ProfileEnd()
		gl.BindVertexArray(t.vao)
		gl.Disable(gl.DEPTH_TEST)
		gl.DepthMask(false)
	}

	ProfileBegin("taa: output")
	op := &t.output
	f := t.final
	gl.BindFramebuffer(gl.FRAMEBUFFER, f.FBO)
	gl.Viewport(f.Viewport[0], f.Viewport[1], f.Viewport[2], f.Viewport[3])
	gl.UseProgram(op.prog)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, src)
	gl.Uniform1i(op.U("src"), 0)
	sharpen := gfx.TAASharpen
	if t.upscaling() {
		sharpen = upscaleSharpen(sharpen, float32(t.rw)/float32(t.w))
	}
	if !t.on {
		sharpen = 0
	}
	gl.Uniform1f(op.U("sharpen"), sharpen)
	gl.Uniform2i(op.U("size"), t.w, t.h)
	gl.Uniform2i(op.U("origin"), f.Viewport[0], f.Viewport[1])
	// The tone curve, when the frame shaded in HDR (gfx/hdr.go): the scene
	// block says so, as world.PackFog wrote it for this frame.
	gl.Uniform1i(op.U("tonemap"), boolToInt32(hdr))
	// And the glow, mixed in before it (bloom.go).
	gl.Uniform1i(op.U("bloomOn"), boolToInt32(bloomTex != 0))
	if bloomTex != 0 {
		gl.ActiveTexture(gl.TEXTURE2)
		gl.BindTexture(gl.TEXTURE_2D, bloomTex)
		gl.Uniform1i(op.U("bloomTex"), 2)
		gl.Uniform1f(op.U("bloomStrength"), BloomStrength)
		gl.Uniform1f(op.U("bloomScale"), 1/float32(bloom.levels))
		gl.ActiveTexture(gl.TEXTURE0)
	}
	gl.Uniform1f(op.U("exposure"), gfx.Scene.Block.HdrExposure)
	// The eye's adaptation, on the GPU (exposure.go), for a level that meters.
	_, _, auto := world.LevelHDR(lvl)
	bindExposureTo(op, hdr && auto)
	// The editor's view: the scene's depth into the caller's depth buffer
	// as well (editor_scene_frame.go). A depth write needs the test on;
	// ALWAYS makes it a copy.
	gl.Uniform1i(op.U("writeDepth"), boolToInt32(t.editorDepth))
	if t.editorDepth {
		gl.ActiveTexture(gl.TEXTURE1)
		gl.BindTexture(gl.TEXTURE_2D, scene.depth)
		gl.Uniform1i(op.U("depthSrc"), 1)
		gl.Enable(gl.DEPTH_TEST)
		gl.DepthFunc(gl.ALWAYS)
		gl.DepthMask(true)
	}
	gl.DrawArrays(gl.TRIANGLES, 0, 3)
	if t.editorDepth {
		gl.DepthFunc(gl.LEQUAL)
		gl.Disable(gl.DEPTH_TEST)
		gl.DepthMask(false)
	}
	ProfileEnd()

	for u := uint32(0); u < 3; u++ {
		gl.ActiveTexture(gl.TEXTURE0 + u)
		gl.BindTexture(gl.TEXTURE_2D, 0)
	}
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindVertexArray(0)
	gl.Enable(gl.DEPTH_TEST)
	gl.DepthMask(true)
	gl.UseProgram(gfx.Program)

	t.prevViewProj, t.prevValid = t.viewProj, true
	t.prevCam, t.prevProjection = gfx.Cam.Position, t.baseProjection
	t.active = false
	gfx.HDRFrame = false
	drawTarget.t = f
}
