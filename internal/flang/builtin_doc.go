package flang

// builtin_doc.go — what each library function is, in one line.
//
// Kept beside the table it describes rather than in the editor that shows it,
// for the reason builtin.go gives for being a map: the reference, the
// completion list and the "did you mean" message all read one table, so a
// builtin added to the language cannot be missing from any of them by accident.
// TestBuiltinDocsCoverTable makes that mechanical — a new entry in `builtins`
// with no line here fails the build's tests, and a line here naming a function
// that does not exist fails it too.
//
// The summaries are deliberately one line. This is the list you read to
// remember which of `get` and `has` you wanted; SCRIPTING.md is where the
// paragraphs live.

import "sort"

// BuiltinDoc is one library function's reference entry.
//
// Sig is written the way a call is written, including the argument names, since
// "which argument is the separator" is most of what a reader wants from a
// signature. Ret is the type the call evaluates to.
type BuiltinDoc struct {
	Group   string
	Name    string
	Sig     string
	Ret     string
	Summary string
}

// Builtin groups, in the order the reference lists them. Roughly the order a
// script needs them in: talking to the log, then arithmetic, then the data, and
// only then the engine.
const (
	GroupOutput     = "Output"
	GroupMath       = "Math"
	GroupStrings    = "Strings"
	GroupContainers = "Lists and dicts"
	GroupVectors    = "Vectors"
	GroupConvert    = "Conversion"
	GroupWorld      = "World"
)

// builtinGroupOrder fixes the order groups are shown in.
var builtinGroupOrder = []string{
	GroupOutput, GroupMath, GroupStrings, GroupContainers,
	GroupVectors, GroupConvert, GroupWorld,
}

// BuiltinGroups returns the group names in display order.
func BuiltinGroups() []string {
	out := make([]string, len(builtinGroupOrder))
	copy(out, builtinGroupOrder)
	return out
}

// BuiltinDocs returns every library function's entry, grouped in display order
// and alphabetical within a group.
func BuiltinDocs() []BuiltinDoc {
	rank := map[string]int{}
	for i, g := range builtinGroupOrder {
		rank[g] = i
	}
	out := make([]BuiltinDoc, len(builtinDocs))
	copy(out, builtinDocs)
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := rank[out[i].Group], rank[out[j].Group]; a != b {
			return a < b
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// BuiltinDocsIn returns one group's entries, alphabetically.
func BuiltinDocsIn(group string) []BuiltinDoc {
	var out []BuiltinDoc
	for _, d := range BuiltinDocs() {
		if d.Group == group {
			out = append(out, d)
		}
	}
	return out
}

// builtinDocs is the table. Unsorted here — BuiltinDocs orders it — so entries
// can be kept beside the implementation they describe.
var builtinDocs = []BuiltinDoc{
	// ---- output ----
	{GroupOutput, "log", "log(v, ...)", "null",
		"Write the values, concatenated, to the log window and the terminal."},

	// ---- maths ----
	{GroupMath, "abs", "abs(n)", "int|float", "Magnitude. An int stays an int."},
	{GroupMath, "floor", "floor(n)", "int", "Round down to an int."},
	{GroupMath, "ceil", "ceil(n)", "int", "Round up to an int."},
	{GroupMath, "round", "round(n)", "int", "Round to the nearest int, halves away from zero."},
	{GroupMath, "sqrt", "sqrt(n)", "float", "Square root."},
	{GroupMath, "sin", "sin(radians)", "float", "Sine. Angles are radians throughout."},
	{GroupMath, "cos", "cos(radians)", "float", "Cosine."},
	{GroupMath, "tan", "tan(radians)", "float", "Tangent."},
	{GroupMath, "atan2", "atan2(y, x)", "float",
		"The angle of the vector (x, y), in radians, quadrant included."},
	{GroupMath, "pi", "pi()", "float", "3.14159…"},
	{GroupMath, "min", "min(a, b, ...)", "int|float",
		"The smallest argument, returned as the value that was passed in."},
	{GroupMath, "max", "max(a, b, ...)", "int|float", "The largest argument."},
	{GroupMath, "clamp", "clamp(n, lo, hi)", "int|float",
		"n held between lo and hi; a clamped int is still an int, so it is still an index."},
	{GroupMath, "random", "random()", "float", "A float in [0, 1). Varies from run to run."},
	{GroupMath, "random_range", "random_range(lo, hi)", "int|float",
		"A number from lo to hi, both included. An int when both bounds are ints."},

	// ---- strings ----
	{GroupStrings, "upper", "upper(s)", "str", "Uppercased."},
	{GroupStrings, "lower", "lower(s)", "str", "Lowercased."},
	{GroupStrings, "trim", "trim(s)", "str", "Leading and trailing whitespace removed."},
	{GroupStrings, "split", "split(s, sep)", "list", "Cut s at every sep."},
	{GroupStrings, "join", "join(list, sep)", "str", "The elements written out, sep between them."},
	{GroupStrings, "replace", "replace(s, old, new)", "str", "Every occurrence of old replaced."},
	{GroupStrings, "starts_with", "starts_with(s, prefix)", "bool", "Prefix test."},
	{GroupStrings, "ends_with", "ends_with(s, suffix)", "bool", "Suffix test."},
	{GroupStrings, "format", "format(template, v, ...)", "str",
		"Each `{}` in the template replaced, left to right. Not printf: a `{}` with no value left is left alone."},

	// ---- containers ----
	{GroupContainers, "len", "len(x)", "int", "The length of a str, list, tuple or dict."},
	{GroupContainers, "append", "append(list, v, ...)", "list",
		"Add to the end, in place. The list is returned too — a list is a reference, so both are the same object."},
	{GroupContainers, "remove", "remove(list|dict, v)", "bool",
		"Drop the first matching element, or the named key. False if it was not there."},
	{GroupContainers, "contains", "contains(str|list|tuple, v)", "bool",
		"Membership, or substring for a str."},
	{GroupContainers, "has", "has(dict, key)", "bool", "Whether the key is present, whatever its value."},
	{GroupContainers, "get", "get(dict, key, fallback?)", "any",
		"The value, or the fallback, or null. Never an error, so a missing key need not be checked twice."},
	{GroupContainers, "keys", "keys(dict)", "list", "The keys, sorted — so iterating twice gives the same order twice."},
	{GroupContainers, "values", "values(dict)", "list", "The values, in key order."},
	{GroupContainers, "sort", "sort(list)", "list",
		"Sort a list of numbers or a list of strings, in place. A mixed list is an error."},

	// ---- vectors ----
	{GroupVectors, "dot", "dot(a, b)", "float", "Dot product."},
	{GroupVectors, "cross", "cross(a, b)", "vec", "Cross product."},
	{GroupVectors, "length", "length(v)", "float", "Magnitude of a vec."},
	{GroupVectors, "normalize", "normalize(v)", "vec", "The same direction, length 1."},
	{GroupVectors, "distance", "distance(a, b)", "float",
		"Between two vecs — or between two objects, which is the commoner reading in a script and is accepted too."},

	// ---- conversion ----
	{GroupConvert, "str", "str(v)", "str", "How the value prints."},
	{GroupConvert, "int", "int(v)", "int", "From an int, float, bool or str. A str that is not a number is an error."},
	{GroupConvert, "float", "float(v)", "float", "From any number or a numeric str."},
	{GroupConvert, "type", "type(v)", "atom", "The value's type, as an atom: `:int:`, `:list:`, `:ref:`…"},

	// ---- world ----
	{GroupWorld, "player", "player()", "ref",
		"The player entity. Null in the editor and before the level finishes loading."},
	{GroupWorld, "time", "time()", "float", "Seconds of play time since the level loaded."},
	{GroupWorld, "find", "find(tag)", "list",
		"Every object in the level carrying that tag. Empty, with a warning in the log, if none does."},
	{GroupWorld, "spawn", "spawn(type, vec)", "ref",
		"Create an entity from an entity type. It exists only for this play session."},
	{GroupWorld, "destroy", "destroy(ref)", "bool",
		"Remove an entity or a volume, posting `:destroy:` first. Geometry cannot be destroyed."},
	{GroupWorld, "play_anim", "play_anim(ref, name)  ·  play_anim(ref, model, name)", "bool",
		"Play the animation by the name the entity gives it — not the clip name inside the .fmdl."},
	{GroupWorld, "stop_anim", "stop_anim(ref)", "bool", "Stop whatever is playing on the entity."},
	{GroupWorld, "after", "ref <- after(ms, atom, ...)", "null",
		"Send the message to that object, that many milliseconds of play time from now. Only ever sent — `after(...)` on its own has nobody to deliver to."},
	{GroupWorld, "mount", "mount(ref, vehicle, seat?)", "bool",
		"Put something aboard a vehicle; without a seat it stands on the deck. Posts `:mount:` to the vehicle."},
	{GroupWorld, "dismount", "dismount(ref)", "bool", "Take it back off, posting `:dismount:` to the vehicle."},
	{GroupWorld, "ease_sky", "ease_sky(seconds)", "bool",
		"Arm a blend: whatever `sky_*` properties are written next are eased into over that many seconds rather than snapped to. `seconds <= 0` cancels a blend in progress instead, and returns false."},
	{GroupWorld, "emit", "emit(emitter)", "null",
		"Start a particle emitter, or resume one that was stopped. Switches it on as well, so it works on a disabled emitter."},
	{GroupWorld, "stop_emit", "stop_emit(emitter)", "null",
		"Stop new particles. The ones in flight finish their lives, and bursts still fire. Set `.prop.enabled` instead to make everything vanish at once."},
	{GroupWorld, "burst", "burst(emitter, count)  ·  burst(emitter, count, layer)", "null",
		"Release that many particles now, from every layer or from one named layer."},
	{GroupWorld, "spawn_effect", "spawn_effect(name, vec)", "ref",
		"Play a .fpar effect once at a point, with no emitter authored for it. It removes itself when its last particle dies."},
	{GroupWorld, "hitscan", "hitscan(atom)  ·  hitscan(atom, options)", "null",
		"Cast a ray from the reticle and post `(atom, hit, at)` back to the caster: the nearest object hit, or a list of every one in the line of fire."},
	{GroupWorld, "play", "play(sound)", "null",
		"Start a placed sound, switching it on if it was off."},
	{GroupWorld, "stop", "stop(sound)", "null",
		"Stop a placed sound. It ramps rather than cutting, so it does not click."},
	{GroupWorld, "play_sound", "play_sound(name, ref)  ·  play_sound(name, ref, gain)", "ref",
		"Play a one-shot at an object's position. Returns the sound it placed."},
	{GroupWorld, "play_sound_at", "play_sound_at(name, vec)  ·  play_sound_at(name, vec, gain)", "ref",
		"Play a one-shot at a point. The sound removes itself when it finishes."},
	{GroupWorld, "play_music", "play_music(name)  ·  play_music(name, fade)", "bool",
		"Switch the level's global track, crossfading over `fade` seconds."},
	{GroupWorld, "stop_music", "stop_music()  ·  stop_music(fade)", "null",
		"Fade the level's track out."},
	{GroupWorld, "attach", "attach(obj, entity)  ·  attach(obj, entity, bone)  ·  attach(obj, entity, model, bone)  ·  attach(obj, frame)", "bool",
		"Make a sprite or an emitter ride an entity, a bone of one of its models, or a named frame. Its `position` becomes an offset in that space; it is not moved to keep it where it was."},
	{GroupWorld, "detach", "detach(obj)", "bool",
		"Put a sprite or an emitter back in world space, leaving it where it currently is. False if it was riding nothing."},

	{GroupWorld, "show_screen", "show_screen(name)", "ref",
		"Put a .fui screen on the stack, or raise it if it is already up. Returns the screen."},
	{GroupWorld, "hide_screen", "hide_screen(screen)", "bool",
		"Take a screen off the stack. Takes a screen ref or a name; false if it was not up."},
	{GroupWorld, "screen", "screen(name)", "ref|null",
		"The screen with that name if it is on the stack, or null."},
	{GroupWorld, "widget", "widget(screen, id)", "ref|null",
		"One widget inside a screen, by the id the UI editor gives it."},
	{GroupWorld, "focus", "focus(widget)", "bool",
		"Move keyboard and gamepad focus to a widget. False if it is hidden, disabled or gone."},
	{GroupWorld, "load_level", "load_level(name)", "bool",
		"Ask for a level to be loaded, covered by the `loading` screen. It happens between frames, not at the call, because loading tears down the runtime running the script."},
	{GroupWorld, "quit_game", "quit_game()", "bool",
		"Ask for the game to end. Like load_level it happens between frames, not at the call: the window and the GL context belong to the main thread, and only the frame loop may end them."},
}
