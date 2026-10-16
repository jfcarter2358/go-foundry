package procgen

// validate.go — what is wrong with a document, in words an author can act on.
//
// Advisory in the editor (it lists the problems under the stage list and
// still saves), binding for a run: Run refuses a document with any problem,
// because a stage that runs against a missing upstream would make outputs
// nobody asked for and a manifest that lies about them.

import (
	"fmt"
	"strings"
)

// Problem is one thing wrong with a document. Stage and Param say where, when
// it is somewhere in particular.
type Problem struct {
	Stage string
	Param string
	Msg   string
}

func (p Problem) String() string {
	var b strings.Builder
	if p.Stage != "" {
		b.WriteString("stage " + p.Stage)
		if p.Param != "" {
			b.WriteString(", " + p.Param)
		}
		b.WriteString(": ")
	}
	b.WriteString(p.Msg)
	return b.String()
}

// Validate lists the document's problems: an empty or illegal name or stage
// ID, a duplicate ID, a kind this build does not know, a stage ref that names
// nothing, something downstream, a disabled stage or the wrong kind, a list
// or spline outside its item bounds, and whatever each kind's own Validate
// adds.
func (d *Doc) Validate() []Problem {
	var out []Problem
	add := func(stage, param, format string, args ...any) {
		out = append(out, Problem{Stage: stage, Param: param, Msg: fmt.Sprintf(format, args...)})
	}
	if d.Name != "" && !ValidDocName(d.Name) {
		add("", "", "the generator's name %q must be lower-case letters, digits and underscores, "+
			"starting with a letter — it is part of every ID it places", d.Name)
	}
	if strings.Contains(d.OutputDir(), "..") || strings.HasPrefix(d.Output, "/") {
		add("", "", "output %q must be a folder under resources/", d.Output)
	}
	out = append(out, validatePaintDefs(d)...)
	index := map[string]int{}
	for i := range d.Stages {
		s := &d.Stages[i]
		name := s.ID
		if name == "" {
			name = fmt.Sprintf("#%d", i+1)
		}
		switch {
		case s.ID == "":
			add(name, "", "has no id")
		case !ValidStageID(s.ID):
			add(name, "", "id %q must be lower-case letters, digits and underscores, starting with a letter", s.ID)
		}
		if j, dup := index[s.ID]; dup && s.ID != "" {
			add(name, "", "the id is also stage %d's", j+1)
		} else {
			index[s.ID] = i
		}
		k := KindByName(s.Kind)
		if k == nil {
			add(name, "", "this build has no stage kind %q", s.Kind)
			continue
		}
		p := Resolve(k.Params, s.Params)
		out = append(out, validateParams(d, i, name, "", k.Params, p)...)
		if k.Validate != nil {
			for _, msg := range k.Validate(d, s, p) {
				add(name, "", "%s", msg)
			}
		}
	}
	return out
}

// validateParams checks refs and item bounds, recursing into list items.
func validateParams(d *Doc, at int, stage, prefix string, defs []ParamDef, p Params) []Problem {
	var out []Problem
	for i := range defs {
		def := &defs[i]
		v := p[def.Name]
		where := prefix + def.Name
		switch def.Type {
		case ParamStage:
			if v.Str == "" {
				continue
			}
			j := d.StageIndex(v.Str)
			switch {
			case j < 0:
				out = append(out, Problem{stage, where, fmt.Sprintf("names stage %q, and there is none", v.Str)})
			case j >= at:
				out = append(out, Problem{stage, where, fmt.Sprintf("names stage %q, which runs after this one — move it above", v.Str)})
			case d.Stages[j].Disabled && !d.Stages[at].Disabled:
				out = append(out, Problem{stage, where, fmt.Sprintf("names stage %q, which is disabled", v.Str)})
			case len(def.StageKinds) > 0 && !containsStr(def.StageKinds, d.Stages[j].Kind):
				out = append(out, Problem{stage, where, fmt.Sprintf("names stage %q, a %s; it wants %s",
					v.Str, d.Stages[j].Kind, strings.Join(def.StageKinds, " or "))})
			}
		case ParamMask:
			if def.IsMaskWord(v.Str) {
				continue
			}
			if msg := validateMask(d, v.Str); msg != "" {
				out = append(out, Problem{stage, where, msg})
			}
		case ParamSpline, ParamCurve:
			if n := len(v.Points); n < def.MinItems {
				out = append(out, Problem{stage, where, fmt.Sprintf("has %d points, needs at least %d", n, def.MinItems)})
			} else if def.MaxItems > 0 && n > def.MaxItems {
				out = append(out, Problem{stage, where, fmt.Sprintf("has %d points, at most %d", n, def.MaxItems)})
			}
		case ParamList:
			if n := len(v.Items); n < def.MinItems {
				out = append(out, Problem{stage, where, fmt.Sprintf("has %d items, needs at least %d", n, def.MinItems)})
			} else if def.MaxItems > 0 && n > def.MaxItems {
				out = append(out, Problem{stage, where, fmt.Sprintf("has %d items, at most %d", n, def.MaxItems)})
			}
			for k, it := range v.Items {
				out = append(out, validateParams(d, at, stage, fmt.Sprintf("%s[%d].", where, k+1), def.Fields, it)...)
			}
		}
	}
	return out
}

// ValidationError is Validate's problems as an error, for Run.
type ValidationError struct{ Problems []Problem }

func (e *ValidationError) Error() string {
	lines := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		lines[i] = p.String()
	}
	if len(lines) == 1 {
		return lines[0]
	}
	return fmt.Sprintf("%d problems:\n  %s", len(lines), strings.Join(lines, "\n  "))
}
