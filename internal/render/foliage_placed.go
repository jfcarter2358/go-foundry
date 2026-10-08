package render

// foliage_placed.go — plants placed by hand (world.Tree), drawn as foliage.
//
// A biome grows its plants out of terrain paint (foliage_units.go); a tree
// an author places is a level object of its own, with a position, a turn and
// a size. It is drawn exactly as a grown one is — the same templates, the
// same Nanite foliage, wind bones, shadows and GI — by giving the placed
// plants units of their own and putting them on the same draw list. Every
// draw path reads foliage.draws (a model matrix and a unit), so nothing
// downstream knows a unit came from a hand rather than from paint.
//
// The units are cells of a world-space grid, foliagePlacedCell across, each
// holding the placed plants whose foot is in it. There are far fewer placed
// plants than grown ones, so there is no cluster tree and no thinning cut: a
// cell is built whole, and only again when a placement in it changes —
// world.TreeBatches' fingerprint, which moving a tree in the editor changes.

import (
	"math"
	"sort"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// foliagePlacedCell is the side of a placed plants' cell, in units: about a
// section's worth of grown foliage, so a cell's nearest point stands for its
// plants when the CPU path picks a mesh for it.
const foliagePlacedCell = 48

// foliagePlaced is the placed plants' units.
type foliagePlaced struct {
	cells map[[2]int32]*foliageUnit
	// fp is the placements' fingerprint the cells were built from.
	fp    uint64
	model mgl32.Mat4
}

// sync rebuilds the cells when the level's placements changed, and puts the
// ones in range on this frame's draw list.
func (p *foliagePlaced) sync(lvl *world.Level, frame uint64, reach float32) {
	batches, fp := world.TreeBatches(lvl)
	if fp != p.fp {
		p.rebuild(batches)
		p.fp = fp
	}
	p.draw(frame, reach)
}

// draw puts the cells within reach of the camera on this frame's draw list,
// placed by p.model (the identity for trees; a frame's matrix for a scatter
// volume riding one).
func (p *foliagePlaced) draw(frame uint64, reach float32) {
	if p.model == (mgl32.Mat4{}) {
		p.model = mgl32.Ident4()
	}
	cam := gfx.Cam.Position
	keys := make([][2]int32, 0, len(p.cells))
	for k := range p.cells {
		keys = append(keys, k)
	}
	// A stable order, so a frame's draw list (and a headless picture) does
	// not depend on map order.
	sort.Slice(keys, func(a, b int) bool {
		if keys[a][0] != keys[b][0] {
			return keys[a][0] < keys[b][0]
		}
		return keys[a][1] < keys[b][1]
	})
	for _, k := range keys {
		u := p.cells[k]
		u.used = frame
		if u.count == 0 {
			continue
		}
		lo, hi := u.box()
		lo, hi = transformBox(p.model, lo, hi)
		d := boxDistance(cam, lo, hi)
		if d > reach {
			continue
		}
		var dmax float32
		for i := 0; i < 3; i++ {
			e := max(math.Abs(float64(cam[i]-lo[i])), math.Abs(float64(hi[i]-cam[i])))
			dmax += float32(e * e)
		}
		foliage.draws = append(foliage.draws, foliageDraw{model: &p.model, u: u, wlo: lo, whi: hi, d: d,
			dmax: float32(math.Sqrt(float64(dmax)))})
	}
}

// rebuild re-uploads every cell from batches.
func (p *foliagePlaced) rebuild(batches []world.FoliageBatch) {
	if p.cells == nil {
		p.cells = map[[2]int32]*foliageUnit{}
	}
	type key = [2]int32
	byCell := map[key]map[[2]int]*world.FoliageBatch{}
	for _, b := range batches {
		for _, in := range b.Instances {
			k := key{int32(math.Floor(float64(in.Pos[0] / foliagePlacedCell))), int32(math.Floor(float64(in.Pos[2] / foliagePlacedCell)))}
			cell := byCell[k]
			if cell == nil {
				cell = map[[2]int]*world.FoliageBatch{}
				byCell[k] = cell
			}
			bk := [2]int{int(b.Shape), b.Variant}
			cb := cell[bk]
			if cb == nil {
				cb = &world.FoliageBatch{Shape: b.Shape, Variant: b.Variant}
				cell[bk] = cb
			}
			cb.Instances = append(cb.Instances, in)
		}
	}
	for k, u := range p.cells {
		if _, ok := byCell[k]; !ok {
			u.release()
			delete(p.cells, k)
		}
	}
	for k, cell := range byCell {
		bks := make([][2]int, 0, len(cell))
		for bk := range cell {
			bks = append(bks, bk)
		}
		sort.Slice(bks, func(a, b int) bool {
			if bks[a][0] != bks[b][0] {
				return bks[a][0] < bks[b][0]
			}
			return bks[a][1] < bks[b][1]
		})
		var out []world.FoliageBatch
		count := 0
		inf := float32(math.Inf(1))
		lo, hi := mgl32.Vec3{inf, inf, inf}, mgl32.Vec3{-inf, -inf, -inf}
		for _, bk := range bks {
			b := cell[bk]
			ext := world.FoliageExtent[b.Shape]
			for _, in := range b.Instances {
				// Bounded by the plant's own extent at its size, as
				// placement bounds a section.
				r := max(ext[0], ext[1]) * in.Scale
				lo, hi = boxUnion(lo, hi, in.Pos.Sub(mgl32.Vec3{r, r, r}), in.Pos.Add(mgl32.Vec3{r, r, r}))
			}
			count += len(b.Instances)
			out = append(out, *b)
		}
		u := p.cells[k]
		if u == nil {
			u = &foliageUnit{}
			p.cells[k] = u
		}
		foliage.upload(u, out, count, lo, hi)
	}
}

// release gives every cell's instances back.
func (p *foliagePlaced) release() {
	for k, u := range p.cells {
		u.release()
		delete(p.cells, k)
	}
	p.fp = 0
}
