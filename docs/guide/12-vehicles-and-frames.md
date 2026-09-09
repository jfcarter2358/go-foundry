# Vehicles and frames

A **frame** is a moving coordinate system. Geometry and entities can live in one,
and everything inside is carried exactly — because nothing has to be carried: a
body inside a frame stores its position in that frame's coordinates and never
moves at all.

A **vehicle** is an entity with a `vehicle:` block. It is not a separate type —
an entity already has an ID, tags, attributes, a script, a transform, a velocity,
bounds, collision flags and model attachments, which is everything a vehicle
needs before anything vehicle-specific starts.

## Making one

In the entity editor, with a type open, press **Make this a vehicle**.

| Field | |
| --- | --- |
| **Mode** | `ground`, `air`, `water`, `space` |
| **Interior** | the chunk(s) that are this vehicle's decks. A floor and a roof can both bind here |

Setting an interior binds both sides: the chunk names the entity as its frame,
and the vehicle lists the chunk. You can also do it from the chunk's **Carried
By** dropdown in the level editor.

Omit interiors entirely for a seat-only vehicle — a car you sit in rather than
walk around.

### Seats

| Field | |
| --- | --- |
| **Name** | how a script asks for it |
| **driver** | sitting here hands over the movement keys |
| **Position** | vehicle-local, the occupant's feet |
| **Exit** | vehicle-local, where they are put down on leaving |
| **Yaw** | rest facing |
| **Eye** | view height above the seat |
| **Yaw Limit / Pitch Limit** | the look cone; 0 is unrestricted |

Exactly one seat is the driver's; ticking one takes it off whichever had it.

### Drive tuning

Every field is optional — unmentioned ones keep the mode's defaults, and
**Reset to defaults** puts them back.

`Max Speed`, `Accel`, `Brake`, `Reverse`, `Turn Rate`, `Pitch Rate`, `Roll Rate`,
`Drag`, `Angular Drag`, plus `Lift` (air), `Buoyancy` (water), and `Ground Stick`
/ `Max Climb` (ground and water).

All of it is writable from a script during play, which is what lets a car handle
differently in the rain.

## Driving

| Key | |
| --- | --- |
| `E` | take a seat within reach; press again to stand up or get out |
| `W` / `S` | throttle forward / back |
| `A` / `D` | steer |
| `Space` | brake |
| `Q` / `E` | roll (air and space) |
| `L Shift` / `L Ctrl` | pitch nose down / up (air and space) |
| Mouse | look around from the seat, within its limits |

Controls are **rates**: a vehicle turns as fast as its tuning says. There is no
inertia tensor and no torque — a hull striking a wall stops, it does not spin.

Ground and water vehicles are flattened to a yaw every frame. Their body may tilt
to the surface for looks, but the space an occupant stands in keeps its own up,
or walking around a cabin would fight the terrain.

## Riding

Being aboard is two things: a **frame**, and optionally a **seat**. Seated pins
the occupant to the seat every frame; standing lets them walk the deck. Sitting
down and standing up are the same entity changing one string.

Mounting is a change of frame, not a teleport — the occupant ends where they
started in the world and is carried from then on.

From a script:

```
mount(@player, @shuttle, "pilot")   -- seat optional; without it they stand aboard
dismount(@player)
```

Both post `:mount:` / `:dismount:` to the vehicle, so a door script hears about a
boarding it did not arrange. `mount` refuses a seat that does not exist, an
entity that is not a vehicle, and anything that would put a frame inside itself.

An entity's `frame` property is a ref to the vehicle it is riding, or `null`, and
`seat` is which seat it occupies — empty while standing on the deck.

## Frame doorways — the way in

A doorway between a moving thing and the world is a **volume with a doorway**.
Place a volume, set **Rides** to the vehicle under Transform, tick **This volume
is a way in and out** under Frame Doorway, and leave **Opens onto** at `world`
for the ground.

The doorway is authored on the *moving* side — it rides the ship, because the
ship is what knows where its own ramp is. Nothing has to be authored on the
other side at all, which is what lets a ship set down wherever the pilot puts it
rather than only in a dock the level guessed at.

Crossing carries position, velocity **and** facing through the relative
transform, so a walker arrives facing the way they were going.

Two rules.

**1. Frames must be gravity-aligned to be joined.** A ship resting on a deck
qualifies; so does one flying straight and level. A barrel-rolling ship does not,
and while misaligned the doorway **seals** — it resolves as solid, so nobody
walks out of a hatch currently pointing at the sky.

**2. The doorway must be deeper than one step.** Which side of it you are on is
settled by *containment*: standing in it is being aboard, and being aboard while
outside all of the ship's chunks is having left. There is no "before" at which
to hand you over early, so a body moving further in a tick than the volume is
deep passes straight through it. Three metres against a walking pace is
comfortable; a projectile-speed body is not something this catches.

Containment is why there is no third rule. The old portal-edge doorway fired on
*proximity* — within 0.6 units of an authored line — which meant both sides
claimed the same ground unless they overlapped by more than that, and a walker
in the overlap got traded back and forth once a tick with the screen strobing
between the two interiors. Inside a volume and outside it cannot both be true,
so that failure has no way to happen here.

The rest of the level authoring is unchanged: build the interior as brushes in a
chunk, bind the chunk to the vehicle under **Carried By**, and put the doorway
volume across the threshold.

## What does not go in a frame

- **Scatter volumes.** They build in world coordinates, so a framed chunk would
  leave its grass where the ship used to be. A vehicle interior is a built
  environment, not terrain.
- **Cross-frame entity collision.** Two entities in different frames do not
  collide. The one pair that is always cross-frame is a rider against the hull
  carrying it, which it is inside by definition.

A frame ignores its owner's scale, but the geometry it carries does not — scale a
ship 2x and its hold is twice as big, drawn and collided alike, while the player
who walks into it stays their own size.

Frames nest, which is how a shuttle in a carrier's hangar works.

## Not a vehicle, but a frame

You do not need `vehicle:` to get a moving interior. An entity that names a chunk
gets a frame, and a script that writes its position moves everything inside it.
That is what `resources/entities/dropship.yaml` is: a hold you walk into and a
script that drops the ship two hundred metres, with no drive model at all. The
passenger rides it by standing in it.
