package render

// sprite_stream.go — the sprite pass's instance buffer.
//
// Every pass that draws sprites (the camera's three blend modes, each shadow
// cascade, each point light's cube face, each virtual-shadow-map rectangle)
// writes ALL of its batches' instances in one go and then draws each batch
// from its own stretch of the buffer with glDrawArraysInstancedBaseInstance.
// The base instance offsets every per-instance attribute fetch (sprite_
// common.glsl's locations 1-5), which is all the shader needs: it never reads
// gl_InstanceID or gl_BaseInstance itself.
//
// Until this, each BATCH orphaned the whole 3.67 MB buffer and wrote its own
// instances to the front of the new store — a driver allocation and a
// re-specification per draw, several dozen a frame.
//
// Where glBufferStorage is there (GL 4.4 / ARB_buffer_storage) the buffer is
// one immutable store mapped once, persistently and coherently, and used as a
// RING: a pass's instances go at the head, and the head moves on. The GPU may
// still be reading what earlier frames wrote, so each frame's stretches are
// fenced when the next frame starts writing, and a write that would land on a
// stretch whose fence has not passed waits for it — which a ring holding
// several frames' worth only ever does when a single frame writes more than
// the whole ring. Without buffer storage (or with FOUNDRY_SPRITE_PERSISTENT=0)
// each pass orphans the buffer and gives it exactly the bytes it writes.

import (
	"log"
	"unsafe"

	"foundry/internal/gfx"
	"foundry/internal/glext"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// SpritePersistent switches the mapped ring; see the file comment.
var SpritePersistent = gfx.EnvFlag("FOUNDRY_SPRITE_PERSISTENT", true)

// spriteRingInstances is the ring's size in instances: four full batches.
const spriteRingInstances = 4 * spriteMaxInstances

// spriteSpan is a stretch of the ring, in instances: [lo, hi).
type spriteSpan struct{ lo, hi int }

// spriteRingFrame is a finished frame's stretches and the fence after them.
type spriteRingFrame struct {
	spans []spriteSpan
	fence uintptr
}

type spriteStream struct {
	persistent bool
	base       unsafe.Pointer
	head       int
	// frame is the DrawWorld frame the current stretches were written in.
	frame   uint64
	current []spriteSpan
	// pending are earlier frames' stretches, oldest first, until their fences
	// pass.
	pending []spriteRingFrame
	spare   [][]spriteSpan
}

// initSpriteStream gives sprites.ibo its store. Called with the VAO bound,
// before specifySpriteAttribs points the attributes at it.
func initSpriteStream() {
	st := &sprites.stream
	glext.Init()
	gl.GenBuffers(1, &sprites.ibo)
	gl.BindBuffer(gl.ARRAY_BUFFER, sprites.ibo)
	if SpritePersistent && glext.BufferStorageAvailable() {
		size := spriteRingInstances * spriteInstanceFloats * 4
		flags := uint32(gl.MAP_WRITE_BIT | glext.MapPersistentBit | glext.MapCoherentBit)
		glext.BufferStorage(gl.ARRAY_BUFFER, size, nil, flags)
		st.base = gl.MapBufferRange(gl.ARRAY_BUFFER, 0, size, flags)
		if st.base != nil {
			st.persistent = true
			return
		}
		log.Printf("sprites: the instance ring could not be mapped — orphaned uploads instead")
		gl.BindBuffer(gl.ARRAY_BUFFER, 0)
		gl.DeleteBuffers(1, &sprites.ibo)
		gl.GenBuffers(1, &sprites.ibo)
		gl.BindBuffer(gl.ARRAY_BUFFER, sprites.ibo)
	}
	gl.BufferData(gl.ARRAY_BUFFER, spriteMaxInstances*spriteInstanceFloats*4, nil, gl.STREAM_DRAW)
}

// releaseSpriteStream lets the ring go: its fences, and its mapping.
func releaseSpriteStream() {
	st := &sprites.stream
	for _, f := range st.pending {
		gl.DeleteSync(f.fence)
	}
	st.pending = nil
	if st.persistent && sprites.ibo != 0 {
		gl.BindBuffer(gl.ARRAY_BUFFER, sprites.ibo)
		gl.UnmapBuffer(gl.ARRAY_BUFFER)
		gl.BindBuffer(gl.ARRAY_BUFFER, 0)
	}
	sprites.stream = spriteStream{}
}

// spriteUploadCount is how many instances of batches a pass draws: each batch
// capped at spriteMaxInstances, as one draw always has been.
func spriteUploadCount(batches []spriteBatch) int {
	n := 0
	for i := range batches {
		n += min(len(batches[i].Instances), spriteMaxInstances)
	}
	return n
}

// uploadSpriteBatches writes every batch's instances, back to back, and
// returns where the first one starts in the buffer, in instances; batch i's
// start is that plus the counts before it. The caller has checked the total
// fits in one draw's worth of an orphaned store (spriteMaxInstances).
func uploadSpriteBatches(batches []spriteBatch, total int) int {
	st := &sprites.stream
	if !st.persistent {
		// Orphaned: a fresh store of exactly what this pass writes.
		gl.BindBuffer(gl.ARRAY_BUFFER, sprites.ibo)
		gl.BufferData(gl.ARRAY_BUFFER, total*spriteInstanceFloats*4, nil, gl.STREAM_DRAW)
		at := 0
		for i := range batches {
			n := min(len(batches[i].Instances), spriteMaxInstances)
			if n == 0 {
				continue
			}
			gl.BufferSubData(gl.ARRAY_BUFFER, at*spriteInstanceFloats*4, n*spriteInstanceFloats*4, unsafe.Pointer(&batches[i].Instances[0]))
			at += n
		}
		return 0
	}
	lo := st.alloc(total)
	dst := unsafe.Slice((*SpriteInstance)(st.base), spriteRingInstances)[lo : lo+total]
	at := 0
	for i := range batches {
		n := min(len(batches[i].Instances), spriteMaxInstances)
		copy(dst[at:at+n], batches[i].Instances[:n])
		at += n
	}
	return lo
}

// alloc finds n instances of the ring nothing in flight is reading, at the
// head or, when the head is too near the end, at the start.
func (st *spriteStream) alloc(n int) int {
	if st.frame != drawFrameNo || !drawFrameOpen {
		st.closeFrame()
		st.frame = drawFrameNo
	}
	lo := st.head
	if lo+n > spriteRingInstances {
		lo = 0
	}
	span := spriteSpan{lo, lo + n}
	// Earlier frames' stretches under this one: retired as their fences
	// pass, oldest first, waited for when they have not.
	for len(st.pending) > 0 && st.pendingOverlaps(span) {
		st.retireOldest()
	}
	// This frame's own: only a frame writing more than the whole ring gets
	// here, and it waits for its own earlier draws.
	if overlapsAny(st.current, span) {
		f := gl.FenceSync(gl.SYNC_GPU_COMMANDS_COMPLETE, 0)
		gl.ClientWaitSync(f, gl.SYNC_FLUSH_COMMANDS_BIT, 1<<62)
		gl.DeleteSync(f)
		st.current = st.current[:0]
	}
	st.current = append(st.current, span)
	st.head = span.hi
	return lo
}

// closeFrame fences the stretches the last frame wrote: everything it drew
// from them has been issued by now.
func (st *spriteStream) closeFrame() {
	if len(st.current) == 0 {
		return
	}
	next := st.current
	if k := len(st.spare); k > 0 {
		st.current = st.spare[k-1]
		st.spare = st.spare[:k-1]
	} else {
		st.current = nil
	}
	st.pending = append(st.pending, spriteRingFrame{spans: next, fence: gl.FenceSync(gl.SYNC_GPU_COMMANDS_COMPLETE, 0)})
	// A frame that writes little takes a long time to come round the ring;
	// its oldest frames are let go well before then rather than piling up
	// fences. Eight frames back is long finished.
	for len(st.pending) > spriteRingMaxPending {
		st.retireOldest()
	}
}

// spriteRingMaxPending is how many finished frames keep their fences.
const spriteRingMaxPending = 8

// retireOldest waits for (if it must) and forgets the oldest pending frame.
func (st *spriteStream) retireOldest() {
	f := st.pending[0]
	if !fencePassed(f.fence) {
		gl.ClientWaitSync(f.fence, gl.SYNC_FLUSH_COMMANDS_BIT, 1<<62)
	}
	gl.DeleteSync(f.fence)
	st.spare = append(st.spare, f.spans[:0])
	st.pending = st.pending[1:]
}

func (st *spriteStream) pendingOverlaps(s spriteSpan) bool {
	for i := range st.pending {
		if overlapsAny(st.pending[i].spans, s) {
			return true
		}
	}
	return false
}

func overlapsAny(spans []spriteSpan, s spriteSpan) bool {
	for _, o := range spans {
		if s.lo < o.hi && o.lo < s.hi {
			return true
		}
	}
	return false
}
