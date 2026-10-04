package render

// vsm_track.go — what changed since the last frame, for the virtual shadow
// map's page cache (vsm.go). Phase 5 of the Nanite plan
// (docs/nanite/PHASE_5.md).
//
// A virtual shadow map's page stays drawn until something that casts into
// it changes; this file is how it finds out. It is the point-shadow cache's
// question (point_shadow_cache.go) asked of the whole level rather than one
// light's reach, and answered with places rather than a yes or no: every
// caster the sun pass draws is hashed on the inputs its draw reads, and one
// whose hash changed — or that appeared or went away — hands in the world
// box it was in and the one it is in now. The pages under those boxes, at
// every level, are drawn again; nothing else is.
//
// The boxes say which layer changed (vsm_pages.go): a chunk or a prop is the
// static layer, and its pages are drawn again whole; anything else moves,
// and its pages draw their movers again over their static copies.
//
// What it hashes, under the same predicates the sun pass's walkers draw by
// (renderSceneShadowLayers and the sprite, scatter and foliage passes):
//
//   - the static world as a whole: which level, the brush mesh generation
//     (every edit and load), the compiled level and what of it is hidden —
//     any change to these marks EVERY page stale;
//   - each chunk: switched on or off, its matrix, its terrain's generation,
//     its moved brushes' placements;
//   - each entity: visible, its matrix and its frame's, each attachment's
//     matrix and mesh — and a posed (skinned) one counts as moved every
//     frame, rather than have every bone of it hashed to find out;
//   - each scenery placement: its matrix and meshes;
//   - the shadow-casting sprites and particles, instance by instance.
//
// And what it cannot hash it treats as moving every frame, as the point
// cache does: scatter (its tiers follow the camera, the wind sways it) and
// terrain foliage (the wind, and its thinning by distance from the camera)
// hand in their boxes each frame while they cast. A level carpeted in
// casting grass redraws the pages under it every frame — correct, and no
// slower than the cascades did.
//
// ANYTHING NEW THAT THE SUN PASS DRAWS MUST BE HASHED HERE, or its shadow in
// the virtual map freezes wherever it was when its pages were last drawn.

import (
	"unsafe"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// vsmBox is a world box something changed in; static: something of the
// static layer (a chunk, a prop), whose pages are drawn again whole, else
// something moving, whose pages redraw their movers over their static layer.
type vsmBox struct {
	lo, hi mgl32.Vec3
	static bool
}

// vsmTracked is one caster as last seen.
type vsmTracked struct {
	sig  uint64
	box  vsmBox
	seen uint64
}

// vsmTracker is the last frame's view of the casters; see the file comment.
type vsmTracker struct {
	lvl      *world.Level
	static   uint64
	frame    uint64
	chunks   map[int]*vsmTracked
	entities map[int]*vsmTracked
	scenery  map[int]*vsmTracked
	sprites  vsmTracked
	lastCam  mgl32.Vec3
	boxes    []vsmBox

	// What the movers' pass draws this frame, gathered on the way past
	// (vsm.go, drawMovers): each visible entity's attachments, with their
	// matrices, and its box; and a box round all the casting sprites, the
	// casting scatter and the casting foliage (zero: none).
	draws                          []vsmEntityDraw
	spriteBox, scatterBox, folBox  vsmBox
	anySprites, anyScatter, anyFol bool
	// meshBounds is each entity mesh's own extent, by its vertex array, for
	// entities with no bounds authored (meshBox).
	meshBounds map[uintptr]vsmBox
}

// vsmEntityDraw is one entity as the movers' pass draws it: what
// RenderEntitiesShadow would, worked out once a frame instead of once a
// rectangle.
type vsmEntityDraw struct {
	box    vsmBox
	models []vsmModelDraw
	// lo, hi: the box across the light (vsm.go fills them in).
	lo, hi [2]float64
}

type vsmModelDraw struct {
	m      mgl32.Mat4
	meshes []gfx.Geometry
}

func (t *vsmTracker) reset() {
	*t = vsmTracker{chunks: map[int]*vsmTracked{}, entities: map[int]*vsmTracked{}, scenery: map[int]*vsmTracked{}}
}

// note compares a caster's hash with last frame's, handing in the boxes when
// it changed (box.static says which layer it is in).
func (t *vsmTracker) note(m map[int]*vsmTracked, key int, sig uint64, box vsmBox) {
	tr := m[key]
	if tr == nil {
		m[key] = &vsmTracked{sig: sig, box: box, seen: t.frame}
		t.boxes = append(t.boxes, box)
		return
	}
	if tr.sig != sig {
		t.boxes = append(t.boxes, tr.box, box)
		tr.sig, tr.box = sig, box
	}
	tr.seen = t.frame
}

// gone hands in the boxes of casters not seen this frame, and forgets them.
func (t *vsmTracker) gone(m map[int]*vsmTracked) {
	for k, tr := range m {
		if tr.seen != t.frame {
			t.boxes = append(t.boxes, tr.box)
			delete(m, k)
		}
	}
}

// mover is a box of the moving layer.
func mover(lo, hi mgl32.Vec3) vsmBox { return vsmBox{lo: lo, hi: hi} }

// collect walks the level's casters: all reports that every page is stale
// (the static world changed, or a new level), else boxes are the places
// that changed. reach bounds the casters that sway every frame (scatter,
// foliage) to the shadow's own distance round the camera.
func (t *vsmTracker) collect(level *world.Level, cam mgl32.Vec3, reach float32) (all bool, boxes []vsmBox) {
	if t.chunks == nil || t.lvl != level {
		t.reset()
		t.lvl = level
		all = true
	}
	t.frame++
	t.boxes = t.boxes[:0]
	t.draws = t.draws[:0]
	t.anySprites, t.anyScatter, t.anyFol = false, false, false

	h := newSig()
	h.u64(world.BrushMeshGeneration())
	h.u64(uint64(uintptr(unsafe.Pointer(level.Static))))
	if level.Static != nil {
		h.u64(level.Static.HiddenVersion())
		h.u64(uint64(len(level.Static.HiddenChunks)))
	}
	// The switches that change what casts at all.
	h.u64(uint64(boolToInt32(gfx.FoliageShadows)))
	h.u64(uint64(boolToInt32(BiomeShadows.CastRockMoss)))
	h.u64(uint64(boolToInt32(BiomeShadows.CastFoliage)))
	h.u64(uint64(BiomeShadows.CastCascades))
	h.u64(uint64(boolToInt32(NaniteLOD)))
	if s := h.sum(); s != t.static {
		t.static = s
		all = true
	}

	for ci := range level.Chunks {
		ch := &level.Chunks[ci]
		h := newSig()
		h.u64(uint64(boolToInt32(ch.IsEnabled())))
		h.str(ch.ID)
		h.mat(ch.ModelMatrix())
		if ch.Terrain != nil {
			h.u64(ch.Terrain.Generation)
		}
		for bi := range ch.Brushes {
			if b := &ch.Brushes[bi]; !b.AtRest() {
				h.u64(uint64(bi))
				h.mat(b.Placement())
			}
		}
		lo, hi := ch.WorldBounds()
		t.note(t.chunks, ci, h.sum(), vsmBox{lo, hi, true})
	}
	t.gone(t.chunks)

	for ei := range level.Entities {
		e := &level.Entities[ei]
		// The authored bounds (a collision box) and the box its meshes
		// span, together: an attachment — a rifle, a turret's barrel — can
		// reach past the bounds, and a page is small; a part outside the box
		// would be left out of the rectangles it falls in and its shadow
		// left behind when it moves. With no bounds authored (which the
		// cascades' walk does not cull at all), the meshes' box alone.
		lo, hi, ok := e.WorldAABB()
		if mlo, mhi, mok := t.meshBox(e); mok {
			if ok {
				lo, hi = minVec3(lo, mlo), maxVec3(hi, mhi)
			} else {
				lo, hi, ok = mlo, mhi, true
			}
		}
		if !ok {
			continue
		}
		frame := level.Frames.FrameFor(e)
		h := newSig()
		h.u64(uint64(boolToInt32(e.Visible)))
		draw := e.DrawMatrix()
		h.mat(draw)
		if !frame.IsWorld() {
			h.mat(frame.World)
			lo, hi = TransformAABB(frame.World, lo, hi)
			draw = frame.World.Mul4(draw)
		}
		// RenderEntitiesShadow's walk, for the movers' pass.
		var ed *vsmEntityDraw
		if e.Visible {
			// Last frame's entries are reused, attachment lists and all.
			if n := len(t.draws); n < cap(t.draws) {
				t.draws = t.draws[:n+1]
			} else {
				t.draws = append(t.draws, vsmEntityDraw{})
			}
			ed = &t.draws[len(t.draws)-1]
			ed.box = mover(lo, hi)
			ed.models = ed.models[:0]
		}
		animated := false
		for mi := range e.Models {
			m := &e.Models[mi]
			if !entityDrawable(e, m) || m.Unlit {
				continue
			}
			am := e.AttachmentMatrix(m)
			if ed != nil {
				ed.models = append(ed.models, vsmModelDraw{m: draw.Mul4(am), meshes: m.Submeshes})
			}
			h.u64(uint64(mi))
			h.mat(am)
			for si := range m.Submeshes {
				g := &m.Submeshes[si]
				if len(g.BonePalette) > 0 {
					// Posed: not worth hashing every bone of every frame
					// to learn what an animated character almost always
					// is — moved.
					animated = true
					continue
				}
				h.geometry(g)
			}
		}
		if animated && e.Visible {
			h.u64(t.frame)
		}
		t.note(t.entities, ei, h.sum(), mover(lo, hi))
	}
	t.gone(t.entities)

	for i := range level.Scenery {
		s := &level.Scenery[i]
		lo, hi, ok := s.WorldAABB()
		if !ok {
			continue
		}
		h := newSig()
		h.u64(uint64(boolToInt32(s.HasGeometry())))
		h.mat(s.ModelMatrix())
		h.u64(uint64(uintptr(unsafe.Pointer(s.Nanite))))
		h.u64(uint64(boolToInt32(s.NaniteGPU)))
		for si := range s.Submeshes {
			h.geometry(&s.Submeshes[si])
		}
		t.note(t.scenery, i, h.sum(), vsmBox{lo, hi, true})
	}
	t.gone(t.scenery)

	// Sprites and particles: one hash and one box for all that cast; a
	// spark anywhere redraws the pages under all of them, which is a few.
	if spritePassLive(level) {
		h := newSig()
		var box vsmBox
		any := false
		for _, b := range SpriteBatches(level, nil, cam, true) {
			h.str(b.Texture)
			h.u64(uint64(b.Billboard))
			for _, in := range b.Instances {
				h.vec3(in.Pos)
				h.f32(in.Size[0])
				h.f32(in.Size[1])
				h.f32(in.Rot)
				h.vec4(in.UV)
				r := max(in.Size[0], in.Size[1])
				lo, hi := in.Pos.Sub(mgl32.Vec3{r, r, r}), in.Pos.Add(mgl32.Vec3{r, r, r})
				if !any {
					box, any = mover(lo, hi), true
				} else {
					box = mover(minVec3(box.lo, lo), maxVec3(box.hi, hi))
				}
			}
		}
		t.spriteBox, t.anySprites = box, any
		if sig := h.sum(); sig != t.sprites.sig {
			if t.sprites.sig != 0 && t.sprites.box != (vsmBox{}) {
				t.boxes = append(t.boxes, t.sprites.box)
			}
			if any {
				t.boxes = append(t.boxes, box)
			}
			t.sprites = vsmTracked{sig: sig, box: box}
		}
	} else if t.sprites.sig != 0 {
		// The last sprite or emitter went away (or the pass is not up):
		// what they cast is no longer drawn, so its pages draw again.
		if t.sprites.box != (vsmBox{}) {
			t.boxes = append(t.boxes, t.sprites.box)
		}
		t.sprites = vsmTracked{}
		t.spriteBox, t.anySprites = vsmBox{}, false
	}

	// Scatter sways and follows the camera: every casting volume in reach.
	if scatters.initialized && BiomeShadows.AnyCast() {
		forEachScatterVolume(level, cam, true, reach, nil, func(v *world.Volume) {
			if lo, hi, ok := v.WorldAABB(level); ok {
				t.boxes = append(t.boxes, mover(lo, hi))
				t.scatterBox = growBox(t.scatterBox, t.anyScatter, lo, hi)
				t.anyScatter = true
			}
		})
	}
	// Terrain foliage sways in the wind, and thins by distance from the
	// camera: every casting draw in reach, while either can change it.
	if foliage.initialized && gfx.FoliageShadows {
		changing := foliageWind.strength > 0 || cam != t.lastCam
		lim := foliageShadowRange * FoliageDistanceScale()
		for di := range foliage.draws {
			if dr := &foliage.draws[di]; dr.d <= lim {
				if changing {
					t.boxes = append(t.boxes, mover(dr.wlo, dr.whi))
				}
				t.folBox = growBox(t.folBox, t.anyFol, dr.wlo, dr.whi)
				t.anyFol = true
			}
		}
	}
	t.lastCam = cam
	return all, t.boxes
}

// meshBox is the world box an entity's drawable attachments span (in its
// frame's space, as WorldAABB is), from each mesh's own extent grown by half
// again — a pose can reach past the rest pose — or false for none.
func (t *vsmTracker) meshBox(e *world.Entity) (lo, hi mgl32.Vec3, ok bool) {
	draw := e.DrawMatrix()
	for mi := range e.Models {
		m := &e.Models[mi]
		if !entityDrawable(e, m) || m.Unlit {
			continue
		}
		am := draw.Mul4(e.AttachmentMatrix(m))
		for si := range m.Submeshes {
			g := &m.Submeshes[si]
			if len(g.Vertices) < 5 {
				continue
			}
			key := uintptr(unsafe.Pointer(&g.Vertices[0]))
			b, seen := t.meshBounds[key]
			if !seen {
				b.lo = mgl32.Vec3{g.Vertices[0], g.Vertices[1], g.Vertices[2]}
				b.hi = b.lo
				for i := 0; i+2 < len(g.Vertices); i += 5 {
					p := mgl32.Vec3{g.Vertices[i], g.Vertices[i+1], g.Vertices[i+2]}
					b.lo, b.hi = minVec3(b.lo, p), maxVec3(b.hi, p)
				}
				pad := b.hi.Sub(b.lo).Mul(0.25)
				b.lo, b.hi = b.lo.Sub(pad), b.hi.Add(pad)
				if t.meshBounds == nil {
					t.meshBounds = map[uintptr]vsmBox{}
				}
				t.meshBounds[key] = b
			}
			wlo, whi := TransformAABB(am, b.lo, b.hi)
			if !ok {
				lo, hi, ok = wlo, whi, true
			} else {
				lo, hi = minVec3(lo, wlo), maxVec3(hi, whi)
			}
		}
	}
	return lo, hi, ok
}

// growBox is b grown round [lo, hi], or that box alone when b is not yet
// one (has false).
func growBox(b vsmBox, has bool, lo, hi mgl32.Vec3) vsmBox {
	if !has {
		return mover(lo, hi)
	}
	return mover(minVec3(b.lo, lo), maxVec3(b.hi, hi))
}

func minVec3(a, b mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{min(a[0], b[0]), min(a[1], b[1]), min(a[2], b[2])}
}

func maxVec3(a, b mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{max(a[0], b[0]), max(a[1], b[1]), max(a[2], b[2])}
}
