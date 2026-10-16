package procgen

// preview.go — fast in-memory previews for the editor's viewports.
//
// A stage kind's Preview returns a PreviewData: whatever it wants shown, in
// the generic shapes the Generator editor knows how to draw. The editor has
// no idea what a heightfield or a rock is; it draws
//
//   - image layers in the top-down 2D view (a heightmap, a mask, a shaded
//     relief), each placed over a world-space XZ rectangle, with an optional
//     raw value per pixel for the hover readout;
//   - point layers (scatter positions, with radii) and line layers (a path,
//     a tile grid) over the images;
//   - meshes in the 3D view, through the engine's world program.
//
// So a later stage plugs into both viewports by filling these in, and the
// editor grows no case for it.
//
// Coordinates: the top-down view looks down -Y with +X to the right and +Z
// toward the bottom of the screen. An image's pixel (i, j) — row j, column i,
// row 0 first in the slice — covers x in MinX + [i, i+1]·(MaxX-MinX)/W and z
// in MinZ + [j, j+1]·(MaxZ-MinZ)/H.
//
// Previews are cached by stage hash (PreviewCache), and a stage's preview is
// given its upstream stages' previews (PreviewContext.Upstream), computed
// first and cached the same way: editing a scatter rule re-previews the
// scatter, not the terrain under it.

import (
	"container/list"
	"context"
	"fmt"
	"sync"
	"time"
)

// Rect is a world-space XZ rectangle.
type Rect struct {
	MinX, MinZ, MaxX, MaxZ float64
}

// Empty reports whether the rectangle has no area.
func (r Rect) Empty() bool { return r.MaxX <= r.MinX || r.MaxZ <= r.MinZ }

// Union is the smallest rectangle holding both (an empty one is ignored).
func (r Rect) Union(o Rect) Rect {
	if r.Empty() {
		return o
	}
	if o.Empty() {
		return r
	}
	return Rect{min(r.MinX, o.MinX), min(r.MinZ, o.MinZ), max(r.MaxX, o.MaxX), max(r.MaxZ, o.MaxZ)}
}

// PreviewData is one stage's preview.
type PreviewData struct {
	// Bounds is what the 2D view frames by default. Empty: the union of the
	// layers'.
	Bounds Rect
	Images []ImageLayer
	Points []PointLayer
	Lines  []LineLayer
	Meshes []PreviewMesh
	// Notes are lines of text shown under the viewport: sizes, counts,
	// timings.
	Notes []string
	// Artifact is whatever in-memory product a downstream preview wants from
	// this one (a low-resolution heightfield, say).
	Artifact any
	// Elapsed is how long the preview took (set by the engine).
	Elapsed time.Duration
}

// FrameBounds is Bounds, or the union of every layer's when it is empty.
func (p *PreviewData) FrameBounds() Rect {
	if p == nil {
		return Rect{}
	}
	if !p.Bounds.Empty() {
		return p.Bounds
	}
	var r Rect
	for _, im := range p.Images {
		r = r.Union(im.Bounds)
	}
	grow := func(pts [][2]float64) {
		for _, q := range pts {
			r = r.Union(Rect{q[0] - 0.5, q[1] - 0.5, q[0] + 0.5, q[1] + 0.5})
		}
	}
	for _, l := range p.Points {
		grow(l.Points)
	}
	for _, l := range p.Lines {
		grow(l.Points)
	}
	return r
}

// ImageLayer is a picture over a world rectangle.
type ImageLayer struct {
	Name string
	W, H int
	// RGBA is W*H*4 bytes, row-major from MinZ.
	RGBA []uint8
	// Values, when set, is one raw value per pixel, shown under the cursor
	// ("height 132.4 m").
	Values []float32
	Unit   string
	Bounds Rect
}

// GrayImage is a layer from scalar values mapped lo..hi to black..white
// (Values keeps the scalars for the readout).
func GrayImage(name string, w, h int, vals []float32, lo, hi float32, bounds Rect) ImageLayer {
	return RampImage(name, w, h, vals, lo, hi, bounds, [][4]uint8{{0, 0, 0, 255}, {255, 255, 255, 255}})
}

// RampImage maps scalars through a colour ramp (evenly spaced stops).
func RampImage(name string, w, h int, vals []float32, lo, hi float32, bounds Rect, ramp [][4]uint8) ImageLayer {
	im := ImageLayer{Name: name, W: w, H: h, RGBA: make([]uint8, w*h*4), Values: vals, Bounds: bounds}
	span := hi - lo
	if span == 0 {
		span = 1
	}
	n := len(ramp)
	for i, v := range vals {
		if i >= w*h {
			break
		}
		t := (v - lo) / span
		t = min(max(t, 0), 1)
		c := ramp[0]
		if n > 1 {
			f := t * float32(n-1)
			k := min(int(f), n-2)
			u := f - float32(k)
			a, b := ramp[k], ramp[k+1]
			for ch := 0; ch < 4; ch++ {
				c[ch] = uint8(float32(a[ch]) + (float32(b[ch])-float32(a[ch]))*u + 0.5)
			}
		}
		copy(im.RGBA[i*4:], c[:])
	}
	return im
}

// ValueAt is the raw value under world (x, z), and whether there is one.
func (im *ImageLayer) ValueAt(x, z float64) (float32, bool) {
	if im.Values == nil || im.W == 0 || im.H == 0 || im.Bounds.Empty() {
		return 0, false
	}
	i := int((x - im.Bounds.MinX) / (im.Bounds.MaxX - im.Bounds.MinX) * float64(im.W))
	j := int((z - im.Bounds.MinZ) / (im.Bounds.MaxZ - im.Bounds.MinZ) * float64(im.H))
	if i < 0 || j < 0 || i >= im.W || j >= im.H || j*im.W+i >= len(im.Values) {
		return 0, false
	}
	return im.Values[j*im.W+i], true
}

// PointLayer is dots at world XZ positions.
type PointLayer struct {
	Name   string
	Points [][2]float64
	// Radii, when set, draws each point as a circle of that world radius.
	Radii []float64
	Color [4]float32
}

// LineLayer is a polyline in world XZ.
type LineLayer struct {
	Name   string
	Points [][2]float64
	Closed bool
	Color  [4]float32
}

// PreviewMesh is a triangle mesh for the 3D view, in world space.
type PreviewMesh struct {
	Name      string
	Positions [][3]float32
	// Normals may be nil; the editor derives smooth ones.
	Normals [][3]float32
	Indices []uint32
	// Image, when set with UVs (one per position), names one of the
	// preview's image layers to paint the mesh with — a terrain's albedo
	// hint draped over its tiles. Without it (or with a lookdev texture
	// chosen in the view) the mesh draws as before.
	Image string
	UVs   [][2]float32
	// Colors, when set (one per position), are vertex colours: RGB
	// multiplies the albedo (white when there is no texture or image), A is
	// the cavity/occlusion the shader takes off the ambient light — a
	// generated rock's bake.
	Colors [][4]uint8
	// Turntable asks the 3D view to frame this mesh alone and spin it (a
	// rock variant), rather than frame the whole preview as a landscape.
	Turntable bool
	// Instances, when set, draws the mesh once per entry, each its own
	// model matrix (column-major, mesh space to world) — a scatter's
	// thousands of rocks as one instanced draw of one proxy. Positions are
	// then in the mesh's own space. Without them the mesh is drawn once, in
	// world space, as before.
	Instances [][16]float32
}

// InstanceBounds is the world box the mesh's instances span: each
// instance's transform of the mesh's own bounding box. ok is false for a
// mesh with no instances or no positions.
func (m *PreviewMesh) InstanceBounds() (lo, hi [3]float32, ok bool) {
	if len(m.Instances) == 0 || len(m.Positions) == 0 {
		return lo, hi, false
	}
	mlo, mhi := m.Positions[0], m.Positions[0]
	for _, p := range m.Positions {
		for k := 0; k < 3; k++ {
			mlo[k], mhi[k] = min(mlo[k], p[k]), max(mhi[k], p[k])
		}
	}
	first := true
	for _, t := range m.Instances {
		for c := 0; c < 8; c++ {
			v := [3]float32{mlo[0], mlo[1], mlo[2]}
			if c&1 != 0 {
				v[0] = mhi[0]
			}
			if c&2 != 0 {
				v[1] = mhi[1]
			}
			if c&4 != 0 {
				v[2] = mhi[2]
			}
			var w [3]float32
			for r := 0; r < 3; r++ {
				w[r] = t[r]*v[0] + t[4+r]*v[1] + t[8+r]*v[2] + t[12+r]
			}
			if first {
				lo, hi, first = w, w, false
			}
			for k := 0; k < 3; k++ {
				lo[k], hi[k] = min(lo[k], w[k]), max(hi[k], w[k])
			}
		}
	}
	return lo, hi, true
}

// ---------- the preview runner ----------

// PreviewOptions configure a preview.
type PreviewOptions struct {
	Workers  int
	Progress func(stage string, done float32)
	Log      func(line string)
}

// PreviewCache keeps recent previews by stage hash. Safe for concurrent use.
type PreviewCache struct {
	mu    sync.Mutex
	max   int
	lru   *list.List
	index map[string]*list.Element
}

type previewEntry struct {
	hash string
	data *PreviewData
}

// NewPreviewCache keeps up to max previews (16 when max <= 0).
func NewPreviewCache(max int) *PreviewCache {
	if max <= 0 {
		max = 16
	}
	return &PreviewCache{max: max, lru: list.New(), index: map[string]*list.Element{}}
}

func (c *PreviewCache) get(hash string) *PreviewData {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.index[hash]; ok {
		c.lru.MoveToFront(e)
		return e.Value.(*previewEntry).data
	}
	return nil
}

func (c *PreviewCache) put(hash string, d *PreviewData) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.index[hash]; ok {
		e.Value.(*previewEntry).data = d
		c.lru.MoveToFront(e)
		return
	}
	c.index[hash] = c.lru.PushFront(&previewEntry{hash, d})
	for c.lru.Len() > c.max {
		last := c.lru.Back()
		c.lru.Remove(last)
		delete(c.index, last.Value.(*previewEntry).hash)
	}
}

// Len is how many previews are cached.
func (c *PreviewCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}

// lenientPlan hashes every stage whose kind is known and whose refs resolve
// to stages above it, disabled ones included (a disabled stage can still be
// previewed). Unlike plan it never refuses: a problem elsewhere in the
// document must not stop the stage being looked at from previewing.
func lenientPlan(d *Doc) map[string]*plannedStage {
	out := map[string]*plannedStage{}
	for i := range d.Stages {
		s := &d.Stages[i]
		k := KindByName(s.Kind)
		if k == nil || !ValidStageID(s.ID) || out[s.ID] != nil {
			continue
		}
		p := Resolve(k.Params, s.Params)
		ps := &plannedStage{stage: s, kind: k, params: p, refs: StageRefs(k.Params, p)}
		ps.seed = StageSeed(d.Seed, s.ID, k.Params, p)
		var up []string
		ok := true
		for _, r := range ps.refs {
			u := out[r]
			if u == nil {
				ok = false
				break
			}
			up = append(up, u.hash)
		}
		if !ok {
			continue
		}
		if k.HashExtra != nil {
			if x, err := k.HashExtra(d, s, p); err == nil {
				if x != "" { // as plan: an empty extra is no extra
					up = append(up, "extra:"+x)
				}
			} else {
				up = append(up, "extra-error:"+err.Error())
			}
		}
		up = foldPaint(d, k.Params, p, up)
		ps.hash = PreviewHash(k, ps.seed, p, up)
		out[s.ID] = ps
	}
	return out
}

// Preview computes (or fetches from cache) the preview of one stage, after
// its upstream stages'. A nil result with a nil error means the stage's kind
// has no preview.
func Preview(ctx context.Context, d *Doc, stageID string, cache *PreviewCache, opts PreviewOptions) (*PreviewData, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	planned := lenientPlan(d)
	target := planned[stageID]
	if target == nil {
		if s := d.StageByID(stageID); s != nil && KindByName(s.Kind) == nil {
			return nil, fmt.Errorf("stage %s: this build has no stage kind %q", stageID, s.Kind)
		}
		return nil, fmt.Errorf("stage %s: fix its stage refs to preview it (see Problems)", stageID)
	}
	// The chain: every stage the target depends on, in document order.
	need := map[string]bool{stageID: true}
	var mark func(id string)
	mark = func(id string) {
		for _, r := range planned[id].refs {
			if !need[r] {
				need[r] = true
				mark(r)
			}
		}
	}
	mark(stageID)
	done := map[string]*PreviewData{}
	for i := range d.Stages {
		id := d.Stages[i].ID
		p := planned[id]
		if !need[id] || p == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, ErrCancelled
		}
		if p.kind.Preview == nil {
			done[id] = nil
			continue
		}
		if c := cache.get(p.hash); c != nil {
			done[id] = c
			continue
		}
		label := id + " (" + p.kind.Name + ")"
		pc := &PreviewContext{
			Env: Env{Ctx: ctx, Doc: d, Stage: p.stage, Kind: p.kind, Params: p.params, Seed: p.seed,
				Workers: opts.Workers, logf: opts.Log},
			upstream: done,
		}
		if opts.Progress != nil {
			pc.progress = func(f float32) { opts.Progress(label, f) }
		}
		t0 := time.Now()
		data, err := previewStage(p.kind, pc)
		if err == nil && ctx.Err() != nil {
			err = ErrCancelled
		}
		if err != nil {
			if id == stageID {
				return nil, err
			}
			done[id] = nil // the target previews without it
			continue
		}
		if data != nil {
			data.Elapsed = time.Since(t0)
			cache.put(p.hash, data)
		}
		done[id] = data
	}
	return done[stageID], nil
}

func previewStage(k *StageKind, pc *PreviewContext) (data *PreviewData, err error) {
	defer func() {
		if r := recover(); r != nil {
			data, err = nil, fmt.Errorf("preview panicked: %v", r)
		}
	}()
	return k.Preview(pc)
}
