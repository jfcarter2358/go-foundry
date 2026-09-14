package script

// script_event_doc.go — the events the engine posts, described.
//
// Here rather than in the editor that shows them because this package is what
// posts them (script_events.go): an event added to the tick and left out of the
// reference is a feature nobody can find, and the two being neighbours is the
// only thing that reliably prevents it.
//
// The table is descriptive, not a dispatch table — the posting code names its
// atoms inline, and rewriting that to read from here would put a lookup in the
// per-frame path to save a duplication of thirteen strings. TestEventDocsMatch
// keeps them honest instead.

// EventDoc is one engine-posted message.
//
// Args lists what follows the atom in the clause's parameter list, so it can be
// read straight across into a `def` line.
type EventDoc struct {
	Atom     string // written as it is matched: `:tick:`
	PostedTo string
	Args     string
	When     string
}

// EventDocs returns every event the engine posts, in the order the reference
// shows them: lifecycle, then per-frame, then the ones something has to happen
// for.
func EventDocs() []EventDoc {
	out := make([]EventDoc, len(eventDocs))
	copy(out, eventDocs)
	return out
}

// EventAtoms returns just the atoms, for a completion list.
func EventAtoms() []string {
	out := make([]string, len(eventDocs))
	for i, d := range eventDocs {
		out[i] = d.Atom
	}
	return out
}

var eventDocs = []EventDoc{
	{":spawn:", "any object", "—",
		"The level loaded, or the object was created by spawn()."},
	{":destroy:", "any object", "—",
		"The object is about to be removed."},
	{":tick:", "any object", "dt float",
		"Once per frame during play. dt is the frame's length in seconds."},
	{":enter:", "entity, volume", "other ref",
		"Another object's origin entered its bounds."},
	{":exit:", "entity, volume", "other ref",
		"…and left again."},
	{":collide:", "entity", "other ref",
		"Two entities overlapped and were pushed apart."},
	{":interact:", "entity, volume", "src ref",
		"The interact key (E) was pressed while inside its bounds."},
	{":anim_start:", "entity", "src ref, model str, anim str",
		"A clip began, including each wrap of a looping one. anim is the name the entity gives it, never the .fmdl clip name."},
	{":anim_end:", "entity", "src ref, model str, anim str",
		"A clip finished, or a looping one wrapped."},
	{":mount:", "vehicle", "src ref, seat str",
		"Something came aboard. seat is empty if it is standing on the deck rather than sitting."},
	{":dismount:", "vehicle", "src ref, seat str",
		"…and left again; seat is the one it was in."},
	{":waypoint:", "the object on the path", "src ref, animation ref, index int",
		"A brush, entity, sprite, emitter, sound, light or volume travelling a waypoint animation reached one of its points. index counts from 0."},
	{":waypoints_end:", "the object on the path", "src ref, animation ref",
		"…reached the end of the animation's length, and stopped following it."},
	{":target_acquired:", "an AI agent", "src ref, target ref",
		"The agent noticed something on its Targets list and is now hunting it."},
	{":target_lost:", "an AI agent", "src ref, target ref",
		"…gave up on it after its Lose Time out of sight, or it was destroyed."},
	{":target_in_range:", "an AI agent", "src ref, target ref",
		"The target is in sight within the agent's Attack Range; repeated every Attack Interval while it stays there. Attack here."},
	{":ai_arrived:", "an AI agent", "src ref",
		"The agent reached where ai_move_to sent it."},
	{":ai_path_failed:", "an AI agent", "src ref",
		"The agent could not find, or could not follow, a way to where it was going."},
	{":objective_reached:", "an AI agent, and the objective volume", "src ref, objective ref",
		"The agent arrived inside an objective and has begun performing it. src is the agent either way."},
	{":objective_complete:", "an AI agent, and the objective volume", "src ref, objective ref",
		"…stayed its Duration and finished it — straight after the objective's own Action atom, which carries the same arguments."},
	{":objectives_done:", "an AI agent", "src ref",
		"The agent finished every objective on a list that does not loop."},
	{":cover_taken:", "an AI agent", "src ref",
		"The agent reached a hiding spot at a point of its waypoint path that takes cover. A path's points also post :waypoint: and :waypoints_end: to an agent walking it."},
	{":cover_lost:", "an AI agent", "src ref",
		"…and the spot stopped hiding it — its target moved round the cover — so it is looking for more."},

	{":click:", "widget, then its screen", "src ref",
		"A button, toggle or slider was activated, by pointer or by the navigation key."},
	{":hover:", "widget, then its screen", "src ref",
		"The pointer or the navigation highlight entered the widget."},
	{":unhover:", "widget, then its screen", "src ref",
		"…and left again."},
	{":change:", "widget, then its screen", "src ref, value",
		"A slider, toggle or text field's value changed. value is a float, a bool or a str, following the widget's kind."},
	{":submit:", "widget, then its screen", "src ref, text str",
		"Enter was pressed in a text field."},
	{":finished:", "widget, then its screen", "src ref",
		"A video widget reached the end of its clip. Never fires for a looping one, which has no end to reach."},
	{":cancel:", "screen", "—",
		"Escape was pressed while this screen was the topmost modal one. The screen closes either way."},
	{":shown:", "screen", "—",
		"The screen was put on the stack."},
	{":hidden:", "screen", "—",
		"…and taken off it."},
}
