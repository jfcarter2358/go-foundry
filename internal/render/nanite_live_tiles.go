package render

// nanite_live_tiles.go — instance-level culling before the range tiles are
// expanded: each pass, the range cull (and the packing's range scatter) run
// over a LIVE list of the tiles of the instances the instance stage kept,
// not over every range tile of the batch (nanite_live_tiles_comp.glsl).
//
// A cooked mesh's placement brings its DAG's every leaf window as a range
// tile: about 3 for a 600-triangle rubble stone, 210 for a 120k-triangle
// cliff chunk, a thousand for a hero scan. The cull launched a thread for
// every one of them every pass, and a thread costs its launch whether its
// instance is in view or not — the range cull's per-tile state keeps its
// occupancy low. Measured on the dev box (WSLg, docs/canyon/PHASE_3.md): a
// kilometre of canyon with 1,265 cliff chunks (287k range tiles) drew a frame
// looking at the empty sky in 125 ms against 25 ms without them; skipping the
// range dispatch outright brought it to 31 ms. With the live list a pass
// pays for the instances it can see.
//
// What changes and what does not:
//
//   - The command slots stay fixed, and every tile that runs writes its
//     own, so the draw order and a capture are as before.
//   - A culled tile does not run, so what it would have written — no
//     commands, no survivors — is written by the list instead, when its run
//     goes out of view (runLive), so the fixed list and the survivor counts
//     hold every pass exactly the live commands the full dispatch wrote, in
//     their slots: what a shadow view draws, what the packing's prefix sum
//     reads, what the packing is checked against. (A culled slot reads as
//     all zeros rather than a zero count over its first cluster: the same to
//     every reader.) Clearing the whole command list every pass instead
//     cost ~10 ms a frame on the dev box over the canyon's passes.
//   - The history of a culled instance's clusters (pass 2's "not seen") is
//     cleared once when it goes out of view, per run, rather than every
//     pass (runSeen).
//
// FOUNDRY_NANITE_LIVE_TILES=0 dispatches over every range tile, as before.
// Batches without range tiles (compiled levels, debug views' cluster tiles)
// never take this path.

import (
	"fmt"

	"foundry/internal/gfx"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// NaniteLiveTiles switches the live-tile list on; see the file comment.
var NaniteLiveTiles = gfx.EnvFlag("FOUNDRY_NANITE_LIVE_TILES", true)

// liveTiles is a batch's live-list state.
type liveTiles struct {
	// runs are the batch's runs (first kind-list index, tile count,
	// instance, 0), uploaded to runBuf when the tiles change; seenBuf is
	// per run whether its history may be set, liveBuf whether its slots
	// hold what the range cull last wrote.
	runs                     []uint32
	runBuf, seenBuf, liveBuf uint32
	// listBuf holds a pass's live tiles, countBuf its length and the
	// dispatch's workgroups.
	listBuf, countBuf uint32
	nRuns             int
	dirty             bool
	// stale: a pass dispatched every tile (the switch off), so every run's
	// slots are written and runLive must say so again.
	stale bool
}

// liveTilePrograms are the list's two stages.
type liveTilePrograms struct {
	list, args progUniforms
	ok         bool
}

var naniteLive liveTilePrograms

// buildLiveTiles compiles the stages (once, from naniteCuller.build). A
// failure leaves the old dispatch in place.
func buildLiveTiles() error {
	src := mustReadShader(biomeShaderPath("nanite_live_tiles_comp.glsl"))
	for _, v := range []struct {
		define string
		out    *progUniforms
	}{{"LIVE_LIST", &naniteLive.list}, {"LIVE_ARGS", &naniteLive.args}} {
		cs, err := gfx.CompileShader(fmt.Sprintf("#version 430\n#define RANGE_SLOTS %du\n#define %s\n", rangeSlots, v.define)+src, gl.COMPUTE_SHADER)
		if err != nil {
			return fmt.Errorf("nanite_live_tiles_comp.glsl (%s): %v", v.define, err)
		}
		prog, err := linkStages(cs)
		if err != nil {
			return fmt.Errorf("nanite_live_tiles_comp.glsl (%s): %v", v.define, err)
		}
		*v.out = progUniforms{prog: prog, loc: map[string]int32{}}
	}
	naniteLive.ok = true
	return nil
}

// liveOn reports whether this batch's range tiles go through the list.
func (b *cullBatch) liveOn() bool {
	return NaniteLiveTiles && naniteLive.ok && b.rangeTile > 0
}

// setLiveRuns files the range tiles of kinds (the kind list, range tiles
// from index b.clusterTiles) by run. Called when the tiles change.
func (b *cullBatch) setLiveRuns(kinds []uint32) {
	l := &b.live2
	l.runs = l.runs[:0]
	l.nRuns = 0
	for n := b.clusterTiles; n < b.clusterTiles+b.rangeTile; {
		inst := b.tiles[kinds[n]].A[2]
		group := b.tiles[kinds[n]].B[3]
		start := n
		for n < b.clusterTiles+b.rangeTile && b.tiles[kinds[n]].A[2] == inst && b.tiles[kinds[n]].B[3] == group {
			n++
		}
		l.runs = append(l.runs, uint32(start), uint32(n-start), inst, 0)
		l.nRuns++
	}
	l.dirty = true
}

// uploadLive sends the runs and sizes the list when the tiles changed.
func (b *cullBatch) uploadLive() {
	l := &b.live2
	if l.runBuf == 0 {
		gl.GenBuffers(1, &l.runBuf)
		gl.GenBuffers(1, &l.seenBuf)
		gl.GenBuffers(1, &l.liveBuf)
		gl.GenBuffers(1, &l.listBuf)
		gl.GenBuffers(1, &l.countBuf)
		gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, l.countBuf)
		gl.BufferData(gl.SHADER_STORAGE_BUFFER, 16, nil, gl.DYNAMIC_COPY)
	}
	if l.stale && !l.dirty {
		l.stale = false
		l.uploadOnes(l.liveBuf)
	}
	if !l.dirty {
		return
	}
	l.dirty, l.stale = false, false
	runs := l.runs
	if len(runs) == 0 {
		runs = []uint32{0, 0, 0, 0}
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, l.runBuf)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, len(runs)*4, gl.Ptr(runs), gl.STATIC_DRAW)
	// Every run starts "seen" and "live": the first pass 2 that finds it
	// culled clears its history, and the first pass that does clears its
	// slots (the store is new: what is in it is undefined).
	l.uploadOnes(l.seenBuf)
	l.uploadOnes(l.liveBuf)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, l.listBuf)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, max(b.rangeTile, 1)*4, nil, gl.DYNAMIC_COPY)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
}

// uploadOnes fills buf with a 1 per run.
func (l *liveTiles) uploadOnes(buf uint32) {
	ones := make([]uint32, max(l.nRuns, 1))
	for i := range ones {
		ones[i] = 1
	}
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, buf)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, len(ones)*4, gl.Ptr(ones), gl.DYNAMIC_COPY)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
}

// buildLive builds this pass's list and its dispatch, clearing the slots of
// runs that went out of view (see the file comment). The cull's buffers
// are bound (instances at 1, tiles at 3, history at 4, the kind list at 13).
func (b *cullBatch) buildLive(mode int32) {
	l := &b.live2
	b.uploadLive()
	var zero uint32
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, l.countBuf)
	gl.ClearBufferData(gl.SHADER_STORAGE_BUFFER, gl.R32UI, gl.RED_INTEGER, gl.UNSIGNED_INT, gl.Ptr(&zero))
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT)

	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 18, l.countBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 19, l.listBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 20, l.runBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 21, l.seenBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 22, l.liveBuf)
	p := &naniteLive.list
	gl.UseProgram(p.prog)
	gl.Uniform1ui(p.U("runCount"), uint32(l.nRuns))
	gl.Uniform1i(p.U("mode"), mode)
	groups := (l.nRuns + 63) / 64
	for done := 0; done < groups; done += 65535 {
		gl.Uniform1ui(p.U("workBase"), uint32(done*64))
		gl.DispatchCompute(uint32(min(groups-done, 65535)), 1, 1)
	}
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT)
	gl.UseProgram(naniteLive.args.prog)
	gl.DispatchCompute(1, 1, 1)
	gl.MemoryBarrier(gl.SHADER_STORAGE_BARRIER_BIT | gl.COMMAND_BARRIER_BIT)
}

// dispatchLive runs program p (the range cull or the range scatter, its
// uniforms set) over this pass's live list: the list bound as the kind list,
// its length as the bound, an indirect dispatch.
func (b *cullBatch) dispatchLive(p *progUniforms) {
	l := &b.live2
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 13, l.listBuf)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 18, l.countBuf)
	gl.Uniform1i(p.U("liveList"), 1)
	gl.Uniform1ui(p.U("tileBase"), 0)
	gl.Uniform1ui(p.U("workBase"), 0)
	gl.BindBuffer(gl.DISPATCH_INDIRECT_BUFFER, l.countBuf)
	gl.DispatchComputeIndirect(4)
	gl.BindBuffer(gl.DISPATCH_INDIRECT_BUFFER, 0)
	gl.Uniform1i(p.U("liveList"), 0)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 13, b.kindBuf)
}

// releaseLive frees the list's buffers.
func (b *cullBatch) releaseLive() {
	l := &b.live2
	for _, buf := range []*uint32{&l.runBuf, &l.seenBuf, &l.liveBuf, &l.listBuf, &l.countBuf} {
		if *buf != 0 {
			gl.DeleteBuffers(1, buf)
			*buf = 0
		}
	}
	l.dirty = true
}
