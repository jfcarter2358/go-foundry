package editor

// editor_biomes.go — keeping the editor's biomes in step with biomes/*.fbio.
//
// A biome file can change under the editor from anywhere: a text editor, a
// branch switched in the Git window, Claude. Each of those should show on the
// terrain without a restart, so the directory is polled once a second
// (stamp-gated, so an idle poll is a ReadDir and a stat per file) and a file
// that moved is re-read and every terrain painted with it rebuilt. A file
// that no longer parses keeps the biome as it was and says why, in the log and
// in the terrain panel, rather than reverting it to the built-in behind the
// author's back.

import (
	"log"
	"maps"
	"slices"
	"time"

	"foundry/internal/world"
)

var biomePoll struct {
	stamps map[string]string
	last   time.Time
	// errors are the files that did not parse on their last read, by name.
	errors map[string]string
}

// PollBiomeFiles re-reads any biome file that changed on disk since the last
// look. Called between frames from the frame loop.
func PollBiomeFiles() {
	if time.Since(biomePoll.last) < time.Second {
		return
	}
	biomePoll.last = time.Now()
	now := world.BiomeFileStamps(world.BiomesDir())
	if biomePoll.stamps == nil {
		// The first look only records: app.Run has just loaded them all.
		biomePoll.stamps = now
		return
	}
	var changed []string
	for name, st := range now {
		if biomePoll.stamps[name] != st {
			changed = append(changed, name)
		}
	}
	for name := range biomePoll.stamps {
		if _, ok := now[name]; !ok {
			changed = append(changed, name)
		}
	}
	biomePoll.stamps = now
	for _, name := range changed {
		if n, err := reloadBiome(name); err != nil {
			log.Printf("WARNING: biome %s: %v", name, err)
		} else if n > 0 {
			log.Printf("biome %s changed on disk; %d terrain(s) regrowing", name, n)
		}
	}
}

// reloadBiome re-reads one biome file and rebuilds the terrains that paint
// with it. Returns how many terrains it rebuilt.
func reloadBiome(name string) (int, error) {
	if biomePoll.errors == nil {
		biomePoll.errors = map[string]string{}
	}
	if err := world.ReloadBiome(world.BiomesDir(), name); err != nil {
		biomePoll.errors[name] = err.Error()
		return 0, err
	}
	delete(biomePoll.errors, name)
	biomePoll.stamps = world.BiomeFileStamps(world.BiomesDir())
	return world.TouchTerrainsUsing(Editor.level, name), nil
}

// biomeFileErrors lists the biome files that do not parse, by name, sorted.
func biomeFileErrors() []string {
	return slices.Sorted(maps.Keys(biomePoll.errors))
}
