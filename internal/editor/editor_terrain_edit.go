package editor

// editor_terrain_edit.go — what one application of the terrain tool does to
// a terrain's blocks and paint. Pure: no editor state, no undo, no GL — the
// tool (editor_terrain.go) decides where and when, this decides what.

import (
	"math"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// terrainEditMode is what the terrain tool does to what it is pointed at.
type terrainEditMode int

const (
	// terrainMine removes the block pointed at (a sphere of them, with a
	// radius).
	terrainMine terrainEditMode = iota
	// terrainPlace adds a block against the face pointed at.
	terrainPlace
	// terrainRetexture changes the material of the blocks pointed at.
	terrainRetexture
	// terrainPaint paints a biome on the faces around the point.
	terrainPaint
	// terrainErase clears the biome from the faces around the point.
	terrainErase
	// terrainVolume places a box of blocks, sized by dragging its faces,
	// then fills or carves it (editor_terrain_volume.go).
	terrainVolume
)

var terrainEditModes = []terrainEditMode{terrainMine, terrainPlace, terrainRetexture, terrainPaint, terrainErase, terrainVolume}

func (m terrainEditMode) String() string {
	switch m {
	case terrainMine:
		return "Mine"
	case terrainPlace:
		return "Place"
	case terrainRetexture:
		return "Material"
	case terrainPaint:
		return "Paint Biome"
	case terrainErase:
		return "Erase Biome"
	case terrainVolume:
		return "Volume"
	}
	return "?"
}

// inverse is the mode Shift turns m into for one stroke.
func (m terrainEditMode) inverse() terrainEditMode {
	switch m {
	case terrainMine:
		return terrainPlace
	case terrainPlace:
		return terrainMine
	case terrainPaint:
		return terrainErase
	case terrainErase:
		return terrainPaint
	}
	return m
}

// repeats reports whether holding the button repeats the mode on a timer
// (a block at a time) rather than every frame (a brush).
func (m terrainEditMode) repeats() bool {
	return m == terrainMine || m == terrainPlace || m == terrainRetexture
}

// terrainEdit is one application of the tool.
type terrainEdit struct {
	mode terrainEditMode
	// face is the face the cursor is on, and point where on it, in the
	// terrain's chunk space.
	face  int32
	point mgl32.Vec3
	// radius is the reach in blocks: 0 is the one block or face.
	radius int
	// material is what Place and Material lay down (1..4); biome is the slot
	// Paint lays down (counting from 1).
	material uint8
	biome    uint8
}

// terrainEditTarget is the block an edit centres on: the block owning the
// face for Mine and Material, the air block in front of it for Place.
func terrainEditTarget(t *world.Terrain, e terrainEdit) (x, y, z int) {
	x, y, z, d := t.FaceBlock(e.face)
	if e.mode == terrainPlace {
		s := world.TerrainDirs[d]
		x, y, z = x+s[0], y+s[1], z+s[2]
	}
	return x, y, z
}

// applyTerrainEdit applies e to t and reports whether anything changed.
func applyTerrainEdit(t *world.Terrain, e terrainEdit) bool {
	changed := false
	r := e.radius
	switch e.mode {
	case terrainMine, terrainPlace, terrainRetexture:
		cx, cy, cz := terrainEditTarget(t, e)
		reach := float64(r) + 0.5
		for z := cz - r; z <= cz+r; z++ {
			for y := cy - r; y <= cy+r; y++ {
				for x := cx - r; x <= cx+r; x++ {
					dx, dy, dz := float64(x-cx), float64(y-cy), float64(z-cz)
					if r > 0 && math.Sqrt(dx*dx+dy*dy+dz*dz) > reach {
						continue
					}
					switch e.mode {
					case terrainMine:
						changed = t.SetBlock(x, y, z, 0) || changed
					case terrainPlace:
						if !t.Solid(x, y, z) {
							changed = t.SetBlock(x, y, z, e.material) || changed
						}
					case terrainRetexture:
						if t.Solid(x, y, z) {
							changed = t.SetBlock(x, y, z, e.material) || changed
						}
					}
				}
			}
		}
	case terrainPaint, terrainErase:
		slot := e.biome
		if e.mode == terrainErase {
			slot = 0
		}
		if r == 0 {
			return t.SetPaint(e.face, slot)
		}
		// Every face whose centre is within reach of the point.
		reach := (float32(r) + 0.5) * t.BlockSize
		cx, cy, cz := t.BlockAt(e.point)
		for z := cz - r - 1; z <= cz+r+1; z++ {
			for y := cy - r - 1; y <= cy+r+1; y++ {
				for x := cx - r - 1; x <= cx+r+1; x++ {
					if !t.Solid(x, y, z) {
						continue
					}
					for d := 0; d < 6; d++ {
						k := t.FaceKey(x, y, z, d)
						if !t.FaceExists(k) {
							continue
						}
						s := world.TerrainDirs[d]
						c := t.BlockMin(x, y, z).Add(mgl32.Vec3{
							(0.5 + 0.5*float32(s[0])) * t.BlockSize,
							(0.5 + 0.5*float32(s[1])) * t.BlockSize,
							(0.5 + 0.5*float32(s[2])) * t.BlockSize,
						})
						if c.Sub(e.point).Len() > reach {
							continue
						}
						changed = t.SetPaint(k, slot) || changed
					}
				}
			}
		}
	}
	return changed
}
