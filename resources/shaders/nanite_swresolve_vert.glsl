#version 430

// nanite_swresolve_vert.glsl — the compute rasteriser's resolve covers the
// screen box of the pixels it wrote (render/nanite_swraster.go; Phase 5),
// not the screen: swBox, which the rasteriser's workgroups gathered, read
// back through a buffer texture. An empty box (nothing written: max < min)
// puts the quad off screen.

uniform isamplerBuffer swBox;
uniform ivec2 viewSize;

const vec2 corners[6] = vec2[6](vec2(0, 0), vec2(1, 0), vec2(1, 1), vec2(0, 0), vec2(1, 1), vec2(0, 1));

void main() {
    ivec4 b = texelFetch(swBox, 0);
    if (b.z < b.x || b.w < b.y) {
        gl_Position = vec4(2.0, 2.0, 2.0, 1.0);
        return;
    }
    vec2 p = mix(vec2(b.xy), vec2(b.zw + 1), corners[gl_VertexID]) / vec2(viewSize);
    gl_Position = vec4(p * 2.0 - 1.0, 0.0, 1.0);
}
