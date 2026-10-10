// Package glext reaches the few OpenGL entry points past the GL 4.3 core the
// engine is built against (github.com/go-gl/gl/v4.3-core), each one only after
// the driver has said it has it. Phase 1 of the Nanite plan
// (docs/nanite/PHASE_1.md).
//
// The engine's baseline stays 4.3: nothing here is required, every caller has
// a 4.3 path for when Available is false, and a function pointer is looked up
// only once a context is current. The lookup is the one go-gl itself uses
// (wglGetProcAddress / glXGetProcAddress), so it finds what the binding would.
//
// What is here:
//
//   - glMultiDrawElementsIndirectCount (core in 4.6, ARB_indirect_parameters
//     before that): an indirect multi-draw whose draw count is read from a
//     buffer, so a GPU cull that compacted its survivors draws exactly those —
//     the 4.3 alternative is a fixed list in which a culled draw is a zero
//     count, and a driver still walks every one (render/nanite_cull.go).
//   - glBufferStorage (core in 4.4, ARB_buffer_storage before that): an
//     immutable buffer that can stay MAPPED while the GPU uses it
//     (persistent, coherent), so the Nanite streamer writes pages into its
//     staging ring and reads its feedback back without a map call a frame
//     (render/nanite_stream.go, Phase 3). The 4.3 alternative is a plain
//     buffer written with glBufferSubData and read with glMapBufferRange,
//     fenced the same way.
//   - 64-bit integer atomics in shaders (GL_NV_shader_atomic_int64, with the
//     64-bit integer types of GL_ARB_gpu_shader_int64 or GL_NV_gpu_shader5):
//     no entry point, only a feature the compute rasteriser's one-pass form
//     needs — an atomicMax of depth<<32 | triangle ID into one word a pixel
//     (render/nanite_swraster.go, Phase 5). No 4.x core version has them.
//     Without them the rasteriser is either off (the hardware draws every
//     triangle) or, asked for, runs its two-pass 32-bit form.
package glext

/*
#cgo windows CFLAGS: -DTAG_WINDOWS
#cgo windows LDFLAGS: -lopengl32
#cgo linux freebsd netbsd openbsd CFLAGS: -DTAG_POSIX
#cgo linux freebsd netbsd openbsd pkg-config: gl
#cgo darwin CFLAGS: -DTAG_DARWIN
#cgo darwin LDFLAGS: -framework OpenGL
#include <stdint.h>
#include <stdlib.h>
#if defined(TAG_WINDOWS)
	#define WIN32_LEAN_AND_MEAN 1
	#include <windows.h>
	static HMODULE glextOgl32 = NULL;
	static void* glextProc(const char* name) {
		void* pf = (void*)wglGetProcAddress((LPCSTR)name);
		if (pf == (void*)0 || pf == (void*)1 || pf == (void*)2 || pf == (void*)3 || pf == (void*)-1) {
			if (glextOgl32 == NULL) {
				glextOgl32 = LoadLibraryA("opengl32.dll");
			}
			pf = (void*)GetProcAddress(glextOgl32, (LPCSTR)name);
		}
		return pf;
	}
	#define GLEXT_APIENTRY __stdcall
#elif defined(TAG_DARWIN)
	#include <dlfcn.h>
	static void* glextProc(const char* name) { return dlsym(RTLD_DEFAULT, name); }
	#define GLEXT_APIENTRY
#else
	#include <GL/glx.h>
	static void* glextProc(const char* name) { return (void*)glXGetProcAddress((const GLubyte*)name); }
	#define GLEXT_APIENTRY
#endif

typedef void (GLEXT_APIENTRY *glextMDEICFn)(unsigned int mode, unsigned int type, const void* indirect,
	intptr_t drawcount, int maxdrawcount, int stride);

static void glextCallMDEIC(void* fn, unsigned int mode, unsigned int type, uintptr_t indirect,
		intptr_t drawcount, int maxdrawcount, int stride) {
	((glextMDEICFn)fn)(mode, type, (const void*)indirect, drawcount, maxdrawcount, stride);
}

typedef void (GLEXT_APIENTRY *glextBufferStorageFn)(unsigned int target, intptr_t size, const void* data, unsigned int flags);

static void glextCallBufferStorage(void* fn, unsigned int target, intptr_t size, uintptr_t data, unsigned int flags) {
	((glextBufferStorageFn)fn)(target, size, (const void*)data, flags);
}
*/
import "C"

import (
	"strings"
	"unsafe"

	"github.com/go-gl/gl/v4.3-core/gl"
)

var (
	tried         bool
	mdeic         unsafe.Pointer
	bufferStorage unsafe.Pointer
	// int64Atomics: the shading language has 64-bit integers and atomics on
	// them in storage buffers.
	int64Atomics bool
)

// Init looks the entry points up. Call with a context current; calling it
// again does nothing.
func Init() {
	if tried {
		return
	}
	tried = true
	if has(4, 6, "GL_ARB_indirect_parameters") {
		mdeic = lookup("glMultiDrawElementsIndirectCount", "glMultiDrawElementsIndirectCountARB")
	}
	if has(4, 4, "GL_ARB_buffer_storage") {
		bufferStorage = lookup("glBufferStorage")
	}
	int64Atomics = hasExtension("GL_NV_shader_atomic_int64") &&
		(hasExtension("GL_ARB_gpu_shader_int64") || hasExtension("GL_NV_gpu_shader5"))
}

// lookup is the first of names the driver has an entry point for.
func lookup(names ...string) unsafe.Pointer {
	for _, name := range names {
		cname := C.CString(name)
		p := C.glextProc(cname)
		C.free(unsafe.Pointer(cname))
		if p != nil {
			return p
		}
	}
	return nil
}

// has is whether the context offers a feature: GL major.minor, or the
// extension.
func has(major, minor int32, ext string) bool {
	var ma, mi int32
	gl.GetIntegerv(gl.MAJOR_VERSION, &ma)
	gl.GetIntegerv(gl.MINOR_VERSION, &mi)
	if ma > major || (ma == major && mi >= minor) {
		return true
	}
	return hasExtension(ext)
}

// hasExtension is whether the context lists the extension.
func hasExtension(ext string) bool {
	var n int32
	gl.GetIntegerv(gl.NUM_EXTENSIONS, &n)
	for i := int32(0); i < n; i++ {
		if strings.EqualFold(gl.GoStr(gl.GetStringi(gl.EXTENSIONS, uint32(i))), ext) {
			return true
		}
	}
	return false
}

// IndirectCountAvailable reports whether MultiDrawElementsIndirectCount can be
// called. Init must have run.
func IndirectCountAvailable() bool { return mdeic != nil }

// Disable forgets the entry points, so every caller takes its 4.3 path: for
// A/B timing and for the tests that pin the fallback.
func Disable() { mdeic, bufferStorage, int64Atomics = nil, nil, false }

// Reinit looks the entry points up again, undoing Disable.
func Reinit() {
	tried = false
	mdeic, bufferStorage, int64Atomics = nil, nil, false
	Init()
}

// Int64AtomicsAvailable reports whether a shader may declare uint64_t storage
// and atomicMax on it (see the file comment); the shaders that do declare the
// extensions themselves. Init must have run.
func Int64AtomicsAvailable() bool { return int64Atomics }

// DisableInt64Atomics forgets the 64-bit atomics alone (the compute
// rasteriser's two-pass and hardware fallbacks, for tests and A/B timing).
func DisableInt64Atomics() { int64Atomics = false }

// DisableBufferStorage forgets glBufferStorage alone (the streamer's 4.3
// path, for its tests and FOUNDRY_NANITE_PERSISTENT=0).
func DisableBufferStorage() { bufferStorage = nil }

// BufferStorageAvailable reports whether BufferStorage can be called. Init
// must have run.
func BufferStorageAvailable() bool { return bufferStorage != nil }

// BufferStorage gives the buffer bound to target an immutable store of size
// bytes (data may be nil) with flags (the MAP_* and storage bits below).
func BufferStorage(target uint32, size int, data unsafe.Pointer, flags uint32) {
	C.glextCallBufferStorage(bufferStorage, C.uint(target), C.intptr_t(size), C.uintptr_t(uintptr(data)), C.uint(flags))
}

// The glBufferStorage and glMapBufferRange bits past the 4.3 binding's
// constants.
const (
	MapPersistentBit   = 0x0040
	MapCoherentBit     = 0x0080
	DynamicStorageBit  = 0x0100
	ClientStorageBit   = 0x0200
	ClientMappedBuffer = 0x00004000 // GL_CLIENT_MAPPED_BUFFER_BARRIER_BIT
)

// MultiDrawElementsIndirectCount draws up to maxDraws commands from the bound
// DRAW_INDIRECT_BUFFER at byte offset indirect, as many as the uint32 at byte
// offset drawCount in the bound PARAMETER_BUFFER says.
func MultiDrawElementsIndirectCount(mode, xtype uint32, indirect, drawCount uintptr, maxDraws, stride int32) {
	C.glextCallMDEIC(mdeic, C.uint(mode), C.uint(xtype), C.uintptr_t(indirect), C.intptr_t(drawCount), C.int(maxDraws), C.int(stride))
}

// ParameterBuffer is GL_PARAMETER_BUFFER (0x80EE), the binding the draw count
// is read from — not in the 4.3 binding's constants.
const ParameterBuffer = 0x80EE
