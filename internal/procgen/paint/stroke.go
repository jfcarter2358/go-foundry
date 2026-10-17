package paint

// stroke.go — painting: a circular stamp, and a line of them.
//
// A Stroke is one stamp of the brush at a world point: a disc of Radius
// metres whose weight is 1 across its core and eases (smoothstep) to 0 at
// the rim over the outer Falloff fraction of the radius (0: a hard edge,
// 1: a cone-like blob with no flat core). Each texel whose centre the disc
// covers is changed by Strength × that weight, by the mode:
//
//	add     unit kinds: v += s (to at most 1); height: v += s metres;
//	        direction: v moves toward Dir by s
//	erase   v moves toward 0 by s (v *= 1 - s)
//	set     v moves toward Value (direction: toward Dir) by s
//	smooth  v moves toward the mean of its 3×3 neighbourhood by s, taken
//	        from the layer as it was before the stamp
//	flatten (height) v moves toward Value − Base(x, z) by s: the ground
//	        under the layer is brought to the absolute height Value, which
//	        the editor samples under the cursor as the stroke starts (a
//	        path's bed, a terrace). Base is the ground the layer is added
//	        to; without one, flatten is set toward Value.
//
// where s is Strength × the weight there. Height's add takes Strength in
// metres (the stamp's centre rises that much); every other mode and kind
// reads Strength as 0..1.
//
// Every written value is quantised to the file's precision, and a tile the
// stamp leaves all zero is deleted, so erasing a patch away leaves no file.
// A brush dragged across the view is a line of stamps (ApplyLine) spaced a
// fraction of the radius apart; the editor and `foundry gen paint` both
// paint through it.

import (
	"math"
)

// Mode is how a stroke changes the texels under it.
type Mode int

const (
	ModeAdd Mode = iota
	ModeErase
	ModeSmooth
	ModeSet
	// ModeRaise adds as ModeAdd does but never past the stroke's Value and
	// never lowering a texel already above it: a brush preset's layer
	// painted "to 0.6" (procgen/stages paint_preset.go) reaches 0.6 however
	// many stamps overlap, and leaves a stronger stroke alone.
	ModeRaise
	// ModeFlatten moves a height layer toward the offset that brings the
	// ground to Value metres (Value − Base at each texel): see the file
	// comment. Phase W2 (docs/terrain-paint/PHASE_W2.md).
	ModeFlatten
)

// ModeNames are the modes' spellings, in Mode order.
var ModeNames = []string{"add", "erase", "smooth", "set", "raise", "flatten"}

// ParseMode is a mode by name.
func ParseMode(s string) (Mode, bool) {
	for i, n := range ModeNames {
		if n == s {
			return Mode(i), true
		}
	}
	return 0, false
}

func (m Mode) String() string {
	if int(m) >= 0 && int(m) < len(ModeNames) {
		return ModeNames[m]
	}
	return "mode?"
}

// Stroke is one stamp of the brush.
type Stroke struct {
	// X and Z are the centre (world metres); Radius its reach.
	X, Z, Radius float64
	// Strength scales the change (0..1; metres for height's add).
	Strength float64
	// Falloff is the outer fraction of the radius the weight eases out
	// over: 0 hard, 1 soft all the way in.
	Falloff float64
	Mode    Mode
	// Value is what ModeSet moves toward (a unit value, or metres).
	Value float64
	// Dir is the vector a direction layer's add and set move toward.
	Dir [2]float64
	// Curve is the falloff's profile across the eased ring: CurveSmooth
	// (the zero value, smoothstep), CurveLinear, CurveSharp (a peaked
	// t², a pen pressed lightly) or CurveDome (a full 1 − (1 − t)², a
	// pen pressed hard).
	Curve Curve
	// Base is the ground height under world (x, z) that a height layer is
	// added to: what ModeFlatten levels against. Nil reads 0.
	Base func(x, z float64) float64
}

// Curve is a brush falloff profile.
type Curve int

const (
	CurveSmooth Curve = iota
	CurveLinear
	CurveSharp
	CurveDome
)

// CurveNames are the curves' spellings, in Curve order.
var CurveNames = []string{"smooth", "linear", "sharp", "dome"}

// ParseCurve is a curve by name.
func ParseCurve(s string) (Curve, bool) {
	for i, n := range CurveNames {
		if n == s {
			return Curve(i), true
		}
	}
	return 0, false
}

func (c Curve) String() string {
	if int(c) >= 0 && int(c) < len(CurveNames) {
		return CurveNames[c]
	}
	return "curve?"
}

// shape is the curve at t, 0 at the rim to 1 at the core's edge.
func (c Curve) shape(t float64) float64 {
	switch c {
	case CurveLinear:
		return t
	case CurveSharp:
		return t * t
	case CurveDome:
		return 1 - (1-t)*(1-t)
	}
	return t * t * (3 - 2*t)
}

// weight is the brush's weight at distance d from the centre.
func (s *Stroke) weight(d float64) float64 {
	r := s.Radius
	if d >= r || r <= 0 {
		return 0
	}
	f := math.Max(0, math.Min(1, s.Falloff))
	core := r * (1 - f)
	if d <= core {
		return 1
	}
	t := (r - d) / (r - core)
	return s.Curve.shape(t)
}

// Apply stamps the stroke into the layer and returns the keys of the tiles
// it may have changed (created, written or deleted), in key order: what an
// undo step snapshots BEFORE the stamp (TouchedBy answers the same keys
// without painting).
func (l *Layer) Apply(s Stroke) []TileKey {
	keys := l.TouchedBy(s)
	if len(keys) == 0 || s.Strength == 0 {
		return keys
	}
	n := l.Def.TexelsPerTile()
	c := l.Def.Channels()
	r := l.Def.Res()
	gi0 := int(math.Floor((s.X-s.Radius)/r - 0.5))
	gi1 := int(math.Ceil((s.X+s.Radius)/r - 0.5))
	gj0 := int(math.Floor((s.Z-s.Radius)/r - 0.5))
	gj1 := int(math.Ceil((s.Z+s.Radius)/r - 0.5))
	w, h := gi1-gi0+1, gj1-gj0+1
	// The smooth's neighbourhood reads the layer before this stamp.
	var before []float64
	if s.Mode == ModeSmooth {
		before = make([]float64, (w+2)*(h+2)*c)
		for j := -1; j <= h; j++ {
			for i := -1; i <= w; i++ {
				for ch := 0; ch < c; ch++ {
					before[((j+1)*(w+2)+(i+1))*c+ch] = l.texel(gi0+i, gj0+j, ch)
				}
			}
		}
	}
	var cur *Tile
	curKey := TileKey{math.MinInt, math.MinInt}
	for j := 0; j < h; j++ {
		gj := gj0 + j
		cz := (float64(gj) + 0.5) * r
		for i := 0; i < w; i++ {
			gi := gi0 + i
			cx := (float64(gi) + 0.5) * r
			wt := s.weight(math.Hypot(cx-s.X, cz-s.Z))
			if wt <= 0 {
				continue
			}
			k := TileKey{floorDiv(gi, n), floorDiv(gj, n)}
			if k != curKey {
				cur, curKey = l.writable(k), k
			}
			li, lj := gi-k.I*n, gj-k.J*n
			base := (lj*n + li) * c
			amt := s.Strength * wt
			for ch := 0; ch < c; ch++ {
				v := float64(cur.V[base+ch])
				var nv float64
				switch s.Mode {
				case ModeAdd:
					switch l.Def.Kind {
					case KindHeight:
						nv = v + amt
					case KindDirection:
						nv = v + (s.Dir[ch]-v)*math.Min(amt, 1)
					default:
						nv = v + amt
					}
				case ModeErase:
					nv = v * (1 - math.Min(amt, 1))
				case ModeRaise:
					nv = v
					if v < s.Value {
						nv = math.Min(s.Value, v+amt)
					}
				case ModeFlatten:
					target := s.Value
					if s.Base != nil {
						target -= s.Base(cx, cz)
					}
					nv = v + (target-v)*math.Min(amt, 1)
				case ModeSet:
					target := s.Value
					if l.Def.Kind == KindDirection {
						target = s.Dir[ch]
					}
					nv = v + (target-v)*math.Min(amt, 1)
				case ModeSmooth:
					sum := 0.0
					for dj := 0; dj < 3; dj++ {
						for di := 0; di < 3; di++ {
							sum += before[((j+dj)*(w+2)+(i+di))*c+ch]
						}
					}
					nv = v + (sum/9-v)*math.Min(amt, 1)
				default:
					nv = v
				}
				q := l.Def.Quantize(nv)
				// A small erase or smooth step can round back to where it
				// started and never reach 0: step one quantum toward the
				// target instead, so erasing always finishes.
				if q == l.Def.Quantize(v) && nv != v && (s.Mode == ModeErase || s.Mode == ModeSmooth || s.Mode == ModeFlatten) {
					q = stepToward(l.Def, v, nv)
				}
				cur.V[base+ch] = float32(q)
			}
		}
	}
	l.rev++
	l.prune(keys)
	return keys
}

// stepToward is one quantum from v in the direction of target.
func stepToward(d LayerDef, v, target float64) float64 {
	q := 1.0 / 255
	switch d.Kind {
	case KindHeight:
		q = 1.0 / 256
	case KindDirection:
		q = 1.0 / 127
	}
	if math.Abs(target-v) < q/2 {
		return d.Quantize(v)
	}
	if target < v {
		return d.Quantize(v - q)
	}
	return d.Quantize(v + q)
}

// TouchedBy is the keys of the tiles a stamp of s reaches, in key order.
func (l *Layer) TouchedBy(s Stroke) []TileKey {
	if l == nil || s.Radius <= 0 {
		return nil
	}
	pad := l.Def.Res() // the smooth reads one texel beyond; harmless otherwise
	return TilesOverlapping(s.X-s.Radius-pad, s.Z-s.Radius-pad, s.X+s.Radius+pad, s.Z+s.Radius+pad)
}

// LineStamps is the stamp centres along a segment from (x0, z0) to (x1,
// z1), spacing metres apart, both ends included.
func LineStamps(x0, z0, x1, z1, spacing float64) [][2]float64 {
	d := math.Hypot(x1-x0, z1-z0)
	if spacing <= 0 {
		spacing = 1
	}
	n := int(math.Ceil(d / spacing))
	out := make([][2]float64, 0, n+1)
	for i := 0; i <= n; i++ {
		t := 0.0
		if n > 0 {
			t = float64(i) / float64(n)
		}
		out = append(out, [2]float64{x0 + (x1-x0)*t, z0 + (z1-z0)*t})
	}
	return out
}

// ApplyLine stamps s along the segment from (x0, z0) to (x1, z1), a quarter
// of the radius apart, and returns every tile it touched, in key order.
// For the editor's drag and the CLI's lines.
func (l *Layer) ApplyLine(s Stroke, x0, z0, x1, z1 float64) []TileKey {
	seen := map[TileKey]bool{}
	var out []TileKey
	for _, p := range LineStamps(x0, z0, x1, z1, math.Max(s.Radius*0.25, l.Def.Res())) {
		st := s
		st.X, st.Z = p[0], p[1]
		for _, k := range l.Apply(st) {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sortKeys(out)
	return out
}

func sortKeys(keys []TileKey) {
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j].Less(keys[j-1]); j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
}
