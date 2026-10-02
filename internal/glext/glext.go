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
*/
import "C"

import (
	"strings"
	"unsafe"

	"github.com/go-gl/gl/v4.3-core/gl"
)

var (
	tried bool
	mdeic unsafe.Pointer
)

// Init looks the entry points up. Call with a context current; calling it
// again does nothing.
func Init() {
	if tried {
		return
	}
	tried = true
	if !hasIndirectCount() {
		return
	}
	for _, name := range []string{"glMultiDrawElementsIndirectCount", "glMultiDrawElementsIndirectCountARB"} {
		cname := C.CString(name)
		p := C.glextProc(cname)
		C.free(unsafe.Pointer(cname))
		if p != nil {
			mdeic = p
			return
		}
	}
}

// hasIndirectCount is whether the context offers the draw: GL 4.6, or the
// extension.
func hasIndirectCount() bool {
	var major, minor int32
	gl.GetIntegerv(gl.MAJOR_VERSION, &major)
	gl.GetIntegerv(gl.MINOR_VERSION, &minor)
	if major > 4 || (major == 4 && minor >= 6) {
		return true
	}
	var n int32
	gl.GetIntegerv(gl.NUM_EXTENSIONS, &n)
	for i := int32(0); i < n; i++ {
		if strings.EqualFold(gl.GoStr(gl.GetStringi(gl.EXTENSIONS, uint32(i))), "GL_ARB_indirect_parameters") {
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
func Disable() { mdeic = nil }

// MultiDrawElementsIndirectCount draws up to maxDraws commands from the bound
// DRAW_INDIRECT_BUFFER at byte offset indirect, as many as the uint32 at byte
// offset drawCount in the bound PARAMETER_BUFFER says.
func MultiDrawElementsIndirectCount(mode, xtype uint32, indirect, drawCount uintptr, maxDraws, stride int32) {
	C.glextCallMDEIC(mdeic, C.uint(mode), C.uint(xtype), C.uintptr_t(indirect), C.intptr_t(drawCount), C.int(maxDraws), C.int(stride))
}

// ParameterBuffer is GL_PARAMETER_BUFFER (0x80EE), the binding the draw count
// is read from — not in the 4.3 binding's constants.
const ParameterBuffer = 0x80EE
