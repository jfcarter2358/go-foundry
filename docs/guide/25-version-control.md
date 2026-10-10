# Version control

Everything a game is made of — levels, textures, models, scripts, screens — lives
in `resources/`. **Window > Git**, in any editor, opens a window that
keeps that folder under git without leaving the editor: see what changed, commit
it, switch branches, and push and pull.

The window needs `git` installed and on the `PATH`. It runs the real `git`
program, so anything you do in the window you could also do in a terminal, and
anything done in a terminal shows up in the window within a couple of seconds.

## Three kinds of folder

What the window offers depends on where `resources/` sits.

- **In no repository.** The window offers **Initialise repository**. That makes
  `resources/` a git repository of its own and writes a `.gitignore` that leaves
  out the `*.bak` copies every save keeps beside the file it saved. Nothing is
  committed until you commit it.
- **A repository of its own.** Everything works on `resources/` alone.
- **A folder inside a bigger repository**, such as a game's project repository
  or the engine's own checkout. The window says which repository that is. It
  lists, stages and commits only what is under `resources/`. The branch, **Pull**
  and **Push** still belong to the whole repository, because git has no smaller
  unit for them.

Neither `.git/` nor the `.gitignore` ends up in a release build: the packer
copies only the asset folders and skips hidden files.

## Setting up

Two things git needs appear at the top of the window until they are set:

- **A remote**, to push to and pull from. Paste its URL and press **Add remote**.
  It is saved as `origin`.
- **A name and email** to sign commits with. **Save** writes them to this
  repository's own config, not your global one.

The editor cannot answer git's prompts. A remote that asks for a password needs
an SSH key or a credential helper, such as Git Credential Manager or `gh auth
setup-git`, set up beforehand. Without one, a push fails and says why rather
than waiting for input.

## Changes

The **Changes** tab lists every file under `resources/` that differs from the
last commit. Each file's colour says what state it is in:

| Colour | Meaning |
| --- | --- |
| Green | Staged: it goes in the next commit |
| White | Changed, but not staged |
| Red | Left unresolved by a merge |

Tick a file to stage it, and untick it to take it back out. **Stage all** and
**Unstage all** do every file at once. A file can be *partly staged* if it
changed again after you ticked it. Its tooltip says so, and ticking it again
stages the rest.

Select a file to see its diff on the right. A new file is shown whole. An image
or a model shows only "Binary files differ", because there are no lines to
compare.

**Discard changes…** puts the selected file back the way the last commit has it,
after asking first. A file that was never committed is deleted, and a deleted
file cannot be got back.

Write a message under the list and press **Commit**. The button stays disabled
until it has something to commit. The line beside it says what is missing.

> Git commits what is **saved on disk**. The level open in front of you is not
> saved until you press Save, so save first.

## Branches, pull and push

The branch dropdown checks out another branch. Type a name beside it and press
**Create** to start a new branch from where you are. Uncommitted changes come
along with you.

The line beside the branch shows how many commits there are to push and to pull.
**Fetch** refreshes those counts without touching any files.

- **Pull** brings in the upstream's commits. It only fast-forwards. If both
  sides have new commits, it stops and says so rather than merging. Merging two
  people's edits to one texture is not something the window can resolve, so do
  that in a terminal or a git client.
- **Push** sends your commits. The first push of a new branch creates it on
  `origin` and tracks it from then on.

Only one git command runs at a time. While one runs, its name and elapsed time
show under the toolbar with a **Cancel** button. You can close the window and the
command carries on. The Window menu reads **Git (running...)** until it finishes.

## After a pull

Pulling, switching branch and discarding all rewrite files on disk. The level
open in the editor was loaded before that, so the window offers
**Reload level**. Reloading loses any unsaved edits to the level. Textures and
models already loaded keep their old versions until the editor restarts.

## History

The **History** tab lists the last hundred commits that touched `resources/`.
Select one to see its message and what it changed.
