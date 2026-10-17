package procgen

// hash.go — what makes a run deterministic and incremental.
//
// Two numbers per stage:
//
//   - its SEED, Hash64(doc seed, stage ID, the stage's own seed params):
//     what its RNG starts from. Folding the ID in is what lets two stages of
//     one kind with the same parameters still differ, and lets a stage be
//     reordered without reseeding everything after it.
//   - its HASH, sha256 over the kind's name and Version, the stage's seed,
//     its parameters fully resolved (defaults spelled out, so changing a
//     default in code reruns every stage that relied on it), and the hashes
//     of the upstream stages its stage refs name, in ref order. Equal hash,
//     equal output: the run engine skips a stage whose hash matches the
//     manifest and whose files are all still there.
//
// The parameters are hashed through their canonical YAML encoding (schema
// order, every field present), which is deterministic — no map iteration
// order reaches it.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"

	"foundry/internal/procgen/noise"

	"gopkg.in/yaml.v2"
)

// StageSeed is the stage's RNG seed: the document's seed, the stage's ID and
// every seed-typed parameter the kind declares at the top level.
func StageSeed(docSeed int64, stageID string, defs []ParamDef, p Params) uint64 {
	words := []uint64{uint64(docSeed), noise.HashString(stageID)}
	for i := range defs {
		if defs[i].Type == ParamSeed {
			words = append(words, uint64(int64(p[defs[i].Name].Num)))
		}
	}
	return noise.Hash64(words...)
}

// StageHash is the stage's output hash (see the file comment), as hex.
// params must be resolved; upstream are the hashes of the stages its refs
// name, in StageRefs order.
func StageHash(k *StageKind, seed uint64, params Params, upstream []string) string {
	return stageHashOf(k, hashedParams(k.Params, false), seed, params, upstream)
}

// StageBaseHash is StageHash without the kind's RectCovered parameters
// (ParamDef.RectCovered): what a stage's base hash is made of. Equal to
// StageHash for a kind that has none.
func StageBaseHash(k *StageKind, seed uint64, params Params, upstream []string) string {
	defs := hashedParams(k.Params, false)
	var kept []ParamDef
	cut := false
	for i := range defs {
		if defs[i].RectCovered {
			cut = true
			continue
		}
		kept = append(kept, defs[i])
	}
	if !cut {
		return stageHashOf(k, defs, seed, params, upstream)
	}
	return stageHashOf(k, kept, seed, params, upstream)
}

// CoveredHash is a hash of the stage's RectCovered parameters as the full
// hash encodes them, or "" when the kind has none or all are Added and at
// their defaults (a ground_edit with no rivers).
func CoveredHash(k *StageKind, params Params) string {
	var defs []ParamDef
	for i := range k.Params {
		if k.Params[i].RectCovered && !k.Params[i].PreviewOnly {
			defs = append(defs, k.Params[i])
		}
	}
	if len(defs) == 0 {
		return ""
	}
	ms := encodeParams(defs, params, false)
	if len(ms) == 0 {
		return ""
	}
	data, _ := yaml.Marshal(ms)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:12])
}

// stageHashOf is StageHash over the parameters defs.
func stageHashOf(k *StageKind, defs []ParamDef, seed uint64, params Params, upstream []string) string {
	h := sha256.New()
	var b [8]byte
	h.Write([]byte("fgen-stage\x00"))
	h.Write([]byte(k.Name))
	binary.LittleEndian.PutUint64(b[:], uint64(k.Version))
	h.Write(b[:])
	binary.LittleEndian.PutUint64(b[:], seed)
	h.Write(b[:])
	data, _ := yaml.Marshal(encodeParams(defs, params, false))
	binary.LittleEndian.PutUint64(b[:], uint64(len(data)))
	h.Write(b[:])
	h.Write(data)
	for _, u := range upstream {
		h.Write([]byte{0})
		h.Write([]byte(u))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// hashedParams is defs without (preview false) or with only (preview true)
// the PreviewOnly ones. Without any, defs itself, so every kind's hash is
// what it was before PreviewOnly existed.
func hashedParams(defs []ParamDef, preview bool) []ParamDef {
	has := false
	for i := range defs {
		has = has || defs[i].PreviewOnly
	}
	if !has {
		if preview {
			return nil
		}
		return defs
	}
	var out []ParamDef
	for i := range defs {
		if defs[i].PreviewOnly == preview {
			out = append(out, defs[i])
		}
	}
	return out
}

// PreviewHash is StageHash with the kind's PreviewOnly parameters folded in:
// what a preview is cached by, since they change what it shows. Equal to
// StageHash for a kind that has none.
func PreviewHash(k *StageKind, seed uint64, params Params, upstream []string) string {
	base := StageHash(k, seed, params, upstream)
	only := hashedParams(k.Params, true)
	if len(only) == 0 {
		return base
	}
	h := sha256.New()
	h.Write([]byte(base))
	data, _ := yaml.Marshal(encodeParams(only, params, false))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// StageRefs is every upstream stage ID p names through stage-typed params,
// top level and inside list items, in schema order, without repeats or
// blanks.
func StageRefs(defs []ParamDef, p Params) []string {
	var out []string
	seen := map[string]bool{}
	var walk func(defs []ParamDef, p Params)
	walk = func(defs []ParamDef, p Params) {
		for i := range defs {
			d := &defs[i]
			v, ok := p[d.Name]
			if !ok {
				v = d.Default
			}
			switch d.Type {
			case ParamStage:
				if v.Str != "" && !seen[v.Str] {
					seen[v.Str] = true
					out = append(out, v.Str)
				}
			case ParamList:
				for _, it := range v.Items {
					walk(d.Fields, it)
				}
			}
		}
	}
	walk(defs, p)
	return out
}
