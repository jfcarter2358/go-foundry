package world

// layer.go — authoring layers ("levels") within a single chunk.
//
// A layer is how one chunk holds sectors that overlap in XZ: a cliff with an
// overhang, a mezzanine over a hall, a bridge over the ground it crosses.
// Before layers the only way to stack two sectors at the same XZ was to put
// them in different chunks, because every lookup that resolved a position to a
// sector took the first polygon that contained it and had no way to tell two
// stacked candidates apart.
//
// What actually unblocked that is the vertical sector choice in spatial.go,
// which ranks the candidates under a point by their floors rather than taking
// the first. A layer is deliberately NOT part of that decision: containment is
// geometry, and a sector that spans two bands (a ramp, a sloped terrace) would
// be mis-resolved by anything that read the band as authoritative.
//
// So Sector.Layer is an AUTHORING band, and it does exactly two jobs:
//
//   - it filters the 2D map, so overlapping footprints do not become an
//     unpickable pile of coincident lines, and
//   - it supplies BaseY/CeilY as the starting heights for a sector drawn on it.
//
// Nothing at runtime — collision, entity location, scripts, rendering — reads
// it. A level whose layers were all set to nonsense would look and play exactly
// the same; only editing it would be unpleasant.
//
// Layer 0 is the ground layer and is what every sector gets by default, which
// is why levels authored before this exist load unchanged.

import "strconv"

// DefaultLayerHeight is the vertical size of a synthesized layer band — the
// floor-to-ceiling height a sector drawn on a fresh layer starts with, and the
// spacing between bands that have no metadata of their own.
const DefaultLayerHeight = float32(4)

// SlabThickness is the gap AddLayer leaves between a storey's ceiling and the
// floor of the storey above it — the thickness of the slab between them.
//
// It is not a cosmetic margin. Two coplanar surfaces cannot both be drawn
// correctly: opaque world geometry is rendered with face culling off (see
// app.go), so a ceiling and a floor at the same height both rasterize and
// z-fight, and no draw order fixes it because each is the right answer from one
// side and the wrong one from the other. Giving the slab a thickness is what
// makes the two separate surfaces they already are.
const SlabThickness = float32(0.2)

// ChunkLayer is one authoring band's metadata: a name for the editor's layer
// selector, and the floor/ceiling heights a sector drawn on the band starts at.
//
// Bands are advisory. Nothing forces a sector to stay inside its layer's
// BaseY/CeilY — a stair spanning two floors is supposed to leave, and the
// runtime never consults the band anyway. The editor flags a sector that has
// drifted well outside its band, rather than preventing it.
type ChunkLayer struct {
	Name  string
	BaseY float32 // starting Floor.Height for a sector drawn on this layer
	CeilY float32 // starting Ceiling.Height for a sector drawn on this layer
}

// LayerCount is how many layers the chunk has: enough to cover every stored
// ChunkLayer and every layer index some sector or biome sector actually claims,
// and never fewer than one (the ground layer always exists, even in an empty
// chunk).
//
// Derived rather than stored because a sector's Layer is the durable fact and
// the ChunkLayer list is metadata that may simply be absent — an old save has
// no layers block at all, and a chunk whose only layer is the ground one has no
// reason to write one.
func (ch *Chunk) LayerCount() int {
	n := len(ch.Layers)
	for i := range ch.Sectors {
		if l := ch.Sectors[i].Layer; l+1 > n {
			n = l + 1
		}
	}
	for i := range ch.BiomeSectors {
		if l := ch.BiomeSectors[i].Layer; l+1 > n {
			n = l + 1
		}
	}
	if n < 1 {
		return 1
	}
	return n
}

// LayerAt returns layer i's band, synthesizing one for a layer that carries no
// metadata of its own.
//
// The synthesis reads the band off the sectors already on the layer, so a level
// that has never been through the layer UI still gets sensible starting heights
// when something new is drawn on it — a second layer authored at y=12 offers
// y=12, not a made-up multiple of DefaultLayerHeight. Only a layer that is
// genuinely empty falls back to stacking bands by index.
func (ch *Chunk) LayerAt(i int) ChunkLayer {
	if i < 0 {
		i = 0
	}
	if i < len(ch.Layers) {
		l := ch.Layers[i]
		if l.Name == "" {
			l.Name = DefaultLayerName(i)
		}
		if l.CeilY <= l.BaseY {
			l.CeilY = l.BaseY + DefaultLayerHeight
		}
		return l
	}
	if lo, hi, ok := ch.layerSectorBand(i); ok {
		return ChunkLayer{Name: DefaultLayerName(i), BaseY: lo, CeilY: hi}
	}
	base := float32(i) * DefaultLayerHeight
	return ChunkLayer{Name: DefaultLayerName(i), BaseY: base, CeilY: base + DefaultLayerHeight}
}

// layerSectorBand is the floor-to-ceiling extent actually occupied by the
// sectors on layer i, or ok=false when the layer holds none.
func (ch *Chunk) layerSectorBand(i int) (lo, hi float32, ok bool) {
	for si := range ch.Sectors {
		s := &ch.Sectors[si]
		if s.Layer != i {
			continue
		}
		if !ok {
			lo, hi, ok = s.Floor.Height, s.Ceiling.Height, true
			continue
		}
		if s.Floor.Height < lo {
			lo = s.Floor.Height
		}
		if s.Ceiling.Height > hi {
			hi = s.Ceiling.Height
		}
	}
	if ok && hi <= lo {
		hi = lo + DefaultLayerHeight
	}
	return lo, hi, ok
}

// DefaultLayerName is what an unnamed layer is called in the editor.
func DefaultLayerName(i int) string {
	if i == 0 {
		return "Ground"
	}
	return "Layer " + strconv.Itoa(i)
}

// EnsureLayers materializes stored metadata for every layer up to n-1, so the
// editor can edit a band that was previously only implied. Existing entries are
// left alone; new ones take whatever LayerAt was already synthesizing for them,
// which is what makes materializing a no-op as far as the level is concerned.
func (ch *Chunk) EnsureLayers(n int) {
	if n > len(ch.Layers) {
		// LayerAt reads ch.Layers for indices below len, so grow one at a time:
		// each synthesized band is derived with the earlier ones already in place.
		for i := len(ch.Layers); i < n; i++ {
			ch.Layers = append(ch.Layers, ch.LayerAt(i))
		}
	}
}

// AddLayer appends a new layer above the topmost one and returns its index.
//
// The new band starts one slab above where the one below it ends, rather than
// exactly on it. Sitting the new floor directly on the ceiling below makes the
// two coplanar, which is the one arrangement the renderer cannot resolve — see
// SlabThickness.
func (ch *Chunk) AddLayer() int {
	n := ch.LayerCount()
	ch.EnsureLayers(n)
	below := ch.LayerAt(n - 1)
	base := below.CeilY + SlabThickness
	ch.Layers = append(ch.Layers, ChunkLayer{
		Name:  DefaultLayerName(n),
		BaseY: base,
		CeilY: base + DefaultLayerHeight,
	})
	return n
}

// RemoveLayer deletes layer i, moving everything on it down onto the layer
// below (or onto the ground layer, when i is 0) and shifting the layers above
// it down by one. Geometry is never moved — only its band assignment changes,
// so removing a layer is an editing-organisation edit, not a destructive one.
//
// The ground layer's metadata can be removed but the layer itself cannot: a
// chunk always has at least one band for its sectors to live on.
func (ch *Chunk) RemoveLayer(i int) {
	if i < 0 || i >= ch.LayerCount() || ch.LayerCount() <= 1 {
		return
	}
	merged := i - 1
	if merged < 0 {
		merged = 0
	}
	remap := func(l int) int {
		switch {
		case l == i:
			return merged
		case l > i:
			return l - 1
		default:
			return l
		}
	}
	for si := range ch.Sectors {
		ch.Sectors[si].Layer = remap(ch.Sectors[si].Layer)
	}
	for bi := range ch.BiomeSectors {
		ch.BiomeSectors[bi].Layer = remap(ch.BiomeSectors[bi].Layer)
	}
	if i < len(ch.Layers) {
		ch.Layers = append(ch.Layers[:i], ch.Layers[i+1:]...)
	}
}

// ClampLayer brings a layer index into the chunk's valid range. Used wherever
// an index arrives from outside the chunk — a save file, the editor's active
// layer after the chunk changed underneath it.
func (ch *Chunk) ClampLayer(i int) int {
	if i < 0 {
		return 0
	}
	if n := ch.LayerCount(); i >= n {
		return n - 1
	}
	return i
}
