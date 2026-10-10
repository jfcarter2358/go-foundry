#version 430

// volume_vert.glsl — the geometry every volumetric program draws, with no
// vertex buffer behind it.
//
// Two shapes, both generated from gl_VertexID, so a draw needs only a bound
// vertex array and no attributes — which is what lets a secondary window's
// preview draw through these programs with a VAO of its own (see
// VolumeTargets): VAOs are never shared between contexts, but a VAO with
// nothing in it is trivially made in any of them.
//
//   volFullscreen = 0: the unit box [-1, 1]^3, 36 vertices, carried into the
//   world by proxyModel. This is a volume's PROXY — it exists only to make
//   the fragment stage run on the pixels the volume could cover. The fragment
//   stage casts its own ray and never reads a position from here. Drawn
//   front-face-culled, so exactly one fragment lands per covered pixel, from
//   outside the box or in.
//
//   volFullscreen = 1: one triangle covering the viewport, for the passes that
//   touch every pixel (the depth downsample, the composite, the shadow bake,
//   and the sky's deck, which has no box to draw).

uniform mat4 projView;
uniform mat4 proxyModel;
uniform int  volFullscreen;

// Corner i's bits are its (x, y, z) signs.
const vec3 volCorners[8] = vec3[](
    vec3(-1, -1, -1), vec3( 1, -1, -1), vec3(-1,  1, -1), vec3( 1,  1, -1),
    vec3(-1, -1,  1), vec3( 1, -1,  1), vec3(-1,  1,  1), vec3( 1,  1,  1)
);

// Two triangles per face, wound counter-clockwise seen from OUTSIDE the box —
// the engine's front face — so culling front faces keeps the far side.
const int volBoxIndex[36] = int[](
    1, 3, 7,  1, 7, 5,   // +X
    0, 6, 2,  0, 4, 6,   // -X
    2, 6, 7,  2, 7, 3,   // +Y
    0, 1, 5,  0, 5, 4,   // -Y
    4, 5, 7,  4, 7, 6,   // +Z
    0, 2, 3,  0, 3, 1    // -Z
);

void main() {
    if (volFullscreen != 0) {
        vec2 p = vec2(gl_VertexID == 1 ? 3.0 : -1.0, gl_VertexID == 2 ? 3.0 : -1.0);
        gl_Position = vec4(p, 0.0, 1.0);
        return;
    }
    gl_Position = projView * proxyModel * vec4(volCorners[volBoxIndex[gl_VertexID]], 1.0);
}
