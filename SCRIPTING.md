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
| `ref` | — | An opaque handle to a world object. Produced by `@id`, `#tag` and `@self`; never written as a literal. Comparable with `==`. |
| `list` | `[1, "two", :three:]` | Ordered, any element types, growable. |
| `dict` | `{"key": val, "other": 2}` | String keys, any value types. |
| `tuple` | `(a, b, c)` | Fixed length. A one-tuple is `(x,)` — plain `(x)` is grouping. |
| `vec` | `<1.0, 2.0, 3.0>` | Three floats with vector operations built in. |

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
| `@some_param` | whatever the level bound to a declared [parameter](#parameters) |
| `#some_tag` | a `list` of `ref` for every object carrying that tag |
| `@player` | a `ref` to the player |
| `@level` | a `ref` to the level itself |

IDs are unique across a level, so `@id` never needs qualifying. Both halves are
authored — the ID on the instance, the tags on the definition it came from plus
any the instance adds.

A `ref` is a first-class value: store it in a `list`, use it as a `dict` value,
compare two with `==`, and pass it in a message.

`ref.id` gives the ID as a `str`. `ref.kind` gives an atom — `:entity:`,
`:volume:`, `:sector:`, `:edge:`, `:floor:`, `:ceiling:`, `:chunk:`, `:light:`,
`:biome:` or `:level:`.

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
per object, and reads it back as `@name`.

```
param target ref
param drop float = 3.0
param latching bool = true

def on_interact(:interact:, src ref) -> null
    if @target == null
        return
    end
    @target.floor.prop.height = @target.floor.prop.height - @drop
end
```

Twenty buttons share that one file. Each one's Script panel in the 3D editor says
which door *it* opens, how far that door drops, and whether it latches.

A declaration is `param <name> <type>` with an optional `= <literal>` default, at
the top level of a file. The type is one of `ref`, `int`, `float`, `bool` or
`str` — narrower than the language's own type list, because a param has to be
stored in a level file and edited in a panel, and neither is true of a `list` or
a `dict`. Anything compound belongs in [`@self.state`](#state).

Reading one:

- **`@name` is the parameter**, not an object called `name`. A declaration
  shadows the world: the script declared the name, and the level it happens to be
  running in did not.
- **Unbound falls back** to the declaration's default, and then to `null`. So
  `if @target == null` is the check to write for a param nobody wired up.
- **A `ref` param stores the ID** of the object it names, and resolves it on every
  read. A param pointing at something that does not exist yet starts working the
  moment it does; one pointing at something deleted reads as `null`.
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
| entity | `position` `rotation` `scale` `velocity` `angular_velocity` `visible` `texture` `bounding` `bound_half` `bound_offset` `collision_world` `collision_entities` `collision_gravity` `collision_movable` `on_ground` · read-only: `type` `current_sector` `current_chunk` `frame` `seat` |
| vehicle | `vehicle_throttle` `vehicle_steer` `vehicle_pitch` `vehicle_roll` · tuning: `vehicle_max_speed` `vehicle_accel` `vehicle_brake` `vehicle_turn_rate` `vehicle_pitch_rate` `vehicle_roll_rate` `vehicle_drag` · read-only: `vehicle_speed` `vehicle_driver` |
| volume | `position` `rotation` `half` `shape` `enabled` |
| edge | `kind` `texture` `collision` `visible` `pinned` `double_sided` `alpha` `uv_offset` `uv_scale` `uv_rotation` `smooth` `smooth_angle` · portals also: `to_sector` `draw_gap` `gap_texture` |
| floor | `enabled` `hidden` `height` `texture` `double_sided` `alpha` `uv_offset` `uv_scale` `uv_rotation` `smooth` `smooth_angle` · read-only: `shaped` |
| ceiling | as floor, without `hidden` |
| chunk | `origin` `rotation` `scale` `frame` — all read-only |
| light | `color` `brightness` `cast_shadows` `shadow_strength` · point: `position` `radius` · directional: `yaw` `pitch` |
| biome | `kind` `density` `range` `color` `ground_texture` |
| sound | `position` `enabled` `playing` `loop` `gain` `pitch` `bus` `ref_distance` `max_distance` `rolloff` · read-only: `clip` `sounding` |

An entity's `texture` is its attachments' skin: reading it reports what the first
attachment draws with, and writing it reskins them all. `set_texture` is the same
write, plus the form that names one attachment of several.

`shaped` reports whether a surface carries an editor-authored mesh (a bump, a
recess, a hole) rather than being flat. It replaced the writable `terrain` flag,
which chose between two representations that no longer both exist — see
`internal/world/surface_mesh.go`.

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
message. It is posted to the target's box and delivered later in the same frame.
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
| `:anim_start:` | entity | `src ref, model str, anim str` | A clip began, including each loop wrap |
| `:anim_end:` | entity | `src ref, model str, anim str` | A clip finished, or a looping one wrapped |
| `:mount:` | vehicle | `src ref, seat str` | Something came aboard; `seat` is empty if it is standing |
| `:dismount:` | vehicle | `src ref, seat str` | …and left again; `seat` is the one it was in |
| `:click:` | widget, then its screen | `src ref` | A button, toggle or slider was activated, by pointer or navigation key |
| `:hover:` | widget, then its screen | `src ref` | The pointer or the navigation highlight entered it |
| `:unhover:` | widget, then its screen | `src ref` | …and left again |
| `:change:` | widget, then its screen | `src ref, value` | A slider, toggle or field's value changed; `value` follows the kind |
| `:submit:` | widget, then its screen | `src ref, text str` | Enter was pressed in a text field |
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
after(500, :open_done:)
after(1000, :damage:, @self, 5)
```

Posts the message to `@self` after the given number of milliseconds of play time.
Timers do not survive a level reload.

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

**Strings** — `len` `upper` `lower` `split` `join` `trim` `replace` `contains`
`starts_with` `ends_with` `format`

**Lists and dicts** — `len` `append` `remove` `contains` `keys` `values` `get`
`has` `sort`

**Vectors** — `dot` `cross` `length` `normalize` `distance`

**World** — `log(...)` `time()` `player()` `find(tag)` `distance(a, b)`
`move_to(ref, vec)` `set_velocity(ref, vec)` `play_anim(ref, model, name)`
`stop_anim(ref)` `set_texture(ref, name)` `spawn(type, vec)` `destroy(ref)`
`after(ms, atom, ...)` `emit(emitter)` `stop_emit(emitter)`
`burst(emitter, count)` `spawn_effect(name, vec)` `attach(obj, carrier)`
`detach(obj)`

**UI** — `show_screen(name)` `hide_screen(screen)` `screen(name)`
`widget(screen, id)` `focus(widget)` `load_level(name)`

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

`set_texture` reskins whatever it is given — an edge, a floor, a ceiling, or every
model attachment of an entity — with a texture name resolved against `textures/`.
Like `play_anim` it has a three-argument form, `set_texture(ref, model, name)`,
which reskins one attachment of an entity that has several. It returns whether it
found anything to reskin, so naming an attachment the entity does not have is
`false` rather than an error; naming an object with no texture at all *is* an
error.

```
set_texture(@door_wall, "steel")        -- a wall
set_texture(@room.floor, "wet_stone")   -- a floor
set_texture(@guard, "armour_red")       -- every attachment
set_texture(@tank, "turret", "rusted")  -- one attachment
```

The texture is uploaded, and any geometry it invalidates rebuilt, at the end of
the frame; repeats within one tick are coalesced. A name with no matching `.png`
in `textures/` is a warning in the log window naming the texture, and the object
keeps drawing what it had.

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

A **sprite**'s are `position`, `visible`, `texture`, `alpha`, `tint`, `size` and
`frame`, plus the same read-only `attach` and `carrier`. `size` is a `vec` of
width and height, whose `z` is unused.

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

A sprite, an emitter or a sound can **ride** something, so that smoke follows a ship, a
flame follows the hand holding the torch, and dust follows a wheel. It is the
same binding the editor's **Attach** panel authors, so an effect bound by a
script and one bound by hand are the same effect.

```
attach(@smoke, @ship)                    -- ride an entity
attach(@flame, @guard, "hand_r")         -- ride a bone of its only model
attach(@flame, @tank, "turret", "muzzle") -- name the attachment too
attach(@wake, "ship")                    -- ride a frame: a vehicle's own space
detach(@smoke)                           -- back to world space
```

Only sprites and emitters. An **entity** boards a vehicle with `mount`, which has
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
```

A screen and a widget carry the same tags, attributes, script and params any
level object does, so `#dimmable.prop.alpha = 0.5` reaches widgets, and a button
gets its behaviour the same way a door does.

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
| `set_texture did nothing` | The entity has no attachment by that name, or no attachments at all |
| `destroy did nothing` | The ref names geometry, or something already gone |
| `attach did nothing` | No bone by that name, a model with no armature, or an entity with several attachments and no say which |
| `find(...) matched nothing` | No object carries that tag |
| `player() is null` | There is no player — the editor, or before the level finished loading |
| `nothing handled :atom:` | A `<-` or an `after` reached an object whose script has no clause for it |

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
- `play_anim` and `set_texture` naming an attachment, animation or texture that
  is not there — the same sentences the runtime would log
- `spawn` naming a type with no definition file
- `attach` binding something that cannot ride, riding something that is not an
  entity, or naming a bone the model does not have
- an unknown property, or a call to a function nothing defines
- `after` and `@self <- (...)` with an atom this script has no clause for
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
send        = target "<-" tuple ;
target      = "@" ident | "#" ident | postfix ;

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
            | ident | "@" ident | "#" ident | "(" expr ")" ;

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
