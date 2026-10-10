package render

// vsm_pages.go — the virtual shadow map's page bookkeeping, on the CPU and
// without GL. Phase 5 of the Nanite plan (docs/nanite/PHASE_5.md),
// component 10; the GL half is vsm.go.
//
// # The virtual map
//
// The sun's shadow is a CLIPMAP of vsmLevels square levels in light space,
// each twice the width of the one before — level 0 is vsmLevel0Width across,
// the last over two kilometres — and each vsmPageTexels·vsmWindowPages texels
// across: 4096² per level, eleven levels, where the three cascades it
// replaces were 2048² each. A receiver reads the level whose texel is about
// the size of its own pixel (vsm.glsl, vsmLevelFor), so a shadow edge is as
// sharp at 300 m as at 3.
//
// Nobody could afford all of it, and nobody needs to: a level is cut into
// PAGES of vsmPageTexels² texels, and only the pages some pixel on screen
// reads are given memory and drawn. A page is named by its level and its
// place on that level's page grid, which is fixed in light space — the grid
// does not move with the camera, so a page drawn once holds good for as long
// as nothing in it changes. Each level's WINDOW, vsmWindowPages² pages round
// the camera, is what the page table can address this frame; the window
// slides a page at a time as the camera moves, and a page that leaves it
// keeps its memory (and its contents) until the pool needs them.
//
// # The pool
//
// Physical pages are layers of two depth arrays (vsm.go): each page's STATIC
// layer — the world that does not move: compiled surfaces, chunks, terrain,
// props — and its whole depth, the static layer with everything that does
// (entities, sprites, scatter, foliage) drawn over it, which is what the
// shaders read. The pager hands physical pages out to requested pages that
// have none, taking a free one if there is one and otherwise the one least
// recently requested (never one requested this frame).
//
// A page's static layer is valid once drawn and until something static that
// casts into it changes; its whole depth likewise, and also until something
// moving in it does. Only requested pages are drawn: a static layer that is
// not valid is drawn whole (and the movers over it); a whole depth that is
// not valid over a valid static layer is the static layer copied and only the
// movers drawn. A still scene draws nothing, and a character walking through
// a compiled level redraws only itself, on the few pages under it.
//
// # The budget
//
// A frame draws at most a budget of rectangles (budget, vsmRectBudget): on a
// GTX 1070 a rectangle costs ~0.6 ms before its first page, and walking a
// street turning a corner asked for 340 new pages in 13 rectangles at once —
// 13.5 ms in one frame. What the budget leaves is drawn in the frames after,
// and is never a hole meanwhile: a page that has been drawn (drawn) stays in
// the table, out of date, until it is drawn again — a swaying tree's shadow
// a frame behind — and one never drawn is absent, so a receiver reads the
// coarser page under it, which the budget serves first. Only invalidateAll
// (a turned light, a new level, the depth window moving) takes pages out of
// the table, since what they hold no longer means anything.
//
// What the pager does not know is what to request and what changed: vsm.go
// reads the requests back from the GPU (the receivers' marks) and adds a
// coarse level over the whole view itself, and its tracker turns everything
// that moved into light-space rectangles.

import (
	"math"
	"sort"
)

const (
	// vsmLevels is the clipmap's level count; vsmWindowPages the pages
	// across a level's window, vsmPageTexels the texels across a page.
	// vsm.glsl mirrors all three (VSM_LEVELS, VSM_PAGES, VSM_PAGE_TEXELS).
	vsmLevels      = 11
	vsmWindowPages = 32
	vsmPageTexels  = 128
	// vsmLevel0Width is level 0's width in world units: 2 m, a texel of
	// half a millimetre.
	vsmLevel0Width = 2.0
	// vsmRectPages is the most pages across a rectangle drawn at once: the
	// scratch target's size, vsmRectPages·vsmPageTexels texels square.
	vsmRectPages = 16
)

// vsmLevelTexels is a level's width in texels.
const vsmLevelTexels = vsmWindowPages * vsmPageTexels

// vsmTexel is a texel's width at level l, in world units.
func vsmTexel(l int) float64 { return vsmLevel0Width / vsmLevelTexels * math.Ldexp(1, l) }

// vsmPageWidth is a page's width at level l, in world units.
func vsmPageWidth(l int) float64 { return vsmTexel(l) * vsmPageTexels }

// vsmKey names a page: its level and its place on the level's grid, in pages
// from light space's origin.
type vsmKey struct {
	level int8
	x, y  int32
}

// vsmPage is a page that has a physical page.
type vsmPage struct {
	key   vsmKey
	layer int32
	// static: its static layer is drawn, and nothing static that casts into
	// it has changed since. valid: the same of its whole depth.
	static, valid bool
	// drawn: its depth holds a drawing of this page, up to date or not —
	// what the table shows (see "The budget"). staleSince: the frame it went
	// out of date in (0: it is not), so the budget serves the oldest first.
	drawn      bool
	staleSince uint64
	// requested: the frame it was last asked for.
	requested uint64
}

// vsmRect is a run of pages of one level to draw at once: [x0, x1) × [y0, y1)
// on the level's grid.
type vsmRect struct {
	level          int
	x0, y0, x1, y1 int32
	pages          []*vsmPage
}

// vsmPager is the bookkeeping; see the file comment.
type vsmPager struct {
	pool    int
	pages   map[vsmKey]*vsmPage
	byLayer []*vsmPage
	free    []int32
	frame   uint64
	// origin is each level's window: its first page on the level's grid.
	origin [vsmLevels][2]int32
	// slots is each level's window, row by row: the resident page at each
	// place, or nil — what invalidateRect and table look pages up in. A
	// page that leaves its window keeps its physical page (until evicted)
	// but is marked stale, since nothing can tell it of changes while it
	// is out: it is drawn again if the window comes back over it.
	slots [vsmLevels][vsmWindowPages * vsmWindowPages]*vsmPage
	// Counts, for the overlay: pages drawn, pages a request found no
	// physical page for, and pages the budget left for a later frame, this
	// frame.
	drawn, starved, deferred int
}

func newVSMPager(pool int) *vsmPager {
	p := &vsmPager{pool: pool, pages: map[vsmKey]*vsmPage{}, byLayer: make([]*vsmPage, pool)}
	for l := pool - 1; l >= 0; l-- {
		p.free = append(p.free, int32(l))
	}
	return p
}

// setWindows centres each level's window on the camera, at cam in light space
// (x and y across the light).
func (p *vsmPager) setWindows(camX, camY float64) {
	for l := 0; l < vsmLevels; l++ {
		w := vsmPageWidth(l)
		o := [2]int32{int32(math.Floor(camX/w)) - vsmWindowPages/2, int32(math.Floor(camY/w)) - vsmWindowPages/2}
		if o == p.origin[l] {
			continue
		}
		p.origin[l] = o
		// The window moved: its places are filled again from the level's
		// resident pages, and the ones it left are stale.
		for i, pg := range p.slots[l] {
			if pg != nil && !p.inWindow(pg.key) {
				p.stale(pg, true)
			}
			p.slots[l][i] = nil
		}
		for k, pg := range p.pages {
			if int(k.level) == l && p.inWindow(k) {
				p.slots[l][p.slotOf(k)] = pg
			}
		}
	}
}

// slotOf is a page's place in its level's window (it must be in it).
func (p *vsmPager) slotOf(k vsmKey) int {
	o := p.origin[k.level]
	return int(k.y-o[1])*vsmWindowPages + int(k.x-o[0])
}

// inWindow reports whether k is addressable this frame.
func (p *vsmPager) inWindow(k vsmKey) bool {
	o := p.origin[k.level]
	return k.x >= o[0] && k.x < o[0]+vsmWindowPages && k.y >= o[1] && k.y < o[1]+vsmWindowPages
}

// invalidateAll marks every page's contents stale (the light turned, the
// world was reloaded): requested ones are drawn again, the rest when next
// asked for.
func (p *vsmPager) invalidateAll() {
	for _, pg := range p.pages {
		pg.static, pg.valid, pg.drawn, pg.staleSince = false, false, false, 0
	}
}

// stale marks pg out of date — its static layer too when static — keeping
// what it holds in the table until it is drawn again.
func (p *vsmPager) stale(pg *vsmPage, static bool) {
	if pg.valid && pg.staleSince == 0 {
		pg.staleSince = p.frame + 1
	}
	pg.valid = false
	if static {
		pg.static = false
	}
}

// invalidateRect marks stale every page that overlaps the light-space
// rectangle [lo, hi] (x and y), on every level: something that casts into
// them changed — something static (static: the pages' static layers too),
// or something moving (their whole depth only).
func (p *vsmPager) invalidateRect(lo, hi [2]float64, static bool) {
	// Only the windows: a page outside one is stale already (setWindows).
	for l := 0; l < vsmLevels; l++ {
		w := vsmPageWidth(l)
		o := p.origin[l]
		x0 := max(int64(math.Floor(lo[0]/w)), int64(o[0]))
		y0 := max(int64(math.Floor(lo[1]/w)), int64(o[1]))
		x1 := min(int64(math.Floor(hi[0]/w)), int64(o[0])+vsmWindowPages-1)
		y1 := min(int64(math.Floor(hi[1]/w)), int64(o[1])+vsmWindowPages-1)
		for y := y0; y <= y1; y++ {
			row := (y - int64(o[1])) * vsmWindowPages
			for x := x0; x <= x1; x++ {
				if pg := p.slots[l][row+x-int64(o[0])]; pg != nil {
					p.stale(pg, static)
				}
			}
		}
	}
}

// request takes this frame's requested pages (in the windows; others are
// ignored) and returns the ones to draw, given physical pages: whole, the
// requested pages whose static layer is not valid; movers only, the rest of
// the requested pages that are not valid. Coarse levels are served first,
// so that when the pool runs short it is fine detail that goes without —
// the coarser page under it is what a receiver falls back to.
func (p *vsmPager) request(keys []vsmKey) (whole, movers []*vsmPage) {
	p.frame++
	p.starved = 0
	sort.Slice(keys, func(a, b int) bool {
		ka, kb := keys[a], keys[b]
		if ka.level != kb.level {
			return ka.level > kb.level
		}
		if ka.y != kb.y {
			return ka.y < kb.y
		}
		return ka.x < kb.x
	})
	var evict []*vsmPage // the eviction order, built once if needed
	ei := 0
	for i, k := range keys {
		if i > 0 && k == keys[i-1] || !p.inWindow(k) {
			continue
		}
		pg := p.pages[k]
		if pg == nil {
			layer := int32(-1)
			if n := len(p.free); n > 0 {
				layer = p.free[n-1]
				p.free = p.free[:n-1]
			} else {
				if evict == nil {
					evict = p.evictionOrder()
				}
				for ei < len(evict) && evict[ei].requested == p.frame {
					ei++
				}
				if ei < len(evict) {
					old := evict[ei]
					ei++
					delete(p.pages, old.key)
					if p.inWindow(old.key) {
						p.slots[old.key.level][p.slotOf(old.key)] = nil
					}
					layer = old.layer
				}
			}
			if layer < 0 {
				p.starved++
				continue
			}
			pg = &vsmPage{key: k, layer: layer}
			p.pages[k] = pg
			p.byLayer[layer] = pg
			p.slots[k.level][p.slotOf(k)] = pg
		}
		pg.requested = p.frame
		switch {
		case !pg.static:
			whole = append(whole, pg)
		case !pg.valid:
			movers = append(movers, pg)
		}
	}
	return whole, movers
}

// evictionOrder is every resident page, least recently requested first
// (ties: finest level first, then grid order, so the order is the same every
// run).
func (p *vsmPager) evictionOrder() []*vsmPage {
	out := make([]*vsmPage, 0, len(p.pages))
	for _, pg := range p.pages {
		out = append(out, pg)
	}
	sort.Slice(out, func(a, b int) bool {
		pa, pb := out[a], out[b]
		if pa.requested != pb.requested {
			return pa.requested < pb.requested
		}
		if pa.key.level != pb.key.level {
			return pa.key.level < pb.key.level
		}
		if pa.key.y != pb.key.y {
			return pa.key.y < pb.key.y
		}
		return pa.key.x < pb.key.x
	})
	return out
}

// markDrawn records pages as drawn, whole (static layer and all) or their
// movers over a static layer that was valid.
func (p *vsmPager) markDrawn(pages []*vsmPage) {
	for _, pg := range pages {
		pg.static, pg.valid, pg.drawn, pg.staleSince = true, true, true, 0
	}
	p.drawn += len(pages)
}

// table fills out with the page table: for each level, row and column of the
// windows, the physical layer + 1 of a drawn page there (up to date or one
// the budget has not reached yet), 0 for none.
func (p *vsmPager) table(out []uint32) []uint32 {
	n := vsmLevels * vsmWindowPages * vsmWindowPages
	if cap(out) < n {
		out = make([]uint32, n)
	}
	out = out[:n]
	for l := range p.slots {
		for i, pg := range p.slots[l] {
			v := uint32(0)
			if pg != nil && pg.drawn {
				v = uint32(pg.layer) + 1
			}
			out[l*vsmWindowPages*vsmWindowPages+i] = v
		}
	}
	return out
}

// vsmRects groups pages to draw into rectangles of one level, at most
// vsmRectPages across: each level's window is cut into vsmRectPages blocks,
// and each block's pages make one rectangle, their bounding box. A
// rectangle may cover pages not being drawn; they are drawn into the scratch
// and not copied.
func (p *vsmPager) rects(pages []*vsmPage) []vsmRect {
	type block struct {
		level  int
		bx, by int32
	}
	idx := map[block]int{}
	var out []vsmRect
	for _, pg := range pages {
		o := p.origin[pg.key.level]
		b := block{int(pg.key.level), (pg.key.x - o[0]) / vsmRectPages, (pg.key.y - o[1]) / vsmRectPages}
		i, ok := idx[b]
		if !ok {
			i = len(out)
			idx[b] = i
			out = append(out, vsmRect{level: b.level, x0: pg.key.x, y0: pg.key.y, x1: pg.key.x + 1, y1: pg.key.y + 1})
		}
		r := &out[i]
		r.x0, r.y0 = min(r.x0, pg.key.x), min(r.y0, pg.key.y)
		r.x1, r.y1 = max(r.x1, pg.key.x+1), max(r.y1, pg.key.y+1)
		r.pages = append(r.pages, pg)
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].level > out[b].level })
	return out
}

// budget keeps of a frame's pages to draw (request's whole and movers) the
// ones in its first max rectangles (rects' blocks; 0: no limit), and counts
// the rest in deferred: they are asked for again next frame. Holes first —
// rectangles with a page never drawn, coarse to fine, so a receiver always
// finds the coarser page it falls back to — then the out-of-date ones, the
// longest out of date first, with a share of the budget kept for them so
// none waits for ever.
func (p *vsmPager) budget(whole, movers []*vsmPage, max int) (keepWhole, keepMovers []*vsmPage) {
	p.deferred = 0
	if max <= 0 {
		return whole, movers
	}
	type cand struct {
		r     vsmRect
		whole bool
		hole  bool
		since uint64
	}
	var cs []cand
	add := func(pages []*vsmPage, w bool) {
		for _, r := range p.rects(pages) {
			c := cand{r: r, whole: w, since: math.MaxUint64}
			for _, pg := range r.pages {
				if !pg.drawn {
					c.hole = true
				}
				if pg.staleSince < c.since {
					c.since = pg.staleSince
				}
			}
			cs = append(cs, c)
		}
	}
	add(whole, true)
	add(movers, false)
	if len(cs) <= max {
		return whole, movers
	}
	sort.SliceStable(cs, func(a, b int) bool {
		ca, cb := &cs[a], &cs[b]
		if ca.hole != cb.hole {
			return ca.hole
		}
		if !ca.hole && ca.since != cb.since {
			return ca.since < cb.since
		}
		return ca.r.level > cb.r.level
	})
	// A third of the budget (at least one) is kept for the out-of-date
	// while any wait: walking makes new holes every frame, and they alone
	// would leave a swaying tree's shadow standing still.
	holes := 0
	for _, c := range cs {
		if c.hole {
			holes++
		}
	}
	take := make([]bool, len(cs))
	if stale := len(cs) - holes; stale > 0 && holes > max {
		reserve := min(stale, (max+2)/3)
		for i := 0; i < max-reserve; i++ {
			take[i] = true
		}
		for i := holes; i < holes+reserve; i++ {
			take[i] = true
		}
	} else {
		for i := 0; i < max; i++ {
			take[i] = true
		}
	}
	for i, c := range cs {
		if !take[i] {
			p.deferred += len(c.r.pages)
			continue
		}
		if c.whole {
			keepWhole = append(keepWhole, c.r.pages...)
		} else {
			keepMovers = append(keepMovers, c.r.pages...)
		}
	}
	return keepWhole, keepMovers
}

// resident is how many pages hold a physical page.
func (p *vsmPager) resident() int { return len(p.pages) }
