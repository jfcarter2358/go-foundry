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

## Frame portals — the way in

A doorway between a moving thing and the world is a **portal with a `to_frame:`**.
Select the edge, set **Kind** to `portal`, and set **To Frame** to the vehicle's
frame — or `world` for the ground.

Crossing carries position, velocity **and** facing through the relative
transform, so a walker arrives facing the way they were going.

There are three rules, and levels break on the second one.

**1. Frames must be gravity-aligned to be joined.** A ship resting on a deck
qualifies; so does one flying straight and level. A barrel-rolling ship does not,
and while misaligned the portal **seals** — it resolves as a solid wall, so
nobody walks out of a doorway currently pointing at the sky.

**2. The two halves must overlap, and by a real margin.** A crossing fires on
proximity — within 0.6 units of the line — so the far side has to be somewhere
you can stand the moment you arrive. Two portals authored in the same place *do
not work*: you cross 0.6 short of the line and land 0.6 outside the sector you
were entering, in no sector at all.

What works: the ship's threshold reaches *past* the deck's portal line and the
deck reaches past the ship's, by more than 0.6 each, with both floors present in
between. The band is a radius, so this applies across the doorway too — the far
sector has to be wider than the segment by the same margin.

**3. A crossing has to go deeper than the way back.** Overlap by *less* than the
band and both sides claim the same ground, and the two spaces trade the walker
back and forth once a tick. Movement looks fine; what you see is the screen
strobing between the two interiors. Geometry following rule 2 satisfies this
automatically.

`resources/chunks/dropship_hold.yaml` and `resources/levels/dropship_demo.yaml`
are the worked example. Read them before authoring an interior.

## What does not go in a frame

- **Biome paint and procedural detail.** They build in world coordinates, so a
  framed chunk would leave its grass where the ship used to be. The editor
  refuses to paint one. A vehicle interior is a built environment, not terrain.
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
