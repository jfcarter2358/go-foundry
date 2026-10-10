package bsp

// pvs.go — the visibility bit array.
//
// One row per cell, one bit per cell: what could possibly be seen from anywhere
// within it. The runtime looks up the row for the cell the camera is in and
// skips everything outside it, before any frustum test and before any draw
// call. That is what a sealed level buys — a room you are not in costs nothing,
// not even a rejected bounding box.
//
// The set is POTENTIALLY visible. Marking a cell visible that turns out not to
// be costs a few wasted draws; failing to mark one that is costs a hole in the
// world. Everything that computes these (internal/compile/pvs.go) errs toward
// visible, and a reader should assume the same.

// PVS is the visibility bit array.
//
// Rows are addressed by LEAF index, including solid and void leaves, whose rows
// are simply empty. Compacting to interior leaves only would save a little
// space and cost every lookup a translation table, and the runtime lookup is
// the thing that has to be cheap.
type PVS struct {
	Leaves   int
	RowBytes int
	Bits     []byte
}

// NewPVS is an all-invisible set.
func NewPVS(leaves int) *PVS {
	rb := (leaves + 7) / 8
	return &PVS{Leaves: leaves, RowBytes: rb, Bits: make([]byte, leaves*rb)}
}

// Set marks to as visible from.
func (v *PVS) Set(from, to int) {
	if from < 0 || to < 0 || from >= v.Leaves || to >= v.Leaves {
		return
	}
	v.Bits[from*v.RowBytes+to/8] |= 1 << uint(to%8)
}

// Visible reports whether anything in leaf to could be seen from leaf from.
func (v *PVS) Visible(from, to int) bool {
	if v == nil || from < 0 || to < 0 || from >= v.Leaves || to >= v.Leaves {
		return false
	}
	return v.Bits[from*v.RowBytes+to/8]&(1<<uint(to%8)) != 0
}

// Row is the raw bits for one leaf, for a runtime that walks them directly
// rather than calling Visible once per leaf.
func (v *PVS) Row(leaf int) []byte {
	if leaf < 0 || leaf >= v.Leaves {
		return nil
	}
	return v.Bits[leaf*v.RowBytes : (leaf+1)*v.RowBytes]
}

// VisibleCount is how many leaves are visible from one — the number worth
// watching when tuning a level: a PVS where every row is nearly the whole level
// means the level is not sealed.
func (v *PVS) VisibleCount(leaf int) int {
	n := 0
	for i := 0; i < v.Leaves; i++ {
		if v.Visible(leaf, i) {
			n++
		}
	}
	return n
}
