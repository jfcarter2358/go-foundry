// ao_blur_comp.glsl — the AO and contact shadow's depth-aware blur, at half
// resolution (render/ao.go): a 4x4 neighbourhood, each texel weighted by how
// near its view distance is to this one's, so occlusion does not bleed off a
// railing onto the wall far behind it. What GTAO's per-pixel turn leaves of
// noise this halves; TAA's history does the rest. Its b, the distance, is
// passed through untouched for the reprojection.
//
// #version is spliced ahead of it.

layout(local_size_x = 8, local_size_y = 8) in;

layout(rgba16f, binding = 0) uniform writeonly image2D aoOut;
uniform sampler2D aoRaw;
uniform ivec2 halfSize;

void main() {
    ivec2 p = ivec2(gl_GlobalInvocationID.xy);
    if (p.x >= halfSize.x || p.y >= halfSize.y) {
        return;
    }
    vec4 c = texelFetch(aoRaw, p, 0);
    if (c.b <= 0.0) {
        imageStore(aoOut, p, c);
        return;
    }
    vec2 sum = vec2(0.0);
    float wsum = 0.0;
    for (int y = -1; y <= 2; y++) {
        for (int x = -1; x <= 2; x++) {
            ivec2 q = clamp(p + ivec2(x, y), ivec2(0), halfSize - 1);
            vec4 s = texelFetch(aoRaw, q, 0);
            float w = s.b > 0.0 ? max(1.0 - abs(s.b - c.b) / (0.05 * c.b + 0.05), 0.0) : 0.0;
            sum += s.rg * w;
            wsum += w;
        }
    }
    vec2 r = wsum > 0.0 ? sum / wsum : c.rg;
    imageStore(aoOut, p, vec4(r, c.b, 1.0));
}
