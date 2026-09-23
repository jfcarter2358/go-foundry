# Claude Code in the editor

**Window > Claude Code**, in any editor, opens a chat panel that can build things
in this project: write and fix `.flang` scripts, edit level and entity YAML, add
generators under `tools/`, and explain what something in the codebase does.

It runs the real `claude` command, the same one you would run in a terminal. So
it works on the same files, uses the same login, reads the same `CLAUDE.md`, and
a conversation started here can be picked up in a terminal and the other way
round.

What the panel adds over a terminal is that Claude can see the editor: what level
is open, what is selected, whether a script still checks out against the level
that is actually loaded, and what the viewport looks like right now.

## Before it works

You need Claude Code installed and signed in. The panel finds it in the usual
places, including the copy the VS Code extension ships, so if you already use
Claude Code in your editor there is probably nothing to do. If it cannot find it,
the panel says where it looked; install it from
[claude.com/claude-code](https://claude.com/claude-code), or set `binary:` in
`claude.yaml` beside the project.

If it is found but never answers, run `claude` once in a terminal to sign in.

## Asking for something

Type and press **Enter**. Ctrl+Enter gives you a new line instead.

The message carries a note about what the editor is looking at, so **this** and
**the selected brush** mean what you expect:

> make this brush a door that opens when the player is within 3 metres

The line above the input box shows that note before it is sent. Untick it to ask
about something unrelated to whatever happens to be selected.

Right-click any message, or any block of code in one, to copy it — ImGui's text
cannot be selected with the mouse, so this is the way out of the panel.

Claude works in turns: it reads files, makes changes, and says what it did. While
a turn runs the editor keeps working — the panel is a separate window and nothing
in it blocks a frame. **Stop** ends a turn early.

## Permission

By default Claude asks before it changes a file or runs a command. Each request
appears in the conversation with three answers:

- **Allow** — this once.
- **Allow all edits** — stop asking about file edits for the rest of this
  conversation. Commands still ask.
- **Decline** — with an optional note saying why, which Claude reads and usually
  acts on. "not that file, use the other chunk" is a useful thing to type here.

The picker at the top sets the starting mode:

| Mode | What it means |
| --- | --- |
| **Ask** | Ask before every change and command. |
| **Accept edits** | File edits go through; commands still ask. |
| **Plan** | Read and plan only. Nothing is changed. Good for "how would you…". |

The editor's own tools never ask. They are the editor answering questions about
itself.

## What Claude can see of the editor

Alongside the files, Claude has these:

| Tool | What it does |
| --- | --- |
| **editor_state** | The open level, the active chunk, the selection, and which editors have unsaved changes. |
| **viewport** | A screenshot of the 3D view as it is now. This is how it checks that a light it placed is not inside a wall. |
| **lint_scripts** | Runs the level-aware script checks against the level actually loaded — names the level does not have, animations an entity never declared. |
| **reload** | Makes the editor re-read a file. |
| **select_object** | Selects a brush by id, so you can see what it is talking about. |
| **script_api** | Every flang builtin with its signature, and the properties each kind of object has, from the engine's own tables. |
| **level_schema** | Every key a level file, entity type or `game.yaml` may contain, from the schema's own comments. |
| **list_assets** | The names of the project's textures, models, scripts, entity types, effects, sounds and levels. |
| **context_volume** | Everything inside a region you named — see below. |

The last three are why it writes fewer broken files than a terminal session
would. A level file names assets by **name**, not by path, and a key the schema
does not have is dropped in silence when the level loads — so "does this key
exist" and "is there a texture called this" are exactly the questions worth
answering from inside the engine rather than by guessing from the files.

The viewport tool shows what is on screen, so point the camera at the thing you
are asking about — or name a context volume and ask Claude to look at it, which
is the one thing that moves the camera for you.

## Naming a place: context volumes

A level file is one long list of brushes in chunk-local coordinates. Nothing in
it says where *the mess hall* is, so "restyle the mess hall" would mean working
out which forty of four hundred brushes that is — from vertex arrays.

A **context volume** is you saying where a place is. Place a volume around the
room, give it an ID, and tick **Describe this region to Claude** in its
*Assistant Context* group. Then the name means something:

> take a look at @mess_hall and change the style of the room to something
> warmer — less steel, more worn wood

What Claude gets back is the region's bounds and everything in it: every brush
with its position, size and textures, every entity with its type and tags, the
lights, sprites, sounds and triggers. Brushes that straddle the edge are
included and counted separately, because a room's walls always do.

The optional **note** is for what the geometry cannot say — "the crew's mess —
lived-in, warm light". It is the one part of a room's description that has to
come from you.

Saying *look at* also points the editor's camera at the region, so Claude can
take a screenshot of it. Your selection is left alone.

Context volumes change nothing about the game. They are not triggers, they draw
nothing, and a level ships with them exactly as it behaves without them. The
panel lists the ones in the open level when a conversation is empty, so you can
see what there is to point at.

## Unsaved work, and changes underneath you

Claude reads files from disk. Anything you have not saved, it cannot see — so
when an editor has unsaved changes the panel says so above the input box, with
**Save All** beside it.

The other direction is handled for you. When Claude changes a file the editor is
holding, the editor picks it up:

- **Scripts, models, entity types and particle effects** are re-read straight
  away, including any script tab you have open on one.
- **A file you have unsaved edits to** is never overwritten. The panel names it
  and leaves your version alone.
- **The open level** is always offered rather than taken, because reloading it
  starts the undo history again. **Reload level** takes Claude's version;
  **Keep mine** ignores it.

## Long conversations

Only the last stretch of a conversation is drawn, with a **Show earlier** button
above it. Everything is still there — Claude has the whole thing, and so does the
session on disk — but ImGui lays out every row it is handed whether or not it is
on screen, and a day's work would cost a frame.

## Conversations

**New** starts a fresh conversation. **Resume…** lists the ones this project has
had, newest first, with the first thing you said in each. Resuming brings the
whole history back — Claude remembers it, though the panel starts its transcript
from where you pick up.

The model and permission mode are remembered in `claude.yaml` beside the project.
It is the editor's own file and never ships in a release.

## Making it better at this project

Claude is told the shape of a Foundry project — where things live, what the file
types are, that textures are generated by Python under `tools/` — and pointed at
`SCRIPTING.md` and this guide.

Everything beyond that belongs in a `CLAUDE.md` in the project, which Claude
reads on its own. That is the place for what is true of *your* game: the naming
conventions, which levels are generated and must not be hand-edited, the way you
lay out chunks. It also applies when you run `claude` in a terminal, which a
setting in the editor would not.

## What it is not good at

- **Anything visual it cannot check.** Ask for a texture generator and it will
  write you one; ask whether the result looks rusty and it is guessing unless it
  takes a screenshot.
- **Large hand-edits to generated levels.** If a level comes out of a generator,
  point Claude at the generator.
- **Knowing what you have not saved.** Save first.
