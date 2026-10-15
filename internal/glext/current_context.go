package glext

// current_context.go — the GL context current on this thread, as an integer.
//
// GL names belong to the context that made them. A renderer global that
// outlives its context (render/nanite_vis_overflow.go's fenced readback)
// remembers which context it built its objects in and forgets them, without
// a GL call, when a frame comes from another. SDL's binding has no
// SDL_GL_GetCurrentContext, so this asks the window system directly — the
// same layer glext's proc lookup already uses: wglGetCurrentContext,
// CGLGetCurrentContext, glXGetCurrentContext. Under an EGL-only context the
// GLX answer is 0 for every context, which reads as "always the same one":
// the check degrades to off, never to a false change.

/*
#include <stdint.h>
#if defined(_WIN32)
	#define WIN32_LEAN_AND_MEAN 1
	#include <windows.h>
	static uintptr_t glextCurrentContext(void) { return (uintptr_t)wglGetCurrentContext(); }
#elif defined(__APPLE__)
	#include <OpenGL/OpenGL.h>
	static uintptr_t glextCurrentContext(void) { return (uintptr_t)CGLGetCurrentContext(); }
#else
	#include <GL/glx.h>
	static uintptr_t glextCurrentContext(void) { return (uintptr_t)glXGetCurrentContext(); }
#endif
*/
import "C"

// CurrentContext is the GL context current on the calling thread (0 when
// none is, or when the window system cannot say).
func CurrentContext() uintptr { return uintptr(C.glextCurrentContext()) }
