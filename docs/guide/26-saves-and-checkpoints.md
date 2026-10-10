# Saves and checkpoints

A game saves in two ways. **Checkpoints** are places in a level: the first time
the player walks into one, the game autosaves, and the death screen's **Retry**
puts them back there. **Save slots** are the player's own saves, made from the
pause menu and loaded from the pause menu or the main menu.

Both use the same save format, so a slot can hold a checkpoint. The main
menu's **Resume** loads whichever save is newest, the autosave included.

## What a save is

A save does not contain the level. It records the level's name and everything
play has changed in it:

- where every entity stands and what it is doing, including its attributes,
  its AI and its animation
- which chunks are on
- where dynamic brushes have moved to
- the state of every light, sound, emitter, sprite and volume
- the sky
- every script's `.state` and pending `after` timers
- the player: position, view, health, ammo, and whatever else is in
  `attr`/`state`
- the screens that were up, such as the HUD

Loading a save loads the level from its own file, exactly as `load_level`
does, and then applies the recorded changes on top. This has three
consequences:

- **A save survives the level being edited.** Objects are matched by ID. One
  the save knows about but the level no longer has is skipped. One the level
  has but the save does not know about stays where the author put it. The
  exception is an object the save recorded as **destroyed**: if a new object
  reuses that ID, it is taken out too, so avoid reusing IDs.
- **Things a script spawned come back.** They are made again from their type
  under the same ID. Things a script destroyed stay destroyed.
- **A compiled level and an authored one save the same way.** A release build
  saves against the `.fbsp`, and a development build saves against the `.yaml`.
  Both work because the save only names the level.

Some things are deliberately not restored, and restart instead:

- live particles
- how far through its clip a sound was
- a music crossfade
- one-shot effects such as a gunshot or an explosion

Saves are files in `saves/` in the player's data directory, next to
`settings.yaml`. A development build writes them to the project directory, and
the packer never ships them.

## Checkpoints

To make a checkpoint, tick **Checkpoint** in a volume's properties.

```yaml
volumes:
  - id: checkpoint_lz
    position: {x: -1.5, y: -3.75, z: -75}
    half: {x: 20, y: 3.25, z: 25}
    checkpoint: true
```

A checkpoint behaves like a level exit:

- Only the **player** triggers one.
- Only while the volume is **enabled**.
- It fires on the way in.

Each checkpoint fires **once per play-through**. The set of checkpoints
already reached is part of the save, so retrying from one does not save it
again the moment the player stands up.

When the player reaches a checkpoint:

1. The game is saved to the **autosave** slot, between frames, once the tick
   that reached it has finished.
2. That save becomes the one **Retry** returns to.
3. `(:checkpoint:, name)` is posted to the player's script and to every screen
   that is up, after the file is on disk. The shipped HUD shows "Checkpoint
   reached" when it receives this.

A script can reach a checkpoint without a volume, for example when a door
opens or a boss dies:

```
checkpoint("boss_down")
```

The name follows the same once-per-play-through rule as a volume's ID.

Make a checkpoint box tall enough to catch the player standing on the floor,
but no taller than needed. The test point is the centre of the player's body,
about 0.9 above their feet. In level1 the landing-zone checkpoint stops short of
the ship's hold, so it does not fire while the player is riding the ship down.

## Retry and Restart Level

The death screen has two buttons:

| | |
| --- | --- |
| **Retry** — `retry()` | Returns to the last checkpoint reached. If none has been reached yet, restarts the level. |
| **Restart Level** — `restart_level()` | Reloads the level from scratch and forgets its checkpoints. |

The pause menu's **Load Last Checkpoint** does the same as Retry. It is greyed
out until the player has reached a checkpoint in the level, which the menu
checks with `@level.prop.last_checkpoint`. That property holds the
checkpoint's name, or `""` if none has been reached.

The main menu's **Resume** loads the most recent save, whether that is the
autosave or one of the player's slots. It is greyed out when there are no
saves.

The last checkpoint is kept in memory and is also stored inside every save.
Dying after loading a save therefore retries from the checkpoint that save was
made after, not from the save itself.

## Save mode

The level's **Saving** group sets what a save made in that level contains:

| | |
| --- | --- |
| `exact` (default) | The game exactly as it stands, down to where the player is. |
| `checkpoint` | The last checkpoint reached, or the start of the level before the first one. Use this when the design depends on the stretch between two checkpoints being played through in one go. |

```yaml
save_mode: checkpoint
```

Scripts can read it as `@level.prop.save_mode`.

## The script side

| | |
| --- | --- |
| `save_game(slot)` | Save to a slot (`"slot1"`). The save happens between frames, not at the call. |
| `load_game(slot)` | Load a slot. Returns false, and does nothing, if the slot is empty. |
| `save_info(slot)` | `{slot, level, kind, checkpoint, time, date, play_time}`, or null if the slot is empty. |
| `latest_save()` | The most recently saved slot, autosave included — what the main menu's Resume loads. |
| `delete_save(slot)` | Empty a slot. |
| `checkpoint(name)` | Reach a checkpoint from a script. |
| `retry()`, `restart_level()` | See above. |
| `@level.prop.name` | The running level's name. |
| `@level.prop.last_checkpoint` | The checkpoint `retry()` returns to, or `""`. |

A slot name is any name without a path in it. The shipped screens
(`ui/save_game.fui`, `ui/load_game.fui` and `scripts/menus/saves.flang`) use
`autosave` and `slot1` to `slot5`, and do not let the player save over the
autosave.

## Checking a level

`foundry sim` can save and restore a level mid-run:

```sh
foundry sim -level level1 -seconds 20 -restore-at 4,12
```

At each `-restore-at` time it does three things:

1. saves the game
2. loads the level fresh
3. restores the save, then saves again and prints every object that differs
   between the two saves

A clean run prints only `SAVED ... RESTORED ...` lines. Each checkpoint the
player reaches also prints as `CHECKPOINT <name>`, so `-at` can walk the player
through the level to check where checkpoints fire.
