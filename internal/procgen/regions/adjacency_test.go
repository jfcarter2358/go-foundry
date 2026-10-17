package regions

// adjacency_test.go — phase 5's adjacency rules: the override file, pair
// widths, finding touching pairs, and inserting an intermediate region that
// separates a forbidden one.

import (
	"math"
	"strings"
	"testing"
)

func TestAdjacencyFileOverridesTheTable(t *testing.T) {
	f, err := ParseAdjacencyFile([]byte(`
pairs:
  - between: [desert_canyon, savannah]
    transition: 250
  - between: [desert_canyon, savannah]
    forbidden: true
    intermediate: steppe
    note: no grass off the rim
  - between: [coast, "*"]
    remove: true
  - between: [marsh, savannah]
    transition: 80
`))
	if err != nil {
		t.Fatal(err)
	}
	a := f.Apply(DefaultAdjacency())
	if r, _ := a.Lookup(EnvSavannah, EnvDesertCanyon); !r.Forbidden || r.Intermediate != "steppe" || r.Transition != 0 {
		t.Errorf("desert/savannah = %+v: the later entry replaces the rule wholesale", r)
	}
	if _, ok := a.Lookup(EnvCoast, EnvAlpine); ok {
		t.Error("the removed coast/any rule still applies")
	}
	if r, ok := a.Lookup("savannah", "marsh"); !ok || r.Transition != 80 {
		t.Errorf("a new pair = %+v %v", r, ok)
	}
	if r, _ := DefaultAdjacency().Lookup(EnvSavannah, EnvDesertCanyon); r.Transition != 200 {
		t.Error("Apply changed the table it was given")
	}
	// The listing round-trips.
	enc, err := a.Encode()
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseAdjacencyFile(enc)
	if err != nil {
		t.Fatalf("the listing does not parse back: %v\n%s", err, enc)
	}
	if got := back.Apply(NewAdjacency()); len(got.Pairs()) != len(a.Pairs()) {
		t.Errorf("round trip has %d pairs, want %d", len(got.Pairs()), len(a.Pairs()))
	}
	for _, bad := range []string{
		"pairs: [{between: [a]}]",
		"pairs: [{between: [a, b], transition: -1}]",
		"pairs: [{between: [a, b], remove: true, transition: 5}]",
		"pairs: [{between: [a, b], intermediate: c}]",
		"pairs: [{between: [a, b], widht: 5}]",
	} {
		if _, err := ParseAdjacencyFile([]byte(bad)); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// desertSavannah is two default-transition squares sharing x = 0.
func desertSavannah(t *testing.T, adj *Adjacency, w Warp) *Set {
	rs := []Region{
		{ID: "a_west", Environment: EnvDesertCanyon, Footprint: square(-400, 0, 400)},
		{ID: "b_east", Environment: EnvSavannah, Footprint: square(400, 0, 400)},
	}
	ResolvePairTransitions(rs, adj)
	return mustSet(t, rs, w)
}

func TestPairTransitionUsesTheTablesWidth(t *testing.T) {
	s := desertSavannah(t, DefaultAdjacency(), Warp{})
	if got := s.Regions[0].PairTransitions[EnvSavannah]; got != 200 {
		t.Fatalf("desert's width against savannah = %v, want the table's 200", got)
	}
	// The band is 200 m: 0 at x = -100 for the savannah, 1 at +100.
	if w := s.Weight(1, -101, 0); w != 0 {
		t.Errorf("savannah share 101 m inside the desert = %v, want 0 (half of a 200 m band)", w)
	}
	if w := s.Weight(1, -90, 0); w <= 0 {
		t.Errorf("savannah share 90 m inside the desert = %v, want > 0 (a 150 m band would give 0)", w)
	}
	if w := s.Weight(1, 0, 0); math.Abs(w-0.5) > 1e-9 {
		t.Errorf("the seam = %v, want 0.5", w)
	}
	// Symmetric.
	for _, x := range []float64{10, 40, 70, 95} {
		if a, b := s.Weight(0, -x, 0), s.Weight(1, x, 0); math.Abs(a-b) > 1e-9 {
			t.Errorf("x=±%v: %v vs %v", x, a, b)
		}
	}
	// An explicit transition keeps its own.
	rs := []Region{
		{ID: "west", Environment: EnvDesertCanyon, Transition: 100, Footprint: square(-400, 0, 400)},
		{ID: "east", Environment: EnvSavannah, Transition: 100, Footprint: square(400, 0, 400)},
	}
	ResolvePairTransitions(rs, DefaultAdjacency())
	if rs[0].PairTransitions != nil || rs[1].PairTransitions != nil {
		t.Errorf("explicit transitions took pair widths: %v %v", rs[0].PairTransitions, rs[1].PairTransitions)
	}
}

func TestNoPairWidthsIsTheOldArithmetic(t *testing.T) {
	// The same set with and without an (inapplicable) table: same bits.
	w := Warp{Amount: 40, Scale: 300, Seed: 7}
	plain := mustSet(t, []Region{
		{ID: "a", Environment: "x", Footprint: square(-300, 0, 300)},
		{ID: "b", Environment: "y", Footprint: square(300, 0, 300)},
	}, w)
	rs := append([]Region(nil), plain.Regions...)
	ResolvePairTransitions(rs, DefaultAdjacency())
	resolved := mustSet(t, rs, w)
	for x := -700.0; x <= 700; x += 13.7 {
		for z := -400.0; z <= 400; z += 41.3 {
			for i := 0; i < 2; i++ {
				if a, b := plain.Weight(i, x, z), resolved.Weight(i, x, z); a != b {
					t.Fatalf("(%v, %v) region %d: %v vs %v", x, z, i, a, b)
				}
			}
		}
	}
}

func TestPairBandIsContinuousAcrossNeighbours(t *testing.T) {
	// A desert with a savannah east (200 m) and dunes south (150 m): along
	// its south-east corner the band changes smoothly.
	rs := []Region{
		{ID: "desert", Environment: EnvDesertCanyon, Footprint: square(0, 0, 400)},
		{ID: "grass", Environment: EnvSavannah, Footprint: square(800, 0, 400)},
		{ID: "sand", Environment: EnvDunes, Footprint: square(0, 800, 400)},
	}
	ResolvePairTransitions(rs, DefaultAdjacency())
	s := mustSet(t, rs, Warp{})
	if got := s.PairBandAt(0, 395, -300); math.Abs(got-200) > 1 {
		t.Errorf("band facing the savannah = %v, want 200", got)
	}
	if got := s.PairBandAt(0, -300, 395); math.Abs(got-150) > 1 {
		t.Errorf("band facing the dunes = %v, want 150", got)
	}
	prev := s.Weight(0, 300, 380)
	for k := 1; k <= 200; k++ {
		f := float64(k) / 200
		x, z := 300+100*f, 380+20*math.Sin(f*3)
		w := s.Weight(0, x, z)
		if math.Abs(w-prev) > 0.05 {
			t.Fatalf("a step of %v in the desert's share at (%.1f, %.1f)", w-prev, x, z)
		}
		prev = w
	}
}

func TestFindAdjacentAndTheForbiddenWarning(t *testing.T) {
	rs := []Region{
		{ID: "dry", Environment: EnvDesertCanyon, Footprint: square(-400, 0, 400)},
		{ID: "cold", Environment: EnvTundra, Footprint: square(400, 0, 400)},
		{ID: "far", Environment: EnvSavannah, Footprint: square(4000, 0, 200)},
	}
	adj := DefaultAdjacency()
	ResolvePairTransitions(rs, adj)
	s := mustSet(t, rs, Warp{Amount: 40, Scale: 300, Seed: 3})
	pairs, err := FindAdjacent(nil, s, adj, AdjacentOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// cold/dry touch; far is a gap away, but the fill hands the gap to the
	// nearest regions — cold and far share its midline.
	var forb []Adjacent
	for _, p := range pairs {
		if p.Forbidden() {
			forb = append(forb, p)
		}
	}
	if len(forb) != 1 || forb[0].IDA != "cold" || forb[0].IDB != "dry" {
		t.Fatalf("forbidden pairs %+v", forb)
	}
	f := forb[0]
	if f.Suggestion() != EnvSavannah || math.Abs(f.Where[0]) > 120 {
		t.Errorf("suggestion %q at %v, want savannah near x = 0", f.Suggestion(), f.Where)
	}
	if w := f.Warning(); !strings.Contains(w, "cold (tundra)") || !strings.Contains(w, "Insert intermediate region") {
		t.Errorf("warning %q", w)
	}
}

// insertAndCheck plans the intermediate for (a, b), adds it, and returns the
// new set's forbidden pairs.
func insertAndCheck(t *testing.T, rs []Region, adj *Adjacency, w Warp, a, b string) ([]Adjacent, *Intermediate) {
	t.Helper()
	ResolvePairTransitions(rs, adj)
	s := mustSet(t, rs, w)
	plan, err := PlanIntermediate(s, adj, s.Index(Key(a)), s.Index(Key(b)), IntermediateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	next := append([]Region(nil), s.Regions...)
	for k, fp := range plan.Footprints {
		if _, _, bad := SelfIntersection(fp); bad || len(fp) < 3 {
			t.Fatalf("footprint %d is not a simple polygon (%d points)", k, len(fp))
		}
		next = append(next, Region{ID: "between_" + string(rune('a'+k)), Environment: plan.Environment,
			Priority: plan.Priority, Footprint: fp})
	}
	ResolvePairTransitions(next, adj)
	after := mustSet(t, next, w)
	pairs, err := FindAdjacent(nil, after, adj, AdjacentOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return ForbiddenPairs(pairs), plan
}

func TestInsertIntermediateSeparatesTheForbiddenPair(t *testing.T) {
	adj := DefaultAdjacency()
	w := Warp{Amount: 40, Scale: 300, Seed: 11}
	left, plan := insertAndCheck(t, []Region{
		{ID: "dry", Environment: EnvDesertCanyon, Footprint: square(-400, 0, 400)},
		{ID: "cold", Environment: EnvTundra, Footprint: square(400, 0, 400)},
	}, adj, w, "dry", "cold")
	if len(left) != 0 {
		t.Fatalf("still forbidden after the insertion: %v", left[0].Warning())
	}
	if plan.Environment != EnvSavannah || plan.Priority != 1 || len(plan.Footprints) != 1 {
		t.Errorf("plan %s priority %d, %d footprints", plan.Environment, plan.Priority, len(plan.Footprints))
	}
	b := PolygonBounds(plan.Footprints[0])
	if b.MinX > -100 || b.MaxX < 100 || b.MaxX-b.MinX > 1200 {
		t.Errorf("the savannah's footprint %v should straddle x = 0, a few bands wide", b)
	}
}

func TestInsertIntermediateOnAConcaveContact(t *testing.T) {
	// A U of desert with tundra filling its mouth and lying past each arm:
	// the contact is three-sided and concave.
	u := []Point{{-600, -400}, {600, -400}, {600, 400}, {300, 400}, {300, -100}, {-300, -100}, {-300, 400}, {-600, 400}}
	plug := []Point{{-300, -100}, {300, -100}, {300, 900}, {-300, 900}}
	left, plan := insertAndCheck(t, []Region{
		{ID: "u", Environment: EnvDesertCanyon, Footprint: u},
		{ID: "plug", Environment: EnvTundra, Footprint: plug},
	}, DefaultAdjacency(), Warp{Amount: 30, Scale: 250, Seed: 5}, "u", "plug")
	if len(left) != 0 {
		t.Fatalf("still forbidden after the insertion: %v", left[0].Warning())
	}
	if len(plan.Footprints) == 0 {
		t.Fatal("no footprint")
	}
}

func TestInsertIntermediateRefusesAnAllowedPair(t *testing.T) {
	s := desertSavannah(t, DefaultAdjacency(), Warp{})
	if _, err := PlanIntermediate(s, DefaultAdjacency(), 0, 1, IntermediateOptions{}); err == nil {
		t.Error("an allowed pair got an intermediate")
	}
}
