package render

// point_shadow_cache.go — not re-rendering a point light's shadow cubemap,
// or any face of it, when nothing that face could show has changed.
//
// A shadowed point light costs six scene walks a frame, one per cube face, and
// in a level lit like a hangar almost none of those frames changes a thing: the
// light is bolted to the ceiling and the walls are walls. So before the walks,
// each light gets signatures — hashes of every input the walks read — and
// whatever matches what it was rendered with is kept as it is.
//
// The signature comes in two halves, because the usual reason a light has to
// render is a character standing in it, not the room changing: the STATIC half
// (the compiled world, the chunks and their brushes), one per light over
// everything whose box reaches its cube, and the MOVING half (entities,
// scenery, sprites, scatter), one per FACE, over what that face's own frustum
// takes in. A light with anything moving in reach keeps a second cubemap
// holding just its static layer; when only some faces' moving halves have
// changed, each of THOSE faces starts from a copy of that and draws the moving
// casters on top — a spark under a ceiling lamp redraws the face looking down,
// not all six, and a walker crossing one side of a room redraws the face or
// two that see it.
//
// The signatures are built from what renderSceneShadow, the sprite and the
// scatter cube passes actually draw, walked under the same predicates with
// the same face frusta (pointShadowFaces), so there is one rule to keep in
// step: ANYTHING NEW THAT A CUBE FACE DRAWS MUST ADD ITS INPUTS HERE, or its
// shadow freezes wherever it was when the face last had a reason to re-render.
// One kind of caster is not hashed but simply marks the light dirty while it
// is in range, because what it draws is not a function of anything this file
// can cheaply read: scatter, whose tiers follow the camera and which the wind
// moves. Particles and sprites ARE hashed, instance by instance, from the same
// gather the pass draws from — a static shadow-casting sprite costs nothing, a
// spark dirties the faces it is in for as long as it lives.
//
// A posed character's skinning palette is not hashed bone by bone: its
// animation counts the times it re-posed (world.EntityAnim.PoseGen), and that
// count is what goes in. Hashing every bone of everyone in reach of every
// light, every frame, was most of this file's cost.

import (
	"math"
	"unsafe"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// pointShadowState is what one point shadow map holds, by the signatures it was
// rendered from. Zero is never a signature (see sigHash), so a fresh map, and a
// face that must render regardless, always renders.
type pointShadowState struct {
	static     uint64    // the static half of what the live cubemap shows
	faces      [6]uint64 // the moving half of what each live face shows
	staticCube uint64    // what the static-layer cubemap holds
}

// pointShadowStates is indexed like Shadows.PointMaps.
var pointShadowStates []pointShadowState

// allPointShadowFaces is every face of a cube, as a pointShadowPlan.faces mask.
const allPointShadowFaces = 1<<6 - 1

// pointShadowPlan is what a light's cubemap needs this frame.
type pointShadowPlan struct {
	// faces are the live faces to redraw, one bit per face (+X, -X, +Y, -Y,
	// +Z, -Z). None: keep the cubemap as it is.
	faces uint8
	// static redraws all six faces of the static-layer cubemap first, for
	// the faces below to start from.
	static bool
	// all draws every layer straight into the faces: the static world changed
	// and nothing in reach moves, so there is no static layer worth keeping
	// apart. Otherwise each face starts from the static layer's copy and
	// draws the moving casters over it.
	all bool
}

// keep reports whether the cubemap is kept whole.
func (p pointShadowPlan) keep() bool { return p.faces == 0 }

// PointShadowCaching is on in play; a test or a debugging session can turn it
// off to compare against rendering every face every frame.
var PointShadowCaching = true

type sigHash uint64

func newSig() sigHash { return 14695981039346656037 }

func (h *sigHash) u64(v uint64)  { *h = sigHash((uint64(*h) ^ v) * 1099511628211) }
func (h *sigHash) f32(v float32) { h.u64(uint64(math.Float32bits(v))) }
func (h *sigHash) vec3(v mgl32.Vec3) {
	h.f32(v[0])
	h.f32(v[1])
	h.f32(v[2])
}
func (h *sigHash) vec4(v mgl32.Vec4) {
	h.f32(v[0])
	h.f32(v[1])
	h.f32(v[2])
	h.f32(v[3])
}
func (h *sigHash) mat(m mgl32.Mat4) {
	for _, f := range m {
		h.f32(f)
	}
}
func (h *sigHash) str(s string) {
	h.u64(uint64(len(s)))
	for i := 0; i < len(s); i++ {
		h.u64(uint64(s[i]))
	}
}
func (h *sigHash) sum() uint64 {
	if *h == 0 {
		return 1
	}
	return uint64(*h)
}

// boxFrustum is the axis-aligned box [lo, hi] as a Frustum, so the culling
// predicates the passes use can be asked about it.
func boxFrustum(lo, hi mgl32.Vec3) Frustum {
	return Frustum{Planes: [6]mgl32.Vec4{
		{1, 0, 0, -lo[0]}, {-1, 0, 0, hi[0]},
		{0, 1, 0, -lo[1]}, {0, -1, 0, hi[1]},
		{0, 0, 1, -lo[2]}, {0, 0, -1, hi[2]},
	}}
}

// pointShadowFaces is a point light's six face matrices and the frusta culled
// against them — the one place both are made, so the signatures and the
// faces' draws cannot disagree about what a face takes in.
func pointShadowFaces(pl world.PointLight) (lsm [6]mgl32.Mat4, cull [6]Frustum) {
	proj := mgl32.Perspective(mgl32.DegToRad(90.0), 1.0, 0.1, pl.Radius)
	views := cubeFaceViews(pl.Position)
	for f := range lsm {
		lsm[f] = proj.Mul4(views[f])
		cull[f] = ExtractFrustum(lsm[f])
	}
	return lsm, cull
}

// faceMask is which of the six frusta take in [lo, hi].
func faceMask(cull *[6]Frustum, lo, hi mgl32.Vec3) uint8 {
	var m uint8
	for f := range cull {
		if AABBInFrustum(cull[f], lo, hi) {
			m |= 1 << f
		}
	}
	return m
}

// geometry covers what drawGeometryShadow reads of one submesh: which
// vertices, whether it is drawn at all, and the pose it is deformed by.
func (h *sigHash) geometry(g *gfx.Geometry) { h.geometryPosed(g, false) }

// geometryPosed is geometry for a submesh whose pose is already in the hash
// when posed is set — by its animation's generation, which the caller hashed
// (world.EntityAnim.PoseGen) — so only which palette it is, not every bone of
// it, goes in here.
func (h *sigHash) geometryPosed(g *gfx.Geometry, posed bool) {
	h.u64(uint64(len(g.Vertices)))
	if len(g.Vertices) > 0 {
		h.u64(uint64(uintptr(unsafe.Pointer(&g.Vertices[0]))))
	}
	// An indexed model draws its index list, so that is part of "which
	// vertices" too.
	h.u64(uint64(len(g.Indices)))
	if len(g.Indices) > 0 {
		h.u64(uint64(uintptr(unsafe.Pointer(&g.Indices[0]))))
	}
	h.f32(g.Alpha)
	h.u64(uint64(len(g.BonePalette)))
	if posed {
		if len(g.BonePalette) > 0 {
			h.u64(uint64(uintptr(unsafe.Pointer(&g.BonePalette[0]))))
		}
		return
	}
	for _, m := range g.BonePalette {
		h.mat(m)
	}
}

// pointShadowSignatures hashes everything a point light's cube faces would
// draw: the static layer, and the moving one face by face with how many
// moving casters are in reach. ok is false when a moving caster in reach
// cannot be hashed and every face's moving layer must be drawn regardless.
func pointShadowSignatures(level *world.Level, pl world.PointLight, cubemap uint32) (static uint64, faces [6]uint64, movers int, ok bool) {
	_, cull := pointShadowFaces(pl)
	return pointShadowSignaturesIn(level, pl, cubemap, &cull)
}

func pointShadowSignaturesIn(level *world.Level, pl world.PointLight, cubemap uint32, cull *[6]Frustum) (static uint64, faces [6]uint64, movers int, ok bool) {
	h := newSig()
	h.u64(uint64(cubemap))
	h.vec3(pl.Position)
	h.f32(pl.Radius)

	r := mgl32.Vec3{pl.Radius, pl.Radius, pl.Radius}
	lo, hi := pl.Position.Sub(r), pl.Position.Add(r)
	box := boxFrustum(lo, hi)

	// The static world: compiled surfaces never change once loaded, so
	// which set it is and what of it is hidden is all there is; brush meshes
	// anywhere change the generation, which catches every edit and load.
	h.u64(world.BrushMeshGeneration())
	h.u64(uint64(uintptr(unsafe.Pointer(level.Static))))
	if level.Static != nil {
		h.u64(level.Static.HiddenVersion())
		h.u64(uint64(len(level.Static.HiddenChunks)))
	}

	// Chunks, under renderSceneShadow's own predicates: which are in range,
	// where each is, and where each of its moved brushes is.
	for ci := range level.Chunks {
		ch := &level.Chunks[ci]
		// Terrain casts from compiled chunks too (see renderSceneShadowLayers),
		// and mining moves no brush mesh, so its generation is the edit.
		if ch.Terrain != nil && ch.IsEnabled() {
			if cmin, cmax := ch.WorldBounds(); AABBInFrustum(box, cmin, cmax) {
				h.u64(uint64(ci))
				h.u64(ch.Terrain.Generation)
				h.mat(ch.ModelMatrix())
			}
		}
		if ch.Compiled && !ch.HasDynamicBrushes() || !ch.IsEnabled() {
			continue
		}
		cmin, cmax := ch.WorldBounds()
		if !AABBInFrustum(box, cmin, cmax) {
			continue
		}
		h.u64(uint64(ci))
		h.str(ch.ID)
		h.mat(ch.ModelMatrix())
		for bi := range ch.Brushes {
			if b := &ch.Brushes[bi]; !b.AtRest() {
				h.u64(uint64(bi))
				h.mat(b.Placement())
			}
		}
	}

	static = h.sum()

	// The moving half, one hash per face. Each caster is hashed once, then
	// folded with its index into every face whose frustum takes it in — the
	// test the face's own draw makes.
	var fh [6]sigHash
	for f := range fh {
		fh[f] = newSig()
	}
	fold := func(mask uint8, idx, sig uint64) {
		for f := range fh {
			if mask&(1<<f) != 0 {
				fh[f].u64(idx)
				fh[f].u64(sig)
			}
		}
	}

	// Entities, under RenderEntitiesShadow's.
	for ei := range level.Entities {
		e := &level.Entities[ei]
		frame := level.Frames.FrameFor(e)
		if !e.Visible || !EntityInFrustumOf(frame, e, &box) {
			continue
		}
		var mask uint8
		for f := range cull {
			if EntityInFrustumOf(frame, e, &cull[f]) {
				mask |= 1 << f
			}
		}
		if mask == 0 {
			continue
		}
		movers++
		eh := newSig()
		eh.mat(e.DrawMatrix())
		if !frame.IsWorld() {
			eh.mat(frame.World)
		}
		for mi := range e.Models {
			m := &e.Models[mi]
			if !entityDrawable(e, m) || m.Unlit {
				continue
			}
			eh.u64(uint64(mi))
			eh.mat(e.AttachmentMatrix(m))
			// The pose, by its generation: which animation, and how many
			// times it has re-posed.
			posed := m.Anim != nil
			if posed {
				eh.u64(uint64(uintptr(unsafe.Pointer(m.Anim))))
				eh.u64(m.Anim.PoseGen)
			}
			for si := range m.Submeshes {
				eh.geometryPosed(&m.Submeshes[si], posed)
			}
		}
		fold(mask, uint64(ei), eh.sum())
	}

	// Scenery, under RenderSceneryShadow's.
	for i := range level.Scenery {
		s := &level.Scenery[i]
		if !s.HasGeometry() || !SceneryInFrustum(s, &box) {
			continue
		}
		var mask uint8
		for f := range cull {
			if SceneryInFrustum(s, &cull[f]) {
				mask |= 1 << f
			}
		}
		if mask == 0 {
			continue
		}
		movers++
		sh := newSig()
		sh.mat(s.ModelMatrix())
		for si := range s.Submeshes {
			sh.geometry(&s.Submeshes[si])
		}
		// Swaying in the wind (nanite_scenery.go): changed every frame.
		if sceneryWindMoving(s) {
			sh.f32(BiomeWindTime)
		}
		fold(mask, 1<<32|uint64(i), sh.sum())
	}

	// Sprites and particles: the very instances the cube pass would gather,
	// each in the faces its bounding sphere reaches. A quad outside a face's
	// frustum draws nothing there, so what one does cannot change that face.
	if spritePassLive(level) {
		for bi, b := range SpriteBatches(level, &box, pl.Position, true) {
			kh := newSig()
			kh.str(b.Texture)
			kh.u64(uint64(b.Blend))
			kh.u64(uint64(b.Billboard))
			if b.FadingCutout {
				kh.u64(1)
			}
			kh.vec3(b.FixedRight)
			kh.vec3(b.FixedUp)
			key := kh.sum()
			for ii, in := range b.Instances {
				// The quad's corners are pos ± half its size along two unit
				// axes (sprite_common.glsl), so half the sum bounds them
				// whatever the axes — billboarded to the light or fixed.
				rad := 0.5 * (float32(math.Abs(float64(in.Size[0]))) + float32(math.Abs(float64(in.Size[1]))))
				ext := mgl32.Vec3{rad, rad, rad}
				mask := faceMask(cull, in.Pos.Sub(ext), in.Pos.Add(ext))
				if mask == 0 {
					continue
				}
				movers++
				ih := newSig()
				ih.u64(key)
				ih.vec3(in.Pos)
				ih.f32(in.Size[0])
				ih.f32(in.Size[1])
				ih.f32(in.Rot)
				ih.vec4(in.Color)
				ih.vec4(in.UV)
				fold(mask, 2<<32|uint64(bi)<<20|uint64(ii), ih.sum())
			}
		}
	}

	// Scatter follows the camera and sways in the wind: any in range and the
	// light renders.
	if scatters.initialized && BiomeShadows.AnyCast() {
		inRange := false
		forEachScatterVolume(level, gfx.Cam.Position, true, pl.Radius, &box, func(*world.Volume) { inRange = true })
		if inRange {
			return static, faces, movers + 1, false
		}
	}
	for f := range fh {
		faces[f] = fh[f].sum()
	}
	return static, faces, movers, true
}

// planPointShadow decides what map idx needs this frame, and records the
// signatures it is about to be rendered from. It makes the map's static-layer
// cubemap the first time one is wanted.
func planPointShadow(idx int, level *world.Level, pl world.PointLight, pm *PointShadowMap) pointShadowPlan {
	_, cull := pointShadowFaces(pl)
	return planPointShadowIn(idx, level, pl, pm, &cull)
}

func planPointShadowIn(idx int, level *world.Level, pl world.PointLight, pm *PointShadowMap, cull *[6]Frustum) pointShadowPlan {
	for len(pointShadowStates) <= idx {
		pointShadowStates = append(pointShadowStates, pointShadowState{})
	}
	st := &pointShadowStates[idx]
	if !PointShadowCaching {
		*st = pointShadowState{}
		return pointShadowPlan{faces: allPointShadowFaces, all: true}
	}
	static, faces, movers, ok := pointShadowSignaturesIn(level, pl, pm.Cubemap, cull)
	// A face that must render regardless is recorded as never rendered, so
	// the frame after it stops having to it renders once more and settles.
	if !ok {
		faces = [6]uint64{}
	}

	var dirty uint8
	if st.static != static {
		dirty = allPointShadowFaces
	} else {
		for f := range faces {
			if !ok || st.faces[f] != faces[f] {
				dirty |= 1 << f
			}
		}
	}
	if dirty == 0 {
		return pointShadowPlan{}
	}
	for f := range faces {
		if dirty&(1<<f) != 0 {
			st.faces[f] = faces[f]
		}
	}
	staticChanged := st.static != static
	st.static = static

	// Nothing in reach moves and there is no static layer kept for this
	// world: the faces are drawn whole, as they were before anything moved.
	staticLayerValid := pm.StaticCubemap != 0 && st.staticCube == static
	if movers == 0 && (staticChanged || !staticLayerValid) {
		return pointShadowPlan{faces: dirty, all: true}
	}
	if pm.StaticCubemap == 0 {
		pm.StaticCubemap, pm.StaticFBO = createDepthCube()
	}
	p := pointShadowPlan{faces: dirty}
	if st.staticCube != static {
		st.staticCube = static
		p.static = true
	}
	return p
}
