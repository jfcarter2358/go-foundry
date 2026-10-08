#version 430

// nfoliage_voxresolve_frag.glsl — the Nanite foliage compute rasteriser's
// resolve (render/nfoliage_voxraster.go). The 64-bit extensions, the
// defines, foliage_params.glsl, nfoliage_common.glsl, the lighting and
// nfoliage_voxel.glsl are the Go side's, after the #version line.
//
// Drawn over the screen box the rasteriser wrote in (Phase 5's
// nanite_swresolve_vert.glsl), with the depth test on: each pixel the
// rasteriser wrote nearer than what the hardware drew takes the voxel's
// depth, its shade (nfVoxelShade, the ray march's own) and its motion. Every
// word is cleared as it is read, which readies the buffer for the next frame.

layout(std430, binding = 14) coherent buffer VisFoliage {
    uint64_t vis[];
};

// The reference list, as (item, brick) texels.
uniform usamplerBuffer nfRefs;
uniform ivec2 viewSize;
uniform int coverageDither;
uniform mat4 motionViewProj;
uniform mat4 motionPrevViewProj;
uniform int writeMotion;

layout(location = 0) out vec4 outputColor;
layout(location = 2) out vec2 outMotion;

void main() {
    ivec2 p = ivec2(gl_FragCoord.xy);
    uint i = uint(p.y) * uint(viewSize.x) + uint(p.x);
    uint64_t v = vis[i];
    if (v == uint64_t(0)) {
        discard;
    }
    vis[i] = uint64_t(0);
    uint key = uint(v >> 32);
    uint payload = uint(v & uint64_t(0xffffffffu));
    uint r = payload >> 6u;
    int l = int(payload & 63u);
    gl_FragDepth = uintBitsToFloat(0xffffffffu - key);
    if (!nfBeginRef(texelFetch(nfRefs, int(r)).xy)) {
        discard;
    }
    int bi = nFirst + int(nWhich);
    uvec4 b1 = texelFetch(nfBricks, bi * 2 + 1);
    uvec4 vox;
    if (!nfVoxelAt(b1.x, b1.y, b1.z, l, vox)) {
        discard;
    }
    vec3 o;
    mat3 A;
    nfBrickMap(false, bi, o, A);
    mat3 Ainv = inverse(A);
    vec3 c = vec3(float(l & 3), float((l >> 2) & 3), float(l >> 4)) + 0.5;
    vec3 world = o + A * c;
    vec3 upWorld = normalize(mat3(nModel) * nUp);
    vec3 bark = shapeBark[nShape] * (0.8 + 0.4 * iAccentSeed.w);
    uvec4 seed = uvec4(uvec2(p), uint(coverageDither) + 17u, uint(bi) * 64u + uint(l));
    vec3 col = nfVoxelShade(vox, world, camPos, vec3(Ainv[0][0], Ainv[1][0], Ainv[2][0]), vec3(Ainv[0][1], Ainv[1][1], Ainv[2][1]),
                            vec3(Ainv[0][2], Ainv[1][2], Ainv[2][2]), upWorld, iColorRank.rgb, iAccentSeed.rgb, bark, seed);
    outputColor = vec4(col, 1.0);
    outMotion = vec2(16384.0);
    if (writeMotion != 0) {
        vec3 po;
        mat3 pA;
        nfBrickMap(true, bi, po, pA);
        vec4 cc = motionViewProj * vec4(world, 1.0);
        vec4 pc = motionPrevViewProj * vec4(po + pA * c, 1.0);
        outMotion = (cc.xy / cc.w - pc.xy / pc.w) * 0.5;
    }
}
