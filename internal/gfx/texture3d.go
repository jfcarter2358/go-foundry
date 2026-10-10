package gfx

// texture3d.go — uploading a volume texture.
//
// The engine's first GL_TEXTURE_3D. The volumetric passes (render/volumetric.go)
// sample tileable noise volumes built by internal/noise3d, and a volume is the
// only form that noise can take: a 2D texture cannot be marched through.
//
// The noise is generated on the CPU and uploaded once: this predates the GL 4.3
// requirement, when the engine had no compute to evaluate it on the GPU with.

import (
	"github.com/go-gl/gl/v4.3-core/gl"
)

// UploadTexture3D uploads a size³ volume of one or two unsigned-byte channels
// (R8 or RG8) and returns its handle.
//
// REPEAT on all three axes, because every volume the engine uploads tiles and
// is sampled at coordinates far outside [0, 1]. Mipmapped and trilinearly
// filtered: a cloud seen from a kilometre away samples its noise at a far
// coarser rate than it was generated at, and without mips that aliases into a
// shimmer the march's jitter only makes worse.
func UploadTexture3D(size int32, channels int, data []uint8) uint32 {
	internal, format := int32(gl.R8), uint32(gl.RED)
	if channels == 2 {
		internal, format = gl.RG8, gl.RG
	}

	var tex uint32
	gl.GenTextures(1, &tex)
	gl.BindTexture(gl.TEXTURE_3D, tex)
	// Rows are not padded to 4 bytes at these widths, but a one-channel
	// volume of a width that is not a multiple of 4 would be, so the alignment
	// is pinned rather than assumed.
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 1)
	gl.TexImage3D(gl.TEXTURE_3D, 0, internal, size, size, size, 0, format, gl.UNSIGNED_BYTE, gl.Ptr(data))
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 4)
	gl.TexParameteri(gl.TEXTURE_3D, gl.TEXTURE_WRAP_S, gl.REPEAT)
	gl.TexParameteri(gl.TEXTURE_3D, gl.TEXTURE_WRAP_T, gl.REPEAT)
	gl.TexParameteri(gl.TEXTURE_3D, gl.TEXTURE_WRAP_R, gl.REPEAT)
	gl.TexParameteri(gl.TEXTURE_3D, gl.TEXTURE_MIN_FILTER, gl.LINEAR_MIPMAP_LINEAR)
	gl.TexParameteri(gl.TEXTURE_3D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.GenerateMipmap(gl.TEXTURE_3D)
	gl.BindTexture(gl.TEXTURE_3D, 0)
	return tex
}
