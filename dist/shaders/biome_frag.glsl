#version 410

// biome_frag.glsl — flat Gouraud output for GPU-scattered grass/rock. Lighting
// is computed per-vertex in biome_vert.glsl; here we just emit the interpolated
// color. Blades/rocks are solid (no texture), so no alpha test is needed.

in vec3 vColor;
out vec4 outputColor;

void main() {
    outputColor = vec4(vColor, 1.0);
}
