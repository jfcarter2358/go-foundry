// Package glyph is the music font the Score editor engraves with: Bravura,
// the SMuFL reference font, embedded whole.
//
// SMuFL (the Standard Music Font Layout) fixes three things a music font
// needs beyond its outlines, and this package reads all three:
//
//   - which code point each symbol lives at, by name: "noteheadBlack" is
//     U+E0A4 (bravura/glyphnames.json, SMuFL's own table, plus Bravura's
//     optional glyphs from its metadata);
//   - every glyph's bounding box and its anchors — where a stem joins a
//     notehead (stemUpSE, stemDownNW), where a flag meets its stem, where a
//     grace-note slash crosses — in staff spaces (bravura_metadata.json);
//   - the engraving defaults the font was drawn for: staff-line, stem,
//     beam, ledger-line and barline thicknesses, beam spacing, tie and slur
//     thicknesses (the same file).
//
// The unit throughout is the staff space, the distance between two staff
// lines; a SMuFL font is designed so one em is four of them (the height of
// a staff). SMuFL measures y upwards. This package turns every coordinate it
// hands out y-down, the screen's way and internal/score/engrave's: a point
// above the glyph's origin has a negative Y.
//
// One outline source feeds every backend: Outline for vector output (PDF in
// phase 5, the PNG rasteriser in engrave/raster) and Atlas (atlas.go) for
// the editor, which draws glyphs as textured quads.
//
// Bravura is © Steinberg Media Technologies GmbH under the SIL Open Font
// License 1.1 (bravura/OFL.txt, License); an editor install carries the
// license beside the binary (internal/pack, StageEditor).
package glyph

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/image/font/sfnt"

	"foundry/internal/score/glyph/bravura"
)

//go:embed bravura/Bravura.otf
var bravuraOTF []byte

//go:embed bravura/bravura_metadata.json
var bravuraMetadata []byte

//go:embed bravura/glyphnames.json
var smuflGlyphNames []byte

// License is Bravura's license, which travels with any copy of the font.
var License = bravura.License

// LicenseFileName is what an install calls the license file.
const LicenseFileName = bravura.LicenseFileName

// spacesPerEm is SMuFL's em: four staff spaces, a staff's height.
const spacesPerEm = 4

// Point is a position in staff spaces, y down.
type Point struct{ X, Y float64 }

// Add is p + q.
func (p Point) Add(q Point) Point { return Point{p.X + q.X, p.Y + q.Y} }

// Rect is a box in staff spaces, y down: Min is the top-left corner.
type Rect struct{ Min, Max Point }

// W is the box's width.
func (r Rect) W() float64 { return r.Max.X - r.Min.X }

// H is the box's height.
func (r Rect) H() float64 { return r.Max.Y - r.Min.Y }

// Empty reports a box with no area.
func (r Rect) Empty() bool { return r.Max.X <= r.Min.X || r.Max.Y <= r.Min.Y }

// Union is the box holding both; an empty box is no box.
func (r Rect) Union(o Rect) Rect {
	switch {
	case r.Empty():
		return o
	case o.Empty():
		return r
	}
	return Rect{
		Min: Point{math.Min(r.Min.X, o.Min.X), math.Min(r.Min.Y, o.Min.Y)},
		Max: Point{math.Max(r.Max.X, o.Max.X), math.Max(r.Max.Y, o.Max.Y)},
	}
}

// Offset moves the box.
func (r Rect) Offset(d Point) Rect { return Rect{r.Min.Add(d), r.Max.Add(d)} }

// Scale scales the box about the origin.
func (r Rect) Scale(s float64) Rect {
	return Rect{Point{r.Min.X * s, r.Min.Y * s}, Point{r.Max.X * s, r.Max.Y * s}}
}

// SegOp is what an outline segment does.
type SegOp uint8

const (
	MoveTo SegOp = iota
	LineTo
	QuadTo
	CubeTo
)

// Segment is one step of an outline: a move, a line, or a quadratic or
// cubic Bézier, with its points (1, 1, 2 or 3 of P) in staff spaces, y down.
type Segment struct {
	Op SegOp
	P  [3]Point
}

// Glyph is one SMuFL symbol.
type Glyph struct {
	Name string
	Rune rune
	// Advance is the advance width in staff spaces.
	Advance float64
	// Box is the ink's bounding box relative to the origin (y down).
	Box Rect
	// Anchors are SMuFL's named attachment points, relative to the origin
	// (y down): "stemUpSE", "stemDownNW", "graceNoteSlashSW" …
	Anchors map[string]Point
	// Outline is the glyph's contours relative to the origin.
	Outline []Segment

	index sfnt.GlyphIndex
}

// Anchor is a named anchor, and whether the glyph has it.
func (g *Glyph) Anchor(name string) (Point, bool) {
	p, ok := g.Anchors[name]
	return p, ok
}

// Defaults are SMuFL's engravingDefaults: line thicknesses and spacings in
// staff spaces, what the font's glyphs were drawn to sit beside.
type Defaults struct {
	StaffLineThickness         float64  `json:"staffLineThickness"`
	StemThickness              float64  `json:"stemThickness"`
	BeamThickness              float64  `json:"beamThickness"`
	BeamSpacing                float64  `json:"beamSpacing"`
	LegerLineThickness         float64  `json:"legerLineThickness"`
	LegerLineExtension         float64  `json:"legerLineExtension"`
	SlurEndpointThickness      float64  `json:"slurEndpointThickness"`
	SlurMidpointThickness      float64  `json:"slurMidpointThickness"`
	TieEndpointThickness       float64  `json:"tieEndpointThickness"`
	TieMidpointThickness       float64  `json:"tieMidpointThickness"`
	ThinBarlineThickness       float64  `json:"thinBarlineThickness"`
	ThickBarlineThickness      float64  `json:"thickBarlineThickness"`
	DashedBarlineThickness     float64  `json:"dashedBarlineThickness"`
	DashedBarlineDashLength    float64  `json:"dashedBarlineDashLength"`
	DashedBarlineGapLength     float64  `json:"dashedBarlineGapLength"`
	BarlineSeparation          float64  `json:"barlineSeparation"`
	ThinThickBarlineSeparation float64  `json:"thinThickBarlineSeparation"`
	RepeatBarlineDotSeparation float64  `json:"repeatBarlineDotSeparation"`
	BracketThickness           float64  `json:"bracketThickness"`
	SubBracketThickness        float64  `json:"subBracketThickness"`
	HairpinThickness           float64  `json:"hairpinThickness"`
	OctaveLineThickness        float64  `json:"octaveLineThickness"`
	PedalLineThickness         float64  `json:"pedalLineThickness"`
	RepeatEndingLineThickness  float64  `json:"repeatEndingLineThickness"`
	ArrowShaftThickness        float64  `json:"arrowShaftThickness"`
	LyricLineThickness         float64  `json:"lyricLineThickness"`
	TextEnclosureThickness     float64  `json:"textEnclosureThickness"`
	TupletBracketThickness     float64  `json:"tupletBracketThickness"`
	HBarThickness              float64  `json:"hBarThickness"`
	TextFontFamily             []string `json:"textFontFamily"`
}

// Font is a parsed SMuFL font with its metadata. Safe for concurrent use.
type Font struct {
	Name     string
	Version  string
	Defaults Defaults

	sf    *sfnt.Font
	cff   *cffFont
	upem  float64
	runes map[string]rune
	names map[rune]string
	boxes map[string]Rect
	adv   map[string]float64
	anch  map[string]map[string]Point

	mu     sync.Mutex
	glyphs map[string]*Glyph
	buf    sfnt.Buffer
}

var (
	bravuraOnce sync.Once
	bravuraFont *Font
	bravuraErr  error
)

// Bravura is the embedded font, parsed on first use. It panics if the
// embedded files are broken, which a test catches at build time.
func Bravura() *Font {
	bravuraOnce.Do(func() {
		bravuraFont, bravuraErr = Parse(bravuraOTF, bravuraMetadata, smuflGlyphNames)
	})
	if bravuraErr != nil {
		panic("glyph: embedded Bravura: " + bravuraErr.Error())
	}
	return bravuraFont
}

// rawMetadata is the part of a SMuFL font metadata file read here.
type rawMetadata struct {
	FontName           string                           `json:"fontName"`
	FontVersion        json.Number                      `json:"fontVersion"`
	EngravingDefaults  Defaults                         `json:"engravingDefaults"`
	GlyphAdvanceWidths map[string]float64               `json:"glyphAdvanceWidths"`
	GlyphBBoxes        map[string]rawBox                `json:"glyphBBoxes"`
	GlyphsWithAnchors  map[string]map[string][2]float64 `json:"glyphsWithAnchors"`
	OptionalGlyphs     map[string]rawCodepoint          `json:"optionalGlyphs"`
}

type rawBox struct {
	NE [2]float64 `json:"bBoxNE"`
	SW [2]float64 `json:"bBoxSW"`
}

type rawCodepoint struct {
	Codepoint string `json:"codepoint"`
}

// Parse reads a SMuFL font: its OpenType file, its metadata JSON and the
// SMuFL glyph-name table (name → code point).
func Parse(otf, metadata, glyphNames []byte) (*Font, error) {
	sf, err := sfnt.Parse(otf)
	if err != nil {
		return nil, fmt.Errorf("font: %w", err)
	}
	cf, err := parseCFF(otf)
	if err != nil {
		return nil, err
	}
	var md rawMetadata
	if err := json.Unmarshal(metadata, &md); err != nil {
		return nil, fmt.Errorf("metadata: %w", err)
	}
	var names map[string]rawCodepoint
	if err := json.Unmarshal(glyphNames, &names); err != nil {
		return nil, fmt.Errorf("glyph names: %w", err)
	}
	f := &Font{
		Name: md.FontName, Version: md.FontVersion.String(), Defaults: md.EngravingDefaults,
		sf: sf, cff: cf, upem: float64(sf.UnitsPerEm()),
		runes: map[string]rune{}, names: map[rune]string{},
		boxes: map[string]Rect{}, adv: md.GlyphAdvanceWidths, anch: map[string]map[string]Point{},
		glyphs: map[string]*Glyph{},
	}
	add := func(name, cp string) error {
		r, err := parseCodepoint(cp)
		if err != nil {
			return fmt.Errorf("glyph %s: %w", name, err)
		}
		f.runes[name] = r
		if _, dup := f.names[r]; !dup {
			f.names[r] = name
		}
		return nil
	}
	for n, c := range names {
		if err := add(n, c.Codepoint); err != nil {
			return nil, err
		}
	}
	for n, c := range md.OptionalGlyphs {
		if err := add(n, c.Codepoint); err != nil {
			return nil, err
		}
	}
	for n, b := range md.GlyphBBoxes {
		// SMuFL's north-east corner is the top right: y up becomes y down.
		f.boxes[n] = Rect{Min: Point{b.SW[0], -b.NE[1]}, Max: Point{b.NE[0], -b.SW[1]}}
	}
	for n, as := range md.GlyphsWithAnchors {
		m := make(map[string]Point, len(as))
		for k, p := range as {
			m[k] = Point{p[0], -p[1]}
		}
		f.anch[n] = m
	}
	if f.upem <= 0 {
		return nil, fmt.Errorf("font: no units per em")
	}
	return f, nil
}

func parseCodepoint(s string) (rune, error) {
	h, ok := strings.CutPrefix(s, "U+")
	if !ok {
		return 0, fmt.Errorf("bad code point %q", s)
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("bad code point %q", s)
	}
	return rune(v), nil
}

// Codepoint is the code point of a SMuFL glyph name.
func (f *Font) Codepoint(name string) (rune, bool) {
	r, ok := f.runes[name]
	return r, ok
}

// NameOf is the SMuFL name of a code point ("" if it has none).
func (f *Font) NameOf(r rune) string { return f.names[r] }

// Names are every glyph name the font draws, sorted.
func (f *Font) Names() []string {
	out := make([]string, 0, len(f.boxes))
	for n := range f.boxes {
		if _, ok := f.runes[n]; ok {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// Box is a glyph's bounding box from the metadata, without loading its
// outline. ok is false for a name the font does not have.
func (f *Font) Box(name string) (Rect, bool) {
	b, ok := f.boxes[name]
	return b, ok
}

// Width is a glyph's ink width (zero for an unknown name).
func (f *Font) Width(name string) float64 { return f.boxes[name].W() }

// Advance is a glyph's advance width.
func (f *Font) Advance(name string) float64 {
	if a, ok := f.adv[name]; ok {
		return a
	}
	return f.boxes[name].Max.X
}

// Anchor is a glyph's named anchor, without loading its outline.
func (f *Font) Anchor(glyph, anchor string) (Point, bool) {
	p, ok := f.anch[glyph][anchor]
	return p, ok
}

// Glyph loads a glyph by SMuFL name, with its outline. Glyphs are cached;
// the returned value is shared and must not be changed.
func (f *Font) Glyph(name string) (*Glyph, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if g, ok := f.glyphs[name]; ok {
		return g, nil
	}
	r, ok := f.runes[name]
	if !ok {
		return nil, fmt.Errorf("glyph: no SMuFL glyph %q", name)
	}
	idx, err := f.sf.GlyphIndex(&f.buf, r)
	if err != nil || idx == 0 {
		return nil, fmt.Errorf("glyph: %s (U+%04X) is not in %s", name, r, f.Name)
	}
	segs, err := f.cff.glyph(int(idx))
	if err != nil {
		return nil, fmt.Errorf("glyph: %s: %w", name, err)
	}
	// Font units, y up, to staff spaces, y down.
	scale := spacesPerEm / f.upem
	g := &Glyph{Name: name, Rune: r, index: idx, Anchors: f.anch[name], Outline: make([]Segment, len(segs))}
	ink := Rect{}
	first := true
	for i, s := range segs {
		for k := 0; k < segPoints(s.Op); k++ {
			p := Point{X: s.P[k].X * scale, Y: -s.P[k].Y * scale}
			s.P[k] = p
			if first {
				ink, first = Rect{p, p}, false
			} else {
				ink.Min.X, ink.Min.Y = math.Min(ink.Min.X, p.X), math.Min(ink.Min.Y, p.Y)
				ink.Max.X, ink.Max.Y = math.Max(ink.Max.X, p.X), math.Max(ink.Max.Y, p.Y)
			}
		}
		g.Outline[i] = s
	}
	if b, ok := f.boxes[name]; ok {
		g.Box = b
	} else {
		// No metadata box: the control points' hull is a fair stand-in.
		g.Box = ink
	}
	g.Advance = f.Advance(name)
	if g.Anchors == nil {
		g.Anchors = map[string]Point{}
	}
	f.glyphs[name] = g
	return g, nil
}

// segPoints is how many of a segment's points it uses.
func segPoints(op SegOp) int {
	switch op {
	case QuadTo:
		return 2
	case CubeTo:
		return 3
	}
	return 1
}

// MustGlyph is Glyph for the names the engraver uses, which a test checks
// are all in the font.
func (f *Font) MustGlyph(name string) *Glyph {
	g, err := f.Glyph(name)
	if err != nil {
		panic(err)
	}
	return g
}
