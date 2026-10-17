package render

// amb_tiles.go — the ambient volumes each screen tile can see
// (amb_tiles.glsl, amb_tiles_comp.glsl; docs/adventure/PHASE_F.md).
//
// After the opaque world has its depth (the visibility buffer's two raster
// passes) and before the material pass shades it, one compute dispatch marks
// for each 32-pixel tile the packed ambient volumes (world.AmbientPacked)
// whose boxes reach into the tile's frustum, eye to farthest depth. The
// material pass and the shafts' march read the mask and skip the rest; every
// other program leaves ambTileOn at 0 and loops over them all, as before.
//
// On the adventure level a camera in the slot canyon packs the cave's
// volumes too (they are ahead, in the frustum, behind 100 m of rock): eight
// volumes where the screen sees one. FOUNDRY_AMBIENT_TILES=0 switches it off.

import (
	"log"

	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// AmbientTiles switches the per-tile ambient volume masks.
var AmbientTiles = gfx.EnvFlag("FOUNDRY_AMBIENT_TILES", true)

const (
	// ambTilePx is a tile's side in pixels.
	ambTilePx = 32
	// ambTilePad grows each tile by this many pixels each way: TAA's jitter,
	// and the shafts' half-resolution distances, which may come from the
	// pixel next door.
	ambTilePad = 3
	// ambTileBinding is amb_tiles.glsl's storage block.
	ambTileBinding = 23
)

var ambTiles struct {
	tried, ok bool
	prog      progUniforms
	buf       uint32
	bufWords  int
	// on: this frame's masks are built; vp the matrix they were made with
	// (the shaders project a point by it to find its tile).
	on    bool
	vp    mgl32.Mat4
	count [2]int32
}

// ambTilesReset marks the frame's masks not built, at the top of a frame.
func ambTilesReset() { ambTiles.on = false }

// ambTilesBuild makes the frame's masks from the scene target's depth. A
// level with one ambient volume or none gains nothing and builds nothing.
func ambTilesBuild() {
	ambTiles.on = false
	if !AmbientTiles || scene.fbo == 0 || scene.depth == 0 || world.AmbientPacked.N < 2 {
		return
	}
	a := &ambTiles
	if !a.tried {
		a.tried = true
		p, err := buildComputeProgram(mustReadShader(biomeShaderPath("amb_tiles_comp.glsl")))
		if err != nil {
			log.Printf("ambient tiles: %v — every pixel loops over every volume", err)
			return
		}
		a.prog, a.ok = p, true
		gl.GenBuffers(1, &a.buf)
	}
	if !a.ok {
		return
	}
	w, h := scene.w, scene.h
	cx, cy := (w+ambTilePx-1)/ambTilePx, (h+ambTilePx-1)/ambTilePx
	words := int(cx * cy)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, a.buf)
	if words > a.bufWords {
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, words*4, nil, gl.DYNAMIC_COPY)
		a.bufWords = words
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)

	vp := gfx.Projection.Mul4(gfx.Cam.Matrix)
	inv := vp.Inv()
	p := &a.prog
	gl.UseProgram(p.prog)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, scene.depth)
	gl.Uniform1i(p.U("atDepth"), 0)
	gl.UniformMatrix4fv(p.U("atInvVP"), 1, false, &inv[0])
	gl.Uniform2i(p.U("atSize"), w, h)
	gl.Uniform1i(p.U("atTile"), ambTilePx)
	gl.Uniform1i(p.U("atPad"), ambTilePad)
	gl.Uniform2i(p.U("atCount"), cx, cy)
	n := world.AmbientPacked.N
	gl.Uniform1i(p.U("atVolumes"), int32(n))
	// A metre's pad on each box, as the frustum cull's.
	var lo, hi [8 * 3]float32
	for i := 0; i < n && i < 8; i++ {
		for k := 0; k < 3; k++ {
			lo[3*i+k] = world.AmbientPacked.Lo[i][k] - 1
			hi[3*i+k] = world.AmbientPacked.Hi[i][k] + 1
		}
	}
	gl.Uniform3fv(p.U("atLo"), 8, &lo[0])
	gl.Uniform3fv(p.U("atHi"), 8, &hi[0])
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, a.buf)
	gl.DispatchCompute(uint32(cx), uint32(cy), 1)
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 0, 0)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	gl.UseProgram(gfx.Program)
	a.on, a.vp, a.count = true, vp, [2]int32{cx, cy}
}

// ambTilesBindTo tells prog (bound by the caller or not) whether this
// frame's masks are there, and binds them. Every program that opts in calls
// it each frame, so one that read them last frame does not read stale ones.
func ambTilesBindTo(prog uint32) {
	loc := gfx.UniformLocName(prog, "ambTileOn")
	if loc < 0 {
		return
	}
	gl.UseProgram(prog)
	a := &ambTiles
	if !a.on {
		gl.Uniform1i(loc, 0)
		return
	}
	gl.Uniform1i(loc, 1)
	gl.UniformMatrix4fv(gfx.UniformLocName(prog, "ambTileVP"), 1, false, &a.vp[0])
	gl.Uniform2i(gfx.UniformLocName(prog, "ambTileCount"), a.count[0], a.count[1])
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, ambTileBinding, a.buf)
}
