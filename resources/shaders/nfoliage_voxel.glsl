// nfoliage_voxel.glsl — reading and shading a Nanite foliage voxel
// (render/nfoliage.go): spliced into every stage that meets one — the ray
// march (nfoliage_vox_frag.glsl), the compute rasteriser
// (nfoliage_voxraster_comp.glsl) and its resolve (nfoliage_voxresolve_frag
// .glsl) — so a voxel stops a ray and shades the same whichever drew it.
//
// A voxel record is four words (render.nfVoxel): its coverage along the
// three axes and the four body diagonals (cluster.VoxelDirs) as the chance a
// ray that way is stopped; its tone; its mean normal (snorm) and blade
// share; its accent, bark and thin shares and its atlas grey.
//
// NF_VOXEL_SHADE (where biome_light.glsl is present) adds nfVoxelShade.

uniform usamplerBuffer nfVoxels;

// cluster.VoxelDirs: the axes and the four body diagonals.
const vec3 VOX_DIRS[7] = vec3[7](
    vec3(1.0, 0.0, 0.0), vec3(0.0, 1.0, 0.0), vec3(0.0, 0.0, 1.0),
    vec3(0.57735027, 0.57735027, 0.57735027), vec3(0.57735027, 0.57735027, -0.57735027),
    vec3(0.57735027, -0.57735027, 0.57735027), vec3(-0.57735027, 0.57735027, 0.57735027));

// nfVoxelCoverage is the chance a ray along unit direction dn (in the
// brick's voxel frame) is stopped in a voxel: its seven directional chances,
// the nearest directions weighted most.
float nfVoxelCoverage(uvec4 vox, vec3 dn) {
    float c[7] = float[7](
        float(vox.x & 0xffu), float((vox.x >> 8u) & 0xffu), float((vox.x >> 16u) & 0xffu), float(vox.x >> 24u),
        float(vox.y & 0xffu), float((vox.y >> 8u) & 0xffu), float((vox.y >> 16u) & 0xffu));
    float sum = 0.0, wsum = 0.0;
    for (int k = 0; k < 7; k++) {
        float a = abs(dot(dn, VOX_DIRS[k]));
        float a2 = a * a;
        float a4 = a2 * a2;
        float w = a4 * a4;
        sum += w * c[k];
        wsum += w;
    }
    return sum / max(wsum, 1e-6) / 255.0;
}

uint nfHash(uvec4 v) {
    uint h = v.x * 0x8da6b343u ^ v.y * 0xd8163841u ^ v.z * 0xcb1ab31fu ^ v.w * 0x165667b1u;
    h ^= h >> 15;
    h *= 0x2c1b3c6du;
    h ^= h >> 12;
    h *= 0x297a2d39u;
    h ^= h >> 15;
    return h;
}

float nfHash01(uvec4 v) {
    return float(nfHash(v) >> 8) / 16777216.0;
}

// nfVoxelAt is local cell l (x + 4y + 16z) of a brick whose mask is
// (lo, hi) and whose voxels start at first: false when it is empty.
bool nfVoxelAt(uint lo, uint hi, uint first, int l, out uvec4 vox) {
    uint bit = l < 32 ? (lo >> uint(l)) & 1u : (hi >> uint(l - 32)) & 1u;
    if (bit == 0u) {
        vox = uvec4(0u);
        return false;
    }
    int rank = l < 32 ? bitCount(lo & ((1u << uint(l)) - 1u))
                      : bitCount(lo) + bitCount(hi & ((1u << uint(l - 32)) - 1u));
    vox = texelFetch(nfVoxels, int(first) + rank);
    return true;
}

#ifdef NF_VOXEL_SHADE
uniform int receiveShadows;

// nfVoxelShade is a voxel's colour at world, seen from eye: its normal
// drawn from its distribution with seed (the mean, spread by how little its
// normals agreed), carried to the world by the rows of the inverse of its
// brick's map (inverse transpose: a normal), its colouring from the
// instance's colour, accent and bark by its shares, lit as foliage_vert.glsl
// lights a vertex — here per pixel — and fogged.
vec3 nfVoxelShade(uvec4 vox, vec3 world, vec3 eye, vec3 row0, vec3 row1, vec3 row2, vec3 upWorld,
                  vec3 colour, vec3 accent, vec3 bark, uvec4 seed) {
    vec3 m = vec3(float(int(vox.z << 24u) >> 24), float(int(vox.z << 16u) >> 24), float(int(vox.z << 8u) >> 24)) / 127.0;
    float agree = clamp(length(m), 0.0, 1.0);
    float z = nfHash01(seed) * 2.0 - 1.0;
    float a = nfHash01(seed + uvec4(0u, 0u, 101u, 0u)) * 6.2831853;
    vec3 rnd = vec3(sqrt(max(1.0 - z * z, 0.0)) * vec2(cos(a), sin(a)), z);
    vec3 nl = m + (1.0 - agree) * rnd;
    if (dot(nl, nl) < 1e-8) {
        nl = vec3(0.0, 1.0, 0.0);
    }
    vec3 n = normalize(nl.x * row0 + nl.y * row1 + nl.z * row2);
    float blade = float(vox.z >> 24u) / 255.0;
    n = normalize(mix(n, upWorld, 0.6 * blade));

    float tone = float(vox.y >> 24u) / 255.0 * 2.0;
    float wA = float(vox.w & 0xffu) / 255.0;
    float wB = float((vox.w >> 8u) & 0xffu) / 255.0;
    float wC = max(1.0 - wA - wB, 0.0);
    float thinShare = float((vox.w >> 16u) & 0xffu) / 255.0;
    float grey = float(vox.w >> 24u) / 255.0;
    vec3 base = (colour * wC + accent * wA + bark * wB) / max(wC + wA + wB, 1e-3);
    bool thin = thinShare > 0.5;
    float viewDepth = biomeViewDepth(world);
    vec3 lighting = biomeLighting(world + upWorld * 0.06, n, viewDepth, receiveShadows != 0, thin);
    base = giDebugAlbedo(base);
    tone = giDebug == 1 ? 1.0 : tone;
    grey = giDebug == 1 ? 1.0 : grey;
    vec3 col = base * tone * grey * clamp(lighting, 0.0, 1.2);
    if (thin) {
        vec3 V = normalize(eye - world);
        for (int i = 0; i < numDirLights; i++) {
            vec3 ld = -normalize(dirLightDir[i]);
            float through = pow(max(dot(-V, ld), 0.0), 4.0);
            col += base * grey * dirLightColor[i] * dirLightBrightness[i] * through * 0.3 * tone * thinShare;
        }
    }
    return applyFog(col, world);
}
#endif
