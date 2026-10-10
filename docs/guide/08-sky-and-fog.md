# Sky and fog

Both live in the **Environment** group of the level properties panel. Both are
optional — a level with neither renders exactly as it did before they existed.

The checkbox at the top of each group is what creates and destroys the block.
Turning fog *off* is a different gesture from deleting it, and keeps your
numbers.

## Sky

A whole sky — the mode, the art, the tint and the cloud deck — can be saved as a
sky [material](28-materials.md) and named from the **Material** row at the top of
this panel, which is how several levels share one sky without spelling out nine
fields each. The rows below then show what the material set.

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

## Cloud volumes

Fog is a colour laid over what is behind it. A **cloud** is a body you can see
the shape of: lumpy, lit by the sun on one side and shaded on the other, and
drifting. Clouds are ray-marched through 3D noise, and like fog they come in two
kinds.

A **cloud volume** is a [volume](13-volumes.md) full of cloud. Place one, select
it, and tick **This volume is full of cloud** in its **Cloud** group. A big, flat
box over the level is a cloud layer; a sphere is one cumulus.

| Field | |
| --- | --- |
| **Colour** | the cloud's albedo — white for fair weather, grey for a storm |
| **Coverage** | how much of the region is cloud, 0 to 1. The one you will change most |
| **Density** | thickness per world unit at the cloud's densest |
| **Scale** | world units across the largest lumps |
| **Detail** | world units across the fine noise that breaks the edges up |
| **Erosion** | how hard that detail eats the edges. 0 is cotton, 1 is ragged |
| **Wind** | world units per second the pattern drifts. Moves the shape, not the region |
| **Absorption** | how dark the underside gets |
| **Ambient** | how much sky light fills the shadowed side |
| **Casts shadows** | see *Cloud shadows* below |
| **Softness** | how far in from the region's walls the cloud fades up |

The cloud is lit by the level's first directional light, filled by its global
light and tinted by its sky, so a cloud under a dusk sky is shaded dusk-coloured.
Point lights light it too — a torch lights the smoke rising past it.

A cloud volume is still a volume: it fires `:enter:` and `:exit:`, and disabling
the volume clears the cloud.

### Keeping cloud out

The opposite job: a dropship parked in a cloud bank should not have the bank
drifting through its hold. Tick **Clears clouds** in any volume's **Cloud** group
and no volumetric cloud is drawn inside it — cloud volumes and the deck alike.
It needs no cloud of its own, and is usually a plain box fitted to the room.

```yaml
volumes:
  - id: hold
    attach: {entity: dropship_1}
    position: {x: 0, y: 1.5, z: -2}
    half: {x: 2.5, y: 1.5, z: 4}
    clears_clouds: true
```

Put it on the ship's **type** instead (the same checkbox on a volume in the entity
editor, `clears_clouds: true` under the type's `volumes:`) and every placement
carries a clear hold wherever it flies. It clears only while the volume is
enabled, and scripts can switch it with `@hold.prop.clears_clouds`.

Only what the player sees is cleared. The cloud's shading and its shadow are
untouched, so a hold inside a bank is still in the bank's shade. At most four
clearing volumes in view take effect at once; past that, the later ones in level
order are ignored. Particle smoke and explosions are never cleared.

## The cloud deck

The sky's **Volumetric cloud deck** is cloud with no edge: a layer between two
heights running out to the horizon. Tick it in the sky group.

| Field | |
| --- | --- |
| **Altitude** | the deck's base, in world units |
| **Thickness** | how deep it is |
| **Range** | how far out it is drawn before fading into the horizon. Also bounds its cost |

and every field a cloud volume has, above.

The deck has an altitude rather than living at infinity like the dome, so a
mountain can rise through it and the camera can fly above it. `ease_sky` blends
the deck with the rest of the sky: a script can clear the weather over a few
seconds.

## Cloud shadows

A cloud with **Casts shadows** on darkens everything under it — floors, walls,
scatter and sprites — from the level's first directional light, and the shadow
drifts with the wind. It is a soft, partial shadow, not a hard one, and it lands
only *below* the cloud: a peak standing above a cloud layer stays sunlit.

The shadow covers a square around the camera (600 units either side by default).
On a compiled level, whose lighting is baked, it takes part of the baked light
away rather than all of it.

## Cost

Clouds cost fill rate: a cloud covering the screen marches every pixel. `F4`
cycles **half resolution** (the default), **full resolution** and **off**, and
the editor's render settings expose the march and light step counts and the
cloud shadow switch. The `F3` overlay reports the current setting and how many
marches the frame ran.

Half resolution keeps edges against geometry sharp: the upsample matches each
screen pixel to the cloud samples taken at its own depth.
