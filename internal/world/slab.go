package world

// slab.go — the thickness a floor or a ceiling has.
//
// A floor was a plane. Seen from below — from the room under a mezzanine, from
// outside a building through a window, from inside a stairwell — it was a sheet
// of zero thickness, and the only way to make it look like a slab was to build
// one out of a second sector or to reach for a skirt.
//
// A Slab is that thickness as a property of the surface itself. Give a floor a
// Thickness and it grows a second face parallel to it, offset *away* from the
// room — down for a floor, up for a ceiling — and a rim around the perimeter
// joining the two. Nothing else about the level changes: the floor is still one
// surface at one Height, and Thickness 0 (the default) is the plane every
// existing level already has, written into no file.
//
// # Why it follows the surface's own mesh rather than owning one
//
// The far face is built from the *same* SurfaceMesh and the same Openings as
// the surface it hangs off, through a frame whose BaseHeight and corner heights
// are shifted by the thickness. That is what a slab is: a constant offset of
// the shape above it. Carve a pit into a floor and the underside dips with it;
// cut a stairwell through it and the hole goes all the way through, because the
// hole is the same Opening. An independently shapeable underside would let the
// two drift into a solid whose faces cross each other, and there is no useful
// level that wants that.
//
// The offset frame keeps the surface's own `Up`, so its Outline() winds
// identically and SyncSurfaceBoundary — which rewrites the mesh's boundary
// points on every rebuild — is a genuine no-op rather than something that
// quietly renumbers the corners of the mesh it shares. The far face is turned
// to look away from the room by reversing the built triangles instead
// (surfaceBuild.Reverse).
//
// # Why the rim skips portal edges
//
// A rim quad is the exposed *edge* of the slab. Across a portal the slab is not
// exposed — it continues into the neighbouring sector, whose own rim would sit
// in exactly the same place and z-fight with this one. So portal edges are
// skipped and wall edges are not; where a portal joins two floors at different
// heights, the riser the portal already draws (RebuildGaps) is what fills the
// step. Slab.RimAcrossPortals is the escape hatch for an author who wants the
// edge sealed anyway.
//
// Slab geometry is derived, like a skirt's and a portal gap's: rebuilt by
// Sector.Load and never serialized. Only the numbers and texture names are
// saved.

import (
	"foundry/internal/geom"
	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// SlabMinThickness is the thinnest slab that is built at all. Below it the
// far face and the surface are close enough to z-fight, and an author dragging
// the control back to zero means "no slab" rather than "a very thin one".
const SlabMinThickness = float32(1e-3)

// Slab is the solid a floor or ceiling becomes when it is given a thickness.
//
// The zero value is the plane: no thickness, nothing built, nothing written to
// the level file. Every "" texture inherits from the surface the slab belongs
// to, so a slab that has only been switched on already looks like the floor
// above it rather than like missing art.
//
// Alpha and smoothing are deliberately not fields here: they are inherited from
// the surface. A slab is the *same piece of material* as the floor it hangs
// under, and a half-transparent floor over an opaque underside — or a smoothed
// floor over a faceted one — describes a solid that could not exist.
type Slab struct {
	// Thickness is how far the far face sits from the surface, in world units:
	// down from a floor, up from a ceiling. 0 (the default) is no slab.
	Thickness float32

	// Hidden stops the far face being drawn while keeping the rim, which is
	// the floor's Hidden flag one level down: what a biome ground patch or a
	// lower storey's ceiling already covers does not need drawing twice.
	Hidden bool

	// Texture is the far face's; "" takes the surface's own.
	Texture      string
	BlendTexture string
	BlendMask    string

	// RimTexture is the perimeter band's; "" takes Texture, and so through it
	// the surface's.
	RimTexture string

	// RimAcrossPortals builds the rim over portal edges too, instead of
	// skipping them. Off by default: see the file comment on why a portal
	// edge normally gets no rim at all. An author switches it on when a slab
	// needs a sealed edge even where the level would otherwise continue it
	// into the sector next door — the neighbour's own rim, if it draws one,
	// then sits flush against this one rather than avoiding it.
	RimAcrossPortals bool

	// DoubleSided defaults to false, unlike a floor's, and that is the right
	// default here rather than an oversight: the far face is the back of a
	// solid, and the side of it that faces into the slab is never seen.
	DoubleSided bool

	UVOffset   mgl32.Vec2
	UVScale    mgl32.Vec2 // zero = {1,1}
	UVRotation float32

	// Mesh is the far face's OWN editable geometry, shaped in the 3D editor
	// exactly as the surface's is. Empty — the default, and what nearly every
	// slab stays — means it MIRRORS the surface's mesh, offset by the
	// thickness, so a shaped floor's underside follows its shape for free and a
	// plain floor's is plain.
	//
	// Shaping it is what breaks that link, and the link is worth having a way
	// out of: a slab whose two faces must stay parallel cannot be a vaulted
	// ceiling with a flat roof, or a floor with joists under it. The moment an
	// author enters shaping on the far face it is seeded from whatever the
	// mirror was showing (SeedSurfaceMeshFor), so the shape on screen is where
	// the editing starts rather than a flat ring replacing it.
	Mesh SurfaceMesh

	// Derived, rebuilt by Sector.Load and never serialized.
	Geometry          gfx.Geometry
	SubmeshGeometries []gfx.Geometry
	RimGeometry       gfx.Geometry

	// Collision cache for the far face — the triangles it was actually built
	// out of, so what a head hits from below is the surface that was drawn.
	// Filled by Sector.Load alongside the geometry, like a floor's.
	CollisionPts    []mgl32.Vec2
	CollisionDepths []float32
	CollisionTris   []int
}

// ShapeMesh is the mesh the far face is built from: the slab's own once it has
// been shaped, and otherwise the surface's, mirrored. surface may be nil.
func (sl *Slab) ShapeMesh(surface *SurfaceMesh) *SurfaceMesh {
	if !sl.Mesh.IsEmpty() {
		return &sl.Mesh
	}
	return surface
}

// Shaped reports whether the far face has been given a shape of its own, and so
// no longer follows the surface it hangs off.
func (sl *Slab) Shaped() bool { return !sl.Mesh.IsEmpty() }

// HasThickness reports whether this slab is thick enough to be built.
func (sl *Slab) HasThickness() bool { return sl.Thickness > SlabMinThickness }

// SlabDrawable reports whether a slab has geometry to draw, so render passes
// can skip the ones that do not without reaching into the meshes — the same
// question SkirtDrawable answers for a skirt.
func SlabDrawable(sl *Slab) bool {
	return len(sl.Geometry.Vertices) > 0 || len(sl.RimGeometry.Vertices) > 0
}

// Unload releases the slab's GPU buffers. Called when the thickness goes back
// to zero as well as when the chunk is switched off, so a slab that stops
// existing does not leave its VAO and VBO allocated with nothing holding the
// handles — the same reason RebuildGaps unloads a gap quad rather than dropping
// its vertices.
func (sl *Slab) Unload() {
	sl.Geometry.Unload()
	for i := range sl.SubmeshGeometries {
		sl.SubmeshGeometries[i].Unload()
	}
	sl.SubmeshGeometries = nil
	sl.RimGeometry.Unload()
	sl.CollisionPts, sl.CollisionDepths, sl.CollisionTris = nil, nil, nil
}

// Authored is the slab without its derived geometry: the numbers and names a
// level file records, and none of the GL handles.
//
// It exists for the editor's sector split and merge, which build new Sectors
// out of pieces of the old ones. Copying a Slab wholesale there would hand two
// sectors the same VAO and VBO, and whichever of them rebuilt first would pull
// the buffers out from under the other.
func (sl Slab) Authored() Slab {
	sl.Geometry = gfx.Geometry{}
	sl.SubmeshGeometries = nil
	sl.RimGeometry = gfx.Geometry{}
	// The mesh IS authored, so it comes across — but deep, since a SurfaceMesh
	// is slices and a shallow copy would leave two sectors editing one shape.
	sl.Mesh = sl.Mesh.Clone()
	sl.CollisionPts, sl.CollisionDepths, sl.CollisionTris = nil, nil, nil
	return sl
}

// ---------- frames ----------

// offsetPlanarFrame is f moved dy along world Y, corner heights and all.
//
// Up is left alone on purpose: see the file comment. The frame's Outline() and
// OutlineDepth() therefore answer exactly what f's do, which is what makes it
// safe to build a second surface out of the mesh f's surface owns.
func offsetPlanarFrame(f PlanarSurfaceFrame, dy float32) PlanarSurfaceFrame {
	out := f
	out.BaseHeight = f.BaseHeight + dy
	out.Heights = make([]float32, len(f.Heights))
	for i, h := range f.Heights {
		out.Heights[i] = h + dy
	}
	return out
}

// FloorSlabFrame is the underside of the sector's floor as a surface: the floor
// frame dropped by its slab thickness.
func (s *Sector) FloorSlabFrame(points []mgl32.Vec2) PlanarSurfaceFrame {
	return offsetPlanarFrame(s.FloorFrame(points), -s.Floor.Slab.Thickness)
}

// CeilingSlabFrame is the top side of the sector's ceiling: the ceiling frame
// raised by its slab thickness.
func (s *Sector) CeilingSlabFrame(points []mgl32.Vec2) PlanarSurfaceFrame {
	return offsetPlanarFrame(s.CeilingFrame(points), s.Ceiling.Slab.Thickness)
}

// ---------- building ----------

// slabBuild is one surface's slab and everything about the surface the slab
// inherits. A struct for the same reason surfaceBuild is one: a floor and a
// ceiling differ here only in a sign and in which fields the values come out
// of, and a call site that reads as named properties is one where a ceiling's
// alpha cannot be passed as a floor's.
type slabBuild struct {
	Slab     *Slab
	Frame    PlanarSurfaceFrame // the SURFACE's frame, not the slab's
	Mesh     *SurfaceMesh
	Openings []Opening

	// Inherited from the surface: its texture when the slab names none, and
	// its alpha and smoothing unconditionally.
	Texture     string
	Alpha       float32
	Smooth      bool
	SmoothAngle float32

	// Down is true for a floor, whose slab hangs below it.
	Down bool
}

// loadSlab builds and uploads one surface's slab, or releases it when there is
// no longer a slab to build.
func (s *Sector) loadSlab(points []mgl32.Vec2, b slabBuild) error {
	sl := b.Slab
	sl.Unload()
	if !sl.HasThickness() || len(points) < 3 {
		return nil
	}

	dy := sl.Thickness
	if b.Down {
		dy = -dy
	}

	tex := sl.Texture
	if tex == "" {
		tex = b.Texture
	}

	farFrame := offsetPlanarFrame(b.Frame, dy)
	mesh := sl.ShapeMesh(b.Mesh)

	if !sl.Hidden {
		if err := loadSurfaceGeometry(surfaceBuild{
			Frame:       farFrame,
			Mesh:        mesh,
			DoubleSided: sl.DoubleSided,
			// The far face carries the shape of the surface above it, so it
			// comes out of the builder facing the same way the surface does —
			// into the slab. Turning it round here rather than by flipping the
			// frame is what keeps the shared mesh's corner numbering intact.
			Reverse:     true,
			Openings:    b.Openings,
			Texture:     tex,
			UVOffset:    sl.UVOffset,
			UVScale:     sl.UVScale,
			UVRotation:  sl.UVRotation,
			Smooth:      b.Smooth,
			SmoothAngle: b.SmoothAngle,
			Geometry:    &sl.Geometry,
			Submeshes:   &sl.SubmeshGeometries,
		}); err != nil {
			return err
		}
		sl.Geometry.Alpha = b.Alpha
		sl.Geometry.BlendTexture = sl.BlendTexture
		sl.Geometry.BlendMask = sl.BlendMask
	}

	// The cache is filled whether or not the face is drawn: Hidden is about
	// what the eye sees, and a slab you cannot see is still solid.
	sl.CollisionPts, sl.CollisionDepths, sl.CollisionTris =
		SurfaceCollisionMesh(farFrame, mesh, b.Openings)

	rimTex := sl.RimTexture
	if rimTex == "" {
		rimTex = tex
	}
	verts, indices := s.buildSlabRim(points, b, dy)
	if len(indices) > 0 {
		ApplyUVTransform(verts, sl.UVOffset, sl.UVScale, sl.UVRotation)
		sl.RimGeometry.Smooth, sl.RimGeometry.SmoothAngle = b.Smooth, b.SmoothAngle
		if err := sl.RimGeometry.Load(verts, indices, rimTex, mgl32.Vec3{}); err != nil {
			return err
		}
		sl.RimGeometry.Alpha = b.Alpha
	}
	return nil
}

// buildSlabRim emits the band that closes the gap between the surface and its
// far face: one quad per exposed outline edge, and one per edge of every
// opening cut through it, so a stairwell shows the slab's cut edge rather than
// a paper-thin lip.
func (s *Sector) buildSlabRim(points []mgl32.Vec2, b slabBuild, dy float32) ([][]float32, []int) {
	var verts [][]float32
	var indices []int
	// nearA/nearC are the heights on the surface's side of the band, farA/farC
	// on the slab's. They are given separately rather than derived as near+dy
	// because an independently shaped far face is not a constant offset of the
	// surface any more, and a rim built as though it were would hang in the air
	// or bury itself.
	add := func(a, c mgl32.Vec2, nearA, nearC, farA, farC float32) {
		segLen := c.Sub(a).Len()
		if segLen < 1e-6 {
			return
		}
		lo1, lo2, hi1, hi2 := farA, farC, nearA, nearC
		if !b.Down {
			lo1, lo2, hi1, hi2 = hi1, hi2, lo1, lo2
		}
		q, qi := buildGapQuad(a, c, lo1, lo2, hi1, hi2, segLen)
		base := len(verts)
		verts = append(verts, q...)
		for _, i := range qi {
			indices = append(indices, base+i)
		}
	}

	n := len(points)
	for i := 0; i < n; i++ {
		// A portal edge is where the slab continues into the sector next door
		// rather than where it ends. See the file comment. RimAcrossPortals
		// is the author's way to override that and seal the edge anyway.
		if !b.Slab.RimAcrossPortals && i < len(s.EdgeKinds) && s.EdgeKinds[i] == "portal" {
			continue
		}
		j := (i + 1) % n
		// The perimeter needs no height query on either side. Both meshes'
		// boundary points are pinned to the sector outline and re-derived from
		// their frames on every rebuild (SyncSurfaceBoundary), so a shaped far
		// face still meets its own corners at exactly the offset heights —
		// shaping is free in the interior and pinned at the edge.
		add(points[i], points[j],
			b.Frame.Heights[i], b.Frame.Heights[j],
			b.Frame.Heights[i]+dy, b.Frame.Heights[j]+dy)
	}

	for oi := range b.Openings {
		ring := b.Openings[oi].Points
		for i := 0; i < len(ring); i++ {
			j := (i + 1) % len(ring)
			add(ring[i], ring[j],
				s.surfaceHeightAt(points, ring[i], b.Down),
				s.surfaceHeightAt(points, ring[j], b.Down),
				slabHeightAt(b.Slab, b.Frame.BaseHeight+dy, ring[i]),
				slabHeightAt(b.Slab, b.Frame.BaseHeight+dy, ring[j]))
		}
	}
	return verts, indices
}

// slabHeightAt is the far face's height at a chunk-local point: from the
// triangles it was built out of when there is a cache, and from the flat offset
// plane when there is not.
func slabHeightAt(sl *Slab, baseH float32, xz mgl32.Vec2) float32 {
	h, _, _ := InterpolateTriMeshHeight(sl.CollisionPts, sl.CollisionDepths, sl.CollisionTris,
		baseH, xz, false)
	return h
}

// surfaceHeightAt is the height of a sector's floor or ceiling at an arbitrary
// chunk-local point, for the rim around an opening — whose corners are not
// outline corners and so have no height of their own to read.
//
// It is InterpolateSurfaceHeight's question without the chunk: Sector.Load
// already holds the resolved outline and has just filled the surface's own
// collision cache, so a shaped surface answers from the triangles that were
// actually built and a flat or sloped one from the corner fan.
func (s *Sector) surfaceHeightAt(points []mgl32.Vec2, xz mgl32.Vec2, floor bool) float32 {
	if floor && len(s.Floor.CollisionTris) > 0 {
		if h, _, ok := interpolateSurfaceMeshHeight(s, xz, true); ok {
			return h
		}
	}
	if !floor && len(s.Ceiling.CollisionTris) > 0 {
		if h, _, ok := interpolateSurfaceMeshHeight(s, xz, false); ok {
			return h
		}
	}
	vertexHeight, base := s.Floor.VertexHeight, s.Floor.Height
	if !floor {
		vertexHeight, base = s.Ceiling.VertexHeight, s.Ceiling.Height
	}
	for i := 1; i < len(points)-1; i++ {
		inside, wA, wB, wC := geom.PointInTriangle2D(xz, points[0], points[i], points[i+1])
		if inside {
			return wA*vertexHeight(0) + wB*vertexHeight(i) + wC*vertexHeight(i+1)
		}
	}
	return base
}

// ---------- solidity ----------
//
// A slab is solid. Before it, a floor was a plane with nothing under it, and
// what stopped a body walking under a mezzanine was the *ceiling of the room
// down there* — so a mezzanine over an open hall had nothing underneath it at
// all and a body could rise straight through the floor above.
//
// Thickness is what changes that, and only thickness: a surface with none is
// exactly the plane it always was, so no level that predates this can start
// blocking somewhere it did not. The rule is one sentence — the band a slab
// occupies is not somewhere a body may be — and the whole of it is expressed as
// a ceiling, because the underside is the face a body arrives at.

// SlabCeilingAt is the lowest floor-slab underside over xz that sits above
// refY, and whether there is one.
//
// refY is the floor of the sector doing the asking rather than the body's own
// Y, and that is the more useful reference: a slab underside above that floor
// is overhead for anyone standing in the sector, whatever they are doing
// vertically, and one below it is not something they could ever hit from here.
// It also makes the answer stateless — the same question gets the same answer
// mid-jump as at rest, which is what keeps a head-bonk from stuttering.
//
// A slab whose surface has an opening at xz contributes nothing: the hole goes
// through the whole solid, which is what lets a stairwell stay climbable.
//
// Ceiling slabs are not consulted. A ceiling's underside is the ceiling itself,
// already the head-stop it always was, and the top of a ceiling slab can only
// be reached from a sector above it — whose own floor is what a body there
// stands on.
func SlabCeilingAt(ch *Chunk, xz mgl32.Vec2, refY float32) (float32, bool) {
	if ch == nil {
		return 0, false
	}
	best, found := float32(0), false
	for i := range ch.Sectors {
		s := &ch.Sectors[i]
		sl := &s.Floor.Slab
		if !s.Floor.Enabled || !sl.HasThickness() {
			continue
		}
		if !PointInSector(ch, s, xz) || SurfaceIsOpenAt(s.Floor.Openings, xz) {
			continue
		}
		under := slabHeightAt(sl, s.Floor.Height-sl.Thickness, xz)
		if under <= refY {
			continue // at or below the asker's own floor: not overhead
		}
		if !found || under < best {
			best, found = under, true
		}
	}
	return best, found
}

// ---------- level file ----------

// SlabFromRaw materializes a slab, or the zero slab when the block is absent.
func SlabFromRaw(r *RawSlab) Slab {
	if r == nil {
		return Slab{}
	}
	scale := mgl32.Vec2{r.UVScale.X, r.UVScale.Y}
	if scale == (mgl32.Vec2{}) {
		scale = mgl32.Vec2{1, 1}
	}
	return Slab{
		Thickness:        r.Thickness,
		Hidden:           r.Hidden,
		Texture:          r.Texture,
		BlendTexture:     r.BlendTexture,
		BlendMask:        r.BlendMask,
		RimTexture:       r.RimTexture,
		RimAcrossPortals: r.RimAcrossPortals,
		DoubleSided:      r.DoubleSided,
		UVOffset:         mgl32.Vec2{r.UVOffset.X, r.UVOffset.Y},
		UVScale:          scale,
		UVRotation:       r.UVRotation,
		Mesh:             SurfaceMeshFromRaw(r.Mesh),
	}
}

// SlabToRaw writes a slab out, or nothing at all when there is no slab. A
// thickness under SlabMinThickness builds nothing, so writing it would record a
// setting that has no effect and make the file say more than the level draws.
func SlabToRaw(sl Slab) *RawSlab {
	if !sl.HasThickness() {
		return nil
	}
	return &RawSlab{
		Thickness:        sl.Thickness,
		Hidden:           sl.Hidden,
		Texture:          sl.Texture,
		BlendTexture:     sl.BlendTexture,
		BlendMask:        sl.BlendMask,
		RimTexture:       sl.RimTexture,
		RimAcrossPortals: sl.RimAcrossPortals,
		DoubleSided:      sl.DoubleSided,
		UVOffset:         geom.Point{X: sl.UVOffset.X(), Y: sl.UVOffset.Y()},
		UVScale:          geom.Point{X: sl.UVScale.X(), Y: sl.UVScale.Y()},
		UVRotation:       sl.UVRotation,
		Mesh:             SurfaceMeshToRaw(&sl.Mesh),
	}
}
