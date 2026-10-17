package nanitecook

// quanthash.go — the quantisation bits in a cook's source hash.
//
// Options.PositionBits/UVBits/NormalBits change every byte of a cook, but the
// hash left them out: a mesh recooked with different bits looked up to date
// and was skipped. Nothing set them before the procedural terrain, which
// does — it picks PositionBits per tile so every tile of a field quantises on
// ONE grid (procgen/meshout's CookOptions.PositionStep), and two tiles whose
// seam vertices snapped to different grids would open sub-millimetre cracks.
// They are hashed only when set, so every cook made before keeps its hash.

import (
	"encoding/binary"
	"hash"
)

// hashQuantBits folds non-default quantisation bits into h.
func hashQuantBits(h hash.Hash, opt Options) {
	if opt.PositionBits == 0 && opt.UVBits == 0 && opt.NormalBits == 0 {
		return
	}
	var b [12]byte
	binary.LittleEndian.PutUint32(b[0:], uint32(int32(opt.PositionBits)))
	binary.LittleEndian.PutUint32(b[4:], uint32(int32(opt.UVBits)))
	binary.LittleEndian.PutUint32(b[8:], uint32(int32(opt.NormalBits)))
	h.Write([]byte("quant\x00"))
	h.Write(b[:])
}
