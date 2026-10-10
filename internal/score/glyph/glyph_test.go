package glyph

import (
	"math"
	"testing"

	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// EngraverGlyphs are the names internal/score/engrave draws. Every one must
// be in the font and load.
var EngraverGlyphs = []string{
	"noteheadDoubleWhole", "noteheadWhole", "noteheadHalf", "noteheadBlack",
	"gClef", "fClef", "cClef", "gClef8vb", "gClef8va", "fClef8vb", "fClef8va", "unpitchedPercussionClef1",
	"accidentalSharp", "accidentalFlat", "accidentalNatural", "accidentalDoubleSharp", "accidentalDoubleFlat",
	"accidentalParensLeft", "accidentalParensRight",
	"timeSig0", "timeSig1", "timeSig2", "timeSig3", "timeSig4", "timeSig5", "timeSig6", "timeSig7", "timeSig8", "timeSig9",
	"timeSigPlusSmall", "timeSigCommon", "timeSigCutCommon",
	"flag8thUp", "flag16thUp", "flag32ndUp", "flag64thUp", "flag128thUp", "flag256thUp",
	"flag8thDown", "flag16thDown", "flag32ndDown", "flag64thDown", "flag128thDown", "flag256thDown",
	"restDoubleWhole", "restWhole", "restHalf", "restQuarter", "rest8th", "rest16th", "rest32nd", "rest64th", "rest128th", "rest256th",
	"augmentationDot", "brace",
	"tuplet0", "tuplet1", "tuplet2", "tuplet3", "tuplet4", "tuplet5", "tuplet6", "tuplet7", "tuplet8", "tuplet9", "tupletColon",
}

func TestEngraverGlyphsLoad(t *testing.T) {
	f := Bravura()
	for _, n := range EngraverGlyphs {
		g, err := f.Glyph(n)
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		if len(g.Outline) == 0 || g.Box.Empty() {
			t.Errorf("%s: empty outline or box", n)
		}
	}
}

func TestEveryGlyphLoads(t *testing.T) {
	f := Bravura()
	names := f.Names()
	if len(names) < 3000 {
		t.Fatalf("only %d glyph names", len(names))
	}
	for _, n := range names {
		if _, err := f.Glyph(n); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
}

// TestCFFMatchesSFNT checks the charstring interpreter against sfnt's on
// every glyph sfnt can read (all but the ones drawn with flex).
func TestCFFMatchesSFNT(t *testing.T) {
	f := Bravura()
	var buf sfnt.Buffer
	compared := 0
	for _, n := range f.Names() {
		r, _ := f.Codepoint(n)
		idx, err := f.sf.GlyphIndex(&buf, r)
		if err != nil || idx == 0 {
			continue
		}
		want, err := f.sf.LoadGlyph(&buf, idx, fixed.I(int(f.upem)), nil)
		if err != nil {
			continue
		}
		got, err := f.cff.glyph(int(idx))
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		// sfnt closes nothing explicitly either; compare point for point
		// (it rounds to 1/64 of a unit and is y down).
		if len(got) != len(want) {
			t.Fatalf("%s: %d segments, sfnt %d", n, len(got), len(want))
		}
		for i := range got {
			k := segPoints(got[i].Op)
			for j := 0; j < k; j++ {
				wx := float64(want[i].Args[j].X) / 64
				wy := -float64(want[i].Args[j].Y) / 64
				if math.Abs(got[i].P[j].X-wx) > 0.02 || math.Abs(got[i].P[j].Y-wy) > 0.02 {
					t.Fatalf("%s: segment %d point %d: %v, sfnt (%g,%g)", n, i, j, got[i].P[j], wx, wy)
				}
			}
		}
		compared++
	}
	if compared < 3000 {
		t.Errorf("compared only %d glyphs", compared)
	}
}

func TestMetricsAreYDown(t *testing.T) {
	f := Bravura()
	g := f.MustGlyph("gClef")
	// The G clef rises 4.392 spaces above its origin (the G line) and
	// falls 2.632 below.
	if math.Abs(g.Box.Min.Y+4.392) > 1e-9 || math.Abs(g.Box.Max.Y-2.632) > 1e-9 {
		t.Errorf("gClef box %v", g.Box)
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, s := range g.Outline {
		for k := 0; k < segPoints(s.Op); k++ {
			lo, hi = math.Min(lo, s.P[k].Y), math.Max(hi, s.P[k].Y)
		}
	}
	if lo > -4.2 || hi < 2.4 {
		t.Errorf("gClef outline spans %g..%g, want about -4.39..2.63", lo, hi)
	}
	a, ok := f.Anchor("noteheadBlack", "stemUpSE")
	if !ok || math.Abs(a.X-1.18) > 1e-9 || math.Abs(a.Y+0.168) > 1e-9 {
		t.Errorf("stemUpSE %v %v", a, ok)
	}
	if d := f.Defaults; d.StaffLineThickness != 0.13 || d.StemThickness != 0.12 || d.BeamThickness != 0.5 {
		t.Errorf("defaults %+v", d)
	}
	if r, ok := f.Codepoint("noteheadBlack"); !ok || r != 0xE0A4 || f.NameOf(0xE0A4) != "noteheadBlack" {
		t.Errorf("noteheadBlack is %U", r)
	}
	if _, ok := f.Codepoint("braceLarge"); !ok {
		t.Error("optional glyphs are not named")
	}
}

func TestAtlasBakes(t *testing.T) {
	a := NewAtlas(Bravura(), 10)
	g, ok := a.Glyph("noteheadBlack", 1)
	if !ok || g.Rev == 0 {
		t.Fatal("noteheadBlack did not bake")
	}
	// The quad holds the ink: 1.18 × 1 spaces plus the pixel margin.
	if g.Quad.Min.X > 0 || g.Quad.Max.X < 1.18 || g.Quad.Min.Y > -0.5 || g.Quad.Max.Y < 0.5 {
		t.Errorf("quad %v", g.Quad)
	}
	// The middle of the head is covered, the page's corner is not.
	x := int((float64(g.U0)+float64(g.U1))/2*float64(a.W)) + 0
	y := int((float64(g.V0) + float64(g.V1)) / 2 * float64(a.H))
	if c := a.Pix.RGBAAt(x, y); c.A < 200 || c.R != 255 {
		t.Errorf("head centre %v", c)
	}
	if a.Pix.RGBAAt(0, 0).A != 0 {
		t.Error("the page's corner is inked")
	}
	small, _ := a.Glyph("noteheadBlack", 0.66)
	if small.Quad.W() >= g.Quad.W() {
		t.Error("a grace-size head is not smaller")
	}
	again, _ := a.Glyph("noteheadBlack", 1)
	if again != g {
		t.Error("a second ask baked again")
	}
	// Growing keeps every glyph and bumps the revision.
	rev := a.Rev
	for _, n := range EngraverGlyphs {
		for _, s := range []float64{1, 0.66, 2, 3} {
			if _, ok := a.Glyph(n, s); !ok {
				t.Fatalf("%s at %g did not bake", n, s)
			}
		}
	}
	if a.Rev <= rev || a.W < 512 {
		t.Errorf("rev %d page %d", a.Rev, a.W)
	}
	if _, ok := a.Glyph("noSuchGlyph", 1); ok {
		t.Error("an unknown glyph baked")
	}
}

func TestZoomBuckets(t *testing.T) {
	if b := ZoomBucket(10); math.Abs(b-math.Exp2(math.Round(math.Log2(10)*4)/4)) > 1e-12 {
		t.Errorf("bucket %g", b)
	}
	if ZoomBucket(10.2) != ZoomBucket(10) {
		t.Error("a 2% zoom changed bucket")
	}
	if ZoomBucket(1000) != MaxPxPerSpace || ZoomBucket(0.1) != MinPxPerSpace {
		t.Error("buckets are not clamped")
	}
	s := NewAtlases(Bravura())
	if s.For(10) != s.For(10.1) || s.For(10) == s.For(20) || len(s.Pages()) != 2 {
		t.Error("pages are not per bucket")
	}
}

func TestLicenseEmbedded(t *testing.T) {
	if len(License) < 1000 || !contains(License, "SIL Open Font License") {
		t.Error("the OFL is not embedded")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
