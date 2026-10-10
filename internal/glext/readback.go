package glext

// readback.go — reading pixels, textures and buffers back to the CPU, with a
// glFinish in front of each.
//
// ---------- why ----------
//
// Mesa's D3D12 driver (the GL that WSLg gives a Windows GPU: seen on Mesa
// 26.2.3, "D3D12 (NVIDIA GeForce GTX 1070)") deadlocks the SECOND synchronous
// readback in a context when nothing finished the GPU's work in between:
//
//	clear; glReadPixels   -> returns
//	clear; glReadPixels   -> blocks in the driver forever
//
// No draw is needed. The same calls with a glFinish before each readback return
// at once, and llvmpipe never needed it. Every GL test that compares two frames
// (indexed vs de-indexed, instanced vs one-by-one, ...) does exactly this, and
// sat in glReadPixels until `go test` killed it ten minutes later — which read
// like a draw that never ended, and was not one.
//
// A synchronous readback has to wait for everything before it anyway, so the
// glFinish costs nothing it was not already paying. Every readback in the engine
// and its tests goes through here; a bare gl.ReadPixels is the bug coming back.

import (
	"unsafe"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// ReadPixels is gl.ReadPixels from the bound read framebuffer, finished first.
func ReadPixels(x, y, w, h int32, format, xtype uint32, pixels unsafe.Pointer) {
	gl.Finish()
	gl.ReadPixels(x, y, w, h, format, xtype, pixels)
}

// GetTexImage is gl.GetTexImage of the texture bound to target, finished
// first.
func GetTexImage(target uint32, level int32, format, xtype uint32, pixels unsafe.Pointer) {
	gl.Finish()
	gl.GetTexImage(target, level, format, xtype, pixels)
}

// GetBufferSubData is gl.GetBufferSubData of the buffer bound to target,
// finished first.
func GetBufferSubData(target uint32, offset, size int, data unsafe.Pointer) {
	gl.Finish()
	gl.GetBufferSubData(target, offset, size, data)
}
