package render

// vsm_pages_test.go — the virtual shadow map's page bookkeeping (vsm_pages.go),
// without GL.

import (
	"testing"
)

func TestVSMLevelsCoverTheirWindow(t *testing.T) {
	// Level 0 is 2 m across, each next one twice that; a window is the
	// level's width; the last level reaches past the shadow distance.
	if got := vsmPageWidth(0) * vsmWindowPages; got != vsmLevel0Width {
		t.Fatalf("level 0's window is %v m, want %v", got, vsmLevel0Width)
	}
	for l := 1; l < vsmLevels; l++ {
		if vsmTexel(l) != 2*vsmTexel(l-1) {
			t.Fatalf("level %d's texel is not twice level %d's", l, l-1)
		}
	}
	if half := vsmPageWidth(vsmLevels-1) * vsmWindowPages / 2; half < ShadowMaxDistance*2 {
		t.Fatalf("the last level reaches %v m round the camera, under twice the shadow distance", half)
	}
}

func TestVSMWindowsFollowTheCamera(t *testing.T) {
	p := newVSMPager(16)
	p.setWindows(10.3, -4.1)
	for l := 0; l < vsmLevels; l++ {
		w := vsmPageWidth(l)
		cx, cy := int32(10.3/w), int32(-4.1/w)
		if -4.1/w < 0 {
			cy = int32(-4.1/w) - 1
		}
		o := p.origin[l]
		if cx-o[0] != vsmWindowPages/2 || cy-o[1] != vsmWindowPages/2 {
			t.Fatalf("level %d: camera page (%d, %d) is not the window's centre (origin %v)", l, cx, cy, o)
		}
		if !p.inWindow(vsmKey{int8(l), o[0], o[1]}) || p.inWindow(vsmKey{int8(l), o[0] + vsmWindowPages, o[1]}) {
			t.Fatalf("level %d: window bounds wrong", l)
		}
	}
}

func TestVSMRequestsAllocateDrawAndCache(t *testing.T) {
	p := newVSMPager(8)
	p.setWindows(0, 0)
	keys := []vsmKey{{3, 1, 1}, {3, 2, 1}, {5, 0, 0}, {3, 1, 1}}
	draw, movers := p.request(keys)
	if len(draw) != 3 || len(movers) != 0 || p.resident() != 3 {
		t.Fatalf("first request: %d to draw, %d resident; want 3, 3", len(draw), p.resident())
	}
	// Coarse first.
	if draw[0].key.level != 5 {
		t.Fatalf("drawn first: level %d, want the coarsest", draw[0].key.level)
	}
	p.markDrawn(draw)
	if again, m := p.request(keys); len(again)+len(m) != 0 {
		t.Fatalf("a still scene drew %d pages again", len(again)+len(m))
	}
	// The table holds them, at their window places.
	tab := p.table(nil)
	n := 0
	for _, v := range tab {
		if v != 0 {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("table holds %d pages, want 3", n)
	}
	o := p.origin[3]
	if tab[(3*vsmWindowPages+int(1-o[1]))*vsmWindowPages+int(1-o[0])] == 0 {
		t.Fatalf("page (3, 1, 1) not at its place in the table")
	}
	// Something changed over one of them: it is drawn again, and so is the
	// coarse page that covers the same ground — the level-3 page beside it
	// is not.
	w := vsmPageWidth(3)
	p.invalidateRect([2]float64{2.2 * w, 1.2 * w}, [2]float64{2.4 * w, 1.4 * w}, true)
	again, _ := p.request(keys)
	if len(again) != 2 || again[0].key != (vsmKey{5, 0, 0}) || again[1].key != (vsmKey{3, 2, 1}) {
		t.Fatalf("after a change over (3, 2, 1): drew %d pages", len(again))
	}
	// An out-of-date page keeps its drawing in the table until it is drawn
	// again (the budget may leave it a frame); only invalidateAll takes it
	// out, since then what it holds means nothing.
	tab = p.table(tab)
	if tab[(3*vsmWindowPages+int(1-o[1]))*vsmWindowPages+int(2-o[0])] == 0 {
		t.Fatalf("an out-of-date page left the table before it was drawn again")
	}
	p.invalidateAll()
	for i, v := range p.table(tab) {
		if v != 0 {
			t.Fatalf("after invalidateAll, table entry %d still holds a page", i)
		}
	}
}

func TestVSMEvictsTheLeastRecentlyRequested(t *testing.T) {
	p := newVSMPager(3)
	p.setWindows(0, 0)
	draw, _ := p.request([]vsmKey{{0, 0, 0}, {0, 1, 0}, {0, 2, 0}})
	p.markDrawn(draw)
	p.request([]vsmKey{{0, 1, 0}, {0, 2, 0}}) // (0,0,0) not asked for
	draw, _ = p.request([]vsmKey{{0, 1, 0}, {0, 2, 0}, {0, 3, 0}})
	if len(draw) != 1 || draw[0].key != (vsmKey{0, 3, 0}) {
		t.Fatalf("drew %v, want only the new page", draw)
	}
	p.markDrawn(draw)
	if p.pages[vsmKey{0, 0, 0}] != nil {
		t.Fatalf("the least recently requested page was kept")
	}
	// A pool full of this frame's pages starves the rest rather than evict
	// one of them.
	draw, _ = p.request([]vsmKey{{0, 1, 0}, {0, 2, 0}, {0, 3, 0}, {0, 4, 0}})
	if p.starved != 1 || p.pages[vsmKey{0, 4, 0}] != nil || len(draw) != 0 {
		t.Fatalf("starved %d, drew %d", p.starved, len(draw))
	}
}

func TestVSMIgnoresRequestsOutsideTheWindow(t *testing.T) {
	p := newVSMPager(4)
	p.setWindows(0, 0)
	if draw, _ := p.request([]vsmKey{{0, 100, 0}}); len(draw) != 0 || p.resident() != 0 {
		t.Fatalf("a page outside the window was allocated")
	}
}

func TestVSMInvalidateAllRedrawsWhatIsAskedFor(t *testing.T) {
	p := newVSMPager(4)
	p.setWindows(0, 0)
	keys := []vsmKey{{1, 0, 0}, {2, 0, 0}}
	draw, _ := p.request(keys)
	p.markDrawn(draw)
	p.invalidateAll()
	if draw, _ := p.request(keys); len(draw) != 2 {
		t.Fatalf("after invalidating all: drew %d, want 2", len(draw))
	}
}

func TestVSMInvalidateABigBoxAtAFineLevel(t *testing.T) {
	p := newVSMPager(16)
	p.setWindows(0, 0)
	keys := []vsmKey{{0, 0, 0}, {0, 5, 5}, {8, 0, 0}}
	draw, _ := p.request(keys)
	p.markDrawn(draw)
	// A 100 m box: millions of level-0 grid cells, three pages resident.
	p.invalidateRect([2]float64{-50, -50}, [2]float64{50, 50}, true)
	if draw, _ := p.request(keys); len(draw) != 3 {
		t.Fatalf("drew %d after a box over all of them, want 3", len(draw))
	}
}

func TestVSMRectsCoverEveryPageWithinTheScratch(t *testing.T) {
	p := newVSMPager(1024)
	p.setWindows(0, 0)
	var keys []vsmKey
	for l := 0; l < 3; l++ {
		o := p.origin[l]
		for y := int32(0); y < vsmWindowPages; y += 3 {
			for x := int32(0); x < vsmWindowPages; x += 2 {
				keys = append(keys, vsmKey{int8(l), o[0] + x, o[1] + y})
			}
		}
	}
	draw, _ := p.request(keys)
	rects := p.rects(draw)
	seen := map[vsmKey]bool{}
	for _, r := range rects {
		if r.x1-r.x0 > vsmRectPages || r.y1-r.y0 > vsmRectPages {
			t.Fatalf("rect %dx%d pages, over the scratch's %d", r.x1-r.x0, r.y1-r.y0, vsmRectPages)
		}
		for _, pg := range r.pages {
			if int(pg.key.level) != r.level || pg.key.x < r.x0 || pg.key.x >= r.x1 || pg.key.y < r.y0 || pg.key.y >= r.y1 {
				t.Fatalf("page %v outside its rect", pg.key)
			}
			seen[pg.key] = true
		}
	}
	if len(seen) != len(draw) {
		t.Fatalf("rects hold %d pages of %d", len(seen), len(draw))
	}
	if len(rects) != 3*4 {
		t.Fatalf("%d rects for three full windows, want 12", len(rects))
	}
}

// Something moving over valid pages redraws its movers only: the static
// layer stays valid, and only the pages under it are listed.
func TestVSMMoversRedrawOnlyTheirPages(t *testing.T) {
	p := newVSMPager(16)
	p.setWindows(0, 0)
	keys := []vsmKey{{2, 0, 0}, {2, 3, 0}}
	draw, _ := p.request(keys)
	p.markDrawn(draw)
	w := vsmPageWidth(2)
	p.invalidateRect([2]float64{0.1 * w, 0.1 * w}, [2]float64{0.2 * w, 0.2 * w}, false)
	whole, movers := p.request(keys)
	if len(whole) != 0 || len(movers) != 1 || movers[0].key != (vsmKey{2, 0, 0}) {
		t.Fatalf("a mover over (2, 0, 0): %d whole, %d movers", len(whole), len(movers))
	}
	if !movers[0].static || movers[0].valid {
		t.Fatalf("its static layer should stand and its depth be stale")
	}
	// Still in the table, a frame behind, until its movers are drawn again.
	o := p.origin[2]
	if p.table(nil)[(2*vsmWindowPages+int(-o[1]))*vsmWindowPages+int(-o[0])] == 0 {
		t.Fatalf("a page with stale movers left the table")
	}
	p.markDrawn(movers)
	if whole, movers := p.request(keys); len(whole)+len(movers) != 0 {
		t.Fatalf("drawn again after its movers were")
	}
}

// A page the window leaves keeps its physical page but cannot be told of
// changes while it is out, so it is drawn again when the window comes back.
func TestVSMPagesLeftByTheWindowAreRedrawnOnReturn(t *testing.T) {
	p := newVSMPager(16)
	p.setWindows(0, 0)
	keys := []vsmKey{{0, 10, 10}, {5, 0, 0}}
	draw, _ := p.request(keys)
	p.markDrawn(draw)
	// Level 0's window is 2 m across: a 3 m walk takes it away from
	// (0, 10, 10); level 5's (64 m) still holds (5, 0, 0).
	p.setWindows(3, 0)
	if p.inWindow(vsmKey{0, 10, 10}) || !p.inWindow(vsmKey{5, 0, 0}) {
		t.Fatalf("windows: level 0 should have left the page, level 5 not")
	}
	if p.pages[vsmKey{0, 10, 10}] == nil {
		t.Fatalf("the page lost its physical page on leaving the window")
	}
	p.setWindows(0, 0)
	whole, movers := p.request(keys)
	if len(whole) != 1 || whole[0].key != (vsmKey{0, 10, 10}) || len(movers) != 0 {
		t.Fatalf("back in the window: %d whole (want the level-0 page), %d movers", len(whole), len(movers))
	}
}

// budgetKeys is a page in each of n (at most 4) vsmRectPages blocks of level
// l's window, so each is a rectangle of its own.
func budgetKeys(p *vsmPager, l int8, n int) []vsmKey {
	if n > 4 {
		panic("a window holds 2x2 rectangle blocks")
	}
	o := p.origin[l]
	var keys []vsmKey
	for i := 0; i < n; i++ {
		keys = append(keys, vsmKey{l, o[0] + int32(i%2)*vsmRectPages, o[1] + int32(i/2)*vsmRectPages})
	}
	return keys
}

// Over budget, a frame draws the holes coarse first and leaves the rest,
// which are asked for again and drawn in the frames after — and until then
// a page never drawn is absent from the table.
func TestVSMBudgetDrawsHolesCoarseFirstAndDefersTheRest(t *testing.T) {
	p := newVSMPager(64)
	p.setWindows(0, 0)
	keys := append(budgetKeys(p, 1, 4), budgetKeys(p, 6, 2)...)
	whole, movers := p.request(keys)
	whole, movers = p.budget(whole, movers, 3)
	if len(whole) != 3 || len(movers) != 0 || p.deferred != 3 {
		t.Fatalf("budget 3 of 6 one-page rectangles: kept %d whole, %d movers, deferred %d", len(whole), len(movers), p.deferred)
	}
	if whole[0].key.level != 6 || whole[1].key.level != 6 {
		t.Fatalf("the coarse holes were not served first: levels %d, %d", whole[0].key.level, whole[1].key.level)
	}
	p.markDrawn(whole)
	drawn := 3
	for frame := 0; frame < 3 && drawn < 6; frame++ {
		w, m := p.request(keys)
		w, m = p.budget(w, m, 3)
		drawn += len(w) + len(m)
		p.markDrawn(w)
		p.markDrawn(m)
	}
	if drawn != 6 {
		t.Fatalf("after the frames that followed, %d of 6 pages drawn", drawn)
	}
	if w, m := p.request(keys); len(w)+len(m) != 0 {
		t.Fatalf("a still scene still has %d pages to draw", len(w)+len(m))
	}
}

// Walking makes new holes every frame; the out-of-date pages (a tree's
// shadow swaying) still get a share of the budget, oldest first.
func TestVSMBudgetKeepsAShareForOutOfDatePages(t *testing.T) {
	p := newVSMPager(64)
	p.setWindows(0, 0)
	old := budgetKeys(p, 2, 4)
	w, _ := p.request(old)
	p.markDrawn(w)
	p.invalidateRect([2]float64{-1e6, -1e6}, [2]float64{1e6, 1e6}, false) // everything sways
	holes := append(budgetKeys(p, 0, 4), budgetKeys(p, 1, 4)...)
	whole, movers := p.request(append(holes, old...))
	whole, movers = p.budget(whole, movers, 6)
	if len(movers) != 2 || len(whole) != 4 {
		t.Fatalf("budget 6 over 8 holes and 4 out-of-date: kept %d holes, %d out-of-date; want 4 and 2", len(whole), len(movers))
	}
}

// No limit, nothing deferred.
func TestVSMBudgetZeroIsNoLimit(t *testing.T) {
	p := newVSMPager(64)
	p.setWindows(0, 0)
	whole, movers := p.request(append(budgetKeys(p, 1, 4), budgetKeys(p, 3, 2)...))
	whole, _ = p.budget(whole, movers, 0)
	if len(whole) != 6 || p.deferred != 0 {
		t.Fatalf("budget 0: kept %d of 6, deferred %d", len(whole), p.deferred)
	}
}
