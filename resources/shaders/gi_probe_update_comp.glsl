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
// gi_sdf_common.glsl and light_model.glsl (for giSlot/giTile and the GI
// uniforms) are spliced in ahead of this file.

layout(local_size_x = 64) in;

layout(rgba16f, binding = 0) uniform image2D probeImage;
// The probe's nearest surface, where one is within half a spacing: the normal
// facing the probe (xyz) and the distance to it (w). Zero where none is close.
layout(rgba16f, binding = 1) writeonly uniform image2D surfaceImage;

uniform int   cascade;
uniform ivec3 prevMin;   // the cascade's first probe index last frame
uniform int   resetAll;  // 1: treat every probe as fresh

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
}
