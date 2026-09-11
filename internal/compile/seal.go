package compile

// seal.go — the level's sealing box (world/seal.go), as the compiler sees it:
// fitting one, the slabs that make it seal, and what it changes about the bake
// and the report.

import (
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/bsp"
	"foundry/internal/world"
)

// SealPadding is how far past a level's contents FitSeal puts the box: room
// around the outside of the outermost wall, and for something parked at the
// edge of the level to stay in, without sealing in so much open air that
// visibility has a great deal of nothing to work through.
const SealPadding = float32(4)

// sealThickness is how thick each slab around the box is. Any thickness seals;
// this is enough that the tree never has to fit a cell between a slab's faces.
const sealThickness = float32(1)

// sealChunkName is what a slab's faces say they came from, for diagnostics.
const sealChunkName = "(seal)"

// defaultSealHalf is the box a level with nothing in it yet is given, so the
// first room built in a new level is sealed already.
var defaultSealHalf = mgl32.Vec3{32, 16, 32}

// FitSeal is a sealing box around everything a level holds that the box has to
// hold: every brush the compiler bakes, the spawn and the entities, padded by
// SealPadding. A level with none of those gets a default-sized box at the
// origin.
//
// Baked brushes only — the ones Bakes and BakesBrush accept — because those are
// what the box seals. A disabled chunk, a vehicle's interior and a dynamic brush
// take no part in the compile, and a far-off one would stretch the box over open
// space for nothing.
func FitSeal(lvl *world.Level) *world.Seal {
	var lo, hi mgl32.Vec3
	found := false
	add := func(p mgl32.Vec3) {
		if !found {
			lo, hi, found = p, p, true
			return
		}
		for k := 0; k < 3; k++ {
			lo[k] = min(lo[k], p[k])
			hi[k] = max(hi[k], p[k])
		}
	}
	if lvl != nil {
		eachBakedPoint(lvl, func(_ *world.Brush, p mgl32.Vec3) bool {
			add(p)
			return true
		})
		// A spawn aboard a vehicle is in the vehicle's coordinates, which say
		// nothing about where it is in the level.
		if lvl.Spawn != nil && lvl.Spawn.Frame == "" {
			add(lvl.Spawn.Position)
		}
		for i := range lvl.Entities {
			add(lvl.Entities[i].Position)
		}
	}
	if !found {
		return &world.Seal{Half: defaultSealHalf}
	}
	pad := mgl32.Vec3{SealPadding, SealPadding, SealPadding}
	lo, hi = lo.Sub(pad), hi.Add(pad)
	return &world.Seal{
		Position: lo.Add(hi).Mul(0.5),
		Half:     world.ClampSealHalf(hi.Sub(lo).Mul(0.5)),
	}
}

// eachBakedPoint calls fn with every hull corner, in world space, of every
// brush the compiler bakes. fn returns false to skip the rest of that brush.
func eachBakedPoint(lvl *world.Level, fn func(b *world.Brush, p mgl32.Vec3) bool) {
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if !Bakes(ch) {
			continue
		}
		model := ch.ModelMatrix()
		for bi := range ch.Brushes {
			b := &ch.Brushes[bi]
			if !BakesBrush(b) {
				continue
			}
			for _, p := range worldPoints(b, model) {
				if !fn(b, p) {
					break
				}
			}
		}
	}
}

// sealSolids is the seal as CSG input: six solid slabs around the box, each a
// solid to clip against and six faces of structure.
//
// The faces are NoDraw, as a detailed brush's hull faces are: they choose
// planes and decide what is solid in the tree, and are never drawn, lit or put
// in a cell's list. The slabs are not brushes, so collision, raycasts and the
// editor never meet them.
//
// order is the first CSG order the slabs may take. They come after every
// brush, so a brush face lying flush against a slab keeps its whole surface.
func sealSolids(s *world.Seal, order int) ([]solid, []Face) {
	lo, hi := s.Bounds()
	t := sealThickness
	slabs := []struct {
		id     string
		lo, hi mgl32.Vec3
	}{
		{"seal_-x", mgl32.Vec3{lo.X() - t, lo.Y() - t, lo.Z() - t}, mgl32.Vec3{lo.X(), hi.Y() + t, hi.Z() + t}},
		{"seal_+x", mgl32.Vec3{hi.X(), lo.Y() - t, lo.Z() - t}, mgl32.Vec3{hi.X() + t, hi.Y() + t, hi.Z() + t}},
		{"seal_-y", mgl32.Vec3{lo.X(), lo.Y() - t, lo.Z() - t}, mgl32.Vec3{hi.X(), lo.Y(), hi.Z() + t}},
		{"seal_+y", mgl32.Vec3{lo.X(), hi.Y(), lo.Z() - t}, mgl32.Vec3{hi.X(), hi.Y() + t, hi.Z() + t}},
		{"seal_-z", mgl32.Vec3{lo.X(), lo.Y(), lo.Z() - t}, mgl32.Vec3{hi.X(), hi.Y(), lo.Z()}},
		{"seal_+z", mgl32.Vec3{lo.X(), lo.Y(), hi.Z()}, mgl32.Vec3{hi.X(), hi.Y(), hi.Z() + t}},
	}
	var solids []solid
	var faces []Face
	for i, sl := range slabs {
		b := world.NewBoxBrush(sl.id, sl.lo.Add(sl.hi).Mul(0.5), sl.hi.Sub(sl.lo).Mul(0.5), "")
		so := solid{order: order + i, lo: sl.lo, hi: sl.hi}
		for fi := range b.Faces {
			pts := facePoints(b.Verts, &b.Faces[fi])
			pl, ok := bsp.PlaneFromPoints(pts)
			if !ok {
				continue
			}
			so.planes = append(so.planes, pl)
			faces = append(faces, Face{
				Points: pts, Plane: pl, UVOrigin: pts[0], NoDraw: true,
				Chunk: sealChunkName, Brush: sl.id, FaceIdx: fi,
				Alpha: 1, sourceOrder: order + i,
			})
		}
		solids = append(solids, so)
	}
	return solids, faces
}

// sealExit is where a shadow ray from a sample inside the seal stops on its way
// toward a directional light: just short of the box, where the level ends.
//
// The bake tests shadows against the tree's solid cells, and the slabs are
// solid. A ray out to the edge of the world box — which is how a sun's ray is
// cast when there is no seal — would cross one from every surface in the level
// and bake the whole of it into shadow. Beyond the seal is sky, so a ray that
// reaches the box is out.
//
// ok is false with no active seal, or for a sample outside the box, which keeps
// the ray it had.
func sealExit(s *world.Seal, origin, dir mgl32.Vec3) (end mgl32.Vec3, ok bool) {
	if !s.Active() || !s.Contains(origin) {
		return mgl32.Vec3{}, false
	}
	lo, hi := s.Bounds()
	exit := float32(math.MaxFloat32)
	for k := 0; k < 3; k++ {
		switch {
		case dir[k] > 1e-9:
			exit = min(exit, (hi[k]-origin[k])/dir[k])
		case dir[k] < -1e-9:
			exit = min(exit, (lo[k]-origin[k])/dir[k])
		}
	}
	if exit == float32(math.MaxFloat32) {
		return mgl32.Vec3{}, false
	}
	// Pulled back off the box, so the segment ends in the level's own air
	// rather than on a slab's face.
	if exit -= 0.01; exit <= 0 {
		return origin, true
	}
	return origin.Add(dir.Mul(exit)), true
}

// BrushesBeyondSeal names every baked brush that reaches outside the level's
// seal, sorted. What is outside is in the void the seal keeps out: it compiles,
// and no cell inside the level ever draws it.
func BrushesBeyondSeal(lvl *world.Level) []string {
	if lvl == nil || !lvl.Seal.Active() {
		return nil
	}
	lo, hi := lvl.Seal.Bounds()
	const eps = float32(1e-3)
	seen := map[*world.Brush]bool{}
	var out []string
	eachBakedPoint(lvl, func(b *world.Brush, p mgl32.Vec3) bool {
		for k := 0; k < 3; k++ {
			if p[k] < lo[k]-eps || p[k] > hi[k]+eps {
				if !seen[b] {
					seen[b] = true
					out = append(out, b.ID)
				}
				return false
			}
		}
		return true
	})
	sort.Strings(out)
	return out
}
