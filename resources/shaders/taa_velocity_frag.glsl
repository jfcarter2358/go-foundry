#version 430

// taa_velocity_frag.glsl — the frame's motion vectors, as the TAA resolve
// reads them (render/taa.go).
//
// Every pixel's motion is where the surface it shows was LAST frame, in
// texture coordinates, subtracted from where it is now. Two sources:
//
//   - A moving object — an entity, a chunk a frame carries, a brush a script
//     moves — rasterised its own into objectMotion (taa_object_*.glsl),
//     from its matrix now and its matrix last frame.
//   - Everything else stood still, so only the camera moved: the surface's
//     world position is read back out of the depth through this frame's
//     inverse (jittered — the depth was rasterised through the jittered
//     projection), and projected through last frame's camera. Both
//     projections in the subtraction are unjittered: the jitter is the
//     sampling pattern, not motion, and leaving it in would shake the
//     history by a sub-pixel every frame.
//
// Dilated: each pixel takes the motion of the NEAREST surface in its 3x3
// neighbourhood. An edge's pixels are a mix of the near thing and the far
// one, and following the far one's motion drags the near one's silhouette
// through the history as a trail; the near one's is what makes an edge stay
// sharp as it moves.

in vec2 vUV;

uniform sampler2D sceneDepth;
uniform sampler2D objectMotion;
uniform mat4 invViewProj;     // this frame, jittered
uniform mat4 viewProj;        // this frame, unjittered
uniform mat4 prevViewProj;    // last frame, unjittered
uniform ivec2 size;

// render.taaNoObjectMotion: the clear value of objectMotion, "the camera's".
#define NO_OBJECT_MOTION 16384.0

out vec2 outMotion;

void main() {
    ivec2 p = ivec2(gl_FragCoord.xy);
    float best = 2.0;
    ivec2 bp = p;
    for (int dy = -1; dy <= 1; dy++) {
        for (int dx = -1; dx <= 1; dx++) {
            ivec2 q = clamp(p + ivec2(dx, dy), ivec2(0), size - 1);
            float d = texelFetch(sceneDepth, q, 0).r;
            if (d < best) {
                best = d;
                bp = q;
            }
        }
    }
    vec2 om = texelFetch(objectMotion, bp, 0).xy;
    if (om.x < NO_OBJECT_MOTION * 0.5) {
        outMotion = om;
        return;
    }
    vec2 ndc = (vec2(bp) + 0.5) / vec2(size) * 2.0 - 1.0;
    vec4 w = invViewProj * vec4(ndc, best * 2.0 - 1.0, 1.0);
    w /= w.w;
    vec4 c = viewProj * w;
    vec4 pr = prevViewProj * w;
    outMotion = (c.xy / c.w - pr.xy / pr.w) * 0.5;
}
