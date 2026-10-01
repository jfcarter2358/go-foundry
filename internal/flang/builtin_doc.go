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
	{GroupWorld, "bone_position", "bone_position(@e.model.name, bone)  ·  bone_position(ref, model, bone)", "vec",
		"Where one bone of an attachment is right now, in world space — a muzzle to put a flash at. The player's are the viewmodel's, carried by the camera."},
	{GroupWorld, "recoil", "recoil(pitch)  ·  recoil(pitch, yaw)  ·  recoil(pitch, yaw, recover)", "null",
		"Kick the player's view up by `pitch` and right by `yaw` degrees, over a few hundredths of a second. `recover` (0–1, default 0.7) is the share that eases back by itself. The rest stays, so a held trigger climbs. Pulling the view down while it recovers counts against the recovery."},
	{GroupWorld, "look_at", "look_at(place)  ·  look_at(place, seconds)  ·  look_at(null)", "bool",
		"Turn the player's view toward a vec or an object over `seconds` (default at once), aiming again each frame so a moving view still ends on it. While player control is off the view then stays on it until `look_at(null)`; with control on, the player has the view back as soon as it arrives."},
	{GroupWorld, "camera_detach", "camera_detach()", "bool",
		"Take the camera off the player and hold it where it is. Every camera_* verb that places or turns it does this first; the player's own look is left alone for when it comes back."},
	{GroupWorld, "camera_attach", "camera_attach()  ·  camera_attach(seconds)  ·  camera_attach(seconds, ease)", "bool",
		"Give the camera back to the player, blending from where it is to the player's eye over `seconds` (at once without). False if it was not detached."},
	{GroupWorld, "camera_move_to", "camera_move_to(place)  ·  camera_move_to(place, seconds)  ·  camera_move_to(place, seconds, ease)", "null",
		"Slide the camera to a vec or an object's position. Ends an orbit, not a camera_look_at, so it can dolly past something while staying on it."},
	{GroupWorld, "camera_turn_to", "camera_turn_to(yaw, pitch)  ·  camera_turn_to(yaw, pitch, seconds)  ·  camera_turn_to(yaw, pitch, seconds, ease)", "null",
		"Pan and tilt the camera to a facing, in degrees: yaw 0 looks along +X and grows turning right, pitch is up. While orbiting it swings round the target instead. The short way round."},
	{GroupWorld, "camera_roll_to", "camera_roll_to(degrees)  ·  camera_roll_to(degrees, seconds)  ·  camera_roll_to(degrees, seconds, ease)", "null",
		"Bank the camera; positive leans its top to the right."},
	{GroupWorld, "camera_look_at", "camera_look_at(place)  ·  camera_look_at(place, seconds, ease)  ·  camera_look_at(null)", "bool",
		"Turn the camera toward a vec or an object and keep it there — an object is followed as it moves — until camera_look_at(null) or another facing is asked for."},
	{GroupWorld, "camera_orbit", "camera_orbit(target)  ·  camera_orbit(target, options)  ·  camera_orbit(options)  ·  camera_orbit(null)", "bool",
		"Swing the camera round a vec or an object (followed as it moves), looking at it. Starts from where the camera is, so nothing jumps. Options: distance, yaw, pitch (degrees), seconds, ease; each one given blends to its new value. Options alone adjust the orbit already running; null stops it."},
	{GroupWorld, "camera_zoom", "camera_zoom(fov)  ·  camera_zoom(fov, seconds)  ·  camera_zoom(fov, seconds, ease)", "null",
		"Ease the field of view, in degrees (5–120; lower is closer). Works attached or detached — a scope wants it too."},
	{GroupWorld, "pick", "pick(atom)  ·  pick(atom, options)", "null",
		"hitscan from the camera through the cursor, answering `(atom, hit, at)` the same way. Takes hitscan's options; `screen` picks through another point on the window. With the cursor captured the cursor is the middle of the view."},
	{GroupWorld, "screen_ray", "screen_ray()  ·  screen_ray(point)", "dict",
		"The ray from the camera through the cursor, or a point on the window in pixels, as `{\"from\": vec, \"direction\": vec}` — the dict hitscan takes."},
	{GroupWorld, "world_to_screen", "world_to_screen(place)", "vec",
		"Where a vec or an object is on the window: x and y in pixels from the top-left, z its distance in front of the camera. Null when it is behind the camera."},
	{GroupWorld, "after", "ref <- after(ms, atom, ...)", "null",
		"Send the message to that object, that many milliseconds of play time from now. Only ever sent — `after(...)` on its own has nobody to deliver to."},
	{GroupWorld, "impulse", "impulse(ref, vec)", "bool",
		"Add to something's velocity, in world units a second — a blast, a launch pad, a shove. The physics carries on from there: gravity still pulls, walls still stop it, and standing on the ground bleeds it off. Works on `player()` the same as on any entity. Use this rather than writing `velocity`, which throws away whatever the body was already doing."},
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
	{GroupWorld, "explode", "explode(vec)  ·  explode(ref)  ·  explode(vec, radius)", "ref",
		"Set off a volumetric explosion at a point or at an object's position, with no effect authored for it: a fireball that radiates outward, cools into smoke and flashes light over the level. It removes itself when the smoke has cleared. Returns the effect, whose `.prop` fields can be changed before it draws."},
	{GroupWorld, "decal", "decal(texture, vec)  ·  decal(texture, ref)  ·  decal(texture, vec, options)", "ref",
		"Project a texture onto the level's surfaces at a point — a bullet hole, a scorch mark, a splatter. It takes a POINT, not an orientation: the surface there is found and the mark laid flat against it, so `hitscan`'s `at` is all a weapon script needs. Its mesh is the surface's own triangles clipped to the mark's box, so it follows corners and steps and rides the lift it landed on. Returns the decal, or null when there was no surface at that point. Options: size, depth, normal, roll, material, blend (alpha|multiply|additive), tint, alpha, life, order, max_angle, unlit, permanent. Marks placed this way are capped, oldest first, unless `permanent`."},
	{GroupWorld, "hitscan", "hitscan(atom)  ·  hitscan(atom, options)", "null",
		"Cast a ray from the reticle and post `(atom, hit, at)` back to the caster: the nearest object hit, or a list of every one in the line of fire."},
	{GroupWorld, "play_waypoints", "play_waypoints(obj, animation)", "bool",
		"Send a dynamic brush, a chunk, an entity, a sprite, an emitter, a sound, a light or a volume along a waypoint animation from wherever it is now: to each point in turn, arriving at that point's time, easing its rotation and scale on the way where it has them. A chunk only takes position and its Y rotation; a point light only moves; a directional light only turns, shining the way each arrow points. `animation` is `@id` or its name. Posts `:waypoint:` at every point and `:waypoints_end:` when the animation's length runs out. False, with a warning, for an animation with no points."},
	{GroupWorld, "stop_waypoints", "stop_waypoints(obj)", "bool",
		"Stop an object where it has got to along its waypoint animation. False if it was not following one."},
	{GroupWorld, "move_to", "move_to(obj, vec, seconds)", "null",
		"Ease a dynamic brush, a chunk or an entity to a position over that many seconds, from wherever it is now. The vec is in the space `position` reads (`origin` for a chunk). Its velocity, and an entity's gravity, wait until it arrives. 0 seconds moves it at once."},
	{GroupWorld, "rotate_to", "rotate_to(obj, vec, seconds)", "null",
		"Ease a dynamic brush or an entity to Euler angles, in radians, over that many seconds. Blended as numbers, not the short way round, so 2π is a whole turn. Its angular velocity waits until it arrives."},
	{GroupWorld, "scale_to", "scale_to(obj, vec, seconds)", "null",
		"Ease a dynamic brush or an entity to a per-axis scale over that many seconds; <1, 1, 1> is its authored size."},
	{GroupWorld, "ai_move_to", "ai_move_to(agent, place)  ·  ai_move_to(agent, place, run)", "bool",
		"Send an entity with an AI controller to a vec, or to an object's position, by the level's navigation. It outranks the agent's target and objectives until it arrives, then posts `:ai_arrived:`. `run` true sends it at its run speed with its run animation — a dash for cover. False, with a warning, when the place cannot be reached — the agent still walks as near as it can."},
	{GroupWorld, "ai_dodge", "ai_dodge(agent, from)  ·  ai_dodge(agent, from, away)", "bool",
		"Make an agent dive aside now: across the line from `from` (an object or a vec), or with `away` true straight off from it — a grenade at its feet. Posts `:ai_dodge:` as it goes and `:ai_in_position:` once it is up again, and plays the type's dodge_left_anim/dodge_right_anim. False when it is already diving, being moved by something else, or has no ground to land on."},
	{GroupWorld, "ai_stop", "ai_stop(agent)", "bool",
		"Cancel an `ai_move_to`. The agent goes back to whatever it would otherwise be doing. False if there was no command to cancel."},
	{GroupWorld, "ai_target", "ai_target(agent, entity)  ·  ai_target(agent, null)", "bool",
		"Make an agent hunt one entity, and keep hunting it however long it is out of sight. `null` hands targeting back to what the agent can see."},
	{GroupWorld, "ai_objective", "ai_objective(agent, volume)  ·  ai_objective(agent, null)", "bool",
		"Send an agent to perform one objective volume now, ahead of its own list. `null` drops the objective it is working. False, with a warning, for an objective it may not take."},
	{GroupWorld, "ai_follow_waypoints", "ai_follow_waypoints(agent, animation)  ·  ai_follow_waypoints(agent, animation, loop)  ·  ai_follow_waypoints(agent, null)", "bool",
		"Send an AI agent along a waypoint animation from its first point, walking to each on the level's navigation and doing what each point's AI block says: waiting, taking cover, holding its fire. `animation` is `@id`, or a name — the agent's own type's path by that name included. Posts `:waypoint:` at each point and `:waypoints_end:` at the end. `null` stops it following."},
	{GroupWorld, "ai_continue", "ai_continue(agent)", "bool",
		"Send an agent on from the point of its path it is waiting at — cutting its wait short, or giving the signal a Wait for script point holds for. False if it was not waiting at one."},
	{GroupWorld, "ai_follow", "ai_follow(agent, leader)  ·  ai_follow(agent, null)", "bool",
		"Make an agent keep up with a leader — `@player` or an entity — as a squadmate: it keeps a place in a loose group behind it, walks or runs to stay there, fights from where it stands, and regroups once the leader is `ai_follow_leash` away. A waypoint path it is walking comes first. `null` follows nobody."},
	{GroupWorld, "find_path", "find_path(from, to)  ·  find_path(from, to, entity)", "list|null",
		"The corners of a walkable route between two vecs or objects, sized for a player or for the given entity, ending at `to`. Null when there is no complete route."},
	{GroupWorld, "can_see", "can_see(entity, target)", "bool",
		"Whether nothing solid lies between an entity's eyes and another entity, or a vec. Range and field of view are not considered — that is the controller's business."},
	{GroupWorld, "find_cover", "find_cover(agent, radius)  ·  find_cover(agent, radius, options)", "vec|null",
		"A spot within `radius` the agent can reach where level geometry hides it from its target, nearest to itself; null when there is none, or nothing to hide from. Options: `\"from\"` an entity or vec to hide from instead of the target; `\"near\"` a place to look round instead of its feet; `\"duck\"` the share of its height to hide (0.6: crouched behind something waist high); `\"peek\"` true to also need a clear shot from its standing eye there — a firing position. Send it with `ai_move_to(agent, spot, true)`."},
	{GroupWorld, "play", "play(sound)  ·  play(effect)", "null",
		"Start a placed sound, switching it on if it was off. On an effect, set it off from the start."},
	{GroupWorld, "stop", "stop(sound)  ·  stop(effect)", "null",
		"Stop a placed sound. It ramps rather than cutting, so it does not click. On an effect, stop its run where it is; it vanishes."},
	{GroupWorld, "play_sound", "play_sound(name, ref)  ·  play_sound(name, ref, gain)", "ref",
		"Play a one-shot at an object's position. Returns the sound it placed."},
	{GroupWorld, "play_sound_at", "play_sound_at(name, vec)  ·  play_sound_at(name, vec, gain)", "ref",
		"Play a one-shot at a point. The sound removes itself when it finishes."},
	{GroupWorld, "play_music", "play_music(name)  ·  play_music(name, fade)", "bool",
		"Switch the level's global track, crossfading over `fade` seconds."},
	{GroupWorld, "stop_music", "stop_music()  ·  stop_music(fade)", "null",
		"Fade the level's track out."},
	{GroupWorld, "attach", "attach(obj, entity)  ·  attach(obj, entity, bone)  ·  attach(obj, entity, model, bone)  ·  attach(obj, frame)", "bool",
		"Make a sprite, an emitter, a volume or a dynamic point light ride an entity, a bone of one of its models, or a named frame. Its `position` becomes an offset in that space; it is not moved to keep it where it was."},
	{GroupWorld, "detach", "detach(obj)", "bool",
		"Put a sprite, an emitter, a volume or a point light back in world space, leaving it where it currently is. False if it was riding nothing."},

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
		"Ask for the game to end. Like load_level it happens between frames, not at the call: the window and the GL context belong to the main thread, and only the frame loop may end them. In the editor's play mode it returns to the editor instead."},

	{GroupWorld, "bind_key", "bind_key(action, slot)", "bool",
		"Listen for the player's next press and make it the binding for an action's slot: 1 or 2 take a key or mouse button, 3 takes a game controller's button, trigger or stick direction — tapped to bind its press, held @level.prop.pad_hold_time to bind holding it. Escape or the pad's Start cancels, Backspace or the pad's Back clears the slot, and the engine's own inputs (Escape, `~`, 0, 8, 9, F1–F12, the pad's Start, Back and Guide) cannot be bound; a press of the wrong kind for the slot is ignored. The input is taken off any other action live at the same time. Call it from a screen or widget script: that screen or widget gets `:bound:` when the press arrives, however it ended."},
	{GroupWorld, "binding_name", "binding_name(action, slot)", "str",
		"The name of the input in an action's slot (1 or 2 for keys, 3 for the pad), such as \"W\", \"Left Shift\", \"Mouse Left\", \"Pad A\" or \"Hold Pad X\" (a pad button bound as a hold) — the name key_down takes; \"\" when the slot is empty."},
	{GroupWorld, "binding_label", "binding_label(action, slot)", "str",
		"What to show the player for an action's slot: binding_name for a key, and for a pad input the words printed on the pad they last used — \"Cross\" on a PlayStation pad, \"A\" on an Xbox one, \"B\" on a Nintendo one for the same south button. \"\" when the slot is empty."},
	{GroupWorld, "reset_bindings", "reset_bindings()", "null",
		"Put every action back on its default keys and pad inputs. Saved like any rebind."},
	{GroupWorld, "key_down", "key_down(key)", "bool",
		"Whether a key, mouse button or pad input is held down, by name as binding_name spells it — \"W\", \"Space\", \"Left Shift\", \"Mouse Left\", \"Pad A\", \"Pad Right Stick Up\" — in any case. A trigger or stick direction is down once pushed halfway. The physical key, whatever the player has rebound; prefer action_down for anything a player might want on another key. A press a menu took is not down."},
	{GroupWorld, "key_pressed", "key_pressed(key)", "bool",
		"Whether a key or mouse button went down since the last tick — true on exactly one tick per press, so it is what a `:tick:` handler checks for a keypress. Key repeat is not a press."},
	{GroupWorld, "key_released", "key_released(key)", "bool",
		"Whether a key or mouse button came up since the last tick. A tap made inside one frame is pressed and released on the same tick."},
	{GroupWorld, "key_held_time", "key_held_time(key)", "float",
		"How long a key or mouse button has been held, in seconds: 0 on the tick it went down, growing each tick after, and 0 again once it is up."},
	{GroupWorld, "keys_down", "keys_down()", "list",
		"The name of every key and mouse button held down, sorted."},
	{GroupWorld, "action_down", "action_down(action)", "bool",
		"key_down for an action (\"jump\", \"interact\"): true while any key bound to it in the player's layout is held. A pad hold binding is down once held @level.prop.pad_hold_time; a pad press sharing its button with a hold is a tap — pressed and released together as the button comes up — and never down."},
	{GroupWorld, "action_pressed", "action_pressed(action)", "bool",
		"key_pressed for an action: any of its keys went down since the last tick."},
	{GroupWorld, "action_released", "action_released(action)", "bool",
		"key_released for an action: one of its keys came up since the last tick and none is still held."},
	{GroupWorld, "action_held_time", "action_held_time(action)", "float",
		"key_held_time for an action: how long its longest-held key has been down, in seconds."},
	{GroupWorld, "action_value", "action_value(action)", "float",
		"How far an action is pushed, 0 to 1: 1 while a key or button bound to it is held, and a trigger's or stick's travel when its pad input is analog — the number movement and driving read, so half a stick walks at half speed."},
	{GroupWorld, "pad_axis", "pad_axis(axis)", "float",
		"A pad's stick or trigger by name, after the dead zone: \"left_x\", \"left_y\", \"right_x\", \"right_y\" from -1 to 1 (right and up positive), \"left_trigger\", \"right_trigger\" from 0 to 1. The physical stick, whatever the player has rebound. 0 with no pad plugged in."},
	{GroupWorld, "rumble", "rumble(heavy, light, seconds)", "null",
		"Shake the pad the player last used: heavy and light are the two motors' strength, 0 to 1, scaled by @level.prop.pad_vibration. A weaker shake during a stronger one does not cut it short. Does nothing with no pad plugged in, so call it freely."},
	{GroupWorld, "rumble_triggers", "rumble_triggers(left, right, seconds)", "null",
		"rumble for the impulse triggers, on pads that have them (Xbox One and later); does nothing on any other."},
	{GroupWorld, "setting", "setting(name, default)", "any",
		"One of the player's own game settings — whatever a settings screen offers beyond the mouse, the keys and the volume, such as which corner a HUD element sits in. The default is returned when nothing is saved under the name, and is also its type: a str, int, float or bool."},
	{GroupWorld, "set_setting", "set_setting(name, value)", "null",
		"Save one of the player's game settings (a str, int, float or bool) for every level and session, in settings.yaml beside controls.yaml. Written once the change has settled, like the mouse settings."},

	{GroupWorld, "save_game", "save_game(slot)", "bool",
		"Ask for the game to be saved to a slot (a name such as \"slot1\"), in saves/ in the player's data directory. Between frames, like load_level. A level whose `save_mode` is `checkpoint` saves its last checkpoint instead of where the player stands."},
	{GroupWorld, "load_game", "load_game(slot)", "bool",
		"Ask for a slot's save to be loaded: its level, then everything play had changed in it. False, and nothing happens, when the slot is empty."},
	{GroupWorld, "save_info", "save_info(slot)", "dict|null",
		"What a slot holds, without loading it: `slot`, `level`, `kind` (\"manual\" or \"checkpoint\"), `checkpoint`, `time` (unix seconds), `date` (\"2026-09-21 14:03\") and `play_time` (seconds). Null for an empty slot."},
	{GroupWorld, "latest_save", "latest_save()", "str|null",
		"The slot saved most recently, autosave included — what a Continue button loads. Null when there are no saves."},
	{GroupWorld, "delete_save", "delete_save(slot)", "bool",
		"Empty a slot. True if it is empty afterwards."},
	{GroupWorld, "checkpoint", "checkpoint(name)", "bool",
		"Reach a checkpoint from a script, as walking into a `checkpoint:` volume does: autosave, and remember it as where retry() goes. Once per name per play-through; false if already reached."},
	{GroupWorld, "retry", "retry()", "bool",
		"Put the player back at the last checkpoint reached, or restart the level if none has been. What a death screen's Retry button calls."},
	{GroupWorld, "restart_level", "restart_level()", "bool",
		"Load the running level again from scratch, forgetting its checkpoints."},
}
