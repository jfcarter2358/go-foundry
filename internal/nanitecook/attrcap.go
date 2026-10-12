package nanitecook

// attrcap.go — Options.AttrRadiusCap in the cook's hash.

import (
	"encoding/binary"
	"hash"
	"math"
)

// hashAttrRadiusCap folds a set AttrRadiusCap into h: it changes the DAG.
// Unset, nothing is written, so every cook made before it existed keeps its
// hash.
func hashAttrRadiusCap(h hash.Hash, opt Options) {
	if opt.AttrRadiusCap <= 0 {
		return
	}
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], math.Float32bits(opt.AttrRadiusCap))
	h.Write([]byte("attrcap\x00"))
	h.Write(b[:])
}
