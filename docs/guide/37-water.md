# Water

Water in Foundry is a **surface**: a face, or a generated mesh, wearing a
material with the `water` effect. [Materials](28-materials.md) covers how it
looks — depth colour, refraction, reflection, foam, flow maps, falling sheets,
the presets. This page is everything else: how water answers what happens in
it, what the world looks and sounds like from under it, the sound of rivers,
and how a script finds it.

Where water comes from:

- **Generated**: a [generator](33-generators.md)'s `ground_edit` stage makes
  ponds, lakes, marshes, rivers and waterfalls (painted with the
  [terrain paint](36-terrain-paint.md) brushes, or traced from drainage),
  and a region terrain fills its sea and hollows. Each is a non-solid mesh
  wearing one of the water presets.
- **By hand**: any brush face wearing a water material — a pool, a fountain,
  a flooded cellar.

Both kinds do everything on this page. Water is not solid and not a volume:
bodies walk through it on the bed, which is what wading is.

## Rings and splashes

Anything that disturbs a water surface rings it, with no setup:

| What | What you see and hear |
| --- | --- |
| Walking in the shallows (water below about three-quarters of a body's height) | a small splash at every stride (`water_step` effect and sound), a ring at each foot and a wake of rings round the legs while moving; standing still, a slow ring now and then |
| Dropping in — jumping or falling into water faster than stepping down a bank | a big splash (`water_splash`): a crown of water thrown up, spray spreading over the surface, mist, a strong ring with a little foam at its heart |
| Something small dropping in — a thrown stone, a can (a body under 0.6 m tall) | a plop (`water_impact`) |
| A shot (`hitscan`) crossing a water surface on its way to what it hits | a spout at the crossing (`water_impact`) and its ring; the water does not stop the shot |
| Swimming or walking the bed with the water over your head | rings above you while you move near the top |

The player and every entity that falls (walkers, thrown props, bodies) take
part; vehicles and anything riding a moving frame do not. A splash is tinted
by the water's own shallow colour, so a swamp's spray is browner than a
mountain lake's.

The effects and sounds are ordinary assets under `particles/` and `sounds/`:
put a `water_splash.fpar` or `water_step.wav` in your project and it is used
instead. A project without them still gets the rings.

**Rain.** A level's `rain_ripples:` (0–1, `@level.prop.rain_ripples` from a
script) covers every water surface with small drop rings, more of them the
harder it rains. There is no rain *weather* in the engine (no falling
streaks, no wet surfaces): pair it with your own particle rain.

```
rain_ripples: 0.6      # in the level file: a steady shower
```

```
@level.prop.rain_ripples = 1.0    -- a script: the downpour starts
```

How it works, for the curious: the simulation queues each disturbance, the
renderer keeps the sixteen live rings nearest the camera and the water shader
draws them as wave packets in the surface's normal — no texture, no extra
pass. Rain is computed per pixel from a hashed grid of drops.

## Under water

With the camera below a water surface:

- **The water fills with its own murk.** How far you see and what colour the
  distance fades to come from the material: `absorption` and `shallow_color`
  set how thick it is (alpine water lets you see the far side of a pond, a
  swamp a metre or two), `scatter_color` its colour, lit by the level's sun
  and sky and darker the deeper you are. Only the water between you and what
  you look at fogs it: a tree on the bank seen up through the surface is
  fogged by the water, not by its whole distance.
- **The surface from below**: straight overhead, inside a cone of about 49°
  (Snell's window), the world above shows through, rippling; outside it the
  surface is a mirror of the water itself (total internal reflection).
- **Caustics** — the sun's light focused by the waves — play over the bed,
  rocks and anything else under the surface around you (high water quality).
- **Sound is muffled**: everything on the effects bus is low-passed and
  quieter; music is untouched.

Coming back up undoes it all. A small band either side of the surface (a few
centimetres, plus the material's wave height) keeps a wave passing the lens
from flipping between the two every frame.

Underwater is decided at the camera, so a third-person camera above the water
with the player swimming below it shows the dry view, and a camera dipped
under with the player on the bank shows the underwater one.

Not done: swimming (bodies walk the bed), breath, buoyancy for props, and a
split view when the surface cuts through the middle of the screen (the near
plane exactly at the water line shows one side or the other, not both).

## River sounds

A `ground_edit` with **Water sounds** on (`water_sounds: true`; the River,
Stream, Pond, Lake and Marsh brush presets turn it on) places looping
positional sounds under the generator's prefix, replaced on every run:

| Where | Clip | |
| --- | --- | --- |
| The foot of each waterfall | `water_falls` | a roar, louder and carrying further the higher and wider the fall; a cascade's steps share one |
| Along each river, every **Sound spacing** metres (`water_sound_spacing`, 24) | `water_rapids` at riffles, rapids and white water, `water_river` elsewhere | louder for a wider, faster river |
| Each confluence | `water_rapids` | the mixing water at the mouth |
| Pond, lake and marsh shores | `water_lap` | gentle lapping, a few per body |

No two are placed closer than 0.7 of the spacing (a fall's roar keeps a wider
berth), each carries a little further than the spacing, and each is detuned
slightly so neighbours never phase. Walking a river you hear two or three at
a time. A looping sound past its **max distance** gives its voice back and
takes one again when you come within it, so a long river costs nothing beyond
earshot — and the mixer's eight looping voices go to the sounds you can hear.

The clips ship as **generated** loops (seeded noise and bubble chirps; no CC0
recordings were at hand). Drop a recording with the same name into your
project's `sounds/` to replace one everywhere.

## From a script

`water_at(place)` asks about a vec or an object's position. It is null where
no water surface lies over or under the point, else a dict:

| Key | |
| --- | --- |
| `under` | the point is below the surface (by at most 40 m: a cave far under a lake is not in it) |
| `depth` | metres below the surface; negative above it |
| `surface` | the still surface's height there (waves ride a few centimetres either side) |
| `flow` | the current, m/s, as a vec (x and z; zero on still water) |
| `material` | the material the water wears |

```
def on_tick(:tick:, dt float) -> null
    w = water_at(player())
    @self.attr.head_under = w != null and w["under"] and w["depth"] > 1.6
end
```

A float that drifts with a river:

```
def on_tick(:tick:, dt float) -> null
    w = water_at(@self)
    if w != null and w["under"]
        impulse(@self, w["flow"] * dt)
    end
end
```

It reads the same meshes the water draws with, so it is right for generated
and hand-built water alike, and in a release build. Asking costs a grid
cell's worth of triangle tests: every entity, every tick, is fine.

See [Scripting](../SCRIPTING.md) for `rain_ripples` and the rest of the
`@level` properties.

## Performance

| | Cost |
| --- | --- |
| Rings | per water pixel, a loop over the live rings near the camera (at most 16); nothing with none |
| Rain | per water pixel, three hashed drop lookups while `rain_ripples` > 0 |
| Under water | one more fog volume for every fogged program; the caustic material variants (built when the camera first comes near water, so diving in does not stall) |
| Muffle | one two-pole filter over the effects bus, only while under |
| Splashes | one-shot particle emitters, removed when spent; never saved |

The F3 profiler's `water: copy` and `water` rows are the water pass at the
player's **water quality** ([Materials](28-materials.md)).
