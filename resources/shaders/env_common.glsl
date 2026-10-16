// env_common.glsl — cube-map face texels as directions, for the environment
// passes (render/envmap.go): GL's cube-map convention (the spec's table of
// major axis, sc and tc), so a texel written at (face, uv) is the one a
// texture(cube, dir) lookup reads for dir.

#ifndef ENV_COMMON_GLSL
#define ENV_COMMON_GLSL

// envFaceDir is the unit direction through uv (0..1 across the face) of face
// (0..5: +X, -X, +Y, -Y, +Z, -Z).
vec3 envFaceDir(int face, vec2 uv) {
    vec2 st = uv * 2.0 - 1.0;
    vec3 d;
    if (face == 0) {
        d = vec3(1.0, -st.y, -st.x);
    } else if (face == 1) {
        d = vec3(-1.0, -st.y, st.x);
    } else if (face == 2) {
        d = vec3(st.x, 1.0, st.y);
    } else if (face == 3) {
        d = vec3(st.x, -1.0, -st.y);
    } else if (face == 4) {
        d = vec3(st.x, -st.y, 1.0);
    } else {
        d = vec3(-st.x, -st.y, -1.0);
    }
    return normalize(d);
}

#endif
