package world

// terrain.go — a chunk's heightfield ground.
//
// Brushes are convex solids, and building open ground out of them means
// hundreds of hulls: level1's canyon is 734 of them, every one a separate
// candidate for collision, rays, the nav rasteriser and the grass bake. A
// terrain answers the question all four of those actually ask — "how high is
// the ground at this XZ" — with one cell lookup instead.
//
// # The grid
//
// A Terrain is a regular grid in its chunk's local space. Origin is the grid's
// minimum-XZ corner, CellSize the edge of one square cell, and there are
// (CellsX+1)*(CellsZ+1) vertices, stored row by row along X (Index). Heights
// are relative to Origin.Y, so moving the whole ground up is one field.
//
// # One triangulation, everywhere
//
// Every cell is two triangles, split along whichever diagonal is flatter
// (SplitsMain). The renderer, collision, raycasts, navigation and scatter all
// read the ground through that same split — HeightAt interpolates on the
// triangle, never bilinearly — so what a body stands on is exactly what is
// drawn, and a sharp ridge sculpted along a diagonal stays sharp instead of
// being folded the other way.
//
// # Holes
//
// A cell can be a hole: it is not drawn, not solid and not ground. Holes are
// how brushes join terrain where a heightfield cannot go — a cave mouth, a
// bunker sunk into a hillside, a sheer cliff built from slabs.
//
// # Layers
//
// The ground is painted with up to TerrainLayerCount textures. Every vertex
// carries one weight per layer (0..255, summing to 255), blended across the
// triangle by the shader. Weights at vertex resolution rather than a finer
// splat map are deliberate: at 64x64 textures and metre-scale cells the
// transitions come out as chunky as the rest of the world's detail.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// TerrainLayerCount is how many textures one terrain can blend. Four is what
// one RGBA weight per vertex holds.
const TerrainLayerCount = 4

// TerrainMaxCells caps a terrain's cell count along each axis, so a typo in a
// level file cannot ask for a grid the size of a planet.
const TerrainMaxCells = 1024

// Terrain is a chunk's heightfield ground. See the file comment.
type Terrain struct {
	// Origin is the chunk-local position of the grid's (0,0) vertex before
	// its height is added.
	Origin mgl32.Vec3
	// CellSize is the edge length of one square cell, in chunk units.
	CellSize float32
	// CellsX, CellsZ are the grid's size in cells.
	CellsX, CellsZ int
	// Heights holds one height per vertex, relative to Origin.Y, indexed by
	// Index(x, z).
	Heights []float32
	// Holes holds one flag per cell, indexed by CellIndex(x, z): non-zero
	// cells are holes. nil means no holes at all.
	Holes []uint8
	// Layers names the texture painted by each weight channel. An empty name
	// is an unused layer; a terrain whose layers are all empty is drawn in
	// its normal colour, the same as an untextured brush face.
	Layers [TerrainLayerCount]string
	// Weights holds TerrainLayerCount weights per vertex, vertex-major:
	// Weights[Index(x,z)*TerrainLayerCount + layer]. nil means every vertex
	// is entirely layer 0.
	Weights []uint8
	// TextureScale is how many chunk units one repeat of a layer texture
	// spans. 0 reads as DefaultTerrainTextureScale.
	TextureScale float32

	// Generation is runtime-only: bumped by Touch on every edit, so caches
	// built from the grid (render meshes, nav tiles, scatter bakes) can tell
	// they are stale without hashing it.
	Generation uint64

	// dirty is the vertex rectangle edited since the renderer last looked
	// (TakeDirty), so a brush stroke re-uploads the rows it touched rather
	// than the whole grid. dirtyAll means "everything": the state after a
	// load, a clone, or a Touch that said nothing about where. Runtime-only;
	// unexported, so gob leaves it out of compiled levels.
	dirty    [4]int
	dirtyAll bool
	dirtySet bool
}

// DefaultTerrainTextureScale is TextureScale's zero value: one repeat per two
// units, which puts a 64x64 texture at the texel density brush faces carry.
const DefaultTerrainTextureScale = 2

// terrainGeneration is shared by every terrain so a clone never repeats a
// generation its source already handed out.
var terrainGeneration uint64

// TerrainGeneration moves whenever any terrain anywhere is edited: the
// terrain counterpart of BrushMeshGeneration, for caches that span the level
// (decals) and would otherwise have to walk every terrain to learn nothing
// changed.
func TerrainGeneration() uint64 { return terrainGeneration }

// NewTerrain builds a flat terrain of cellsX by cellsZ cells whose (0,0)
// corner sits at origin, entirely painted with layer 0.
func NewTerrain(origin mgl32.Vec3, cellSize float32, cellsX, cellsZ int) *Terrain {
	t := &Terrain{Origin: origin, CellSize: cellSize, CellsX: cellsX, CellsZ: cellsZ}
	t.Normalize()
	return t
}

// Normalize makes a terrain read from a file safe to use: sizes clamped into
// range, and every per-vertex or per-cell array exactly the length the grid
// needs (padded with zero heights and full layer-0 weights, or truncated).
func (t *Terrain) Normalize() {
	if t.CellSize <= 0 {
		t.CellSize = 1
	}
	t.CellsX = clampInt(t.CellsX, 1, TerrainMaxCells)
	t.CellsZ = clampInt(t.CellsZ, 1, TerrainMaxCells)
	nv := t.VertexCount()
	if len(t.Heights) != nv {
		h := make([]float32, nv)
		copy(h, t.Heights)
		t.Heights = h
	}
	if t.Holes != nil && len(t.Holes) != t.CellCount() {
		h := make([]uint8, t.CellCount())
		copy(h, t.Holes)
		t.Holes = h
	}
	if t.Weights != nil && len(t.Weights) != nv*TerrainLayerCount {
		w := make([]uint8, nv*TerrainLayerCount)
		n := copy(w, t.Weights)
		for i := n / TerrainLayerCount * TerrainLayerCount; i < len(w); i += TerrainLayerCount {
			if i+TerrainLayerCount > n {
				w[i], w[i+1], w[i+2], w[i+3] = 255, 0, 0, 0
			}
		}
		t.Weights = w
	}
	t.Touch()
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Touch marks the terrain edited. Anything that writes Heights, Holes,
// Weights or Layers must call it, or caches keep serving the old grid.
func (t *Terrain) Touch() {
	terrainGeneration++
	t.Generation = terrainGeneration
	t.dirtyAll = true
}

// TouchRect marks vertices [x0,x1] x [z0,z1] edited (and, since a cell's
// shape depends on its corners, the cells around them). Cheaper than Touch
// for the renderer, which re-uploads only that region.
func (t *Terrain) TouchRect(x0, z0, x1, z1 int) {
	terrainGeneration++
	t.Generation = terrainGeneration
	x0, z0 = clampInt(x0, 0, t.CellsX), clampInt(z0, 0, t.CellsZ)
	x1, z1 = clampInt(x1, 0, t.CellsX), clampInt(z1, 0, t.CellsZ)
	if !t.dirtySet {
		t.dirty = [4]int{x0, z0, x1, z1}
		t.dirtySet = true
		return
	}
	t.dirty = [4]int{minInt(t.dirty[0], x0), minInt(t.dirty[1], z0), maxInt(t.dirty[2], x1), maxInt(t.dirty[3], z1)}
}

// TakeDirty returns the vertex rectangle edited since the last call and
// clears it; all is true when the whole grid must be treated as changed.
// There is one caller, the renderer: a second would steal the first's
// rectangle.
func (t *Terrain) TakeDirty() (x0, z0, x1, z1 int, all bool) {
	x0, z0, x1, z1 = t.dirty[0], t.dirty[1], t.dirty[2], t.dirty[3]
	all = t.dirtyAll || !t.dirtySet
	t.dirtyAll, t.dirtySet = false, false
	return x0, z0, x1, z1, all
}

// Clone returns an independent deep copy.
func (t *Terrain) Clone() *Terrain {
	if t == nil {
		return nil
	}
	cp := *t
	cp.Heights = append([]float32(nil), t.Heights...)
	if t.Holes != nil {
		cp.Holes = append([]uint8(nil), t.Holes...)
	}
	if t.Weights != nil {
		cp.Weights = append([]uint8(nil), t.Weights...)
	}
	cp.Touch()
	return &cp
}

// VertsX and VertsZ are the grid's size in vertices.
func (t *Terrain) VertsX() int { return t.CellsX + 1 }
func (t *Terrain) VertsZ() int { return t.CellsZ + 1 }

// VertexCount and CellCount are the lengths of the per-vertex and per-cell
// arrays.
func (t *Terrain) VertexCount() int { return (t.CellsX + 1) * (t.CellsZ + 1) }
func (t *Terrain) CellCount() int   { return t.CellsX * t.CellsZ }

// Index is vertex (x, z)'s position in Heights.
func (t *Terrain) Index(x, z int) int { return z*(t.CellsX+1) + x }

// CellIndex is cell (x, z)'s position in Holes.
func (t *Terrain) CellIndex(x, z int) int { return z*t.CellsX + x }

// Height is vertex (x, z)'s height relative to Origin.Y, clamped to the grid.
func (t *Terrain) Height(x, z int) float32 {
	x = clampInt(x, 0, t.CellsX)
	z = clampInt(z, 0, t.CellsZ)
	return t.Heights[t.Index(x, z)]
}

// Vertex is vertex (x, z) in chunk space.
func (t *Terrain) Vertex(x, z int) mgl32.Vec3 {
	return mgl32.Vec3{
		t.Origin.X() + float32(x)*t.CellSize,
		t.Origin.Y() + t.Height(x, z),
		t.Origin.Z() + float32(z)*t.CellSize,
	}
}

// IsHole reports whether cell (x, z) is a hole. Out-of-grid cells are holes:
// there is no ground there either.
func (t *Terrain) IsHole(x, z int) bool {
	if x < 0 || z < 0 || x >= t.CellsX || z >= t.CellsZ {
		return true
	}
	return t.Holes != nil && t.Holes[t.CellIndex(x, z)] != 0
}

// SetHole marks or clears cell (x, z) as a hole. It does not Touch.
func (t *Terrain) SetHole(x, z int, hole bool) {
	if x < 0 || z < 0 || x >= t.CellsX || z >= t.CellsZ {
		return
	}
	if t.Holes == nil {
		if !hole {
			return
		}
		t.Holes = make([]uint8, t.CellCount())
	}
	v := uint8(0)
	if hole {
		v = 1
	}
	t.Holes[t.CellIndex(x, z)] = v
}

// HasHoles reports whether any cell is a hole.
func (t *Terrain) HasHoles() bool {
	for _, h := range t.Holes {
		if h != 0 {
			return true
		}
	}
	return false
}

// Weight is vertex (x, z)'s weight for layer, 0..255.
func (t *Terrain) Weight(x, z, layer int) uint8 {
	if t.Weights == nil {
		if layer == 0 {
			return 255
		}
		return 0
	}
	return t.Weights[t.Index(x, z)*TerrainLayerCount+layer]
}

// ensureWeights materialises the implicit all-layer-0 weights.
func (t *Terrain) ensureWeights() {
	if t.Weights != nil {
		return
	}
	t.Weights = make([]uint8, t.VertexCount()*TerrainLayerCount)
	for i := 0; i < len(t.Weights); i += TerrainLayerCount {
		t.Weights[i] = 255
	}
}

// PaintWeight moves vertex (x, z) toward being entirely layer by amount
// (0..1): the painted layer gains and every other layer gives up weight in
// proportion to what it has, so the four always sum to 255. It does not
// Touch.
func (t *Terrain) PaintWeight(x, z, layer int, amount float32) {
	if x < 0 || z < 0 || x > t.CellsX || z > t.CellsZ || layer < 0 || layer >= TerrainLayerCount {
		return
	}
	if amount <= 0 {
		return
	}
	if amount > 1 {
		amount = 1
	}
	t.ensureWeights()
	base := t.Index(x, z) * TerrainLayerCount
	var w [TerrainLayerCount]float32
	for i := range w {
		w[i] = float32(t.Weights[base+i]) / 255
	}
	for i := range w {
		if i == layer {
			w[i] += (1 - w[i]) * amount
		} else {
			w[i] *= 1 - amount
		}
	}
	setNormalizedWeights(t.Weights[base:base+TerrainLayerCount], w)
}

// setNormalizedWeights writes w into dst as bytes summing to exactly 255,
// handing the rounding remainder to the heaviest layer so a painted-over
// layer cannot creep back one unit at a time.
func setNormalizedWeights(dst []uint8, w [TerrainLayerCount]float32) {
	sum := float32(0)
	for _, v := range w {
		sum += v
	}
	if sum <= 0 {
		dst[0], dst[1], dst[2], dst[3] = 255, 0, 0, 0
		return
	}
	total := 0
	heaviest := 0
	for i, v := range w {
		// Floored, not rounded: a nearly-gone layer must be able to reach 0,
		// and rounding kept handing half a unit back to it forever.
		b := int(v / sum * 255)
		if b > 255 {
			b = 255
		}
		dst[i] = uint8(b)
		total += b
		if w[i] > w[heaviest] {
			heaviest = i
		}
	}
	fix := int(dst[heaviest]) + 255 - total
	if fix < 0 {
		fix = 0
	}
	if fix > 255 {
		fix = 255
	}
	dst[heaviest] = uint8(fix)
}

// EffectiveTextureScale is TextureScale with its zero value read as the
// default.
func (t *Terrain) EffectiveTextureScale() float32 {
	if t.TextureScale <= 0 {
		return DefaultTerrainTextureScale
	}
	return t.TextureScale
}

// HasTexture reports whether any layer names a texture.
func (t *Terrain) HasTexture() bool {
	for _, l := range t.Layers {
		if l != "" {
			return true
		}
	}
	return false
}

// SplitsMain reports whether cell (x, z) is split along its (x,z)-(x+1,z+1)
// diagonal. The other choice is (x+1,z)-(x,z+1). Whichever pair of corners
// is closer in height is joined, which runs the fold along a ridge or a
// valley rather than across it.
func (t *Terrain) SplitsMain(x, z int) bool {
	h00 := t.Height(x, z)
	h11 := t.Height(x+1, z+1)
	h10 := t.Height(x+1, z)
	h01 := t.Height(x, z+1)
	return abs32(h00-h11) <= abs32(h10-h01)
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// CellTriangles returns cell (x, z)'s two triangles in chunk space, each
// wound counter-clockwise seen from above so its geometric normal points up.
func (t *Terrain) CellTriangles(x, z int) [2][3]mgl32.Vec3 {
	p00 := t.Vertex(x, z)
	p10 := t.Vertex(x+1, z)
	p01 := t.Vertex(x, z+1)
	p11 := t.Vertex(x+1, z+1)
	if t.SplitsMain(x, z) {
		return [2][3]mgl32.Vec3{{p00, p01, p11}, {p00, p11, p10}}
	}
	return [2][3]mgl32.Vec3{{p00, p01, p10}, {p10, p01, p11}}
}

// CellCorners returns the vertex coordinates of cell (x, z)'s two triangles in
// the same order and winding as CellTriangles, for callers that need the
// grid indices rather than positions.
func (t *Terrain) CellCorners(x, z int) [2][3][2]int {
	if t.SplitsMain(x, z) {
		return [2][3][2]int{
			{{x, z}, {x, z + 1}, {x + 1, z + 1}},
			{{x, z}, {x + 1, z + 1}, {x + 1, z}},
		}
	}
	return [2][3][2]int{
		{{x, z}, {x, z + 1}, {x + 1, z}},
		{{x + 1, z}, {x, z + 1}, {x + 1, z + 1}},
	}
}

// CellAt returns the cell containing chunk-space (lx, lz) and the point's
// fractional position inside it. ok is false outside the grid.
func (t *Terrain) CellAt(lx, lz float32) (cx, cz int, fx, fz float32, ok bool) {
	gx := (lx - t.Origin.X()) / t.CellSize
	gz := (lz - t.Origin.Z()) / t.CellSize
	if gx < 0 || gz < 0 || gx > float32(t.CellsX) || gz > float32(t.CellsZ) {
		return 0, 0, 0, 0, false
	}
	cx = int(gx)
	cz = int(gz)
	if cx >= t.CellsX {
		cx = t.CellsX - 1
	}
	if cz >= t.CellsZ {
		cz = t.CellsZ - 1
	}
	return cx, cz, gx - float32(cx), gz - float32(cz), true
}

// HeightAt returns the chunk-space Y of the ground at chunk-space (lx, lz),
// interpolated on the cell's own triangle, and that triangle's unit normal.
// ok is false outside the grid or over a hole.
func (t *Terrain) HeightAt(lx, lz float32) (y float32, n mgl32.Vec3, ok bool) {
	cx, cz, fx, fz, ok := t.CellAt(lx, lz)
	if !ok || t.IsHole(cx, cz) {
		return 0, mgl32.Vec3{}, false
	}
	h00 := t.Height(cx, cz)
	h10 := t.Height(cx+1, cz)
	h01 := t.Height(cx, cz+1)
	h11 := t.Height(cx+1, cz+1)
	s := t.CellSize
	var h float32
	if t.SplitsMain(cx, cz) {
		if fz >= fx { // triangle (00, 01, 11)
			h = h00 + (h11-h01)*fx + (h01-h00)*fz
			n = mgl32.Vec3{-(h11 - h01) / s, 1, -(h01 - h00) / s}
		} else { // triangle (00, 11, 10)
			h = h00 + (h10-h00)*fx + (h11-h10)*fz
			n = mgl32.Vec3{-(h10 - h00) / s, 1, -(h11 - h10) / s}
		}
	} else {
		if fx+fz <= 1 { // triangle (00, 01, 10)
			h = h00 + (h10-h00)*fx + (h01-h00)*fz
			n = mgl32.Vec3{-(h10 - h00) / s, 1, -(h01 - h00) / s}
		} else { // triangle (10, 01, 11)
			h = h11 + (h11-h01)*(fx-1) + (h11-h10)*(fz-1)
			n = mgl32.Vec3{-(h11 - h01) / s, 1, -(h11 - h10) / s}
		}
	}
	return t.Origin.Y() + h, n.Normalize(), true
}

// SmoothNormal is the grid's smooth normal at vertex (x, z), from central
// differences of its neighbours' heights.
func (t *Terrain) SmoothNormal(x, z int) mgl32.Vec3 {
	dx := (t.Height(x+1, z) - t.Height(x-1, z)) / (float32(minInt(x+1, t.CellsX)-maxInt(x-1, 0)) * t.CellSize)
	dz := (t.Height(x, z+1) - t.Height(x, z-1)) / (float32(minInt(z+1, t.CellsZ)-maxInt(z-1, 0)) * t.CellSize)
	return mgl32.Vec3{-dx, 1, -dz}.Normalize()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// LocalBounds is the chunk-space box around every vertex, holes included.
func (t *Terrain) LocalBounds() (lo, hi mgl32.Vec3) {
	minH, maxH := float32(math.Inf(1)), float32(math.Inf(-1))
	for _, h := range t.Heights {
		if h < minH {
			minH = h
		}
		if h > maxH {
			maxH = h
		}
	}
	if len(t.Heights) == 0 {
		minH, maxH = 0, 0
	}
	lo = mgl32.Vec3{t.Origin.X(), t.Origin.Y() + minH, t.Origin.Z()}
	hi = mgl32.Vec3{
		t.Origin.X() + float32(t.CellsX)*t.CellSize,
		t.Origin.Y() + maxH,
		t.Origin.Z() + float32(t.CellsZ)*t.CellSize,
	}
	return lo, hi
}

// CellRange returns the cells whose XZ footprint overlaps the chunk-space box
// [lo, hi], clamped to the grid. ok is false when the box misses the grid.
func (t *Terrain) CellRange(lo, hi mgl32.Vec3) (x0, z0, x1, z1 int, ok bool) {
	fx0 := (lo.X() - t.Origin.X()) / t.CellSize
	fz0 := (lo.Z() - t.Origin.Z()) / t.CellSize
	fx1 := (hi.X() - t.Origin.X()) / t.CellSize
	fz1 := (hi.Z() - t.Origin.Z()) / t.CellSize
	if fx1 < 0 || fz1 < 0 || fx0 > float32(t.CellsX) || fz0 > float32(t.CellsZ) {
		return 0, 0, 0, 0, false
	}
	x0 = clampInt(int(math.Floor(float64(fx0))), 0, t.CellsX-1)
	z0 = clampInt(int(math.Floor(float64(fz0))), 0, t.CellsZ-1)
	x1 = clampInt(int(math.Floor(float64(fx1))), 0, t.CellsX-1)
	z1 = clampInt(int(math.Floor(float64(fz1))), 0, t.CellsZ-1)
	return x0, z0, x1, z1, true
}

// TrianglesIn appends to dst the triangles of every non-hole cell overlapping
// the chunk-space box's XZ footprint whose own height range reaches the box,
// in chunk space, and returns it.
func (t *Terrain) TrianglesIn(dst [][3]mgl32.Vec3, lo, hi mgl32.Vec3) [][3]mgl32.Vec3 {
	x0, z0, x1, z1, ok := t.CellRange(lo, hi)
	if !ok {
		return dst
	}
	for z := z0; z <= z1; z++ {
		for x := x0; x <= x1; x++ {
			if t.IsHole(x, z) {
				continue
			}
			cmin, cmax := t.cellHeightRange(x, z)
			if t.Origin.Y()+cmax < lo.Y() || t.Origin.Y()+cmin > hi.Y() {
				continue
			}
			tris := t.CellTriangles(x, z)
			dst = append(dst, tris[0], tris[1])
		}
	}
	return dst
}

func (t *Terrain) cellHeightRange(x, z int) (lo, hi float32) {
	a, b, c, d := t.Height(x, z), t.Height(x+1, z), t.Height(x, z+1), t.Height(x+1, z+1)
	lo, hi = a, a
	for _, v := range [3]float32{b, c, d} {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	return lo, hi
}

// Raycast intersects the chunk-space ray origin + t*dir (t in [0, maxT]) with
// the terrain's non-hole triangles and returns the nearest hit's distance and
// the triangle's unit normal (pointing up, whichever side was hit). It walks
// the grid cell by cell along the ray (a 2D DDA), so a cast costs the cells it
// crosses, not the terrain's size.
func (t *Terrain) Raycast(origin, dir mgl32.Vec3, maxT float32) (hitT float32, n mgl32.Vec3, ok bool) {
	// Clip the ray to the grid's box first, so the walk starts inside.
	lo, hi := t.LocalBounds()
	t0, t1, hitBox := rayBoxSpan(origin, dir, lo, hi)
	if !hitBox || t1 < 0 || t0 > maxT {
		return 0, mgl32.Vec3{}, false
	}
	if t0 < 0 {
		t0 = 0
	}
	if t1 > maxT {
		t1 = maxT
	}
	start := origin.Add(dir.Mul(t0))
	gx := (start.X() - t.Origin.X()) / t.CellSize
	gz := (start.Z() - t.Origin.Z()) / t.CellSize
	cx := clampInt(int(math.Floor(float64(gx))), 0, t.CellsX-1)
	cz := clampInt(int(math.Floor(float64(gz))), 0, t.CellsZ-1)

	stepX, stepZ := 0, 0
	tMaxX, tMaxZ := float32(math.Inf(1)), float32(math.Inf(1))
	tDeltaX, tDeltaZ := float32(math.Inf(1)), float32(math.Inf(1))
	if dir.X() > 0 {
		stepX = 1
		tDeltaX = t.CellSize / dir.X()
		tMaxX = t0 + (t.Origin.X()+float32(cx+1)*t.CellSize-start.X())/dir.X()
	} else if dir.X() < 0 {
		stepX = -1
		tDeltaX = -t.CellSize / dir.X()
		tMaxX = t0 + (t.Origin.X()+float32(cx)*t.CellSize-start.X())/dir.X()
	}
	if dir.Z() > 0 {
		stepZ = 1
		tDeltaZ = t.CellSize / dir.Z()
		tMaxZ = t0 + (t.Origin.Z()+float32(cz+1)*t.CellSize-start.Z())/dir.Z()
	} else if dir.Z() < 0 {
		stepZ = -1
		tDeltaZ = -t.CellSize / dir.Z()
		tMaxZ = t0 + (t.Origin.Z()+float32(cz)*t.CellSize-start.Z())/dir.Z()
	}

	for {
		if !t.IsHole(cx, cz) {
			best := float32(math.Inf(1))
			var bestN mgl32.Vec3
			for _, tri := range t.CellTriangles(cx, cz) {
				if d, hit := rayTriangle(origin, dir, tri[0], tri[1], tri[2]); hit && d >= 0 && d <= maxT && d < best {
					best = d
					bestN = tri[1].Sub(tri[0]).Cross(tri[2].Sub(tri[0]))
				}
			}
			if !math.IsInf(float64(best), 1) {
				return best, bestN.Normalize(), true
			}
		}
		// Step into whichever neighbour the ray reaches first.
		if tMaxX < tMaxZ {
			if tMaxX > t1 {
				break
			}
			cx += stepX
			tMaxX += tDeltaX
		} else {
			if tMaxZ > t1 || stepZ == 0 {
				break
			}
			cz += stepZ
			tMaxZ += tDeltaZ
		}
		if cx < 0 || cz < 0 || cx >= t.CellsX || cz >= t.CellsZ {
			break
		}
	}
	return 0, mgl32.Vec3{}, false
}

// rayBoxSpan is the slab test: the parameter range over which the ray is
// inside [lo, hi]. Tolerant of zero direction components.
func rayBoxSpan(o, d, lo, hi mgl32.Vec3) (t0, t1 float32, ok bool) {
	t0, t1 = float32(math.Inf(-1)), float32(math.Inf(1))
	for i := 0; i < 3; i++ {
		if d[i] == 0 {
			if o[i] < lo[i]-1e-4 || o[i] > hi[i]+1e-4 {
				return 0, 0, false
			}
			continue
		}
		a := (lo[i] - o[i]) / d[i]
		b := (hi[i] - o[i]) / d[i]
		if a > b {
			a, b = b, a
		}
		if a > t0 {
			t0 = a
		}
		if b < t1 {
			t1 = b
		}
	}
	// A flat terrain has a zero-height box; pad so grazing rays still count.
	return t0 - 1e-3, t1 + 1e-3, t0 <= t1+1e-3
}

// rayTriangle is Möller–Trumbore, two-sided.
func rayTriangle(o, d, a, b, c mgl32.Vec3) (float32, bool) {
	e1 := b.Sub(a)
	e2 := c.Sub(a)
	p := d.Cross(e2)
	det := e1.Dot(p)
	if det > -1e-9 && det < 1e-9 {
		return 0, false
	}
	inv := 1 / det
	s := o.Sub(a)
	u := s.Dot(p) * inv
	if u < -1e-5 || u > 1+1e-5 {
		return 0, false
	}
	q := s.Cross(e1)
	v := d.Dot(q) * inv
	if v < -1e-5 || u+v > 1+1e-5 {
		return 0, false
	}
	return e2.Dot(q) * inv, true
}

// Resize changes the grid to cellsX by cellsZ cells, keeping its (0,0)
// corner, cell size and every height, hole and weight that is still inside
// it. New vertices are flat at height 0 and entirely layer 0.
func (t *Terrain) Resize(cellsX, cellsZ int) {
	cellsX = clampInt(cellsX, 1, TerrainMaxCells)
	cellsZ = clampInt(cellsZ, 1, TerrainMaxCells)
	if cellsX == t.CellsX && cellsZ == t.CellsZ {
		return
	}
	old := *t
	t.CellsX, t.CellsZ = cellsX, cellsZ
	t.Heights = make([]float32, t.VertexCount())
	if old.Weights != nil {
		t.Weights = make([]uint8, t.VertexCount()*TerrainLayerCount)
		for i := 0; i < len(t.Weights); i += TerrainLayerCount {
			t.Weights[i] = 255
		}
	}
	for z := 0; z <= minInt(cellsZ, old.CellsZ); z++ {
		for x := 0; x <= minInt(cellsX, old.CellsX); x++ {
			src, dst := old.Index(x, z), t.Index(x, z)
			t.Heights[dst] = old.Heights[src]
			if old.Weights != nil {
				copy(t.Weights[dst*TerrainLayerCount:(dst+1)*TerrainLayerCount],
					old.Weights[src*TerrainLayerCount:(src+1)*TerrainLayerCount])
			}
		}
	}
	t.Holes = nil
	if old.Holes != nil {
		for z := 0; z < minInt(cellsZ, old.CellsZ); z++ {
			for x := 0; x < minInt(cellsX, old.CellsX); x++ {
				if old.IsHole(x, z) {
					t.SetHole(x, z, true)
				}
			}
		}
	}
	t.Touch()
}
