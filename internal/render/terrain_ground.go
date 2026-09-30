package render

// terrain_ground.go — the textures biomes lay over the ground they are
// painted on.
//
// A biome names a ground texture (FoliagePreset.GroundTexture); a terrain
// gives each distinct one in its palette a channel (Terrain.GroundTextures),
// and every face corner carries a weight per channel. The terrain program has
// four layer samplers already and the texture units are spent up to 14, so
// the ground textures do not get a sampler each: every one in use by the
// level is packed into a single 2D array texture on the one free unit, all
// resampled to groundArraySize square, and a terrain is told which array
// layer each of its channels is.
//
// The array is rebuilt when the set of textures in use changes, when a biome
// file changes what it names (the preset generation), or when one of the
// files is rewritten on disk — checked once a second, since a stat per
// texture per frame is waste and a second is faster than anyone switches
// windows after saving a texture.

import (
	"fmt"
	"image"
	"log"
	"os"
	"slices"
	"time"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.1-core/gl"
)

// GroundTexUnit holds the biome ground array: the one unit the terrain
// program has left (see shadow.go's unit map, which runs to 14).
const GroundTexUnit = 15

// groundArraySize is the edge every ground texture is resampled to.
const groundArraySize = 512

type groundArray struct {
	tex uint32
	// names are the textures in the array, layer by layer; missing marks
	// the ones that could not be read, which draw as the block's own layers.
	names   []string
	missing map[string]bool
	stamps  map[string]string
	gen     uint64
	checked time.Time
}

var ground groundArray

// groundArrayInit makes the placeholder array: a sampler of a type other than
// its unit's binding is an invalid draw, so the unit always holds an array.
func groundArrayInit() {
	if ground.tex != 0 {
		return
	}
	gl.GenTextures(1, &ground.tex)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, ground.tex)
	px := []uint8{128, 128, 128, 255}
	gl.TexImage3D(gl.TEXTURE_2D_ARRAY, 0, gl.RGBA8, 1, 1, 1, 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(px))
	gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D_ARRAY, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, 0)
	ground.names = nil
}

// syncGroundArray rebuilds the array if what the level's terrains need has
// changed. Called once a frame from SyncTerrainMeshes.
func syncGroundArray(lvl *world.Level) {
	groundArrayInit()
	var want []string
	for ci := range lvl.Chunks {
		if t := lvl.Chunks[ci].Terrain; t != nil {
			for _, n := range t.GroundTextures() {
				if !slices.Contains(want, n) {
					want = append(want, n)
				}
			}
		}
	}
	slices.Sort(want)
	stale := !slices.Equal(want, ground.names) || ground.gen != world.FoliagePresetGeneration()
	if !stale && len(want) > 0 && time.Since(ground.checked) > time.Second {
		ground.checked = time.Now()
		for _, n := range want {
			if groundTextureStamp(n) != ground.stamps[n] {
				stale = true
				break
			}
		}
	}
	if !stale {
		return
	}
	buildGroundArray(want)
}

func groundTextureStamp(name string) string {
	path, ok := gfx.TextureFile(name)
	if !ok {
		return ""
	}
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s|%d|%d", path, st.ModTime().UnixNano(), st.Size())
}

func buildGroundArray(names []string) {
	ground.names = names
	ground.gen = world.FoliagePresetGeneration()
	ground.checked = time.Now()
	ground.stamps = map[string]string{}
	ground.missing = map[string]bool{}
	if len(names) == 0 {
		return
	}
	n := groundArraySize
	pix := make([]uint8, n*n*4*len(names))
	for i, name := range names {
		ground.stamps[name] = groundTextureStamp(name)
		img, err := gfx.DecodeTexture(name)
		if err != nil {
			log.Printf("terrain: biome ground texture %q: %v — that ground keeps its block's texture", name, err)
			ground.missing[name] = true
			continue
		}
		resampleWrapped(img, pix[i*n*n*4:(i+1)*n*n*4], n)
	}
	gl.DeleteTextures(1, &ground.tex)
	gl.GenTextures(1, &ground.tex)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, ground.tex)
	gl.TexImage3D(gl.TEXTURE_2D_ARRAY, 0, gl.RGBA8, int32(n), int32(n), int32(len(names)), 0,
		gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(pix))
	gfx.ApplyWorldTextureFilterTo(gl.TEXTURE_2D_ARRAY)
	gl.GenerateMipmap(gl.TEXTURE_2D_ARRAY)
	gl.BindTexture(gl.TEXTURE_2D_ARRAY, 0)
}

// resampleWrapped scales img to n x n into dst, bilinearly, wrapping at the
// edges — the textures tile, so the far edge is the near one's neighbour.
func resampleWrapped(img *image.RGBA, dst []uint8, n int) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if w == n && h == n {
		for y := 0; y < n; y++ {
			copy(dst[y*n*4:(y+1)*n*4], img.Pix[y*img.Stride:y*img.Stride+n*4])
		}
		return
	}
	at := func(x, y int) []uint8 {
		x, y = (x%w+w)%w, (y%h+h)%h
		o := y*img.Stride + x*4
		return img.Pix[o : o+4]
	}
	for y := 0; y < n; y++ {
		fy := (float32(y)+0.5)*float32(h)/float32(n) - 0.5
		y0 := int(fy)
		if fy < 0 {
			y0 = -1
		}
		ty := fy - float32(y0)
		for x := 0; x < n; x++ {
			fx := (float32(x)+0.5)*float32(w)/float32(n) - 0.5
			x0 := int(fx)
			if fx < 0 {
				x0 = -1
			}
			tx := fx - float32(x0)
			a, b, c, d := at(x0, y0), at(x0+1, y0), at(x0, y0+1), at(x0+1, y0+1)
			o := (y*n + x) * 4
			for k := 0; k < 4; k++ {
				top := float32(a[k])*(1-tx) + float32(b[k])*tx
				bot := float32(c[k])*(1-tx) + float32(d[k])*tx
				dst[o+k] = uint8(top*(1-ty) + bot*ty + 0.5)
			}
		}
	}
}

// bindGroundUniforms tells the terrain program which array layer each of t's
// ground channels is, how big it tiles and whether it climbs walls.
func bindGroundUniforms(p *progUniforms, t *world.Terrain) {
	var layer [world.TerrainGroundChannels]int32
	var scale, walls [world.TerrainGroundChannels]float32
	for i := range layer {
		layer[i] = -1
		scale[i] = world.DefaultGroundScale
	}
	names := t.GroundTextures()
	for c, name := range names {
		if ground.missing[name] {
			continue
		}
		if i := slices.Index(ground.names, name); i >= 0 {
			layer[c] = int32(i)
		}
	}
	// The channel's scale and walls come from the first biome in the
	// palette that names its texture.
	var set [world.TerrainGroundChannels]bool
	for i := range t.Biomes {
		b := &t.Biomes[i]
		if b.GroundLayer > 0 {
			continue
		}
		d := b.Def()
		if c := slices.Index(names, d.GroundTexture); c >= 0 && d.GroundTexture != "" && !set[c] {
			set[c] = true
			scale[c] = d.EffectiveGroundScale()
			if d.GroundWalls {
				walls[c] = 1
			}
		}
	}
	gl.Uniform1iv(p.U("groundLayer"), int32(len(layer)), &layer[0])
	gl.Uniform1fv(p.U("groundScale"), int32(len(scale)), &scale[0])
	gl.Uniform1fv(p.U("groundWalls"), int32(len(walls)), &walls[0])
}
