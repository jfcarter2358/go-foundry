package nanitecook

// mesh.go — cooking a mesh that exists only in memory.
//
// Cook and CookFile take a FILE, which suits an artist's glTF. A procedural
// generator has an ImportMesh it built itself, and writing it out as a .glb
// only to read it straight back would cost a parse and the disk for nothing.
// CookMesh cooks the ImportMesh directly, through the same fromImport and
// cookSource the file path takes, so the two produce the same .fnm for the
// same mesh.
//
// What stands in for "the source file" in the hash is the mesh's GLB
// encoding (model.EncodeGLB, which is deterministic): the hash a CookFile of
// that .glb would carry, so CookMeshFile skips exactly when CookFile would.

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"

	"foundry/internal/fileutil"
	"foundry/internal/model"
	"foundry/internal/nanite"
)

// meshSourceName is the name hashed for an in-memory mesh, in place of a
// source file's basename.
const meshSourceName = "mesh.glb"

// HashMesh is the source hash a cook of m with opt carries.
func HashMesh(m *model.ImportMesh, opt Options) ([32]byte, error) {
	data, err := model.EncodeGLB(m)
	if err != nil {
		return [32]byte{}, err
	}
	h := sha256.New()
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], nanite.CookVersion)
	h.Write(b[:])
	cs := opt.Collision.Resolved()
	for _, f := range []float32{cs.Error, float32(cs.MaxHulls), float32(cs.MaxHullVerts), cs.Concavity, cs.ConcavityRel, cs.AutoSize, float32(cs.Mode)} {
		binary.LittleEndian.PutUint32(b[:4], math.Float32bits(f))
		h.Write(b[:4])
	}
	hashLocks(h, opt)
	hashQuantBits(h, opt)
	hashAttrRadiusCap(h, opt)
	h.Write([]byte(meshSourceName))
	binary.LittleEndian.PutUint64(b[:], uint64(len(data)))
	h.Write(b[:])
	h.Write(data)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

// CookMesh cooks an in-memory mesh. source is recorded as the cooked mesh's
// Source (what `foundry nanite inspect` prints); it need not exist.
func CookMesh(m *model.ImportMesh, source string, opt Options) (*nanite.Mesh, error) {
	if m == nil || len(m.Indices) == 0 {
		return nil, fmt.Errorf("no triangles to cook")
	}
	if len(m.TriSlot) != len(m.Indices)/3 {
		return nil, fmt.Errorf("%d triangles but %d slot entries", len(m.Indices)/3, len(m.TriSlot))
	}
	if len(m.Slots) == 0 {
		return nil, fmt.Errorf("no material slots")
	}
	src := fromImport(m)
	cooked, err := cookSource(src, opt)
	if err != nil {
		return nil, err
	}
	if cooked.SourceHash, err = HashMesh(m, opt); err != nil {
		return nil, err
	}
	cooked.Source = fileutil.RelAssetPath(source)
	return cooked, nil
}

// CookMeshFile cooks m into out, unless out already holds a cook of exactly
// this mesh with these options and force is off.
func CookMeshFile(m *model.ImportMesh, out string, force bool, opt Options) (CookResult, error) {
	res := CookResult{Out: out}
	if !force {
		want, err := HashMesh(m, opt)
		if err != nil {
			return res, err
		}
		version := uint32(nanite.FileVersionPlain)
		if len(m.Colors) > 0 {
			version = nanite.FileVersion
		}
		if v, have, err := nanite.ReadHeader(out); err == nil && v == version && have == want {
			res.Skipped = true
			return res, nil
		}
	}
	cooked, err := CookMesh(m, out, opt)
	if err != nil {
		return res, err
	}
	// No .bak: CookMeshFile is the generators' cook (procgen/meshout), whose
	// outputs are rebuilt on demand; a backup of each recooked tile doubled
	// a world's output folder on every rerun.
	if err := cooked.WriteNoBackup(out); err != nil {
		return res, err
	}
	res.Mesh = cooked
	return res, nil
}
