#version 430

// volume_composite_frag.glsl — the volume target, put over the frame.
//
// At full resolution this is a copy. At half resolution it is a DEPTH-AWARE
// upsample: each screen pixel takes the four target texels around it, weighted
// bilinearly as usual, and then re-weighted by how closely each texel's scene
// distance matches this pixel's own. A plain bilinear upsample smears a cloud
// half a texel over the silhouette of whatever stands in front of it, which
// reads as a halo round every tree against the sky; this makes the texels that
// were marched against the tree's depth speak for the tree's pixels, and the
// ones marched against the sky speak for the sky's.
//
// Output is the target's premultiplied colour, blended ONE, ONE_MINUS_SRC_ALPHA.

uniform sampler2D volColor;       // premultiplied, target resolution
uniform sampler2D volDist;        // scene distance, target resolution
uniform sampler2D volSceneDepth;  // the depth copy, viewport resolution
uniform ivec2     volLowSize;
uniform ivec2     volFullSize;
uniform vec2      volViewportOrigin;
uniform int       volScale;
uniform mat4      volInvProjView;
uniform vec3      volCamPos;

out vec4 outColor;

#define VOL_NO_SURFACE 1e9

void main() {
    vec2 px = gl_FragCoord.xy - volViewportOrigin;
    ivec2 ipx = clamp(ivec2(px), ivec2(0), volFullSize - 1);
    if (volScale <= 1) {
        outColor = texelFetch(volColor, ipx, 0);
        return;
    }

    float d = texelFetch(volSceneDepth, ipx, 0).r;
    float dist = VOL_NO_SURFACE;
    if (d < 1.0) {
        vec2 uv = (vec2(ipx) + 0.5) / vec2(volFullSize);
        vec4 w = volInvProjView * vec4(uv * 2.0 - 1.0, d * 2.0 - 1.0, 1.0);
        dist = distance(w.xyz / w.w, volCamPos);
    }

    vec2 lp = px / float(volScale) - 0.5;
    ivec2 i0 = ivec2(floor(lp));
    vec2 f = lp - vec2(i0);

    vec4 sum = vec4(0.0);
    float wSum = 0.0;
    for (int y = 0; y < 2; ++y) {
        for (int x = 0; x < 2; ++x) {
            ivec2 q = clamp(i0 + ivec2(x, y), ivec2(0), volLowSize - 1);
            float bilinear = (x == 0 ? 1.0 - f.x : f.x) * (y == 0 ? 1.0 - f.y : f.y);
            float ld = texelFetch(volDist, q, 0).r;
            // Relative, so a metre means a lot next to the camera and nothing a
            // kilometre out. Two open-sky texels match exactly.
            float rel = abs(ld - dist) / max(min(ld, dist), 1.0);
            float w = (bilinear + 1e-3) / (0.02 + rel);
            sum += texelFetch(volColor, q, 0) * w;
            wSum += w;
        }
    }
    outColor = sum / max(wSum, 1e-6);
}
