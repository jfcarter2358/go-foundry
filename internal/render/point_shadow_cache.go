package render

// point_shadow_cache.go — not re-rendering a point light's shadow cubemap
// when nothing it could show has changed.
//
// A shadowed point light costs six scene walks a frame, one per cube face, and
// in a level lit like a hangar almost none of those frames changes a thing: the
// light is bolted to the ceiling and the walls are walls. So before the walks,
// each light gets a signature — a hash of every input the six walks read, over
// everything whose box reaches the light's cube (the union of the six face
// frusta) — and a cubemap whose signature matches the one it was rendered with
// is kept as it is.
//
// The signature comes in two halves, because the usual reason a light has to
// render is a character standing in it, not the room changing: the STATIC half
// (the compiled world, the chunks and their brushes) and the MOVING half
// (entities, scenery, sprites, scatter). A light with anything moving in reach
// keeps a second cubemap holding just its static layer; when only the moving
// half has changed, each face starts from a copy of that and draws the moving
// casters on top, instead of drawing the room six times again.
//
// The signature is built from what renderSceneShadow, the sprite and the
// scatter cube passes actually draw, walked in the same order under the same
// predicates, so there is one rule to keep in step: ANYTHING NEW THAT A CUBE
// FACE DRAWS MUST ADD ITS INPUTS HERE, or its shadow freezes wherever it was
// when the light last had a reason to re-render. One kind of caster is not
// hashed but simply marks the light dirty while it is in range, because what it
// draws is not a function of anything this file can cheaply read: scatter,
// whose tiers follow the camera and which the wind moves. Particles and
// sprites ARE hashed, instance by instance, from the same gather the pass
// draws from — a static shadow-casting sprite costs nothing, a spark dirties
// its light for as long as it lives.

import (
	"math"
	"unsafe"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// pointShadowState is what one point shadow map holds, by the signatures it was
// rendered from. Zero is never a signature (see sigHash), so a fresh map, and a
// half that must render regardless, always renders.
type pointShadowState struct {
	static, moving uint64 // what the live cubemap shows
	staticCube     uint64 // what the static-layer cubemap holds
}

// pointShadowStates is indexed like Shadows.PointMaps.
var pointShadowStates []pointShadowState

// pointShadowPlan is what a light's cubemap needs this frame.
type pointShadowPlan uint8

const (
	// Nothing in reach has changed.
	pointShadowKeep pointShadowPlan = iota
	// Draw everything into the cubemap: the static world changed and nothing
	// in reach moves, so there is no static layer worth keeping apart.
	pointShadowEverything
	// Copy the kept static layer in and draw the moving casters over it.
	pointShadowMoving
	// Redraw the static layer first, then as pointShadowMoving.
	pointShadowStaticAndMoving
)

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

// geometry covers what drawGeometryShadow reads of one submesh: which
// vertices, whether it is drawn at all, and the pose it is deformed by.
func (h *sigHash) geometry(g *gfx.Geometry) {
	h.u64(uint64(len(g.Vertices)))
	if len(g.Vertices) > 0 {
		h.u64(uint64(uintptr(unsafe.Pointer(&g.Vertices[0]))))
	}
	h.f32(g.Alpha)
	h.u64(uint64(len(g.BonePalette)))
	for _, m := range g.BonePalette {
		h.mat(m)
	}
}

// pointShadowSignatures hashes everything a point light's six cube faces would
// draw: the static layer, and the moving one with how many moving casters are
// in reach. ok is false when a moving caster in reach cannot be hashed and the
// moving layer must be drawn regardless.
func pointShadowSignatures(level *world.Level, pl world.PointLight, cubemap uint32) (static, moving uint64, movers int, ok bool) {
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

	// Entities, under RenderEntitiesShadow's.
	h = newSig()
	for ei := range level.Entities {
		e := &level.Entities[ei]
		frame := level.Frames.FrameFor(e)
		if !e.Visible || !EntityInFrustumOf(frame, e, &box) {
			continue
		}
		movers++
		h.u64(uint64(ei))
		h.mat(e.DrawMatrix())
		if !frame.IsWorld() {
			h.mat(frame.World)
		}
		for mi := range e.Models {
			m := &e.Models[mi]
			if !entityDrawable(e, m) || m.Unlit {
				continue
			}
			h.u64(uint64(mi))
			h.mat(e.AttachmentMatrix(m))
			for si := range m.Submeshes {
				h.geometry(&m.Submeshes[si])
			}
		}
	}

	// Scenery, under RenderSceneryShadow's.
	for i := range level.Scenery {
		s := &level.Scenery[i]
		if !s.HasGeometry() || !SceneryInFrustum(s, &box) {
			continue
		}
		movers++
		h.u64(uint64(i))
		h.mat(s.ModelMatrix())
		for si := range s.Submeshes {
			h.geometry(&s.Submeshes[si])
		}
	}

	// Sprites and particles: the very instances the cube pass would gather.
	if spritePassLive(level) {
		for _, b := range SpriteBatches(level, &box, pl.Position, true) {
			h.str(b.Texture)
			h.u64(uint64(b.Blend))
			h.u64(uint64(b.Billboard))
			if b.FadingCutout {
				h.u64(1)
			}
			h.vec3(b.FixedRight)
			h.vec3(b.FixedUp)
			movers += len(b.Instances)
			for _, in := range b.Instances {
				h.vec3(in.Pos)
				h.f32(in.Size[0])
				h.f32(in.Size[1])
				h.f32(in.Rot)
				h.vec4(in.Color)
				h.vec4(in.UV)
			}
		}
	}

	// Scatter follows the camera and sways in the wind: any in range and the
	// light renders.
	if scatters.initialized && BiomeShadows.AnyCast() {
		inRange := false
		forEachScatterVolume(level, gfx.Cam.Position, true, pl.Radius, &box, func(*world.Volume) { inRange = true })
		if inRange {
			return static, 0, movers + 1, false
		}
	}
	return static, h.sum(), movers, true
}

// planPointShadow decides what map idx needs this frame, and records the
// signatures it is about to be rendered from. It makes the map's static-layer
// cubemap the first time one is wanted.
func planPointShadow(idx int, level *world.Level, pl world.PointLight, pm *PointShadowMap) pointShadowPlan {
	for len(pointShadowStates) <= idx {
		pointShadowStates = append(pointShadowStates, pointShadowState{})
	}
	st := &pointShadowStates[idx]
	static, moving, movers, ok := pointShadowSignatures(level, pl, pm.Cubemap)
	if !PointShadowCaching {
		*st = pointShadowState{}
		return pointShadowEverything
	}
	if ok && st.static == static && st.moving == moving {
		return pointShadowKeep
	}
	st.static, st.moving = static, moving
	if movers == 0 {
		return pointShadowEverything
	}
	if pm.StaticCubemap == 0 {
		pm.StaticCubemap, pm.StaticFBO = createDepthCube()
	}
	if st.staticCube == static {
		return pointShadowMoving
	}
	st.staticCube = static
	return pointShadowStaticAndMoving
}
