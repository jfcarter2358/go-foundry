// ao_comp.glsl — the frame's ambient occlusion and the sun's contact shadow,
// at half resolution, from the depth the opaque world left (render/ao.go;
// docs/lighting/PLAN.md, phases 3 and 6).
//
// For each half-resolution texel: the surface at the middle of its 2x2 block,
// as a view-space point and a normal reconstructed from the depth around it;
// then
//
//   GTAO (Jimenez et al., "Practical Real-Time Strategies for Accurate
//   Indirect Occlusion"): SLICES great circles about the view vector, the
//   highest horizon each way along each found by STEPS depth samples out to
//   aoRadius metres, the visible arc between the two horizons integrated
//   against the cosine. The slices turn per pixel and per frame, and TAA's
//   history averages the turns;
//
//   the GI distance field's occlusion (when giSdfLevels > 0): four samples
//   along the normal, a metre and a half out, for the occluders too far off or
//   behind something on screen for GTAO to find;
//
//   the sun's contact shadow: a short march toward it through the depth, for
//   the small shadows a shadow map's texels are too coarse for.
//
// Out: r ambient occlusion, g the sun's contact visibility, b the surface's
// view-space distance (clip w), which is how the next frame's reprojection
// knows the texel still describes what it is shading.
//
// #version and, when the field is there, gi_sdf_common.glsl are spliced
// ahead of it.

layout(local_size_x = 8, local_size_y = 8) in;

layout(rgba16f, binding = 0) uniform writeonly image2D aoOut;

uniform sampler2D sceneDepth;  // full resolution, [0, 1]
uniform mat4  aoProj;          // the frame's projection (jitter and all)
uniform mat4  aoInvProj;
uniform mat3  aoViewToWorld;   // rotation part of the inverse view
uniform vec3  aoCamPos;
uniform ivec2 fullSize;
uniform ivec2 halfSize;
uniform float aoRadius;        // metres
uniform int   aoFrame;         // turns the slices
uniform int   aoSlices;
uniform int   aoSteps;
uniform int   sdfAO;           // sample the GI field too
uniform vec3  contactDir;      // view space, toward the sun; zero for none
uniform float contactLength;   // metres
uniform int   contactSteps;

#define PI 3.14159265

vec3 viewPosAt(vec2 uv) {
    float d = textureLod(sceneDepth, uv, 0.0).r;
    vec4 v = aoInvProj * vec4(uv * 2.0 - 1.0, d * 2.0 - 1.0, 1.0);
    return v.xyz / v.w;
}

vec2 uvOf(vec3 v) {
    vec4 c = aoProj * vec4(v, 1.0);
    return c.xy / c.w * 0.5 + 0.5;
}

float ign(vec2 p) {
    return fract(52.9829189 * fract(dot(p, vec2(0.06711056, 0.00583715))));
}

float fastAcos(float x) {
    float ax = abs(x);
    float r = (-0.156583 * ax + 1.570796) * sqrt(max(1.0 - ax, 0.0));
    return x >= 0.0 ? r : PI - r;
}

void main() {
    ivec2 hp = ivec2(gl_GlobalInvocationID.xy);
    if (hp.x >= halfSize.x || hp.y >= halfSize.y) {
        return;
    }
    // The centre of the block's first full-resolution pixel — a pixel centre,
    // not the block's middle: that is a corner between four pixels, and the
    // nearest-texel depth reads flip between them across the screen in
    // stripes.
    vec2 px = vec2(hp * 2) + 0.5;
    vec2 texel = 1.0 / vec2(fullSize);
    vec2 uv = px * texel;
    float depth = textureLod(sceneDepth, uv, 0.0).r;
    if (depth >= 1.0) {
        imageStore(aoOut, hp, vec4(1.0, 1.0, 0.0, 1.0)); // sky: nothing to occlude
        return;
    }
    vec3 P = viewPosAt(uv);

    // The normal, from whichever neighbour on each axis is on the same
    // surface (the nearer in depth), so an edge does not bend it.
    vec3 pr = viewPosAt(uv + vec2(texel.x, 0.0)) - P;
    vec3 pl = P - viewPosAt(uv - vec2(texel.x, 0.0));
    vec3 pu = viewPosAt(uv + vec2(0.0, texel.y)) - P;
    vec3 pd = P - viewPosAt(uv - vec2(0.0, texel.y));
    vec3 dx = abs(pr.z) < abs(pl.z) ? pr : pl;
    vec3 dy = abs(pu.z) < abs(pd.z) ? pu : pd;
    vec3 N = normalize(cross(dx, dy));
    vec3 V = normalize(-P);
    if (dot(N, V) < 0.0) {
        N = -N;
    }

    // ---- GTAO ----
    float viewDist = -P.z;
    float radiusPx = aoRadius * aoProj[1][1] * 0.5 * float(fullSize.y) / max(viewDist, 1e-3);
    float ao = 1.0;
    if (radiusPx > 1.0) {
        radiusPx = min(radiusPx, 96.0);
        float noise = ign(vec2(hp)) ;
        float turn = fract(noise + float(aoFrame) * 0.618034);
        float jitter = fract(noise * 7.13 + float(aoFrame) * 0.3819);
        float vis = 0.0;
        for (int s = 0; s < aoSlices; s++) {
            float phi = (float(s) + turn) * PI / float(aoSlices);
            vec2 dir = vec2(cos(phi), sin(phi));
            vec3 dirV = vec3(dir, 0.0);
            vec3 ortho = dirV - dot(dirV, V) * V;
            vec3 axis = normalize(cross(ortho, V));
            vec3 projN = N - axis * dot(N, axis);
            float projLen = length(projN);
            float cosN = clamp(dot(projN, V) / max(projLen, 1e-5), 0.0, 1.0);
            float n = sign(dot(ortho, projN)) * fastAcos(cosN);
            float low0 = cos(n + PI * 0.5);
            float low1 = cos(n - PI * 0.5);
            float h0c = low0, h1c = low1;
            for (int k = 0; k < aoSteps; k++) {
                float t = (float(k) + jitter) / float(aoSteps);
                t = t * t * radiusPx + 1.0;
                vec2 off = dir * t * texel;
                vec3 d0 = viewPosAt(uv + off) - P;
                vec3 d1 = viewPosAt(uv - off) - P;
                float l0 = length(d0), l1 = length(d1);
                float w0 = clamp(1.0 - l0 * l0 / (aoRadius * aoRadius), 0.0, 1.0);
                float w1 = clamp(1.0 - l1 * l1 / (aoRadius * aoRadius), 0.0, 1.0);
                h0c = max(h0c, mix(low0, dot(d0 / max(l0, 1e-5), V), w0));
                h1c = max(h1c, mix(low1, dot(d1 / max(l1, 1e-5), V), w1));
            }
            float h0 = fastAcos(h0c);
            float h1 = -fastAcos(h1c);
            h0 = n + clamp(h0 - n, -PI * 0.5, PI * 0.5);
            h1 = n + clamp(h1 - n, -PI * 0.5, PI * 0.5);
            float sinN = sin(n);
            float a0 = (cosN + 2.0 * h0 * sinN - cos(2.0 * h0 - n)) * 0.25;
            float a1 = (cosN + 2.0 * h1 * sinN - cos(2.0 * h1 - n)) * 0.25;
            vis += projLen * (a0 + a1);
        }
        ao = clamp(vis / float(aoSlices), 0.0, 1.0);
    }

#ifdef AO_SDF
    // ---- the GI field's occlusion ----
    if (sdfAO != 0) {
        vec3 Pw = aoCamPos + aoViewToWorld * P;
        vec3 Nw = aoViewToWorld * N;
        float occ = 0.0;
        const float dists[4] = float[4](0.15, 0.4, 0.85, 1.6);
        const float weights[4] = float[4](0.4, 0.3, 0.2, 0.1);
        for (int i = 0; i < 4; i++) {
            float voxel;
            float s = giSdf(Pw + Nw * dists[i], voxel);
            if (voxel < 0.0) {
                break; // out of the field
            }
            occ += weights[i] * clamp((dists[i] - s) / dists[i], 0.0, 1.0);
        }
        ao *= clamp(1.0 - occ * 1.5, 0.0, 1.0);
    }
#endif

    // ---- the sun's contact shadow ----
    float contact = 1.0;
    if (contactSteps > 0 && dot(contactDir, contactDir) > 0.5 && dot(N, contactDir) > 0.0) {
        float jitter = fract(ign(vec2(hp) + 17.0) + float(aoFrame) * 0.618034);
        vec3 start = P + N * (0.004 * viewDist + 0.01);
        float stepLen = contactLength / float(contactSteps);
        float thickness = max(0.15, viewDist * 0.01);
        for (int k = 0; k < contactSteps; k++) {
            vec3 S = start + contactDir * (stepLen * (float(k) + jitter));
            vec2 suv = uvOf(S);
            if (any(lessThan(suv, vec2(0.0))) || any(greaterThan(suv, vec2(1.0)))) {
                break;
            }
            float sceneZ = viewPosAt(suv).z;
            float behind = sceneZ - S.z; // > 0: the march has gone behind the surface there
            if (behind > 0.02 * stepLen + 0.002 * viewDist && behind < thickness) {
                contact = 0.0;
                break;
            }
        }
        // Gone with distance: far off, the shadow map has it.
        contact = mix(contact, 1.0, smoothstep(20.0, 35.0, viewDist));
    }

    imageStore(aoOut, hp, vec4(ao, contact, viewDist, 1.0));
}
