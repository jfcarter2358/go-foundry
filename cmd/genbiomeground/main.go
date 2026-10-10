// genbiomeground writes the ground textures the built-in biomes lay over the
// terrain they are painted on to resources/textures/ground_<biome>.png. Run
// from the project root:
//
//	go run ./cmd/genbiomeground            # every biome
//	go run ./cmd/genbiomeground desert     # just the named ones
//
// The painting is internal/biomeground's, which the renderer also calls for a
// project with no file of that name — so a file is only needed to edit one.
// A new ground is a new recipe there.
package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"foundry/internal/biomeground"
)

func main() {
	dir := filepath.Join("resources", "textures")
	names := os.Args[1:]
	if len(names) == 0 {
		names = biomeground.Names()
	}
	for _, name := range names {
		img := biomeground.Image(biomeground.Prefix + name)
		if img == nil {
			fmt.Fprintf(os.Stderr, "no recipe for %q\n", name)
			os.Exit(2)
		}
		path := filepath.Join(dir, biomeground.Prefix+name+".png")
		if err := save(path, img); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("wrote", path)
	}
}

func save(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
