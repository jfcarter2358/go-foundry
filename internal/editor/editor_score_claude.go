package editor

// editor_score_claude.go — a score or an instrument the Claude panel wrote.
//
// claudeApplyFileChange routes a written .fscore and .finst here:
//
//   - A score: the library's cached copy is dropped, so a level that plays
//     it next (a level load, play_music) hears the file as Claude left it,
//     as the Score editor's own Save does. The Score editor, when it holds
//     that score unedited, re-reads it; with edits of its own Claude is told
//     of the conflict and the edits stay.
//   - An instrument: the instrument and drum map caches are dropped, and
//     the Score editor's player prepares its score again, so the next note
//     plays the new sound and a kit's staff reads the new map.

import (
	"path/filepath"
	"strings"

	"foundry/internal/audio"
	"foundry/internal/score"
	"foundry/internal/tracker"
)

// scoreNameOf is a score file's library name: its path under sounds/
// without the extension, "" for a file outside it.
func scoreNameOf(abs string) string {
	rel, err := filepath.Rel(audio.SoundsDir(), abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return strings.TrimSuffix(filepath.ToSlash(rel), score.FileExt)
}

// scoreFileChanged takes a score file Claude wrote. It reports false when
// the Score editor holds that score with edits of its own (a conflict: the
// editor keeps them).
func scoreFileChanged(abs string) bool {
	score.Reload()
	name := scoreNameOf(abs)
	if scEd == nil || name == "" || scEd.Name != name {
		return true
	}
	// Fresh, not throttled: an edit made a moment ago counts.
	prev := saveStateFresh
	saveStateFresh = true
	dirty := scoreEditorDirty()
	saveStateFresh = prev
	if dirty {
		return false
	}
	if err := scoreLoad(name); err != nil {
		return false
	}
	scEd.Status = "Re-read " + name + score.FileExt + ": Claude changed it"
	return true
}

// scoreInstrumentChanged takes an instrument file Claude wrote: the
// caches go, and the Score editor's player hears the new sound.
func scoreInstrumentChanged() {
	tracker.ReloadInstruments()
	score.ReloadKits()
	if scEd != nil {
		scEd.laneRev++ // the staves read a kit's map again
	}
	if scorePlayer != nil {
		scoreSyncPlayer(true)
	}
}
