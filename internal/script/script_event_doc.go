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
	{":checkpoint:", "player, every screen up", "name str",
		"A checkpoint was reached and its autosave is on disk. name is the checkpoint volume's ID, or checkpoint()'s argument."},
	{":primary_fire:", "player", "—",
		"The primary fire button (left mouse) went down."},
	{":primary_fire_end:", "player", "—",
		"…and came back up."},
	{":secondary_fire:", "player", "—",
		"The secondary fire button (right mouse) went down."},
	{":secondary_fire_end:", "player", "—",
		"…and came back up."},
	{":throw:", "player", "—",
		"The throw key (G) was pressed."},
	{":weapon_slot:", "player", "slot int",
		"A weapon-slot key (1 to 5) was pressed. slot is the number pressed."},
	{":weapon_cycle:", "player", "step int",
		"The mouse wheel or Q asked for the next (step 1) or previous (step -1) weapon."},
	{":reload:", "player", "—",
		"The reload key (R) was pressed."},
	{":landed:", "player", "speed float",
		"The player touched the ground after being off it. speed is how fast they were falling when they arrived, in units a second — 0 for a step down, tens for a drop. This is where fall damage goes; the velocity is already zero by the time a handler runs."},
	{":left_ground:", "player", "—",
		"…and left it again — jumped, walked off a ledge, was thrown by a blast, or had the floor taken away."},
	{":key_down:", "level, player", "key str",
		"A key or mouse button went down, named as key_down takes it (\"Space\", \"F\", \"Mouse Left\"). Every key, not just the bound ones; not key repeat, and not a press a menu took. For how long it has been held, ask key_held_time."},
	{":key_up:", "level, player", "key str, held float",
		"…and came back up. held is how long it was down, in seconds."},
	{":song_marker:", "level, player", "name str, value int",
		"The level's tracker song played a marker (an E command in a pattern). name is the song's name for that marker, or empty; value is the command's number. A score's markers come here too, with value the bar the marker is in."},
	{":song_section:", "level, player", "name str",
		"The level's song started a section (a score, a rehearsal mark) — at the top, on a section's own Next, or where song_section asked."},
	{":song_beat:", "level, player", "beat int, section str",
		"A beat of the level's song, for a song with beat_events on. beat counts from 0 within the pattern. A score posts every beat, counted from 0 within the bar."},
	{":song_bar:", "level, player", "section str",
		"…and the first beat of each bar, after its :song_beat:."},
	{":song_end:", "level, player", "song str",
		"A song (or score) that does not loop reached its end."},
	{":pointer_down:", "level, player, and the object under the cursor", "button atom, hit, at",
		"A mouse button went down on the world with the cursor free (a screen or @level.prop.cursor asked for it). button is :left:, :right: or :middle:; hit is the nearest object under the cursor or null; at is where the ray met it, or null for the sky."},
	{":pointer_up:", "level, player, and the object under the cursor", "button atom, hit, at",
		"…and came back up, wherever the cursor is now."},
	{":pointer_click:", "level, player, and the object under the cursor", "button atom, hit, at",
		"A press and a release on the same thing with no drag between them — the event a selection wants."},
	{":pointer_enter:", "level, player, and the object", "hit ref",
		"The cursor moved onto an object in the world. Not while it is over a widget."},
	{":pointer_exit:", "level, player, and the object", "hit ref",
		"…and off it again."},
	{":pointer_wheel:", "level, player, and the object under the cursor", "amount float, hit, at",
		"The wheel turned over the world with the cursor free; positive is away from the user. The wheel is the pointer's then, not the weapon switch."},
	{":pointer_drag_start:", "level, player, and the object pressed on", "button atom, hit, at",
		"A button held on the world has moved a few pixels: a drag, not a click. hit and at are where the press began."},
	{":pointer_drag:", "level, player, and the object pressed on", "button atom, delta vec, at",
		"The drag moved; at most once a frame. delta is the pointer's travel since the last one in window pixels (z unused); at is the world point under the cursor now, or null."},
	{":pointer_drag_end:", "level, player, and the object pressed on", "button atom, hit, at",
		"The button came up and ended the drag. hit and at are what it was let go over."},
	{":pointer_drop:", "the object a drag ended on", "button atom, dragged, at",
		"Something was dragged onto this object and let go. dragged is the object the drag began on, or null for one that began on nothing."},
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
		"The agent reached a hiding spot at a point of its waypoint path that takes cover, or — on_target: fight — a firing position behind cover. A path's points also post :waypoint: and :waypoints_end: to an agent walking it."},
	{":ai_maneuver:", "an AI agent", "src ref",
		"on_target: fight — the agent is setting off for a new firing position: strafing, advancing, backing off, flanking, taking cover or searching for a target it lost. Stand a crouched body up here."},
	{":ai_in_position:", "an AI agent", "src ref",
		"…it has reached it (or given up on it), or got up from a dive, and stands to fight. Pose it here."},
	{":ai_dodge:", "an AI agent", "src ref",
		"The agent is diving aside: from the player's aim (on_target: fight, its dodge_chance) or an ai_dodge call. :ai_in_position: follows once it is up."},
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
	{":drag_start:", "widget, then its screen", "src ref, pos vec",
		"A draggable widget was pressed and the pointer moved a few pixels with it. pos is where the press began, in the screen's canvas units."},
	{":drag:", "widget, then its screen", "src ref, pos vec, delta vec",
		"…and moved on; at most once a frame. pos is the pointer, delta its travel since the last :drag:, both in canvas units — add delta to the widget's translate to carry it."},
	{":drop:", "widget, then its screen", "src ref, onto, pos vec",
		"…and was let go. onto is the widget it landed on (not counting itself), or null over empty space."},
	{":submit:", "widget, then its screen", "src ref, text str",
		"Enter was pressed in a text field."},
	{":finished:", "widget, then its screen", "src ref",
		"A video widget reached the end of its clip. Never fires for a looping one, which has no end to reach."},
	{":bound:", "widget or screen that called bind_key", "src ref, action str, slot int",
		"The press bind_key was listening for arrived — bound, cleared with Backspace (or the pad's Back) or cancelled with Escape (or the pad's Start). Read binding_name to see which."},
	{":cancel:", "screen", "—",
		"Escape was pressed while this screen was the topmost modal one. The screen closes either way."},
	{":shown:", "screen", "—",
		"The screen was put on the stack."},
	{":hidden:", "screen", "—",
		"…and taken off it."},
}
