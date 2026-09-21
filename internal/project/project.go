// Package project decides which project a development build works on.
//
// The engine reads and saves every asset under a project's resources/, and
// used to take the project to be wherever the executable was. That still
// works — a checkout, or a game's own folder with foundry.exe copied into it —
// but an editor installed once from a Foundry release lives in a folder of its
// own and opens projects from anywhere: a clone of the game's repository in
// somebody's Documents, most likely. So the project is chosen at startup, in
// this order:
//
//  1. -project <dir> on the command line, or a folder given as the only
//     argument (what dropping a folder onto the program does on Windows);
//  2. the executable's own folder, when it holds a project
//     (resources/game.yaml) — every layout from before this package;
//  3. the project opened last, from the recent list;
//  4. a new, empty project in the user's Documents.
//
// File > Open Project and New Project restart the program on the folder
// chosen: every registry in the engine is filled from one project at boot,
// and a relaunch is the one way to empty all of them that cannot miss one.
//
// Nothing here is in a release build, whose project is its install.
package project

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/game"

	"gopkg.in/yaml.v2"
)

// Flag is the command-line flag naming a project.
const Flag = "project"

// IsProject reports whether dir holds a project: a resources/ with a manifest.
func IsProject(dir string) bool {
	_, err := os.Stat(filepath.Join(fileutil.ResourcesDir(dir), game.FileName))
	return err == nil
}

// Choice is the project startup settled on, and why.
type Choice struct {
	Dir    string
	Reason string
}

// Resolve picks the project for a development build started with args from an
// executable in exeDir. dataDir is where the recent list is kept.
func Resolve(exeDir, dataDir string, args []string) (Choice, error) {
	if dir, ok, err := fromArgs(args); err != nil {
		return Choice{}, err
	} else if ok {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return Choice{}, err
		}
		return Choice{abs, "named on the command line"}, nil
	}
	if IsProject(exeDir) {
		return Choice{exeDir, "beside the executable"}, nil
	}
	for _, dir := range LoadRecent(dataDir) {
		if IsProject(dir) {
			return Choice{dir, "opened last"}, nil
		}
	}
	return Choice{DefaultNewDir(), "a new project, as none was named or opened before"}, nil
}

// fromArgs finds a project named on the command line.
func fromArgs(args []string) (string, bool, error) {
	fs := flag.NewFlagSet("foundry", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String(Flag, "", "the project to open")
	if err := fs.Parse(args); err != nil {
		return "", false, fmt.Errorf("command line: %w", err)
	}
	if *dir != "" {
		return *dir, true, nil
	}
	if fs.NArg() == 1 {
		if info, err := os.Stat(fs.Arg(0)); err == nil && info.IsDir() {
			return fs.Arg(0), true, nil
		}
	}
	return "", false, nil
}

// DefaultNewDir is where a new project goes when nobody said where: a folder in
// Documents, or in the home directory when there is no Documents. Not
// created here; startup's scaffold does that.
func DefaultNewDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "Foundry Projects", "Untitled")
	}
	base := home
	if info, err := os.Stat(filepath.Join(home, "Documents")); err == nil && info.IsDir() {
		base = filepath.Join(home, "Documents")
	}
	return filepath.Join(base, "Foundry Projects", "Untitled")
}

// DataDirName is the per-user folder the editor keeps its own state in —
// today, the recent list.
const DataDirName = "Foundry Editor"

const recentFile = "projects.yaml"

// maxRecent bounds the recent list.
const maxRecent = 10

type recentList struct {
	Recent []string `yaml:"recent"`
}

// LoadRecent is the recent list, most recent first. A list that cannot be read
// is an empty one: nothing depends on it but a convenience.
func LoadRecent(dataDir string) []string {
	if dataDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dataDir, recentFile))
	if err != nil {
		return nil
	}
	var l recentList
	if yaml.Unmarshal(data, &l) != nil {
		return nil
	}
	return l.Recent
}

// Remember puts dir at the top of the recent list.
func Remember(dataDir, dir string) error {
	if dataDir == "" {
		return errors.New("no editor data directory")
	}
	list := []string{dir}
	for _, d := range LoadRecent(dataDir) {
		if !strings.EqualFold(filepath.Clean(d), filepath.Clean(dir)) && len(list) < maxRecent {
			list = append(list, d)
		}
	}
	data, err := yaml.Marshal(recentList{Recent: list})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, recentFile), data, 0644)
}

// CheckOpen says why dir cannot be opened as a project, or nil.
func CheckOpen(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("no folder named")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("%s does not exist", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a folder", dir)
	}
	if !IsProject(dir) {
		return fmt.Errorf("%s has no resources/%s, so it is not a project — use New Project to make one there", dir, game.FileName)
	}
	return nil
}

// CheckNew says why a new project cannot be made in dir, or nil. The folder
// may not exist yet, or may be empty — a fresh clone of an empty repository —
// but anything else in it would become part of a project nobody meant to make.
func CheckNew(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("no folder named")
	}
	if IsProject(dir) {
		return fmt.Errorf("%s is already a project — open it instead", dir)
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") { // .git, .gitignore
			return fmt.Errorf("%s already has files in it", dir)
		}
	}
	return nil
}

// Create makes the folder a new project will be scaffolded into by the
// program started on it.
func Create(dir string) error {
	if err := CheckNew(dir); err != nil {
		return err
	}
	return os.MkdirAll(fileutil.ResourcesDir(dir), 0755)
}
