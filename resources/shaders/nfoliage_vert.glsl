#version 430

// nfoliage_vert.glsl — the Nanite foliage's triangles and parts
// (render/nfoliage.go). Placement and bending are nfoliage_common.glsl's; the
// lighting is foliage_vert.glsl's, per vertex through biome_light.glsl, so a
// plant on this path shades as the classic path shades it.
//
// A triangle cluster's vertex is the plant's own, bone and all. A part
// cluster's is a card template's corner (its bone attribute is -1): the
// reference's part (nfoliage_cull_comp.glsl kept the cluster, the scatter
// wrote one reference per part) places it — base + x·right + y·up, the
// part's normal and tone, its bone, and its place along the bone from the
// part's bottom to its top edge.
//
// Where the frame has TAA (coverageDither != 0), the vertex also says where
// it was last frame — last frame's poses, gust and camera — so its pixels
// carry their own motion (nfoliage_frag.glsl writes it).

layout(location = 0) in vec3 aPos;
layout(location = 1) in vec3 aNormal;
layout(location = 2) in vec2 aUV;
// sway (unused here: the bones replace it), part, tone.
layout(location = 3) in vec3 aParam;
// bone (-1: the part's), place along it (a template's: its corner's y).
layout(location = 5) in vec2 aBind;
// The reference: item, part (nfoliage_scatter_comp.glsl).
layout(location = 4) in uvec2 aRef;

uniform mat4 projView;
uniform int receiveShadows;
uniform vec3 barkColor;
// TAA: this frame's and last frame's unjittered view-projection.
uniform mat4 motionViewProj;
uniform mat4 motionPrevViewProj;
uniform int writeMotion;

out vec3 vColor;
out vec2 vUV;
flat out int vPart;
out float vFade;
out vec3 vWorldPos;
out vec4 vClip;
out vec4 vPrevClip;

void main() {
    if (!nfBeginRef(aRef)) {
        gl_Position = vec4(2.0, 2.0, 2.0, 1.0);
        vColor = vec3(0.0);
        vUV = vec2(0.0);
        vPart = 0;
        vFade = 0.0;
        vWorldPos = vec3(0.0);
        vClip = vec4(0.0, 0.0, 0.0, 1.0);
        vPrevClip = vClip;
        return;
    }
    vec3 p = aPos;
    vec3 n = aNormal;
    float tone = aParam.z;
    int bone = int(aBind.x + 0.5);
    float w = aBind.y;
    if (aBind.x < 0.0) {
        int pi = (nFirst + int(nWhich)) * 4;
        vec4 right = texelFetch(nfParts, pi);
        vec4 upv = texelFetch(nfParts, pi + 1);
        vec4 nt = texelFetch(nfParts, pi + 2);
        vec4 base = texelFetch(nfParts, pi + 3);
        p = base.xyz + right.xyz * aPos.x + upv.xyz * aPos.y;
        n = nt.xyz;
        tone *= nt.w;
        bone = int(base.w + 0.5);
        w = mix(right.w, upv.w, aBind.y);
    }
    vec4 r0, r1, r2;
    nfSkinMatrix(false, bone, w, r0, r1, r2);
    vec3 world = nfToWorld(nfApply(r0, r1, r2, p));
    vec3 nw = normalize(nfDirToWorld(nfApplyDir(r0, r1, r2, n)));
    vec3 upW = normalize(mat3(nModel) * nUp);

    int part = int(aParam.y + 0.5);
    vec3 base = iColorRank.rgb;
    if (part == 2 || part == 3) {
        base = iAccentSeed.rgb;
    } else if (part == 4) {
        base = barkColor * (0.8 + 0.4 * iAccentSeed.w);
    }
    bool thin = part == 1 || part == 3 || part == 5;
    if (part == 5) {
        nw = normalize(mix(nw, upW, 0.6));
    }
    float viewDepth = biomeViewDepth(world);
    vec3 lightAt = world + upW * 0.06;
    vec3 lighting = biomeLighting(lightAt, nw, viewDepth, receiveShadows != 0, thin);
    base = giDebugAlbedo(base);
    tone = giDebug == 1 ? 1.0 : tone;
    vec3 col = base * tone * clamp(lighting, 0.0, 1.2);
    if (thin) {
        vec3 V = normalize(camPos - world);
        for (int i = 0; i < numDirLights; i++) {
            vec3 ld = -normalize(dirLightDir[i]);
            float through = pow(max(dot(-V, ld), 0.0), 4.0);
            col += base * dirLightColor[i] * dirLightBrightness[i] * through * 0.3 * tone;
        }
    }
    vColor = col;
    vUV = aUV;
    vPart = part;
    vFade = nFade;
    vWorldPos = world;
    gl_Position = projView * vec4(world, 1.0);
    vClip = vec4(0.0, 0.0, 0.0, 1.0);
    vPrevClip = vClip;
    if (writeMotion != 0) {
        nfSkinMatrix(true, bone, w, r0, r1, r2);
        vec3 was = nfToWorld(nfApply(r0, r1, r2, p));
        vClip = motionViewProj * vec4(world, 1.0);
        vPrevClip = motionPrevViewProj * vec4(was, 1.0);
    }
}
