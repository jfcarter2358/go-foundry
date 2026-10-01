#version 430

// foliage_hiz_vert.glsl — a triangle over the whole target, from
// gl_VertexID alone (render/foliage_gpu.go builds the Hi-Z pyramid with it).

void main() {
    vec2 p = vec2(gl_VertexID == 1 ? 3.0 : -1.0, gl_VertexID == 2 ? 3.0 : -1.0);
    gl_Position = vec4(p, 0.0, 1.0);
}
