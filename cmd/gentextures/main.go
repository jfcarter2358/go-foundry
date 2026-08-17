// gentextures generates procedural 64x64 PNG textures for the Foundry engine.
// Run from the repo root: go run ./cmd/gentextures
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand"
	"os"
)

const size = 64

func save(name string, img image.Image) {
	f, err := os.Create("resources/textures/" + name + ".png")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

func newRGBA() *image.NRGBA {
	return image.NewNRGBA(image.Rect(0, 0, size, size))
}

// lerp blends two uint8 values by t in [0,1].
func lerp8(a, b uint8, t float64) uint8 {
	return uint8(float64(a)*(1-t) + float64(b)*t)
}

// smoothNoise generates 2D smooth noise using bilinear interpolation of a random grid.
func smoothNoise(rng *rand.Rand, gridSize int) func(x, y int) float64 {
	cells := (size/gridSize + 2) * (size/gridSize + 2)
	grid := make([]float64, cells*cells)
	for i := range grid {
		grid[i] = rng.Float64()
	}
	stride := size/gridSize + 2
	get := func(gx, gy int) float64 {
		if gx < 0 {
			gx = 0
		}
		if gy < 0 {
			gy = 0
		}
		if gx >= stride {
			gx = stride - 1
		}
		if gy >= stride {
			gy = stride - 1
		}
		return grid[gy*stride+gx]
	}
	return func(x, y int) float64 {
		fx := float64(x) / float64(gridSize)
		fy := float64(y) / float64(gridSize)
		gx := int(fx)
		gy := int(fy)
		tx := fx - float64(gx)
		ty := fy - float64(gy)
		// smoothstep
		tx = tx * tx * (3 - 2*tx)
		ty = ty * ty * (3 - 2*ty)
		v00 := get(gx, gy)
		v10 := get(gx+1, gy)
		v01 := get(gx, gy+1)
		v11 := get(gx+1, gy+1)
		return v00*(1-tx)*(1-ty) + v10*tx*(1-ty) + v01*(1-tx)*ty + v11*tx*ty
	}
}

// fractalNoise sums octaves of smooth noise.
func fractalNoise(rng *rand.Rand, octaves int) func(x, y int) float64 {
	type layer struct {
		n    func(x, y int) float64
		amp  float64
		grid int
	}
	layers := make([]layer, octaves)
	total := 0.0
	amp := 1.0
	grid := 4
	for i := range layers {
		layers[i] = layer{smoothNoise(rng, grid), amp, grid}
		total += amp
		amp *= 0.5
		grid *= 2
		if grid > size {
			grid = size
		}
	}
	return func(x, y int) float64 {
		v := 0.0
		for _, l := range layers {
			v += l.n(x, y) * l.amp
		}
		return v / total
	}
}

// grass: green base with noise variation + darker blade lines
func genGrass(rng *rand.Rand) {
	img := newRGBA()
	noise := fractalNoise(rng, 4)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			n := noise(x, y)
			// base green: 50-90, 120-160, 30-60
			r := uint8(50 + n*40)
			g := uint8(120 + n*40)
			b := uint8(30 + n*30)
			// blade lines: thin vertical-ish stripes
			bx := (x * 7) % 5
			if bx == 0 {
				g = uint8(math.Max(0, float64(g)-30))
				r = uint8(math.Max(0, float64(r)-10))
			}
			img.SetNRGBA(x, y, color.NRGBA{r, g, b, 255})
		}
	}
	save("grass", img)
}

// moss: dense dark-green clumpy base with small darker crevice flecks —
// distinct from grass (no blade stripes; blotchier, damper-looking noise) and
// from dirt/rock (green, not brown/grey), for BiomeMoss's ground patch.
func genMoss(rng *rand.Rand) {
	img := newRGBA()
	noise := fractalNoise(rng, 5)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			n := noise(x, y)
			// base mossy green: 25-55, 65-105, 25-50
			r := uint8(25 + n*30)
			g := uint8(65 + n*40)
			b := uint8(25 + n*25)
			// crevice flecks: small dark clumps, denser than dirt's pebbles
			if rng.Float64() < 0.08 {
				r = uint8(math.Max(0, float64(r)-15))
				g = uint8(math.Max(0, float64(g)-35))
				b = uint8(math.Max(0, float64(b)-15))
			}
			img.SetNRGBA(x, y, color.NRGBA{r, g, b, 255})
		}
	}
	save("moss", img)
}

// dirt: brown earthy base with pebble-like speckles
func genDirt(rng *rand.Rand) {
	img := newRGBA()
	noise := fractalNoise(rng, 3)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			n := noise(x, y)
			r := uint8(100 + n*60)
			g := uint8(70 + n*40)
			b := uint8(40 + n*20)
			// pebbles: random dark spots
			if rng.Float64() < 0.04 {
				r = uint8(math.Max(0, float64(r)-40))
				g = uint8(math.Max(0, float64(g)-30))
				b = uint8(math.Max(0, float64(b)-20))
			}
			img.SetNRGBA(x, y, color.NRGBA{r, g, b, 255})
		}
	}
	save("dirt", img)
}

// rock: grey with fracture lines and variation
func genRock(rng *rand.Rand) {
	img := newRGBA()
	noise := fractalNoise(rng, 4)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			n := noise(x, y)
			v := uint8(90 + n*80)
			// crack pattern: diagonal lines
			crack := ((x + y*2) % 13) == 0 || ((x*2+y) % 17) == 0
			if crack {
				v = uint8(math.Max(0, float64(v)-50))
			}
			img.SetNRGBA(x, y, color.NRGBA{v, v - 5, v - 10, 255})
		}
	}
	save("rock", img)
}

// brick: red bricks with mortar lines
func genBrick(rng *rand.Rand) {
	img := newRGBA()
	noise := fractalNoise(rng, 3)
	brickW := 16
	brickH := 8
	mortarThick := 1
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			row := y / brickH
			// offset alternate rows
			ox := x
			if row%2 == 1 {
				ox = (x + brickW/2) % size
			}
			lx := ox % brickW
			ly := y % brickH
			isMortar := lx < mortarThick || ly < mortarThick
			n := noise(x, y) * 0.15
			if isMortar {
				v := uint8(160 + n*50)
				img.SetNRGBA(x, y, color.NRGBA{v, v, v - 10, 255})
			} else {
				r := uint8(170 + n*50)
				g := uint8(70 + n*30)
				b := uint8(50 + n*20)
				img.SetNRGBA(x, y, color.NRGBA{r, g, b, 255})
			}
		}
	}
	save("brick", img)
}

// stone_floor: large stone tiles with grout
func genStoneFloor(rng *rand.Rand) {
	img := newRGBA()
	noise := fractalNoise(rng, 3)
	tileW := 20
	tileH := 20
	groutThick := 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			lx := x % tileW
			ly := y % tileH
			isGrout := lx < groutThick || ly < groutThick
			n := noise(x, y)
			if isGrout {
				v := uint8(80 + n*20)
				img.SetNRGBA(x, y, color.NRGBA{v, v, v, 255})
			} else {
				v := uint8(130 + n*50)
				img.SetNRGBA(x, y, color.NRGBA{v, v - 5, v - 15, 255})
			}
		}
	}
	save("stone_floor", img)
}

// wood_floor: horizontal wood planks with grain lines
func genWoodFloor(rng *rand.Rand) {
	img := newRGBA()
	noise := fractalNoise(rng, 4)
	plankH := 8
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			ly := y % plankH
			n := noise(x, y)
			// seam
			if ly == 0 {
				v := uint8(60)
				img.SetNRGBA(x, y, color.NRGBA{v + 20, v, v - 20, 255})
				continue
			}
			// grain: vary based on x and noise
			grainV := math.Sin(float64(x)*0.8+float64(y)*0.1+n*2) * 0.5
			r := uint8(math.Min(255, math.Max(0, 160+grainV*40+n*20)))
			g := uint8(math.Min(255, math.Max(0, 100+grainV*25+n*15)))
			b := uint8(math.Min(255, math.Max(0, 40+grainV*10+n*10)))
			img.SetNRGBA(x, y, color.NRGBA{r, g, b, 255})
		}
	}
	save("wood_floor", img)
}

// concrete: uniform grey with subtle variation and aggregate speckles
func genConcrete(rng *rand.Rand) {
	img := newRGBA()
	noise := fractalNoise(rng, 3)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			n := noise(x, y)
			v := uint8(140 + n*40)
			// aggregate speckles
			if rng.Float64() < 0.03 {
				speck := uint8(rng.Intn(60) + 160)
				img.SetNRGBA(x, y, color.NRGBA{speck, speck, speck, 255})
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{v, v, v, 255})
		}
	}
	save("concrete", img)
}

// stucco: off-white rough textured surface
func genStucco(rng *rand.Rand) {
	img := newRGBA()
	noise := fractalNoise(rng, 4)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			n := noise(x, y)
			r := uint8(210 + n*20)
			g := uint8(200 + n*20)
			b := uint8(185 + n*20)
			img.SetNRGBA(x, y, color.NRGBA{r, g, b, 255})
		}
	}
	save("stucco", img)
}

// tileableNoise is smoothNoise with wrapping grid lookups, so the result is
// seamless when the texture repeats. gridSize must divide size evenly.
func tileableNoise(rng *rand.Rand, gridSize int) func(x, y int) float64 {
	stride := size / gridSize
	grid := make([]float64, stride*stride)
	for i := range grid {
		grid[i] = rng.Float64()
	}
	get := func(gx, gy int) float64 {
		return grid[(gy%stride)*stride+(gx%stride)]
	}
	return func(x, y int) float64 {
		fx := float64(x) / float64(gridSize)
		fy := float64(y) / float64(gridSize)
		gx := int(fx)
		gy := int(fy)
		tx := fx - float64(gx)
		ty := fy - float64(gy)
		tx = tx * tx * (3 - 2*tx)
		ty = ty * ty * (3 - 2*ty)
		v00 := get(gx, gy)
		v10 := get(gx+1, gy)
		v01 := get(gx, gy+1)
		v11 := get(gx+1, gy+1)
		return v00*(1-tx)*(1-ty) + v10*tx*(1-ty) + v01*(1-tx)*ty + v11*tx*ty
	}
}

// tileableFractalNoise sums octaves of tileableNoise. Grid sizes are halved per
// octave starting at size/2, so every octave divides size and stays seamless.
func tileableFractalNoise(rng *rand.Rand, octaves int) func(x, y int) float64 {
	type layer struct {
		n   func(x, y int) float64
		amp float64
	}
	layers := make([]layer, 0, octaves)
	total := 0.0
	amp := 1.0
	grid := size / 2
	for i := 0; i < octaves && grid >= 1; i++ {
		layers = append(layers, layer{tileableNoise(rng, grid), amp})
		total += amp
		amp *= 0.5
		grid /= 2
	}
	return func(x, y int) float64 {
		v := 0.0
		for _, l := range layers {
			v += l.n(x, y) * l.amp
		}
		return v / total
	}
}

// sand: warm tan base with wind ripples and fine grain speckle
func genSand(rng *rand.Rand) {
	img := newRGBA()
	noise := tileableFractalNoise(rng, 5)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			n := noise(x, y)
			// wind ripples: low-amplitude wave running at a slight angle.
			// Integer cycle counts keep the pattern seamless across the tile.
			fx := float64(x) / size
			fy := float64(y) / size
			// Warping the wave phase by the noise breaks up the regular banding
			// that a pure sine shows once the texture repeats.
			warp := (n - 0.5) * 0.6
			ripple := math.Sin(2*math.Pi*(3*fy+fx+warp)) * 0.5
			ripple += math.Sin(2*math.Pi*(5*fy+2*fx+warp)) * 0.2
			r := 200 + n*26 + ripple*7
			g := 175 + n*24 + ripple*6
			b := 130 + n*20 + ripple*5
			// individual grains: bright quartz flecks and darker mineral bits
			if s := rng.Float64(); s < 0.07 {
				r += 22
				g += 20
				b += 16
			} else if s < 0.12 {
				r -= 26
				g -= 24
				b -= 18
			}
			img.SetNRGBA(x, y, color.NRGBA{clamp8(r), clamp8(g), clamp8(b), 255})
		}
	}
	save("sand", img)
}

// clamp8 converts a float channel value to uint8, clamping to [0,255].
func clamp8(v float64) uint8 {
	return uint8(math.Min(255, math.Max(0, v)))
}

func main() {
	rng := rand.New(rand.NewSource(42))
	genGrass(rng)
	genDirt(rng)
	genRock(rng)
	genBrick(rng)
	genStoneFloor(rng)
	genWoodFloor(rng)
	genConcrete(rng)
	genStucco(rng)
	genSand(rng)
	// Appended last on purpose: every generator draws from this one seeded
	// rng, so inserting a call earlier in the sequence would shift the draws
	// of all the textures after it and silently regenerate them differently.
	genMoss(rng)
}
