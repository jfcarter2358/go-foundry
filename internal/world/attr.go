package world

// attr.go — the attribute model, re-exported from where it now lives.
//
// Both halves moved to internal/scriptdata when UI widgets started carrying
// attributes too: the values because a shared helper belongs below everything
// that shares it, and the YAML shape because a .fui writes the same block a
// level file does and two implementations of it would drift.
//
// These are aliases and variables rather than defined types and wrappers on
// purpose. world.AttrValue and scriptdata.AttrValue must be the *same* type, or
// every one of the several hundred call sites in this package would need a
// conversion at it.

import "foundry/internal/scriptdata"

type (
	AttrType     = scriptdata.AttrType
	AttrValue    = scriptdata.AttrValue
	AttrSet      = scriptdata.AttrSet
	RawAttribute = scriptdata.RawAttribute
)

const (
	AttrInt    = scriptdata.AttrInt
	AttrFloat  = scriptdata.AttrFloat
	AttrBool   = scriptdata.AttrBool
	AttrString = scriptdata.AttrString
)

// AttrTypeNames is the four types, in the order the editor's dropdown shows.
var AttrTypeNames = scriptdata.AttrTypeNames

var (
	IntAttr     = scriptdata.IntAttr
	FloatAttr   = scriptdata.FloatAttr
	BoolAttr    = scriptdata.BoolAttr
	StringAttr  = scriptdata.StringAttr
	ZeroAttr    = scriptdata.ZeroAttr
	ConvertAttr = scriptdata.ConvertAttr
)

func validAttrType(t AttrType) bool         { return scriptdata.ValidAttrType(t) }
func mergeAttrs(base, over AttrSet) AttrSet { return scriptdata.MergeAttrs(base, over) }

func attrsFromRaw(raw map[string]RawAttribute) (AttrSet, error) {
	return scriptdata.AttrsFromRaw(raw)
}
func attrsToRaw(s AttrSet) map[string]RawAttribute { return scriptdata.AttrsToRaw(s) }
func attrToRaw(a AttrValue) RawAttribute           { return scriptdata.AttrToRaw(a) }
