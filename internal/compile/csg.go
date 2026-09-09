package compile

// csg.go — removing the geometry nobody can see.
//
// A level blocked out of brushes is full of surfaces that exist only because a
// solid has six sides. The floor of a room is a slab, and the slab has an
// underside; two rooms sharing a wall have two walls, back to back; a pillar
// standing on a floor buries the disc it stands on and the patch of floor
// underneath it. None of that is ever visible, and all of it is drawn, lit,
// shadow-mapped and depth-tested every frame.
//
// CSG is the pass that finds it. Every face of every solid brush is clipped
// against every OTHER solid brush: what survives is the part of that face
// outside all of them, and what does not survive is thrown away. A face wholly
// inside a neighbour disappears entirely; one partly inside comes back as the
// fragments that stick out.
//
// # Why this and not a depth prepass
//
// A depth prepass moves the cost rather than removing it: the geometry is still
// submitted, still transformed, still rasterised once. CSG removes it from the
// level, so it costs nothing in every pass thereafter — the color pass, every
// shadow cascade, every cubemap face — and the BSP that comes after has fewer
// faces to partition and fewer planes to choose from.
//
// # What "inside" means, exactly
//
// A brush is convex, so a point is inside it when it is behind every one of its
// face planes. That is the whole test, and it is what makes convexity worth
// requiring: for a general polyhedron there is no such local rule and this
// stage would need a full polygon boolean.
//
// Faces flush WITH a neighbour's surface — the underside of a floor lying
// exactly on the top of the slab below it — are the interesting case. They are
// not strictly inside anything, and the coplanar rule below is what decides
// which of the two survives so that neither z-fights nor leaves a hole.

import (
	"sort"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/bsp"
	"foundry/internal/world"
)

// Face is one piece of drawable surface: a wound polygon plus everything the
// renderer needs to shade it. It is deliberately NOT a world.BrushFace — a
// compiled face has been clipped and may be one of several fragments of the
// face it came from, so it carries its own points rather than indices into a
// brush's vertex list.
type Face struct {
	Points []mgl32.Vec3
	Plane  bsp.Plane

	// Source names where this came from, for diagnostics and for the editor's
	// "why did my face vanish" question. Not used at runtime.
	Chunk, Brush string
	FaceIdx      int

	// Detail marks a face that came off a brush's DETAIL mesh rather than off
	// its hull (world/brush_detail.go). It is drawn and lit like any other
	// face and takes no part in the partition: see BuildBSP on why a detail
	// face must not be allowed to choose a splitting plane or to decide what
	// is solid.
	Detail bool

	// NoDraw marks the other half of that arrangement: a face that shapes the
	// partition and is never rendered. A DETAILED brush emits one per hull
	// face, because the tree's cells, its portals and above all its SEALING
	// have to describe the solid — and the solid is the hull, whatever has
	// since been carved into the surface drawn over it. Without these a
	// detailed wall would be a hole in the shell and the void fill would pour
	// through it.
	NoDraw bool

	// Everything that decides how it draws, copied off the source face. Copied
	// rather than referenced because the source brush is not present at run
	// time in a release build — that is the whole point of compiling.
	Texture      string
	BlendTexture string
	BlendMask    string
	UVOffset     mgl32.Vec2
	UVScale      mgl32.Vec2
	UVRotation   float32
	Smooth       bool
	SmoothAngle  float32
	Alpha        float32
	DoubleSided  bool

	// UVOrigin is the corner the SOURCE face's texture coordinates are measured
	// from, in world space, and it is inherited by every fragment that face is
	// cut into. Without it each fragment would restart the texture at its own
	// first corner, which is a visible seam along every cut in the level — and
	// a compiled level is nothing but cuts. See world.BuildBrushFaceMeshFrom.
	UVOrigin mgl32.Vec3

	// Light is the baked per-vertex lighting, parallel to Points. Filled by the
	// light stage; nil until then.
	Light []mgl32.Vec3

	// sourceOrder is which brush this came from, in the level's own walk. Used
	// only by the coplanar tie-break in clipsAgainst, and deliberately
	// unexported: it is an artefact of how CSG runs, not part of what a
	// compiled face is.
	sourceOrder int
}

// solid is one brush as CSG sees it: its planes, its bounds, and the order it
// appeared in, all in world space.
type solid struct {
	planes []bsp.Plane
	lo, hi mgl32.Vec3
	// order is the brush's position in the level's own walk. It is what breaks
	// the tie between two coplanar faces facing each other, so that the choice
	// is stable rather than depending on map iteration.
	order int
}

// containsPoint reports whether p is inside the solid — behind every plane.
//
// The tolerance is on the INSIDE: a point exactly on the surface does not count
// as inside. That matters for the flush case, where a face's own points lie on
// a neighbour's plane and would otherwise all read as buried.
func (s *solid) containsPoint(p mgl32.Vec3) bool {
	for _, pl := range s.planes {
		if pl.Distance(p) > -bsp.OnPlaneEps {
			return false
		}
	}
	return true
}

// CSG clips every solid brush face in the level against every other solid
// brush and returns what is left.
//
// The level's own transforms are applied here and never again: a compiled face
// is in world space, because the chunk it came from will not exist at run time
// to carry it out. That is also why frame-owned chunks are SKIPPED — a
// vehicle's interior moves, so its geometry cannot be baked into a static
// world-space list. Those keep their brushes and are drawn the live way.
// A concave brush is an error here rather than a warning, because "inside is
// behind every plane" is not true of one and every stage below this depends on
// it. See validate.go on why the editor takes the opposite line.
func CSG(lvl *world.Level) ([]Face, error) {
	if err := Validate(lvl); err != nil {
		return nil, err
	}
	solids, faces := gatherSolids(lvl)
	out := make([]Face, 0, len(faces))
	for i := range faces {
		out = append(out, clipFaceAgainstSolids(faces[i], solids)...)
	}
	return out, nil
}

// Bakes reports whether the compiler turns this chunk's surfaces into compiled
// geometry.
//
// The one place the rule is written. Two callers read it from opposite ends —
// the compiler, deciding what to consume, and the writer of the compiled file,
// deciding what to mark as already drawn — and if they ever disagreed the
// geometry would be either dropped or drawn twice.
//
// A disabled chunk is not in the world, and a frame-owned one moves with a
// vehicle and so cannot be baked into a static world-space list. Both are kept
// and built the ordinary way.
func Bakes(ch *world.Chunk) bool {
	return ch.IsEnabled() && ch.FrameOwner == ""
}

// BakesBrush is Bakes one level down: whether this particular brush of a baking
// chunk becomes compiled geometry.
//
// A dynamic brush does not (world/brush_dynamic.go). It is a door, a lift or a
// crusher that a script moves at run time, so baking it would freeze it at the
// position it happened to be authored in — and, worse, it would take part in
// the CSG and the BSP as a solid: the floor under a lift would be clipped away,
// the space a door fills would be a solid leaf of the partition, and neither is
// true a second after the level starts.
//
// So a dynamic brush is left out of the bake in every sense. It clips nothing,
// nothing clips it, it seals nothing, and it survives into the compiled level
// as a live brush the runtime draws and collides the ordinary way (see
// levelio/compiled_load.go). The consequence an author has to know about: a
// dynamic brush cannot be part of the shell that seals a level, because a hole
// it happens to be plugging is a hole as far as the void fill is concerned.
func BakesBrush(b *world.Brush) bool { return !b.Dynamic }

// gatherSolids walks the level once, producing the world-space solids CSG clips
// against and the world-space faces it clips.
//
// One walk rather than two, because the two have to agree about which brushes
// took part: a face clipped against a solid its own brush was left out of would
// clip itself away.
func gatherSolids(lvl *world.Level) ([]solid, []Face) {
	var solids []solid
	var faces []Face
	if lvl == nil {
		return nil, nil
	}
	order := 0
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !Bakes(ch) {
			continue
		}
		model := ch.ModelMatrix()
		for bi := range ch.Brushes {
			b := &ch.Brushes[bi]
			if !BakesBrush(b) {
				// Moves at run time, so it is neither a clipper nor something
				// to clip. Its order is deliberately not consumed either: the
				// coplanar tie-break compares orders, and a gap in them would
				// still be stable but would mean nothing.
				continue
			}
			ws := worldPoints(b, model)

			if b.Solid {
				s := solid{order: order}
				for fi := range b.Faces {
					pts := facePoints(ws, &b.Faces[fi])
					if pl, ok := bsp.PlaneFromPoints(pts); ok {
						s.planes = append(s.planes, pl)
					}
				}
				if len(s.planes) >= 4 {
					// Fewer than four planes cannot bound a volume, so a brush
					// with that many is malformed and clips nothing rather
					// than clipping everything.
					s.lo, s.hi = bsp.Bounds(ws)
					solids = append(solids, s)
				}
			}

			// The solid above is the HULL — its planes are where the level is
			// sealed and where a body stops. What is DRAWN may be a detail
			// mesh instead, indexing its own vertex pool, so the two walks
			// resolve their points against different vertex lists.
			drawn := b.DrawMesh()
			dws := ws
			if b.HasDetail() {
				dws = meshWorldPoints(drawn, model)
				// The hull goes in too, as structure nobody draws. Everything
				// downstream then partitions this brush exactly as it would
				// have without a detail mesh — same cells, same portals, same
				// seal — and the detail is dropped into the cells afterwards.
				for fi := range b.Faces {
					f := &b.Faces[fi]
					if f.NoDraw {
						continue // buried by hand; it was never part of the shell
					}
					pts := facePoints(ws, f)
					pl, ok := bsp.PlaneFromPoints(pts)
					if !ok {
						continue
					}
					faces = append(faces, Face{
						Points: pts, Plane: pl, UVOrigin: pts[0], NoDraw: true,
						Chunk: ch.ID, Brush: b.ID, FaceIdx: fi,
						Alpha: 1, sourceOrder: order,
					})
				}
			}
			for fi := range drawn.Faces {
				f := &drawn.Faces[fi]
				if f.NoDraw {
					continue // never drawn, so there is nothing to keep
				}
				pts := facePoints(dws, f)
				pl, ok := bsp.PlaneFromPoints(pts)
				if !ok {
					continue
				}
				faces = append(faces, Face{
					Points: pts, Plane: pl, UVOrigin: pts[0],
					Chunk: ch.ID, Brush: b.ID, FaceIdx: fi, Detail: b.HasDetail(),
					Texture: f.Texture, BlendTexture: f.BlendTexture, BlendMask: f.BlendMask,
					UVOffset: f.UVOffset, UVScale: f.UVScale, UVRotation: f.UVRotation,
					Smooth: f.Smooth, SmoothAngle: f.SmoothAngle,
					Alpha: f.Alpha, DoubleSided: f.DoubleSided,
					// order is stashed on the face so the coplanar tie-break
					// below can compare it against a solid's without a second
					// lookup; it is not part of the output.
					sourceOrder: order,
				})
			}
			order++
		}
	}
	return solids, faces
}

// worldPoints carries a brush's HULL verts out through its chunk's placement.
func worldPoints(b *world.Brush, model mgl32.Mat4) []mgl32.Vec3 {
	return meshWorldPoints(&b.BrushMesh, model)
}

// meshWorldPoints is worldPoints for either of a brush's two meshes.
func meshWorldPoints(b *world.BrushMesh, model mgl32.Mat4) []mgl32.Vec3 {
	out := make([]mgl32.Vec3, len(b.Verts))
	for i, v := range b.Verts {
		if model == mgl32.Ident4() {
			out[i] = v
			continue
		}
		r := model.Mul4x1(v.Vec4(1))
		out[i] = mgl32.Vec3{r.X(), r.Y(), r.Z()}
	}
	return out
}

// facePoints resolves a face's index loop against an already-transformed vertex
// list.
func facePoints(ws []mgl32.Vec3, f *world.BrushFace) []mgl32.Vec3 {
	pts := make([]mgl32.Vec3, 0, len(f.Indices))
	for _, vi := range f.Indices {
		if vi >= 0 && vi < len(ws) {
			pts = append(pts, ws[vi])
		}
	}
	return pts
}

// clipFaceAgainstSolids returns the fragments of f that are outside every
// solid.
//
// Each solid takes the fragments it is handed and gives back what stuck out of
// it, so the work is a fold: a face buried by the first brush produces nothing
// and every later brush is skipped.
func clipFaceAgainstSolids(f Face, solids []solid) []Face {
	frags := [][]mgl32.Vec3{f.Points}
	lo, hi := bsp.Bounds(f.Points)

	for si := range solids {
		s := &solids[si]
		if len(frags) == 0 {
			break
		}
		if !bsp.BoxesOverlap(lo, hi, s.lo, s.hi) {
			continue
		}
		planes, clips := clipPlanesFor(&f, s)
		if !clips {
			continue
		}
		var next [][]mgl32.Vec3
		for _, frag := range frags {
			next = append(next, outsideOf(frag, planes)...)
		}
		frags = next
	}

	out := make([]Face, 0, len(frags))
	for _, frag := range frags {
		if bsp.PolygonArea(frag) < bsp.MinFragmentArea {
			continue
		}
		g := f
		g.Points = frag
		out = append(out, g)
	}
	return out
}

// clipPlanesFor decides how a solid clips this face, and which of its planes do
// the clipping. clips is false when the solid leaves the face alone entirely.
//
// Three cases, and two of them are about surfaces that TOUCH rather than
// overlap — which is most of what a blocked-out level is made of:
//
//   - A face never clips against its OWN brush. Every face of a brush lies on
//     one of that brush's own planes, and "on" is not "inside", so in exact
//     arithmetic this would be a no-op. The epsilon that makes the rest of the
//     compiler terminate makes it a coin toss instead, and a brush that eats
//     its own faces is not a subtle failure.
//
//   - Two COPLANAR faces pointing at each other — the underside of one slab
//     lying exactly on the top of the next, a pillar standing flush on a floor
//     — must resolve to exactly ONE surviving surface over the region they
//     share. Not both, which z-fights; not neither, which leaves a hole. The
//     face on the earlier brush wins: arbitrary, but stable, which is what
//     matters — a level has to compile the same way twice.
//
//     The loser is clipped against the solid's OTHER planes, with the shared
//     one left out. That is the part worth reading twice. Clipping against all
//     of them would keep the face whole, because a polygon lying exactly on a
//     plane classifies as in FRONT of it and so reads as outside the solid.
//     Dropping the face whole would be wrong the other way, since the two only
//     conflict where the solids actually overlap. Leaving the shared plane out
//     turns the solid into the infinite prism through its other faces, and the
//     part of the face inside THAT is exactly the region the winner covers.
func clipPlanesFor(f *Face, s *solid) (planes []bsp.Plane, clips bool) {
	if f.sourceOrder == s.order {
		return nil, false
	}
	for i, pl := range s.planes {
		if !pl.SameAs(f.Plane.Flip()) {
			continue
		}
		// Flush against this solid's surface, facing into it.
		if f.sourceOrder < s.order {
			return nil, false // this face wins and is kept whole
		}
		out := make([]bsp.Plane, 0, len(s.planes)-1)
		out = append(out, s.planes[:i]...)
		out = append(out, s.planes[i+1:]...)
		return out, true
	}
	return s.planes, true
}

// outsideOf returns the parts of a polygon that are not inside the region
// bounded by planes.
//
// The polygon is cut by each plane in turn. What is in FRONT of
// a plane is outside the solid by that plane alone and is kept immediately;
// what is behind carries on to the next plane. A fragment that survives every
// plane on the "behind" side is inside the region and is dropped.
//
// That is the standard convex-subtraction fold, and it is exact: the union of
// what it keeps plus what it drops is the original polygon, so nothing is
// double-counted and nothing goes missing.
func outsideOf(pts []mgl32.Vec3, planes []bsp.Plane) [][]mgl32.Vec3 {
	var kept [][]mgl32.Vec3
	remaining := pts
	for _, pl := range planes {
		if remaining == nil {
			break
		}
		front, back := bsp.SplitPolygon(remaining, pl)
		if front != nil {
			kept = append(kept, front)
		}
		remaining = back
	}
	// `remaining` is now the part behind every plane — inside the region — and
	// is deliberately not returned.
	return kept
}

// SortFaces puts the output in a stable order: by chunk, then brush, then the
// source face index, then by the fragment's own first point.
//
// Compilation has to be deterministic. The BSP's plane choice, the bsp.PVS's leaf
// numbering and the baked lighting all follow from the order faces arrive in,
// so a level that compiled to one thing on Tuesday and another on Wednesday
// would make every diff of a compiled level unreadable.
func SortFaces(faces []Face) {
	sort.SliceStable(faces, func(i, j int) bool {
		a, b := &faces[i], &faces[j]
		if a.Chunk != b.Chunk {
			return a.Chunk < b.Chunk
		}
		if a.Brush != b.Brush {
			return a.Brush < b.Brush
		}
		if a.FaceIdx != b.FaceIdx {
			return a.FaceIdx < b.FaceIdx
		}
		for k := 0; k < 3; k++ {
			if a.Points[0][k] != b.Points[0][k] {
				return a.Points[0][k] < b.Points[0][k]
			}
		}
		return false
	})
}
