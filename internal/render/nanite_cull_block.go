package render

// nanite_cull_block.go — a warning for a cull batch whose command buffer is
// larger than the driver's largest shader storage block.
//
// The cull writes one 20-byte indirect command per SLOT (an instance's
// cluster or cluster range), into one storage buffer bound whole. A native
// NVIDIA driver allows a block of up to 2 GB; Mesa's D3D12 driver (WSLg)
// reports 128 MB. Past that the binding is undefined behaviour, and on WSLg
// it showed as a frame drawn entirely black — sky included — with no GL
// error: the biome showcase world's 3,668 scanned firs (300k triangles each)
// took the scenery batch to ~8 M slots (docs/regions/PRESETS_3.md). Nothing
// here changes what is drawn; it says why, once, so a black frame on a small
// driver is not a mystery. The cure is fewer slots (fewer or lighter
// instances in the batch).

import (
	"log"
	"sync"

	"github.com/go-gl/gl/v4.3-core/gl"
)

var cmdBlock struct {
	once   sync.Once
	max    int64
	warned bool
}

// cmdBlockTooBig reports whether a command buffer of size bytes passes the
// largest storage block max (0: unknown, never too big).
func cmdBlockTooBig(size, max int64) bool { return max > 0 && size > max }

// warnCmdBlock logs, once a run, a command buffer past the driver's largest
// storage block. Call with a GL context current.
func warnCmdBlock(size int) {
	cmdBlock.once.Do(func() { gl.GetInteger64v(gl.MAX_SHADER_STORAGE_BLOCK_SIZE, &cmdBlock.max) })
	if cmdBlock.warned || !cmdBlockTooBig(int64(size), cmdBlock.max) {
		return
	}
	cmdBlock.warned = true
	log.Printf("nanite: WARNING: a cull batch's command buffer is %d MB, past the driver's largest storage block (%d MB): "+
		"this driver may draw the frame wrong or black; place fewer or lighter Nanite instances", size>>20, cmdBlock.max>>20)
}
