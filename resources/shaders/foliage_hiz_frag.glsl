#version 430

// foliage_hiz_frag.glsl — one level of the foliage cull's Hi-Z pyramid
// (render/foliage_gpu.go): each texel the furthest depth of the 2x2 (3x3
// where the level above is odd) under it, so a sphere nearer than a texel's
// depth is nearer than everything that texel covers.

uniform sampler2D src;
uniform ivec2 srcSize;

out float outDepth;

void main() {
    ivec2 p = ivec2(gl_FragCoord.xy) * 2;
    float d = 0.0;
    for (int y = 0; y < 3; y++) {
        for (int x = 0; x < 3; x++) {
            // The third row and column only where the source has one left
            // over, which the level below would otherwise never see.
            if ((x == 2 && (srcSize.x & 1) == 0) || (y == 2 && (srcSize.y & 1) == 0)) {
                continue;
            }
            ivec2 q = min(p + ivec2(x, y), srcSize - 1);
            d = max(d, texelFetch(src, q, 0).r);
        }
    }
    outDepth = d;
}
