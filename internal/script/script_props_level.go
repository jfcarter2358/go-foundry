package script

// script_props_level.go — the `.prop` surface on `@level`.
//
// One thing so far, and it is there because a loading screen needs it: a bar
// bound to `@level.prop.load_progress` is the whole reason the load reports
// itself at all. Everything else about a level is either a chunk's business or
// an authored list the editor owns.
//
// Read-only, both of them. A script that could write the load progress would be
// lying to the bar rather than telling it anything.

import (
	"foundry/internal/flang"
	"foundry/internal/hooks"
)

func levelProps() map[string]propAccess {
	return map[string]propAccess{
		"load_progress": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.FloatVal(float64(hooks.LoadProgress())), true
			},
			Doc: "how far a level load has got, from 0 to 1. 1 when nothing is loading",
		},
		"load_stage": {
			Get: func(*refCache, flang.RefVal) (flang.Value, bool) {
				return flang.StrVal(hooks.LoadStage()), true
			},
			Doc: "a one-line description of what the load is doing, or empty when nothing is",
		},
	}
}
