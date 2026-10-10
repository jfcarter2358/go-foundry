package app

// resources.go — the resources/ folder every build starts in.
//
// The same for the editor and for a shipped game: whatever resources/ is
// missing of the asset folders and the manifest is made at boot. See
// internal/game/scaffold.go for what that does and does not touch.

import (
	"log"
	"path/filepath"

	"foundry/internal/audio"
	"foundry/internal/fileutil"
	"foundry/internal/game"
	"foundry/internal/levelio"
	"foundry/internal/material"
	"foundry/internal/model"
	"foundry/internal/particle"
	"foundry/internal/ui"
	"foundry/internal/uifont"
	"foundry/internal/video"
	"foundry/internal/world"
)

// resourceDirs are the asset folders startup makes under resources/. Named
// through each package's own constant where it has one, so a folder renamed
// where it is read is renamed here too. Shaders have none: they are named by
// path, as "shaders/<file>", wherever a program is loaded.
var resourceDirs = []string{
	world.EntitiesDirName,
	uifont.FontsDirName,
	levelio.LevelsDirName,
	material.MaterialsDirName,
	model.ModelsDirName,
	model.SkeletonsDirName,
	particle.ParticlesDirName,
	fileutil.ScriptsDirName,
	"shaders",
	audio.SoundsDirName,
	fileutil.TexturesDirName,
	ui.UIDirName,
	video.VideoDirName,
}

// scaffoldResources makes whatever resources/ is missing. It runs before the
// log file exists — ahead of startupConfigure, which reads the manifest this
// may write — so what it did is handed back for logScaffold to report.
func scaffoldResources() ([]string, error) {
	dirs := resourceDirs
	if fileutil.EngineShadersDir != "" {
		// The shaders come with the executable, so a project opened by it has
		// no use for a folder of its own.
		dirs = nil
		for _, d := range resourceDirs {
			if d != "shaders" {
				dirs = append(dirs, d)
			}
		}
	}
	return game.Scaffold(fileutil.ResourcesPath, dirs)
}

// logScaffold reports what startup made under resources/, and what it could
// not. Neither is fatal here: a folder that could not be made is a problem only
// for whatever reads it, and a manifest that could not be written is reported
// by the release build's own boot, which cannot start without one.
func logScaffold(created []string, err error) {
	for _, rel := range created {
		log.Printf("created %s", filepath.Join(fileutil.ResourcesPath, rel))
	}
	if err != nil {
		log.Printf("WARNING: could not set up %s: %v", fileutil.ResourcesPath, err)
	}
}
