// mkbump authors a bump channel onto an existing texture, the way the texture
// editor would, so the result can be looked at in the running engine.
package main

import (
	"fmt"
	"log"
	"os"

	"foundry/internal/fileutil"
	"foundry/internal/texdoc"
	"foundry/internal/texture"
)

func main() {
	fileutil.ExecutablePath = os.Args[1]
	name := os.Args[2]

	texture.LoadGlobalPalette()
	d, err := texture.ImportPNG(fileutil.TexturesDir() + "/" + name + ".png")
	if err != nil {
		log.Fatal(err)
	}

	// A grid of raised square studs — the pattern whose relief is unmistakable
	// under a moving light, and impossible to confuse with the albedo.
	d.SetChannel(texdoc.ChanBump)
	d.SetBumpStrength(8)
	const cell = 32
	for y := 0; y < d.H; y++ {
		for x := 0; x < d.W; x++ {
			cx, cy := x%cell, y%cell
			if cx > 4 && cx < cell-4 && cy > 4 && cy < cell-4 {
				d.Layers[0].Idx[y*d.W+x] = 255
			}
		}
	}
	d.SetChannel(texdoc.ChanColor)

	if err := texture.SaveTexDoc(d, name); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s.ftex, %s.png and %s_normal.png (%dx%d, strength %.1f)\n",
		name, name, name, d.W, d.H, d.BumpStrength())
}
