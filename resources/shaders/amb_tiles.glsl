// amb_tiles.glsl — which ambient volumes a screen tile can see
// (render/amb_tiles.go; docs/adventure/PHASE_F.md).
//
// Every ambient volume in the scene block is a loop trip per pixel in the
// ambient (light_model.glsl), the fog's path through the volumes (fog.glsl)
// and two per step of the shafts' march — about 0.6 ms a volume on the
// GTX 1070 at 2560x1080. The frame's volumes are the camera's, but a camera
// in a slot canyon sees the cave's volumes ahead in its frustum while rock
// hides them. So after the opaque world has its depth, a compute pass marks
// for each screen tile the volumes whose boxes reach into what the tile can
// show — its frustum from the eye to its farthest depth — and the loops skip
// the rest. Exact: a point a tile shows, and the eye ray to it, lie in that
// frustum, so a volume outside it neither holds the one nor crosses the
// other.
//
// A point finds its tile by projecting itself (ambTileVP, the matrix the
// depth was drawn with), so the shafts' march — at its own resolution — and
// any stage can ask. ambTileOn is 0 in every program nothing opted in, and
// then every volume is in: shading exactly as before.

#ifndef AMB_TILES_GLSL
#define AMB_TILES_GLSL

uniform int   ambTileOn;
uniform mat4  ambTileVP;
uniform ivec2 ambTileCount; // tiles across, down

layout(std430, binding = 23) readonly buffer AmbTileMasks {
    uint ambTileMask[];
};

// ambVolMaskAt is the bitmask of ambient volumes (scene block order) that can
// touch point p: all of them unless the frame's tiles are on.
uint ambVolMaskAt(vec3 p) {
    if (ambTileOn == 0) {
        return 0xffffffffu;
    }
    vec4 c = ambTileVP * vec4(p, 1.0);
    if (c.w <= 1e-4) {
        return 0xffffffffu;
    }
    vec2 uv = c.xy / c.w * 0.5 + 0.5;
    ivec2 t = clamp(ivec2(floor(uv * vec2(ambTileCount))), ivec2(0), ambTileCount - 1);
    return ambTileMask[t.y * ambTileCount.x + t.x];
}

#endif
