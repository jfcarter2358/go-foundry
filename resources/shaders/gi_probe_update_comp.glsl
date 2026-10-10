// gi_probe_update_comp.glsl — placing the GI probes (render/gi.go).
//
// One invocation per probe of one cascade, every frame, before the trace. A
// probe sits on its grid point unless that point is in or against geometry,
// in which case it is pushed out along the distance field's gradient — up to
// most of half a spacing, so it never crosses into its neighbour's cell. One
// that is still buried after that is switched off, and the shading ignores
// it. A probe whose slot has just scrolled into range is flagged fresh, so
// its first trace replaces whatever the slot held instead of blending with it.
//
// A probe further from every surface than a cell's diagonal is RESTING: no
// surface point has it among its eight, so only what floats in the air (a
// sprite, a particle) ever reads it, and the trace updates it a fraction as
// often. In an open level that is most of the grid.
//
// It also decides which probes the trace takes this frame — a live one whose
// turn it is: one in `period` frames, a resting one in `restPeriod` — and
// lists them for it (see gi_probe_trace_comp.glsl, which is dispatched over
// the list rather than over every probe).
//
// GI_PROBES_PER_CASCADE (a define), gi_sdf_common.glsl and light_model.glsl
// (for giSlot/giTile and the GI uniforms) are spliced in ahead of this file.

layout(local_size_x = 64) in;

layout(rgba16f, binding = 0) uniform image2D probeImage;
// The probe's nearest surface, where one is within half a spacing: the normal
// facing the probe (xyz) and the distance to it (w). Zero where none is close.
layout(rgba16f, binding = 1) writeonly uniform image2D surfaceImage;
// Both of the above in one texel, as the shading reads them (light_model.glsl's
// giProbePacked): probeImage's offset and state, then surfaceImage's plane,
// each pair of halves packed into a word. Both images are half floats
// already, so the packing loses nothing.
layout(rgba32ui, binding = 2) writeonly uniform uimage2D packedImage;

// The trace's list (probeList, at cascade * GI_PROBES_PER_CASCADE) and its
// indirect dispatches, three words a cascade: the count of probes listed,
// then 1, 1. render/gi.go zeroes the counts before this pass runs.
layout(std430, binding = 0) writeonly buffer ProbeList {
    uint probeList[];
};
layout(std430, binding = 1) buffer ProbeDispatch {
    uint probeDispatch[];
};

uniform int   cascade;
uniform ivec3 prevMin;   // the cascade's first probe index last frame
uniform int   resetAll;  // 1: treat every probe as fresh
uniform int   frameIndex;
uniform int   period;     // the trace takes one probe in `period` each frame
uniform int   restPeriod; // and one resting probe in `restPeriod`

// The state bits, as render/gi.go has them.
#define STATE_ACTIVE 1.0
#define STATE_FRESH 2.0
#define STATE_RESTING 4.0

void main() {
    int total = giDims.x * giDims.y * giDims.z;
    int i = int(gl_GlobalInvocationID.x);
    if (i >= total) {
        return;
    }
    ivec3 slot = ivec3(i % giDims.x, (i / giDims.x) % giDims.y, i / (giDims.x * giDims.y));
    ivec3 lo = ivec3(giCascade[cascade].xyz);
    float s = giCascade[cascade].w;
    // The world probe in this slot: the one in range that wraps to it.
    ivec3 wi = lo + giSlot(slot - lo);
    ivec2 tile = giTile(slot, cascade);

    float old = imageLoad(probeImage, tile).w;
    bool fresh = resetAll != 0 || mod(old, 4.0) >= STATE_FRESH
        || any(lessThan(wi, prevMin)) || any(greaterThanEqual(wi, prevMin + giDims));

    vec3 home = vec3(wi) * s;
    vec3 offset = vec3(0.0);
    bool alive = true;
    float voxel;
    float d = giSdf(home, voxel);
    float want = 0.25 * s;
    if (voxel > 0.0 && d < want) {
        vec3 n = giSdfNormal(home, voxel);
        offset = n * min(want - d, 0.45 * s);
        float d2 = giSdf(home + offset, voxel);
        alive = voxel < 0.0 || d2 > 0.05 * s;
    }

    float clear = giSdf(home + offset, voxel);
    bool resting = alive && (voxel < 0.0 || clear > 1.8 * s);

    float state = (alive ? STATE_ACTIVE : 0.0) + (fresh ? STATE_FRESH : 0.0)
        + (resting ? STATE_RESTING : 0.0);
    imageStore(probeImage, tile, vec4(offset, state));

    // A probe this close to a surface sees it at such a grazing angle that
    // its distance moments cannot tell the near side of a thin floor from
    // the far: the probe just under the hangar's floor, whose rays run on
    // under it a long way, passed the shading's visibility test for points
    // on top of it and lit every wall foot and the underside of the ship
    // with the void below. The plane lets the shading refuse it outright.
    vec4 plane = vec4(0.0);
    if (alive && voxel > 0.0 && clear < 0.5 * s) {
        vec3 n = giSdfNormal(home + offset, voxel);
        plane = vec4(n, max(clear, 0.0));
    }
    imageStore(surfaceImage, tile, plane);
    imageStore(packedImage, tile, uvec4(packHalf2x16(offset.xy), packHalf2x16(vec2(offset.z, state)),
        packHalf2x16(plane.xy), packHalf2x16(plane.zw)));

    // Is it the trace's this frame? A buried probe has nothing to trace
    // from; a fresh one is taken at the live rate whatever it rests as.
    // (The trace used to ask this itself, of every probe, from what this pass
    // had just written.)
    int every = (resting && !fresh) ? restPeriod : period;
    if (alive && (i + frameIndex) % every == 0) {
        uint at = atomicAdd(probeDispatch[cascade * 3], 1u);
        probeList[cascade * GI_PROBES_PER_CASCADE + int(at)] = uint(i);
    }
}
