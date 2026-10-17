# Phase A — third-person player

Status: done (uncommitted). The engine can now run a level in third person: a
camera boom behind and over the shoulder, a visible body in the world pass with
shadows, turned toward its motion and animated by it. First person stays the
default and every existing level is unchanged.

## What

### Level YAML (`player:` block)

```yaml
player:
  camera:
    mode: third_person   # first_person (default) | third_person
    distance: 4.8        # boom length behind the pivot
    height: 1.55         # pivot above the body's centre (FP eye is 1.7)
    shoulder: -0.8       # pivot to the camera's right; negative = left shoulder
    pitch_min: -65       # degrees (third person only)
    pitch_max: 70
    radius: 0.3          # camera clearance from walls
    smoothing: 5         # ease-out rate of the boom (1/s); pull-in is instant
    hide_distance: 0.9   # boom shorter than this hides the body
  body:
    turn_rate: 600       # deg/s
    models: [...]        # exactly an entity type's `models:`; origin = feet, facing -Z
    locomotion:          # all optional; defaults = human.fskl clip names
      {idle, walk, run, sprint: run, jump: jump_up, jump_run, fall: jet_fall,
       crouch_idle, crouch_walk, walk_speed: 1.8, run_speed: 4.95}
```

Every key is a pointer/zero-default; `PlayerCameraToRaw` writes only keys that
differ from `DefaultPlayerCamera()`, `PlayerBodyToRaw` nothing for an empty body,
so a level without the blocks round-trips byte-identical.

Scripts: `@level.prop.camera_mode` (`"first_person"`/`"third_person"`) and
`@level.prop.camera_distance`, both read/write; saved in `savefile.WorldState.Camera`
(an old save without it leaves the level's camera alone).

### How

- **Schema/runtime** — `internal/world/player_view.go`: `PlayerCamera`
  (+`Effective`, raw conversion), `PlayerBody`, `PlayerLocomotion`,
  `ChooseLocomotion` (pure: speed/air/crouch/sprint → clip, fallbacks, playback
  rate scaled to ground speed, clamped 0.6–1.6), the engine-owned
  `world.PlayerBodyEntity` (ID `player_body`, not in `Level.Entities`) and
  `world.PlayerThirdPerson`. `PlayerSettings` gained `Camera`/`Body`;
  `RawPlayer` gained `camera:`/`body:`.
- **Loading** — `levelio.InstallPlayerBody` (called from `InstallPlayerModels`, so
  every path that installs the viewmodel installs the body); body models are
  prefetched with the viewmodel's (`model_prefetch.go`).
- **Camera** — `internal/sim/third_person.go`. `PlacePlayerCamera`'s on-foot path
  branches to `placeThirdPerson` when the level's camera is third person (seated /
  riding a frame still takes `SeatedCamera`). Look angles stay `gfx.Cam.Yaw/Pitch`
  (pitch clamped to the block's limits), so movement, aim and every script that
  reads the look are unchanged. The pivot is placed by casts (centre → up to
  `height`, then → sideways to `shoulder`, each keeping `radius` clear), then the
  boom is cast back along −look. Pull-in is immediate, ease-out exponential at
  `smoothing`; a pivot jump > 4 units (teleport) snaps.
- **Boom cast** — `collision.CameraBoom`: five rays (centre + four at 0.8·radius
  around it) against level geometry (`geometryQuery`: brushes, compiled surfaces,
  terrain) and solid scenery (`sceneryRayNearest`), nearest hit − radius, clamped
  to [0, length]. Solid props whose box is under 1.2 units on every axis are
  skipped (recast past them, up to 4); entities are never tested.
- **Body** — `sim.UpdatePlayerBody` (after the camera, in `sim.Step`): position =
  player feet (`Position − BoundHalfY`), yaw turned toward horizontal motion at
  `turn_rate`, smoothed speed from position deltas (teleports ignored), air time /
  jumped flag from `platform.IsOnGround` and vertical velocity, then
  `ChooseLocomotion` → `playLocomotion` (an authored animation of that name on an
  attachment, else the model clip; crossfades on change) and `UpdateAnimations`.
  Hidden when: not third person, editor owns the frame, riding a frame or seated,
  or the boom is shorter than `hide_distance`. Paused (`!g.Play()`) → held.
- **Drawing** — `internal/render/entity_walk.go`: `drawnEntityCount/drawnEntity`
  append the body (when shown) to the level's entities, and every world entity walk
  goes through them: colour pass + depth prepass (`RenderEntities`), cascades
  (`RenderEntitiesShadow`), the VSM movers tracker (`vsm_track.go`), the point-shadow
  cache signature (`point_shadow_cache.go`); the prepass warm-up includes it.
  `RenderViewmodel` draws nothing while `world.PlayerThirdPerson`. GI skips the body
  (as it skips the player).
- **Editor** — Player panel: **Camera** group (third-person checkbox + all boom
  fields) and **Body (third person)** group (the shared attachment editor on the
  loaded body, turn rate, locomotion clip names with the defaults as hints, clip
  speeds). YAML field docs regenerated.
- **Docs** — guide `docs/guide/11-entities.md` → "Third-person camera and body".

## Verification

- Unit tests: `collision/camera_boom_test.go` (open air, wall pull-in = hit −
  radius, side rays catch a grazed surface, never negative, small props looked
  through / boulders not), `sim/third_person_test.go` (behind/above/shoulder side,
  pulled in by the wall behind then eased back out over frames, never under the
  floor or through the ceiling at any pitch, first person unchanged, easing, turn
  wrap), `world/player_view_test.go` (no block → first person + no block written,
  YAML round trip of camera/body/locomotion incl. an authored `shoulder: 0`,
  clip choice for 11 motion states + rate clamp + fallback),
  `render/entity_walk_test.go` (body walked only while shown; VSM tracker hands in
  the old and new box as it moves and the last box when hidden).
- Suites: world, sim, collision, levelio, savegame, savefile, script, harness,
  render (incl. `FOUNDRY_GL_TEST=1 -run 'Viewmodel|Entit|VSM'`) pass. `go build ./...`,
  `-tags release`, `go vet` on touched packages clean. The editor suite's
  `TestScoreSibeliusKeymap`/`TestScoreRebinding` failures are the concurrent score
  editor session's.
- Visual (`foundry play run`, scratch project = OneDrive resources symlinked + repo
  shaders, level `tp_test`: yard, 4-unit-wide 14-high slot, 6-wide cave with a 3.4
  roof, marine.fmdl body at scale 1.5): body seen from behind idle/walking/running,
  over the left shoulder, crossfading clips; jump and fall poses in the air with
  the shadow below; strafing turns the body side-on; in the slot the boom never
  enters rock, looking straight at a wall squeezes it to just behind the head; in
  the cave the camera stays under the roof. Shadows present under both VSM and
  cascades (`FOUNDRY_NANITE_VSM=0`).

  Repro: `foundry play run -root <proj> -level tp_test -script 'teleport 22,0.05,0;
  look 0 -8; forward 0.8; shot slot_walk.png; ...'`.

## Known gaps

- **VSM ghost shadow** (needs a VSM owner): in some sequences a copy of the body's
  shadow stays on the ground where it was drawn a while back, seen only at a
  distance (a coarse page) and only with the virtual shadow map on; cascades are
  clean, and the tracker's handing-in is unit-tested correct. Repro on `tp_test`:
  `look 0 -10; wait 0.5; shot idle.png; forward 0.3; shot walk.png; forward 1.5;
  shot run.png; look 45 -15; right 1; shot strafe.png; teleport 2,0.05,-6; look 90
  -20; wait 2; shot later.png` → blob at the left edge of `later.png`. Not the
  rect budget (`FOUNDRY_NANITE_VSM_RECTS=100000` still shows it). Likely affects any
  mover; not chased further here.
- Third person is on foot on the world only: riding a frame or sitting keeps the
  first-person eye and hides the body.
- Very low roofs + steep look-down squeeze the boom under `hide_distance`, so the
  body hides (e.g. 3.4-unit cave, pitch −60). A smarter boom would slide the pivot
  down/forward instead.
- The boom ignores entities (squadmates, enemies) and props < 1.2 units; a big
  entity between camera and player is not avoided.
- Scripts cannot address the body's animations (`@player.model.*` is the
  viewmodel); no turn-in-place, strafe or start/stop clips — the body turns to face
  its motion; no root motion; no foot IK on slopes.
- The body has no bounds (never frustum-culled: one character), and is skipped by
  live GI as the player always was.
- Hitscan goes from the camera: geometry between the camera and the player (rare
  with the boom pulled in) would block a shot.
- No body preview in the editor viewport.
