package render

// nfoliage_cache.go — the Nanite foliage assets (nfoliage_asset.go) kept on
// disk between launches.
//
// Building every variant's cluster DAG and voxels is CPU work proportional
// to the plants' detail: under a second for the original biomes' plants, but
// the temperate rainforest's giants (foliage_rainforest_trees.go) take it to
// a quarter of a minute of all cores. The result depends only on the plants'
// meshes and the atlas, so it is built once and read back after.
//
// # Never stale
//
// An entry is named by a hash of exactly what the build reads — every
// template FoliageInit built, at every level of detail, and the atlas's full
// size level — together with nfCacheFormat. A plant changed by one vertex is
// a different name. nfCacheFormat must change whenever the BUILD changes for
// the same plants (the DAG's options, the voxel sampling, the packing, this
// file's layout), since that is the one thing the hash cannot see. An entry
// that fails to decode is ignored and the assets built.
//
// # Where
//
// The user's cache directory (os.UserCacheDir/foundry/foliage), like the
// model cache (internal/model/model_cache.go) and for its reasons: not in the
// author's resources/. FOUNDRY_FOLIAGE_CACHE=off switches it off and any
// other value is used as the directory; tests write nowhere.

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
)

// nfCacheFormat is part of every entry's name: bump it when the assets built
// from the same plants change (see the file comment).
const nfCacheFormat = 1

// nfCacheDir is where entries are written and read, or "" for nowhere.
var nfCacheDir = func() string {
	switch v := os.Getenv("FOUNDRY_FOLIAGE_CACHE"); v {
	case "off", "0", "false":
		return ""
	case "":
	default:
		return v
	}
	if testing.Testing() {
		return ""
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "foundry", "foliage")
}()

// nfCacheName is the entry for assets built from the template buffers
// (verts, idx) and the atlas.
func nfCacheName(verts []float32, idx []uint32, atlas nfAtlas) string {
	h := sha256.New()
	var b [4]byte
	for _, f := range verts {
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(f))
		h.Write(b[:])
	}
	for _, i := range idx {
		binary.LittleEndian.PutUint32(b[:], i)
		h.Write(b[:])
	}
	if len(atlas.chain) > 0 {
		h.Write(atlas.chain[0].Pix)
	}
	sum := h.Sum(nil)
	return fmt.Sprintf("nfoliage-%s-%d.bin", hex.EncodeToString(sum[:16]), nfCacheFormat)
}

// nfAssetsCached is buildNaniteFoliageAssets(atlas) by way of the cache entry
// name.
func nfAssetsCached(name string, atlas nfAtlas) nfAssetData {
	if nfCacheDir != "" && name != "" {
		if b, err := os.ReadFile(filepath.Join(nfCacheDir, name)); err == nil {
			if d, err := decodeNfAssets(b); err == nil {
				return d
			}
		}
	}
	start := time.Now()
	d := buildNaniteFoliageAssets(atlas)
	log.Printf("foliage: built the Nanite foliage assets in %v", time.Since(start).Round(time.Millisecond))
	if nfCacheDir != "" && name != "" {
		// Best effort: a cache that cannot be written is a slower next
		// launch, not a failure.
		if err := writeNfCacheEntry(name, d.encode()); err != nil {
			log.Printf("foliage: cache: %v", err)
		}
	}
	return d
}

func writeNfCacheEntry(name string, b []byte) error {
	if err := os.MkdirAll(nfCacheDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(nfCacheDir, name+".tmp*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(nfCacheDir, name)); err != nil {
		return err
	}
	pruneNfCache(name)
	return nil
}

// nfCacheKeep is how many entries the cache keeps. The assets depend on the
// engine's plants, not on the project, so one is current; a second covers
// switching between two builds of the engine. An entry is over a hundred
// megabytes, and every change to a plant would otherwise leave one behind.
const nfCacheKeep = 2

// pruneNfCache removes all but the newest nfCacheKeep entries, never keep.
func pruneNfCache(keep string) {
	ents, err := os.ReadDir(nfCacheDir)
	if err != nil {
		return
	}
	type entry struct {
		name string
		mod  time.Time
	}
	var es []entry
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), "nfoliage-") || !strings.HasSuffix(e.Name(), ".bin") || e.Name() == keep {
			continue
		}
		if info, err := e.Info(); err == nil {
			es = append(es, entry{e.Name(), info.ModTime()})
		}
	}
	sort.Slice(es, func(a, b int) bool { return es[a].mod.After(es[b].mod) })
	for i := nfCacheKeep - 1; i < len(es); i++ {
		os.Remove(filepath.Join(nfCacheDir, es[i].name))
	}
}

// ---------- the encoding: every array as words ----------

type nfWords []uint32

func (w *nfWords) i(v int)         { *w = append(*w, uint32(int32(v))) }
func (w *nfWords) f(v float32)     { *w = append(*w, math.Float32bits(v)) }
func (w *nfWords) v4(v mgl32.Vec4) { w.f(v[0]); w.f(v[1]); w.f(v[2]); w.f(v[3]) }

type nfReader struct {
	w   []uint32
	at  int
	bad bool
}

func (r *nfReader) u() uint32 {
	if r.at >= len(r.w) {
		r.bad = true
		return 0
	}
	r.at++
	return r.w[r.at-1]
}
func (r *nfReader) i() int     { return int(int32(r.u())) }
func (r *nfReader) f() float32 { return math.Float32frombits(r.u()) }
func (r *nfReader) v4() mgl32.Vec4 {
	return mgl32.Vec4{r.f(), r.f(), r.f(), r.f()}
}

// count reads a length, refusing one longer than what is left at per words
// apiece (a damaged entry, not a huge allocation).
func (r *nfReader) count(per int) int {
	n := r.i()
	if n < 0 || n*max(per, 1) > len(r.w)-r.at {
		r.bad = true
		return 0
	}
	return n
}

// encode writes d as words: a magic, then each array as its length and its
// elements.
func (d *nfAssetData) encode() []byte {
	w := nfWords{0x464f4c4e, nfCacheFormat}
	w.i(len(d.verts))
	for _, f := range d.verts {
		w.f(f)
	}
	w.i(len(d.idx))
	w = append(w, d.idx...)
	w.i(len(d.templates))
	for _, t := range d.templates {
		w.i(t.firstIndex)
		w.i(t.indexCount)
		w.i(t.baseVertex)
	}
	w.i(len(d.buckets))
	for _, b := range d.buckets {
		w.i(b.firstCluster)
		w.i(b.clusters)
		w.i(b.firstBone)
		w.i(b.bones)
		w.f(b.radius)
		w.i(b.levels)
		w.i(b.triClusters)
		w.i(b.voxClusters)
		w.i(b.partClusters)
	}
	w.i(len(d.clusters))
	for _, c := range d.clusters {
		for _, x := range []int{c.kind, c.bucket, c.group, c.level, c.firstIndex, c.indexCount, c.baseVertex,
			c.firstPart, c.parts, c.firstBrick, c.bricks, c.mult} {
			w.i(x)
		}
		w.v4(c.sphere)
		w.v4(c.lod)
		w.v4(c.parent)
		w.f(c.err)
		w.f(c.perr)
		w.f(c.voxelSize)
	}
	w.i(len(d.parts))
	for _, p := range d.parts {
		w.v4(p.right)
		w.v4(p.up)
		w.v4(p.n)
		w.v4(p.base)
	}
	w.i(len(d.bricks))
	for _, b := range d.bricks {
		for _, c := range b.Corner {
			w.f(c)
		}
		w = append(w, b.Mask[0], b.Mask[1], b.First, b.Bind)
	}
	w.i(len(d.voxels))
	for _, v := range d.voxels {
		w = append(w, v[:]...)
	}
	w.i(len(d.bones))
	for _, b := range d.bones {
		w.v4(b.pivot)
		w.v4(b.tip)
		w.v4(b.params)
	}
	w.i(d.maxBones)
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, []uint32(w))
	return buf.Bytes()
}

// decodeNfAssets reads what encode wrote.
func decodeNfAssets(b []byte) (nfAssetData, error) {
	var d nfAssetData
	if len(b)%4 != 0 {
		return d, errors.New("foliage cache: torn entry")
	}
	words := make([]uint32, len(b)/4)
	if err := binary.Read(bytes.NewReader(b), binary.LittleEndian, words); err != nil {
		return d, err
	}
	r := &nfReader{w: words}
	if r.u() != 0x464f4c4e || r.u() != nfCacheFormat {
		return d, errors.New("foliage cache: not an entry of this format")
	}
	d.verts = make([]float32, r.count(1))
	for i := range d.verts {
		d.verts[i] = r.f()
	}
	d.idx = make([]uint32, r.count(1))
	for i := range d.idx {
		d.idx[i] = r.u()
	}
	d.templates = make([]nfTemplate, r.count(3))
	for i := range d.templates {
		d.templates[i] = nfTemplate{firstIndex: r.i(), indexCount: r.i(), baseVertex: r.i()}
	}
	d.buckets = make([]nfBucket, r.count(9))
	for i := range d.buckets {
		d.buckets[i] = nfBucket{firstCluster: r.i(), clusters: r.i(), firstBone: r.i(), bones: r.i(), radius: r.f(),
			levels: r.i(), triClusters: r.i(), voxClusters: r.i(), partClusters: r.i()}
	}
	d.clusters = make([]nfCluster, r.count(27))
	for i := range d.clusters {
		c := &d.clusters[i]
		c.kind, c.bucket, c.group, c.level = r.i(), r.i(), r.i(), r.i()
		c.firstIndex, c.indexCount, c.baseVertex = r.i(), r.i(), r.i()
		c.firstPart, c.parts, c.firstBrick, c.bricks, c.mult = r.i(), r.i(), r.i(), r.i(), r.i()
		c.sphere, c.lod, c.parent = r.v4(), r.v4(), r.v4()
		c.err, c.perr, c.voxelSize = r.f(), r.f(), r.f()
	}
	d.parts = make([]nfPart, r.count(16))
	for i := range d.parts {
		d.parts[i] = nfPart{right: r.v4(), up: r.v4(), n: r.v4(), base: r.v4()}
	}
	d.bricks = make([]nfBrick, r.count(8))
	for i := range d.bricks {
		b := &d.bricks[i]
		b.Corner = [4]float32{r.f(), r.f(), r.f(), r.f()}
		b.Mask = [2]uint32{r.u(), r.u()}
		b.First, b.Bind = r.u(), r.u()
	}
	d.voxels = make([]nfVoxel, r.count(4))
	for i := range d.voxels {
		d.voxels[i] = nfVoxel{r.u(), r.u(), r.u(), r.u()}
	}
	d.bones = make([]nfBone, r.count(12))
	for i := range d.bones {
		d.bones[i] = nfBone{pivot: r.v4(), tip: r.v4(), params: r.v4()}
	}
	d.maxBones = r.i()
	if r.bad || r.at != len(r.w) {
		return nfAssetData{}, errors.New("foliage cache: damaged entry")
	}
	return d, nil
}
