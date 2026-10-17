package procgen

// reference.go — a stage kind's schema as text: what `foundry gen list -kind`
// prints, what the editor's Claude reference serves, and what the guide's
// stage tables are written from. One printer, so the three cannot disagree.

import (
	"fmt"
	"strings"
)

// KindReference is one kind's full parameter table as text: what `gen list
// -kind` prints and what the editor's Claude reference serves.
func KindReference(k *StageKind) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s) — %s\n%s\n\nParams:\n", k.Name, k.Category, k.DisplayLabel(), k.Doc)
	var rows func(defs []ParamDef, indent string)
	rows = func(defs []ParamDef, indent string) {
		for _, p := range defs {
			fmt.Fprintf(&b, "%s%-12s %-8s default %-14s %s\n", indent, p.Name, ParamTypeName(p.Type),
				DescribeDefault(&p), p.Doc)
			if extra := describeShape(&p); extra != "" {
				fmt.Fprintf(&b, "%s%-12s %s\n", indent, "", extra)
			}
			if p.Type == ParamList {
				rows(p.Fields, indent+"    ")
			}
		}
	}
	rows(k.Params, "  ")
	return b.String()
}

// DescribeDefault is a param's default as a short string.
func DescribeDefault(p *ParamDef) string {
	v := p.Default
	switch p.Type {
	case ParamFloat:
		return fmt.Sprintf("%g", v.Num)
	case ParamInt, ParamSeed:
		return fmt.Sprintf("%d", int64(v.Num))
	case ParamBool:
		return fmt.Sprintf("%v", v.Bool)
	case ParamVec2:
		return fmt.Sprintf("(%g,%g)", v.Vec[0], v.Vec[1])
	case ParamVec3, ParamColor:
		return fmt.Sprintf("(%g,%g,%g)", v.Vec[0], v.Vec[1], v.Vec[2])
	case ParamSpline, ParamCurve:
		return fmt.Sprintf("%d points", len(v.Points))
	case ParamList:
		return fmt.Sprintf("%d items", len(v.Items))
	}
	if v.Str == "" {
		return `""`
	}
	return v.Str
}

func describeShape(p *ParamDef) string {
	var parts []string
	if p.Min != p.Max {
		parts = append(parts, fmt.Sprintf("range %g..%g", p.Min, p.Max))
	}
	switch p.Type {
	case ParamEnum:
		parts = append(parts, "one of "+strings.Join(p.Choices, ", "))
	case ParamStage:
		if len(p.StageKinds) > 0 {
			parts = append(parts, "an upstream stage of kind "+strings.Join(p.StageKinds, " or "))
		} else {
			parts = append(parts, "an upstream stage's id")
		}
	case ParamSpline, ParamCurve:
		parts = append(parts, "points {"+strings.Join(p.PointKeys(), ", ")+"}")
	}
	return strings.Join(parts, "; ")
}
