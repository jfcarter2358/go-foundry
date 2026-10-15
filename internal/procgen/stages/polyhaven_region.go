package stages

// polyhaven_region.go — the Poly Haven stages expanded per environment
// region (docs/regions/PHASE_4.md, place_region.go).
//
// An import already lands in one place per asset and setting —
// resources/models/polyhaven/<id>/ (textures under textures/polyhaven/<id>/),
// its downloads in the client's md5-addressed cache, its cooks skipped when
// the .fnm already holds the mesh's hash — so two regions growing one
// preset never download or cook an asset twice. What the second region
// still paid was re-reading and re-measuring the scan (a two-million-
// triangle cliff takes seconds to load and decimate before the cook's
// hash check can skip it). importModelShared remembers, for the life of
// the process, each import a region's stage made, by asset and settings,
// and hands it to the next region's stage as long as every mesh file it
// made is still on disk as it was.
//
// Classic documents (no region) import exactly as before.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"foundry/internal/polyhaven"
	"foundry/internal/polyhaven/phimport"
)

// importMemo holds region imports by key (importKey).
var importMemo sync.Map

type importMemoEntry struct {
	mu    sync.Mutex
	imp   *phimport.ModelImport
	stamp map[string]time.Time // each variant's model file's modification time
}

// importKey is an import's identity: where, what and every setting that
// shapes its files (not its log or progress hooks).
func importKey(id string, o phimport.ModelOptions) string {
	k := fmt.Sprintf("%s|%s|%s|%d|%d|%d|%s|%v|%s|%g|%v|%v", o.Resources, id, o.Resolution, o.MaxSize, o.MaxTriangles,
		o.LiteTriangles, o.Back, o.Plant, o.Collision, o.HullSize, o.NoSplit, o.Cook)
	if o.ThinFoliage {
		k += "|thin"
	}
	return k
}

// importStamp is the modification time of every variant's file, or nil
// when one is missing.
func importStamp(res string, imp *phimport.ModelImport) map[string]time.Time {
	out := map[string]time.Time{}
	for _, v := range imp.Variants {
		st, err := os.Stat(filepath.Join(res, filepath.FromSlash(v.Model)))
		if err != nil {
			return nil
		}
		out[v.Model] = st.ModTime()
	}
	return out
}

// importModelShared is phimport.ImportModel for a region's stage, reusing an
// import this process already made with the same key while its files stand.
// reused says it was.
func importModelShared(ctx context.Context, c *polyhaven.Client, id string, opt phimport.ModelOptions) (imp *phimport.ModelImport, reused bool, err error) {
	v, _ := importMemo.LoadOrStore(importKey(id, opt), &importMemoEntry{})
	e := v.(*importMemoEntry)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.imp != nil {
		if now := importStamp(opt.Resources, e.imp); now != nil && sameStamp(now, e.stamp) {
			return e.imp, true, nil
		}
	}
	imp, err = phimport.ImportModel(ctx, c, id, opt)
	if err != nil {
		return nil, false, err
	}
	e.imp, e.stamp = imp, importStamp(opt.Resources, imp)
	return imp, false, nil
}

func sameStamp(a, b map[string]time.Time) bool {
	if len(a) != len(b) {
		return false
	}
	for k, t := range a {
		if u, ok := b[k]; !ok || !u.Equal(t) {
			return false
		}
	}
	return true
}
