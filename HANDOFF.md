# Handoff: first-person viewmodel — input wiring and an editor panel

Same branch, an unrelated feature — nothing above this line is affected by
anything below it, and nothing below depends on the brush migration except by
sharing a codebase.

## What already exists

The engine now has a real first-person viewmodel system, built and verified
this session. In brief (see `[[first-person-viewmodel]]` if you're the same
Claude session reading this back, or just read the code — it's the accurate
record either way):

- `world.PlayerSettings.Models []EntityModel` and `RawPlayer.Models` — a
  level's `player:` block can carry a `models:` list, identical in shape to an
  entity type's own (`internal/world/player_settings.go`, `level_schema.go`).
- `internal/levelio/level_loader.go` populates `world.PlayerEntity.Models`
  from it on every level load (`Delete()` the old ones first, to not leak
  their GPU buffers, then reassign and `Load()`).
- `internal/app/app.go`'s main loop ticks `world.PlayerEntity.UpdateAnimations`
  every frame (nothing else does — `collision.EntityUpdate` only walks
  `lvl.Entities`, which the player still isn't in).
- `internal/render/viewmodel.go`'s `RenderViewmodel()` draws them: it clears
  the depth buffer first and uses `gfx.Cam.Matrix.Inv()` as the "entity"
  matrix, so a `hands` root attachment sits relative to the CAMERA rather than
  the player's physical body, and can never be poked into by nearby geometry.
  Called from `app.go` between `RenderSpritesTransparent` and the runtime UI
  loop, gated `!devActive()` — **it draws nothing in the editor today**, which
  matters for the preview question below.
- `models/fp_arms.fmdl` (in the OneDrive project, not this repo) — a
  two-armed rig with `hand_socket.R`/`.L` bones, 8 clips
  (`rifle_idle/walk/fire`, `pistol_idle/walk/fire`, `grenade_idle`,
  `grenade_throw`). `level1.yaml`'s `player:` block equips it with the rifle
  visible and the pistol/grenade attached-but-hidden, exactly the pattern
  `enemy_combat.flang` uses for a carried grenade.
- Docs: `docs/guide/11-entities.md`'s new "First-person viewmodel" subsection
  under "The player", and a pointer from `02-building-levels.md`'s "Player
  step height" section (the two live in the same `player:` block).

## What does not exist yet — the two things this handoff is for

**Nothing presses fire, switches weapons, or throws a grenade.** The clips and
the sockets exist; nothing in the input layer or in a script calls
`play_anim`, toggles `.model.<name>.prop.visible`, or spawns a live grenade
for the player. `enemy_combat.flang` is the AI side of this same shape of
problem and is the file to read before starting — the player side needs the
same state machine, minus the AI decision-making, plus real input.

**There is no way to author `player: models:` except by hand-editing the
level's YAML.** No panel, no picker, no add/remove button. The step-height
checkbox next to where this would live is `internal/editor/editor_player_panel.go`.

Both are real, separable pieces of work. Read both sections below before
starting either — the editor panel section explains why the model list can't
just be handed the existing per-entity widgets unchanged.

---

## Part 1 — input wiring

### The one existing precedent, and why it's not quite the right shape

`:interact:` (E key) is the only player-input event the scripting layer has
today, and it does two things a fire button should not:

1. It's a plain edge (`hooks.ScriptInteractRequest()` sets a bool,
   `hooks.ScriptInteractPressed()` reads-and-clears it — see
   `internal/hooks/script_engine_hooks.go:83-90`), consumed once per frame by
   `internal/script/script_events.go`'s `postInteractEvent`
   (`internal/app/input.go:126-132` is where the key press sets the flag).
2. `postInteractEvent` doesn't post to `@player` — it posts to *whatever
   contains the player* (nearest of `EntitiesContaining`/`VolumesContaining`,
   `script_events.go:529-558`), because interacting is about the world object
   you're standing in front of.

A fire/throw/switch event is the opposite shape: it's about the player, not
about what the player is near, and at least primary fire needs to distinguish
*held* from *pressed* (a rifle fires on a timer while the button is down; a
grenade throw and a weapon switch are single presses).

### Recommended shape

Mirror the `:interact:` plumbing exactly, but post straight to `@player` and
carry a press/release pair for anything that can be held:

| SDL input | New hook (in `internal/hooks`, same pattern as `scriptInteractPressed`) | Posted event | Args |
|---|---|---|---|
| LMB down / up | `ScriptPrimaryFireRequest(down bool)` | `:primary_fire:` / `:primary_fire_end:` | — |
| RMB down / up | `ScriptSecondaryFireRequest(down bool)` | `:secondary_fire:` / `:secondary_fire_end:` | — |
| `G` down | `ScriptThrowRequest()` | `:throw:` | — |
| `1`/`2`/`3` down | `ScriptWeaponSlotRequest(n int)` | `:weapon_slot:` | `slot int` |

Capture the keys in `internal/app/input.go`'s `sdl.KeyboardEvent`/new
`sdl.MouseButtonEvent` cases, gated the same way `SCANCODE_E` already is
(`!devActive() && platform.PlayerControlEnabled && !script.GamePaused()`,
`input.go:126-132`) — there's no existing mouse-button case in gameplay input
today (only editor picking uses mouse buttons elsewhere), so that whole
`case sdl.MouseButtonEvent:` arm is new. Post the events from
`internal/script/script_events.go`, next to `postInteractEvent`, straight to
`flang.RefVal{Kind: flang.RefEntity, A: "player"}` — no containment lookup
needed. Add each new atom to `docs/guide/18-scripting.md`'s event table and
`internal/script/script_event_doc.go`'s `eventDocs` — `eventDocs`' own
neighbouring entries are the format to match, and
`TestEventDocsMatchPosts` (`script_event_doc_test.go`) will fail the build
if a new atom is posted and not documented, or the reverse. It only AST-scans
`eventPostSources` (`script_events.go`, `script_actions.go`
today) for `rt.post`/`rt.postFrom` calls, so posting the new events from
`script_events.go` — the recommendation above — keeps this test honest for
free; a new file posting them would need adding to that list. Also add the
new atoms to `keybindings.go`/`docs/guide/21-keybindings.md`.

### Why held-vs-pressed, not a per-frame poll

A per-tick `is_down(:primary_fire:)`-style query builtin was the other option
and was rejected for this write-up: it would need a new value type
(`flang` has no "input state" concept) and a script would still have to build
its own held/not-held state machine on top of it. A press/release event pair
gives the script `@self.state["firing"] = true` directly and is one line to
turn into automatic fire, self-rescheduling exactly like
`enemy_combat.flang`'s `on_grenade_check` already does:

```
def on_primary_fire(:primary_fire:) -> null
    @self.state["firing"] = true
    fire_once()
end

def fire_once() -> null
    if not @self.state["firing"]
        return
    end
    play_anim(@player, "hands", "rifle_fire")
    hitscan(:hit:)
    @player <- after(180, :fire_tick:)   -- rifle_fire's own duration/rate
end

def on_fire_tick(:fire_tick:) -> null
    fire_once()
end

def on_primary_fire_end(:primary_fire_end:) -> null
    @self.state["firing"] = false
end
```

Semi-auto (pistol) just never reschedules `:fire_tick:`. This sketch is
illustrative, not a spec — whoever builds this should decide the actual rate
per weapon, how `hitscan`'s target resolves to damage, and how weapon-switch
interacts with a mid-reload or mid-throw animation (probably: it doesn't,
until the current one-shot clip's `:anim_end:` fires — see
`enemy_combat.flang`'s own comment on why one-shot clips need that).

### Open questions for whoever picks this up

- Does `:primary_fire:` fire once immediately on press (shown above) or only
  on the first `:fire_tick:`? The sketch above double-fires on a very fast
  press-release if not careful — worth a test once this exists.
- Grenade throw needs the same "spawn the live entity at the clip's release
  frame" pattern `enemy_combat.flang`'s `on_grenade_check`/`on_release` use,
  aimed by camera direction instead of a known target position — `gfx.Cam`
  isn't reachable from script; the aim direction needs to come in as
  something script-visible (a new read-only `.prop` on `@player`, most likely
  — camera direction isn't currently exposed to scripts at all, only
  position and rotation are, per `script_props.go`, and rotation is not the
  same thing for the player, whose look direction is `gfx.Cam.Yaw/Pitch`, not
  an `Entity.Rotation` field it has never had a reason to keep in sync).

---

## Part 2 — the editor panel

### What the user asked for

A panel to edit `player: models:` visually instead of by hand, **and** —
explicitly — one that isn't player-only: it should also edit any AI entity's
model attachments. Read on before assuming that means duplicating work: most
of the second half is already free.

### The reusable half already exists

`internal/editor/editor_entity_panel.go`'s `editorEntityModels(e
*world.Entity)` (and its two helpers, `editorEntityModelSection`/
`editorEntityModelBody`, `entity_panel.go:424-553`) is **already** a
plain function of a `*world.Entity` — not of the entity-editor's globals the
way `editor_entity_editor_panel.go`'s near-duplicate `entityPanelModel` is.
It's what draws the Models section of a **placed entity's** properties panel
today, which already covers every placed AI entity (`enemy_rifle`,
`enemy_pistol`, a placed `guard`, anything) — click one in the viewport, its
properties panel has this section already. Add/remove attachment, mesh
picker, texture override, `editorModelMountUI` (Parent/Bone — the same shared
widget the entity editor and this panel both already use,
`editor_model_mount_panel.go`), Offset/Rotation/Scale/Alpha, and the
animation list with in-editor playback — all of it, already generic, already
working, already reachable for an AI entity **today** by selecting it in the
viewport.

So "edit any AI entity" may already be satisfied depending on what the user
actually wants: if it's "let me get to an AI entity's models without hunting
for it in the viewport," the missing piece is a **selector**, not a new
editor. If it's something richer (side-by-side player/enemy loadout
comparison, or editing an entity *type's* models — which is a different
screen, the entity editor — rather than one placed *instance*), that's worth
confirming with the user before building. This write-up assumes the first
reading; it's the smaller, more clearly-scoped one and the one the wording
("edit any AI entity") most directly supports.

### The one thing that is genuinely new: reaching the player at all

`world.PlayerEntity` is a real `*world.Entity` (`internal/world/entity.go`),
so `editorEntityModels(world.PlayerEntity)` would, mechanically, draw the same
UI for it. Two things stop that from being the whole job:

1. **`world.PlayerEntity.Models` and `lvl.Player.Models` are separate
   slices.** `level_loader.go` copies one into the other
   (`append([]EntityModel(nil), level.Player.Models...)`) on load — a value
   copy, not a shared backing array. Editing `PlayerEntity.Models[i].Offset`
   in a panel would not touch `lvl.Player.Models[i].Offset`, and saving the
   level (`PlayerSettingsToRaw(lvl.Player)`) would silently write back the
   *old* values. This is the bug shape to design out from the start, not
   discover after the panel looks like it works.

   The fix that needs the least new code: build the temporary entity the
   panel edits **from `lvl.Player.Models` directly**, and write the (possibly
   restructured, if an attachment was added/removed) slice back after:

   ```go
   tmp := &world.Entity{ID: "player", Models: lvl.Player.Models}
   editorEntityModels(tmp)      // may reassign tmp.Models — add/remove does
   lvl.Player.Models = tmp.Models
   ```

   Field-level edits (`m.Offset = ...` etc.) write straight through the
   shared backing array either way; only a structural change (add/remove)
   needs the slice header copied back, which is what the last line is for.
   `lvl.Player` is *the* source of truth here, the same way `lvl.Entities[i]`
   already is for a normal placed entity — there is no separate "player
   definition" file for it to disagree with.

2. **The live preview.** `world.PlayerEntity.Models` (a *different* set of
   `EntityModel`s than `lvl.Player.Models` until something syncs them) is
   what actually has GPU buffers and a bone palette — `editorEntityModels`
   itself doesn't load or render anything, it only edits fields, so a change
   made through the temporary entity above needs `.Load()` called again
   before anything shows it, exactly like `reloadActiveEntityModels()` already
   does for a placed entity (`editor_entity_panel.go:442-447`) and like
   `entityMarkDirty()`/`entityPreviewInvalidate()` do for the entity editor's
   own preview object. And `RenderViewmodel()` is gated `!devActive()` today
   — with nothing calling it, there is **no 3D preview at all** while this
   panel is open, however correct the data ends up being. Cheapest fix:
   lift that gate specifically while the Player section of the panel is open
   (a package-level `bool` `editor` sets, `render` reads — same shape as
   `render.DebugToggle()`), not unconditionally, since a floating gun
   overlaid on the free-fly camera at all other times would be noise while
   placing brushes. Whether that reads as "the player, looking where the
   free-fly camera happens to be pointed" is a good enough preview, or
   whether it wants its own fixed preview camera the way the entity editor's
   3D view is its own separate window, is a judgement call for whoever builds
   this to make by trying the cheap version first.

### Where the panel itself goes

`internal/editor/editor_player_panel.go`'s `editorPlayerSection` already
exists as the level-scoped, selection-free home for exactly this kind of
thing (`editorPropGroup("Movement##playermovegrp", ...)` is its only group
today). Add a sibling:

```go
editorPropGroup("Viewmodel##playermodelsgrp", 0, func() {
    tmp := &world.Entity{ID: "player", Models: lvl.Player.Models}
    editorEntityModels(tmp)
    lvl.Player.Models = tmp.Models
})
```

For the "any AI entity" selector, the natural spot is either right above that
(a combo: "Player" plus every `lvl.Entities[i]` with `.AI != nil`, switching
which target `editorEntityModels` is called on) inside this same section, or
as an entirely separate toolbar tool if the intent is closer to a dedicated
loadout-comparison window. There's no existing helper enumerating a level's
AI entities — `internal/ai/ai.go`'s own `Update` just inlines
`for i := range lvl.Entities { if e.AI == nil { continue } ... }`
(`ai.go:87-90`); worth promoting to an exported `world.AIEntities(lvl)
[]*Entity` if this panel and anything else both end up wanting the list.

### Verifying any of this without a GL context

Found the hard way this session: `levelio.LoadLevel` on a real level
segfaults with no GL context (it uploads chunk/brush geometry
unconditionally — `BrushMesh.Load` → `gl.GetUniformLocation` with
`Program==0`, the same "a GL call with no context segfaults rather than
errors" trap `gfx.UpdateCamMatrix`'s own comment already warns about). To
check a `player:` block's *shape* headlessly — bones exist, clips resolve,
sockets resolve — parse just the raw YAML and stop short of `.Load()`:

```go
data, _ := os.ReadFile(levelPath)
var raw world.RawLevel
yaml.Unmarshal(data, &raw)
settings := world.PlayerSettingsFromRaw(raw.Player)
// settings.Models[i].Path -> model.LoadModel(...) for CPU-only bone/clip checks;
// never call EntityModel.Load()/Entity.Load() outside a real GL context.
```

This is exactly `levelio.readRawLevelFile` (unexported, hence reimplemented
above) plus the conversion `level_loader.go` already calls. Testing the
*rendered* result (the depth-clear behaviour, the camera-relative placement)
needs the real app running — there's no headless GL test harness for the
render package today beyond the `FOUNDRY_GL_TEST=1` unit tests, and this
pass isn't covered by any of them yet.
