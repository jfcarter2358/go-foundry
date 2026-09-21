package main

// foundrypack — stage a release build into an install tree.
//
// The packing is internal/pack, which the editor's Release window runs too;
// this is its command line, for the Makefile. See internal/pack/pack.go for
// what a pack is and what the prune decides.

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"

	"foundry/internal/pack"
)

func main() {
	// `foundrypack editor ...` packs the editor itself for a Foundry release
	// rather than a game; see editor.go.
	if len(os.Args) > 1 && os.Args[1] == "editor" {
		log.SetFlags(0)
		log.SetPrefix("foundrypack editor: ")
		editorMain(os.Args[2:])
		return
	}
	var (
		src      = flag.String("src", "resources", "content directory to pack from")
		out      = flag.String("out", "", "install tree to write (required; created, and emptied if it exists)")
		exe      = flag.String("exe", "", "built game executable to copy in (required)")
		exeName  = flag.String("exe-name", "", "name to give the executable in the install tree (default: the manifest title, or the source name)")
		goos     = flag.String("goos", runtime.GOOS, "the platform this pack is for; decides which runtime libraries belong beside the executable")
		prune    = flag.Bool("prune", false, "copy only the assets reachable from the manifest")
		manifest = flag.String("manifest", "", "path to game.yaml (default: <src>/game.yaml)")
		verbose  = flag.Bool("v", false, "list every file copied, and every asset pruned")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "foundrypack — stage a Foundry release into an install tree.\n\nUsage:\n  foundrypack -exe build/foundry -out build/install [-prune]\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	log.SetFlags(0)
	log.SetPrefix("foundrypack: ")

	if *out == "" || *exe == "" {
		flag.Usage()
		os.Exit(2)
	}

	_, err := pack.Pack(pack.Options{
		Src:      *src,
		Out:      *out,
		Exe:      *exe,
		ExeName:  *exeName,
		GOOS:     *goos,
		Prune:    *prune,
		Manifest: *manifest,
		Verbose:  *verbose,
	})
	if err != nil {
		log.Fatalf("%v", err)
	}
}
