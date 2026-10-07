package levelio

// compiled_load.go — standing a compiled level up.
//
// The ordinary loader reads YAML, builds chunks and uploads every brush face.
// This one reads a .fbsp and differs in exactly two respects: the surfaces come
// from the compiled list rather than from the brushes, and the chunks the
// compiler consumed are built WITHOUT geometry.
//
// Everything else is the same code, in the same order, driven by the same
// LoadJob. The level's entities, volumes, lights, spawn, sprites, emitters,
// scenery, sounds, sky, fog and scripts all go through loadLevelObjects exactly
// as they do for an authored level — none of them was compiled, and none should
// have a second implementation to drift from the first.
//
// # The chunks with no geometry
//
// A compiled chunk keeps its brushes and loses only its meshes. That is enough
// for everything that is not drawing:
//
//   - Collision resolves a body against ConvexHull{Pts: b.Verts} — the brush's
//     own vertices, no mesh involved.
//   - A particle tests b.FacePoints(f), likewise.
//   - A script addresses a brush by ID.
//
// The one thing that did want the mesh is the geometry raycast, which walks
// each face's triangles. It reads the compiled surfaces instead when a level
// has them (collision/raycast.go), which is better than the alternative:
// keeping a second copy of every mesh in memory purely so rays could hit it,
// and having rays hit surfaces CSG had already established were invisible.

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/bsp"
	"foundry/internal/compile"
	"foundry/internal/world"
)

// LoadCompiledLevel reads a compiled level and builds the runtime level from
// it, uploading what has to go to the GPU.
//
// The counterpart of LoadLevel and interchangeable with it: what comes back is
// an ordinary world.Level that every pass, resolver and script already knows
// how to work with. Driven through the same stepped job the loading screen
// pumps, for the reason level_load_job.go gives — one loader, driven two ways,
// so a level cannot load differently depending on which drove it.
func LoadCompiledLevel(path string) (world.Level, error) {
	job, err := BeginLoadCompiled(path)
	if err != nil {
		return world.Level{}, err
	}
	job.waitForRead()
	for !job.Step() {
		job.waitForStatic()
	}
	if err := job.Err(); err != nil {
		return world.Level{}, err
	}
	return *job.Level(), nil
}

// StaticGeometryFrom uploads all of a compiled level's surfaces at once.
//
// For a caller with the artefact already in hand and no loading screen to
// animate — the editor's compiled preview. A level change during play goes
// through the stepped job instead.
func StaticGeometryFrom(c *CompiledLevel) (*world.StaticGeometry, error) {
	var lvl world.Level
	if err := buildStaticAll(c, &lvl, nil); err != nil {
		return nil, err
	}
	if lvl.Static == nil {
		// No faces at all: still a valid, empty static world rather than nil,
		// so a caller can install it without a special case.
		lvl.Static = &world.StaticGeometry{Tree: c.Tree, Vis: c.PVS}
	}
	return lvl.Static, nil
}

// loadCompiledChunkRange builds chunks [from, from+count) of a compiled level.
//
// The counterpart of loadChunkRange, and the one place the two loaders really
// differ: a chunk the compiler consumed is installed with its brushes and
// without their meshes.
func loadCompiledChunkRange(c *CompiledLevel, level *world.Level, texNeeded map[string]bool, from, count int) (int, error) {
	to := from + count
	if to > len(c.Chunks) {
		to = len(c.Chunks)
	}
	for i := from; i < to; i++ {
		ch := c.Chunks[i]
		// Built whether it starts enabled or not, exactly as in loadRawChunk,
		// unless it opted into unloading while disabled.
		ch.Unloaded = !ch.WantsGeometry()
		for bi := range ch.Brushes {
			ch.Brushes[bi].EachFace(func(f *world.BrushFace) {
				noteFaceTextures(texNeeded, f)
			})
		}
		if ch.Terrain != nil {
			for _, tex := range ch.Terrain.Layers {
				if tex != "" {
					texNeeded[tex] = true
				}
			}
		}
		if !ch.Unloaded {
			for bi := range ch.Brushes {
				// A compiled chunk's static brushes are drawn out of the
				// compiled surface list and need no mesh of their own. Its
				// DYNAMIC ones do: the compiler left them out of the bake
				// because they move, so they are still live geometry and are
				// drawn, shadowed and picked from their own buffers. See
				// Chunk.DrawsBrushLive, which is where that rule is written.
				if !ch.DrawsBrushLive(&ch.Brushes[bi]) {
					continue
				}
				if err := ch.Brushes[bi].Load(); err != nil {
					return i, fmt.Errorf("chunk %q brush %q: %w", ch.ID, ch.Brushes[bi].ID, err)
				}
			}
		}
		ch.RecomputeBounds()
		level.Chunks = append(level.Chunks, ch)
	}
	return to, nil
}

// parallelStaticBuild spreads the compiled surfaces' mesh builds over every
// core (buildStaticAll). Off, they are built in order on the calling
// goroutine, which is what the test comparing the two forces.
var parallelStaticBuild = true

// staticBuildBatch is how many surfaces a worker claims at a time: enough
// that claiming is not the cost, few enough that the cores finish together.
const staticBuildBatch = 256

// buildStaticRange builds compiled surfaces [from, from+count) in order, and
// finishes the static world after the last one.
func buildStaticRange(c *CompiledLevel, level *world.Level, from, count int) (int, error) {
	beginStatic(c, level)
	to := from + count
	if to > len(c.Faces) {
		to = len(c.Faces)
	}
	for i := from; i < to; i++ {
		if err := buildStaticSurface(c, level.Static, i); err != nil {
			return i, err
		}
	}
	if to == len(c.Faces) {
		finishStatic(c, level)
	}
	return to, nil
}

// buildStaticAll builds every compiled surface and finishes the static world.
//
// Each surface is built from its own face into its own slot of Surfaces and
// touches nothing else — no GL either, since a compiled surface is CPU-only
// (the renderer batches them) — so they are built on every core at once, and
// the result is the same surfaces, bit for bit, as building them in order
// (TestTheParallelStaticBuildIsTheSerialOne). done, if not nil, counts
// surfaces as they finish, for a progress bar read from another goroutine.
//
// An error names the LOWEST failing surface, as the serial build would have:
// which surface a broken file reports should not depend on scheduling.
func buildStaticAll(c *CompiledLevel, level *world.Level, done *atomic.Int64) error {
	beginStatic(c, level)
	n := len(c.Faces)
	workers := runtime.GOMAXPROCS(0)
	if !parallelStaticBuild || workers < 2 || n < 2*staticBuildBatch {
		if _, err := buildStaticRange(c, level, 0, n); err != nil {
			return err
		}
		if done != nil {
			done.Store(int64(n))
		}
		return nil
	}
	var next atomic.Int64
	var mu sync.Mutex
	failAt, failErr := n, error(nil)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				from := int(next.Add(staticBuildBatch)) - staticBuildBatch
				if from >= n {
					return
				}
				to := min(from+staticBuildBatch, n)
				for i := from; i < to; i++ {
					if err := buildStaticSurface(c, level.Static, i); err != nil {
						mu.Lock()
						if i < failAt {
							failAt, failErr = i, err
						}
						mu.Unlock()
						break
					}
				}
				if done != nil {
					done.Add(int64(to - from))
				}
			}
		}()
	}
	wg.Wait()
	if failErr != nil {
		return failErr
	}
	finishStatic(c, level)
	return nil
}

// beginStatic gives the level its static world, with a slot for every
// surface, if it has none yet.
func beginStatic(c *CompiledLevel, level *world.Level) {
	if level.Static == nil {
		level.Static = &world.StaticGeometry{
			Tree:     c.Tree,
			Vis:      c.PVS,
			Surfaces: make([]world.StaticSurface, len(c.Faces)),
		}
	}
}

// buildStaticSurface builds compiled surface i into its slot.
//
// Through the same BuildBrushFaceMesh and ApplyUVTransform a brush face goes
// through, so a compiled surface and an authored one are the same mesh built
// the same way — which is what makes the two interchangeable in the draw loop,
// and is the only reason a compiled level looks identical to the one it came
// from.
func buildStaticSurface(c *CompiledLevel, static *world.StaticGeometry, i int) error {
	f := &c.Faces[i]
	if f.NoDraw {
		// Structure, not surface: it shaped the tree and is in no cell's
		// draw list (compile/csg.go, Face.NoDraw). The light stage drops
		// these, so only a level compiled without one still carries them —
		// the surface is left zeroed and the draw loop skips it for having
		// no vertices.
		return nil
	}
	surf := &static.Surfaces[i]
	surf.DoubleSided = f.DoubleSided
	surf.Chunk, surf.Brush = f.Chunk, f.Brush
	surf.CoveredBy = f.CoveredBy
	surf.Normal = f.Plane.N
	surf.Lo, surf.Hi = bsp.Bounds(f.Points)

	// From the SOURCE face's corner and plane, not this fragment's: every
	// piece of one authored face has to land on the same texture, or every
	// cut in the level is a seam.
	verts, indices := world.BuildBrushFaceMeshFrom(f.Points, f.DoubleSided, f.Plane.N, f.UVOrigin)
	if len(verts) == 0 {
		return nil
	}
	if f.UVMapped {
		// An unwrapped face: its UVs are a function of position, evaluated
		// here at this fragment's own corners (compile.Face.UVMap).
		world.ApplyUVMap(verts, f.UVMap)
	}
	world.ApplyUVTransform(verts, f.UVOffset, f.UVScale, f.UVRotation)

	g := &surf.Geometry
	g.Smooth, g.SmoothAngle = f.Smooth, f.SmoothAngle
	// No buffers of its own: the renderer batches a compiled level's
	// surfaces per source chunk and material (render/static_batch.go).
	g.CPUOnly = true
	g.VertexLight = bakedLightRows(f, f.Light, f.BackLight, f.Indirect, f.BackIndirect, len(verts))
	g.VertexLightSmooth = bakedLightRows(f, f.SmoothLight, f.SmoothBackLight, f.SmoothIndirect, f.SmoothBackIndirect, len(verts))
	// The face's own smoothed normals, interpolated from its source face
	// (compile/smooth.go). A flat face has none, and shades from the facet
	// the way a flat face would anyway.
	g.VertexNormals = compiledNormalRows(f.VertexNormals(), len(f.Points), len(verts))
	if err := g.Load(verts, indices, f.Texture, mgl32.Vec3{}); err != nil {
		return fmt.Errorf("compiled face %d: %w", i, err)
	}
	g.Alpha = f.Alpha
	g.BlendTexture = f.BlendTexture
	g.BlendMask = f.BlendMask
	return nil
}

// finishStatic is what follows the last surface.
func finishStatic(c *CompiledLevel, level *world.Level) {
	// The compiler's cut of the surfaces into clusters, for the
	// GPU-driven cull (world/static_clusters.go). The renderer checks
	// each group's against the surfaces it actually built and cuts any
	// that do not match.
	level.Static.SetClusters(c.Clusters)
	// A streamed chunk is baked but starts switched off, and its surfaces
	// have to start hidden with it. SyncChunkGeometry would hide them on
	// the first frame, which is one frame too late: the area would flash
	// up as the level appeared. After the last surface rather than the
	// first, because hiding reads which chunk each surface came from.
	for ci := range level.Chunks {
		if ch := &level.Chunks[ci]; ch.Compiled && ch.Disabled {
			level.Static.SetChunkHidden(ch.ID, true)
		}
	}
}

func noteFaceTextures(texNeeded map[string]bool, f *world.BrushFace) {
	for _, tex := range []string{f.Texture, f.BlendTexture, f.BlendMask} {
		if tex != "" {
			texNeeded[tex] = true
		}
	}
}

func noteCompiledFaceTextures(texNeeded map[string]bool, f *compile.Face) {
	for _, tex := range []string{f.Texture, f.BlendTexture, f.BlendMask} {
		if tex != "" {
			texNeeded[tex] = true
		}
	}
}

// bakedLightRows lines the baked values up with the mesh's vertices.
//
// BuildBrushFaceMesh emits one vertex per polygon corner, then — for a
// double-sided face — a second copy of the whole ring, facing the other way,
// which takes back: that side was baked facing the other way too.
//
// A face with no light, or with a light list that does not match its points,
// gets none rather than a guess: the shader's flag is set from whether the
// buffer exists, so "no baked light" cleanly means "light it the ordinary way".
//
// Each row is gfx.LightStride floats: the colour, the baked sun's share
// (Face.SunShare) — -1, "the bake's sun is not to be touched", for a face
// without one — and
// the indirect part of the colour (Face.Indirect), which live GI takes back off
// before adding its own. A face with no indirect list carries zero there.
func bakedLightRows(f *compile.Face, front, back, indFront, indBack []mgl32.Vec3, n int) [][]float32 {
	if len(front) == 0 || len(front) != len(f.Points) {
		return nil
	}
	if len(back) != len(front) {
		back = front // a file baked before faces had a far side
	}
	if len(indFront) != len(front) {
		indFront = nil
	}
	if len(indBack) != len(front) {
		indBack = indFront
	}
	shareFront, shareBack := f.SunShare, f.BackSunShare
	if len(shareFront) != len(front) {
		shareFront = nil
	}
	if len(shareBack) != len(front) {
		shareBack = shareFront
	}
	// One backing array for every row rather than an allocation per vertex;
	// each row is capped so it cannot grow into its neighbour.
	out := make([][]float32, n)
	flat := make([]float32, n*bakedLightRow)
	for i := range out {
		k := i % len(front)
		l, share, ind := front[k], shareFront, indFront
		if i >= len(front) {
			l, share, ind = back[k], shareBack, indBack
		}
		sun := float32(-1)
		if share != nil {
			sun = share[k]
		}
		var in mgl32.Vec3
		if ind != nil {
			in = ind[k]
		}
		row := flat[i*bakedLightRow : (i+1)*bakedLightRow : (i+1)*bakedLightRow]
		row[0], row[1], row[2], row[3], row[4], row[5], row[6] = l.X(), l.Y(), l.Z(), sun, in.X(), in.Y(), in.Z()
		out[i] = row
	}
	return out
}

// bakedLightRow is the floats in one of bakedLightRows' rows: gfx.LightStride.
const bakedLightRow = 7

// compiledNormalRows lays a face's per-point normals out per vertex of its mesh
// — the far side of a double-sided face, whose vertices follow the near
// side's, facing the other way. Nil for a flat face.
func compiledNormalRows(ns []mgl32.Vec3, points, n int) [][]float32 {
	if len(ns) == 0 || len(ns) != points {
		return nil
	}
	out := make([][]float32, n)
	flat := make([]float32, 3*n)
	for i := range out {
		v := ns[i%points]
		if i >= points {
			v = v.Mul(-1)
		}
		row := flat[3*i : 3*i+3 : 3*i+3]
		row[0], row[1], row[2] = v.X(), v.Y(), v.Z()
		out[i] = row
	}
	return out
}
