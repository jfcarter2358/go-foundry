# Scripting

Scripts are written in **flang** and live in `resources/scripts/` as `.flang`
files. A script is attached to an object — an entity, a volume, a sector, an
edge, a floor, a ceiling, a chunk, a light, a UI screen or widget, or the level
itself — and reacts to messages that object receives.

This guide covers what you need to write behaviour. `SCRIPTING.md` at the repo
root is the full language reference.

## Attaching one

Every object's properties panel has a **Script** group with a dropdown over the
`.flang` files under `scripts/` — a combo rather than a text field, because a
mistyped path is a script that silently never runs.

An entity *definition* names the script for its type; an instance may override it
or clear it. A level takes a list of its own, for rules that belong to no single
object; in a level script `@self` is `@level`.

## The shape of a script

```
def on_interact(:interact:, src ref) -> null
    @self.attr.opened = true
    play_sound("clank", @self)
end
```

A handler is a function whose first parameter is an **atom** — a `:name:` in
colons. The function's own name does not matter for dispatch; the atom does.
Comments run from `--` to end of line.

Once per frame during play the engine posts events, then drains the whole message
queue to a fixpoint — so `hit → damage → die → open_door` resolves in one frame
rather than across three.

Scripts do not run while the editor is active, except through the script editor's
**Run on the editor level** button.

## Addressing things

| | |
| --- | --- |
| `@self` | the object this script is attached to |
| `@some_id` | the object with that ID, or `null` |
| `#some_tag` | a **list** of every object carrying that tag |
| `@player` | the player |
| `@level` | the level itself |
| `?some_param` | a [parameter](#parameters) the level bound |

IDs are unique across a level, so `@id` never needs qualifying. `ref.id` gives
the ID as a string; `ref.kind` gives an atom (`:entity:`, `:volume:`, `:sector:`,
`:edge:`, `:floor:`, `:ceiling:`, `:chunk:`, `:light:`, `:biome:`, `:level:`).

A sector's parts:

```
@sec.floor              -- the floor
@sec.ceil               -- the ceiling
@sec.edge.<edge id>     -- one edge, wall or portal
@sec.edges              -- every edge, in ring order
@chunk.sector.<sector id>
```

Identity belongs to the **edge**, not to the wall or portal occupying it — an
edge keeps its ID when you convert it, so `@sec.edge.<id>` is the durable
address. Never store an edge *index*; inserting a vertex renumbers the ring.

## The three kinds of data

| | Owned by | Saved | |
| --- | --- | --- | --- |
| `@x.prop.<name>` | the engine | with the level | intrinsic fields: a wall's visibility, an entity's position, a floor's height |
| `@x.attr.<name>` | the script | with the level (writes during play are discarded) | your own values: health, ammo, whether the door is locked |
| `@self.state[...]` | the script | never | scratch bookkeeping. A `dict`, and only reachable on `@self` |
| `?name` | the **level** | with the level | read-only configuration; see below |

```
@self.attr.health -= amount
@door_wall.prop.visible = false
#lamps.prop.brightness = 0.0
@self.state["opening"] = true
```

Reading an attribute that does not exist gives `null`. Writing one that does not
exist creates it, typed from the value. Writing an unknown *property* is an error
naming the closest valid one.

Assigning to `#tag` writes to every object carrying it.

### Properties worth knowing

The full catalogue is in the `F1` reference, generated from the engine's own
table. The ones that trip people up:

- **`height` is chunk-local; `world_height` is not.** A floor's `height` is the
  authored number the level file stores, so `height = height + 1` round-trips.
  Compare against a player's or an entity's Y using `world_height`.
- **`water_depth` is measured downward from the floor**, so increasing it *lowers*
  the water and a negative value floods flat ground. `water_level` is its
  world-space companion and the one to animate against anything else.
- **`enabled` on a chunk is how a level streams.** `false` takes it out of the
  world entirely — not drawn, not solid, buffers released. `true` rebuilds it.
- **A vec is a value, not a container.** `@self.prop.position.y = 1.0` writes to
  a copy and does nothing. Rebuild it:

```
p = @self.prop.position
@self.prop.position = <p.x, p.y + 1.0, p.z>
```

## Events

The engine posts these without being asked.

| Atom | To | After the atom | When |
| --- | --- | --- | --- |
| `:spawn:` | anything | — | the level loaded, or the object was created |
| `:destroy:` | anything | — | it is about to be removed |
| `:tick:` | anything | `dt float` | once per frame during play |
| `:enter:` | entity, volume | `other ref` | something's origin entered its bounds |
| `:exit:` | " | `other ref` | and left |
| `:collide:` | entity | `other ref` | two entities overlapped and were pushed apart |
| `:interact:` | entity, volume | `src ref` | the interact key was pressed inside its bounds |
| `:anim_start:` / `:anim_end:` | entity | `src ref, model str, anim str` | a clip began / finished |
| `:mount:` / `:dismount:` | vehicle | `src ref, seat str` | something came aboard / left |
| `:waypoint:` | the object on the path | `src ref, animation ref, index int` | a brush, entity, sprite, emitter, sound, light or volume on a waypoint animation reached one of its points |
| `:waypoints_end:` | the object on the path | `src ref, animation ref` | …and reached the end of its length |
| `:target_acquired:` `:target_lost:` `:target_in_range:` | AI agent | `src ref, target ref` | see [AI agents](24-ai.md) |
| `:ai_arrived:` `:ai_path_failed:` `:objectives_done:` | AI agent | `src ref` | " |
| `:objective_reached:` `:objective_complete:`, and an objective's Action atom | AI agent and objective volume | `src ref, objective ref` | " |
| `:cover_taken:` `:cover_lost:` | AI agent | `src ref` | " |
| any atom a point lists | the object on the path | `animation ref` | …reached a point with that atom in its **Events**, straight after that point's `:waypoint:` |
| `:click:` `:hover:` `:unhover:` `:change:` `:submit:` | widget, then its screen | see [UI screens](17-ui-screens.md) | |
| `:cancel:` `:shown:` `:hidden:` | screen | — | |

## Messages

```
@some_id <- (:damage:, @self, 20)
#breakable <- (:damage:, @self, 5)
@self <- (:retry:,)
```

The right side is a tuple whose first element is an atom. It is delivered later
in the same frame. `#tag <-` snapshots the tag's members at send time.

A message no clause matches is **ignored**, not an error — a broadcast to a tag
whose members mostly do not care is the normal case. Custom atoms need no
declaration.

```
def on_damage(:damage:, src ref, amount int) -> null
    @self.attr.health -= amount
    if @self.attr.health <= 0
        @self <- (:die:,)
    end
end

def on_die(:die:) -> null
    @self.prop.visible = false
    destroy(@self)
end
```

### Timers

```
after(500, :open_done:)
after(1000, :damage:, @self, 5)
```

Posts to `@self` after that many milliseconds of play time. Timers do not survive
a level reload.

## Parameters

A script that names the object it acts on is a script for exactly one object.
`param` declares a value the **level** supplies, per object, read back as
`?name`.

```
param target ref
param drop float = 3.0
param latching bool = true

def on_interact(:interact:, src ref) -> null
    if ?target == null
        return
    end
    ?target.floor.prop.height = ?target.floor.prop.height - ?drop
end
```

Twenty buttons share that one file, and each one's Script panel says which door
*it* opens and how far. `resources/scripts/button.flang` is the whole of it,
latching included, and is the file to copy.

- Types are `ref`, `int`, `float`, `bool`, `str` — narrower than the language,
  because a param has to be stored in a level file and edited in a panel.
- Unbound falls back to the declaration's default, then to `null`, so
  `if ?target == null` is the check for a param nobody wired up.
- A `ref` param stores the ID and resolves it on every read: one pointing at
  something that does not exist yet starts working the moment it does.
- **A param is read-only.** Use `@self.attr.<name>` for state the script owns.

In the editor, the Script group lists a row per declared parameter. A `ref` row
is a picker over the level's IDs. `(default)` marks one you have not touched, and
`x` hands it back. Bindings whose declaration went away are listed with a **Clear
Unused** button, so renaming a param does not silently take your wiring with it.

A level script has nowhere to bind them and always reads the declared defaults.

## What you do not have to write

Solidity is a property of an entity *type*, not something a script arranges — set
the collision flags once and the engine honours them every frame. What is still
the script's job is *reacting*: `:enter:`, `:exit:` and `:collide:` are what turn
a resolved overlap into behaviour.

## The standard library

**Math** — `abs` `floor` `ceil` `round` `sqrt` `min` `max` `clamp` `sin` `cos`
`tan` `atan2` `pi()` `random()` `random_range(lo, hi)`

`random_range` includes **both** ends, so `random_range(1, 6)` is a die. It keeps
the type it was given. Do not roll inside a `when` guard — the guard's roll is
not the one the body sees.

**Strings** — `len` `upper` `lower` `split` `join` `trim` `replace` `contains`
`starts_with` `ends_with` `format`

**Lists and dicts** — `len` `append` `remove` `contains` `keys` `values` `get`
`has` `sort`

**Vectors** — `dot` `cross` `length` `normalize` `distance`, and `v.x` `v.y`
`v.z` as members

**World** — `log` `time` `player` `find(tag)` `distance(a, b)` `play_anim`
`stop_anim` `spawn(type, vec)` `destroy(ref)` `after(ms, atom, ...)` `hitscan` `emit` `stop_emit` `burst`
`spawn_effect` `attach` `detach` `mount` `dismount` `ai_move_to` `ai_stop` `ai_target`
`ai_objective` `ai_follow_waypoints` `ai_continue` `find_path` `can_see` `play` `stop` `play_sound`
`play_sound_at` `play_music` `stop_music`

**UI** — `show_screen` `hide_screen` `screen(name)` `widget(screen, id)`
`focus(widget)` `load_level(name)` `quit_game()`

## Hitscan

`hitscan` casts a ray from the reticle — the centre of the screen — and posts
what it met back to the caster as a message.

```
def on_interact(:interact:, src ref) -> null
    hitscan(:shot:)
end

def on_shot(:shot:, hit, at vec) -> null
    spawn_effect("impact_sparks", at)
    if hit != null
        hit <- (:damage:, @self, 10)
    end
end
```

`hit` is the nearest object, or `null`. `at` is where the ray *stopped* — useful
even when nothing was hit, and `at.y` is how you tell a headshot from a hit in
the leg.

Bodies do not stop the ray; geometry does. That is what lets one cast answer both
"what did I hit" and, with `{"all": true}`, "everything in the line of fire".

Options, as a dict second argument: `all`, `range` (default 1000), `entities`
(default on), `volumes` (default **off** — levels wrap rooms in them),
`geometry` (default on; clear it to see through walls), `visible_only`, `tag`,
`ignore`. An unknown key is reported as a typo with a suggestion before the
script runs.

A wall comes back as its edge ref, so `hit.prop.texture = "scorched"` works on
whatever the shot landed on.

## Importing

```
import("shared/math_helpers.flang")
```

Relative to the importing file, rooted at `resources/scripts/`. Functions merge
in flat with no qualification; a file loads once however many times it is
imported; cycles are a load error.

## The script editor

Toolbar: **Script**, or the arrow beside a script name in any object panel.

**Tabs** stay open, so reading one file while writing another does not cost you
your place. `Ctrl+W` closes (asking if unsaved), `Ctrl+Tab` moves between them.

**The sidebar** is the real folder tree under `scripts/` — a folder here is the
folder an `import()` resolves through. Right-click for *Move or rename* (takes a
whole path) and *Delete*, which asks first and tells you what will notice. A
folder's right-click has *Delete folder*, which takes everything under it after
the same kind of confirmation. Drag a script onto a folder to move it.

**Completion** opens as you type, and what it offers depends on what you wrote:

| After | It offers |
| --- | --- |
| a name | this function's variables, this file's `def`s, imports, the stdlib, keywords |
| `@` | `self`, `player`, `level`, and every object id in the open level |
| `?` | this file's declared parameters |
| `#` | every tag anything in the level carries |
| `:` | every event the engine posts, then atoms this file already mentions |
| `.` | `prop`, `attr`, `state`, `id`, `kind`, and the structural steps that object has |
| `.prop.` | that kind's properties, with what each means and whether you may write it |

Typing is the only thing that opens the list — a caret arriving by arrow key or
click never does. `Ctrl+Space` opens it anywhere. `Tab` or `Enter` accepts, `Esc`
dismisses it for that word.

The lists come from the engine's own tables and the level that is open, so they
cannot describe a version of flang or a level that is not in front of you.

**F1** opens the reference: keybindings, every event, every stdlib function with
its signature, every property of every kind with whether a script may write it.

## Errors and limits

A runtime error aborts the handler it happened in, logs the file and line, and
the frame continues. A script that errors repeatedly in one session is disabled
and flagged.

Four limits keep a bad script from hanging the frame — message hops (64), messages
per tick (10000), steps per call (1,000,000), call depth (256). Each logs once
when it trips, naming the send or call.

Errors are clickable in the log window: they open the script that produced them.

## Where output goes

`log(...)` writes to the **Log** window — script output, runtime errors with
positions, and the engine's own log beside them, with filters per level and per
script. Everything also reaches the terminal and `foundry.log`.

## What play does not change

Entering play snapshots the level; leaving restores it. Attribute writes,
property writes, spawned entities and everything else a script did during play
are discarded. Your authored level is never modified by a running script.
