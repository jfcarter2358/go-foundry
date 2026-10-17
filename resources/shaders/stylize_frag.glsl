#version 430

// stylize_frag.glsl — a painted level's paint (render/stylize.go;
// world.Stylize, experimental): the scene target's frame with its convex
// edges highlighted, its creases washed and its raised detail dry-brushed,
// before the TAA resolve — so the history anti-aliases the paint like
// everything else, rather than a frame's jittered edges shimmering on top of
// the resolved picture.
//
// What it reads is the normal buffer the opaque world left
// (light_model.glsl's stylizePack): per pixel the geometric and the shading
// normal, and the surface's view depth. The curvature of the normals across a
// few pixels says what the surface does there — a ridge where they spread
// apart, a crease where they close up — which is screen-space ridge/valley
// detection on the real normals, not ones rebuilt from depth.
//
// A record is believed only where the depth buffer still holds its surface:
// foliage, grass and sprites draw after the opaque world without writing the
// buffer, and what they cover keeps its old record underneath them.

#include "scene_block.glsl"

in vec2 vUV;

uniform sampler2D colourSrc;
uniform usampler2D gbuf;
uniform sampler2D depthSrc;
uniform ivec2 size;
// The frame's (jittered) inverse view-projection: a pixel's world position.
uniform mat4 invViewProj;
// World to view rotation, for the normals' curvature on screen.
uniform mat3 viewRot;
// The projection's [2][2] and [3][2]: view depth from window depth.
uniform vec2 depthAB;
// Pixel widths are of a 1080-line frame, scaled by this.
uniform float pxScale;

uniform vec4  edgePaint;     // rgb the colour, a the strength
uniform float edgeWidth;     // pixels at 1080 lines
uniform vec4  washPaint;     // rgb the shade, a the strength
uniform vec4  dryPaint;      // rgb the colour, a the strength
uniform vec2  dryParams;     // x strokes per world unit, y threshold
uniform float fadeDistance;  // world units
// FOUNDRY_STYLIZE_DEBUG (render/stylize.go): 1 the records — black none,
// red one something has since covered, green a good one; 2 the paint's
// masks — red the edge, blue the wash, green the dry-brush.
uniform int debugView;

out vec4 outputColor;

struct StyPx {
    bool ok;
    vec3 geo;
    vec3 bump;
    float depth;
    float weight;
};

float styLuma(vec3 c) {
    return dot(c, vec3(0.2126, 0.7152, 0.0722));
}

vec3 styOctDecode(vec2 e) {
    vec3 n = vec3(e, 1.0 - abs(e.x) - abs(e.y));
    if (n.z < 0.0) {
        n.xy = (1.0 - abs(n.yx)) * vec2(n.x >= 0.0 ? 1.0 : -1.0, n.y >= 0.0 ? 1.0 : -1.0);
    }
    return normalize(n);
}

float sceneViewDepth(ivec2 p) {
    float z = texelFetch(depthSrc, p, 0).r * 2.0 - 1.0;
    return depthAB.y / (z + depthAB.x);
}

StyPx styFetch(ivec2 p) {
    StyPx s;
    s.ok = false;
    s.geo = vec3(0.0);
    s.bump = vec3(0.0);
    s.depth = 0.0;
    s.weight = 0.0;
    p = clamp(p, ivec2(0), size - 1);
    uvec4 g = texelFetch(gbuf, p, 0);
    if ((g.w & 0x100u) == 0u) {
        return s;
    }
    s.depth = uintBitsToFloat(g.z);
    // Covered since: something nearer drew here without a record of its own.
    if (abs(sceneViewDepth(p) - s.depth) > 0.004 * s.depth + 0.002) {
        return s;
    }
    s.geo = styOctDecode(unpackHalf2x16(g.x));
    s.bump = styOctDecode(unpackHalf2x16(g.y));
    s.weight = float(g.w & 0xffu) / 255.0;
    s.ok = true;
    return s;
}

// styCurv is the curvature of the surface at p across w pixels each way, on
// screen: positive on a ridge (the normals spread apart, as across the top of
// a convex edge), negative in a crease. A neighbour that is another surface —
// across a silhouette, nearer or much farther — counts as this one, so an
// outline is not mistaken for an edge of the shape.
float styCurv(ivec2 p, StyPx c, int w, bool bump) {
    vec3 nc = viewRot * (bump ? c.bump : c.geo);
    float tol = c.depth * (0.03 * float(w) + 0.01);
    vec3 n[4];
    ivec2 off[4] = ivec2[4](ivec2(w, 0), ivec2(-w, 0), ivec2(0, w), ivec2(0, -w));
    for (int k = 0; k < 4; k++) {
        StyPx q = styFetch(p + off[k]);
        n[k] = (q.ok && abs(q.depth - c.depth) < tol) ? viewRot * (bump ? q.bump : q.geo) : nc;
    }
    return 0.5 * ((n[0].x - n[1].x) + (n[2].y - n[3].y));
}

float styHash(vec3 p) {
    p = fract(p * 0.3183099 + 0.1);
    p *= 17.0;
    return fract(p.x * p.y * p.z * (p.x + p.y + p.z));
}

float styNoise(vec3 x) {
    vec3 i = floor(x);
    vec3 f = fract(x);
    f = f * f * (3.0 - 2.0 * f);
    return mix(mix(mix(styHash(i), styHash(i + vec3(1, 0, 0)), f.x),
                   mix(styHash(i + vec3(0, 1, 0)), styHash(i + vec3(1, 1, 0)), f.x), f.y),
               mix(mix(styHash(i + vec3(0, 0, 1)), styHash(i + vec3(1, 0, 1)), f.x),
                   mix(styHash(i + vec3(0, 1, 1)), styHash(i + vec3(1, 1, 1)), f.x), f.y), f.z);
}

// styLighten is a step up the paint pot from c: the colour brighter, drawn
// toward the paint, never darker than it was. e is the HDR exposure, so the
// paint's own lift is the same on screen whatever the scene's units.
vec3 styLighten(vec3 c, vec3 paint, float e) {
    float l = styLuma(c) * e;
    vec3 up = mix(c * 1.7, paint * (l * 1.7 + 0.12) / e, 0.5);
    return max(up, c);
}

void main() {
    ivec2 p = ivec2(gl_FragCoord.xy);
    vec4 src = texelFetch(colourSrc, p, 0);
    outputColor = src;
    StyPx c = styFetch(p);
    if (debugView == 1) {
        uvec4 g = texelFetch(gbuf, p, 0);
        outputColor = vec4((g.w & 0x100u) == 0u ? vec3(0.0) : c.ok ? vec3(0.0, 1.0, 0.0) : vec3(1.0, 0.0, 0.0), 1.0);
        return;
    }
    if (debugView == 2) {
        outputColor = vec4(0.0, 0.0, 0.0, 1.0);
    }
    if (!c.ok || c.weight <= 0.0) {
        return;
    }
    float fade = (1.0 - smoothstep(fadeDistance * 0.6, fadeDistance, c.depth)) * c.weight;
    if (fade <= 0.0) {
        return;
    }
    float e = hdrMode != 0 ? hdrExposure : 1.0;
    vec3 col = src.rgb;

    // Which way the surface faces: a painter picks out the edges the light
    // catches, and the tops of things, more than the undersides.
    vec3 toSun = numDirLights > 0 ? -normalize(dirLightDir[0]) : vec3(0.0, 1.0, 0.0);
    float facing = clamp(0.5 * c.geo.y + 0.6 * dot(c.geo, toSun) + 0.35, 0.0, 1.0);

    int w = max(1, int(edgeWidth * pxScale * 0.5 + 0.5));

    vec3 masks = vec3(0.0);

    // The wash: pooled in the creases, a little wider than the edge.
    if (washPaint.a > 0.0) {
        float valley = max(-styCurv(p, c, 2 * w, false), 0.0);
        float m = smoothstep(0.06, 0.45, valley) * washPaint.a * fade;
        masks.b = m;
        col *= mix(vec3(1.0), washPaint.rgb, clamp(m, 0.0, 1.0));
    }

    // The edge highlight: along the ridges.
    float ridge = max(styCurv(p, c, w, false), 0.0);
    if (edgePaint.a > 0.0) {
        float m = smoothstep(0.08, 0.45, ridge) * edgePaint.a * facing * fade;
        masks.r = m;
        col = mix(col, styLighten(col, edgePaint.rgb, e), clamp(m, 0.0, 1.0));
    }

    // The dry-brush: raised detail — the fine bumps of the shading normal and
    // the broad shoulders of the shape — caught by a stroke of paint dragged
    // across it, elongated along the surface.
    if (dryPaint.a > 0.0) {
        vec2 ndc = (vec2(p) + 0.5) / vec2(size) * 2.0 - 1.0;
        vec4 wp = invViewProj * vec4(ndc, texelFetch(depthSrc, p, 0).r * 2.0 - 1.0, 1.0);
        vec3 pos = wp.xyz / wp.w * dryParams.x;
        vec3 t = normalize(cross(c.geo, abs(c.geo.y) < 0.9 ? vec3(0.0, 1.0, 0.0) : vec3(1.0, 0.0, 0.0)));
        vec3 q = pos - t * dot(pos, t) * 0.75;
        float stroke = styNoise(q) * 0.65 + styNoise(q * 2.7 + 13.1) * 0.35;
        stroke = smoothstep(0.35, 0.7, stroke);
        float fine = max(styCurv(p, c, 1, true), 0.0);
        float broad = max(styCurv(p, c, 3 * w, false), 0.0);
        float raised = fine * 1.5 + broad + ridge * 0.5;
        float th = dryParams.y;
        float m = smoothstep(th, th + 0.2, raised * stroke) * dryPaint.a * facing * fade;
        masks.g = m;
        col = mix(col, styLighten(col, dryPaint.rgb, e), clamp(m, 0.0, 1.0));
    }

    if (debugView == 2) {
        outputColor = vec4(clamp(masks, 0.0, 1.0), 1.0);
        return;
    }
    outputColor = vec4(col, src.a);
}
