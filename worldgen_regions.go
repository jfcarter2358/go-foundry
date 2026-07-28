package main

// worldgen_regions.go — the off-grid city regions (rotated buildings and curved
// roads) that get carved into the terrain grid by world_subdivide.go.
//
// Regions are defined purely in world XZ, independent of cells. A building is a
// rotated rectangle with an inner rectangle inset by the wall thickness (the ring
// between them is the thick wall band). A road is a smoothed centerline expanded
// to a ribbon (a union of capsule segments). The carver tags every carved
// sub-polygon by testing its centroid against these regions.

import (
	"math"
	"math/rand"

	"github.com/go-gl/mathgl/mgl32"
)

// regionClass is the kind of terrain a carved sub-polygon belongs to.
type regionClass int

const (
	regGround   regionClass = iota // walkable terrain or road surface (flush)
	regBand                        // building wall band (thick wall volume)
	regInterior                    // building interior floor (the room)
)

const cityWallThick = float32(0.5) // world-unit thickness of building walls
const cityParapet = float32(0.7)   // height the wall band rises above the roof

// ---------- buildings ----------

// doorRegion is the oriented opening on a building's road-facing side. Wall faces
// whose midpoint falls inside it get a door hole; backup colliders go open there.
type doorRegion struct {
	valid     bool
	center    mgl32.Vec2 // mid-band point of the doorway
	tangent   mgl32.Vec2 // unit, along the wall
	halfLen   float32    // half the doorway width
	halfThick float32    // half-depth across the wall band
}

func (d doorRegion) open(p mgl32.Vec2) bool {
	if !d.valid {
		return false
	}
	rel := p.Sub(d.center)
	along := rel.Dot(d.tangent)
	perp := rel.Dot(mgl32.Vec2{-d.tangent.Y(), d.tangent.X()})
	return absf(along) <= d.halfLen && absf(perp) <= d.halfThick
}

// building is a freely-rotated rectangular footprint carved into the grid.
type building struct {
	center mgl32.Vec2
	ux, uy mgl32.Vec2 // unit local axes (rotation)
	hwOut  float32     // outer half-width along ux
	hhOut  float32     // outer half-depth along uy
	hwIn   float32     // inner half-width
	hhIn   float32     // inner half-depth

	roofY       float32 // flat room ceiling height (world Y)
	parapet     float32 // wall rise above the roof
	doorHalfLen float32 // half the doorway width
	enterable   bool    // whether a door is cut
	floorTex    string
	wallTex     string
	roofTex     string
	door        doorRegion
}

func (b *building) local(p mgl32.Vec2) (u, v float32) {
	d := p.Sub(b.center)
	return d.Dot(b.ux), d.Dot(b.uy)
}

// uLine returns the line where the local u coordinate equals off.
func (b *building) uLine(off float32) line2 {
	return line2{b.ux.X(), b.ux.Y(), -(b.center.Dot(b.ux) + off)}
}

// vLine returns the line where the local v coordinate equals off.
func (b *building) vLine(off float32) line2 {
	return line2{b.uy.X(), b.uy.Y(), -(b.center.Dot(b.uy) + off)}
}

// cutLines are the 8 supporting lines (outer + inner rectangle edges) used to
// slice cells so no carved triangle straddles a wall boundary.
func (b *building) cutLines() []line2 {
	return []line2{
		b.uLine(b.hwOut), b.uLine(-b.hwOut), b.uLine(b.hwIn), b.uLine(-b.hwIn),
		b.vLine(b.hhOut), b.vLine(-b.hhOut), b.vLine(b.hhIn), b.vLine(-b.hhIn),
	}
}

func (b *building) corners() [4]mgl32.Vec2 {
	return [4]mgl32.Vec2{
		b.center.Add(b.ux.Mul(b.hwOut)).Add(b.uy.Mul(b.hhOut)),
		b.center.Add(b.ux.Mul(b.hwOut)).Sub(b.uy.Mul(b.hhOut)),
		b.center.Sub(b.ux.Mul(b.hwOut)).Sub(b.uy.Mul(b.hhOut)),
		b.center.Sub(b.ux.Mul(b.hwOut)).Add(b.uy.Mul(b.hhOut)),
	}
}

func (b *building) aabb() (min, max mgl32.Vec2) {
	c := b.corners()
	min, max = c[0], c[0]
	for _, p := range c[1:] {
		min = mgl32.Vec2{float32(math.Min(float64(min.X()), float64(p.X()))), float32(math.Min(float64(min.Y()), float64(p.Y())))}
		max = mgl32.Vec2{float32(math.Max(float64(max.X()), float64(p.X()))), float32(math.Max(float64(max.Y()), float64(p.Y())))}
	}
	return
}

// ---------- roads ----------

// roadSeg is one capsule segment of a road ribbon, with its own half-width so
// strokes of different widths can coexist in one ribbon.
type roadSeg struct {
	a, b  mgl32.Vec2
	halfW float32
}

type roadRibbon struct {
	segs []roadSeg
}

func (r *roadRibbon) contains(p mgl32.Vec2) bool {
	if r == nil {
		return false
	}
	for _, s := range r.segs {
		if d, _ := distToSegment(p, s.a, s.b); d <= s.halfW {
			return true
		}
	}
	return false
}

// lineThroughDir returns the line through p with direction dir.
func lineThroughDir(p, dir mgl32.Vec2) line2 {
	n := mgl32.Vec2{-dir.Y(), dir.X()}
	l := n.Len()
	if l < 1e-9 {
		return line2{1, 0, -p.X()}
	}
	n = n.Mul(1 / l)
	return line2{n.X(), n.Y(), -(n.X()*p.X() + n.Y()*p.Y())}
}

// cutLinesForCell returns the side lines of every road segment whose expanded
// bounding box overlaps the cell rectangle [min,max].
func (r *roadRibbon) cutLinesForCell(min, max mgl32.Vec2) []line2 {
	if r == nil {
		return nil
	}
	var ls []line2
	for _, s := range r.segs {
		smin := mgl32.Vec2{float32(math.Min(float64(s.a.X()), float64(s.b.X()))) - s.halfW, float32(math.Min(float64(s.a.Y()), float64(s.b.Y()))) - s.halfW}
		smax := mgl32.Vec2{float32(math.Max(float64(s.a.X()), float64(s.b.X()))) + s.halfW, float32(math.Max(float64(s.a.Y()), float64(s.b.Y()))) + s.halfW}
		if smax.X() < min.X() || smin.X() > max.X() || smax.Y() < min.Y() || smin.Y() > max.Y() {
			continue
		}
		dir := s.b.Sub(s.a)
		if dir.Len() < 1e-6 {
			continue
		}
		dir = dir.Normalize()
		nrm := mgl32.Vec2{-dir.Y(), dir.X()}
		ls = append(ls,
			lineThroughDir(s.a.Add(nrm.Mul(s.halfW)), dir),
			lineThroughDir(s.a.Sub(nrm.Mul(s.halfW)), dir),
			// End caps bound the segment so the ribbon terminates cleanly rather
			// than the side-lines slicing the whole cell.
			lineThroughDir(s.a, nrm),
			lineThroughDir(s.b, nrm),
		)
	}
	return ls
}

// eraseRoadStrokesNear deletes every road stroke that passes within `radius` of
// point p. Returns true if any stroke was removed.
func eraseRoadStrokesNear(g *WorldGrid, p mgl32.Vec2, radius float32) bool {
	if len(g.Roads) == 0 {
		return false
	}
	kept := g.Roads[:0]
	removed := false
	for _, s := range g.Roads {
		hit := false
		for i := 0; i+1 < len(s.Points); i++ {
			if d, _ := distToSegment(p, s.Points[i], s.Points[i+1]); d <= radius {
				hit = true
				break
			}
		}
		if hit {
			removed = true
			continue
		}
		kept = append(kept, s)
	}
	g.Roads = kept
	return removed
}

// roadStrokeWidth returns the effective width of a stroke (grid default when 0).
func roadStrokeWidth(g *WorldGrid, w float32) float32 {
	if w <= 0 {
		return g.CellSize * 0.6
	}
	return w
}

// buildRoadRibbon builds the road ribbon. Free-hand strokes (true diagonals) take
// priority; otherwise it falls back to the legacy per-cell TerrainRoad graph.
func buildRoadRibbon(g *WorldGrid) *roadRibbon {
	if len(g.Roads) > 0 {
		rib := &roadRibbon{}
		for _, stroke := range g.Roads {
			if len(stroke.Points) < 2 {
				continue
			}
			hw := roadStrokeWidth(g, stroke.Width) * 0.5
			sm := chaikin(stroke.Points, 2) // smooth hand jitter without squaring corners
			for i := 0; i+1 < len(sm); i++ {
				rib.segs = append(rib.segs, roadSeg{a: sm[i], b: sm[i+1], halfW: hw})
			}
		}
		if len(rib.segs) == 0 {
			return nil
		}
		return rib
	}
	return buildRoadRibbonFromCells(g)
}

// buildRoadRibbonFromCells is the legacy path: it extracts the painted per-cell
// TerrainRoad centerline graph, smooths each chain with Chaikin's algorithm, and
// expands it to a ribbon. Kept for worlds/tests that tag road cells directly.
func buildRoadRibbonFromCells(g *WorldGrid) *roadRibbon {
	isRoad := func(cx, cy int) bool {
		ci := g.cIdx(cx, cy)
		return ci >= 0 && g.CellType[ci] == TerrainRoad
	}
	dirs := [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	neighbors := func(cx, cy int) [][2]int {
		var out [][2]int
		for _, d := range dirs {
			if isRoad(cx+d[0], cy+d[1]) {
				out = append(out, [2]int{cx + d[0], cy + d[1]})
			}
		}
		return out
	}

	type ekey struct{ a, b [2]int }
	mkEdge := func(p, q [2]int) ekey {
		if p[0] < q[0] || (p[0] == q[0] && p[1] <= q[1]) {
			return ekey{p, q}
		}
		return ekey{q, p}
	}
	visited := map[ekey]bool{}
	rib := &roadRibbon{}
	hw := g.CellSize * 0.32

	addChain := func(cells [][2]int) {
		if len(cells) < 2 {
			return
		}
		pts := make([]mgl32.Vec2, len(cells))
		for i, c := range cells {
			pts[i] = g.cellCenterWorld(c[0], c[1])
		}
		sm := chaikin(pts, 2)
		for i := 0; i+1 < len(sm); i++ {
			rib.segs = append(rib.segs, roadSeg{a: sm[i], b: sm[i+1], halfW: hw})
		}
	}

	// Walk maximal chains starting at endpoints/junctions (degree != 2).
	for cy := 0; cy < g.Rows; cy++ {
		for cx := 0; cx < g.Cols; cx++ {
			if !isRoad(cx, cy) {
				continue
			}
			start := [2]int{cx, cy}
			nb := neighbors(cx, cy)
			if len(nb) == 2 {
				continue // interior of a chain — visited via an endpoint/junction
			}
			for _, n := range nb {
				if visited[mkEdge(start, n)] {
					continue
				}
				chain := [][2]int{start}
				prev := start
				cur := n
				for {
					visited[mkEdge(prev, cur)] = true
					chain = append(chain, cur)
					cnb := neighbors(cur[0], cur[1])
					if len(cnb) != 2 {
						break // reached endpoint or junction
					}
					var nxt [2]int
					found := false
					for _, c := range cnb {
						if c != prev {
							nxt = c
							found = true
							break
						}
					}
					if !found || visited[mkEdge(cur, nxt)] {
						break
					}
					prev, cur = cur, nxt
				}
				addChain(chain)
			}
		}
	}

	// Any remaining unvisited edges belong to pure loops (all degree 2).
	for cy := 0; cy < g.Rows; cy++ {
		for cx := 0; cx < g.Cols; cx++ {
			if !isRoad(cx, cy) {
				continue
			}
			start := [2]int{cx, cy}
			for _, n := range neighbors(cx, cy) {
				if visited[mkEdge(start, n)] {
					continue
				}
				chain := [][2]int{start}
				prev := start
				cur := n
				for {
					visited[mkEdge(prev, cur)] = true
					chain = append(chain, cur)
					if cur == start {
						break
					}
					cnb := neighbors(cur[0], cur[1])
					var nxt [2]int
					found := false
					for _, c := range cnb {
						if c != prev && !visited[mkEdge(cur, c)] {
							nxt = c
							found = true
							break
						}
					}
					if !found {
						break
					}
					prev, cur = cur, nxt
				}
				addChain(chain)
			}
		}
	}

	if len(rib.segs) == 0 {
		return nil
	}
	return rib
}

// chaikin rounds a polyline by `iters` Chaikin corner-cutting passes, keeping the
// endpoints fixed.
func chaikin(pts []mgl32.Vec2, iters int) []mgl32.Vec2 {
	for it := 0; it < iters; it++ {
		if len(pts) < 3 {
			break
		}
		out := make([]mgl32.Vec2, 0, len(pts)*2)
		out = append(out, pts[0])
		for i := 0; i+1 < len(pts); i++ {
			p := pts[i]
			q := pts[i+1]
			out = append(out, p.Mul(0.75).Add(q.Mul(0.25)), p.Mul(0.25).Add(q.Mul(0.75)))
		}
		out = append(out, pts[len(pts)-1])
		pts = out
	}
	return pts
}

// ---------- city carving context ----------

// cityCarve answers region queries for an arbitrary world point.
type cityCarve struct {
	grid      *WorldGrid
	buildings []*building
	road      *roadRibbon
}

// classify returns the region class of point p and the owning building (if any).
func (cc *cityCarve) classify(p mgl32.Vec2) (regionClass, *building) {
	for _, b := range cc.buildings {
		u, v := b.local(p)
		if absf(u) <= b.hwOut && absf(v) <= b.hhOut {
			if absf(u) <= b.hwIn && absf(v) <= b.hhIn {
				return regInterior, b
			}
			return regBand, b
		}
	}
	return regGround, nil
}

func (cc *cityCarve) isRoad(p mgl32.Vec2) bool {
	return cc.road.contains(p)
}

// ---------- building placement ----------

// collectCityCells returns the building-eligible city cells.
func collectCityCells(g *WorldGrid) [][2]int {
	var cityCells [][2]int
	for cy := 0; cy < g.Rows; cy++ {
		for cx := 0; cx < g.Cols; cx++ {
			ci := g.cIdx(cx, cy)
			if g.CellCity[ci] > 0 && cityEligible(g.CellType[ci]) {
				cityCells = append(cityCells, [2]int{cx, cy})
			}
		}
	}
	return cityCells
}

// generateBuildings places deterministically-seeded buildings over the painted
// city regions. When the grid has a palette, buildings are stamped from it
// (respecting Density and road-facing rotation); otherwise it falls back to the
// procedural style. Footprints overlapping roads, water, non-city terrain, or
// other buildings are rejected.
func generateBuildings(g *WorldGrid, style BuildingStyle, road *roadRibbon, used map[string]bool) []*building {
	if len(g.Palette) > 0 {
		return generateBuildingsFromPalette(g, road, used)
	}
	return generateBuildingsProcedural(g, style, road, used)
}

// placementAxes chooses the building's local axes: a flat face turned toward the
// nearest road when FaceRoads is on and a road is close, else a random rotation.
func placementAxes(g *WorldGrid, road *roadRibbon, center mgl32.Vec2, rng *rand.Rand) (ux, uy mgl32.Vec2) {
	if g.FaceRoads {
		if dir, ok := nearestRoadDir(road, center, g.CellSize*2.5); ok {
			// uy (the door/front axis) points at the road; ux is perpendicular.
			return mgl32.Vec2{dir.Y(), -dir.X()}, dir
		}
	}
	angle := float64(rng.Float32()) * math.Pi
	cs, sn := float32(math.Cos(angle)), float32(math.Sin(angle))
	return mgl32.Vec2{cs, sn}, mgl32.Vec2{-sn, cs}
}

// generateBuildingsFromPalette stamps palette templates into the city.
func generateBuildingsFromPalette(g *WorldGrid, road *roadRibbon, used map[string]bool) []*building {
	cityCells := collectCityCells(g)
	if len(cityCells) == 0 {
		return nil
	}
	rng := rand.New(rand.NewSource(g.Seed*2862933555777941757 + 3037000493))

	density := g.Density
	if density < 0 {
		density = 0
	}
	if density > 1 {
		density = 1
	}
	// Denser cities get more placement attempts and smaller inter-building gaps.
	attempts := int(float32(len(cityCells)) * (2 + 12*density))
	margin := 3.0 - 2.6*density

	var totalW float32
	for i := range g.Palette {
		w := g.Palette[i].Weight
		if w <= 0 {
			w = 1
		}
		totalW += w
	}

	var blds []*building
	for a := 0; a < attempts; a++ {
		t := pickTemplate(g.Palette, totalW, rng)
		c := cityCells[rng.Intn(len(cityCells))]
		base := g.cellCenterWorld(c[0], c[1])
		center := mgl32.Vec2{
			base.X() + (rng.Float32()-0.5)*g.CellSize,
			base.Y() + (rng.Float32()-0.5)*g.CellSize,
		}
		ux, uy := placementAxes(g, road, center, rng)
		b := instantiateBuilding(t, center, ux, uy, g)
		if b.hwIn < 1.0 || b.hhIn < 1.0 {
			continue
		}
		if !buildingFootprintValid(g, b) {
			continue
		}
		if buildingOverlapsRoad(b, road) {
			continue
		}
		if buildingOverlapsAny(b, blds, margin) {
			continue
		}
		used[b.floorTex] = true
		used[b.wallTex] = true
		used[b.roofTex] = true
		setupDoor(b, road)
		blds = append(blds, b)
	}
	return blds
}

// generateBuildingsProcedural is the palette-free fallback (random rotated boxes).
func generateBuildingsProcedural(g *WorldGrid, style BuildingStyle, road *roadRibbon, used map[string]bool) []*building {
	cityCells := collectCityCells(g)
	if len(cityCells) == 0 {
		return nil
	}

	rng := rand.New(rand.NewSource(g.Seed*2862933555777941757 + 3037000493))
	maxSpan := float32(style.MaxLotCells) * g.CellSize
	if maxSpan < 6 {
		maxSpan = 6
	}

	var blds []*building
	attempts := len(cityCells) * 8
	for a := 0; a < attempts; a++ {
		c := cityCells[rng.Intn(len(cityCells))]
		base := g.cellCenterWorld(c[0], c[1])
		center := mgl32.Vec2{
			base.X() + (rng.Float32()-0.5)*g.CellSize,
			base.Y() + (rng.Float32()-0.5)*g.CellSize,
		}
		ux, uy := placementAxes(g, road, center, rng)

		hwOut := 2.6 + rng.Float32()*(maxSpan*0.5-2.6)
		hhOut := 2.6 + rng.Float32()*(maxSpan*0.5-2.6)
		b := &building{
			center:      center,
			ux:          ux,
			uy:          uy,
			hwOut:       hwOut,
			hhOut:       hhOut,
			hwIn:        hwOut - cityWallThick,
			hhIn:        hhOut - cityWallThick,
			parapet:     cityParapet,
			doorHalfLen: 0.95,
			enterable:   true,
		}
		if b.hwIn < 1.6 || b.hhIn < 1.6 {
			continue // too small to walk into
		}
		if !buildingFootprintValid(g, b) {
			continue
		}
		if buildingOverlapsRoad(b, road) {
			continue
		}
		if buildingOverlapsAny(b, blds, 1.2) {
			continue
		}

		// Flat roof above the tallest ground under the footprint.
		stories := weightedStories(rng, style.StoryWeights)
		maxGround := footprintMaxHeight(g, b)
		b.roofY = maxGround + float32(stories)*style.StoryHeight
		b.floorTex = style.FloorTexture
		b.wallTex = pickTexture(style.Textures, rng)
		b.roofTex = pickTexture(style.Textures, rng)
		used[b.floorTex] = true
		used[b.wallTex] = true
		used[b.roofTex] = true

		setupDoor(b, road)
		blds = append(blds, b)
	}
	return blds
}

// instantiateBuilding builds a runtime building from a template at a placement.
func instantiateBuilding(t BuildingTemplate, center, ux, uy mgl32.Vec2, g *WorldGrid) *building {
	hwOut := t.Width * 0.5
	hhOut := t.Depth * 0.5
	if hwOut < 1.5 {
		hwOut = 1.5
	}
	if hhOut < 1.5 {
		hhOut = 1.5
	}
	wt := t.WallThick
	if wt < 0.15 {
		wt = 0.15
	}
	if wt > hwOut-1.0 {
		wt = hwOut - 1.0
	}
	if wt > hhOut-1.0 {
		wt = hhOut - 1.0
	}
	if wt < 0.15 {
		wt = 0.15
	}
	stories := t.Stories
	if stories < 1 {
		stories = 1
	}
	sh := t.StoryHeight
	if sh <= 0 {
		sh = 4
	}
	b := &building{
		center:      center,
		ux:          ux,
		uy:          uy,
		hwOut:       hwOut,
		hhOut:       hhOut,
		hwIn:        hwOut - wt,
		hhIn:        hhOut - wt,
		parapet:     t.Parapet,
		doorHalfLen: t.DoorWidth * 0.5,
		enterable:   t.Enterable,
		floorTex:    t.FloorTex,
		wallTex:     t.WallTex,
		roofTex:     t.RoofTex,
	}
	if b.parapet < 0 {
		b.parapet = 0
	}
	if b.doorHalfLen < 0.4 {
		b.doorHalfLen = 0.4
	}
	if b.floorTex == "" {
		b.floorTex = "stone_floor"
	}
	if b.wallTex == "" {
		b.wallTex = "stucco"
	}
	if b.roofTex == "" {
		b.roofTex = "dirt"
	}
	b.roofY = footprintMaxHeight(g, b) + float32(stories)*sh
	return b
}

// pickTemplate returns a weight-proportional random template from the palette.
func pickTemplate(palette []BuildingTemplate, totalW float32, rng *rand.Rand) BuildingTemplate {
	if totalW <= 0 {
		return palette[rng.Intn(len(palette))]
	}
	x := rng.Float32() * totalW
	for i := range palette {
		w := palette[i].Weight
		if w <= 0 {
			w = 1
		}
		if x < w {
			return palette[i]
		}
		x -= w
	}
	return palette[len(palette)-1]
}

// nearestRoadDir returns the unit direction from p to the closest point on the
// road ribbon within maxDist, or ok=false if no road is that close.
func nearestRoadDir(road *roadRibbon, p mgl32.Vec2, maxDist float32) (mgl32.Vec2, bool) {
	if road == nil {
		return mgl32.Vec2{}, false
	}
	best := maxDist
	var bestPt mgl32.Vec2
	found := false
	for _, s := range road.segs {
		cp, d := closestPointOnSegment2D(p, s.a, s.b)
		if d < best {
			best = d
			bestPt = cp
			found = true
		}
	}
	if !found {
		return mgl32.Vec2{}, false
	}
	dir := bestPt.Sub(p)
	if dir.Len() < 1e-4 {
		return mgl32.Vec2{0, 1}, true
	}
	return dir.Normalize(), true
}

// buildingOverlapsRoad reports whether any part of the footprint (sampled, plus a
// small setback margin) falls on the road ribbon — so buildings never sit on a
// road even when roads are free-hand strokes (which don't tag cells).
func buildingOverlapsRoad(b *building, road *roadRibbon) bool {
	if road == nil {
		return false
	}
	const n = 4
	const margin = float32(0.75)
	hw := b.hwOut + margin
	hh := b.hhOut + margin
	for i := 0; i <= n; i++ {
		for j := 0; j <= n; j++ {
			u := (float32(i)/float32(n)*2 - 1) * hw
			v := (float32(j)/float32(n)*2 - 1) * hh
			p := b.center.Add(b.ux.Mul(u)).Add(b.uy.Mul(v))
			if road.contains(p) {
				return true
			}
		}
	}
	return false
}

// buildingFootprintValid samples the outer footprint; every sample must lie on a
// city-tagged, building-eligible (non road/water) cell inside the grid.
func buildingFootprintValid(g *WorldGrid, b *building) bool {
	const n = 5
	for i := 0; i <= n; i++ {
		for j := 0; j <= n; j++ {
			u := (float32(i)/float32(n)*2 - 1) * b.hwOut
			v := (float32(j)/float32(n)*2 - 1) * b.hhOut
			p := b.center.Add(b.ux.Mul(u)).Add(b.uy.Mul(v))
			cx := int(math.Floor(float64((p.X() - g.Origin.X()) / g.CellSize)))
			cy := int(math.Floor(float64((p.Y() - g.Origin.Y()) / g.CellSize)))
			ci := g.cIdx(cx, cy)
			if ci < 0 {
				return false
			}
			if g.CellCity[ci] <= 0 || !cityEligible(g.CellType[ci]) {
				return false
			}
		}
	}
	return true
}

// footprintMaxHeight returns the maximum terrain height across the footprint.
func footprintMaxHeight(g *WorldGrid, b *building) float32 {
	maxH := float32(-math.MaxFloat32)
	const n = 4
	for i := 0; i <= n; i++ {
		for j := 0; j <= n; j++ {
			u := (float32(i)/float32(n)*2 - 1) * b.hwOut
			v := (float32(j)/float32(n)*2 - 1) * b.hhOut
			p := b.center.Add(b.ux.Mul(u)).Add(b.uy.Mul(v))
			if h := g.HeightAt(p.X(), p.Y()); h > maxH {
				maxH = h
			}
		}
	}
	if maxH == -math.MaxFloat32 {
		return 0
	}
	return maxH
}

// buildingOverlapsAny returns true if b (inflated by margin) overlaps any rect in
// list, using the separating-axis test for oriented rectangles.
func buildingOverlapsAny(b *building, list []*building, margin float32) bool {
	for _, o := range list {
		if obbOverlap(b, o, margin) {
			return true
		}
	}
	return false
}

func obbOverlap(a, b *building, margin float32) bool {
	axes := []mgl32.Vec2{a.ux, a.uy, b.ux, b.uy}
	ac := a.corners()
	bc := b.corners()
	for _, ax := range axes {
		amin, amax := projectCorners(ac[:], ax)
		bmin, bmax := projectCorners(bc[:], ax)
		if amax+margin < bmin || bmax+margin < amin {
			return false // separating axis found
		}
	}
	return true
}

func projectCorners(c []mgl32.Vec2, ax mgl32.Vec2) (min, max float32) {
	min = float32(math.MaxFloat32)
	max = -float32(math.MaxFloat32)
	for _, p := range c {
		d := p.Dot(ax)
		if d < min {
			min = d
		}
		if d > max {
			max = d
		}
	}
	return
}

// setupDoor places the doorway on the building side that faces the nearest road
// (defaulting to the +uy front side if no road is near). No-op for sealed
// (non-enterable) buildings.
func setupDoor(b *building, road *roadRibbon) {
	if !b.enterable {
		b.door = doorRegion{}
		return
	}
	type sideDef struct {
		nrm, tan mgl32.Vec2
		half     float32
	}
	sides := []sideDef{
		{b.uy, b.ux, b.hhOut},        // +uy front (default facing)
		{b.uy.Mul(-1), b.ux, b.hhOut}, // -uy back
		{b.ux, b.uy, b.hwOut},         // +ux
		{b.ux.Mul(-1), b.uy, b.hwOut}, // -ux
	}
	best := -1
	bestT := float32(1e9)
	for i, s := range sides {
		fc := b.center.Add(s.nrm.Mul(s.half))
		for t := float32(0.5); t <= 5.0; t += 0.5 {
			if road.contains(fc.Add(s.nrm.Mul(t))) {
				if t < bestT {
					bestT = t
					best = i
				}
				break
			}
		}
	}
	if best < 0 {
		best = 0 // no road nearby: front side
	}
	s := sides[best]
	halfLen := b.doorHalfLen
	if halfLen < 0.4 {
		halfLen = 0.4
	}
	wt := b.hwOut - b.hwIn // actual wall thickness
	fc := b.center.Add(s.nrm.Mul(s.half))
	b.door = doorRegion{
		valid:     true,
		center:    fc.Sub(s.nrm.Mul(wt * 0.5)),
		tangent:   s.tan,
		halfLen:   halfLen,
		halfThick: wt*0.5 + 0.5,
	}
}
