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
	"slices"
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

	// CoveredBy marks surface that CSG took out from under the solid of
	// another chunk, where one of the two is streamed. Such a fragment is
	// buried only while every chunk named here is switched on, so it is kept
	// rather than dropped, and the runtime draws it only while all of them are
	// off (world.StaticSurface.CoveredBy). Otherwise the zone at the edge of a
	// streamed window shows a hole wherever its switched-off neighbour
	// overlapped it. A cover face lies inside solid as far as the tree knows,
	// so it takes no part in the partition, is in no cell and is drawn past
	// the PVS.
	CoveredBy []string

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

	// UVMap, when UVMapped, replaces that projection altogether: the source face
	// stated its UVs per corner (an atlas unwrap — world/brush_uv.go), and a
	// fragment has none of those corners to carry them. So they are fitted once,
	// on the whole source face, into a function of position, and every fragment
	// evaluates it at its own points. A cut then lands on exactly the texels the
	// uncut face had there, which is what keeps a painted wall painted.
	UVMap    world.UVMap
	UVMapped bool

	// SmoothPoly and SmoothCorner are the SOURCE face's corners and the
	// smoothed normal at each, in world space — how the face shades once
	// smoothing is on (world.BrushMesh.CornerNormals), inherited by every
	// fragment the face is cut into, which interpolates them at its own points
	// (VertexNormals). Nil when every corner's normal is the plane's: a flat
	// face nothing smooths into, the common case, and then there is nothing to
	// store and nothing to bake twice.
	SmoothPoly   []mgl32.Vec3
	SmoothCorner []mgl32.Vec3

	// Light is the baked per-vertex lighting, parallel to Points, taken with
	// the plane's normal. Filled by the light stage; nil until then.
	// SmoothLight is the same with VertexNormals, set only on a face that has
	// them — the release renderer picks between the two by whether the face
	// shades smooth (gfx.Geometry.BakedLightData). BackLight and
	// SmoothBackLight are both again for the far side of a double-sided face,
	// which faces the other way and so is lit by other lights.
	Light           []mgl32.Vec3
	SmoothLight     []mgl32.Vec3
	BackLight       []mgl32.Vec3
	SmoothBackLight []mgl32.Vec3

	// Indirect is the part of Light that did not come straight from a static
	// light — the ambient term plus every bounce — and SmoothIndirect,
	// BackIndirect and SmoothBackIndirect the same for the other three
	// answers, each parallel to its own. Already included in the answer it
	// belongs to: a renderer that lights the indirect part another way (live
	// GI, render/gi.go) takes this back off first, and one that does not
	// ignores it.
	Indirect           []mgl32.Vec3
	SmoothIndirect     []mgl32.Vec3
	BackIndirect       []mgl32.Vec3
	SmoothBackIndirect []mgl32.Vec3

	// SunShare is how much of the baked sun (world.LightSet.BakedSun) each
	// point kept after the bake's own shadow, 0 to 1, parallel to Points, or
	// -1 where the sun was not baked into the point's chunk at all
	// (BakeExclude); BackSunShare the same for a double-sided face's far side.
	// The renderer takes the sun's unshadowed light times this back off and,
	// wherever its shadow cascades reach, puts the sun back shadowed by them
	// instead: the bake's shadow is a per-vertex ramp a grid step wide, the
	// cascades' a per-pixel edge. Nil with no baked sun.
	SunShare     []float32
	BackSunShare []float32

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
	// chunk is the chunk the brush came from, and streamed whether that chunk
	// is marked Streamed — what StreamedClips is reported from. Empty for the
	// seal's slabs.
	chunk    string
	streamed bool
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
	faces, _, err := csg(lvl)
	return faces, err
}

// csg is CSG, also reporting the streamed-chunk clips Stats.StreamedClips
// lists.
func csg(lvl *world.Level) ([]Face, []StreamedClip, error) {
	if err := Validate(lvl); err != nil {
		return nil, nil, err
	}
	solids, faces := gatherSolids(lvl)
	streamed := map[string]bool{}
	for ci := range lvl.Chunks {
		if lvl.Chunks[ci].Streamed {
			streamed[lvl.Chunks[ci].ID] = true
		}
	}
	clips := map[[2]string]*StreamedClip{}
	report := func(faceChunk, solidChunk string, area float32) {
		k := [2]string{faceChunk, solidChunk}
		c := clips[k]
		if c == nil {
			c = &StreamedClip{FaceChunk: faceChunk, SolidChunk: solidChunk}
			clips[k] = c
		}
		c.Faces++
		c.Area += area
	}

	// Every face is clipped on its own, so the faces are clipped in parallel,
	// each against only the solids whose boxes it could overlap (solidBoxes),
	// visited in solid order — the order the clip folds them in. What comes
	// out is gathered per face and joined in face order, and the streamed
	// reports are kept per face and replayed in face order too: Area is a
	// float sum, and adding the same numbers in another order is a different
	// number. So the output is the serial walk's to the bit.
	index := newSolidBoxes(solids)
	pieces := make([][]Face, len(faces))
	reports := make([][]streamedReport, len(faces))
	clipOne := func(i int) {
		var cand []int32
		if index != nil {
			lo, hi := bsp.Bounds(faces[i].Points)
			cand = index.overlapping(lo, hi, nil)
		}
		pieces[i] = clipFaceAgainst(faces[i], solids, cand, index != nil, streamed, func(faceChunk, solidChunk string, area float32) {
			reports[i] = append(reports[i], streamedReport{faceChunk, solidChunk, area})
		})
	}
	if parallelCSG {
		parallelFor(len(faces), clipOne)
	} else {
		for i := range faces {
			clipOne(i)
		}
	}
	n := 0
	for i := range pieces {
		n += len(pieces[i])
	}
	out := make([]Face, 0, max(n, len(faces)))
	for i := range pieces {
		out = append(out, pieces[i]...)
		for _, r := range reports[i] {
			report(r.faceChunk, r.solidChunk, r.area)
		}
	}
	list := make([]StreamedClip, 0, len(clips))
	for _, c := range clips {
		list = append(list, *c)
	}
	sort.Slice(list, func(a, b int) bool {
		if list[a].FaceChunk != list[b].FaceChunk {
			return list[a].FaceChunk < list[b].FaceChunk
		}
		return list[a].SolidChunk < list[b].SolidChunk
	})
	return out, list, nil
}

// parallelCSG clips faces across cores, and indexSolids finds each face's
// solids through solidBoxes rather than by testing every solid's box. Both are
// switches only so the tests can compare against the plain serial walk.
var (
	parallelCSG = true
	indexSolids = true
)

// streamedReport is one call of csg's report, kept until the faces are joined.
type streamedReport struct {
	faceChunk, solidChunk string
	area                  float32
}

// StreamedClip is surface of one chunk that CSG removed because it was inside a
// solid of another, where at least one of the two is streamed.
//
// The compile treats every baked chunk as present at once. The surface is not
// lost — it comes back as CoveredBy fragments, drawn while the solid's chunk is
// off, so a streamed window's edge has no hole where its switched-off neighbour
// overlapped it. What still assumes both are on is the light bake (the solid's
// shadow stays painted) and the PVS (what it hid stays culled). Between
// neighbouring areas that is nothing anyone sees; between two that are
// alternatives (a hangar and the ground it hovers over) it may be. Reported,
// so an author can tell which.
type StreamedClip struct {
	FaceChunk  string  // the chunk whose surface was removed
	SolidChunk string  // the chunk whose solid removed it
	Faces      int     // how many of FaceChunk's faces lost area to it
	Area       float32 // how much surface, in square units
}

// Bakes reports whether the compiler turns this chunk's surfaces into compiled
// geometry.
//
// The one place the rule is written. Two callers read it from opposite ends —
// the compiler, deciding what to consume, and the writer of the compiled file,
// deciding what to mark as already drawn — and if they ever disagreed the
// geometry would be either dropped or drawn twice.
//
// A disabled chunk is not in the world — unless it is marked Streamed, which
// asks for it to be baked anyway and shown later by a script. A frame-owned one
// moves with a vehicle, and one marked Moves is moved by a script, and neither
// can be baked into a static world-space list. All three that are not baked are
// kept and built the ordinary way.
//
// Everything baked is compiled as present at once, so streamed chunks must be
// able to coexist — see Stats.StreamedClips, which reports the ones that look
// as though they cannot.
func Bakes(ch *world.Chunk) bool {
	return (ch.IsEnabled() || ch.Streamed) && ch.FrameOwner == "" && !ch.Moves
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
			var hull []bsp.Plane
			for fi := range b.Faces {
				if pl, ok := bsp.PlaneFromPoints(facePoints(ws, &b.Faces[fi])); ok {
					hull = append(hull, pl)
				}
			}

			if b.Solid {
				// One solid per CONVEX PIECE, not one per brush: "inside" is
				// "behind every plane", which only means the whole brush for
				// an already-convex (or Dynamic — see ConvexBrushPieces) one,
				// where this is exactly the single-element loop hull above
				// already covers. A concave brush's pieces all share this
				// brush's own `order`, so the self-skip below
				// (clipPlanesFor's f.sourceOrder == s.order) exempts every
				// sibling piece from clipping the brush's own faces, the same
				// way it already exempts a single ordinary brush from itself.
				alreadyConvex := b.Dynamic || b.IsConvex()
				for _, piece := range ch.ConvexBrushPieces(bi) {
					pws, pieceHull := ws, hull
					if !alreadyConvex {
						// ConvexBrushPieces' slow path: this piece is a
						// DecomposeConvex fragment, not b itself, so its
						// points and planes have to be rebuilt rather than
						// reusing ws/hull above.
						pws = meshWorldPoints(&piece, model)
						pieceHull = nil
						for fi := range piece.Faces {
							if pl, ok := bsp.PlaneFromPoints(facePoints(pws, &piece.Faces[fi])); ok {
								pieceHull = append(pieceHull, pl)
							}
						}
					}
					s := solid{order: order, planes: pieceHull, chunk: ch.ID, streamed: ch.Streamed}
					if len(s.planes) >= 4 {
						// Fewer than four planes cannot bound a volume, so a
						// piece with that many is malformed and clips nothing
						// rather than clipping everything.
						s.lo, s.hi = bsp.Bounds(pws)
						solids = append(solids, s)
					}
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
			corners := drawn.CornerNormals()
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
				uvOffset, uvScale, uvRotation := f.DrawUVTransform()
				cf := Face{
					Points: pts, Plane: pl, UVOrigin: pts[0],
					Chunk: ch.ID, Brush: b.ID, FaceIdx: fi, Detail: b.HasDetail(),
					Texture: f.DrawTexture(), BlendTexture: f.BlendTexture, BlendMask: f.BlendMask,
					UVOffset: uvOffset, UVScale: uvScale, UVRotation: uvRotation,
					Smooth: f.Smooth, SmoothAngle: f.SmoothAngle,
					Alpha: f.Alpha, DoubleSided: f.DoubleSided,
					// order is stashed on the face so the coplanar tie-break
					// below can compare it against a solid's without a second
					// lookup; it is not part of the output.
					sourceOrder: order,
				}
				if fi < len(corners) {
					cf.SmoothPoly, cf.SmoothCorner = worldCornerNormals(pts, corners[fi], pl.N, model)
				}
				if f.DrawsCornerUVs() {
					// Fitted to the WORLD-space corners, so the map already
					// includes the chunk's placement and a fragment — which is in
					// world space too — can evaluate it as it stands.
					cf.UVMap, cf.UVMapped = world.FitUVMap(pts, f.UVs)
				}
				faces = append(faces, cf)
			}
			order++
		}
	}
	// The level's seal last, so its slabs come after every brush in the order
	// and a brush face flush against one keeps its whole surface. See seal.go.
	if lvl.Seal.Active() {
		ss, sf := sealSolids(lvl.Seal, order)
		solids = append(solids, ss...)
		faces = append(faces, sf...)
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
	return clipFaceAgainstSolidsReporting(f, solids, nil, nil)
}

// clipFaceAgainstSolidsReporting is clipFaceAgainstSolids, calling report with
// the area a solid of another chunk removed whenever either chunk is streamed.
//
// When streamed is non-nil, that area is not thrown away: it comes back as
// CoveredBy fragments, drawn only while the chunk that buried them is off.
// A nil streamed map is the plain clip every other caller wants.
func clipFaceAgainstSolidsReporting(f Face, solids []solid, streamed map[string]bool,
	report func(faceChunk, solidChunk string, area float32)) []Face {
	return clipFaceAgainst(f, solids, nil, false, streamed, report)
}

// clipFaceAgainst is clipFaceAgainstSolidsReporting over only the solids cand
// names, in ascending order, when indexed is set — which must include every
// solid whose box overlaps the face's (solidBoxes.overlapping). Every other
// solid is skipped by the box test anyway, so the answer is the same.
func clipFaceAgainst(f Face, solids []solid, cand []int32, indexed bool, streamed map[string]bool,
	report func(faceChunk, solidChunk string, area float32)) []Face {
	// A fragment and what it is buried under. Every fragment starts visible;
	// one a streamed neighbour's solid swallows becomes a cover fragment and
	// carries on being clipped, since whatever else it is inside still buries
	// it whatever that neighbour is doing.
	type frag struct {
		pts   []mgl32.Vec3
		cover []string
	}
	frags := []frag{{pts: f.Points}}
	lo, hi := bsp.Bounds(f.Points)

	count := len(solids)
	if indexed {
		count = len(cand)
	}
	for ci := 0; ci < count; ci++ {
		si := ci
		if indexed {
			si = int(cand[ci])
		}
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
		crossStreamed := s.chunk != "" && s.chunk != f.Chunk && (s.streamed || streamed[f.Chunk])
		var next []frag
		lost := float32(0)
		for _, fr := range frags {
			if crossStreamed && slices.Contains(fr.cover, s.chunk) {
				next = append(next, fr) // already under this chunk; another of its solids adds nothing
				continue
			}
			kept, inside := outsideAndInside(fr.pts, planes)
			for _, k := range kept {
				next = append(next, frag{pts: k, cover: fr.cover})
			}
			if inside == nil {
				continue
			}
			if !crossStreamed {
				continue // buried for good
			}
			lost += bsp.PolygonArea(inside)
			if streamed == nil {
				continue
			}
			cover := append(slices.Clone(fr.cover), s.chunk)
			slices.Sort(cover)
			next = append(next, frag{pts: inside, cover: cover})
		}
		if report != nil && crossStreamed && lost > bsp.MinFragmentArea {
			report(f.Chunk, s.chunk, lost)
		}
		frags = next
	}

	out := make([]Face, 0, len(frags))
	for _, fr := range frags {
		if bsp.PolygonArea(fr.pts) < bsp.MinFragmentArea {
			continue
		}
		g := f
		g.Points = fr.pts
		if fr.cover != nil {
			g.CoveredBy = fr.cover
		}
		out = append(out, g)
	}
	return out
}

// solidBoxes is a bounding-volume tree over the solids' boxes, for finding the
// few a face could touch without testing all of them: the clip was every face
// against every solid's box, which in a big level is most of CSG's time.
type solidBoxes struct {
	nodes []solidNode
	items []int32 // solid indices, grouped by leaf
}

// solidNode is a box and either its children (count 0) or its solids
// (items[left : left+count]).
type solidNode struct {
	lo, hi             mgl32.Vec3
	left, right, count int32
}

const solidLeafSize = 8

// newSolidBoxes is the tree over solids' boxes, or nil when there are too few
// for one to be worth it (or indexSolids is off) — the caller then walks them
// all.
func newSolidBoxes(solids []solid) *solidBoxes {
	if !indexSolids || len(solids) <= 2*solidLeafSize {
		return nil
	}
	sb := &solidBoxes{}
	idx := make([]int32, len(solids))
	for i := range idx {
		idx[i] = int32(i)
	}
	centre := func(i int32, a int) float32 { return (solids[i].lo[a] + solids[i].hi[a]) / 2 }
	var build func(idx []int32) int32
	build = func(idx []int32) int32 {
		ni := int32(len(sb.nodes))
		sb.nodes = append(sb.nodes, solidNode{})
		lo, hi := solids[idx[0]].lo, solids[idx[0]].hi
		for _, i := range idx[1:] {
			for a := 0; a < 3; a++ {
				lo[a], hi[a] = min(lo[a], solids[i].lo[a]), max(hi[a], solids[i].hi[a])
			}
		}
		sb.nodes[ni].lo, sb.nodes[ni].hi = lo, hi
		if len(idx) <= solidLeafSize {
			sb.nodes[ni].left, sb.nodes[ni].count = int32(len(sb.items)), int32(len(idx))
			sb.items = append(sb.items, idx...)
			return ni
		}
		axis, span := 0, hi[0]-lo[0]
		for a := 1; a < 3; a++ {
			if s := hi[a] - lo[a]; s > span {
				axis, span = a, s
			}
		}
		// Which solids share a leaf only decides how fast the search is, never
		// what it finds, but the index breaks ties anyway to keep the tree the
		// same every run.
		slices.SortFunc(idx, func(x, y int32) int {
			cx, cy := centre(x, axis), centre(y, axis)
			switch {
			case cx < cy:
				return -1
			case cx > cy:
				return 1
			}
			return int(x - y)
		})
		mid := len(idx) / 2
		left := build(idx[:mid])
		right := build(idx[mid:])
		sb.nodes[ni].left, sb.nodes[ni].right = left, right
		return ni
	}
	build(idx)
	return sb
}

// overlapping appends to out every solid whose box might overlap lo..hi by
// bsp.BoxesOverlap — a superset of the ones that do, since a node's box holds
// all of its solids' — in ascending index order, the order the clip visits
// solids in.
func (sb *solidBoxes) overlapping(lo, hi mgl32.Vec3, out []int32) []int32 {
	var stack [64]int32
	sp := 1
	for sp > 0 {
		sp--
		n := &sb.nodes[stack[sp]]
		if !bsp.BoxesOverlap(lo, hi, n.lo, n.hi) {
			continue
		}
		if n.count > 0 {
			out = append(out, sb.items[n.left:n.left+n.count]...)
			continue
		}
		stack[sp], stack[sp+1] = n.left, n.right
		sp += 2
	}
	slices.Sort(out)
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
	kept, _ := outsideAndInside(pts, planes)
	return kept
}

// outsideAndInside is outsideOf, also returning the part behind every plane —
// inside the region — or nil when nothing is.
func outsideAndInside(pts []mgl32.Vec3, planes []bsp.Plane) (kept [][]mgl32.Vec3, inside []mgl32.Vec3) {
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
	return kept, remaining
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
