package render

// editor_scene_frame.go — the editor's 3D view drawn the way play draws it.
//
// Until 2026-10-12 the editor's own view always took the forward path: no
// scene target, so no visibility buffer, no virtual shadow map (the sun fell
// back to its three 2048² cascades) and no TAA, straight into the window's
// 8x multisampled back buffer. That was cheap for brush levels. For a level
// of dense cooked Nanite meshes it is not: the forward path shades every
// micro-triangle's quads at every overlap, and the cascades walk the whole
// scene at a texel-sized cut. On the generated desert canyon (6,607 cooked
// placements) the editor's view cost 88–171 ms of GPU on the GTX 1070 at
// 2560x1080 against 22–48 ms for the same view in play
// (docs/canyon/PERF.md).
//
// So with EditorSceneFrame on (the default) the editor's filled view takes
// the scene-target path too: visibility buffer, VSM, TAA, the HDR curve —
// the picture the author will get in play. Two things the forward path gave
// for free are put back:
//
//   - The editor draws its gizmos, selection outlines and handles AFTER
//     DrawWorld, straight into the window, depth-tested against the world.
//     The scene target's depth is therefore written into the window's depth
//     buffer by the output pass (taa_output_frag.glsl's writeDepth), every
//     sample of each pixel, so those overlays hide behind the world exactly
//     as before.
//   - Wireframe shading stays on the forward path (sceneFrameWanted): an
//     outline pass through a visibility buffer means nothing.
//
// FOUNDRY_EDITOR_SCENE_FRAME=0 puts the editor back on the forward path, for
// A/B runs and in case a driver mishandles the depth copy.

import "os"

// EditorSceneFrame is whether the editor's filled 3D view draws through the
// scene target (visibility buffer, VSM, TAA) like play.
var EditorSceneFrame = os.Getenv("FOUNDRY_EDITOR_SCENE_FRAME") != "0"
