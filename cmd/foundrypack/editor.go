package main

// editor.go — `foundrypack editor`: the editor itself, staged and archived for
// a Foundry release (task dist-editor). See internal/pack/editor.go.

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/pack"
	"foundry/internal/setup"
)

// multiFlag collects a flag given more than once.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func editorMain(args []string) {
	fs := flag.NewFlagSet("foundrypack editor", flag.ExitOnError)
	var (
		exe      = fs.String("exe", "", "the development build to pack (required)")
		goos     = fs.String("goos", "windows", "the platform it is built for: windows or linux")
		version  = fs.String("version", "0.0.0", "the Foundry version being packed")
		shaders  = fs.String("shaders", "resources/shaders", "the shaders the build was made from")
		guide    = fs.String("guide", filepath.Join("docs", "guide"), "the authoring guide to carry along; empty to leave it out")
		stage    = fs.String("stage", "", "install tree to write (required; created, and emptied if it exists)")
		outDir   = fs.String("out", "dist", "where the archive and installer go")
		stub     = fs.String("setup-stub", "", "the Windows installer program (cmd/foundrysetup); required for windows")
		libs     multiFlag
		runtimes multiFlag
	)
	fs.Var(&libs, "lib", "a runtime library to put beside the editor (repeatable)")
	fs.Var(&runtimes, "runtime", "a file for the install's runtimes/ (repeatable)")
	fs.Parse(args)
	if *exe == "" || *stage == "" || (*goos == "windows" && *stub == "") {
		fs.Usage()
		os.Exit(2)
	}

	exeName := "foundry"
	if *goos == "windows" {
		exeName += ".exe"
	}
	res, err := pack.StageEditor(pack.EditorOptions{
		Out: *stage, Exe: *exe, ExeName: exeName, GOOS: *goos, Version: *version,
		Shaders: *shaders, Guide: *guide, Libs: libs, Runtimes: runtimes,
	})
	if err != nil {
		log.Fatalf("%v", err)
	}

	base := "foundry-editor-" + *version
	platform := *goos + "-amd64"
	switch *goos {
	case "windows":
		archive := filepath.Join(*outDir, base+"-"+platform+".zip")
		if err := pack.Zip(*stage, archive, base, res.Stamp.Exe); err != nil {
			log.Fatalf("%v", err)
		}
		fmt.Println(archive)
		installer := filepath.Join(*outDir, base+"-"+platform+"-setup.exe")
		if err := setup.Write(*stub, *stage, installer); err != nil {
			log.Fatalf("%v", err)
		}
		fmt.Println(installer)
	case "linux":
		archive := filepath.Join(*outDir, base+"-"+platform+".tar.gz")
		if err := pack.TarGz(*stage, archive, base, res.Stamp.Exe); err != nil {
			log.Fatalf("%v", err)
		}
		fmt.Println(archive)
	default:
		log.Fatalf("no editor package for %s", *goos)
	}
}
