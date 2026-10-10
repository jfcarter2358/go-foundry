#version 430

// taa_fullscreen_vert.glsl — one triangle over the whole viewport, for the
// scene target's full-screen passes (render/taa.go, render/nanite_vis.go).
// No vertex buffer: the three corners come from gl_VertexID, so a pass binds
// an empty VAO and draws three vertices. The depth is the far plane's, which
// none of those passes tests against.

out vec2 vUV;

void main() {
    vec2 p = vec2(float((gl_VertexID << 1) & 2), float(gl_VertexID & 2));
    vUV = p;
    gl_Position = vec4(p * 2.0 - 1.0, 1.0, 1.0);
}
