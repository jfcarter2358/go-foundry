package regions

// adjacency.go — which environments may touch, and over how wide a band.
//
// The one place WFC-style constraints belong in natural terrain
// (ENVIRONMENT.md, "Why not wavefunction collapse"): a table of environment
// pairs, each either a transition width or FORBIDDEN with a suggested
// intermediate environment to put between them. Desert next to glacier is a
// mistake an author should hear about; desert next to savannah wants a
// 200 m band.
//
// This file is the TYPE and the built-in defaults, transcribed from
// ENVIRONMENT.md's adjacency table. What uses it (phase 5,
// docs/regions/SPEC.md):
//
//   - adjacency_file.go: the project override,
//     resources/environments/adjacency.yaml, merged over the defaults;
//   - pairband.go: a region left at the default transition takes its
//     pair's width from here, pair by pair;
//   - adjacent.go: which regions actually touch (their weights overlap on a
//     coarse raster), and which of those touches the table forbids;
//   - intermediate.go: "Insert intermediate region", the footprint of a
//     region of the suggested environment that separates a forbidden pair.

import "sort"

// AnyEnvironment in a pair matches every environment ("coast ↔ any").
const AnyEnvironment = "*"

// Rule is what the table says about one pair.
type Rule struct {
	// Transition is the band width the pair wants, metres; 0 for no opinion.
	Transition float64 `yaml:"transition,omitempty"`
	// Forbidden pairs must not touch; Intermediate is the environment to put
	// between them.
	Forbidden    bool   `yaml:"forbidden,omitempty"`
	Intermediate string `yaml:"intermediate,omitempty"`
	// Note is the table's reason, for the warning text.
	Note string `yaml:"note,omitempty"`
}

// Pair names two environments; the order does not matter.
type Pair struct{ A, B string }

// key is the pair in a canonical order.
func (p Pair) key() Pair {
	if p.B < p.A {
		return Pair{p.B, p.A}
	}
	return p
}

// Adjacency is the table.
type Adjacency struct {
	rules map[Pair]Rule
}

// NewAdjacency is an empty table.
func NewAdjacency() *Adjacency { return &Adjacency{rules: map[Pair]Rule{}} }

// Set records the rule for a pair (either may be AnyEnvironment).
func (a *Adjacency) Set(x, y string, r Rule) { a.rules[Pair{x, y}.key()] = r }

// Lookup is the rule for two environments: the exact pair's, else a rule for
// one of them against AnyEnvironment, else none. Two regions of the same
// environment have no rule.
func (a *Adjacency) Lookup(x, y string) (Rule, bool) {
	if a == nil || x == y {
		return Rule{}, false
	}
	if r, ok := a.rules[Pair{x, y}.key()]; ok {
		return r, true
	}
	if r, ok := a.rules[Pair{x, AnyEnvironment}.key()]; ok {
		return r, true
	}
	if r, ok := a.rules[Pair{y, AnyEnvironment}.key()]; ok {
		return r, true
	}
	return Rule{}, false
}

// Clone is an independent copy.
func (a *Adjacency) Clone() *Adjacency {
	out := NewAdjacency()
	if a != nil {
		for p, r := range a.rules {
			out.rules[p] = r
		}
	}
	return out
}

// Delete drops the rule for a pair.
func (a *Adjacency) Delete(x, y string) { delete(a.rules, Pair{x, y}.key()) }

// Rule is the rule recorded for exactly this pair (no AnyEnvironment
// fallback), for listings.
func (a *Adjacency) Rule(p Pair) (Rule, bool) {
	r, ok := a.rules[p.key()]
	return r, ok
}

// Pairs is every pair in the table, sorted, for listings.
func (a *Adjacency) Pairs() []Pair {
	out := make([]Pair, 0, len(a.rules))
	for p := range a.rules {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	return out
}

// Environment names the defaults use: the eleven built-in presets
// (procgen/stages/environments_builtin.go and environment_*.go).
const (
	EnvDesertCanyon     = "desert_canyon"
	EnvSavannah         = "savannah"
	EnvDunes            = "dunes"
	EnvPineForest       = "pine_forest"
	EnvAlpine           = "alpine"
	EnvCoast            = "coast"
	EnvTundra           = "tundra"
	EnvVolcanic         = "volcanic"
	EnvTallgrassPrairie = "tallgrass_prairie"
	EnvOakSavannah      = "oak_savannah"
	EnvWetland          = "wetland"
)

// DefaultAdjacency is ENVIRONMENT.md's table.
func DefaultAdjacency() *Adjacency {
	a := NewAdjacency()
	a.Set(EnvDesertCanyon, EnvSavannah, Rule{Transition: 200, Note: "canyon walls erode into grassy slopes; sand gives way to dry grass"})
	a.Set(EnvDesertCanyon, EnvDunes, Rule{Transition: 150, Note: "sand deposition fills canyon floors toward the dunes"})
	a.Set(EnvSavannah, EnvPineForest, Rule{Transition: 300, Note: "tree density ramps; grass thins under canopy"})
	a.Set(EnvPineForest, EnvAlpine, Rule{Transition: 250, Note: "treeline by altitude rather than distance where both overlap"})
	a.Set(EnvCoast, AnyEnvironment, Rule{Transition: 100, Note: "beach or cliff edge chosen by the inland biome's relief"})
	a.Set(EnvDesertCanyon, EnvAlpine, Rule{Forbidden: true, Intermediate: EnvSavannah, Note: "desert cannot touch alpine; insert savannah or steppe"})
	a.Set(EnvDesertCanyon, EnvTundra, Rule{Forbidden: true, Intermediate: EnvSavannah, Note: "desert cannot touch tundra; insert savannah or steppe"})
	// "Desert" in ENVIRONMENT.md's row covers both deserts on the roadmap.
	a.Set(EnvDunes, EnvAlpine, Rule{Forbidden: true, Intermediate: EnvSavannah, Note: "desert cannot touch alpine; insert savannah or steppe"})
	a.Set(EnvDunes, EnvTundra, Rule{Forbidden: true, Intermediate: EnvSavannah, Note: "desert cannot touch tundra; insert savannah or steppe"})
	a.Set(EnvVolcanic, EnvPineForest, Rule{Transition: 150, Note: "ash soils, burnt deadwood in the band"})
	a.Set(EnvTallgrassPrairie, EnvOakSavannah, Rule{Transition: 300, Note: "oak clumps thin out into open prairie"})
	a.Set(EnvOakSavannah, EnvPineForest, Rule{Transition: 300, Note: "oaks give way to pines; grass thins under canopy"})
	a.Set(EnvTallgrassPrairie, EnvSavannah, Rule{Transition: 200, Note: "grass height and colour blend from tall green to short dry"})
	defaultPresetAdjacency(a)
	return a
}

// defaultPresetAdjacency adds the rows for the presets of
// docs/regions/PRESETS.md step 2 that ENVIRONMENT.md's table does not name
// (docs/regions/PRESETS_2.md, "Adjacency", has the reasoning).
//
// Two things decide a row: whether the pair meets in nature, and whether a
// band tile where they meet can hold both presets' ground layers — every
// tile wears at most eight (region_material_palette.go). Every preset has
// at most four layers, so any pair fits; a forbidden pair is forbidden for
// sense, and its intermediate must itself be allowed beside both
// (TestAdjacencyIntermediatesAreAllowed).
func defaultPresetAdjacency(a *Adjacency) {
	allow := func(x, y string, w float64, note string) { a.Set(x, y, Rule{Transition: w, Note: note}) }
	forbid := func(x, y, via, note string) { a.Set(x, y, Rule{Forbidden: true, Intermediate: via, Note: note}) }
	// The cold chain: forest → tundra → ice, mountains above both.
	allow(EnvAlpine, EnvTundra, 200, "scree and snowfields run down into frozen lowland")
	allow(EnvPineForest, EnvTundra, 250, "the treeline: the forest thins into open tundra")
	// The marsh borders whatever is low and wet enough.
	allow(EnvWetland, EnvPineForest, 150, "forest floor gives way to marsh; reeds in the hollows")
	allow(EnvWetland, EnvTallgrassPrairie, 150, "prairie potholes: tall grass down to the reeds")
	allow(EnvWetland, EnvOakSavannah, 150, "grass and open trees down to the marsh edge")
	allow(EnvWetland, EnvTundra, 150, "thermokarst ponds merge into marsh")
	allow(EnvWetland, EnvSavannah, 200, "a seasonal swamp in dry grassland")
	allow(EnvWetland, EnvVolcanic, 150, "marsh over the old lava flows' foot")
	forbid(EnvWetland, EnvDesertCanyon, EnvSavannah, "a marsh cannot touch desert; insert savannah")
	forbid(EnvWetland, EnvDunes, EnvSavannah, "a marsh cannot touch a sand sea; insert savannah")
	// Grassland edges.
	allow(EnvTallgrassPrairie, EnvPineForest, 300, "tall grass gives way to the forest edge")
	allow(EnvTallgrassPrairie, EnvAlpine, 250, "foothills rise out of the plains")
	allow(EnvTallgrassPrairie, EnvDesertCanyon, 200, "badlands breaking out of the prairie")
	allow(EnvTallgrassPrairie, EnvDunes, 200, "a grass-stabilised sand hills margin")
	allow(EnvDunes, EnvSavannah, 200, "dunes thin into grass on the desert margin")
	// Volcanoes stand among most things.
	allow(EnvVolcanic, EnvAlpine, 200, "a volcanic peak in a range")
	allow(EnvVolcanic, EnvSavannah, 200, "a volcano over dry grassland (East Africa)")
	allow(EnvVolcanic, EnvDesertCanyon, 150, "basalt flows over desert country")
	allow(EnvVolcanic, EnvDunes, 150, "dark sand blown from the cinder field")
}
