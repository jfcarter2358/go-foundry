package app

// savegame.go — acting on a script's save, load, checkpoint, retry and restart
// requests, between frames.
//
// What a save holds and how one is put back is internal/savegame's; this file
// is the when. Everything happens at the top of a frame, before the world
// ticks: a request made during the last tick's drain is answered against the
// world that tick finished with, and a load started here is covered by the
// loading screen from its first frame, exactly as load_level's is.
//
// A restore is a level load with a snapshot riding on it (pendingRestore),
// laid over the level once installLevel has put it in place.

import (
	"log"

	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/savefile"
	"foundry/internal/savegame"
	"foundry/internal/script"
	"foundry/internal/world"
)

// pendingRestore is the save to lay over the level pendingLoad is building, or
// nil for an ordinary load.
var pendingRestore []byte

// lastCheckpoint is the running level's last checkpoint, encoded — what
// retry() goes back to, and what a `checkpoint`-mode level saves. nil when the
// player has reached none since the level was loaded or restarted.
//
// Kept as bytes rather than as a Snapshot because restoring one hands its
// maps and pointers to the level's objects, which then go on to change them;
// decoding afresh each time is what lets the same checkpoint be retried twice.
var lastCheckpoint []byte

// setLastCheckpoint changes which checkpoint retry() returns to, and tells
// the scripts' `@level.prop.last_checkpoint` its name.
func setLastCheckpoint(data []byte, name string) {
	lastCheckpoint = data
	if data == nil {
		name = ""
	}
	hooks.SetLastCheckpoint(name)
}

// handleSaveRequests answers every request the last tick made.
func handleSaveRequests(level *world.Level) {
	reqs := hooks.TakeSaveRequests()
	// With the editor open there is no game to save; a request left from the
	// frame play ended on is dropped with the rest of the session — and so is
	// its checkpoint, or the next session's Retry would load the last one's.
	if devActive() {
		if lastCheckpoint != nil {
			setLastCheckpoint(nil, "")
		}
		return
	}
	for _, r := range reqs {
		switch r.Kind {
		case hooks.Checkpoint:
			saveCheckpoint(level, r.Name)
		case hooks.SaveToSlot:
			saveToSlot(level, r.Name)
		case hooks.LoadFromSlot:
			data, err := savefile.ReadData(r.Name)
			if err != nil {
				log.Printf("load_game(%q): %v", r.Name, err)
				continue
			}
			restoreSave(data)
		case hooks.Retry:
			if lastCheckpoint != nil {
				restoreSave(lastCheckpoint)
			} else {
				restartLevel(level)
			}
		case hooks.RestartLevel:
			restartLevel(level)
		}
	}
}

// saveCheckpoint takes a checkpoint's save: the autosave slot, and the one
// retry() comes back to.
func saveCheckpoint(level *world.Level, name string) {
	if pendingLoad != nil {
		return
	}
	data, err := savefile.Encode(savegame.Capture(level, savefile.KindCheckpoint, name))
	if err != nil {
		log.Printf("checkpoint %q: %v", name, err)
		return
	}
	setLastCheckpoint(data, name)
	if err := savefile.WriteData(savefile.AutosaveSlot, data); err != nil {
		// Still a checkpoint for retry, which is held in memory; only the
		// autosave on disk is missing.
		log.Printf("checkpoint %q: autosave: %v", name, err)
		return
	}
	log.Printf("checkpoint %q reached; autosaved", name)
	script.PostCheckpointSaved(name)
}

// saveToSlot writes the game to a slot, as the level's save mode says.
func saveToSlot(level *world.Level, slot string) {
	if pendingLoad != nil {
		log.Printf("save_game(%q): a level is loading; nothing to save", slot)
		return
	}
	var s *savefile.Snapshot
	if level.SaveMode == world.SaveCheckpoint {
		if lastCheckpoint == nil {
			s = savegame.Fresh(level.Name, savefile.KindManual)
		} else {
			var err error
			if s, err = savefile.Decode(lastCheckpoint); err != nil {
				log.Printf("save_game(%q): %v", slot, err)
				return
			}
			s.Kind = savefile.KindManual
		}
	} else {
		s = savegame.Capture(level, savefile.KindManual, "")
	}
	if lastCheckpoint != nil {
		ck, err := savefile.Decode(lastCheckpoint)
		if err == nil {
			s.LastCheckpoint = ck
		}
	}
	if err := savefile.Write(slot, s); err != nil {
		log.Printf("save_game(%q): %v", slot, err)
		return
	}
	log.Printf("saved to %q", slot)
}

// restoreSave starts loading a save's level with the save riding on it.
func restoreSave(data []byte) {
	s, err := savefile.Decode(data)
	if err != nil {
		log.Printf("loading a save: %v", err)
		return
	}
	startLevelLoad(s.Level, data)
}

// restartLevel loads the running level again from its file.
func restartLevel(level *world.Level) {
	if level.Name == "" {
		log.Printf("restart_level: the running level was never loaded from a file")
		return
	}
	startLevelLoad(level.Name, nil)
}

// applyPendingRestore lays the save a load was carrying over the level it has
// just installed, and settles which checkpoint the level now starts from:
// the save's own if it is a checkpoint, the one it recorded if not, and none
// after an ordinary load.
func applyPendingRestore(level *world.Level, data []byte) {
	setLastCheckpoint(nil, "")
	if data == nil {
		return
	}
	s, err := savefile.Decode(data)
	if err != nil {
		log.Printf("restoring a save: %v", err)
		return
	}
	switch {
	case s.Kind == savefile.KindCheckpoint && !s.Fresh:
		setLastCheckpoint(data, s.Checkpoint)
	case s.LastCheckpoint != nil:
		if ck, err := savefile.Encode(s.LastCheckpoint); err == nil {
			setLastCheckpoint(ck, s.LastCheckpoint.Checkpoint)
		}
	}
	if err := savegame.Apply(level, s); err != nil {
		log.Printf("restoring a save: %v", err)
		return
	}
	// The lights the save put back are uniforms, uploaded here rather than on
	// the first frame a script happens to touch one.
	world.UploadLightsTo(gfx.Program, world.SceneLights(level), false)
}
