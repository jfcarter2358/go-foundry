# Foundry Scripting Language (flang)

## About

Foundry scripts are written in **flang**. A script is attached to an object — an
entity, a volume, a sector, an edge, a floor, a ceiling, a chunk, a light, or the
level itself — and reacts to messages that object receives.

The model is Erlang's, narrowed to fit a frame loop. Every object has a **message
box**. The engine posts messages into boxes when things happen in the world
(`:enter:`, `:collide:`, `:anim_end:`), scripts post messages into each other's
boxes with `<-`, and once per frame the runtime drains every box, dispatching each
message to whichever function clause matches it.

There are no threads. The runtime is single-threaded and cooperative, and it runs
inside one frame of the game loop. What looks like concurrency is a queue drained
in order.

---

## Execution model

Once per frame, during play:

1. The engine derives events from the world it has just simulated — overlaps,
   collisions, animation transitions, expired timers — and posts them.
2. The runtime **drains the queue to a fixpoint**: it delivers a message, runs the
   handler, and any messages that handler sends are appended to the same queue and
   delivered in the same frame.
3. Deferred engine work (geometry rebuilds, texture loads, spawns) is applied.

Draining to a fixpoint is what makes `hit → damage → die → open_door` resolve
within one frame. Delivering one hop per frame instead would spread that chain
across three frames of variable length, making behaviour depend on framerate.

The queue is a **single FIFO for the whole runtime**, not one queue per object.
Cross-object ordering is therefore deterministic and reproducible, which is what
makes script behaviour testable at all.

Scripts do not run while the editor is active, except through the script editor's
explicit **Run on the editor level** button.

### Guards

Four independent limits keep a bad script from hanging the frame. Each catches
something the others cannot.

| Limit | Default | Catches |
| --- | --- | --- |
| `max_hops` | 64 | Two objects messaging each other forever. A message sent *while handling* a message inherits `hops + 1`. At the cap the message is **deferred to the next frame, not dropped** — a legitimate deep cascade degrades to one hop per frame and still completes. |
| `max_messages_per_tick` | 10000 | Fan-out explosion: `#tag <- ...` across thousands of members, each re-fanning. The remainder spills to the next frame. |
| `max_steps_per_call` | 1000000 | `while true` inside a single handler, which no message limit can see. |
| `max_call_depth` | 256 | Runaway recursion. Enforced by the interpreter rather than left to the host stack, so a bad script produces a diagnostic instead of taking the engine down. |

Every limit logs once when it trips, naming the file and line of the offending
send or call.

---

## Lexical structure

Comments run from `--` to the end of the line.

```
-- this is a comment
x = 1  -- so is this
```

Identifiers are letters, digits and underscores, and may not begin with a digit.
Keywords are reserved: `def`, `end`, `return`, `if`, `elif`, `else`, `for`, `in`,
`while`, `break`, `continue`, `when`, `and`, `or`, `not`, `true`, `false`, `null`,
`import`, `param`.

Three sigils prefix a name, and each reaches somewhere different: `@name` is an
object in the level, `#name` is a tag across it, and `?name` is a
[parameter](#parameters) the level bound for this object. They share no
namespace, so the same word may be all three at once.

Whitespace and newlines are insignificant except that a comment ends at a newline.
Blocks are delimited by keywords, never by indentation.

---

## Types

Eleven types. The first four are the **scalars**, and they are deliberately the
same four an entity attribute can hold, so a value never needs converting on its
way between a level and a script.

| Type | Literal | Notes |
| --- | --- | --- |
| `int` | `42`, `-7` | 64-bit signed |
| `float` | `0.123`, `9.456`, `-1.0` | Must contain a decimal point. `1` is an `int`; `1.0` is a `float`. |
| `str` | `"hello"` | Double quotes. Escapes: `\"`, `\\`, `\n`, `\t`. |
| `bool` | `true`, `false` | |
| `null` | `null` | The absent value. What a handler returns when it has nothing to say, what a missing `dict` key yields, and what `<-` evaluates to. |
| `atom` | `:damage:` | A literal name used as an enum or an event tag. Lowercase letters and underscores only, colons on both sides. |
| `ref` | — | An opaque handle to a world object. Produced by `@id`, `#tag`, `@self` and a `ref` [parameter](#parameters); never written as a literal. Comparable with `==`. |
| `list` | `[1, "two", :three:]` | Ordered, any element types, growable. |
| `dict` | `{"key": val, "other": 2}` | String keys, any value types. |
| `tuple` | `(a, b, c)` | Fixed length. A one-tuple is `(x,)` — plain `(x)` is grouping. |
| `vec` | `<1.0, 2.0, 3.0>` | Three floats with vector operations built in. Components are read with `.x`, `.y` and `.z`. |

Compound types (`list`, `dict`, `tuple`, `vec`) are things scripts build at
runtime, not things a level authors. A level only ever writes the four scalars.

### Reading `<` and `(`

`<` opens a `vec` when it appears where an expression is expected, and means
less-than when it appears after one. `(` opens a tuple or a grouping where an
expression is expected, and is a call when it follows one.

```
a < b               -- comparison; `<` follows the expression `a`
f(<1.0, 0.0, 0.0>)  -- vec; `<` is where an argument was expected
(1, 2)              -- tuple
f(1, 2)             -- call; `(` follows the name `f`
```

`<-` is always one token and is never read as `<` followed by `-`.

### Vector components

`v.x`, `v.y` and `v.z` read a vec's three floats. They are always `float`,
whatever built the vec, so arithmetic on one never falls into integer division.

```
p = @self.prop.position
if p.y < 0.0
    log("below the floor")
end
```

They are **read-only**, and there is no `v[0]`. A vec is a value rather than a
container — every operator on one hands back a new vec, and the vec a property
gives you is a copy — so `@self.prop.position.y = 1.0` would write to that copy
and quietly do nothing to the entity. Build a new vec instead, which is the one
spelling that always means what it looks like:

```
p = @self.prop.position
@self.prop.position = <p.x, p.y + 1.0, p.z>
```

Both mistakes say this rather than failing obscurely.

---

## Operators

Highest precedence first. All binary operators are left-associative.

| Precedence | Operators | Meaning |
| --- | --- | --- |
| 1 | `a.b` `a[i]` `f(...)` | Member access, index, call |
| 2 | `-a` `not a` | Negation |
| 3 | `*` `/` `%` | Multiply, divide, remainder |
| 4 | `+` `-` | Add, subtract |
| 5 | `<` `<=` `>` `>=` | Ordering |
| 6 | `==` `!=` | Equality |
| 7 | `and` | Conjunction, short-circuiting |
| 8 | `or` | Disjunction, short-circuiting |
| 9 | `<-` | Message send (statement only) |

`+` concatenates strings (`"foo" + "bar"` gives `"foobar"`) and lists. On `vec` it
is component-wise addition; `*` with a `float` scales a `vec`.

Mixing `int` and `float` in arithmetic promotes to `float`. Comparing values of
different types with `==` is `false` rather than an error; ordering them is an
error.

### Assignment

`=` assigns. The first assignment to a name declares it, scoped to the enclosing
function. There is no `let`.

Compound forms: `+=`, `-=`, `*=`, `/=`.

```
x = 1
x += 2
@self.attr.health -= amount
#guards.prop.visible = false
```

---

## Statements

```
if <expr>
    ...
elif <expr>
    ...
else
    ...
end

for <name> in <expr>
    ...
end

while <expr>
    ...
end

break
continue
return <expr>
```

`for` iterates a `list` (yielding elements), a `dict` (yielding keys) or a `tuple`.
`return` with no expression returns `null`.

---

## Functions

```
def <name>(<param>, ...) -> <type>
    <body>
end
```

A parameter is one of:

- `name type` — binds, and only matches a value of that type
- `name` — binds anything
- a literal (`0`, `"go"`, `true`) — matches only that value
- an atom (`:damage:`) — matches only that atom, and takes no type

The return type is a single type, `null`, or a tuple of types `(int, str)`.

### Clauses and pattern matching

A function may have several **clauses**. Calling it selects the matching clause.
Clauses of one function are written as a group: only the first carries `def` and
`->`, and one `end` closes the whole group.

```
def fib(n int) when n > 1 -> int
    return fib(n - 1) + fib(n - 2)
fib(1)
    return 1
fib(0)
    return 0
end
```

All clauses in a group must share the same name, arity and return type.

### Guards

A clause may carry `when <expr>`, evaluated after the head matches. If it is
false, matching continues with the next clause. A guard may call only pure
built-in functions — it may not send messages or mutate anything.

`random()` and `random_range` are built in but are **not** pure, and nothing
stops a guard calling one. Doing so is a bug that looks like a haunting: the
dispatcher evaluates a guard while it is deciding which clause to run, so the
roll a guard makes is not the roll its body sees, and a clause that declined can
be reached anyway on the next message. Roll in the body and keep the answer in
`@self.state`.

If every clause declines, what happens depends on how the function was reached.
A **call** that no clause accepts is an error, the same as calling one whose head
never fitted. A **message** that no clause accepts is ignored, the same as
sending one to an object with no handler for it at all — so a guard is the way to
write a handler that only wants some of its events:

```
def on_tick(:tick:, dt float) when @self.attr.opening -> null
    @self.floor.prop.height -= dt
end
```

The frames where the guard is false are the expected case, not a mistake, and no
catch-all clause is needed beside it.

### Which clause wins

Specificity is resolved when the file loads, not at call time. Each parameter
scores:

| Parameter form | Score |
| --- | --- |
| Literal or atom | 3 |
| Typed binding (`n int`) | 1 |
| Untyped binding (`n`) | 0 |

Clauses are compared element-wise, left to right; the first position where they
differ decides, higher winning. A clause with a guard beats an otherwise identical
clause without one. Remaining ties break by source order.

**Two clauses with identical specificity signatures and no guards are a load
error**, not a coin flip. Ambiguous dispatch is a bug worth seeing.

So in the example above, `fib(0)` is matched over `fib(n int)` when `n` is `0`.

---

## Addressing

| Form | Yields |
| --- | --- |
| `@self` | a `ref` to the object this script is attached to |
| `@some_id` | a `ref` to the object with that ID, or `null` |
| `#some_tag` | a `list` of `ref` for every object carrying that tag |
| `?some_param` | whatever the level bound to a declared [parameter](#parameters) |
| `@player` | a `ref` to the player |
| `@level` | a `ref` to the level itself |

IDs are unique across a level, so `@id` never needs qualifying. Both halves are
authored — the ID on the instance, the tags on the definition it came from plus
any the instance adds.

A `ref` is a first-class value: store it in a `list`, use it as a `dict` value,
compare two with `==`, and pass it in a message.

`ref.id` gives the ID as a `str`. `ref.kind` gives an atom — `:entity:`,
`:model:`, `:volume:`, `:sector:`, `:edge:`, `:floor:`, `:ceiling:`, `:chunk:`,
`:light:`, `:biome:` or `:level:`.

### Reaching a sector's parts

```
@sec.floor            -- the floor
@sec.ceil             -- the ceiling
@sec.edge.<edge id>   -- one edge, whether it is a wall or a portal
@sec.wall.<edge id>   -- the same edge, if it is currently a wall
@sec.portal.<edge id> -- the same edge, if it is currently a portal
@sec.edges            -- every edge, in ring order

@chunk.sector.<sector id>
```

### Reaching an entity's attachments

```
@guard.model.<attachment name>   -- one model attachment
@guard.models                    -- every attachment, in the order the entity holds them
@guard.model.body.entity         -- back to the entity carrying it
```

**Identity belongs to the edge, not to the wall or portal occupying it.** An edge
keeps its ID when the editor converts it between the two, so `@sec.edge.<id>` is
the durable address. `.wall` and `.portal` are spellings of the same address —
which kind an edge currently is, is a property of it (`prop.kind`), and asking
for a wall-only property of an edge that is presently a portal is what reports
the mismatch.

`@sec.edges` may be indexed, but a position in that list is only valid within the
expression that produced it — inserting a vertex or merging a sector renumbers the
ring. Never store an edge index.

---

## Parameters

A script that names the object it acts on is a script for exactly one object.
`param` is how a script stays generic: it declares a value the **level** supplies,
per object, and reads it back as `?name`.

The sigil is `?` and not `@` because the two ask different questions. `@door`
asserts that this level contains an object called `door`; `?door` asks the level
what it wired *this* object's `door` to, and the answer need not be an object at
all.

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

Twenty buttons share that one file. Each one's Script panel in the 3D editor says
which door *it* opens, how far that door drops, and whether it latches.

That is an excerpt; `resources/scripts/button.flang` is the whole of it, latching
included, and is the file to copy.

A declaration is `param <name> <type>` with an optional `= <literal>` default, at
the top level of a file. The type is one of `ref`, `int`, `float`, `bool` or
`str` — narrower than the language's own type list, because a param has to be
stored in a level file and edited in a panel, and neither is true of a `list` or
a `dict`. Anything compound belongs in [`@self.state`](#state).

Reading one:

- **`?name` is the parameter and `@name` never is.** The two namespaces are
  separate, so a script may declare `param door ref` in a level that also has an
  object called `door` without either one shadowing the other.
- **Unbound falls back** to the declaration's default, and then to `null`. So
  `if ?target == null` is the check to write for a param nobody wired up.
- **A `ref` param stores the ID** of the object it names, and resolves it on every
  read. A param pointing at something that does not exist yet starts working the
  moment it does; one pointing at something deleted reads as `null`.
- **An undeclared `?name` is an error**, not a `null` — unlike `@name`, there is
  nothing else it could be, and the set of declarations is closed when the script
  loads.
- **A param is read-only.** It is what the level said, and a script writing one
  would be arguing with its own configuration. Use `@self.attr.<name>` for state
  the script owns, or `@self.state[...]` for state that need not be saved.

Every scriptable object binds its own, under `params:`, beside the `script:` that
declares them:

```yaml
volumes:
  - id: button_north
    script: scripts/button.flang
    params:
      target: {type: str,   value: door_north}
      drop:   {type: float, value: 6.0}
```

An entity definition may bind defaults for its type, which each instance
overrides one name at a time — exactly like attributes:

```yaml
# resources/entities/button.yaml
id: button
script: scripts/button.flang
params:
  latching: {type: bool, value: true}
```

Imports merge parameters into one namespace along with functions, so a library
can declare the tunables its own behaviour needs. Two files declaring one name is
a conflict rather than a shadowing, and is reported.

A **level script** has nowhere to bind them — there is one of it, so there is
nothing to configure per object — and its parameters always read their declared
default. Parameters are for the scripts many objects share.

### In the editor

The Script section of an object's panel lists every parameter its script
declares, one row each. A `ref` row is a picker over the level's IDs — a mistyped
ID is a param that silently reads `null`, and `null` is indistinguishable from
"not wired up yet". A row showing the declared default is marked `(default)`
until you touch it; the `x` beside a bound row hands it back.

Bindings whose declaration went away — a param renamed in the script — are listed
rather than discarded, with a **Clear Unused** button, so a rename does not
silently take your level wiring with it.

---

## Attributes

Objects carry user-defined **attributes**: named values of type `int`, `float`,
`bool` or `str`, authored in the level or the entity definition, saved with it,
and editable in the editor's panels. Entities, volumes, sectors, edges, floors,
ceilings, chunks and lights all have them.

```
@self.attr.health              -- read
@barrel_01.attr.health = 50    -- write
@self.attr.health -= amount
#breakable.attr.health = 0     -- write to every object carrying the tag
```

Reading an attribute that does not exist yields `null`. Writing one that does not
exist creates it, typed from the value assigned.

Attribute writes made during play are **discarded when play ends** — the authored
level is never modified by a running script.

An attribute is state the *script* owns and writes; a [parameter](#parameters) is
configuration the *level* owns and the script only reads. They are stored
separately for that reason — sharing one map would let a script overwrite its own
wiring.

## State

`@self.state` is a `dict` of scratch values that is never saved and is dropped
when the level reloads. Use it for bookkeeping that is not level data.

```
@self.state["opening"] = true
if @self.state["opening"] == null
    ...
end
```

Only `@self.state` is reachable; one object cannot read another's state.

An object made by `spawn` starts with none of it, **including when it has been
handed the name of one that was destroyed**. `spawn` gives out the first free
name of a type, so the grenade thrown after one has gone off is `grenade`
again — and it begins with empty scratch, no memory of having been spawned
before, nothing it was overlapping, and none of its namesake's pending
`after(...)` timers. Everything about an object is keyed by its name, and none
of it outlives the object.

## Properties

Properties are the object's **intrinsic** engine fields — a wall's visibility, an
entity's position, a floor's height.

```
@self.prop.visible = false
@door_wall.prop.collision = false
@sec.floor.prop.height = 4.0
#lamps.prop.brightness = 0.0
```

The full catalogue is generated from the engine's property table and listed in the
editor's reference panel. The core surface:

| Object | Properties |
| --- | --- |
| entity | `position` `rotation` `scale` `velocity` `angular_velocity` `visible` `texture` `bounding` `bound_half` `bound_offset` `hit_height` `collision_world` `collision_entities` `collision_gravity` `collision_movable` `gravity_scale` `max_fall_speed` `on_ground` · `sprinting` (player) · read-only: `gravity` `eye_position` `look_direction` `crouching` `type` `current_sector` `current_chunk` `frame` `local_position` `seat` `waypoints` |
| model | `texture` `visible` `alpha` `offset` `rotation` `scale` `uv_scroll` `uv_offset` · read-only: `path` |
| vehicle | `vehicle_throttle` `vehicle_steer` `vehicle_pitch` `vehicle_roll` · tuning: `vehicle_max_speed` `vehicle_accel` `vehicle_brake` `vehicle_turn_rate` `vehicle_pitch_rate` `vehicle_roll_rate` `vehicle_drag` · read-only: `vehicle_speed` `vehicle_driver` |
| volume | `position` `rotation` `half` `shape` `enabled` `load_level` `gravity` `fog` `fog_color` `fog_density` `fog_max_opacity` · read-only: `attach` `carrier` `waypoints` |
| brush | `solid` · dynamic brushes only: `position` `rotation` `scale` `velocity` `angular_velocity` · read-only: `dynamic` `waypoints` |
| edge | `kind` `texture` `collision` `visible` `pinned` `double_sided` `alpha` `uv_offset` `uv_scale` `uv_rotation` `smooth` `smooth_angle` · portals also: `to_sector` `draw_gap` `gap_texture` |
| floor | `enabled` `hidden` `height` `texture` `double_sided` `alpha` `uv_offset` `uv_scale` `uv_rotation` `smooth` `smooth_angle` · slab: `thickness` `slab_texture` `slab_rim_texture` `slab_hidden` `slab_rim_across_portals` · read-only: `shaped` `world_height` `slab_shaped` |
| ceiling | as floor, without `hidden` |
| chunk | `enabled` `origin` `velocity` · read-only: `rotation` `scale` |
| light | dynamic lights only: `color` `brightness` `cast_shadows` `shadow_strength` · point: `position` `radius` · directional: `yaw` `pitch` · read-only: `dynamic` `attach` `carrier` `waypoints` |
| biome | `density` `color` · Water only: `water_depth` `water_level` `lakebed` · read-only: `ground_texture` |
| sound | `position` `enabled` `playing` `loop` `gain` `pitch` `bus` `ref_distance` `max_distance` `rolloff` · read-only: `clip` `sounding` `waypoints` |
| level | `step_height` `gravity` `player_control` `player_gravity` `player_fly` `camera_fov` `viewmodel_fov` `mouse_sensitivity` `mouse_smoothing` `mouse_invert_y` `master_volume` `music_volume` `effects_volume` `smooth_lighting` `paused` `sky` `sky_kind` `sky_cubemap` `sky_zenith` `sky_horizon` `sky_ground` `sky_tint` `sky_intensity` `sky_rotation` `camera_detached` `camera_position` `camera_yaw` `camera_pitch` `camera_roll` `cursor` · read-only: `load_progress` `load_stage` `dev_build` `camera_direction` `camera_orbit_distance` `look_delta` `cursor_position` `cursor_free` `cursor_over_ui` |

### Moving a brush

A brush is static geometry: its corners are baked into the compiled level, so
writing a placement on one would move a shape the compiled world no longer agrees
exists. Tick **Dynamic** on the brush first and it is left out of that bake, and
`position`, `rotation`, `velocity` and `angular_velocity` become writable:

```flang
def open() -> null
    @blast_door.prop.velocity = <0.0, 2.0, 0.0>
    wait 1.5
    @blast_door.prop.velocity = <0.0, 0.0, 0.0>
end
```

Writing one of them on a brush that is not dynamic is an error naming the brush
and the toggle, rather than a move that works in the editor and silently does
nothing in a shipped build.

`position` is the brush's centre in the space **its chunk sits in**. For a
level's own geometry that is world space, so it compares directly against a
player's or an entity's; for a brush an *entity* carries it is that entity's own
coordinates — see below. Collision, culling, the spatial hash and raycasts all
follow the brush either way — what a body hits is where the brush is drawn.
`velocity` is a rate the engine integrates each tick, so a lift that runs on its
own needs no per-frame handler.

`angular_velocity` is the same idea for turning: radians per second, added to
`rotation` every frame of play, so a fan or a revolving door is one write rather
than a `:tick:` clause.

```flang
@fan.prop.angular_velocity = <0.0, 3.14, 0.0>   -- half a turn a second, forever
@fan.prop.angular_velocity = <0.0, 0.0, 0.0>    -- stop it wherever it got to
```

Unlike `velocity`, it is **not** in world space: it is the rate of change of
`rotation`, which is the brush's own Euler angles about its own centre, so the
two read and write in the same basis. A brush turns in place — the centre
`position` reports does not move — and the angle wraps into ±π as it goes, so a
brush left spinning keeps its precision. Nothing damps either rate: what should
slow a door down is a rule the game has.

Two limits worth knowing: nothing rides a moving brush (a player on a lift is not
carried up by it — attach them to a moving *chunk* instead), and a dynamic brush
cannot be part of the shell that seals a level for compiling.

### Easing to a placement

`velocity` keeps something going; `move_to`, `rotate_to` and `scale_to` get it
*somewhere*, in a set time. Each blends one part of the placement in a straight
line — from wherever the object is when the call runs, to the vec given, over
that many seconds — and the engine carries it the rest of the way. There is no
`:tick:` clause to write and nothing to stop when it arrives:

```flang
def open() -> null
    p = @blast_door.prop.position
    move_to(@blast_door, <p.x, p.y + 3.0, p.z>, 1.5)
    wait 1.5                                   -- it is open now
end
```

| Verb | Works on | The vec is in the terms of |
| --- | --- | --- |
| `move_to(obj, vec, seconds)` | a dynamic brush, a chunk, an entity | `position` — a chunk's `origin` |
| `rotate_to(obj, vec, seconds)` | a dynamic brush, an entity | `rotation`, in radians |
| `scale_to(obj, vec, seconds)` `ai_move_to(agent, place)` `ai_stop(agent)`
`ai_target(agent, entity)` `ai_objective(agent, volume)` `ai_follow_waypoints(agent, path)`
`ai_continue(agent)` `ai_follow(agent, leader)` `find_path(from, to)`
`can_see(entity, target)` | a dynamic brush, an entity | `scale` — `<1.0, 1.0, 1.0>` is as authored |

So the spaces are the properties' own: a brush an entity carries moves in that
entity's coordinates, and an entity's `move_to` is world space.

- **The three are independent.** A door can slide and turn at once, each on its
  own clock. Calling one again replaces that blend, starting from wherever it had
  got to.
- **Zero seconds is immediate** — the same as writing the property.
- **A rotation is blended as numbers**, not the short way round:
  `rotate_to(@fan, <0.0, 2.0 * pi(), 0.0>, 4.0)` is one whole turn, and the angle
  reads back as 2π afterwards rather than wrapping.
- **Whatever else would move that part waits.** `velocity` pauses while the
  position is eased, and `angular_velocity` while the rotation is; an entity
  does not fall. Each takes over again from where the blend left off. A brush or
  an entity following a waypoint animation stops following it, and
  `play_waypoints` drops its blends.
- **The blend wins over a property write.** Writing `position` mid-blend is
  overwritten on the next tick; to snap somewhere instead, call the verb again
  with 0 seconds.
- **It is gameplay.** A pausing screen freezes a blend along with everything else
  that moves, and an entity that collides with the world is still stopped by a
  wall in its way.

The object must be one a script may move: a static brush gets the same "not
dynamic" error writing its `position` does, a chunk can only be moved (its
`rotation` and `scale` are read-only), and the player and vehicles are refused —
their motion belongs to the player's input and the drive model.

### Changing a light

A light is static unless it is marked **Dynamic**, and a static light is baked
into the compiled level's walls and floors. Writing any of its properties, or
sending it along a waypoint animation, is refused with an error naming the
light and the checkbox. Otherwise the change would reach the props and entities,
which are lit live, and not the walls, which were lit at compile time. The
refusal is made in every build, so what works in the editor works shipped.
`dynamic` itself is read-only.

### Waypoint animations

A waypoint animation is a path authored in the level editor — a row of arrows,
each a position, a rotation, a scale and a time — that an object can be sent
along: a dynamic brush, an entity, a sprite, an emitter, a sound, a dynamic light
or a volume. It is an object with an ID like any other, so `@door_open` names it;
the verbs take the name as a string as well. Placing and previewing one is in the
guide's *Building levels* chapter.

```flang
def open(:interact:, who ref) -> null
    play_waypoints(@blast_door, @door_open)
end

def at_point(:waypoint:, src ref, anim ref, index int) -> null
    log("reached point", index)
end

def arrived(:waypoints_end:, src ref, anim ref) -> null
    log("the door is open")
end
```

| | |
| --- | --- |
| `play_waypoints(obj, animation)` | send the object along it from wherever it is now; `false`, with a warning, for an animation with no points |
| `stop_waypoints(obj)` | leave it where it has got to; `false` if it was following nothing |
| `@obj.prop.waypoints` | read-only: the animation it is following, or `null` |

An entity's type can carry animations of its own. They are authored in the
entity editor, in the entity's space, and every placement gets its own copy,
`<entity id>.<name>`, whose points ride the entity. A type's script names one
through the placement, as it names the placement's brushes:
`play_waypoints(@self.hatch, @self.door_close)`. A string such as `"door_close"`
works too. It is looked up as a level ID first, then on the entity the object
belongs to, then on `@self`. An entity cannot follow a path it carries itself.

How an object travels:

- **Point by point, each at its time.** A point's time is when the object
  *arrives* at it, in seconds from being sent — so the first point's time is how
  long the leg from wherever the object was takes, and 0 starts the path on it.
  Two points at the same time make the leg between them instant. The
  animation's **length** may run past its last point, and the object holds that
  point for the rest.
- **Position, rotation and scale ease together**, linearly, so an object that
  turns on its way to a point finishes the turn as it arrives. Rotation is
  blended as numbers, as `rotate_to`'s is: 0 to 4π is two whole turns.
- **Position is world space** — where the arrow is drawn — for every object, and
  it is carried into whatever the object is placed in every tick: a brush's
  chunk, the frame an entity or a volume rides, the thing a sprite, an emitter or
  a sound is attached to. **Rotation and scale are the object's own** `rotation`
  and `scale`.
- **Each kind takes the parts of a point it has:**

  | | position | rotation | scale |
  | --- | --- | --- | --- |
  | dynamic brush | its centre | ✓ | ✓ |
  | entity | ✓ | ✓ | ✓ |
  | sprite | ✓ | ✓ | x its width, y its height |
  | emitter, sound, volume | ✓ | ✓ | |
  | point light | ✓ | | |
  | directional light | | shines the way the arrow points | |

  A directional light's rotation is the turn that points the arrow's shaft along
  the light, so a sun sweeping across the sky is a row of arrows aimed where it
  should shine, and its `yaw` and `pitch` read back where it has got to.
- **The path writes the whole placement.** `velocity` and `angular_velocity` wait
  while a brush or an entity follows it and take over again from the last point,
  and an entity does not fall. `move_to`, `rotate_to` or `scale_to` stops it
  following, and `play_waypoints` drops its blends. A `position` write mid-path
  is overwritten on the next tick.
- **It is gameplay**: a pause freezes it, a disabled chunk's brushes wait, and an
  animation deleted mid-journey leaves the object where it had got to. An entity
  that collides with the world is still stopped by a wall in its way.
- **The player and vehicles are refused**, as `move_to` refuses them: their
  motion belongs to the player's input and the drive model.

The object hears `:waypoint:` at every point, with the point's index counting
from 0, and `:waypoints_end:` when the animation's length runs out — on reaching
the last point, unless the length holds the brush there longer — after which
`waypoints` reads `null`.

A point can also carry **events** of its own, added in its **Events** group in
the editor. Each is an atom posted to the object as `(:atom:, animation ref)`
when it reaches that point, straight after the point's `:waypoint:` and in the
order the point lists them:

```flang
def at_top(:lift_top:, anim ref) -> null
    @top_gate <- (:open:,)
end
```

An event stays with its point when other points are inserted before it, which an
index does not. Event names are atoms (`[a-z_]+`), and the editor refuses the
engine's own.

Previewing an animation in the editor posts none of these.

### A brush an entity carries

An entity is built out of brushes as well as models. A script addresses one of
them the same way it addresses a model attachment: the entity, then the name the
brush has **in the definition** — `@shuttle_2.door` names exactly one solid
however many shuttles the level placed, and every shuttle's own script says
`door`. (The level-unique ID underneath is the two joined, `shuttle_2.door`,
which is what `.id` reads back and what the Explorer shows; a script never has
to spell it.)

Two collections go with it, the counterparts of `.models`:

```flang
for b in @shuttle_2.brushes      -- every brush the entity carries
    b.prop.solid = false
end
@shuttle_2.geometry.prop.enabled = false   -- the one chunk they materialise into
```

`position`, `rotation` and `velocity` on one of them are in the **entity's own
coordinates**, not the world's. A door closing on a ship means "two metres to
port", and that stays true while the ship turns; a world-space write would stop
meaning what it said the moment it did. The entity's own motion carries the
brush on top of whatever the script wrote, which is what makes a lift inside a
moving hull expressible at all:

```flang
-- The hold's blast door, on a ship that is two hundred metres into a fall.
-- Nothing here mentions where the ship is, because nothing has to.
def shut() -> null
    p = @dropship.hatch.prop.position
    @dropship.hatch.prop.position = <0.0, p.y, p.z>
end
```

For a level's own brushes nothing changed: a chunk that no frame owns is placed
by the same matrix either way, so its brushes still read and write in world
space.

### An emitter an entity carries

An entity's type may hang particle effects on it as well as brushes, and a
script reaches one by exactly the same step — the entity, then the name the
emitter has **in the definition**:

```flang
stop_emit(@dropship.thruster)          -- one placement's plume, not every ship's
burst(@dropship.thruster, 40)
@dropship.thruster.prop.rate_scale = 3.0

for e in @dropship.emitters            -- every effect the entity carries
    stop_emit(e)
end
```

The level-unique ID underneath is the two joined, `dropship.thruster`, which is
what `.id` reads back and what the Explorer shows. Every particle verb and every
emitter property works on one of these unchanged, because it *is* an ordinary
emitter — it simply came from the type rather than from the level, and it rides
the entity (or a bone of one of its models) rather than sitting still.

`destroy` on the entity takes its emitters with it, so nothing is left emitting
from where the thing used to be.

The **volumes** a type carries are reached the same way — `@guard.sight` for one,
`@guard.volumes` for all of them — and `destroy` takes those too. Each is an
ordinary volume riding the entity: its own script gets its `:enter:` and
`:exit:`, `@self.prop.carrier` is the entity it rides, and that entity never
counts as entering it.

A surface's `height` is **chunk-local**: the authored number, and the one the
level file stores, so `height = height + 1` on a placed chunk round-trips.
`world_height` is its read-only world-space companion — the same value carried
through the chunk's placement — which is what to compare against a player's or an
entity's Y, since those are world-space:

```
if @crate.prop.position.y - @room.floor.prop.world_height < 0.1
    -- the crate is resting on the floor, wherever that chunk has been put
end
```

The two agree on a chunk that has never been moved or scaled, so a level that
places nothing can use either.

`thickness` is how far a surface's *far side* sits from it — down from a floor,
up from a ceiling — and 0, the default, is the bare plane a surface has always
been. Writing it grows an underside (or a top side) and a rim round the edge,
both textured by `slab_texture` and `slab_rim_texture`, which fall back to the
surface's own texture when left empty. `height` still names the side you walk on,
so raising a floor carries its slab with it and only `thickness` changes how
deep it is.

A slab is **solid**: its underside stops a body from below, so a thick mezzanine
over an open hall can no longer be walked or jumped through. `slab_shaped` reads
whether the far side has been given a shape of its own in the editor rather than
following the surface.

The rim is skipped across a portal edge by default, since the slab continues
into the sector next door there and the neighbour's own rim would sit in the
same place. `slab_rim_across_portals` builds it there anyway, for a slab that
needs a sealed edge even where the level would otherwise carry it through.

A Water biome sector's surface has the same pair, for the same reason.
`water_depth` is the authored number the level file stores — how far *below* the
floor sampled under the polygon the surface sits, so increasing it lowers the
water and a negative value lifts it above that floor and floods flat ground.
`water_level` is its world-space companion, which is the one to animate against
anything else in the world:

```
def on_tick(:tick:, dt float) when @lake.prop.water_level < 12 -> null
    @lake.prop.water_level += 0.4 * dt
end
```

`water_level` is unavailable — reads fail, writes error — until the plane has
actually been built, which means the owning chunk is switched on and at least
one frame has passed since the sector was painted. `water_depth` is always
readable, so a script that runs before anything has drawn should use that.
`lakebed` turns off the ground patch drawn under the surface, for water lying on
a floor that is already textured and detailed.

Writing an entity's `position` places it outright: nothing is swept, nothing is
resolved, and an entity written into a wall is in the wall until something moves
it out. That is what makes it the tool for a teleport or a respawn, and the wrong
one for a step — give a `velocity` and let the collision pass do its work.

### Momentum, impulses and gravity

`velocity` is how fast something is going, in world units a second, and **the
player has one like everything else**. It reads back everything the body is
doing — a shove, a fall, and the player's own walking — so a script asking how
fast the player is moving gets the answer. Writing it sets what the body is
doing and the physics carries on from there: gravity still pulls, walls still
stop it, and the ground still bleeds it off.

The one thing that is not carried between frames is the part the movement keys
are asking for. Holding forward is a continuous request that ends the frame the
key comes up; a blast's shove survives until something spends it. So the two are
added rather than mixed: a shove does not cancel the player's run, the run does
not cancel the shove, and letting go of the walk key does not leave them
skating.

`impulse(ref, vec)` **adds** to a velocity instead of replacing it, and that is
almost always what you want:

```
def on_blast(:explode:, at vec) -> null
    away = @player.prop.position - at
    impulse(player(), normalize(away) * 14.0)
end
```

Writing `velocity` for that would throw away whatever the body was already
doing, so a grenade going off under a running player would stop them dead and
then throw them. `impulse` works on any entity and on `player()`, and the vector
is world-space in both cases.

Gravity itself is three things multiplied together, and each has its own knob:

| where | how | what it means |
|---|---|---|
| the level | `gravity:` in the level file, `@level.prop.gravity` | the ambient pull everywhere, in units a second squared. The engine's default is `36`; about `6` reads as a moon and `0` as no pull at all |
| a region | `gravity:` on a volume, `@vol.prop.gravity` | replaces the level's while a body's centre is inside the volume, and only while the volume is enabled. Where two overlap, the smaller wins |
| a body | `gravity_scale:` in an entity type's `collision:` block or the level's `player:` block, `@e.prop.gravity_scale` | multiplies whichever of the two applies. `0.2` drifts like a feather, `-1` falls upward |

A negative number falls upward, at every level. `@e.prop.gravity` reads back the
answer — what this body is actually falling under, with all three applied — and
is read-only, because there is no single field it corresponds to.

`max_fall_speed` is the terminal speed, per body, in units a second; `0` is the
engine's `30`. It is a cap on falling, not on being thrown: a jump, a launch pad
and an `impulse` all reach whatever speed they were given.

```
-- a zero-g chamber whose gravity a switch restores
def on_switch(:interact:, src ref) -> null
    @vacuum_room.prop.gravity = null     -- null hands the region back to the level
end
```

An entity's `texture` is its attachments' skin: reading it reports what the first
attachment draws with, and writing it reskins them all. One attachment of several
is addressed rather than passed: `@tank.model.turret.prop.texture = "rusted"`.
See **Model attachments** below.

`shaped` reports whether a surface carries an editor-authored mesh (a bump, a
recess, a hole) rather than being flat. It replaced the writable `terrain` flag,
which chose between two representations that no longer both exist — see
`internal/world/surface_mesh.go`.

A chunk's `enabled` is how a level streams. Writing `false` takes the chunk out
of the world completely: nothing draws it, nothing collides with it, it leaves
the spatial hash, and its vertex buffers are released. Writing `true` builds it
again from the same authored data the loader reads. A chunk stays addressable by
name either way — that is what lets one be switched back on — and a level file
can start one switched off with `disabled: true`, in which case it is never
uploaded at all until something asks for it.

```
@east_wing.prop.enabled = true     -- stream the next area in
@west_wing.prop.enabled = false    -- and drop the one behind
```

The buffers are freed and rebuilt between frames rather than at the write, for
the same reason `load_level` is deferred: a script tick runs before the frame's
GL work. Nothing has to be sequenced around that — the flag is what everything
reads.

A chunk's `origin` is where it sits, and writing it moves the chunk with
everything authored in it — its sectors, its floors and ceilings, its biome
paint, the lot. Nothing is rebuilt and nothing is re-baked: every per-chunk
coordinate is chunk-local and is carried into world space through the chunk's
placement, so a move costs one re-bucketing of the spatial hash and no geometry
work at all. It applies at the write, unlike `enabled`, so a location verb later
in the same tick already answers from the new footprint:

```
-- a platform that slides back and forth across the shaft
def on_tick(:tick:, dt float) -> null
    p = @lift.prop.origin
    @lift.prop.origin = <p.x, p.y + 2.0 * dt, p.z>
end
```

A body standing on it is not carried along just by colliding with it — that
still leaves it behind. An entity pinned to the chunk with its own
`attached_chunk` moves the same distance instead (`Entity.AttachedChunk` in
DESIGN.md); an interior meant to be properly ridden — walked around inside,
crossed in and out of — still belongs to a vehicle's frame rather than to a
moved chunk (see `frame:` in DESIGN.md). And the surface heights a script
reads back are still the authored, chunk-local numbers; `world_height` is the
one that reports where the floor has actually ended up. `rotation` and `scale`
stay read-only: only the placement's translation may be written.

A chunk's `velocity` is the same move without the handler. It is world units per
second, added to the origin every frame of play by the engine itself, so the
lift above is one write rather than a `:tick:` clause:

```
@lift.prop.velocity = <0.0, 2.0, 0.0>    -- up, and it keeps going
@lift.prop.velocity = <0.0, 0.0, 0.0>    -- stop it wherever it got to
```

Nothing damps it and nothing stops it — what should slow a platform down is a
rule the game has, exactly as with an entity's own `velocity`. Writing it moves
nothing at that instant: it is a rate, and the move it causes happens on the
next tick, which is also what re-buckets the spatial hash. It is authored state
like any other, so a chunk can carry a `velocity:` in the level file and be
moving from the moment the level loads, and leaving play puts every platform
back where the author left it. A chunk switched off with `enabled` does not
drift while it is out of the world.

The two ways of moving a chunk compose: `velocity` carries it and a write to
`origin` teleports it, which is how a lift that has reached the top goes back to
the bottom. Being carried is still something only a frame does — a body standing
on either kind of moving chunk is left where it was.

`@level.prop.step_height` is how tall a riser the player walks up rather than
being stopped by the wall the height difference makes, in world units. It is the
level's `player: step_height:` block (see DESIGN.md's Level format), and a script
may write it at any time — the movement code asks the level for it once per
frame rather than caching it, so the next step the player takes is the first one
that uses the new value:

```
-- on a volume covering the crawlspace
def on_enter(:enter:, other ref) -> null
    @level.prop.step_height = 0.2    -- in here even a kerb stops you
end

def on_exit(:exit:, other ref) -> null
    @level.prop.step_height = 0      -- 0 hands it back to the default
end
```

Writing `0` — or anything negative — restores the default rather than freezing
the player in place: the default is the player's own height, and a body that
could not step at all would be stopped by the seam between two floors that
differ by a rounding error. Reading always reports the height actually in force,
so a level that never set one reads its default rather than `0`.

`@level.prop.player_control` and `@level.prop.player_gravity` are the spawn
point's **Player control** and **Gravity** switches (`disable_control:` and
`disable_gravity:` under `spawn:`), and the way a script gives them back. With
control off the player's input does nothing — walking, jumping, driving, the
interact key, and looking around with the mouse or the arrow keys, so the view
stays wherever the spawn aimed it.

With gravity off the player does not fall, but they are **weightless rather than
frozen**: whatever momentum they have carries them on, so an `impulse` in a
zero-g corridor drifts them down it. Turning it off mid-fall stops the fall
where it is and leaves the rest of the momentum alone.

`@level.prop.player_fly` is the separate switch for free-fly: the jump and
sprint keys move the body straight up and down, nothing pulls on it, and its
momentum is dropped. It is what the `0` key and the developer console's `fly`
and `noclip` toggle. The two used to be one switch, which made "the player is
weightless" and "the player is a noclip camera" impossible to ask for
separately.

```
-- a spawn with both off, aboard a dropship flying in
def on_arrived(:arrived:) -> null
    @level.prop.player_gravity = true
    @level.prop.player_control = true
end
```

Entering play sets both from the spawn again, and a level with no spawn starts
with both on, so a script that switched them off cannot carry that into the next
level or the next play session.

A script's writes are not saved. They act on the level in memory, and the editor
restores its snapshot when a play session ends, so `step_height` in the level
file stays what the Player block set.

### Sky

The level's sky (see "Sky and fog" in DESIGN.md) is the same idea: a
level-scoped block a script can reach with `sky_*` properties, and a level
with no sky yet gets one at the defaults the moment any of them is written —
the same shortcut a plain volume gets the moment a script writes `fog_*` on
it.

```
@level.prop.sky_kind = :cubemap:
@level.prop.sky_cubemap = "dusk"       -- fills all six faces from one name
@level.prop.sky_tint = <0.9, 0.75, 0.7>
@level.prop.sky_intensity = 1.2
@level.prop.sky_rotation = 45
```

`sky_cubemap` is the editor's own "fill from base name" button as a property:
writing `"dusk"` sets all six faces to `dusk_px`, `dusk_nx`, and so on, and
switches `sky_kind` to `:cubemap:` in the same write. Reading it back
reconstructs the base name from the six faces it finds, or `""` when they were
set by hand and do not agree on one.

`@level.prop.sky = false` removes the level's sky entirely — the same gesture
as unchecking "This level has a sky" in the Environment panel — and `= true`
restores it at the defaults if it had none. There is no separate on/off field
the way fog has `Enabled`: a sky either exists or it does not.

**Easing between two skies.** A straight property write snaps: the very next
frame draws the new state whole. `ease_sky(seconds)` is what turns the *next*
batch of `sky_*` writes into a blend instead — kind, art, colours, tint,
rotation, all of it — cross-fading from whatever the sky looked like the
instant `ease_sky` ran to whatever it is written to look like afterward:

```
def on_enter(:enter:, other ref) -> null
    ease_sky(3)                             -- the next writes blend over 3s
    @level.prop.sky_cubemap = "dusk"
    @level.prop.sky_tint = <0.85, 0.7, 0.65>
end
```

This is one call rather than a property on each of the `sky_*` fields, because
a cubemap cannot be cross-faded by lerping its numbers the way a colour can —
sky_frag.glsl evaluates the sky twice, once for the state being eased away
from and once for the state being eased toward, and mixes the two finished
colours. `ease_sky` is what arms that second evaluation; the ordinary
properties are what still say what the destination looks like.

A duration of `0` or less cancels a blend already in progress instead of
arming one — whatever it was easing toward is what the sky snaps to — and
`ease_sky` returns `false` in that case so a script can tell "eased" from
"snapped". Calling it again before an earlier ease has finished does not
splice the two together: the new one starts from wherever the first one was
*heading*, not from the partway point the screen was actually showing, which
costs one visible snap if a level eases the sky faster than its own transition
finishes. Nothing calls `ease_sky` on every tick, so this is not a case worth
budgeting for.

**The cloud deck.** The sky's volumetric cloud deck (see "Volumetrics" in
DESIGN.md) is reached the same way, through `sky_cloud_*` properties, and a
write gives a level with no sky a sky and a deck at their defaults:

```
@level.prop.sky_cloud_coverage = 0.9       -- clouding over
@level.prop.sky_cloud_altitude = 250
@level.prop.sky_cloud_color = <0.55, 0.55, 0.6>
@level.prop.sky_cloud_wind = <12, 0, 3>
@level.prop.sky_clouds = false             -- off, keeping the numbers
```

The full set is `sky_clouds`, `sky_cloud_coverage`, `sky_cloud_density`,
`sky_cloud_altitude`, `sky_cloud_thickness`, `sky_cloud_range`,
`sky_cloud_scale`, `sky_cloud_erosion`, `sky_cloud_color`, `sky_cloud_wind` and
`sky_cloud_shadows`. Unlike `sky`, `sky_clouds = false` switches the deck off
rather than deleting it — it has an on/off of its own, the way fog does.

`ease_sky` blends the deck along with the rest: its numbers are eased rather
than two decks cross-faded, so clouds thin out and clear instead of one set
ghosting through another. Easing to a sky with no deck clears the one there is.

```
ease_sky(8)
@level.prop.sky_cloud_coverage = 0.1       -- the storm passes
```

Writing an unknown property is an error naming the closest valid one. Some writes
(heights, textures) rebuild geometry; the engine defers that to the end of the
frame and coalesces repeats, so writing a height in a loop costs one rebuild.

---

## What scripts do not have to do

Solidity is a property of an entity type, not something a script arranges: an
entity's definition carries `collision: {world, entities, gravity, movable}`
flags and the engine honours them every frame (see DESIGN.md). A script that
wants a crate to stop at a wall does not write the stopping — it sets the flag
once on the type, or reads `on_ground` to know the crate has landed.

What is still the script's own is *reacting*: `:enter:`, `:exit:` and `:collide:`
are what turn a resolved overlap into game behaviour.

---

## Messages

```
@some_id <- (:damage:, @self, 20)
#breakable <- (:damage:, @self, 5)
@self <- (:retry:,)
```

The right-hand side is a tuple whose **first element must be an atom** naming the
message. It is posted to the target's box and delivered later in the same frame,
or after a delay when the right-hand side is an `after` — see [Timers](#timers).
`<-` is a statement; it evaluates to `null` and cannot be used as a value.

`#tag <- ...` snapshots the tag's members at send time and posts one message to
each, in level order. An object that gains the tag inside a handler does not
receive the in-flight broadcast.

A message whose target has no matching clause is **ignored**, not an error — a
broadcast to a tag whose members mostly do not care is the normal case. Sending to
a `null` ref, or to one whose object has been deleted, is dropped with one warning
per target per session.

### Handling messages

A handler is an ordinary function whose first parameter is the atom.

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

The function's name does not matter for dispatch; the atom does. Handlers in the
same file may share a name or not, as reads best.

Every engine event passes the atom first and a `ref` to whatever triggered it
second. Custom messages are free to use any shape after the atom.

---

## Events

The engine posts these without being asked.

| Atom | Posted to | Arguments after the atom | When |
| --- | --- | --- | --- |
| `:spawn:` | any object | — | The level loaded, or the object was created |
| `:destroy:` | any object | — | The object is about to be removed |
| `:tick:` | any object | `dt float` | Once per frame during play |
| `:enter:` | entity, volume, sector | `other ref` | Another object's origin entered its bounds |
| `:exit:` | entity, volume, sector | `other ref` | …and left again |
| `:collide:` | entity | `other ref` | Two entities overlapped and were pushed apart |
| `:interact:` | entity, volume | `src ref` | The interact key was pressed while inside its bounds |
| `:primary_fire:` / `:primary_fire_end:` | player | — | The primary fire button (left mouse) went down / came back up |
| `:secondary_fire:` / `:secondary_fire_end:` | player | — | The secondary fire button (right mouse) went down / came back up |
| `:throw:` | player | — | The throw key (G) was pressed |
| `:weapon_slot:` | player | `slot int` | A weapon-slot key (1 to 5) was pressed |
| `:weapon_cycle:` | player | `step int` | The mouse wheel or Q asked for the next (`1`) or previous (`-1`) weapon |
| `:reload:` | player | — | The reload key (R) was pressed |
| `:landed:` | player | `speed float` | The player touched the ground after being off it; `speed` is how fast they were falling, in units a second |
| `:left_ground:` | player | — | …and left it again: jumped, walked off a ledge, was thrown, or had the floor taken away |
| `:anim_start:` | entity | `src ref, model str, anim str` | A clip began, including each loop wrap |
| `:anim_end:` | entity | `src ref, model str, anim str` | A clip finished, or a looping one wrapped |
| `:mount:` | vehicle | `src ref, seat str` | Something came aboard; `seat` is empty if it is standing |
| `:dismount:` | vehicle | `src ref, seat str` | …and left again; `seat` is the one it was in |
| `:waypoint:` | the object on the path | `src ref, animation ref, index int` | A brush, entity, sprite, emitter, sound, light or volume on a waypoint animation reached one of its points; `index` counts from 0 |
| `:waypoints_end:` | the object on the path | `src ref, animation ref` | …reached the end of the animation's length, and stopped following it |
| `:target_acquired:` | an AI agent | `src ref, target ref` | The agent noticed something on its Targets list and is now hunting it |
| `:target_lost:` | an AI agent | `src ref, target ref` | …gave up on it after its Lose Time out of sight, or it was destroyed |
| `:target_in_range:` | an AI agent | `src ref, target ref` | The target is in sight within Attack Range; repeated every Attack Interval while it stays there |
| `:ai_arrived:` | an AI agent | `src ref` | The agent reached where `ai_move_to` sent it |
| `:ai_path_failed:` | an AI agent | `src ref` | The agent could not find, or could not follow, a way to where it was going |
| `:objective_reached:` | an AI agent, and the objective volume | `src ref, objective ref` | The agent arrived inside an objective and began performing it |
| `:objective_complete:` | an AI agent, and the objective volume | `src ref, objective ref` | …finished it, straight after the objective's own Action atom, which carries the same arguments |
| `:objectives_done:` | an AI agent | `src ref` | The agent finished every objective on a list that does not loop |
| `:cover_taken:` | an AI agent | `src ref` | The agent reached a hiding spot at a point of its waypoint path that takes cover |
| `:cover_lost:` | an AI agent | `src ref` | …and the spot stopped hiding it, so it is looking for more |
| `:ai_maneuver:` | an AI agent | `src ref` | `on_target: fight`: it is setting off for a new firing position (strafe, advance, back off, flank, cover, search) |
| `:ai_in_position:` | an AI agent | `src ref` | …it reached it or gave up on it, or it is up from a dive |
| `:ai_dodge:` | an AI agent | `src ref` | It is diving aside, from the player's aim or an `ai_dodge` call |
| `:click:` | widget, then its screen | `src ref` | A button, toggle or slider was activated, by pointer or navigation key |
| `:hover:` | widget, then its screen | `src ref` | The pointer or the navigation highlight entered it |
| `:unhover:` | widget, then its screen | `src ref` | …and left again |
| `:change:` | widget, then its screen | `src ref, value` | A slider, toggle or field's value changed; `value` follows the kind |
| `:submit:` | widget, then its screen | `src ref, text str` | Enter was pressed in a text field |
| `:drag_start:` | widget, then its screen | `src ref, pos vec` | A `draggable` widget was pressed and the pointer moved a few pixels with it; `pos` is where the press began, in canvas units |
| `:drag:` | widget, then its screen | `src ref, pos vec, delta vec` | …and moved on, at most once a frame; `delta` is the travel since the last one |
| `:drop:` | widget, then its screen | `src ref, onto, pos vec` | …and was let go; `onto` is the widget it landed on, or `null` |
| `:pointer_down:` / `:pointer_up:` | level, player, and the object under the cursor | `button atom, hit, at` | A mouse button went down / up on the world with the cursor free. See [The pointer](#the-pointer) |
| `:pointer_click:` | level, player, and the object under the cursor | `button atom, hit, at` | A press and a release on the same thing, with no drag between |
| `:pointer_enter:` / `:pointer_exit:` | level, player, and the object | `hit ref` | The cursor moved onto / off an object in the world |
| `:pointer_wheel:` | level, player, and the object under the cursor | `amount float, hit, at` | The wheel turned over the world with the cursor free |
| `:pointer_drag_start:` | level, player, and the object pressed on | `button atom, hit, at` | A button held on the world moved a few pixels |
| `:pointer_drag:` | level, player, and the object pressed on | `button atom, delta vec, at` | …moved on, at most once a frame; `delta` is in window pixels, `at` is under the cursor now |
| `:pointer_drag_end:` | level, player, and the object pressed on | `button atom, hit, at` | …and was let go over `hit` |
| `:pointer_drop:` | the object a drag ended on | `button atom, dragged, at` | Something was dragged onto it and let go |
| `:bound:` | widget or screen that called `bind_key` | `src ref, action str, slot int` | The press `bind_key` was waiting for arrived: bound, cleared (Backspace) or cancelled (Escape) |
| `:cancel:` | screen | — | Escape was pressed while this screen was the topmost modal one |
| `:shown:` | screen | — | The screen was put on the stack |
| `:hidden:` | screen | — | …and taken off it |

A UI event goes to the widget, or to its **screen** when the widget carries no
script of its own. One level of bubbling, not the whole ancestor chain: a
widget's owner is its screen, and routing through every panel in between would
make where a handler runs depend on the layout. `src.id` is the widget's own id
either way, so one handler on a screen can serve every button on it.

`model` is the attachment's name and `anim` is the animation's name as the game
calls it — never the underlying `.fmdl` clip name. A looping clip emits an
`:anim_end:` and an `:anim_start:` per cycle, which is what makes footstep and
cycle-counting scripts writable.

Custom atoms need no declaration. Sending `(:unlock:, @self)` to an object that
defines a clause for `:unlock:` works; sending it to one that does not is
ignored — as is one whose clauses are all guarded and every guard declines. See
[Guards](#guards-1).

### Timers

```
@self <- after(500, :open_done:)
@door <- after(1000, :damage:, @self, 5)
#lamps <- after(250, :flicker:)
```

A timer is a send with a delay on it: everything about `<-` still holds — the
target may be an object, a tag, a list or `null`, a tag is snapshotted when the
timer is set rather than when it fires, and delivery is asynchronous — except
that the message waits that many milliseconds of play time first.
`@level.prop.paused` holds the wait — except for a timer addressed to a screen or
a widget, which keeps counting so a menu's own `after` still comes due (see
[Pausing](#pausing)) — and timers do not survive a level reload. A message whose
target has been destroyed by the time it comes due is dropped.

`after` exists only inside a send. Written on its own it has nobody to deliver
to, and the compiler says so rather than scheduling something that goes nowhere.

### AI agents

An entity with an AI controller (its `ai:` block; see `docs/guide/24-ai.md`)
walks, hunts and works objectives with no script at all. A script reacts to the
events above and overrules the controller with these verbs:

```flang
ai_move_to(@guard, <12.0, 0.0, 4.0>)   -- or an object: walks to its position
ai_move_to(@guard, spot, true)         -- runs there, at ai_run_speed with its run animation
ai_stop(@guard)                        -- cancels ai_move_to
ai_target(@guard, @player)             -- hunt this, however long it is out of sight
ai_target(@guard, null)                -- back to hunting what it can see
ai_objective(@guard, @console)         -- perform this objective now
ai_objective(@guard, null)             -- drop the one it is working
ai_follow_waypoints(@guard, @beat)     -- walk a waypoint path; a third argument loops it
ai_follow_waypoints(@guard, null)      -- stop walking one
ai_continue(@guard)                    -- send it on from the point it is waiting at
ai_follow(@squaddie, @player)          -- keep up with the player as a squadmate
ai_follow(@squaddie, null)             -- stop following
corners = find_path(@guard, @exit)     -- list of vecs, or null when unreachable
seen = can_see(@guard, @player)        -- nothing in the level's geometry in between?
spot = find_cover(@guard, 12.0)        -- a reachable spot hidden from its target, or null
spot = find_cover(@guard, 12.0, {"duck": 0.55, "peek": true})   -- a crouched firing position
ai_dodge(@guard, @player)              -- dive aside across the line from the player
ai_dodge(@guard, g.prop.position, true) -- dive straight away from a grenade
```

`ai_move_to` outranks the agent's target and objectives until it arrives, then
posts `:ai_arrived:`. It returns `false` and warns when the place cannot be
reached, and the agent still walks as near as it can. `find_path` plans for a
player-sized body unless a third argument names the entity to plan for. It
returns `null` rather than a partial route. `can_see` ignores range and field of
view: that is the controller's business. It answers only whether the line
between the two is clear.

`find_cover` is the search a waypoint's Take cover point makes, on demand: the
nearest spot within the radius the agent can reach where level geometry lies
between the threat's eye and the agent's chest and head. The threat is the
agent's target unless `"from"` names an entity or a vec; `"near"` looks round
another place than its feet. `"duck"` asks only that the bottom share of its
height be hidden — 0.55 is a crouch behind something waist high on it — and
`"peek"` that its standing eye there still see the threat, so that rising from
the crouch is enough to shoot back. It answers `null` when there is no such spot,
or nothing to hide from. Send the agent with `ai_move_to(agent, spot, true)`;
`scripts/enemy_combat.flang` is the worked example (a soldier that dashes for
cover when it is shot at, gets down, and pops up to fire).

`hit_height` is how much of its bounds a body presents while it ducks, from its
feet up — `0.6` for a crouch, `1.0` standing. Rounds (`hitscan`, and so every
gun) meet only that much of it, and an agent's sight lines are drawn from and
to that much of it, so a crouch behind a sandbag hides a soldier and robs it of
its view over the top. The bounds themselves are untouched: navigation plans on
them, and a body that changed size with every crouch would ask for a new grid.

An agent walking a waypoint path finds its own way to each point and ignores
the points' times. Each point's AI block (a wait, a speed, how to treat targets,
taking cover, a volume to stop in) is described in `docs/guide/24-ai.md`. It
posts the same `:waypoint:`, point events and `:waypoints_end:` that
`play_waypoints` does. `ai_follow_waypoints` takes `@id` or a name, and a name
also finds a path the agent's own type carries. `ai_continue` returns `false`
when the agent is not waiting at a point.

`ai_follow` makes the agent a squadmate: it keeps a place in a loose group
behind the leader, walks (or runs, at `ai_run_speed`) to keep it, fights from where it
stands, and regroups once the leader is `ai_follow_leash` away. A waypoint path
it is walking comes first, so a script can send a squadmate on a path and it
falls back in at the end. It returns `false` for an agent told to follow itself.

A target that is hidden (`visible = false`), or whose own controller is switched
off, stops being one — a combat script that sets `ai_enabled = false` on death
is also taking the body off every other agent's list, even a target a script
chose.

An entity with no controller refuses the `ai_` verbs by name, and the linter
says so before the script runs.

| Property | | |
| --- | --- | --- |
| `ai_enabled` | bool | whether the controller runs; off stops the agent where it is |
| `ai_state` | atom, read-only | `:idle:` `:moving:` `:wandering:` `:travelling:` `:performing:` `:chasing:` `:engaging:` `:watching:` `:following:` `:taking_cover:` `:in_cover:` `:waiting:` `:escorting:` `:regrouping:` `:disabled:`, or `:none:` without a controller |
| `ai_target` | ref or null | what it is hunting; writing is `ai_target` |
| `ai_objective` | ref or null, read-only | the objective volume it is working |
| `ai_destination` | vec or null, read-only | where it is walking to |
| `ai_on_target` | atom | `:chase:`, `:watch:` or `:ignore:` |
| `ai_targets` | list | `player`, entity IDs and `#tag`s; refs are written as their IDs |
| `ai_objectives` | list | volume IDs and `#tag`s; writing it starts the list over |
| `ai_waypoints` | ref or null, read-only | the waypoint animation it is walking |
| `ai_waypoint_index` | int, read-only | the point it is walking to or waiting at; -1 when it walks none |
| `ai_in_cover` | bool, read-only | hidden at a point that takes cover |
| `ai_waiting` | bool, read-only | waiting at a point of its path |
| `ai_follow` | ref or null | the leader it keeps up with; null too while that leader is gone or down. Writing is `ai_follow` |
| `ai_speed` `ai_accel` `ai_turn_rate` `ai_sight_range` `ai_fov` `ai_hearing_range` `ai_lose_time` `ai_attack_range` `ai_attack_interval` `ai_wander_radius` `ai_follow_distance` `ai_follow_leash` `ai_run_speed` | float | the tuning, writable during play |

Reading any of these on an entity with no controller gives a dull answer rather
than an error, as the vehicle properties do. Writing one is reported.

An objective volume adds `objective_enabled` (bool), `objective_action` (the atom
as a str), `objective_duration` (float) and `objective_agents` (int, read-only:
the agents working it, counting those on their way).

### Vehicles

```
mount(@player, @shuttle, "pilot")   -- seat optional; without it they stand aboard
dismount(@player)
```

Both return a `bool`. `mount` refuses a seat that does not exist, an entity that
is not a vehicle, and anything that would put a frame inside itself — a ship
cannot be driven into its own hangar. Both post `:mount:` / `:dismount:` to the
vehicle, so a door script hears about a boarding it did not arrange.

An entity's `frame` property is a `ref` to the vehicle it is riding in, or
`null`, and `seat` is which seat it occupies — empty while standing on the deck.

A rider stores its position, and so its velocity, in the vehicle's coordinates
— that is what carries it perfectly instead of dragging it along. Scripts never
see those coordinates: the `position` and `velocity` properties read and write
world space, and convert at the boundary. So
`@crate.prop.velocity = <3.0, 0.0, 0.0>` sends the crate east whichever way the
ship is pointing, and the conversion is the identity for anything that is not
aboard something.

### Moving a vehicle without driving it

A vehicle's `velocity` and `angular_velocity` are the ordinary entity properties,
and the drive model integrates those two fields rather than private copies of
them. So a script can shove or spin a hull that nobody is sitting in:

```flang
@shuttle.prop.angular_velocity = <0.0, 1.0, 0.0>   -- a radian a second, and the
                                                   -- hold turns with the hull
```

Two things are different from a plain entity. The rate is read in the vehicle's
**own** axes — pitch about its local X, yaw about local Y, roll about local Z —
because that is the only reading under which a roll rolls about the nose. And
the drive model still runs on top: a spin is shed by `Angular Drag` the way a
velocity is shed by `Drag`, a ground or water vehicle keeps only the yaw of one
(it flattens to a heading every frame, and the property reads back what is
actually happening rather than what was asked for), and only a space vehicle,
which has nothing to push against, keeps whatever it is set spinning at forever.

Reading a `vehicle_*` property of something that is not a vehicle gives zero
rather than an error, since asking a crate how fast it is going is a fair
question with a dull answer. *Writing* one is reported, because a script
steering a barrel is not doing what its author thinks.

The drive tuning is writable during play. "An engine model tuned by attributes"
has to mean tunable while running, or it is a hard-coded model with a
configuration file — a script that makes a car handle differently in the rain
needs those numbers to move.

---

## Standard library

**Math** — `abs` `floor` `ceil` `round` `sqrt` `min` `max` `clamp` `sin` `cos`
`tan` `atan2` `pi()` `random()` `random_range(lo, hi)`

`random()` is a `float` in `[0, 1)`. `random_range` keeps the type it was given,
the way `min`, `max` and `clamp` do — two `int` bounds give an `int`, a `float`
anywhere gives a `float` — and **both ends are included**, so `random_range(1, 6)`
is the six faces of a die and not five of them. An inverted or empty range hands
back the low bound rather than erroring, since that is an author who typed one
number into both places.

```
if random() < 0.25            -- a quarter of the time
    play_sound("creak", @self)
end
spark = random_range(1, 6)    -- an int, 1 to 6
drift = random_range(-0.5, 0.5)
```

The sequence differs from run to run: a game whose loot is the same every
playthrough is not random in the sense anyone means. It is a generator written
out in `internal/flang/random.go` rather than `math/rand`, so a pinned seed
reproduces exactly across builds — `(*Interp).SeedRandom` is what pins one, for
a replay, a test, or a bug report that has to happen twice.

**Strings** — `len` `upper` `lower` `split` `join` `trim` `replace` `contains`
`starts_with` `ends_with` `format`

**Lists and dicts** — `len` `append` `remove` `contains` `keys` `values` `get`
`has` `sort`

**Vectors** — `dot` `cross` `length` `normalize` `distance`

Components are members rather than functions: `v.x`, `v.y`, `v.z`.

**World** — `log(...)` `time()` `player()` `find(tag)` `distance(a, b)`
`play_anim(ref, model, name)` `stop_anim(ref)` `spawn(type, vec)` `destroy(ref)`
`hitscan(atom)` `hitscan(atom, options)` `pick(atom)` `pick(atom, options)`
`screen_ray(point)` `world_to_screen(place)`
`emit(emitter)` `stop_emit(emitter)`
`burst(emitter, count)` `spawn_effect(name, vec)` `attach(obj, carrier)`
`detach(obj)` `impulse(ref, vec)` `move_to(obj, vec, seconds)`
`rotate_to(obj, vec, seconds)` `scale_to(obj, vec, seconds)`

**Camera** — `camera_detach()` `camera_attach(seconds, ease)`
`camera_move_to(place, seconds, ease)` `camera_turn_to(yaw, pitch, seconds, ease)`
`camera_roll_to(degrees, seconds, ease)` `camera_look_at(place, seconds, ease)`
`camera_orbit(target, options)` `camera_zoom(fov, seconds, ease)` — see
[The camera](#the-camera)

**UI** — `show_screen(name)` `hide_screen(screen)` `screen(name)`
`widget(screen, id)` `focus(widget)` `load_level(name)` `quit_game()`
`bind_key(action, slot)` `binding_name(action, slot)` `reset_bindings()`

`spawn` returns a `ref` to the new entity. Entities spawned during play exist only
for that play session.

`play_anim`'s last argument is the name the **entity** gives the animation, not the
name of the clip inside the `.fmdl`. The two are separate on purpose — an entity
can call a clip whatever suits the game — which means authoring a clip in the
model editor is only half the job:

1. In the **model editor**, make the clip. It needs an armature, at least one
   vertex weighted to a bone, and at least one keyframe; a rig missing any of the
   three cannot animate at all.
2. In the **entity panel**, select the attachment, open **Animations**, and
   *Add Animation*. Give it the name scripts will use and point it at the clip.

Only then does `play_anim(@button, "button", "button_press")` find anything. Miss
step 2 and the call returns `false` and writes a line to the log window saying
which of the two lists the name is missing from.

```
play_anim(@guard, "walk")                 -- every attachment that has "walk"
play_anim(@tank, "turret", "traverse")    -- one attachment of several
```

A reskin is a property write, not a verb — `texture` is a field of the thing
wearing it, and there is no reason to spell reading it and writing it two
different ways. Everything that has a texture has the property: an edge, a floor,
a ceiling, an entity, and one attachment of an entity.

```
@door_wall.prop.texture = "steel"              -- a wall
@room.floor.prop.texture = "wet_stone"         -- a floor
@guard.prop.texture = "armour_red"             -- every attachment
@tank.model.turret.prop.texture = "rusted"     -- one attachment
```

The name is resolved against `textures/`. Writing the texture of something that
has none — a light, a volume — is the ordinary "no property called `texture`"
error, and writing one on an entity with no attachments loaded is a warning
rather than an error: the write was legal, it just reskinned nothing.

The texture is uploaded, and any geometry it invalidates rebuilt, at the end of
the frame; repeats within one tick are coalesced. A name with no matching `.png`
in `textures/` is a warning in the log window naming the texture, and the object
keeps drawing what it had.

### Model attachments

An entity is a list of model attachments, not one mesh, so `@guard.model.body` is
how a script names one of them. It is a ref like any other: it has properties, it
can be held in a variable, and `@guard.model.body.entity` steps back out to the
entity carrying it.

```
@tank.model.turret.prop.texture = "rusted"
@guard.model.helmet.prop.visible = false
@ghost.model.body.prop.alpha = 0.3
@tank.model.tracks.prop.uv_scroll = <0.0, 0.8, 0.0>   -- slide its texture along V, 0.8 tiles a second

for m in @wreck.models          -- every attachment of the entity
    m.prop.visible = false
end
```

`bone_position` reads where one of an attachment's bones is right now, in world
space — posed by its animation and carried by its entity. A weapon's `muzzle`
socket is the usual one, for a flash or a sound a shot makes at the barrel:

```
spawn_effect("muzzle_flash", bone_position(@self.model.weapon, "muzzle"))
```

For the player the attachments are the first-person viewmodel, so the point is
where the gun is on screen, carried by the camera.

`recoil(pitch, yaw, recover)` kicks the player's view when a shot goes off.
The view turns up by `pitch` degrees and right by `yaw` over a few hundredths
of a second. `yaw` and `recover` are optional. `recover` (0–1, default 0.7) is
the share of the kick that eases back by itself. The rest stays, so holding
the trigger makes the aim climb, and the player has to pull it back down. A
pull downward while the kick is easing back counts against it, so the two
together never drop the aim below where it started. The kick moves the camera
itself, so the next shot's `look_direction` and `hitscan` aim from the kicked
view.

```
recoil(0.9, random_range(-0.25, 0.25))
```

`look_at(place, seconds)` turns the player's view toward a vec or an object
over that many seconds (at once without them), easing in and out and going the
short way round. It aims again every frame, so a view that moves while it turns
still ends on the point. It is for a cutscene that has taken the controls away:
while `@level.prop.player_control` is off the view stays on the point until
`look_at(null)` lets go. With control on the turn still runs, and the player has
the view back the moment it arrives.

```
@level.prop.player_control = false
look_at(@flagpole_top, 2.5)
```

`offset`, `rotation` and `scale` are the attachment's transform relative to its
entity, so a script can nod a head or slide a hatch without touching the entity
itself. Naming an attachment the entity does not have is an error, the way any
address that resolves to nothing is, and the message names both the entity and
the attachment it looked for.

### Hitscan

`hitscan` casts a ray from the **reticle** — the centre of the screen, which is
exactly where the camera looks — out along the player's view, and posts what it
met back to the caster as a message named by the atom you hand it. Given `from`
and `direction` it casts from anywhere instead, which is how an enemy fires from
its muzzle (see below).

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

It answers with a message rather than a return value for the reason every other
engine answer is one: a shot is a thing that happened, and the handler for "I
hit something" is the same shape whether the cause was a script or the world.
The message carries two arguments, and a third when `"points": true` asks for
it:

| Argument | What it is |
| --- | --- |
| `hit` | A `ref` to the nearest object hit, or `null` if the ray met nothing. With `"all": true` it is instead a `list` of refs, nearest first, which is empty rather than `null` when nothing was hit. |
| `at` | A `vec`: where the ray *stopped* — the surface it ran into, or the far end of its range when nothing stopped it. Useful even when `hit` is `null`, which is what a shot into the sky is; `at.y` is how a handler tells a headshot from a hit in the leg. |
| `points` | Only with `"points": true`. Where each hit *landed*: a `vec` beside the nearest hit (or `null` beside a `null`), or with `"all": true` a `list` of vecs, index for index beside the list of refs. This is what puts an impact on a body — `at` is the wall behind it, since bodies do not stop the ray. |

The two shapes `hit` takes never stand in for one another, so a handler's clause
head says which mode it was written for: `def on_shot(:shot:, hit, at vec)` for
the nearest, `def on_shot(:shot:, hits list, at vec)` for the line of fire.

Bodies do **not** stop the ray — only the level's own shape does. That is what
makes the two modes answer two different questions: the first hit is what a
weapon wants, and the whole list is what a rail gun through a corridor of guards
wants. A cast that stopped at the first entity could answer the first and not the
second.

What comes back is addressed the way everything else is. An entity or a volume
is its own ref; a wall is the edge ref `@sector.edge.<id>` produces, and a floor
or ceiling is its sector's — so `hit.prop.texture = "scorched"` works on whatever
the shot landed on.

#### Options

The optional second argument is a `dict`. Every key is optional; an unknown one
is a typo rather than something to ignore, and is reported with a suggestion —
before the script runs, when the dict is written out literally.

| Option | Type | Default | What it does |
| --- | --- | --- | --- |
| `all` | `bool` | `false` | Report every object in the line of fire instead of the nearest one. Changes `hit` from a ref to a list. |
| `points` | `bool` | `false` | Add the third argument, where each hit landed. Off by default because it changes how many arguments the handler takes. |
| `range` | number | `1000.0` | How far the ray travels, in world units. |
| `spread` | number | `0.0` | The full width, in degrees, of the cone the shot scatters inside. `0` is dead centre down the reticle. |
| `entities` | `bool` | `true` | Entities can be hit. |
| `volumes` | `bool` | `false` | Trigger volumes can be hit. Off by default because a level's rooms are routinely wrapped in one, and including them would make nearly every shot report the room before what was standing in it. |
| `geometry` | `bool` | `true` | Walls, floors and ceilings stop the ray and can be hit. Clearing it is what makes a cast see through walls. |
| `visible_only` | `bool` | `false` | Report only what is drawn: entities and walls whose `visible` property is set. |
| `tag` | `str` | — | Report only objects carrying this tag. |
| `ignore` | `ref` or `list` | — | Refs the ray passes straight through, blocking included. A `null` in the list is skipped, so `{"ignore": @maybe_gone}` needs no guard. |
| `from` | `vec` | the camera | Where the ray starts. A cast with its own `from` can hit the **player's body**, which a cast from the reticle, starting inside it, never does. |
| `direction` | `vec` | the view | Which way the ray goes. Any length but zero; `spread` scatters around it. |

```
hitscan(:shot:, {"all": true, "range": 40.0})          -- everything within 40 units
hitscan(:shot:, {"tag": "enemy", "visible_only": true}) -- tagged, and drawn
hitscan(:shot:, {"geometry": false})                    -- straight through the walls
hitscan(:shot:, {"ignore": @shield})                    -- as if the shield were not there
hitscan(:shot:, {"spread": 3.0})                        -- somewhere in a 3-degree cone
```

An enemy's shot is the same verb cast from its gun. Pass `"ignore": @self` so the
round does not start inside the shooter's own bounds, and the hit is `@player`
when it is the player the round met:

```
muzzle = bone_position(@self.model.weapon, "muzzle")
hitscan(:round:, {"from": muzzle, "direction": @player.prop.position - muzzle,
                  "spread": 3.0, "ignore": @self})
```

`spread` is what stops a hip-fired rifle being a sniper rifle. The shot lands
somewhere inside a cone of that full width, drawn evenly over the cone's *disc*
rather than over its angle — drawing the angle straight would pile most shots
into the middle and leave the rim empty, which reads as a far more accurate gun
than the number says. A shotgun is several casts in a row, each with its own
draw:

```
for i in [0, 1, 2, 3, 4, 5, 6, 7]
    hitscan(:pellet:, {"spread": 9.0, "range": 20.0})
end
```

The draw comes from the running script's own generator, the one `random()` and
`seed_random()` use, so a seeded run scatters its shots the same way twice.

```
def on_fire(:primary_fire:) -> null
    hitscan(:shot:, {"points": true})
end

def on_shot(:shot:, hit, at vec, point) -> null
    if hit != null
        spawn_effect("blood_hit", point)       -- on the body, not the wall behind
        hit <- (:damage:, @self, 10)
    else
        spawn_effect("bullet_impact", at)
    end
end
```

The division between the two kinds of option is what makes them compose.
`range`, `ignore` and `geometry` are **physical**: they change where the ray
goes and what ends it. `tag` and `visible_only` are **reporting** filters,
applied after the ray has already been stopped — so a wall carrying no tag still
ends a tag-filtered cast rather than letting it shoot through the room.

Two things are worth knowing about what is tested. A body — an entity, a volume,
a solid scenery prop — is met at its **bounding volume**, the same one `:enter:`
and the collision resolver work in, so a shot and a shove agree about where a
thing is and an animated model's hitbox does not wander. Geometry is met at its
real triangles, so a sloped floor and a shaped wall are hit where they are drawn.
Solid scenery stops a shot and reports nothing, because nothing in the language
addresses a scenery placement by name; the `at` vec still says where it landed.

There is no player in the editor or before a level has finished loading, so a
cast then does nothing and says so in the log.

A cast always runs down the camera. Anything else that needs the player's aim —
a thrown grenade, a flashlight — reads it as `@player.prop.look_direction`, from
`@player.prop.eye_position`: both are the camera's, in world space, since the
player looks with the camera's yaw and pitch rather than with a `rotation`. On
any other entity they are the eye an AI agent sees from and the entity's own
facing, its local −Z.

```
g = spawn("grenade", @player.prop.eye_position + @player.prop.look_direction * 0.6)
g.prop.velocity = @player.prop.look_direction * 14.0 + <0.0, 3.0, 0.0>
```

`hitscan` takes one more option, `"screen"`: a point on the window in pixels,
`<x, y, 0.0>` from the top-left, to cast through from the camera instead of
through the reticle. It cannot be combined with `from` or `direction`.

### The camera

The camera is the player's eye until a script takes it. Every verb below that
places or turns it **detaches** it from the player first, so a cutscene's first
line can be its first shot:

```
@level.prop.player_control = false
camera_move_to(<12.0, 6.0, -4.0>, 2.0)          -- fly out over two seconds
camera_look_at(@dropship, 1.5)                   -- and keep the ship in frame
...
camera_attach(1.0)                               -- blend back to the player's eye
@level.prop.player_control = true
```

| Verb | |
| --- | --- |
| `camera_detach()` | take the camera off the player and hold it where it is — invisible on its own |
| `camera_attach(seconds, ease)` | give it back, blending from where it is to the player's eye (at once without `seconds`). `false` if it was not detached |
| `camera_move_to(place, seconds, ease)` | slide to a vec or an object's position. Ends an orbit; keeps a look, so a camera can dolly past something while staying on it |
| `camera_turn_to(yaw, pitch, seconds, ease)` | pan and tilt to a facing, in degrees. While orbiting, swings round the target instead |
| `camera_roll_to(degrees, seconds, ease)` | bank; positive leans the camera's top to the right |
| `camera_look_at(place, seconds, ease)` | turn toward a vec or an object and stay on it — an object is followed as it moves. `camera_look_at(null)` lets go |
| `camera_orbit(target, options)` | circle a vec or an object, looking at it. `null` stops; options alone adjust the orbit already running |
| `camera_zoom(fov, seconds, ease)` | ease the field of view (5–120 degrees, lower is closer). Does **not** detach — a scope wants it too |

Every `seconds` is optional and blends from wherever the camera is now; zero or
none is at once. `ease` is one of `:ease_in_out:` (the default — a camera that
starts and stops dead reads as a mistake), `:linear:`, `:ease_in:` and
`:ease_out:`. A new call on a channel replaces the blend already running on it,
from wherever that had got to, so a script can retarget a move halfway through.

**Angles** are degrees in the camera's own convention: yaw `0` looks along +X
and grows turning right (toward +Z), pitch is positive up and stops just short
of ±90, and every turn goes the short way round. `@level.prop.camera_yaw` reads
the same number for whichever camera is live, so reading it and writing it back
is a no-op.

`camera_orbit` starts from **where the camera is**: its distance and direction
to the target become the orbit's first values, so nothing jumps. The options
dict takes `distance`, `yaw`, `pitch` (degrees), `seconds` and `ease`, and each
one given blends to its new value. A second `camera_orbit` with a new target
slides the centre across over the same blend.

```
camera_orbit(@squad_leader, {"distance": 6.0, "pitch": -20.0, "seconds": 1.0})
camera_orbit({"yaw": @level.prop.camera_yaw + 90.0, "seconds": 0.5})   -- a quarter turn round
camera_orbit(null)                                                        -- hold it here
```

**The player's look is left alone.** While the camera is detached the player
keeps their own yaw and pitch — what a save records, what the view goes back to
on `camera_attach`, and where a `hitscan` without `screen` is not aimed any more:
the reticle is the middle of the camera, wherever that is. The walk keys move
the player relative to the **camera's** heading while it is detached, the way a
third-person or fixed-angle game walks. Mouse look turns nothing; it is still
read, as `@level.prop.look_delta` — this frame's turn in degrees, x right and y
up, with the player's sensitivity, smoothing and invert-Y applied — which is how
a script steers an orbit from the mouse:

```
def on_tick(:tick:, dt float) -> null
    d = @level.prop.look_delta
    if d.x != 0.0 or d.y != 0.0
        camera_orbit({"yaw": @level.prop.camera_yaw + d.x,
                      "pitch": @level.prop.camera_pitch + d.y})
    end
end
```

The properties are the same camera: `camera_position`, `camera_yaw`,
`camera_pitch` and `camera_roll` read wherever it is and, written, put it there
at once (detaching it); `camera_detached` is the switch, written `false` to snap
back; `camera_direction` and `camera_orbit_distance` (`null` when not orbiting)
are read-only. A detached camera, like a zoom, belongs to the play session:
leaving play or loading a level gives the view back to the player.

### The pointer

With the cursor captured the mouse is the player's head, and its buttons are
the fire actions. With the cursor **free** it is a pointer at the world: a screen
with `cursor` on frees it, and so does `@level.prop.cursor = true` with no
screen up at all — a strategy view, a build mode. While it is free, a press that
no widget takes is a press on the world, and the mouse buttons and wheel are
the pointer's rather than the fire and weapon-switch actions they are bound to.

What is under the cursor is found the way `hitscan` finds things — from the
camera, through the cursor — and every event carries it:

```
def on_click(:pointer_click:, :left:, hit, at) -> null     -- in the level's script
    if hit != null and hit.kind == "entity"
        @self.state.selected = hit
    elif at != null and @self.state.selected != null
        ai_move_to(@self.state.selected, at)
    end
end
```

| Event | Arguments | When |
| --- | --- | --- |
| `:pointer_down:` / `:pointer_up:` | `button atom, hit, at` | a button went down / came up |
| `:pointer_click:` | `button atom, hit, at` | down and up on the same thing, with no drag between |
| `:pointer_enter:` / `:pointer_exit:` | `hit ref` | the cursor moved onto / off an object |
| `:pointer_wheel:` | `amount float, hit, at` | the wheel turned; positive is away from the user |
| `:pointer_drag_start:` | `button atom, hit, at` | a held button moved a few pixels — where the press began |
| `:pointer_drag:` | `button atom, delta vec, at` | the drag moved: `delta` in window pixels since the last one, `at` under the cursor now |
| `:pointer_drag_end:` | `button atom, hit, at` | the button came up, over `hit` |
| `:pointer_drop:` | `button atom, dragged, at` | to the object a drag ended on: what was dragged onto it |

`button` is `:left:`, `:right:` or `:middle:`, so a clause head can pick one out.
`hit` is the nearest object under the cursor or `null`; `at` is where the ray met
it — or met the floor, for a click on open ground — and `null` only for the sky.
Each event goes to **the level's scripts, the player's, and the object it is
about** when that object has a script: a console handles its own
`:pointer_click:`, and the level handles "the player clicked the ground here".
Over a widget nothing in the world is under the cursor, so hover exits.

A drag moves the camera as naturally as it moves a unit — a right-drag that
orbits, a middle-drag that pans:

```
def on_drag(:pointer_drag:, :right:, delta vec, at) -> null
    camera_orbit({"yaw": @level.prop.camera_yaw + delta.x * 0.3})
end
def on_wheel(:pointer_wheel:, amount float, hit, at) -> null
    camera_orbit({"distance": @level.prop.camera_orbit_distance - amount, "seconds": 0.15})
end
```

`pick(atom, options)` is `hitscan` through the cursor, answering `(atom, hit,
at)` the same way and taking the same options; with the cursor captured the
cursor is the middle of the view. `screen_ray(point)` is the ray through the
cursor, or a window point in pixels, as `{"from": vec, "direction": vec}` — the
dict `hitscan` takes, so `hitscan(:shot:, screen_ray(p))` is a pick through `p`.
`world_to_screen(place)` goes the other way: a vec or an object's window pixel
as `<x, y, depth>`, or `null` behind the camera. `@level.prop.cursor_position`
is the pointer in window pixels (the middle of the window while captured),
`cursor_free` whether it is free, and `cursor_over_ui` whether a widget is
under it.

**Under a screen.** A HUD that is not modal lets the pointer through wherever
it has no widget. A modal screen keeps everything — unless it sets
`click_through: true`, when a click on none of its widgets reaches the world
while the keyboard stays with the screen: a build menu over a level the player
is still placing things in, a tactical pause over one they are still giving
orders in. The events arrive under a pausing screen too — only
`@level.prop.paused` stops them.

### Particles

Four verbs drive particle emitters. The first two look like a pair and are not
quite one:

```
emit(@fountain)        -- start it, switching it on if it was off
stop_emit(@fountain)   -- stop NEW particles; the ones in flight finish
```

`stop_emit` deliberately leaves the plume alone, so a fountain drains rather than
vanishing mid-air. To make everything disappear at once, write the property
instead:

```
@fountain.prop.enabled = false
```

`burst` releases particles now, without waiting for a timed burst to come round.
A third argument names one layer, so an effect with a flash and a smoke plume can
have just the flash fired:

```
burst(@fountain, 40)
burst(@torch, 12, "sparks")
```

`spawn_effect` is the one gameplay reaches for: an effect played once at a point
that no level authored an emitter for. It returns a `ref` to the emitter it
placed, and that emitter removes itself when its last particle dies — so a script
firing one per impact does not fill the level with spent ones.

```
def on_collide(:collide:, other ref, at vec) -> null
    spawn_effect("impact_sparks", at)
end
```

Naming an effect with no matching `.fpar` in `particles/` is reported before the
script runs, with a "did you mean" against what is there.

An **emitter**'s properties are the placement's, not the effect's: `position`,
`rotation`, `enabled`, `emitting`, `rate_scale`, `tint`, and the read-only
`effect`, `live`, `attach` and `carrier`. The effect document itself is shared
between every placement of it, so a script writing to it would change all of them
— which is why there is no property that does.

A **sprite**'s are `position`, `visible`, `texture`, `alpha`, `tint`, `size`,
`scale` and `frame`, plus the same read-only `attach`, `carrier` and `waypoints`.
`size` is a `vec` of width and height, whose `z` is unused. `scale` multiplies
that size as drawn — `x` the width, `y` the height — without changing it; it is
what a waypoint animation's scale writes, and `<1, 1, 1>` is the authored size.

### Decals

A **decal** is a texture projected onto the level's own surfaces: a bullet hole,
a scorch mark, a splatter, a stencil. It is not a quad standing in front of a
wall — its mesh is the wall's own triangles, clipped to the decal's box and
re-textured — so it follows the shape it lands on, bends round its corners, is
occluded by whatever is in front of it, and rides the lift or the hull it was
placed on. The designer's side of it is
[docs/guide/29-decals.md](docs/guide/29-decals.md).

`decal` is `spawn_effect`'s counterpart for the marks a fight leaves. It takes a
texture name and a **point** — not an orientation — and finds the surface there
itself:

```
decal("decal_bullet_hole", at)
decal("decal_bullet_hole", at, {"size": 0.07, "life": 45})
decal("decal_scorch", @crate, {"blend": "multiply", "size": 1.2})
```

A point is what a script has: `hitscan`'s `at` is where the round landed. Making
every weapon script carry a surface normal around instead would be one more thing
to thread through and one more place to get it wrong, so the engine asks the
geometry. Pass `normal` yourself when you already have one and it skips the
search.

It returns a `ref` to the decal, or `null` when there was no surface at that
point — so a script shooting at the sky simply leaves no hole, with nothing to
check:

```
def on_shot(:shot:, hit, at vec) -> null
    if hit != null
        hit <- (:damage:, @self, 10)
    else
        spawn_effect("bullet_impact", at)
        decal("decal_bullet_hole", at, {"size": 0.07, "life": 45})
    end
end
```

The options are `size` (a number for a square, or a `vec` for width and height),
`depth`, `normal`, `roll`, `material`, `blend`, `tint`, `alpha`, `life`, `order`,
`max_angle`, `unlit` and `permanent`.

`blend` is the one worth choosing deliberately: `"alpha"` lays the texture over
the surface, `"multiply"` darkens the surface by it, and `"additive"` adds light.
Multiply is what soot, scorch, oil and dirt want, because one such texture then
reads correctly on every colour it lands on.

Two options govern how long a mark lives. `life` is seconds, and the mark fades
out over the last of them rather than blinking off. Without one it stays until
the cap recycles it: script-placed decals are capped at 192 with the oldest
dropped, because a level that kept every bullet hole a long firefight produced
would grow without bound. `permanent` opts a mark out of that cap and into the
save game — for the few a script places deliberately, not for per-impact ones.
Decals an author placed in the editor are never touched by the cap.

A **decal**'s properties are `position`, `rotation`, `size`, `texture`,
`material`, `tint`, `alpha`, `blend`, `order`, `lifetime`, `visible`, `unlit`,
and the read-only `faces` — the unit `vec` it projects along. `size` is a `vec`
of width, height and projection depth, and a zero `z` leaves the depth alone.

One thing about cost, because it is invisible. Writing `position`, `rotation` or
`size` **re-clips the mesh** out of the world on the next frame; writing `alpha`,
`tint`, `texture`, `material` or `visible` does not, and is free. So fade a decal
through `alpha`, and hide one through `visible` rather than by moving it away.

### Audio

Sound comes in two kinds and the verbs follow the split. A **placed sound** is
level content — a torch, a stream, an engine — with a position, a loudness and
a distance it carries. A **one-shot** is gameplay: an impact, a footstep, a
door, at a place no author could have known about ahead of time.

`play` and `stop` drive a sound the level already places:

```
play(@waterfall)   -- start it, switching it on if it was off
stop(@waterfall)   -- stop it, without giving up the placement
```

`stop` deliberately leaves the placement alone, so a machine switched off can be
switched back on. To give up the voice entirely, write the property instead:

```
@waterfall.prop.enabled = false
```

`play_sound` and `play_sound_at` are what gameplay reaches for. Both place a
sound that removes itself once it has finished, so a script firing one per
impact does not fill the level with spent ones — the same arrangement
`spawn_effect` makes for particles. Both return a `ref` to what they placed.

```
play_sound("clank", @door)              -- at an object
play_sound_at("thud", <3.0, 0.0, 4.0>)  -- at a point
play_sound_at("thud", at, 0.5)          -- quieter
```

The commonest use is an impact handler, where the collision already hands you
the place:

```
def on_collide(:collide:, other ref, at vec) -> null
    play_sound_at("impact", at)
end
```

`play_music` and `stop_music` drive the level's global track, which has no
position and answers to the music slider rather than the effects one:

```
play_music("cavern")        -- crossfades over one second
play_music("cavern", 4.0)   -- over four
stop_music(2.0)
```

`play_music` returns `false` when the named track is already playing, so a
handler that sets the track every tick does not restart it every tick.

A **sound**'s properties are the placement's, not the clip's: `position`,
`enabled`, `playing`, `loop`, `gain`, `pitch`, `bus`, and the three that decide
how far it carries — `ref_distance` (full gain within this radius),
`max_distance` (silent beyond it) and `rolloff`. `clip` is read-only for the
reason `effect` is on an emitter: changing it means reading a file from disk,
and a tick must not do file I/O. `sounding` is read-only and reports whether
the mixer is actually playing it, which is not the same as `playing` — a sound
can be switched on and still be inaudible because it is out of range or because
there is no audio device.

The field is `gain`, not `volume`. `volume` is already a level object type.

Naming a clip with no matching file in `sounds/` is reported once and the
placement kept, exactly as a missing `.fpar` is: the placement is real level
content and the author needs to see it in order to fix it.

### Attaching

A sprite, an emitter, a sound, a volume or a point light can **ride** something, so that smoke follows a ship, a
flame follows the hand holding the torch, dust follows a wheel, a sight cone
follows the guard, and a lantern's light follows the lantern. It is the
same binding the editor's **Attach** panel authors, so an effect bound by a
script and one bound by hand are the same effect.

```
attach(@smoke, @ship)                    -- ride an entity
attach(@flame, @guard, "hand_r")         -- ride a bone of its only model
attach(@flame, @tank, "turret", "muzzle") -- name the attachment too
attach(@wake, "ship")                    -- ride a frame: a vehicle's own space
detach(@smoke)                           -- back to world space
```

Sprites, emitters, volumes and point lights. A volume riding an entity never reports that entity
entering it. A light has to be **dynamic** to be attached, the same rule a
property write on one follows, and a directional light has no position to carry. An **entity** boards a vehicle with `mount`, which has
answers for collision, seats and being thrown off that this does not.

The three-argument form reads the entity's only model attachment, which is how
nearly every entity is authored; an entity with several is an error naming them
rather than a guess, because riding the wrong turret looks like a placement
mistake. A bone that is not in the armature is a warning with a "did you mean",
before the run as well as during it.

The two verbs are deliberately **not** mirror images, and this is the thing to
know about them:

- `attach` leaves `position` alone. Attached, a position is an offset in the
  carrier's space — so `spawn_effect("fire", <0, 0, 0>)` followed by `attach`
  puts the effect *on* the bone, which is the reason to reach for either one.
- `detach` moves the object to wherever it currently is in the world, because a
  local offset means nothing once there is nothing to be local to. A plume
  released from a sinking ship stays over the water rather than snapping back to
  the level origin. Rotation is left as authored.

```
def on_collide(:collide:, other ref, at vec) -> null
    let e = spawn_effect("engine_fire", <0.0, 1.0, -2.0>)
    attach(e, @self)              -- rides me, one unit up and two back
end
```

Reading the binding back: `attach` is the name the editor shows —
`"guard.body.hand_r"`, or empty in world space — and `carrier` is a `ref` to the
entity being ridden, or `null`, so it compares against an address:

```
if @flame.prop.carrier == @guard
    log("still in hand")
end
```

Both are read-only. Binding takes up to three names and a diagnosis for each of
them, which is more than an assignment can say.

Attaching to a **frame** is the vehicle case, and a frame exists once something
is parented to that vehicle — not merely because an entity carries a vehicle
spec. Naming one that does not exist warns and points at the entity of the same
name, which is usually what was meant.

### Screens and widgets

A `.fui` screen is addressed like anything else. `@pause` is a screen that is
up; `@ok` is a widget, when exactly one screen on the stack has one by that
name. Two screens both carrying an `ok` is the ordinary case — two copies of one
prefab — so a name that matches more than one is **refused** rather than guessed
at, and `widget(@pause, "ok")` says which.

```
show_screen("pause")               -- put it up, or raise it if it is already
hide_screen(@pause)                -- or hide_screen("pause")
screen("pause")                    -- the screen, if it is up; null otherwise
widget(screen("pause"), "ok")      -- one widget on it
focus(widget(@pause, "resume"))    -- move keyboard and gamepad focus
load_level("canyon")               -- covered by the `loading` screen
quit_game()                        -- end the game after this frame
bind_key("jump", 1)                -- the next key pressed becomes jump's binding; :bound: follows
binding_name("jump", 1)            -- "Space", "Mouse Left", or "" when empty
reset_bindings()                   -- every action back on its default keys
```

A screen and a widget carry the same tags, attributes, script and params any
level object does, so `#dimmable.prop.alpha = 0.5` reaches widgets, and a button
gets its behaviour the same way a door does.

A widget authored `draggable: true` (any kind — an inventory icon is an image)
can be picked up: pressing it and moving posts `:drag_start:`, a `:drag:` each
frame it moves and `:drop:` when it is let go, naming the widget it landed on.
Positions are in the screen's canvas units, the ones `pos` reads. The widget
does not move by itself; `translate` is a runtime slide from where its layout
puts it, so carrying it is one line:

```
def on_drag(:drag:, src, pos vec, delta vec) -> null
    @self.prop.translate = @self.prop.translate + delta
end
def on_drop(:drop:, src, onto, pos vec) -> null
    if onto == null
        @self.prop.translate = <0.0, 0.0, 0.0>      -- back where it came from
    else
        @self.prop.translate = onto.prop.pos - @self.prop.pos + @self.prop.translate
    end
end
```

`hovered` and `dragging` read what the pointer is doing to a widget, and a
screen's `pointer` is where the pointer is in that screen's canvas units. A
screen's `click_through` lets clicks on its empty space reach the world — see
[The pointer](#the-pointer).

**A widget whose document declares a binding is read-only to scripts.** Writing
`@hud.hp.prop.text` on a label bound to `@player.attr.health` is refused, and
the message names the binding — because the fix is to write *that* instead. A
script write and the binding poll racing each other would be decided by frame
timing, which is not something anyone can debug. A widget with no binding is the
script's to write.

Level changes go through `load_level`, which records a request rather than
loading at the call: loading tears down the runtime executing the script making
it. The frame loop does the work between frames, covered by the `loading` screen,
and `@level.prop.load_progress` is what that screen's bar binds to.

`quit_game` is deferred for the same reason and one more: the window and the GL
context belong to the main thread, and the frame loop is the only place that may
end them. It takes no arguments, and the call returns — the process leaves at the
top of the next frame, not inside the handler.

That is a shipped game. In the editor's play mode there is an editor to go back
to, so `quit_game` ends the play session instead — the same as pressing tilde,
with the level put back — and the engine stays open.

### Pausing

A screen with `pauses: true` freezes the world — the player, vehicles, entities,
particles and the sky — but not scripts: a main menu's handlers have to run, and
so does anything a script is animating behind it. For a pause menu that is only
half a pause, because a `:tick:` counting down a fuse or an `after` spawning the
next wave carries straight on underneath it.

`@level.prop.paused` is the whole pause:

```
def on_shown(:shown:) -> null
    @level.prop.paused = true
end

def on_hidden(:hidden:) -> null
    @level.prop.paused = false
end
```

While it is on, the world stands still as it does under a pausing screen, entity
clips stop as well, and the player can neither move, look around, jump nor
interact. The level's scripts hear nothing: no `:tick:`, no `:spawn:`, no
`:enter:`, `:collide:` or any other engine event, and their timers do not come
due. Play time stops with them, so `time()` stands still. An engine event
recorded in the instant before the pause is held, not lost, and arrives on the
first tick after it lifts. Sound, music and animated textures carry on.

The screen stack is not paused, because the menu that lifts the pause is made of
scripts. Screens and widgets keep receiving `:tick:` and every UI event, and a
timer addressed to a screen or a widget keeps counting — a menu's own `after`
comes due under the pause. A message a menu sends into the level is still
delivered: pausing stops the clock, not the language, so a Restart button can
still tell the level about it.

A write takes effect from the next tick; the rest of the current tick's messages
are delivered. It is one switch, not a count, so whichever script writes it last
wins. Loading a level, and leaving play, both lift it.

**Lift it on `:hidden:`, not beside each `hide_screen`.** `:hidden:` is the one
thing every way off the stack has in common, Escape included — and Escape closes
the topmost modal screen without going through any of its buttons. The shipped
`scripts/pause.flang` does exactly this.

### Video

A `video` widget plays a clip from `video/` — an AVI of motion-JPEG frames, or
an animated GIF. Its transport is properties rather than verbs, on the rule the
rest of the UI follows: showing a screen is something that *happens*, but
whether a film is playing is a fact about the widget.

```
@intro.film.prop.playing = true    -- start it; on a finished clip, start again
@intro.film.prop.time = 12.5       -- seek, in seconds
@intro.film.prop.loop = true
@intro.film.prop.muted = true
@intro.film.prop.volume = 0.6      -- on top of the music slider
@intro.film.prop.video = "outro"   -- a different clip, from its start
```

`prop.time` and `prop.duration` are readable too, which is what a progress bar
binds to. A clip that has not been opened yet reports a duration of zero, and so
does one whose file will not read — a screen author cannot tell those apart
either, so they read the same.

A clip that reaches its end posts `:finished:` to the widget, or to its screen
when the widget carries no script — the same one level of bubbling every UI
event takes. A looping clip never posts it, because it has no end to reach.

```
def on_finished(:finished:, src) -> null
    hide_screen(@intro)
    load_level("chapter1")
end
```

Autoplay is a property of the document rather than something a script has to
remember: a widget with `autoplay: true` starts on the first tick after its
screen goes up, which is what a cutscene screen wants and is one line the
handler does not have to contain. Pausing an autoplaying clip sticks — it does
not restart itself on the next tick.

A hidden video keeps playing. A HUD element faded out for a second and a
background loop behind a menu both expect to come back where they would have
been; stopping the world while nobody is looking is the behaviour nobody can
predict. `@bg.film.prop.playing = false` is how to stop one, and it says so.

### Level exits

The commonest level change needs no script at all. A **volume** carrying a
`load_level` is an exit: walk the player into it and the named level loads,
through exactly the path the verb takes. It is set in the editor, under the
volume's **Level Exit** group, from a list of the levels that exist.

```yaml
volumes:
  - id: cave_mouth
    position: {x: 40, y: 0, z: -12}
    half: {x: 2, y: 3, z: 1}
    load_level: caves
```

Only the **player** takes an exit — a guard on patrol crossing it does not end
the level — and only while the volume is `enabled`, so a locked door is a
disarmed trigger and unlocking it is `@cave_mouth.prop.enabled = true`. It fires
on the way in, once, not every frame the player stands inside.

Place the destination's `spawn:` clear of its own exit back. The player arrives
*inside* a volume they were not in a moment ago, which is an entry like any
other — two levels whose spawns sit in each other's exits will bounce between
themselves.

An exit may also carry a script. Its `:enter:` handler runs in the frame the
load is requested, before the load starts, which is where "save what the player
was carrying, then move on" belongs. `load_level` is what to write instead when
the destination is a decision rather than a fact:

```
def on_enter(:enter:, other ref) -> null
    if @self.attr.key_found
        load_level("vault")
    else
        show_screen("locked")
    end
end
```

### Saves and checkpoints

A **checkpoint** is a volume with `checkpoint: true`, or a script calling
`checkpoint(name)`. Reaching one does two things:

- It saves the game to the `autosave` slot.
- It makes that save the one `retry()` goes back to.

Like an exit, only the player reaches a checkpoint volume, and only while the
volume is `enabled`. Each checkpoint is reached once per play-through, and a
volume's name is its ID. The set of checkpoints already reached is part of the
save, so retrying does not reach the same one again.

```
retry()            -- back to the last checkpoint, or the start of the level
restart_level()    -- the level again from scratch, forgetting its checkpoints
save_game("slot1") -- the player's own save
load_game("slot1") -- false when the slot is empty
save_info("slot1") -- {slot, level, kind, checkpoint, time, date, play_time} or null
latest_save()      -- the newest slot, autosave included: a Continue button
```

Every one of these that changes the game is a request, carried out between
frames, for `load_level`'s reason. A load tears down the runtime that is making
the call, and a save taken halfway through a tick would record a cascade of
messages half delivered. `save_info` and `latest_save` read the file at the
call, so a load screen can fill its buttons on `:shown:`.

A save holds what play has changed, not the level itself. It records:

- every object's fields
- every `.state`
- every pending `after`, with the play clock it is due against
- which objects have already heard `:spawn:`
- who is standing in what
- the player
- the screens that were up

Loading one loads the level fresh and applies those changes on top.
Afterwards:

- `:spawn:` is **not** posted again to anything that already heard it.
- `:enter:` is not posted for a volume the player was already standing in.
- A timer fires when it would have.

What is not kept: a menu's own state, the save screen the player saved from,
and live particles.

Once a checkpoint's save is on disk, `(:checkpoint:, name str)` is posted to the
player and to every screen that is up.

A level's `save_mode:` sets what `save_game` writes there:

- `exact` (the default) saves the game as it stands.
- `checkpoint` saves the last checkpoint reached, or the start of the level
  before the first one.

Read it as `@level.prop.save_mode`. `@level.prop.name` is the running level's
name. `@level.prop.last_checkpoint` is the checkpoint `retry()` would return to,
or `""` if none has been reached. A pause menu reads it to grey out its Load
Last Checkpoint button.

### Fog volumes

A volume may also be **full of fog**. Give one a `fog:` block and the region
fills with it: fog the player can see, walk into and lose sight of things in,
bounded by the volume's shape rather than by a distance from the camera the way
the level's `fog:` is.

```yaml
volumes:
  - id: cellar_damp
    position: {x: 12, y: 1, z: -4}
    half: {x: 6, y: 2, z: 6}
    fog:
      color: {r: 0.55, g: 0.58, b: 0.6}
      density: 0.2          # per world unit travelled INSIDE the region
      max_opacity: 1
```

`density` is per world unit of the view ray that was *inside* the shape, which is
what makes the same number mean the same thing on a doorway and on a ravine —
and what makes a bank read as a body of fog: looking along its length is thick,
looking across it is thin, and walking in fills the screen.

A script switches one the way it switches anything else. The fog is put out by
disabling the *volume* too, since a disabled volume contains nothing:

```
@cellar_damp.prop.fog = false          -- switch the fog off, keep the numbers
@cellar_damp.prop.fog_density = 0.5    -- thicken it
@cellar_damp.prop.enabled = false      -- fog out AND the trigger disarmed
```

Writing any `fog_*` property on a volume that has no fog block gives it one at
the defaults, so a script can fill a plain trigger without the level having to
pre-author an invisible fog on it. Switching `fog` *off* on one is the exception
and leaves it alone — there was nothing to switch.

A fog volume is still a volume, so it still fires `enter` and `exit`. "The player
is in the fog" is usually exactly the event a level wants when there is fog to be
in.

### Cloud volumes

A volume may be **full of cloud** too — ray-marched, lumpy, lit by the sun and
drifting — with a `cloud:` block:

```yaml
volumes:
  - id: thunderhead
    shape: sphere
    position: {x: 0, y: 120, z: 40}
    half: {x: 60, y: 30, z: 60}
    cloud:
      color: {r: 0.6, g: 0.6, b: 0.65}
      coverage: 0.8     # how much of the region is cloud, 0 to 1
      density: 0.4      # per world unit at its densest
      scale: 50         # world units across the largest lumps
      wind: {x: 3, y: 0, z: 1}
```

Its properties follow the fog ones: `cloud`, `cloud_coverage`, `cloud_density`,
`cloud_scale`, `cloud_erosion`, `cloud_softness`, `cloud_color`, `cloud_wind`
and `cloud_shadows`. Writing any of them on a volume with no cloud block gives it
one at the defaults; switching `cloud` off on one that never had any leaves it
alone; disabling the volume clears the cloud with the trigger.

```
@thunderhead.prop.cloud_coverage = 1       -- build the storm
@thunderhead.prop.cloud_color = <0.35, 0.35, 0.4>
@thunderhead.prop.cloud = false            -- clear it, keep the numbers
```

`clears_clouds` is the opposite switch: true on any volume, cloud or not, and no
volumetric cloud is drawn inside its region.

### Where output goes

`log(...)` writes to the **log window** — the `Log` button on the editor toolbar,
or the one in the script editor. It shows script output, runtime errors with the
file and line they happened at, and the engine's own log beside them, with
filters per level and per script. Errors are clickable: they open the script that
produced them.

Everything also reaches the terminal and `foundry.log`, so nothing is lost by
running headless or by reading a crash after the fact.

---

## Importing

```
import("shared/math_helpers.flang")
```

The path resolves relative to the importing file, rooted at `resources/scripts/`.
A path that escapes that root is a load error.

Imported functions merge into the importing file's namespace flat — there is no
qualification. A file is loaded once however many times it is imported. Import
cycles are a load error.

---

## Errors

A runtime error aborts the handler it occurred in. The message is logged with its
file and line, the script's error count increases, and the frame continues. A
script that errors repeatedly within one session is disabled and flagged in the
script editor.

These are errors:

- calling a function with no matching clause
- reading or writing an unknown property
- ordering values of different types
- indexing past the end of a list
- dividing by zero
- exceeding any of the four limits

These are not:

- sending a message nobody handles
- reading an attribute that does not exist (yields `null`)
- reading a `dict` key that does not exist (yields `null`)

The engine never crashes on script input.

### Warnings

A verb that reports failure as `false` rather than as an error is the worst case
to debug, because a script that ignores the result — nearly every script — gets
silence. Each of those paths logs a **warning** instead: the line that made the
call, and a sentence naming the fix.

| Warning | What it means |
| --- | --- |
| `play_anim did nothing` | No such attachment, a rig that cannot animate, an animation the entity never declared, or a declared animation pointing at a missing clip — it says which |
| `writing @x.prop.texture did nothing` | The entity has no model attachments, so there was nothing to reskin |
| `destroy did nothing` | The ref names geometry, or something already gone |
| `attach did nothing` | No bone by that name, a model with no armature, or an entity with several attachments and no say which |
| `find(...) matched nothing` | No object carries that tag |
| `player() is null` | There is no player — the editor, or before the level finished loading |
| `hitscan did nothing` | There is no player to cast from, so no answering message was posted |
| `nothing handled :atom:` | A `<-`, prompt or delayed, reached an object whose script has no clause for it |

Warnings are logged once per distinct message per load, so a broken call in a
`:tick:` handler costs one line rather than sixty a second. Reloading the script
clears that memory, so a fix that did not take says so again.

Engine events are exempt from the last one: `:tick:` is posted to every scripted
object and handled by almost none, so only script-originated sends are reported.

### Issues, before anything runs

A script can parse perfectly and still do nothing, because parsing cannot know
what is in the level. The script editor checks the buffer against the open level
and lists what it finds under the error pane, in amber:

- `@name` and `#tag` that match nothing, with the nearest name that does
- `play_anim` naming an attachment or animation that is not there, and a
  `texture` write naming a texture that is not there or an entity with nothing
  to reskin — the same sentences the runtime would log
- `spawn` naming a type with no definition file
- `attach` binding something that cannot ride, riding something that is not an
  entity, or naming a bone the model does not have
- an unknown property, or a call to a function nothing defines
- `@self <- (...)`, delayed or not, with an atom this script has no clause for
- `after` written as a plain call, with no target to deliver to
- a handler for an atom that is *nearly* an engine event — `:intract:` for
  `:interact:`

Only literal arguments are checked: `play_anim(@self, name)` is skipped, because
neither value is knowable before it runs. Issues never block a run — a script
shared between levels will legitimately name objects this one does not have — and
they are re-checked whenever scripts reload, so the same list appears in the log
window when a level loads.

---

## The script editor

`Scripts` on the editor toolbar, or the arrow beside a script name in any object
panel, opens it.

**Tabs.** Every script you open gets one, and they stay open — reading one file
while writing another does not cost you your place in either. `Ctrl+W` closes the
current tab (asking first, if it has unsaved edits), `Ctrl+Tab` and
`Ctrl+Shift+Tab` move between them, and the `+` at the end of the strip opens a
scratch buffer that has never been saved. A tab with unsaved work is marked.

**Folders.** The sidebar is the real folder tree under `resources/scripts/`, so a
folder here is the folder `import("shared/math.flang")` resolves through. *New
Folder* makes one — including an empty one, which then stays visible until you
put something in it — and right-clicking a script offers *Move or rename*, which
takes a whole path, so moving a file into a folder and renaming it are the same
operation. Dragging a script onto a folder row does the same move without the
typing; hold the drag over a closed folder to open it, and drop on the
`scripts/` row at the top to bring a file back out to the top level. Nothing
that *names* the old path is rewritten: levels and entity definitions live
outside the scripts folder, and the lint pass reports the dangling reference the
next time that level is opened.

**Deleting.** *Delete* is on the same right-click menu, and is the one file
action that asks first — it removes the file from disk for good. The
confirmation says what will notice: whether this level names the script, and
which other scripts import it, since an import is written in the importing
script and nowhere the level could tell you about. An open tab on that file is
closed with it, unsaved text and all, rather than left holding a buffer whose
next save would quietly put the file back.

Right-clicking a folder offers *Delete folder*, which removes it and everything
under it — scripts, sub-folders and all — after the same kind of confirmation:
it lists the scripts that go, which of them have unsaved edits in an open tab,
which the level names, and which scripts *outside* the folder import them. An
import between two scripts in the folder is not mentioned, since both ends go
together. Every tab on a script in the folder is closed. The `scripts/` row at
the top has no delete.

**The buffer** is a normal text field with syntax colouring painted over it and
numbered lines beside it. A line with a parse error is numbered in red and one
with an issue in amber, so a problem stays findable while you scroll past it.
Cut, copy and paste use the system clipboard, `Ctrl+Z` / `Ctrl+Y` undo and redo,
`Shift` with the arrow keys or with a click extends the selection, and `Tab`
inserts a tab rather than moving on. `Esc` does nothing at all — it is swallowed
before ImGui can read it as "revert this field", which in a script buffer would
throw away every edit since the last save. The window closes from its own close
button and nothing else; every tab keeps its unsaved text, and reopening the
editor comes back to exactly where you were.

**Completion.** A list of what can go where the caret is opens as you type, and
what it offers depends on what you have written:

| After | It offers |
| --- | --- |
| a name | this function's variables and parameters, this file's `def`s, what it imports, the standard library, then the keywords |
| `@` | `self`, `player`, `level`, and every object id in the open level, each with its kind |
| `#` | every tag anything in the level carries |
| `?` | this file's `param` names, then those its imports declare |
| `:` | every event the engine posts, then every atom this file already mentions |
| `.` | `prop`, `attr`, `state`, `id`, `kind`, and the structural steps the object in hand actually has |
| `.prop.` | that kind's properties, with what each means and whether a script may write it |
| `.attr.` | the attributes that object is carrying |
| `.prop.<a vec>.` | `x`, `y`, `z` |
| `param name ` / `-> ` / a `def` head | the type names that belong there |

**Typing is the only thing that opens the list.** A caret that arrives by arrow
key, by click or by a scroll never does — moving between lines would otherwise
cost an `Esc` on every line that happened to end in a word — and moving off a
word closes it again. While you are typing, a name opens the list after one
character, and `@`, `#`, `?`, `:` and `.` open it with nothing typed at all,
since only one kind of thing can follow any of them. `Ctrl+Space` opens it anywhere,
keystroke or not. `Up` and `Down` move, `Tab` accepts, `Enter` accepts too — except when
the selected row is already exactly what is typed, where it stays a newline —
clicking a row accepts that one, and `Esc` dismisses the list for the word you
are on. Accepting a function brings its parentheses, and accepting an atom brings
its closing colon, in both cases only if they are not already there. `Ctrl+Z`
takes a completion back the way it takes back typing.

The lists are the engine's own. The builtins and keywords come from the
language's tables, the properties from the engine's property table, and the ids,
tags and attributes from the level that is open — so a completion list cannot
describe a version of flang, or a level, that is not the one in front of you.
It also means `@` offers nothing useful with no level loaded, which is the honest
answer rather than a stale one — while `?` still does, since a file's parameters
are declared in the file.

The kind of an object is what narrows `.prop.` to one list, and that is only
knowable when the chain starts from a name the level has: `@door.prop.` offers an
entity's properties, `@sec.floor.prop.` a floor's, and `src.prop.` — where `src`
is a handler's parameter, and no amount of reading the file will say what the
level will pass — offers the union of every kind's, each row labelled with the
kind it belongs to. Picking one that does not apply is not silently wrong: the
[issues](#issues-before-anything-runs) pass names the kind and the property on
the next parse.

**F1** opens the reference: the keybindings, every event the engine posts, every
standard-library function with its signature and what it returns, and every
property of every object kind with whether a script may write it. It is generated
from the same tables the language itself uses, so it cannot describe a version of
flang that is not the one running.

---

## Attaching a script

Scripts live under `resources/scripts/` as `.flang` files, and may be organised
into subdirectories.

An **entity definition** names the script for its type; an instance may override
it, or set it to `""` to detach:

```yaml
# resources/entities/barrel.yaml
id: barrel
script: scripts/barrel.flang
```

```yaml
# in a level
entities:
  - type: barrel
    id: barrel_01
    script: scripts/special_barrel.flang   # overrides the type's
```

Whatever names a script may also bind its parameters, under `params:` — see
[Parameters](#parameters).

Sectors, edges, floors, ceilings, chunks, lights and volumes each take a `script:`
of their own. The level takes a list, for rules that belong to no single object:

```yaml
scripts:
  - scripts/rules/win_condition.flang
  - scripts/rules/spawning.flang
```

In a level script, `@self` is `@level`.

The player, which no level file describes as an entity, takes its script from
the level's `player:` block — with `attributes:` and `params:` beside it, as an
entity type has. In that script `@self` is `@player`, and every play session
starts it from those attributes again:

```yaml
player:
  script: scripts/player_combat.flang
  attributes:
    health: {type: int, value: 100}
```

### Beside a decision tree

An entity may also carry a **decision tree** — a `.fai` document drawn in the AI
editor rather than written — with `ai_tree:`, on exactly the rule `script:`
follows:

```yaml
# resources/entities/enemy_rifle.yaml
script: scripts/enemy_combat.flang
ai_tree: soldier                      # resources/ai/soldier.fai
```

A tree holds the branching: which reaction wins, how likely it is, how long
before it may happen again, what to remember. The script holds what is
genuinely computational. They are two halves of one behaviour, not two
behaviours, because they run on the same interpreter:

- the tree's `call` verb enters this script's functions directly, and can put
  what one returns on the blackboard
- the tree's `set` verb writes the same dict this script reads as
  `@self.state["..."]`
- the tree's `send` posts into the same queue, delivered under the same rules,
  so a tree and its script can send each other messages
- a tree's `params:` are declared **to** this script: `?grenade_chance` means
  the same number in both, and the entity's own `params:` block sets it for
  both. Declaring one name in both places is reported as a conflict rather than
  resolved one way.
- `random()` draws from one generator, so `seed_random` pins both halves

When a message arrives at an object carrying both, **the tree hears it first** —
deciding comes before acting — and either half handling it counts as handled.

A tree can be checked without launching anything:

```
foundry ai check soldier -script scripts/enemy_combat.flang
```

See `docs/guide/24-ai.md` for the editor and the node vocabulary.

---

## Worked example

A door that opens when something enters its trigger volume, and relights when the
closing animation finishes.

```
-- resources/scripts/door.flang

def on_enter(:enter:, other ref) -> null
    if @self.attr.locked
        return
    end
    if @self.state["open"] == true
        return
    end
    @self.state["open"] = true
    play_anim(@door_frame, "panel", "open")
    #door_lights.prop.color = <0.0, 1.0, 0.0>
end

def on_exit(:exit:, other ref) -> null
    if @self.state["open"] != true
        return
    end
    @self.state["open"] = false
    play_anim(@door_frame, "panel", "close")
end

def on_anim(:anim_end:, src ref, model str, anim str) when anim == "close"
    -> null
    #door_lights.prop.color = <1.0, 0.0, 0.0>
    @self.attr.uses += 1
end
```

```yaml
# in the level
volumes:
  - id: door_trigger
    shape: box
    position: {x: 0.0, y: 1.0, z: 4.0}
    half: {x: 2.0, y: 1.5, z: 0.5}
    tags: [trigger]
    script: scripts/door.flang
    attributes:
      locked: {type: bool, value: false}
      uses:   {type: int,  value: 0}
```

The lights tagged `door_lights` are marked **Dynamic**, since the script
recolours them. A static light would refuse the write. See
[Changing a light](#changing-a-light).

---

## Grammar

```ebnf
file        = { import | paramdecl | funcgroup } ;
import      = "import" "(" string ")" ;
paramdecl   = "param" ident paramtype [ "=" literal ] ;
paramtype   = "ref" | "int" | "float" | "bool" | "str" ;

funcgroup   = "def" clause { clause } "end" ;
clause      = ident "(" [ params ] ")" [ guard ] [ "->" rettype ] block ;
params      = param { "," param } ;
param       = ident type | ident | literal | atom ;
guard       = "when" expr ;
rettype     = type | "null" | "(" type { "," type } ")" ;
type        = "int" | "float" | "str" | "bool" | "atom" | "ref"
            | "list" | "dict" | "tuple" | "vec" ;

block       = { stmt } ;
stmt        = assign | send | if | for | while
            | "break" | "continue" | "return" [ expr ] | expr ;
assign      = lvalue ( "=" | "+=" | "-=" | "*=" | "/=" ) expr ;
lvalue      = ident | postfix ;
send        = target "<-" ( tuple | after ) ;
after       = "after" "(" expr "," expr { "," expr } ")" ;
target      = "@" ident | "#" ident | "?" ident | postfix ;

if          = "if" expr block { "elif" expr block } [ "else" block ] "end" ;
for         = "for" ident "in" expr block "end" ;
while       = "while" expr block "end" ;

expr        = or ;
or          = and { "or" and } ;
and         = equality { "and" equality } ;
equality    = ordering { ( "==" | "!=" ) ordering } ;
ordering    = additive { ( "<" | "<=" | ">" | ">=" ) additive } ;
additive    = multiplicative { ( "+" | "-" ) multiplicative } ;
multiplicative = unary { ( "*" | "/" | "%" ) unary } ;
unary       = [ "-" | "not" ] postfix ;
postfix     = primary { "." ident | "[" expr "]" | "(" [ args ] ")" } ;
primary     = literal | atom | vec | list | dict | tuple
            | ident | "@" ident | "#" ident | "?" ident | "(" expr ")" ;

literal     = int | float | string | "true" | "false" | "null" ;
atom        = ":" lowername ":" ;
vec         = "<" expr "," expr "," expr ">" ;
list        = "[" [ expr { "," expr } ] "]" ;
dict        = "{" [ string ":" expr { "," string ":" expr } ] "}" ;
tuple       = "(" expr "," [ expr { "," expr } ] ")" ;
args        = expr { "," expr } ;
```

`vec` and `tuple` are only recognised where an expression is expected; after an
expression, `<` is ordering and `(` is a call.
