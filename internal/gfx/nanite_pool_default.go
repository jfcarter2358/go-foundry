package gfx

// nanite_pool_default.go — the Nanite page pool's size for a player who has
// never chosen one, by the card's video memory.
//
// 256 MB was the one default for every card. The generated desert canyon's
// cooked meshes are 580 MB of pages, so on an 8 GB GTX 1070 the pool streamed
// some 15 pages in and out every frame of a still camera and starved hundreds
// a pose: coarse clusters shown, and a page upload's stall in the frame
// (docs/canyon/PERF.md). The pool is only ever as big as a level needs
// (render/nanite_stream.go), so a bigger budget costs a small level nothing.
//
// So, as PickDefaultGIQuality does for GI, the budget a player has not set
// (graphics.yaml's nanite_stream_pool_mb, or a script's level prop) is an
// eighth of the card's memory, 256..1024 MB: 1024 on the 1070. A driver that
// does not say how much memory it has (neither GL_NVX_gpu_memory_info nor
// GL_ATI_meminfo) keeps 256.

import (
	"log"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// NaniteStreamPoolChosen is whether NaniteStreamPoolMB was set by the player
// (the graphics settings, a script), so PickDefaultNaniteStreamPool leaves it.
var NaniteStreamPoolChosen bool

// GL_NVX_gpu_memory_info / GL_ATI_meminfo queries, in KB.
const (
	glGPUMemoryTotalNVX = 0x9048
	glTextureFreeATI    = 0x87FC
)

// defaultPoolMB is the budget for a card of vramMB megabytes (0: unknown).
func defaultPoolMB(vramMB int) int {
	if vramMB <= 0 {
		return NaniteStreamPoolMB
	}
	return min(max(vramMB/8, 256), 1024)
}

// PickDefaultNaniteStreamPool sizes NaniteStreamPoolMB by the card, unless the
// player chose it. Call with a context current, before the first level load.
func PickDefaultNaniteStreamPool() {
	if NaniteStreamPoolChosen {
		return
	}
	vram := 0
	if hasGLExtension("GL_NVX_gpu_memory_info") {
		var kb int32
		gl.GetIntegerv(glGPUMemoryTotalNVX, &kb)
		vram = int(kb / 1024)
	} else if hasGLExtension("GL_ATI_meminfo") {
		// Free memory, not total: the best ATI_meminfo says.
		var kb [4]int32
		gl.GetIntegerv(glTextureFreeATI, &kb[0])
		vram = int(kb[0] / 1024)
	}
	for gl.GetError() != gl.NO_ERROR {
	}
	if mb := defaultPoolMB(vram); mb != NaniteStreamPoolMB {
		log.Printf("nanite: page pool budget %d MB for a card of %d MB (nanite_stream_pool_mb unset)", mb, vram)
		NaniteStreamPoolMB = mb
	}
}
