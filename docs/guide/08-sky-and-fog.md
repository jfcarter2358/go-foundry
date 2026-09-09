# Sky and fog

Both live in the **Environment** group of the level properties panel. Both are
optional — a level with neither renders exactly as it did before they existed.

The checkbox at the top of each group is what creates and destroys the block.
Turning fog *off* is a different gesture from deleting it, and keeps your
numbers.

## Sky

Two modes behind one setting.

**Gradient** needs no art: three colour stops up the dome — **Zenith**,
**Horizon**, **Ground**. This is what most levels want. "The horizon should stop
being a flat grey wall" is a lighting decision, not an art request.

**Cubemap** is six textures, named individually: `+X, -X, +Y, -Y, +Z, -Z`. Two
sets ship (`sky_day_*`, `sky_space_*`). An incomplete or unloadable cubemap falls
back to the gradient rather than to nothing, so a half-picked set still looks
like something while you finish choosing.

Both modes then take:

| Field | |
| --- | --- |
| **Tint** | multiplies whatever the mode produced |
| **Intensity** | brightness |
| **Rotation** | degrees about +Y — line the art up with the level's sun |

The three gradient stops are kept when you switch to cubemap and back, so
experimenting costs nothing.

### Scripting the sky

Every field in this group is also a `@level.prop.sky_*` property, so a script
can change the sky during play — day turning to dusk, a portal into a
different biome's weather. A level with no sky yet gets one at the defaults
the moment any of them is written, the same shortcut a fog volume gets from
writing `fog_*`. See "Sky" in SCRIPTING.md for the full list.

```
@level.prop.sky_cubemap = "dusk"        -- fills all six faces, switches kind
@level.prop.sky_tint = <0.85, 0.7, 0.65>
```

A plain write snaps to the new look on the next frame. To blend into it
instead, call `ease_sky(seconds)` first — it arms a cross-fade that whatever
`sky_*` properties are written right after become the target of:

```
ease_sky(3)
@level.prop.sky_cubemap = "dusk"
```

## Distance fog

| Field | |
| --- | --- |
| **Mode** | `linear`, `exp`, `exp2` |
| **Color** | |
| **Start** | world units, every mode |
| **End** | linear only |
| **Density** | exp / exp2 only |
| **Max Opacity** | below 1 leaves distant landmarks as silhouettes rather than erasing them |
| **Sky Blend** | how much fog colour the sky takes at the horizon |
| **Sky Falloff** | how tightly that is confined to the horizon |

Fog reaches every pass that shades — walls, scatter, ground
patches, sprites and the sky — so nothing fades at a different rate from the
thing beside it.

**Sky Blend is the one to get right.** Without it, the ground fades into the fog
while the sky behind it stays crisp, and the join becomes the most conspicuous
edge on screen at exactly the distance you added fog to hide. Raise it until the
horizon dissolves.

Distance is measured radially from the eye, so fog on a stationary object does
not change as you turn your head.

## Fog volumes

The level's `fog:` is the air of the whole level and has no position. A **fog
volume** is fog that has one: a cellar that is damp while the courtyard outside
is clear, a ravine holding a bank the road above it does not.

Place a [volume](13-volumes.md), select it, and open its **Fog** group.

| Field | |
| --- | --- |
| **Color** | |
| **Density** | per world unit travelled *inside* the region |
| **Max Opacity** | |

Density is per unit of the view ray that was inside the shape, not a distance
from the camera. That is what makes a bank behave like a body of fog: looking
along its length is thick, looking across it is thin, a wall past its far face is
fogged by exactly its depth, and walking in fills the screen.

Every volume shape works — box, sphere, cylinder — at any size and rotation.

Eight fog volumes are drawn at once; a level with more drops the extras in level
order.

A fog volume is still a volume. It fires `:enter:` and `:exit:`, which is usually
exactly the event you want when there is fog to be in, and disabling the volume
puts the fog out with it.

```
@cellar.prop.fog = false           -- switch the fog off, keep the numbers
@cellar.prop.fog_density = 0.5     -- thicken it
@cellar.prop.enabled = false       -- fog out and the trigger disarmed
```

Writing any `fog_*` property on a volume with no fog block gives it one at the
defaults, so a script can fill a plain trigger without the level pre-authoring an
invisible fog on it.

## The two together

The screen is cleared to the level's own colour rather than a fixed blue, so a
level with fog and no sky fades geometry into nothing rather than into a
silhouette against slate.

Additive sprites are attenuated by fog rather than tinted by it, so a distant
spark gets dimmer instead of brighter.
