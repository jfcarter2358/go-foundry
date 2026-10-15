package render

// gl_context_owner.go — which GL context is current, as a comparable value.
//
// GL names (buffers, programs, fences) belong to the context that made them.
// A renderer global that outlives its context — the visibility buffer's
// overflow readback is the first that checks (nanite_vis_overflow.go) —
// records the context it built its objects in, and when a frame comes from a
// different one it forgets them without a GL call rather than querying or
// deleting names that are not the current context's.

import "foundry/internal/glext"

// currentGLContext is the window system's current context for this thread
// (glext.CurrentContext; 0 when none, or when it cannot say). A variable so
// a test can stand in a context change.
var currentGLContext = glext.CurrentContext
