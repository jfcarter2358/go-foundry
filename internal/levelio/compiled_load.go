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
	for !job.Step() {
	}
	if err := job.Err(); err != nil {
		return world.Level{}, err
	}
	return *job.Level(), nil
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
		// A chunk authored disabled is not built at all, exactly as in
		// loadRawChunk — it is what a streaming script switches on later.
		ch.Unloaded = ch.Disabled
		for bi := range ch.Brushes {
			for fi := range ch.Brushes[bi].Faces {
				noteFaceTextures(texNeeded, &ch.Brushes[bi].Faces[fi])
			}
		}
		if !ch.Compiled && !ch.Disabled {
			for bi := range ch.Brushes {
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

// buildStaticRange uploads compiled surfaces [from, from+count).
//
// Through the same BuildBrushFaceMesh and ApplyUVTransform a brush face goes
// through, so a compiled surface and an authored one are the same mesh built
// the same way — which is what makes the two interchangeable in the draw loop,
// and is the only reason a compiled level looks identical to the one it came
// from.
func buildStaticRange(c *CompiledLevel, level *world.Level, from, count int) (int, error) {
	if level.Static == nil {
		level.Static = &world.StaticGeometry{
			Tree:     c.Tree,
			Vis:      c.PVS,
			Surfaces: make([]world.StaticSurface, len(c.Faces)),
		}
	}
	to := from + count
	if to > len(c.Faces) {
		to = len(c.Faces)
	}
	for i := from; i < to; i++ {
		f := &c.Faces[i]
		surf := &level.Static.Surfaces[i]
		surf.Detail = f.Detail
		surf.DoubleSided = f.DoubleSided
		surf.Chunk, surf.Brush = f.Chunk, f.Brush
		surf.Normal = f.Plane.N
		surf.Lo, surf.Hi = bsp.Bounds(f.Points)

		// From the SOURCE face's corner and plane, not this fragment's: every
		// piece of one authored face has to land on the same texture, or every
		// cut in the level is a seam.
		verts, indices := world.BuildBrushFaceMeshFrom(f.Points, f.DoubleSided, f.Plane.N, f.UVOrigin)
		if len(verts) == 0 {
			continue
		}
		world.ApplyUVTransform(verts, f.UVOffset, f.UVScale, f.UVRotation)

		g := &surf.Geometry
		g.Smooth, g.SmoothAngle = f.Smooth, f.SmoothAngle
		g.VertexLight = bakedLightRows(f, len(verts))
		if err := g.Load(verts, indices, f.Texture, mgl32.Vec3{}); err != nil {
			return i, fmt.Errorf("compiled face %d: %w", i, err)
		}
		g.Alpha = f.Alpha
		g.BlendTexture = f.BlendTexture
		g.BlendMask = f.BlendMask
	}
	return to, nil
}

func noteFaceTextures(texNeeded map[string]bool, f *world.BrushFace) {
	for _, tex := range []string{f.Texture, f.BlendTexture, f.BlendMask} {
		if tex != "" {
			texNeeded[tex] = true
		}
	}
	if f.Detail.Enabled && f.Detail.ScatterTex != "" {
		texNeeded[f.Detail.ScatterTex] = true
	}
}

func noteCompiledFaceTextures(texNeeded map[string]bool, f *compile.Face) {
	for _, tex := range []string{f.Texture, f.BlendTexture, f.BlendMask} {
		if tex != "" {
			texNeeded[tex] = true
		}
	}
	if f.Detail.Enabled && f.Detail.ScatterTex != "" {
		texNeeded[f.Detail.ScatterTex] = true
	}
}

// bakedLightRows lines the baked values up with the mesh's vertices.
//
// BuildBrushFaceMesh emits one vertex per polygon corner, then — for a
// double-sided face — a second copy of the whole ring. The modulo is what
// covers that second copy: both sides of a surface were lit as one, so both
// take the same values.
//
// A face with no light, or with a light list that does not match its points,
// gets none rather than a guess: the shader's flag is set from whether the
// buffer exists, so "no baked light" cleanly means "light it the ordinary way".
func bakedLightRows(f *compile.Face, n int) [][]float32 {
	if len(f.Light) == 0 || len(f.Light) != len(f.Points) {
		return nil
	}
	out := make([][]float32, n)
	for i := range out {
		l := f.Light[i%len(f.Points)]
		out[i] = []float32{l.X(), l.Y(), l.Z()}
	}
	return out
}
