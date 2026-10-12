package stages

// rocks_run.go — the rocks stage's run: every variant generated, cooked and
// described.
//
//  1. VARIANTS side by side (rocks.Generate), as many at once as cook_memory
//     allows for the budget — a variant holds its mesh at the mesher's
//     oversampled count while it is decimated, and its DAG while it cooks —
//     each with its share of the workers (generation is itself parallel, the
//     decimation and much of the cook are not, so two at a time on a hero
//     cliff beats one with every core).
//  2. THE COOK (meshout.Cook), per variant, with its collision: HULLS — the
//     convex decomposition, what a body rolls off — for a rock under
//     hull_size, the MESH alone above it (the cook then skips the
//     decomposition: on a 600k-triangle cliff chunk that was most of the
//     cook), or none (a pebble; cooked mesh-only, placed without collision).
//     The cooked file carries the colours (format 5).
//  3. rocks.yaml (rocks.Manifest): every variant's file, size, bounds,
//     footprint, height, top, back, triangles, collision and timings — what
//     phase 3's scatter and cliff_wall place from. docs/canyon/PHASE_2.md
//     documents it.
//  4. With gallery on, every variant placed in a row along +X from
//     gallery_origin, each at its footprint's spacing, to look at in a level.

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"foundry/internal/model"
	"foundry/internal/procgen"
	"foundry/internal/procgen/meshout"
	"foundry/internal/procgen/noise"
	"foundry/internal/procgen/rocks"

	"github.com/go-gl/mathgl/mgl32"
)

// RocksArtifact is a rocks stage's product for downstream stages: its
// manifest, and where its files are (Dir absolute, RelDir under resources/ —
// a variant's Model is RelDir + "/" + its File).
type RocksArtifact struct {
	Dir, RelDir string
	Manifest    *rocks.Manifest
}

// ModelPath is variant v's resources-relative model path, for a placement.
// A variant naming a Model elsewhere (a Poly Haven import) is that.
func (a *RocksArtifact) ModelPath(v *rocks.VariantMeta) string {
	if v.Model != "" {
		return v.Model
	}
	return a.RelDir + "/" + v.File
}

// rockGenBytesPerTriangle is a variant's peak memory per target triangle
// while it is made: the oversampled mesh (three times the target for a
// scatter rock), the simplifier's quadrics and heap, the bake's buffers.
// Measured on boulders and cliff chunks (docs/canyon/PHASE_2.md).
const rockGenBytesPerTriangle = 1500

// rockCollision is what variant extent should collide as, for the stage's
// collision choice.
func rockCollision(choice, preset string, extent [3]float32, hullSize float64) string {
	if choice != "auto" {
		return choice
	}
	if preset == "pebble" {
		return "none"
	}
	if float64(max(extent[0], extent[1], extent[2])) <= hullSize {
		return "hulls"
	}
	return "mesh"
}

// rockImport is a finished rock as the cook's ImportMesh: one slot, the
// stage's texture (or none: the vertex colour is the albedo).
func rockImport(m *rocks.Mesh, texture string) *model.ImportMesh {
	im := &model.ImportMesh{
		Positions: make([]mgl32.Vec3, len(m.Positions)),
		Normals:   make([]mgl32.Vec3, len(m.Normals)),
		UVs:       make([]mgl32.Vec2, len(m.UVs)),
		Colors:    m.Colors,
		Indices:   m.Indices,
		TriSlot:   make([]int32, m.TriangleCount()),
		Slots:     []model.ImportSlot{{Name: "rock", Texture: texture, BaseColor: [4]float32{1, 1, 1, 1}}},
	}
	for i := range m.Positions {
		im.Positions[i] = m.Positions[i]
		im.Normals[i] = m.Normals[i]
		im.UVs[i] = m.UVs[i]
	}
	return im
}

func runRocks(rc *procgen.RunContext) error {
	p := rc.Params
	s := rocksSettings(&rc.Env)
	n := p.Int("variants")
	cook := p.Bool("cook")
	tex := p.Str("texture")
	_, _, _, ptris := rocks.PresetDoc(s.Preset)
	tris := s.Triangles
	if tris <= 0 {
		tris = ptris
	}
	collision := p.Str("collision")
	cookPer := cookMeshOnlyBytesPerTriangle
	_, psize, _, _ := rocks.PresetDoc(s.Preset)
	if s.Size > 0 {
		psize = s.Size
	}
	if collision == "hulls" || (collision == "auto" && psize*(1+s.SizeJitter) <= p.Float("hull_size")) {
		cookPer = cookBytesPerTriangle // the decomposition is cooked
	}
	per := float64(tris) * float64(rockGenBytesPerTriangle+cookPer)
	conc := int(float64(p.Int("cook_memory")) * (1 << 20) / per)
	workers := rc.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	conc = min(max(conc, 1), n, max(workers/2, 1))
	each := max(workers/conc, 1)
	rc.Logf("%d %s variants of %d triangles, %d at a time on %d workers each", n, s.Preset, tris, conc, each)

	sem := make(chan struct{}, conc)
	metas := make([]rocks.VariantMeta, n)
	var done atomic.Int64
	t0 := time.Now()
	err := rc.Parallel(n, func(i int) error {
		sem <- struct{}{}
		defer func() { <-sem }()
		if err := rc.Err(); err != nil {
			return err
		}
		vs := s
		vs.Workers = each
		seed := variantSeed(rc.Seed, i)
		r, err := rocks.Generate(vs, seed)
		if err != nil {
			return err
		}
		name := variantName(s.Preset, i)
		meta := r.Meta
		meta.Name = name
		meta.PlaceCollision = rockCollision(collision, s.Preset, meta.Extent, p.Float("hull_size"))
		meta.Collision = "mesh"
		if meta.PlaceCollision == "hulls" {
			meta.Collision = "hulls"
		}
		m := rockImport(r.Mesh, tex)
		t1 := time.Now()
		if cook {
			meta.File = name + ".fnm"
			if _, err := meshout.Cook(m, rc.Path(meta.File), meshout.CookOptions{
				CollisionMesh: meta.Collision == "mesh", CollisionHulls: meta.Collision == "hulls"}); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		} else {
			meta.File = name + ".glb"
			if err := meshout.WriteGLB(rc.Path(meta.File), m); err != nil {
				return err
			}
		}
		meta.CookMS = math.Round(float64(time.Since(t1).Microseconds())/100) / 10
		var size int64
		if st, err := os.Stat(rc.Path(meta.File)); err == nil {
			size = st.Size()
		}
		metas[i] = meta
		rc.Logf("%s: %d tris (meshed %d at %.1f cm), mesh %.0f ms, simplify %.0f ms, bake %.0f ms, cook %.0f ms, %d KB, %.2f×%.2f×%.2f m, %s",
			name, meta.Triangles, meta.MeshTriangles, meta.Cell*100, meta.MeshMS, meta.SimplifyMS, meta.BakeMS, meta.CookMS,
			size>>10, meta.Extent[0], meta.Extent[1], meta.Extent[2], meta.Collision)
		rc.Progress(float32(done.Add(1)) / float32(n))
		return nil
	})
	if err != nil {
		return err
	}
	rc.Logf("%d variants in %s", n, time.Since(t0).Round(time.Millisecond))
	man := &rocks.Manifest{Version: rocks.ManifestVersion, Preset: s.Preset, Lithology: s.Lithology, Palette: s.Palette,
		Frame: rocks.FrameDoc, Variants: metas}
	if man.Material, err = rocksTriplanarMaterial(rc); err != nil {
		return err
	}
	data, err := man.Encode()
	if err != nil {
		return err
	}
	if err := rc.WriteFile(rocks.ManifestFile, data); err != nil {
		return err
	}
	art := &RocksArtifact{Dir: rc.Dir, RelDir: rc.RelDir, Manifest: man}
	rc.Artifact = art
	if p.Bool("gallery") {
		placeGallery(rc, art, p.Vec3("gallery_origin"))
	}
	return nil
}

// placeGallery puts every variant in a row along +X from origin.
func placeGallery(rc *procgen.RunContext, a *RocksArtifact, origin [3]float64) {
	x := origin[0]
	for i := range a.Manifest.Variants {
		v := &a.Manifest.Variants[i]
		r := float64(v.Radius)
		x += r
		pl := procgen.Placement{ID: v.Name, Model: a.ModelPath(v), Material: a.Manifest.Material,
			Position:  procgen.Point3{X: float32(x), Y: float32(origin[1]), Z: float32(origin[2])},
			Collision: v.PlaceCollision, Chunk: procgen.ChunkAuto}
		rc.Place(pl)
		x += r + math.Max(0.25*r, 0.05)
	}
}

func loadRocks(res *procgen.StageResult) (any, error) {
	m, err := rocks.LoadManifest(res.Dir)
	if err != nil {
		return nil, err
	}
	return &RocksArtifact{Dir: res.Dir, RelDir: res.RelDir, Manifest: m}, nil
}

// variantSeed is variant i's seed: the stage's, and the index.
func variantSeed(stageSeed uint64, i int) uint64 {
	return noise.Hash64(stageSeed, 0x726f636b, uint64(i))
}
