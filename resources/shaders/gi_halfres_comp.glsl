// gi_halfres_comp.glsl — the probes' light at half resolution, for the
// visibility buffer's material pass (render/gi_halfres.go).
//
// Sampling the probes is most of what a pixel of the material pass costs: up
// to sixteen probes, each a data fetch, a visibility fetch and an irradiance
// fetch, for a term that changes over metres. So in a scene-target frame it is
// worked out once for each 2x2 block of pixels, here, from the frame's depth
// alone — the position the depth says, and the surface's facing from the
// depths around it — and the material pass reads it back with a depth- and
// normal-aware upsample (light_model.glsl's giHalfResIrradiance), working the
// probes out itself only where none of the four samples around a pixel is on
// its surface.
//
// Which pixel of its block a texel samples turns with TAA's frame (offset),
// so the frames between them sample all four and TAA's history averages them.
//
// light_model.glsl is spliced in ahead of this file.

layout(local_size_x = 8, local_size_y = 8) in;

// rgb the probes' irradiance, a how much of it they gave (giIrradiance's
// cover).
layout(rgba16f, binding = 0) writeonly uniform image2D irrImage;
// The sample's surface: its normal, octahedrally (xy), its plane's distance
// from the origin along that normal (z), 1 in w where there is one at all.
// Full floats: z is in world units, metres from wherever the level's origin
// is.
layout(rgba32f, binding = 1) writeonly uniform image2D guideImage;

uniform sampler2D sceneDepth;
uniform ivec2 fullSize;
uniform ivec2 halfSize;
uniform ivec2 offset;      // the pixel of each 2x2 block this frame samples
uniform mat4  invViewProj; // the frame's own, jitter and all

vec3 worldAt(ivec2 px, float d) {
    vec2 ndc = (vec2(px) + 0.5) / vec2(fullSize) * 2.0 - 1.0;
    vec4 w = invViewProj * vec4(ndc, d * 2.0 - 1.0, 1.0);
    return w.xyz / w.w;
}

// The neighbour along one axis that is on the same surface as p: the side
// whose depth is the nearer continuation (the smaller jump), as a world-space
// step from p. Pixels off the screen are not taken.
vec3 stepAlong(ivec2 px, vec3 p, float d, ivec2 axis) {
    ivec2 a = px + axis, b = px - axis;
    bool aIn = all(greaterThanEqual(a, ivec2(0))) && all(lessThan(a, fullSize));
    bool bIn = all(greaterThanEqual(b, ivec2(0))) && all(lessThan(b, fullSize));
    float da = aIn ? texelFetch(sceneDepth, a, 0).r : 2.0;
    float db = bIn ? texelFetch(sceneDepth, b, 0).r : 2.0;
    if (abs(da - d) <= abs(db - d) && da < 1.0) {
        return worldAt(a, da) - p;
    }
    if (db < 1.0) {
        return p - worldAt(b, db);
    }
    return vec3(0.0);
}

void main() {
    ivec2 h = ivec2(gl_GlobalInvocationID.xy);
    if (any(greaterThanEqual(h, halfSize))) {
        return;
    }
    ivec2 px = min(h * 2 + offset, fullSize - 1);
    float d = texelFetch(sceneDepth, px, 0).r;
    if (d >= 1.0) {
        // The sky: nothing to light.
        imageStore(irrImage, h, vec4(0.0));
        imageStore(guideImage, h, vec4(0.0));
        return;
    }
    vec3 p = worldAt(px, d);
    vec3 n = cross(stepAlong(px, p, d, ivec2(1, 0)), stepAlong(px, p, d, ivec2(0, 1)));
    if (dot(n, n) < 1e-20) {
        imageStore(irrImage, h, vec4(0.0));
        imageStore(guideImage, h, vec4(0.0));
        return;
    }
    n = normalize(n);
    // Toward the eye, as the shading's normal is on whichever side of a
    // surface is seen.
    if (dot(n, giCamPos - p) < 0.0) {
        n = -n;
    }
    float cover;
    vec3 irr = giIrradiance(p, n, cover);
    imageStore(irrImage, h, vec4(irr, cover));
    imageStore(guideImage, h, vec4(giOctEncode(n), dot(n, p), 1.0));
}
