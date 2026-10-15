package regions

// names.go — the `regions` stage's kind and parameter names.
//
// Two packages speak the stage's schema: procgen/stages, which declares and
// runs it, and procgen/genlevel, whose SyncRegions writes the level's region
// volumes into it. genlevel must not import the stages (they reach the
// Nanite cook and so gfx), and both may import this package, so the names
// live here, where neither can drift from the other.

const (
	// StageKind is the stage kind's name.
	StageKind = "regions"

	// ParamRegions is the synced region list.
	ParamRegions = "regions"
	// The list item's fields.
	FieldID          = "id"
	FieldEnvironment = "environment"
	FieldGenerator   = "generator"
	FieldPriority    = "priority"
	FieldTransition  = "transition"
	FieldAnchor      = "anchor"
	FieldAnchored    = "anchored"
	FieldFootprint   = "footprint"

	// ParamSeedLevel is the one-shot "write this list into the level"
	// switch a template sets (genlevel/regions_seed.go).
	ParamSeedLevel = "seed_level"
)
