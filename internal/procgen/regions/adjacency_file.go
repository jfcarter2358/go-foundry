package regions

// adjacency_file.go — a project's own adjacency rules, merged over the
// built-in table (docs/regions/SPEC.md, "Adjacency rules").
//
// The built-in table (adjacency.go, ENVIRONMENT.md's) is a starting point; a
// project that adds environments of its own, or disagrees about a band, says
// so in resources/environments/adjacency.yaml:
//
//	# resources/environments/adjacency.yaml
//	pairs:
//	  - between: [desert_canyon, savannah]
//	    transition: 250                 # a wider band than the built-in 200
//	  - between: [desert_canyon, savannah]
//	    forbidden: true                 # (a later entry for the same pair wins)
//	    intermediate: steppe
//	    note: "no grass straight off the canyon rim"
//	  - between: [coast, "*"]           # "*" is any environment
//	    remove: true                    # drop a built-in rule altogether
//
// Each entry REPLACES whatever the table said about its pair (the built-in
// rule or an earlier entry): it is not a field-by-field merge, so an entry
// that says only `transition: 250` is not forbidden even where the built-in
// rule was. `remove: true` deletes the pair's rule, leaving the pair to the
// default (allowed, each region's own transition). The pair's order does not
// matter. A forbidden entry should name an `intermediate`; without one the
// warning still appears, but "Insert intermediate region" has nothing to
// insert.
//
// The file is parsed strictly (an unknown key is an error naming it), so a
// typo cannot silently fail to apply. Reading it is the caller's business
// (procgen/stages.ProjectAdjacency, which knows where resources/ is): this
// package stays free of file paths.

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v2"
)

// AdjacencyFileName is the project override's name, under
// resources/environments/.
const AdjacencyFileName = "adjacency.yaml"

// AdjacencyFile is the override file's form.
type AdjacencyFile struct {
	Pairs []AdjacencyEntry `yaml:"pairs"`
}

// AdjacencyEntry is one pair's rule in the file.
type AdjacencyEntry struct {
	Between      []string `yaml:"between,flow"`
	Transition   float64  `yaml:"transition,omitempty"`
	Forbidden    bool     `yaml:"forbidden,omitempty"`
	Intermediate string   `yaml:"intermediate,omitempty"`
	Note         string   `yaml:"note,omitempty"`
	Remove       bool     `yaml:"remove,omitempty"`
}

// ParseAdjacencyFile reads an override file's bytes. Each problem (a pair
// that is not two names, a negative transition, a removal with a rule) is
// reported with its entry number.
func ParseAdjacencyFile(data []byte) (*AdjacencyFile, error) {
	var f AdjacencyFile
	if err := yaml.UnmarshalStrict(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", AdjacencyFileName, err)
	}
	var probs []string
	for i, e := range f.Pairs {
		at := fmt.Sprintf("pair %d", i+1)
		if len(e.Between) != 2 || strings.TrimSpace(e.Between[0]) == "" || strings.TrimSpace(e.Between[1]) == "" {
			probs = append(probs, at+": between must name two environments")
			continue
		}
		at += fmt.Sprintf(" (%s, %s)", e.Between[0], e.Between[1])
		if e.Transition < 0 {
			probs = append(probs, at+": transition must not be negative")
		}
		if e.Remove && (e.Transition != 0 || e.Forbidden || e.Intermediate != "") {
			probs = append(probs, at+": remove takes no rule")
		}
		if e.Intermediate != "" && !e.Forbidden {
			probs = append(probs, at+": an intermediate only means something on a forbidden pair")
		}
	}
	if len(probs) > 0 {
		return nil, fmt.Errorf("%s: %s", AdjacencyFileName, strings.Join(probs, "; "))
	}
	return &f, nil
}

// Apply is the table with the file's entries laid over it, in order: a new
// table; a is not changed.
func (f *AdjacencyFile) Apply(a *Adjacency) *Adjacency {
	out := a.Clone()
	if f == nil {
		return out
	}
	for _, e := range f.Pairs {
		x, y := strings.TrimSpace(e.Between[0]), strings.TrimSpace(e.Between[1])
		if e.Remove {
			out.Delete(x, y)
			continue
		}
		out.Set(x, y, Rule{Transition: e.Transition, Forbidden: e.Forbidden, Intermediate: e.Intermediate, Note: e.Note})
	}
	return out
}

// File is the table in the override file's form: every pair, sorted — what
// `foundry gen env adjacency` prints, and a file a project could start from.
func (a *Adjacency) File() *AdjacencyFile {
	f := &AdjacencyFile{}
	for _, p := range a.Pairs() {
		r := a.rules[p]
		f.Pairs = append(f.Pairs, AdjacencyEntry{Between: []string{p.A, p.B}, Transition: r.Transition,
			Forbidden: r.Forbidden, Intermediate: r.Intermediate, Note: r.Note})
	}
	return f
}

// Encode is the table as YAML in the override file's form.
func (a *Adjacency) Encode() ([]byte, error) { return yaml.Marshal(a.File()) }

// Describe is one rule as a phrase: "200 m", "forbidden (insert savannah)".
func (r Rule) Describe() string {
	switch {
	case r.Forbidden && r.Intermediate != "":
		return "forbidden (insert " + r.Intermediate + ")"
	case r.Forbidden:
		return "forbidden"
	case r.Transition > 0:
		return fmt.Sprintf("%g m", r.Transition)
	}
	return "allowed"
}
