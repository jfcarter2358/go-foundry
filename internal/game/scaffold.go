package game

// scaffold.go — a resources/ folder the engine can start in.
//
// Every asset the engine reads resolves under resources/, beside the
// executable, in the editor and in a shipped game alike. A binary run from a
// directory with no resources/ in it — a new project, a fresh dist/ — would
// otherwise come up with every asset folder missing: a texture picker with
// nothing to scan, a first save into a folder that is not there, and a Release
// window with no manifest to read. So startup lays the folders out, empty, and
// writes a manifest to fill in.
//
// Only what is missing is made. Nothing here overwrites a file or touches a
// folder that already exists, which is what makes it safe to run on every boot.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultManifest is the game.yaml Scaffold writes when there is none.
//
// It parses: a manifest written by the engine that the engine then refuses to
// boot would help nobody. Its title is a placeholder that is plainly one — see
// the note on Default for why a title is never filled in silently — and its
// start level is the name the editor saves a level under until it is given one
// (app/startup_dev.go), so the first level saved in a new project is the one
// its release build starts on.
const DefaultManifest = `# game.yaml — what this project is when it is built as a game.
#
# Written by Foundry because resources/ had none. A development build reads it
# only for the Release window; a release build reads it at boot. See
# docs/guide/20-shipping.md.
title: Untitled Game
version: 0.1.0
# The level a release build boots into, by name, from resources/levels/.
# "untitled" is what the editor saves a level as until it is given a name.
start_level: untitled
# window:
#   width: 1280
#   height: 720
#   fullscreen: false
`

// Scaffold makes each of dirs under resources, and a default game.yaml in it,
// wherever one is missing. It returns what it created, relative to resources.
//
// One failure does not stop the rest: a folder that cannot be made is a problem
// for whatever reads it, not for the folders beside it. The failures come back
// joined, for the caller to log.
func Scaffold(resources string, dirs []string) ([]string, error) {
	if err := os.MkdirAll(resources, 0755); err != nil {
		return nil, err
	}

	var created []string
	var errs []error
	for _, dir := range dirs {
		path := filepath.Join(resources, dir)
		info, err := os.Stat(path)
		if err == nil {
			if !info.IsDir() {
				errs = append(errs, fmt.Errorf("%s is a file, not a folder", path))
			}
			continue
		}
		if err := os.MkdirAll(path, 0755); err != nil {
			errs = append(errs, err)
			continue
		}
		created = append(created, dir)
	}

	// O_EXCL rather than a stat first: an existing manifest is never
	// overwritten, whatever else starts at the same moment.
	path := filepath.Join(resources, FileName)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	switch {
	case errors.Is(err, os.ErrExist):
	case err != nil:
		errs = append(errs, err)
	default:
		_, werr := f.WriteString(DefaultManifest)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			errs = append(errs, werr)
		} else {
			created = append(created, FileName)
		}
	}

	return created, errors.Join(errs...)
}
