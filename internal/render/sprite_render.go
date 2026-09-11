package render

// sprite_render.go — the instanced billboard pass.
//
// One quad, four vertices, drawn once per instance with glDrawArraysInstanced.
// It serves level sprites now and will serve particles unchanged: both are a
// textured, tinted, rolled quad turned to face the viewer, and they differ only
// in where the transform comes from — authored, or simulated.
//
// THIS IS THE ENGINE'S FIRST USE OF PER-INSTANCE VERTEX ATTRIBUTES. Every other
// instanced draw here (biome scatter in grass.go, detail scatter in
// detail_render.go) derives placement procedurally in the vertex shader from
// gl_InstanceID plus a uniform lattice. That idiom is deliberate and stays where
// it is, but it can only express placement that is a pure function of position
// and seed — it cannot carry state that something simulated. So this pass
// streams a real instance buffer, with glVertexAttribDivisor, which GL 3.3
// added and the engine's 4.1 core target has had all along.
//
// Buffer handling is gizmo.GizmoFlush's: one STREAM_DRAW buffer, orphaned with
// a nil BufferData and refilled each frame, so the driver never stalls waiting
// for the previous frame's draw to finish reading it.
//
// Three programs are built from the same vertex source (sprite_common.glsl,
// injected into all three) for the reason biome_scatter_common.glsl states: a
// depth stage that computed its own placement would detach a sprite's shadow
// from the sprite. It bites harder here than it does for grass, because a
// billboard's orientation depends on where it is being looked at FROM.

import (
	"fmt"
	"foundry/internal/gfx"
	"foundry/internal/particle"
	"foundry/internal/world"
	"math"
	"sort"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// spriteInstanceFloats is the per-instance record's width. Must agree with the
// layout(location = 1..5) block in sprite_common.glsl.
//
//	pos   vec3   3
//	size  vec2   2
//	rot   float  1
//	color vec4   4
//	uv    vec4   4
const spriteInstanceFloats = 14

// spriteAlphaCutoff is where a cutout sprite stops being drawn. Matched to
// fragment.glsl's own so a sprite and a cutout wall dissolve at the same place.
const spriteAlphaCutoff = 0.5

// spriteModeFadingCutout is sprite_frag.glsl's blendMode for a cutout sprite
// whose Alpha is below 1 — see spriteBatch.FadingCutout. It sits after the
// three gfx.SpriteBlend values rather than among them, because it is how the
// renderer draws a cutout sprite, not a mode anyone authors.
const spriteModeFadingCutout = 3

// spriteMaxInstances caps one draw call. Well above what a level of authored
// sprites reaches; particles will batch against it rather than grow the buffer
// without limit.
const spriteMaxInstances = 65536

// spriteProgram is one linked program plus the uniform locations it uses. The
// three variants differ only in which stages were linked, so one struct serves.
type spriteProgram struct {
	prog uint32

	projView      int32
	viewPos       int32
	viewDir       int32
	directional   int32
	billboardMode int32
	fixedRight    int32
	fixedUp       int32

	// Colour pass only; -1 on the depth programs, which gl.Uniform* ignores.
	tex            int32
	blendMode      int32
	alphaCutoff    int32
	lit            int32
	receiveShadows int32
	farPlane       int32
	lightPos       int32
}

// spriteRenderer owns the pass's GL objects. One of everything: the quad, the
// instance buffer, and the three programs.
type spriteRenderer struct {
	ready bool

	vao uint32 // quad + instance attribute bindings
	qbo uint32 // the static unit quad
	ibo uint32 // the streamed instance buffer

	color spriteProgram
	dir   spriteProgram
	cube  spriteProgram

	// scratch is the CPU side of the instance buffer, reused every frame so a
	// level full of sprites allocates nothing per frame.
	scratch []float32
	// batches and pass are reused for the same reason.
	batches []spriteBatch
	pass    []spriteBatch
	// fill is the staging buffer a particle layer is filled into before being
	// appended to a batch. Grown once and reused.
	fill []particle.Instance
	// preview is the same for DrawSpriteInstances, kept apart so the editor's
	// preview cannot stomp on a level draw mid-frame.
	preview []SpriteInstance
}

var sprites spriteRenderer

// SpriteInstance is one quad's worth of state, in the order the shader reads it.
// Built by the level walk below, and later by the particle simulation.
type SpriteInstance struct {
	Pos   mgl32.Vec3
	Size  mgl32.Vec2
	Rot   float32
	Color mgl32.Vec4
	UV    mgl32.Vec4 // u0, v0, du, dv
}

// spriteBatch is a run of instances that can go out in one draw: everything the
// shader reads from a uniform rather than an attribute has to match.
type spriteBatch struct {
	Texture   string
	Blend     gfx.SpriteBlend
	Billboard gfx.BillboardMode
	Lit       bool
	// FadingCutout marks a cutout sprite whose Alpha is below 1. Cutout has no
	// partial opacity — its alpha is a threshold, so a fade would show nothing
	// until the threshold and then the whole sprite — so one mid-fade draws in
	// the alpha pass instead: Blend reads SpriteBlendAlpha, and the shader still
	// cuts against the texture's alpha before blending by the sprite's.
	FadingCutout bool
	// FixedRight/FixedUp are read only for BillboardFixed, which is why a fixed
	// sprite batches alone — its basis is authored per sprite. Fixed sprites are
	// signs and posters, so one draw each is a price worth paying to keep the
	// hot path (particles, all sharing a mode) at one draw per texture.
	FixedRight mgl32.Vec3
	FixedUp    mgl32.Vec3
	// Depth is the batch's distance from the camera, for ordering the alpha
	// batches back to front.
	Depth     float32
	Instances []SpriteInstance
}

// ---------- init ----------

// SpriteInit builds the quad, the instance buffer and the three programs.
// Called from InitWorldPasses alongside GrassInit and SkyInit.
func SpriteInit() {
	if sprites.ready {
		return
	}

	// The unit quad, in [-0.5, 0.5]. A triangle strip, so four vertices with no
	// index buffer — which matches the rest of the engine, where nothing is
	// indexed at draw time.
	quad := []float32{
		-0.5, -0.5,
		0.5, -0.5,
		-0.5, 0.5,
		0.5, 0.5,
	}

	gl.GenVertexArrays(1, &sprites.vao)
	gl.BindVertexArray(sprites.vao)

	gl.GenBuffers(1, &sprites.qbo)
	gl.BindBuffer(gl.ARRAY_BUFFER, sprites.qbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(quad)*4, gl.Ptr(quad), gl.STATIC_DRAW)
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointer(0, 2, gl.FLOAT, false, 2*4, nil)

	gl.GenBuffers(1, &sprites.ibo)
	gl.BindBuffer(gl.ARRAY_BUFFER, sprites.ibo)
	gl.BufferData(gl.ARRAY_BUFFER, spriteMaxInstances*spriteInstanceFloats*4, nil, gl.STREAM_DRAW)

	specifySpriteAttribs()

	gl.BindVertexArray(0)
	gl.BindBuffer(gl.ARRAY_BUFFER, 0)

	common := mustReadShader(biomeShaderPath("sprite_common.glsl"))
	// The colour stage takes the full lighting block; a lit sprite is a
	// fragment-lit surface, unlike foliage, so it pays for a 3x3 filter and
	// point-light shadows. Cascade blending stays off — a quad is too small to
	// show the seam, exactly as a blade is.
	light := biomeLightSnippet(1, true, false)

	sprites.color = mustSpriteProgram(
		biomeShaderPath("sprite_vert.glsl"), biomeShaderPath("sprite_frag.glsl"),
		common, light, true)
	sprites.dir = mustSpriteProgram(
		biomeShaderPath("sprite_shadow_vert.glsl"), biomeShaderPath("shadow_frag.glsl"),
		common, "", false)
	sprites.cube = mustSpriteProgram(
		biomeShaderPath("sprite_cube_vert.glsl"), biomeShaderPath("shadow_cube_frag.glsl"),
		common, "", false)

	sprites.ready = true
	gl.UseProgram(gfx.Program)
}

// SpriteVAOOverride, when non-zero, is the vertex array the pass binds instead
// of its own. VAOs are never shared between GL contexts, so the particle
// editor's preview supplies one of its own for the duration of its frame — the
// arrangement gizmo.GizmoVAOOverride uses, and for the same reason. The buffers
// and the programs are shared objects and need no such treatment.
//
// A bare VAO carries no attribute state, so bindSpriteVAO re-specifies the
// pointers whenever an override is in force.
var SpriteVAOOverride uint32

// specifySpriteAttribs points the currently-bound VAO at the quad and the
// instance buffer. Locations 1..5 are per-instance; the offsets walk the record
// in sprite_common.glsl's declared order, so changing one means changing both.
func specifySpriteAttribs() {
	gl.BindBuffer(gl.ARRAY_BUFFER, sprites.qbo)
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointer(0, 2, gl.FLOAT, false, 2*4, nil)

	gl.BindBuffer(gl.ARRAY_BUFFER, sprites.ibo)
	stride := int32(spriteInstanceFloats * 4)
	type attr struct {
		loc    uint32
		size   int32
		offset int
	}
	for _, a := range []attr{
		{1, 3, 0},  // iPos
		{2, 2, 12}, // iSize
		{3, 1, 20}, // iRot
		{4, 4, 24}, // iColor
		{5, 4, 40}, // iUV
	} {
		gl.EnableVertexAttribArray(a.loc)
		gl.VertexAttribPointerWithOffset(a.loc, a.size, gl.FLOAT, false, stride, uintptr(a.offset))
		gl.VertexAttribDivisor(a.loc, 1)
	}
}

// bindSpriteVAO binds the pass's vertex array, or the override with its
// attributes re-specified.
func bindSpriteVAO() {
	if SpriteVAOOverride != 0 {
		gl.BindVertexArray(SpriteVAOOverride)
		specifySpriteAttribs()
		return
	}
	gl.BindVertexArray(sprites.vao)
}

// mustSpriteProgram links one vertex + fragment pair, injecting the shared
// placement code into the vertex stage and (for the colour pass) the lighting
// block into the fragment stage. Panics on failure, like the biome and detail
// programs it mirrors — a shader that will not compile is a build error.
func mustSpriteProgram(vertPath, fragPath, vertSnippet, fragSnippet string, colorPass bool) spriteProgram {
	vs, err := gfx.CompileShader(gfx.InjectAfterVersion(mustReadShader(vertPath), vertSnippet), gl.VERTEX_SHADER)
	if err != nil {
		panic(fmt.Sprintf("sprite vert %s: %v", vertPath, err))
	}
	fsrc := mustReadShader(fragPath)
	if fragSnippet != "" {
		fsrc = gfx.InjectAfterVersion(fsrc, fragSnippet)
	}
	fs, err := gfx.CompileShader(fsrc, gl.FRAGMENT_SHADER)
	if err != nil {
		panic(fmt.Sprintf("sprite frag %s: %v", fragPath, err))
	}
	prog, err := linkStages(vs, fs)
	if err != nil {
		panic(fmt.Sprintf("sprite program (%s + %s): %v", vertPath, fragPath, err))
	}

	p := spriteProgram{
		prog:           prog,
		projView:       world.ProgUniformLoc(prog, "projView\x00"),
		viewPos:        world.ProgUniformLoc(prog, "viewPos\x00"),
		viewDir:        world.ProgUniformLoc(prog, "viewDir\x00"),
		directional:    world.ProgUniformLoc(prog, "directional\x00"),
		billboardMode:  world.ProgUniformLoc(prog, "billboardMode\x00"),
		fixedRight:     world.ProgUniformLoc(prog, "fixedRight\x00"),
		fixedUp:        world.ProgUniformLoc(prog, "fixedUp\x00"),
		tex:            -1,
		blendMode:      -1,
		alphaCutoff:    -1,
		lit:            -1,
		receiveShadows: -1,
		// shadow_cube_frag.glsl's own two; -1 on the other programs.
		farPlane: world.ProgUniformLoc(prog, "farPlane\x00"),
		lightPos: world.ProgUniformLoc(prog, "lightPos\x00"),
	}
	if colorPass {
		p.tex = world.ProgUniformLoc(prog, "tex\x00")
		p.blendMode = world.ProgUniformLoc(prog, "blendMode\x00")
		p.alphaCutoff = world.ProgUniformLoc(prog, "alphaCutoff\x00")
		p.lit = world.ProgUniformLoc(prog, "lit\x00")
		p.receiveShadows = world.ProgUniformLoc(prog, "receiveShadows\x00")
	}
	return p
}

// ---------- gathering ----------

// SpriteBatches walks the level's sprites and groups them into draws. cull may
// be nil, which skips frustum culling — what the shadow passes want when they
// have already culled against the light's own volume.
//
// Returned batches alias the renderer's scratch storage and stay valid only
// until the next call, which is what keeps a level of sprites allocation-free
// per frame.
func SpriteBatches(lvl *world.Level, cull *Frustum, camPos mgl32.Vec3, castersOnly bool) []spriteBatch {
	sprites.batches = sprites.batches[:0]
	if lvl == nil {
		return nil
	}
	// Reset here rather than in app.go: the colour pass gathers once a frame,
	// and the shadow passes gather again with castersOnly set. Counting the
	// last gather is what makes the number mean "what is on screen".
	if !castersOnly {
		DebugStats.Sprites, DebugStats.Particles = 0, 0
	}
	for i := range lvl.Sprites {
		s := &lvl.Sprites[i]
		if !s.Drawable() {
			continue
		}
		if castersOnly && !s.CastShadows {
			continue
		}
		pos := s.WorldPosition(lvl)
		if cull != nil {
			min, max, ok := s.WorldAABB(lvl)
			if ok && !AABBInFrustum(*cull, min, max) {
				continue
			}
		}
		if !castersOnly {
			DebugStats.Sprites++
		}
		u0, v0, du, dv := s.UVRect()
		inst := SpriteInstance{
			Pos:   pos,
			Size:  s.DrawnSize(),
			Rot:   s.Rotation.Z(),
			Color: mgl32.Vec4{s.Tint.X(), s.Tint.Y(), s.Tint.Z(), s.Alpha},
			UV:    mgl32.Vec4{u0, v0, du, dv},
		}
		right, up := fixedBasis(s)
		key := spriteBatch{
			Texture:    s.Texture,
			Blend:      s.Blend,
			Billboard:  s.Billboard,
			Lit:        s.Lit,
			FixedRight: right,
			FixedUp:    up,
		}
		if s.Blend == gfx.SpriteBlendCutout && s.Alpha < 1 {
			key.Blend = gfx.SpriteBlendAlpha
			key.FadingCutout = true
		}
		appendSpriteInstance(&sprites.batches, key, inst, camPos)
	}

	gatherEmitters(lvl, cull, camPos, castersOnly)
	return sprites.batches
}

// gatherEmitters adds every live particle to the batches, one layer at a time.
//
// A layer is a batch by construction: every particle in it shares a texture, a
// blend mode and a billboard mode, which is exactly the batch key. So a level
// of a hundred torches sharing one effect is one draw call per layer per
// texture, not one per torch.
func gatherEmitters(lvl *world.Level, cull *Frustum, camPos mgl32.Vec3, castersOnly bool) {
	for ei := range lvl.Emitters {
		e := &lvl.Emitters[ei]
		if !e.Enabled || !e.Loaded() {
			continue
		}
		if cull != nil {
			min, max, ok := e.WorldAABB(lvl)
			if ok && !AABBInFrustum(*cull, min, max) {
				continue
			}
		}
		for li := range e.Systems {
			sys := e.Systems[li]
			if sys.Count() == 0 || sys.Layer.Muted {
				continue
			}
			if castersOnly && !sys.Layer.CastShadows {
				continue
			}
			// Sorted here, inside the layer, rather than by the pass: the pass
			// orders whole batches, and a cloud of alpha particles has to be
			// ordered particle by particle or it composites wrong against
			// itself. Additive and cutout skip it — neither needs an order.
			sortIt := sys.Layer.Blend == gfx.SpriteBlendAlpha
			n := sys.Fill(spriteFillBuf(sys.Count()), camPos, sortIt)
			if n == 0 {
				continue
			}
			if !castersOnly {
				DebugStats.Particles += n
			}
			appendEmitterBatch(sys.Layer, sprites.fill[:n], camPos)
		}
	}
}

// spriteFillBuf hands out the shared fill scratch, grown once and reused, so a
// frame of particles allocates nothing.
func spriteFillBuf(n int) []particle.Instance {
	if cap(sprites.fill) < n {
		sprites.fill = make([]particle.Instance, n)
	}
	return sprites.fill[:n]
}

// appendEmitterBatch files a layer's filled instances into the batch matching
// its appearance.
//
// Whole-slice rather than one at a time: the batch key is fixed for the layer,
// so the per-instance search appendSpriteInstance does would be the same answer
// several thousand times.
func appendEmitterBatch(l particle.Layer, insts []particle.Instance, camPos mgl32.Vec3) {
	key := spriteBatch{
		Texture:   l.Texture,
		Blend:     l.Blend,
		Billboard: l.Billboard,
		Lit:       l.Lit,
	}
	b := findSpriteBatch(&sprites.batches, key)
	for i := range insts {
		in := &insts[i]
		b.Instances = append(b.Instances, SpriteInstance{
			Pos:   in.Pos,
			Size:  in.Size,
			Rot:   in.Rot,
			Color: in.Color,
			UV:    in.UV,
		})
		if d := in.Pos.Sub(camPos).LenSqr(); d > b.Depth {
			b.Depth = d
		}
	}
}

// findSpriteBatch returns the batch matching key, starting one if none exists.
func findSpriteBatch(batches *[]spriteBatch, key spriteBatch) *spriteBatch {
	for i := range *batches {
		b := &(*batches)[i]
		if b.Texture == key.Texture && b.Blend == key.Blend && b.Lit == key.Lit &&
			b.FadingCutout == key.FadingCutout &&
			b.Billboard == key.Billboard && key.Billboard != gfx.BillboardFixed {
			return b
		}
	}
	*batches = append(*batches, key)
	return &(*batches)[len(*batches)-1]
}

// fixedBasis derives the quad's authored basis from a sprite's Euler rotation,
// for BillboardFixed. Unread in the other two modes, but computed
// unconditionally because a branch here would cost more than the two matrix
// multiplies do.
func fixedBasis(s *world.Sprite) (right, up mgl32.Vec3) {
	m := world.EulerMatrixYXZ(s.Rotation)
	r := m.Mul4x1(mgl32.Vec4{1, 0, 0, 0})
	u := m.Mul4x1(mgl32.Vec4{0, 1, 0, 0})
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}, mgl32.Vec3{u.X(), u.Y(), u.Z()}
}

// appendSpriteInstance files inst into the batch matching key, starting one if
// none exists. Linear search, because the batch count is the number of distinct
// (texture, blend, mode) triples in view — a handful, not a dimension worth
// hashing.
func appendSpriteInstance(batches *[]spriteBatch, key spriteBatch, inst SpriteInstance, camPos mgl32.Vec3) {
	d := inst.Pos.Sub(camPos).LenSqr()
	for i := range *batches {
		b := &(*batches)[i]
		if b.Texture != key.Texture || b.Blend != key.Blend || b.Lit != key.Lit ||
			b.FadingCutout != key.FadingCutout || b.Billboard != key.Billboard {
			continue
		}
		// A fixed sprite's basis is a uniform, so two of them can only share a
		// draw when they are oriented alike.
		if key.Billboard == gfx.BillboardFixed &&
			(b.FixedRight != key.FixedRight || b.FixedUp != key.FixedUp) {
			continue
		}
		b.Instances = append(b.Instances, inst)
		if d > b.Depth {
			b.Depth = d
		}
		return
	}
	key.Instances = []SpriteInstance{inst}
	key.Depth = d
	*batches = append(*batches, key)
}

// ---------- drawing ----------

// RenderSpritesOpaque draws the cutout batches. Called inside the opaque block,
// while alpha-to-coverage is still on: a cutout sprite's alpha is coverage, so
// MSAA antialiases the silhouette and nothing has to be sorted.
func RenderSpritesOpaque(lvl *world.Level, projView mgl32.Mat4, camPos mgl32.Vec3, cull *Frustum, frame ShadowFrame) {
	if !spritePassLive(lvl) {
		return
	}
	batches := SpriteBatches(lvl, cull, camPos, false)
	drawSpritePass(lvl, batches, projView, camPos, frame, gfx.SpriteBlendCutout)
}

// RenderSpritesTransparent draws the alpha and additive batches, in that order.
// Called after the world's own transparent pass, so a sprite sorts against
// water and glass rather than in a pass that ignores them.
//
// One gather feeds both modes: walking the level twice would cost twice for an
// answer that cannot have changed between the two calls.
func RenderSpritesTransparent(lvl *world.Level, projView mgl32.Mat4, camPos mgl32.Vec3, cull *Frustum, frame ShadowFrame) {
	if !spritePassLive(lvl) {
		return
	}
	batches := SpriteBatches(lvl, cull, camPos, false)
	drawSpritePass(lvl, batches, projView, camPos, frame, gfx.SpriteBlendAlpha)
	drawSpritePass(lvl, batches, projView, camPos, frame, gfx.SpriteBlendAdditive)
}

// spritePassLive is the cheap "is there anything to do" test both entry points
// take before gathering.
func spritePassLive(lvl *world.Level) bool {
	return sprites.ready && lvl != nil && (len(lvl.Sprites) > 0 || len(lvl.Emitters) > 0)
}

// drawSpritePass draws every batch of one blend mode, setting exactly the GL
// state that mode needs and putting it all back afterwards.
func drawSpritePass(lvl *world.Level, batches []spriteBatch, projView mgl32.Mat4, camPos mgl32.Vec3, frame ShadowFrame, mode gfx.SpriteBlend) {
	sel := sprites.pass[:0]
	for i := range batches {
		if batches[i].Blend == mode && len(batches[i].Instances) > 0 {
			sel = append(sel, batches[i])
		}
	}
	sprites.pass = sel
	if len(sel) == 0 {
		return
	}

	p := &sprites.color
	uploadSpriteLighting(p.prog, lvl, frame)

	gl.UseProgram(p.prog)
	gl.UniformMatrix4fv(p.projView, 1, false, &projView[0])
	gl.Uniform3f(p.viewPos, camPos.X(), camPos.Y(), camPos.Z())
	gl.Uniform1i(p.directional, 0)
	gl.Uniform1f(p.alphaCutoff, spriteAlphaCutoff)
	gl.Uniform1i(p.tex, 0)

	switch mode {
	case gfx.SpriteBlendCutout:
		// Nothing to set. The opaque pass's depth write and alpha-to-coverage
		// are exactly what a cutout wants, and turning either off here would
		// undo the reason cutout is the default.
	case gfx.SpriteBlendAlpha:
		// Back to front, so overlapping translucent quads composite correctly.
		// Per batch rather than per instance: within one texture and one mode
		// the instances are almost always a single effect, and sorting each
		// quad is the particle simulation's job when it starts producing them.
		sort.SliceStable(sel, func(a, b int) bool { return sel[a].Depth > sel[b].Depth })
		gl.Enable(gl.BLEND)
		gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
		gl.DepthMask(false)
	case gfx.SpriteBlendAdditive:
		// Order-independent, so no sort at all — the one real advantage
		// additive has over alpha, and the reason fire and sparks should use it.
		gl.Enable(gl.BLEND)
		gl.BlendFunc(gl.SRC_ALPHA, gl.ONE)
		gl.DepthMask(false)
	}

	bindSpriteVAO()
	for i := range sel {
		drawSpriteBatch(p, &sel[i])
		DebugStats.SpriteDraws++
	}
	gl.BindVertexArray(0)

	if mode != gfx.SpriteBlendCutout {
		gl.DepthMask(true)
		gl.Disable(gl.BLEND)
	}
	gl.ActiveTexture(gl.TEXTURE0)
	gl.UseProgram(gfx.Program)
}

// drawSpriteBatch uploads one batch's instances and issues its draw.
func drawSpriteBatch(p *spriteProgram, b *spriteBatch) {
	n := len(b.Instances)
	if n == 0 {
		return
	}
	if n > spriteMaxInstances {
		// Truncate rather than grow: the cap is far above what a level reaches,
		// and silently reallocating a stream buffer mid-frame is worse than
		// dropping the tail of one effect.
		n = spriteMaxInstances
	}

	if p.lit >= 0 {
		gl.Uniform1i(p.lit, boolToInt32(b.Lit))
	}
	if p.blendMode >= 0 {
		// Per batch rather than per pass: a fading cutout shares the alpha
		// pass with true alpha batches but not their shader path.
		mode := int32(b.Blend)
		if b.FadingCutout {
			mode = spriteModeFadingCutout
		}
		gl.Uniform1i(p.blendMode, mode)
	}
	gl.Uniform1i(p.billboardMode, int32(b.Billboard))
	gl.Uniform3f(p.fixedRight, b.FixedRight.X(), b.FixedRight.Y(), b.FixedRight.Z())
	gl.Uniform3f(p.fixedUp, b.FixedUp.X(), b.FixedUp.Y(), b.FixedUp.Z())

	if p.tex >= 0 {
		gl.ActiveTexture(gl.TEXTURE0)
		gl.BindTexture(gl.TEXTURE_2D, gfx.Textures[b.Texture])
	}

	uploadSpriteInstances(b.Instances[:n])
	gl.DrawArraysInstanced(gl.TRIANGLE_STRIP, 0, 4, int32(n))
}

// uploadSpriteInstances packs the batch into the stream buffer.
//
// The nil BufferData first is buffer orphaning: it tells the driver the old
// contents are dead, so it can hand back fresh storage instead of stalling
// until the previous frame's draw has finished reading the old. Same reasoning
// as gizmo.GizmoFlush.
func uploadSpriteInstances(insts []SpriteInstance) {
	need := len(insts) * spriteInstanceFloats
	if cap(sprites.scratch) < need {
		sprites.scratch = make([]float32, need)
	}
	buf := sprites.scratch[:need]
	for i, s := range insts {
		o := i * spriteInstanceFloats
		buf[o+0], buf[o+1], buf[o+2] = s.Pos.X(), s.Pos.Y(), s.Pos.Z()
		buf[o+3], buf[o+4] = s.Size.X(), s.Size.Y()
		buf[o+5] = s.Rot
		buf[o+6], buf[o+7], buf[o+8], buf[o+9] = s.Color.X(), s.Color.Y(), s.Color.Z(), s.Color.W()
		buf[o+10], buf[o+11], buf[o+12], buf[o+13] = s.UV.X(), s.UV.Y(), s.UV.Z(), s.UV.W()
	}
	gl.BindBuffer(gl.ARRAY_BUFFER, sprites.ibo)
	gl.BufferData(gl.ARRAY_BUFFER, spriteMaxInstances*spriteInstanceFloats*4, nil, gl.STREAM_DRAW)
	gl.BufferSubData(gl.ARRAY_BUFFER, 0, need*4, gl.Ptr(buf))
}

// uploadSpriteLighting sends the scene lighting and shadow state to the colour
// program. The same three calls uploadBiomeLighting makes, for the same reason:
// biome_light.glsl declares fragment.glsl's own uniform names, so there is no
// sprite-specific light path to keep in step with the main shader's.
func uploadSpriteLighting(prog uint32, lvl *world.Level, frame ShadowFrame) {
	world.UploadLightsTo(prog, lvl.Lights, false)
	world.UploadFogTo(prog, lvl.Fog, lvl.Volumes, lvl)
	UploadShadowUniformsTo(prog, lvl.Lights, frame)
	UploadPointShadowUniformsTo(prog, lvl.Lights)

	gl.UseProgram(prog)
	cam := gfx.Cam.Matrix
	gl.UniformMatrix4fv(world.ProgUniformLoc(prog, "camera\x00"), 1, false, &cam[0])
	gl.Uniform1i(world.ProgUniformLoc(prog, "receiveShadows\x00"), boolToInt32(BiomeShadows.Receive))
}

// ---------- shadow passes ----------

// RenderSpriteDirShadow draws shadow-casting sprites into one cascade layer.
// Called from RenderShadowPass beside RenderBiomeScatterDirShadow, and it
// restores that pass's program on the way out, as endBiomePass does.
//
// lightDir is the direction the light travels; the quads turn to face back
// along it rather than toward the camera, which is what makes the shadow the
// silhouette of the quad rather than of its edge.
func RenderSpriteDirShadow(lvl *world.Level, lsm mgl32.Mat4, lightDir mgl32.Vec3, camPos mgl32.Vec3, cull *Frustum) {
	drawSpriteShadow(lvl, &sprites.dir, lsm, camPos, cull, func(p *spriteProgram) {
		gl.Uniform1i(p.directional, 1)
		gl.Uniform3f(p.viewDir, lightDir.X(), lightDir.Y(), lightDir.Z())
	}, Shadows.Program)
}

// RenderSpriteCubeShadow is the same for one face of a point light's cubemap.
// Here the light has a position, so the quads converge on it exactly as they
// converge on the eye in the colour pass.
func RenderSpriteCubeShadow(lvl *world.Level, lsm mgl32.Mat4, lightPos mgl32.Vec3, farPlane float32, cull *Frustum) {
	drawSpriteShadow(lvl, &sprites.cube, lsm, lightPos, cull, func(p *spriteProgram) {
		gl.Uniform1i(p.directional, 0)
		gl.Uniform3f(p.viewPos, lightPos.X(), lightPos.Y(), lightPos.Z())
		if p.lightPos >= 0 {
			gl.Uniform3f(p.lightPos, lightPos.X(), lightPos.Y(), lightPos.Z())
		}
		if p.farPlane >= 0 {
			gl.Uniform1f(p.farPlane, farPlane)
		}
	}, Shadows.CubeProgram)
}

// drawSpriteShadow is the shared body of the two depth passes.
func drawSpriteShadow(lvl *world.Level, p *spriteProgram, lsm mgl32.Mat4, viewPos mgl32.Vec3, cull *Frustum, setup func(*spriteProgram), restore uint32) {
	if !spritePassLive(lvl) {
		return
	}
	batches := SpriteBatches(lvl, cull, viewPos, true)
	if len(batches) == 0 {
		return
	}

	gl.UseProgram(p.prog)
	gl.UniformMatrix4fv(p.projView, 1, false, &lsm[0])
	gl.Uniform3f(p.viewPos, viewPos.X(), viewPos.Y(), viewPos.Z())
	setup(p)

	bindSpriteVAO()
	for i := range batches {
		// A depth pass has no texture and no blend, so a batch's only remaining
		// uniform is its billboard mode and basis.
		drawSpriteBatch(p, &batches[i])
	}
	gl.BindVertexArray(0)

	// Hand the interrupted pass its program back, exactly as endBiomePass does.
	gl.UseProgram(restore)
}

// ---------- teardown ----------

// DeleteSprites releases the pass's GL objects. Nothing calls it in the running
// engine — the pass outlives every level — but it exists so a test that stands
// up a context can tear one down.
func DeleteSprites() {
	if !sprites.ready {
		return
	}
	gl.DeleteVertexArrays(1, &sprites.vao)
	gl.DeleteBuffers(1, &sprites.qbo)
	gl.DeleteBuffers(1, &sprites.ibo)
	for _, p := range []uint32{sprites.color.prog, sprites.dir.prog, sprites.cube.prog} {
		gl.DeleteProgram(p)
	}
	sprites = spriteRenderer{}
}

// spriteRollWrap keeps a roll in [-pi, pi] so a long-lived spinning particle's
// float does not lose precision. Unused by authored sprites; here because the
// simulation will want it.
func spriteRollWrap(r float32) float32 {
	const twoPi = 2 * math.Pi
	for r > math.Pi {
		r -= twoPi
	}
	for r < -math.Pi {
		r += twoPi
	}
	return r
}

// ---------- the particle editor's preview ----------

// DrawSpriteInstances draws one batch of instances directly, outside the level
// walk. The particle editor's preview goes through here, so an effect being
// authored is put on screen by exactly the code that will draw it in the game
// rather than by a second implementation that can drift from it.
//
// Unlit and shadowless by design: the preview has no scene lighting to take,
// and a lit effect authored against a black void would be dialled in wrong.
// What it does share is the shader, the blend state per mode, and the billboard
// basis — everything that decides what the effect actually looks like.
//
// Safe to call from a secondary window: the VAO, buffer and programs live in
// the main context, which every uiwin window shares.
func DrawSpriteInstances(insts []particle.Instance, texture string, blend gfx.SpriteBlend, billboard gfx.BillboardMode, projView mgl32.Mat4, camPos mgl32.Vec3) {
	if !sprites.ready || len(insts) == 0 {
		return
	}
	if err := gfx.EnsureTextureLoaded(texture); err != nil {
		return
	}

	b := spriteBatch{Texture: texture, Blend: blend, Billboard: billboard}
	b.Instances = sprites.preview[:0]
	for i := range insts {
		b.Instances = append(b.Instances, SpriteInstance{
			Pos:   insts[i].Pos,
			Size:  insts[i].Size,
			Rot:   insts[i].Rot,
			Color: insts[i].Color,
			UV:    insts[i].UV,
		})
	}
	sprites.preview = b.Instances

	p := &sprites.color
	gl.UseProgram(p.prog)
	gl.UniformMatrix4fv(p.projView, 1, false, &projView[0])
	gl.Uniform3f(p.viewPos, camPos.X(), camPos.Y(), camPos.Z())
	gl.Uniform1i(p.directional, 0)
	gl.Uniform1f(p.alphaCutoff, spriteAlphaCutoff)
	gl.Uniform1i(p.tex, 0)
	gl.Uniform1i(p.lit, 0)

	switch blend {
	case gfx.SpriteBlendCutout:
		// Depth-writing and unblended, as in the opaque pass. No
		// alpha-to-coverage here — this window has no MSAA to hand it to.
		gl.Disable(gl.BLEND)
	case gfx.SpriteBlendAlpha:
		gl.Enable(gl.BLEND)
		gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
		gl.DepthMask(false)
	case gfx.SpriteBlendAdditive:
		gl.Enable(gl.BLEND)
		gl.BlendFunc(gl.SRC_ALPHA, gl.ONE)
		gl.DepthMask(false)
	}

	bindSpriteVAO()
	drawSpriteBatch(p, &b)
	gl.BindVertexArray(0)

	gl.DepthMask(true)
	gl.Disable(gl.BLEND)
	gl.ActiveTexture(gl.TEXTURE0)
}
