package render

// nanite_stream.go — streaming cooked Nanite meshes' pages through a fixed
// GPU page pool. Phase 3 of the Nanite plan (docs/nanite/PHASE_3.md),
// components 3 and 9.
//
// A cooked mesh's geometry is in PAGES (internal/nanite's pages.go): runs of
// its clusters, quantised and bit-packed in the file, each of which decodes to
// at most nanite.PageMaxVertices vertices and nanite.PageMaxTriangles
// triangles. The renderer holds them in a POOL of fixed slots that size, one
// vertex, normal and index buffer for every cooked mesh of the level; a slot
// holds one page, a cluster draws from wherever its page sits (its record's
// firstIndex and baseVertex), and what is not in the pool is not drawn. The
// pool is as big as the level needs, up to gfx.NaniteStreamPoolMB: under the
// budget everything is loaded with the level and nothing ever streams; over
// it, pages come and go while the level plays.
//
// # What decides which pages
//
// The cut does. internal/nanite's residency.go has the argument: a DAG group
// may be drawn finer than itself only when the pages of what it consumed are
// resident and every group above it may too, and a group that may not is
// given error 0 in its clusters' records — so the per-cluster LOD test, the
// tree walk and the crack-freedom argument all hold, for whatever is
// resident, and what is drawn while a page is missing is the coarser version
// above it. Root pages are always resident: the roots alone are a whole mesh.
//
// The GPU says what it wants. Every camera pass's cluster stage writes, for
// each cluster in the cut, its page's entry of the FEEDBACK buffer (used this
// frame), and for each one it DRAWS whose own cooked error is too coarse for
// the eye — drawn only because the finer pages are missing — the entry of the
// page its finer version is on (requested this frame). Entries are frame
// numbers, so the buffer is never cleared.
//
// # One frame of streaming (update, once a frame before the first cull)
//
//  1. Copy the feedback into the next of a ring of readback buffers and fence
//     it. Read back whichever earlier copies' fences have signalled — a frame
//     or two later — never waiting for one: a persistently mapped buffer is
//     read where it lies (ARB_buffer_storage), else mapped once its fence has
//     passed. Used pages (and the pages above them) are stamped; requested
//     ones, with whatever their groups need above them, are queued.
//  2. Hand queued pages, coarsest first, to the LOADERS: goroutines that read
//     a page from the file (one ReadAt) and decode it off the main thread.
//  3. Take what has arrived, up to streamPagesPerFrame pages, into the pool:
//     a free slot, or the least recently used page that the newest feedback
//     did not use (never a root page, never one this page needs above it).
//     Every byte goes up through the STAGING RING — a persistently mapped
//     buffer where the driver has one, else a plain one written by
//     glBufferSubData — in three fenced regions, one per frame in flight, and
//     is copied into place on the GPU (glCopyBufferSubData). A region whose
//     fence has not passed is not written: the frame uploads nothing rather
//     than wait.
//  4. Every mesh whose residency changed gets its cluster records (draw
//     ranges and effective errors) and its tree's errors rewritten, through
//     the same ring, before this frame's first cull reads them — which is
//     also why a slot can be refilled the frame its page is evicted.
//
// No step waits on the GPU or the disk: a page that has not arrived is drawn
// coarse, a full ring defers to the next frame, an unread feedback is read
// next frame. That is the phase's "without stalls".
//
// # Switches
//
//	FOUNDRY_NANITE_STREAM=0          the Phase 2 pool: every mesh decoded whole
//	                                 and uploaded at load (nanite_scenery.go)
//	FOUNDRY_NANITE_STREAM_POOL_MB=n  the budget (gfx.NaniteStreamPoolMB)
//	FOUNDRY_NANITE_STREAM_PRELOAD=0  start from the root pages alone, even when
//	                                 everything would fit (a test of streaming in)
//	FOUNDRY_NANITE_PERSISTENT=0      the 4.3 path for the staging ring and the
//	                                 readbacks, even where buffer storage is

import (
	"fmt"
	"log"
	"math"
	"os"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"foundry/internal/cluster"
	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/nanite"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// The switches; see the file comment.
var (
	NaniteStream        = gfx.EnvFlag("FOUNDRY_NANITE_STREAM", true)
	NaniteStreamPreload = gfx.EnvFlag("FOUNDRY_NANITE_STREAM_PRELOAD", true)
	NanitePersistent    = gfx.EnvFlag("FOUNDRY_NANITE_PERSISTENT", true)
)

// A slot of the pool: one page's decoded vertices (5 floats), normals (3) and
// indices.
const (
	streamSlotVerts   = nanite.PageMaxVertices
	streamSlotIndices = nanite.PageMaxTriangles * 3
	streamSlotBytes   = streamSlotVerts*(5+3)*4 + streamSlotIndices*4
)

// The per-frame limits and the rings' depth.
var (
	// streamPagesPerFrame is the most pages taken into the pool a frame:
	// about 2.8 MB of uploads at the slot size.
	streamPagesPerFrame = 16
	// streamInFlight is the most pages being read at once.
	streamInFlight = 32
	// streamRingRegion is one frame's region of the staging ring.
	streamRingRegion = 4 << 20
)

const (
	// streamStale is how many frames a queued page may go unasked for
	// before it is dropped from the queue.
	streamStale      = 30
	streamRingFrames = 3
	streamReadbacks  = 3
	// streamLoaders is how many goroutines read and decode pages.
	streamLoaders = 3
)

// streamPoolBudget is the pool's budget in bytes: the setting, or the
// environment's override.
func streamPoolBudget() int {
	mb := gfx.NaniteStreamPoolMB
	if v, err := strconv.Atoi(os.Getenv("FOUNDRY_NANITE_STREAM_POOL_MB")); err == nil && v > 0 {
		mb = v
	}
	return mb << 20
}

// naniteStreamer is a level's page pool and everything that fills it.
type naniteStreamer struct {
	meshes []*streamMesh
	pages  []streamPage

	// The pool.
	slots         int
	slotPage      []int32 // the global page in each slot, -1 none
	free          []int32
	vbo, nbo, ebo uint32
	// cbo is the pool's vertex colours, one RGBA8 word a vertex, when any
	// of its meshes has them (nanite.Mesh.HasColors; 0 otherwise — every
	// level without generated rocks keeps the pool it had). slotBytes is a
	// slot's size with them.
	cbo       uint32
	colors    bool
	slotBytes int

	// What the cull reads (cullBatch.stream): per cluster of the pool, its
	// page, the page it asks for and its cooked own error (infoBuf); per
	// page, when it was last used and asked for (fbBuf).
	infoBuf, fbBuf uint32

	ring      stagingRing
	readbacks [streamReadbacks]readbackSlot
	rbNext    int

	// frame numbers the frames update has run; latestFeedback is the newest
	// frame whose feedback has been read.
	frame          uint32
	latestFeedback uint32

	jobs    chan streamJob
	results chan streamResult
	stop    chan struct{}
	wg      sync.WaitGroup
	queue   []int32
	arrived []streamResult
	flight  int

	stats    NaniteStreamStats
	slowLogs int
	// immutable: the pool's buffers are glBufferStorage stores, written only
	// by copies (writeSlotDirect stages them).
	immutable bool
	loadStage uint32
	// Scratch for flushRecords.
	cbuf []gpuCluster
	nbuf []gpuNode
	// genIndex finds a mesh's streamMesh for meshGen (nanite_stream_gen.go).
	genIndex map[*nanite.Mesh]*streamMesh
}

// streamMesh is one mesh's part of the pool.
type streamMesh struct {
	m   *nanite.Mesh
	res *nanite.Residency
	// pageBase is its first global page; clusterBase and nodeBase where its
	// clusters and tree are in the renderer's buffers.
	pageBase    int32
	clusterBase uint32
	nodeBase    uint32
	// offsets is each page's clusters' first triangle within the page.
	offsets [][]uint32
	dirty   bool
	nodes   []cluster.LODNode
	// gpuClusters and gpuNodes mirror the records the GPU holds, so a
	// change uploads only what differs (flushRecords).
	gpuClusters []gpuCluster
	gpuNodes    []gpuNode
	// gen counts the times how much of the mesh is resident settled in
	// another band (noteDrawGen and meshGen, nanite_stream_gen.go); band is
	// the band it is in. The virtual shadow map's tracker hashes gen per placement.
	gen  uint64
	band int
	// noted is the band gen last counted, bandSince the streamer frame the
	// band last changed in: a band is counted once it has held for
	// streamGenSettle frames.
	noted     int
	bandSince uint32
}

// streamPage is one page's state.
type streamPage struct {
	mesh, local int32
	level       int32
	root        bool
	slot        int32
	// lastUsed and lastAsked are the newest frames the feedback said it was
	// used and asked for.
	lastUsed, lastAsked uint32
	// wanted is the newest frame something asked for it (itself, or a page
	// that needs it): a queued page not wanted for streamStale frames is
	// dropped from the queue. wantWorth is what that ask was worth.
	wanted, wantWorth uint32
	// usedWorth is what the newest feedback said the page is worth where it
	// is used: the error, in sixty-fourths of the threshold, that would show
	// were it gone (the most of any page below it, which would go with it).
	usedWorth       uint32
	queued, loading bool
	// above is every page above it (its Parents, theirs, ...), global.
	above []int32
}

type streamJob struct {
	page int32
	m    *nanite.Mesh
	pi   int
}

type streamResult struct {
	page int32
	data nanite.PageData
	err  error
}

// NaniteStreamStats is what the streamer did, for F3, the bench and tests.
type NaniteStreamStats struct {
	// The pool: slots, pages resident of all the level's pages, its bytes.
	Slots, Resident, Pages int
	PoolBytes              int
	// Persistent says the staging ring and readbacks are mapped buffers.
	Persistent bool
	// This frame: pages taken in and evicted, bytes uploaded, requests
	// queued, pages being read; and what could not be placed (no slot free
	// of current use) or was deferred (the ring full or not yet free).
	Uploaded, Evicted, Requested int
	UploadBytes                  int
	// RecordBytes is what this frame's record changes came to;
	// DirectWrites how many of them could not go through the ring.
	RecordBytes, DirectWrites int
	Queued, InFlight          int
	Starved, Deferred         int
	// Totals since the pool was built.
	TotalUploaded, TotalEvicted, TotalStarved int
	// UpdateTime is this frame's update on the CPU; MaxUpdateTime the
	// longest since the pool was built.
	UpdateTime, MaxUpdateTime time.Duration
}

// DebugNaniteStream is the current pool's stats (zero when there is none).
func DebugNaniteStream() NaniteStreamStats {
	if s := naniteScenery.stream; s != nil {
		return s.stats
	}
	return NaniteStreamStats{}
}

// ResetNaniteStreamPeak starts the pool's longest-update record over (the
// bench, at the end of its warm-up).
func ResetNaniteStreamPeak() {
	if s := naniteScenery.stream; s != nil {
		s.stats.MaxUpdateTime = 0
	}
}

// naniteStreamFrame counts DrawWorld's frames (beginNaniteLODFrame); the
// streamer updates once per value.
var naniteStreamFrame uint32 = 1

// newNaniteStreamer builds the pool for list, whose clusters and trees sit
// at clusterBase[i] and nodeBase[i] in the renderer's buffers. Nil, with the
// reason logged, when it cannot (the renderer then decodes every mesh whole).
func newNaniteStreamer(list []*nanite.Mesh, clusterBase, nodeBase []uint32) *naniteStreamer {
	s := &naniteStreamer{}
	roots := 0
	for i, m := range list {
		if len(m.Pages) == 0 {
			log.Printf("nanite: %s has no pages — streaming off for this level", m.Source)
			return nil
		}
		sm := &streamMesh{m: m, res: nanite.NewResidency(m), pageBase: int32(len(s.pages)),
			clusterBase: clusterBase[i], nodeBase: nodeBase[i]}
		for pi := range m.Pages {
			p := &m.Pages[pi]
			s.pages = append(s.pages, streamPage{mesh: int32(i), local: int32(pi), level: p.Level, root: p.Root, slot: -1})
			sm.offsets = append(sm.offsets, m.ClusterOffsets(pi))
			if p.Root {
				roots++
			}
		}
		s.meshes = append(s.meshes, sm)
	}
	for gp := range s.pages {
		s.pages[gp].above = s.findAncestors(int32(gp))
	}
	s.slotBytes = streamSlotBytes
	for _, m := range list {
		if m.HasColors {
			s.colors = true
			s.slotBytes = streamSlotBytes + streamSlotVerts*4
		}
	}
	if v, err := strconv.Atoi(os.Getenv("FOUNDRY_NANITE_STREAM_PAGES_PER_FRAME")); err == nil && v > 0 {
		streamPagesPerFrame = v
	}
	budget := streamPoolBudget() / s.slotBytes
	s.slots = min(len(s.pages), budget)
	if s.slots < roots+streamPagesPerFrame && s.slots < len(s.pages) {
		// The root pages must fit, with room to stream: past the budget, said.
		want := min(len(s.pages), roots+streamPagesPerFrame)
		log.Printf("nanite: the page pool's budget (%d MB) holds %d pages; the level's root pages alone are %d — the pool takes %d",
			streamPoolBudget()>>20, s.slots, roots, want)
		s.slots = want
	}
	s.slotPage = make([]int32, s.slots)
	for i := range s.slotPage {
		s.slotPage[i] = -1
	}
	for i := s.slots - 1; i >= 0; i-- {
		s.free = append(s.free, int32(i))
	}
	s.stats.Slots, s.stats.Pages, s.stats.PoolBytes = s.slots, len(s.pages), s.slots*s.slotBytes

	glext.Init()
	// The pool's stores are immutable wherever the driver has buffer
	// storage: a mutable buffer in use that a copy writes into, the NVIDIA
	// driver renames — copies whole, a few hundred megabytes — and a frame
	// that took pages in waited 30 to 150 ms for it (docs/nanite/PHASE_3.md).
	// An immutable one it cannot rename, so the copy simply runs in order.
	s.immutable = glext.BufferStorageAvailable()
	gl.GenBuffers(1, &s.vbo)
	gl.GenBuffers(1, &s.nbo)
	gl.GenBuffers(1, &s.ebo)
	bufs := []struct {
		buf  uint32
		size int
	}{{s.vbo, s.slots * streamSlotVerts * 5 * 4}, {s.nbo, s.slots * streamSlotVerts * 3 * 4}, {s.ebo, s.slots * streamSlotIndices * 4}}
	if s.colors {
		gl.GenBuffers(1, &s.cbo)
		bufs = append(bufs, struct {
			buf  uint32
			size int
		}{s.cbo, s.slots * streamSlotVerts * 4})
	}
	for _, b := range bufs {
		gl.BindBuffer(gl.COPY_WRITE_BUFFER, b.buf)
		if s.immutable {
			glext.BufferStorage(gl.COPY_WRITE_BUFFER, b.size, nil, 0)
		} else {
			gl.BufferData(gl.COPY_WRITE_BUFFER, b.size, nil, gl.STATIC_DRAW)
		}
	}
	gl.BindBuffer(gl.COPY_WRITE_BUFFER, 0)

	// What each cluster tells the feedback.
	var info [][4]uint32
	for _, sm := range s.meshes {
		for ci := range sm.m.Clusters {
			c := int32(ci)
			own := uint32(sm.pageBase + sm.m.PageOf(c))
			ask := uint32(0xffffffff)
			if p := sm.res.RequestPage(c); p >= 0 {
				ask = uint32(sm.pageBase + p)
			}
			info = append(info, [4]uint32{own, ask, math.Float32bits(sm.m.Clusters[ci].Error), 0})
		}
	}
	gl.GenBuffers(1, &s.infoBuf)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, s.infoBuf)
	if len(info) > 0 {
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, len(info)*16, gl.Ptr(info), gl.STATIC_DRAW)
	} else {
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, 16, nil, gl.STATIC_DRAW)
	}
	gl.GenBuffers(1, &s.fbBuf)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, s.fbBuf)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, 4*len(s.pages)*4, gl.Ptr(make([]uint32, 4*len(s.pages))), gl.DYNAMIC_COPY)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)

	persistent := NanitePersistent && glext.BufferStorageAvailable()
	s.ring.init(streamRingRegion, persistent)
	for i := range s.readbacks {
		s.readbacks[i].init(4*len(s.pages)*4, persistent)
	}
	s.stats.Persistent = persistent

	s.jobs = make(chan streamJob, len(s.pages))
	s.results = make(chan streamResult, len(s.pages))
	s.stop = make(chan struct{})
	for i := 0; i < streamLoaders; i++ {
		s.wg.Add(1)
		go s.loader()
	}

	s.preload()
	log.Printf("nanite: page pool %d of %d pages (%.1f MB, budget %d MB), %d resident at load, staging %s",
		s.slots, len(s.pages), float64(s.slots*s.slotBytes)/(1<<20), streamPoolBudget()>>20, s.stats.Resident,
		map[bool]string{true: "persistent", false: "glBufferSubData"}[persistent])
	return s
}

// loader is a goroutine that reads and decodes pages.
func (s *naniteStreamer) loader() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stop:
			return
		case j := <-s.jobs:
			d, err := j.m.LoadPage(j.pi)
			select {
			case s.results <- streamResult{page: j.page, data: d, err: err}:
			case <-s.stop:
				return
			}
		}
	}
}

// preload fills the pool at load: the root pages, then — unless
// FOUNDRY_NANITE_STREAM_PRELOAD=0 — every other page coarsest first while
// slots last, read in parallel and uploaded directly (nothing is drawing
// yet).
func (s *naniteStreamer) preload() {
	order := make([]int32, 0, len(s.pages))
	for gp := range s.pages {
		if s.pages[gp].root || NaniteStreamPreload {
			order = append(order, int32(gp))
		}
	}
	sort.SliceStable(order, func(a, b int) bool {
		pa, pb := &s.pages[order[a]], &s.pages[order[b]]
		if pa.root != pb.root {
			return pa.root
		}
		return pa.level > pb.level
	})
	if len(order) > s.slots {
		order = order[:s.slots]
	}
	datas := make([]streamResult, len(order))
	var wg sync.WaitGroup
	next := make(chan int, len(order))
	for i := range order {
		next <- i
	}
	close(next)
	for w := 0; w < min(runtime.GOMAXPROCS(0), 8); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				p := &s.pages[order[i]]
				d, err := s.meshes[p.mesh].m.LoadPage(int(p.local))
				datas[i] = streamResult{page: order[i], data: d, err: err}
			}
		}()
	}
	wg.Wait()
	for _, r := range datas {
		if r.err != nil {
			log.Printf("nanite: page %d of %s: %v", s.pages[r.page].local, s.meshes[s.pages[r.page].mesh].m.Source, r.err)
			continue
		}
		slot := s.free[len(s.free)-1]
		s.free = s.free[:len(s.free)-1]
		s.writeSlotDirect(slot, &r.data)
		s.place(r.page, slot)
	}
	for _, sm := range s.meshes {
		sm.res.Update()
		sm.dirty = true
	}
}

// writeSlotDirect uploads a page into slot with glBufferSubData — for the
// load, before anything draws from the pool.
func (s *naniteStreamer) writeSlotDirect(slot int32, d *nanite.PageData) {
	vo, no, io := s.slotOffsets(slot)
	writes := []struct {
		buf  uint32
		off  int
		data []byte
	}{{s.vbo, vo, f32bytes(d.Vertices)}, {s.nbo, no, f32bytes(d.Normals)}, {s.ebo, io, u32bytes(d.Indices)}}
	if s.cbo != 0 && len(d.Colors) > 0 {
		writes = append(writes, struct {
			buf  uint32
			off  int
			data []byte
		}{s.cbo, s.colorOffset(slot), u32bytes(d.Colors)})
	}
	for _, w := range writes {
		if len(w.data) == 0 {
			continue
		}
		if s.immutable {
			// An immutable store takes copies only: through a buffer of
			// the load's own.
			if s.loadStage == 0 {
				gl.GenBuffers(1, &s.loadStage)
				gl.BindBuffer(gl.COPY_READ_BUFFER, s.loadStage)
				gl.BufferData(gl.COPY_READ_BUFFER, streamSlotVerts*5*4, nil, gl.STREAM_DRAW)
			}
			gl.BindBuffer(gl.COPY_READ_BUFFER, s.loadStage)
			gl.BufferSubData(gl.COPY_READ_BUFFER, 0, len(w.data), gl.Ptr(w.data))
			gl.BindBuffer(gl.COPY_WRITE_BUFFER, w.buf)
			gl.CopyBufferSubData(gl.COPY_READ_BUFFER, gl.COPY_WRITE_BUFFER, 0, w.off, len(w.data))
			gl.BindBuffer(gl.COPY_READ_BUFFER, 0)
			continue
		}
		gl.BindBuffer(gl.COPY_WRITE_BUFFER, w.buf)
		gl.BufferSubData(gl.COPY_WRITE_BUFFER, w.off, len(w.data), gl.Ptr(w.data))
	}
	gl.BindBuffer(gl.COPY_WRITE_BUFFER, 0)
}

// colorOffset is where slot's colours start in cbo, in bytes.
func (s *naniteStreamer) colorOffset(slot int32) int { return int(slot) * streamSlotVerts * 4 }

// slotOffsets is where slot's vertices, normals and indices start, in bytes.
func (s *naniteStreamer) slotOffsets(slot int32) (vert, norm, idx int) {
	return int(slot) * streamSlotVerts * 5 * 4, int(slot) * streamSlotVerts * 3 * 4, int(slot) * streamSlotIndices * 4
}

// place records page gp as resident in slot.
func (s *naniteStreamer) place(gp, slot int32) {
	p := &s.pages[gp]
	p.slot = slot
	p.loading, p.queued = false, false
	p.lastUsed = s.frame
	// New, it is worth what it was asked for until the feedback says.
	p.usedWorth = p.wantWorth
	s.slotPage[slot] = gp
	sm := s.meshes[p.mesh]
	sm.res.Resident[p.local] = true
	sm.dirty = true
	s.stats.Resident++
}

// evict drops the page in slot, which becomes free.
func (s *naniteStreamer) evict(slot int32) {
	gp := s.slotPage[slot]
	p := &s.pages[gp]
	p.slot = -1
	s.slotPage[slot] = -1
	sm := s.meshes[p.mesh]
	sm.res.Resident[p.local] = false
	sm.dirty = true
	s.stats.Resident--
	s.stats.Evicted++
	s.stats.TotalEvicted++
}

// clusterRecords is mesh mi's cluster records as the cull reads them: each
// cluster's draw range where its page is (nothing, when it is not resident)
// and its errors as the residency allows.
func (s *naniteStreamer) clusterRecords(mi int, out []gpuCluster) []gpuCluster {
	sm := s.meshes[mi]
	m := sm.m
	out = out[:0]
	for pi := range m.Pages {
		p := &m.Pages[pi]
		slot := s.pages[sm.pageBase+int32(pi)].slot
		for k := int32(0); k < p.ClusterCount; k++ {
			ci := p.ClusterFirst + k
			c := &m.Clusters[ci]
			g := toGPULODCluster(c, m.ClusterLeaf[ci], 0, 0)
			if slot >= 0 {
				g.Draw[0] = uint32(slot)*streamSlotIndices + 3*sm.offsets[pi][k]
				g.Draw[2] = uint32(slot) * streamSlotVerts
			} else {
				g.Draw[0], g.Draw[1], g.Draw[2] = 0, 0, 0
			}
			own, parent := sm.res.Effective(ci)
			g.Errs[0] = own
			if c.ParentError >= 0 {
				g.Errs[1] = parent
			}
			out = append(out, g)
		}
	}
	return out
}

// staticClusterRecords is mesh m's cluster records in clusterRecords'
// order with their COOKED errors, whatever is resident: what the draw
// bound's LOD keys read (nanite_draw_lod_bound.go). The bound is built once
// from the batch's tiles, not again each time a page streams in, so it must
// not see the residency's edits. Those only ever set an error to 0 — a
// non-resident cluster's parent error, so it cannot be in the cut, and its
// resident ancestor's own error, so the ancestor stands in for it — and the
// key is a tile's largest PARENT error: the cooked one is never below what
// any later residency lets the cull draw, so the bound stays an upper bound.
// Built from the load-time records instead, a page that was not resident at
// load kept its tiles out of the bound for good, and once it streamed in the
// commands it drew were cut off past the bound: whole terrain tiles of the
// biome showcase world went missing on the GTX 1070, and on WSLg
// (docs/regions/PRESETS_3.md).
func staticClusterRecords(m *nanite.Mesh, out []gpuCluster) []gpuCluster {
	for pi := range m.Pages {
		p := &m.Pages[pi]
		for k := int32(0); k < p.ClusterCount; k++ {
			ci := p.ClusterFirst + k
			out = append(out, toGPULODCluster(&m.Clusters[ci], m.ClusterLeaf[ci], 0, 0))
		}
	}
	return out
}

// nodeRecords is mesh mi's tree with its errors as the residency allows.
func (s *naniteStreamer) nodeRecords(mi int, out []gpuNode) []gpuNode {
	sm := s.meshes[mi]
	sm.nodes = sm.res.EffectiveNodes(sm.nodes)
	out = out[:0]
	for ni := range sm.nodes {
		out = append(out, toGPUNode(&sm.nodes[ni], sm.nodeBase))
	}
	return out
}

// update is one frame of streaming (the file comment's four steps): the
// renderer calls it once per frame, before the frame's first cull. clusters
// and nodes are the renderer's buffers, which it rewrites the records in.
func (s *naniteStreamer) update(clusters, nodes uint32) {
	if s.frame == naniteStreamFrame {
		return
	}
	s.frame = naniteStreamFrame
	ProfileBegin("nanite: stream")
	defer ProfileEnd()
	start := time.Now()
	st := &s.stats
	st.Uploaded, st.Evicted, st.Requested, st.UploadBytes, st.Starved, st.Deferred = 0, 0, 0, 0, 0, 0
	st.RecordBytes, st.DirectWrites = 0, 0

	if s.wantsFeedback() || s.rbPending() {
		s.readFeedback()
	}
	tFeedback := time.Since(start)
	s.dispatch()

	// What has arrived.
	for drained := false; !drained; {
		select {
		case r := <-s.results:
			s.flight--
			s.arrived = append(s.arrived, r)
		default:
			drained = true
		}
	}
	ringOK := s.ring.begin()
	taken := 0
	keep := s.arrived[:0]
	for _, r := range s.arrived {
		p := &s.pages[r.page]
		if r.err != nil {
			log.Printf("nanite: page %d of %s: %v", p.local, s.meshes[p.mesh].m.Source, r.err)
			p.loading = false
			continue
		}
		if p.slot >= 0 {
			p.loading = false
			continue
		}
		bytes := 4 * (len(r.data.Vertices) + len(r.data.Normals) + len(r.data.Indices))
		if s.cbo != 0 {
			bytes += 4 * len(r.data.Colors)
		}
		// Room for the page (when it goes through the ring) and for what it
		// changes in the records — a few kilobytes, as a rule (flushRecords).
		staged := bytes
		if !s.immutable {
			staged = 0
		}
		if !ringOK || taken >= streamPagesPerFrame || s.ring.room() < staged+(taken+1)*streamRecordSlack {
			st.Deferred++
			keep = append(keep, r)
			continue
		}
		slot, victim := s.pickSlot(r.page)
		if slot < 0 {
			// Every slot holds something the newest feedback used: the view
			// wants more than the pool holds. The page is let go; it is
			// asked for again while it is still wanted.
			st.Starved++
			st.TotalStarved++
			p.loading = false
			continue
		}
		if victim >= 0 {
			s.evict(slot)
		} else {
			s.free = s.free[:len(s.free)-1]
		}
		if s.immutable {
			vo, no, io := s.slotOffsets(slot)
			s.ring.put(s.vbo, vo, f32bytes(r.data.Vertices))
			s.ring.put(s.nbo, no, f32bytes(r.data.Normals))
			s.ring.put(s.ebo, io, u32bytes(r.data.Indices))
			if s.cbo != 0 && len(r.data.Colors) > 0 {
				s.ring.put(s.cbo, s.colorOffset(slot), u32bytes(r.data.Colors))
			}
		} else {
			// A mutable pool takes the page by glBufferSubData, which the
			// driver stages itself — measured free of the copy's renaming.
			s.writeSlotDirect(slot, &r.data)
		}
		s.place(r.page, slot)
		taken++
		st.Uploaded++
		st.TotalUploaded++
		st.UploadBytes += bytes
	}
	s.arrived = keep
	// The records the frame's pages changed, once for every mesh touched,
	// into the room each page kept for them.
	s.flushRecords(clusters, nodes, ringOK)
	if ringOK {
		s.ring.end()
	}
	st.Queued, st.InFlight = len(s.queue), s.flight
	st.UpdateTime = time.Since(start)
	st.MaxUpdateTime = max(st.MaxUpdateTime, st.UpdateTime)
	if st.UpdateTime > 8*time.Millisecond && s.slowLogs < 20 {
		s.slowLogs++
		log.Printf("nanite: slow stream update %.1f ms (feedback %.1f ms, then %d taken in, %d evicted, %d starved, %d deferred; records %d KB, %d direct)",
			float64(st.UpdateTime.Microseconds())/1000, float64(tFeedback.Microseconds())/1000,
			st.Uploaded, st.Evicted, st.Starved, st.Deferred, st.RecordBytes/1024, st.DirectWrites)
	}
}

// streamRecordSlack is the room a page taken in keeps in the ring for the
// records it changes: its own clusters' draw ranges, and the errors of the
// groups whose refinement it allows or (by eviction) takes away — about 25
// KB a page measured on the stream bench (docs/nanite/PHASE_3.md). A frame
// whose changes outgrow it writes the rest directly (DirectWrites).
const streamRecordSlack = 96 << 10

// flushRecords sends the records of every mesh whose residency changed: its
// clusters' draw ranges and effective errors, and its tree's errors, worked
// out again and compared with what the GPU holds (the mirror), so that only
// the runs that changed go up — through the ring, or, when it cannot (no
// ring this frame, or a change bigger than its room), with glBufferSubData,
// counted as DirectWrites: a write a driver may have to order behind the
// frames in flight.
func (s *naniteStreamer) flushRecords(clusters, nodes uint32, ringOK bool) {
	cs := int(unsafe.Sizeof(gpuCluster{}))
	ns := int(unsafe.Sizeof(gpuNode{}))
	for mi, sm := range s.meshes {
		if !sm.dirty {
			continue
		}
		sm.dirty = false
		sm.res.Update()
		s.cbuf = s.clusterRecords(mi, s.cbuf)
		s.nbuf = s.nodeRecords(mi, s.nbuf)
		s.noteDrawGen(sm)
		diffRuns(len(s.cbuf), func(i int) bool { return s.cbuf[i] != sm.gpuClusters[i] }, func(a, b int) {
			copy(sm.gpuClusters[a:b], s.cbuf[a:b])
			s.writeRecords(clusters, (int(sm.clusterBase)+a)*cs,
				unsafe.Slice((*byte)(unsafe.Pointer(&sm.gpuClusters[a])), (b-a)*cs), ringOK)
		})
		diffRuns(len(s.nbuf), func(i int) bool { return s.nbuf[i] != sm.gpuNodes[i] }, func(a, b int) {
			copy(sm.gpuNodes[a:b], s.nbuf[a:b])
			s.writeRecords(nodes, (int(sm.nodeBase)+a)*ns,
				unsafe.Slice((*byte)(unsafe.Pointer(&sm.gpuNodes[a])), (b-a)*ns), ringOK)
		})
	}
}

// writeRecords puts data at off in buf: through the ring, or directly.
func (s *naniteStreamer) writeRecords(buf uint32, off int, data []byte, ringOK bool) {
	s.stats.RecordBytes += len(data)
	if ringOK && s.ring.put(buf, off, data) {
		return
	}
	s.stats.DirectWrites++
	gl.BindBuffer(gl.COPY_WRITE_BUFFER, buf)
	gl.BufferSubData(gl.COPY_WRITE_BUFFER, off, len(data), gl.Ptr(data))
	gl.BindBuffer(gl.COPY_WRITE_BUFFER, 0)
}

// diffRuns calls run(a, b) for every maximal run [a, b) of indices below n
// that changed, joining runs less than 8 apart (one copy is cheaper than
// two).
func diffRuns(n int, changed func(i int) bool, run func(a, b int)) {
	for i := 0; i < n; {
		if !changed(i) {
			i++
			continue
		}
		a, b := i, i+1
		for j := b; j < n && j < b+8; j++ {
			if changed(j) {
				b = j + 1
			}
		}
		run(a, b)
		i = b
	}
}

// pickSlot is where page gp goes: a free slot (victim -1), or the slot of
// the page to evict for it — the least recently used of those the newest
// feedback did not use, the finest first among equals; failing that, the
// page in use worth least, if gp's ask is worth streamOutworth times more
// (Nanite's own trade: a view that wants more than the pool holds keeps the
// detail that matters most). Never a root, never a page gp needs above it.
// -1 when there is none.
func (s *naniteStreamer) pickSlot(gp int32) (slot, victim int32) {
	if n := len(s.free); n > 0 {
		return s.free[n-1], -1
	}
	p := &s.pages[gp]
	above := s.ancestors(gp)
	best, bestUsed := int32(-1), int32(-1)
	for sl, q := range s.slotPage {
		if q < 0 {
			continue
		}
		qp := &s.pages[q]
		if qp.root || slices.Contains(above, q) || (qp.mesh == p.mesh && qp.local == p.local) {
			continue
		}
		if s.unused(qp) {
			if best < 0 {
				best = int32(sl)
				continue
			}
			bp := &s.pages[s.slotPage[best]]
			if qp.lastUsed < bp.lastUsed || (qp.lastUsed == bp.lastUsed && qp.level < bp.level) {
				best = int32(sl)
			}
			continue
		}
		if best < 0 && s.outworths(p.wantWorth, qp) && qp.lastUsed < s.frame {
			if bestUsed < 0 || qp.usedWorth < s.pages[s.slotPage[bestUsed]].usedWorth {
				bestUsed = int32(sl)
			}
		}
	}
	if best < 0 {
		best = bestUsed
	}
	if best < 0 {
		return -1, -1
	}
	return best, s.slotPage[best]
}

// evictable counts the resident pages pickSlot could take for a page worth
// worth: not roots, and either not used in the newest feedback or used for
// clearly less (streamOutworth).
func (s *naniteStreamer) evictable(worth uint32) int {
	n := 0
	for _, q := range s.slotPage {
		if q < 0 {
			continue
		}
		if qp := &s.pages[q]; !qp.root && (s.unused(qp) || s.outworths(worth, qp)) {
			n++
		}
	}
	return n
}

// streamOutworth is how many times what a page in use is worth an ask must
// be worth to evict it: enough that two asks of a size never trade one
// page for the other back and forth.
const streamOutworth = 2

// unused is whether the newest feedback did not use p (nor is it new).
func (s *naniteStreamer) unused(p *streamPage) bool {
	return p.lastUsed < s.latestFeedback && p.lastUsed < s.frame
}

// outworths is whether an ask worth worth may evict p, which is in use.
func (s *naniteStreamer) outworths(worth uint32, p *streamPage) bool {
	return uint64(p.usedWorth)*streamOutworth < uint64(worth)
}

// ancestors is every page above gp (its Parents, theirs, ...), as global
// pages.
func (s *naniteStreamer) ancestors(gp int32) []int32 { return s.pages[gp].above }

func (s *naniteStreamer) findAncestors(gp int32) []int32 {
	p := &s.pages[gp]
	sm := s.meshes[p.mesh]
	var out []int32
	stack := []int32{p.local}
	for len(stack) > 0 {
		l := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, q := range sm.m.Pages[l].Parents {
			g := sm.pageBase + q
			if !slices.Contains(out, g) {
				out = append(out, g)
				stack = append(stack, q)
			}
		}
	}
	return out
}

// readFeedback copies this frame's feedback into the readback ring and reads
// whatever earlier copies are ready, stamping used pages and queueing asked
// ones.
func (s *naniteStreamer) readFeedback() {
	n := len(s.pages)
	// Read the ready ones, oldest first.
	for k := 0; k < streamReadbacks; k++ {
		rb := &s.readbacks[(s.rbNext+k)%streamReadbacks]
		if !rb.pending || !rb.ready() {
			continue
		}
		data := rb.read()
		rb.pending = false
		if data == nil {
			continue
		}
		s.latestFeedback = max(s.latestFeedback, rb.frame-1)
		for gp := 0; gp < n; gp++ {
			s.pages[gp].usedWorth = 0
		}
		for gp := 0; gp < n; gp++ {
			if u := data[gp]; u > s.pages[gp].lastUsed && u < s.frame {
				s.markUsed(int32(gp), u, data[2*n+gp])
			}
		}
		for gp := 0; gp < n; gp++ {
			if a := data[n+gp]; a > s.pages[gp].lastAsked && a < s.frame {
				s.pages[gp].lastAsked = a
				s.request(int32(gp), a, data[3*n+gp])
			}
		}
		rb.done()
	}
	// Copy this frame's into the next free one, and clear the worths for
	// the frames to come (the frame numbers need no clearing).
	rb := &s.readbacks[s.rbNext]
	if !rb.pending && n > 0 {
		rb.copyFrom(s.fbBuf, 4*n*4, s.frame)
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, s.fbBuf)
		gl.ClearBufferSubData(gl.SHADER_STORAGE_BUFFER, gl.R32UI, 2*n*4, 2*n*4, gl.RED_INTEGER, gl.UNSIGNED_INT, nil)
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
		s.rbNext = (s.rbNext + 1) % streamReadbacks
	}
}

// rbPending is whether a feedback copy is still to be read.
func (s *naniteStreamer) rbPending() bool {
	for i := range s.readbacks {
		if s.readbacks[i].pending {
			return true
		}
	}
	return false
}

// markUsed stamps gp, and every page above it, used at frame u and worth
// worth: a page above is worth at least what any page below it is, since
// they would go with it.
func (s *naniteStreamer) markUsed(gp int32, u, worth uint32) {
	p := &s.pages[gp]
	p.lastUsed = max(p.lastUsed, u)
	p.usedWorth = max(p.usedWorth, worth)
	for _, q := range s.ancestors(gp) {
		s.pages[q].lastUsed = max(s.pages[q].lastUsed, u)
		s.pages[q].usedWorth = max(s.pages[q].usedWorth, worth)
	}
}

// request queues gp and whatever it needs with it: the other pages of the
// groups it holds a part of, and every page above it not resident.
func (s *naniteStreamer) request(gp int32, at, worth uint32) {
	p := &s.pages[gp]
	sm := s.meshes[p.mesh]
	want := []int32{gp}
	for _, q := range sm.res.PagesNeededWith(p.local) {
		want = append(want, sm.pageBase+q)
	}
	want = append(want, s.ancestors(gp)...)
	for _, q := range want {
		qp := &s.pages[q]
		if at > qp.wanted {
			qp.wanted, qp.wantWorth = at, worth
		} else if at == qp.wanted {
			qp.wantWorth = max(qp.wantWorth, worth)
		}
		if qp.slot >= 0 || qp.queued || qp.loading {
			continue
		}
		qp.queued = true
		s.queue = append(s.queue, q)
		s.stats.Requested++
	}
}

// dispatch hands queued pages to the loaders, coarsest first, while fewer
// than streamInFlight are being read — and no more than there is room for:
// a free slot, or a resident page the newest feedback did not use. A pool
// whose every page is in use (the view wants more than the budget holds)
// reads nothing it would only have to drop.
func (s *naniteStreamer) dispatch() {
	if len(s.queue) == 0 || s.flight >= streamInFlight {
		return
	}
	// Requests the view has moved on from go.
	s.queue = slices.DeleteFunc(s.queue, func(gp int32) bool {
		p := &s.pages[gp]
		if p.slot < 0 && p.wanted+streamStale >= s.latestFeedback {
			return false
		}
		p.queued = false
		return true
	})
	var best uint32
	for _, gp := range s.queue {
		best = max(best, s.pages[gp].wantWorth)
	}
	room := len(s.free) + s.evictable(best) - s.flight - len(s.arrived)
	if room <= 0 || len(s.queue) == 0 {
		return
	}
	sort.SliceStable(s.queue, func(a, b int) bool {
		pa, pb := &s.pages[s.queue[a]], &s.pages[s.queue[b]]
		if pa.wantWorth != pb.wantWorth {
			return pa.wantWorth > pb.wantWorth
		}
		if pa.level != pb.level {
			return pa.level > pb.level
		}
		return pa.wanted > pb.wanted
	})
	k := 0
	for ; k < len(s.queue) && s.flight < streamInFlight && room > 0; k++ {
		gp := s.queue[k]
		p := &s.pages[gp]
		p.queued = false
		if p.slot >= 0 {
			continue
		}
		p.loading = true
		s.flight++
		room--
		s.jobs <- streamJob{page: gp, m: s.meshes[p.mesh].m, pi: int(p.local)}
	}
	s.queue = append(s.queue[:0], s.queue[k:]...)
}

// release frees the pool and stops the loaders.
func (s *naniteStreamer) release() {
	close(s.stop)
	s.wg.Wait()
	for _, b := range []*uint32{&s.vbo, &s.nbo, &s.ebo, &s.cbo, &s.infoBuf, &s.fbBuf, &s.loadStage} {
		if *b != 0 {
			gl.DeleteBuffers(1, b)
			*b = 0
		}
	}
	s.ring.release()
	for i := range s.readbacks {
		s.readbacks[i].release()
	}
}

// wantsFeedback is whether the cull should write the feedback: not while
// every page is resident — a level under its budget, once loaded — when
// there is nothing to ask for and nothing will ever be evicted.
func (s *naniteStreamer) wantsFeedback() bool { return s.stats.Resident < len(s.pages) }

// bind binds the streaming buffers for a cull pass (bindings 14 and 15).
func (s *naniteStreamer) bind() {
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 14, s.infoBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 15, s.fbBuf)
}

// String is a line for logs and the overlay.
func (st NaniteStreamStats) String() string {
	return fmt.Sprintf("%d/%d slots (%.0f MB) of %d pages, +%d -%d this frame (%.0f KB), %d queued, %d loading, %d starved",
		st.Resident, st.Slots, float64(st.PoolBytes)/(1<<20), st.Pages, st.Uploaded, st.Evicted,
		float64(st.UploadBytes)/1024, st.Queued, st.InFlight, st.Starved)
}

// ---------- the staging ring ----------

// stagingRing is the streamer's upload buffer: streamRingFrames regions,
// each written in one frame and fenced after its copies, and not written
// again until its fence has passed (see the file comment).
type stagingRing struct {
	buf        uint32
	region     int
	persistent bool
	base       unsafe.Pointer
	fences     [streamRingFrames]uintptr
	cur        int
	used       int
	scratch    []byte
	copies     []stagingCopy
}

type stagingCopy struct {
	dst          uint32
	src, dst0, n int
}

func (r *stagingRing) init(region int, persistent bool) {
	r.region = region
	r.persistent = persistent
	r.cur = -1
	gl.GenBuffers(1, &r.buf)
	gl.BindBuffer(gl.COPY_READ_BUFFER, r.buf)
	size := region * streamRingFrames
	if persistent {
		flags := uint32(gl.MAP_WRITE_BIT | glext.MapPersistentBit | glext.MapCoherentBit)
		glext.BufferStorage(gl.COPY_READ_BUFFER, size, nil, flags)
		r.base = gl.MapBufferRange(gl.COPY_READ_BUFFER, 0, size, flags)
		if r.base == nil {
			log.Printf("nanite: the staging ring could not be mapped — glBufferSubData instead")
			gl.BindBuffer(gl.COPY_READ_BUFFER, 0)
			gl.DeleteBuffers(1, &r.buf)
			r.init(region, false)
			return
		}
	} else {
		gl.BufferData(gl.COPY_READ_BUFFER, size, nil, gl.STREAM_DRAW)
		r.scratch = make([]byte, region)
	}
	gl.BindBuffer(gl.COPY_READ_BUFFER, 0)
}

// begin opens the next region for this frame: false (and nothing may be
// put) when the GPU has not finished with it.
func (r *stagingRing) begin() bool {
	next := (r.cur + 1) % streamRingFrames
	if f := r.fences[next]; f != 0 {
		if !fencePassed(f) {
			return false
		}
		gl.DeleteSync(f)
		r.fences[next] = 0
	}
	r.cur = next
	r.used = 0
	r.copies = r.copies[:0]
	return true
}

// room is the bytes left in this frame's region.
func (r *stagingRing) room() int { return r.region - r.used }

// put stages data for dst at byte offset off: false when it does not fit.
func (r *stagingRing) put(dst uint32, off int, data []byte) bool {
	if len(data) == 0 {
		return true
	}
	// Keep each write 16-byte aligned (GL_COPY_* has no rule, drivers like
	// it).
	at := (r.used + 15) &^ 15
	if at+len(data) > r.region {
		return false
	}
	if r.persistent {
		dst := unsafe.Slice((*byte)(unsafe.Add(r.base, r.cur*r.region+at)), len(data))
		copy(dst, data)
	} else {
		copy(r.scratch[at:], data)
	}
	r.copies = append(r.copies, stagingCopy{dst: dst, src: r.cur*r.region + at, dst0: off, n: len(data)})
	r.used = at + len(data)
	return true
}

// end issues the region's copies and fences it.
func (r *stagingRing) end() {
	if len(r.copies) == 0 {
		return
	}
	gl.BindBuffer(gl.COPY_READ_BUFFER, r.buf)
	if !r.persistent {
		gl.BufferSubData(gl.COPY_READ_BUFFER, r.cur*r.region, r.used, gl.Ptr(r.scratch))
	}
	for _, c := range r.copies {
		gl.BindBuffer(gl.COPY_WRITE_BUFFER, c.dst)
		gl.CopyBufferSubData(gl.COPY_READ_BUFFER, gl.COPY_WRITE_BUFFER, c.src, c.dst0, c.n)
	}
	gl.BindBuffer(gl.COPY_WRITE_BUFFER, 0)
	gl.BindBuffer(gl.COPY_READ_BUFFER, 0)
	r.fences[r.cur] = gl.FenceSync(gl.SYNC_GPU_COMMANDS_COMPLETE, 0)
	// The copies wrote buffers the cull reads as storage and the draw as
	// vertices and indices.
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT | gl.VERTEX_ATTRIB_ARRAY_BARRIER_BIT | gl.ELEMENT_ARRAY_BARRIER_BIT)
	r.copies = r.copies[:0]
}

func (r *stagingRing) release() {
	for i, f := range r.fences {
		if f != 0 {
			gl.DeleteSync(f)
			r.fences[i] = 0
		}
	}
	if r.buf != 0 {
		if r.persistent {
			gl.BindBuffer(gl.COPY_READ_BUFFER, r.buf)
			gl.UnmapBuffer(gl.COPY_READ_BUFFER)
			gl.BindBuffer(gl.COPY_READ_BUFFER, 0)
		}
		gl.DeleteBuffers(1, &r.buf)
		r.buf = 0
	}
}

// ---------- the feedback readbacks ----------

// readbackSlot is one buffer of the feedback's readback ring: a copy of the
// feedback, its fence, and the frame it was copied in.
type readbackSlot struct {
	buf        uint32
	size       int
	persistent bool
	base       unsafe.Pointer
	fence      uintptr
	frame      uint32
	pending    bool
	out        []uint32
}

func (r *readbackSlot) init(size int, persistent bool) {
	r.size = max(size, 4)
	r.persistent = persistent
	gl.GenBuffers(1, &r.buf)
	gl.BindBuffer(gl.COPY_WRITE_BUFFER, r.buf)
	if persistent {
		flags := uint32(gl.MAP_READ_BIT | glext.MapPersistentBit | glext.MapCoherentBit)
		glext.BufferStorage(gl.COPY_WRITE_BUFFER, r.size, nil, flags)
		r.base = gl.MapBufferRange(gl.COPY_WRITE_BUFFER, 0, r.size, flags)
		if r.base == nil {
			gl.BindBuffer(gl.COPY_WRITE_BUFFER, 0)
			gl.DeleteBuffers(1, &r.buf)
			r.init(size, false)
			return
		}
	} else {
		gl.BufferData(gl.COPY_WRITE_BUFFER, r.size, nil, gl.STREAM_READ)
	}
	gl.BindBuffer(gl.COPY_WRITE_BUFFER, 0)
	r.out = make([]uint32, r.size/4)
}

// copyFrom copies size bytes of src into the slot and fences it.
func (r *readbackSlot) copyFrom(src uint32, size int, frame uint32) {
	if size == 0 {
		return
	}
	gl.MemoryBarrier(gl.BUFFER_UPDATE_BARRIER_BIT | glext.ClientMappedBuffer)
	gl.BindBuffer(gl.COPY_READ_BUFFER, src)
	gl.BindBuffer(gl.COPY_WRITE_BUFFER, r.buf)
	gl.CopyBufferSubData(gl.COPY_READ_BUFFER, gl.COPY_WRITE_BUFFER, 0, 0, min(size, r.size))
	gl.BindBuffer(gl.COPY_WRITE_BUFFER, 0)
	gl.BindBuffer(gl.COPY_READ_BUFFER, 0)
	r.fence = gl.FenceSync(gl.SYNC_GPU_COMMANDS_COMPLETE, 0)
	r.frame = frame
	r.pending = true
}

// ready is whether the copy has landed (never waits).
func (r *readbackSlot) ready() bool {
	return r.fence != 0 && fencePassed(r.fence)
}

// alive is whether the slot's fence is still a sync object of the current
// context (glIsSync): a fence deleted behind the slot's back — a copied
// slot whose twin called done — must never be queried, which is
// GL_INVALID_VALUE (nanite_vis_overflow.go, "Fence lifetime").
func (r *readbackSlot) alive() bool {
	return r.fence != 0 && gl.IsSync(r.fence)
}

// fencePassed asks whether the GPU has passed fence f, without waiting:
// glGetSynciv's status, a query. Not glClientWaitSync with a zero timeout,
// which ought to be the same and on the NVIDIA driver measured is not —
// for the first frames after a level's pages went up it blocked 28 to 64 ms
// a call (docs/nanite/PHASE_3.md).
func fencePassed(f uintptr) bool {
	var status, n int32
	gl.GetSynciv(f, gl.SYNC_STATUS, 1, &n, &status)
	return status == gl.SIGNALED
}

// read is the copy's words, valid until done.
func (r *readbackSlot) read() []uint32 {
	if r.persistent {
		src := unsafe.Slice((*uint32)(r.base), r.size/4)
		copy(r.out, src)
		return r.out
	}
	gl.BindBuffer(gl.COPY_WRITE_BUFFER, r.buf)
	p := gl.MapBufferRange(gl.COPY_WRITE_BUFFER, 0, r.size, gl.MAP_READ_BIT)
	if p == nil {
		gl.BindBuffer(gl.COPY_WRITE_BUFFER, 0)
		return nil
	}
	copy(r.out, unsafe.Slice((*uint32)(p), r.size/4))
	gl.UnmapBuffer(gl.COPY_WRITE_BUFFER)
	gl.BindBuffer(gl.COPY_WRITE_BUFFER, 0)
	return r.out
}

// done lets the slot be copied into again.
func (r *readbackSlot) done() {
	if r.fence != 0 {
		gl.DeleteSync(r.fence)
		r.fence = 0
	}
}

func (r *readbackSlot) release() {
	r.done()
	if r.buf != 0 {
		if r.persistent {
			gl.BindBuffer(gl.COPY_WRITE_BUFFER, r.buf)
			gl.UnmapBuffer(gl.COPY_WRITE_BUFFER)
			gl.BindBuffer(gl.COPY_WRITE_BUFFER, 0)
		}
		gl.DeleteBuffers(1, &r.buf)
		r.buf = 0
	}
}

// f32bytes and u32bytes view a slice as its bytes.
func f32bytes(v []float32) []byte {
	if len(v) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&v[0])), len(v)*4)
}

func u32bytes(v []uint32) []byte {
	if len(v) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&v[0])), len(v)*4)
}
