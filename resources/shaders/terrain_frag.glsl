#version 430

// terrain_frag.glsl — shading for a chunk's block terrain.
//
// Four texture layers blended by per-vertex weights, each projected three
// ways (triplanar), then lit and fogged through the same block the world
// shader uses: biomeLighting() from biome_light.glsl and applyFog() from
// fog.glsl. The ground meets brush floors, cliffs and ramps edge to edge, and
// anything it shaded differently would show as a seam.
//
// A painted biome can lay its own ground texture over the blocks' layers —
// sand, leaf mould, lava gravel. Every one the level uses sits in one array
// (groundTex); each vertex carries how much of its ground each of the
// terrain's up-to-eight ground channels covers, and groundLayer says which
// array layer a channel is. Where the paint ends the weight falls off, and
// the edge is broken up by noise so it frays instead of following the
// triangles.
//
// Two things make it read as ground rather than as a tiled texture:
//
//   - the painted biome's cover tint, which pulls the ground toward the
//     colour of what grows there, a little up close (soil under grass is
//     darker and greener than bare soil) and fully by coverFar, where the
//     grass has thinned out and the ground has to carry the meadow alone;
//   - a slow value-noise variation in brightness and hue across the world,
//     which breaks up the repeat of the texture over open ground.
//
// The normal follows fragment.glsl's rule: the interpolated vertex normal
// while Smooth Lighting is on, the triangle's own facet otherwise.

in vec3 vWorldPos;
in vec3 vNormal;
in vec3 vLocalPos;
in vec3 vLocalNormal;
in vec4 vWeights;
in vec4 vCover;
in vec4 vGround0;
in vec4 vGround1;
in float vViewDepth;

uniform sampler2D layer0;
uniform sampler2D layer1;
uniform sampler2D layer2;
uniform sampler2D layer3;
// Which layers name a texture. A layer with none contributes nothing, and its
// weight is handed to the rest.
uniform vec4 layerOn;
// No layer names a texture: paint the ground with its own normal, the way an
// untextured brush face is painted (fragment.glsl's normalColor).
uniform int normalColor;
uniform int smoothShading;
uniform int receiveShadows;
// Chunk units per repeat of a layer texture (Terrain.TextureScale).
uniform float textureScale;
uniform vec3 camPos;
uniform float coverFar;
// The biome ground textures and, per channel, which layer of them it is (-1:
// none, or unreadable), how many units a repeat spans, and whether it climbs
// walls (1) or only lies on ground facing up (0).
uniform sampler2DArray groundTex;
uniform int groundLayer[8];
uniform float groundScale[8];
uniform float groundWalls[8];

layout(location = 0) out vec4 outputColor;
// The stylize pass's record (light_model.glsl's stylizePack): as
// fragment.glsl's, written only where a painted level binds the buffer.
layout(location = 1) out uvec4 outputStylize;

float tHash(vec2 p) {
    p = fract(p * vec2(0.1031, 0.1030));
    p += dot(p, p.yx + 33.33);
    return fract((p.x + p.y) * p.x);
}

float tNoise(vec2 p) {
    vec2 i = floor(p);
    vec2 f = fract(p);
    f = f * f * (3.0 - 2.0 * f);
    return mix(mix(tHash(i), tHash(i + vec2(1, 0)), f.x),
               mix(tHash(i + vec2(0, 1)), tHash(i + vec2(1, 1)), f.x), f.y);
}

// One layer, projected along the three axes and blended by tri.
vec3 triplanar(sampler2D s, vec3 p, vec3 tri) {
    vec2 uvX = p.zy / textureScale;
    vec2 uvY = p.xz / textureScale;
    vec2 uvZ = p.xy / textureScale;
    // Gradients carried in explicitly: the lookups below sit behind branches
    // on the layer weights, and texture() there would take its mip level
    // from neighbours that skipped it.
    vec3 c = vec3(0.0);
    if (tri.x > 0.0) c += tri.x * textureGrad(s, uvX, dFdx(uvX), dFdy(uvX)).rgb;
    if (tri.y > 0.0) c += tri.y * textureGrad(s, uvY, dFdx(uvY), dFdy(uvY)).rgb;
    if (tri.z > 0.0) c += tri.z * textureGrad(s, uvZ, dFdx(uvZ), dFdy(uvZ)).rgb;
    return c;
}

// One ground texture, projected like triplanar() — but the side projections
// run the same way round the terrain (u flipped by the side they face) and
// start from different places, so where two meet on a curving wall they do
// not show the texture mirrored about the seam. v stays world height, so a
// mesa's strata line up across the projections.
vec3 triplanarGround(float layer, float scale, vec3 p, vec3 tri, vec3 n) {
    float sx = n.x < 0.0 ? -1.0 : 1.0;
    float sz = n.z < 0.0 ? -1.0 : 1.0;
    vec2 uvX = vec2(-p.z * sx / scale + 0.37, p.y / scale);
    vec2 uvY = p.xz / scale;
    vec2 uvZ = vec2(p.x * sz / scale + 0.71, p.y / scale);
    vec3 c = vec3(0.0);
    if (tri.x > 0.0) c += tri.x * textureGrad(groundTex, vec3(uvX, layer), dFdx(uvX), dFdy(uvX)).rgb;
    if (tri.y > 0.0) c += tri.y * textureGrad(groundTex, vec3(uvY, layer), dFdx(uvY), dFdy(uvY)).rgb;
    if (tri.z > 0.0) c += tri.z * textureGrad(groundTex, vec3(uvZ, layer), dFdx(uvZ), dFdy(uvZ)).rgb;
    return c;
}

void main() {
    // Derivatives first, under uniform control flow (see fragment.glsl).
    vec3 dPdx = dFdx(vWorldPos);
    vec3 dPdy = dFdy(vWorldPos);
    vec3 faceNormal = normalize(cross(dPdx, dPdy));
    faceNormal *= gl_FrontFacing ? 1.0 : -1.0;

    vec3 normal = faceNormal;
    if (smoothShading != 0 && dot(vNormal, vNormal) > 1e-12) {
        normal = normalize(vNormal);
    }
    vec3 albedoNormal = normal;
    if (!gl_FrontFacing) {
        normal = -normal;
    }

    vec3 ln = normalize(vLocalNormal);
    vec3 tri = pow(abs(ln), vec3(4.0));
    tri /= (tri.x + tri.y + tri.z);
    // The macro variation below, so the ground textures take it too.
    float macro = mix(0.84, 1.12, tNoise(vWorldPos.xz * 0.045) * 0.6 + tNoise(vWorldPos.xz * 0.17) * 0.4);
    float warm = tNoise(vWorldPos.xz * 0.02 + 17.0) * 0.5;

    // The biome ground: a weighted mix of the channels painted here.
    float gw[8] = float[8](vGround0.x, vGround0.y, vGround0.z, vGround0.w,
                           vGround1.x, vGround1.y, vGround1.z, vGround1.w);
    // Ground that faces up takes it, up to fairly steep banks (about 70
    // degrees): a smooth hillside is ground, not cliff, and a texture that
    // gave out halfway up it would stripe every slope with the block's own.
    float up = smoothstep(0.2, 0.5, albedoNormal.y);
    vec3 groundCol = vec3(0.0);
    float groundSum = 0.0;
    for (int i = 0; i < 8; i++) {
        float w = gw[i];
        if (w <= 0.0 || groundLayer[i] < 0) {
            continue;
        }
        w *= mix(up, 1.0, groundWalls[i]);
        if (w <= 0.0) {
            continue;
        }
        groundCol += w * triplanarGround(float(groundLayer[i]), groundScale[i], vLocalPos, tri, ln);
        groundSum += w;
    }
    float groundK = 0.0;
    if (groundSum > 0.0) {
        groundCol /= groundSum;
        // Frayed rather than feathered: the weight is pushed through a
        // narrow step against noise, so the edge of a painted patch is
        // ragged ground, not a soft blur along the triangles.
        float fray = tNoise(vWorldPos.xz * 0.9) * 0.6 + tNoise(vWorldPos.xz * 2.7) * 0.4;
        groundK = smoothstep(0.3, 0.6, min(groundSum, 1.0) + (fray - 0.5) * 0.45);
        groundCol *= macro;
        groundCol = mix(groundCol, groundCol * vec3(1.05, 1.0, 0.9), warm);
    }

    vec3 albedo;
    if (normalColor != 0) {
        albedo = albedoNormal * 0.5 + 0.5;
    } else {
        vec4 w = vWeights * layerOn;
        float sum = w.x + w.y + w.z + w.w;
        if (sum < 1e-4) {
            // Painted entirely with layers that name nothing: show the
            // first layer that does.
            w = layerOn.x > 0.0 ? vec4(1, 0, 0, 0)
              : layerOn.y > 0.0 ? vec4(0, 1, 0, 0)
              : layerOn.z > 0.0 ? vec4(0, 0, 1, 0) : vec4(0, 0, 0, 1);
            sum = 1.0;
        }
        w /= sum;
        // tri (above) is sharpened so a slope shows one projection with a
        // short blend rather than a smear of three.
        albedo = vec3(0.0);
        if (w.x > 0.0) albedo += w.x * triplanar(layer0, vLocalPos, tri);
        if (w.y > 0.0) albedo += w.y * triplanar(layer1, vLocalPos, tri);
        if (w.z > 0.0) albedo += w.z * triplanar(layer2, vLocalPos, tri);
        if (w.w > 0.0) albedo += w.w * triplanar(layer3, vLocalPos, tri);

        // Macro variation, so open ground is not one texture repeated.
        albedo *= macro;
        albedo = mix(albedo, albedo * vec3(1.05, 1.0, 0.9), warm);
    }
    albedo = mix(albedo, groundCol, groundK);

    // The biome's ground tint: roots and litter up close, the whole meadow
    // from afar. Luminance of the texture is kept so its detail survives.
    if (vCover.a > 0.001) {
        float dist = length(vWorldPos - camPos);
        float far = smoothstep(coverFar * 0.35, coverFar, dist);
        // Where the biome has a texture of its own, that texture is the
        // near ground and the tint only takes over with distance.
        float k = vCover.a * mix(0.55 * (1.0 - 0.8 * groundK), 0.92, far);
        // Only faces looking up are carpeted; a cliff keeps its rock.
        k *= smoothstep(0.35, 0.75, albedoNormal.y);
        float luma = dot(albedo, vec3(0.299, 0.587, 0.114));
        vec3 tint = vCover.rgb * (0.55 + 0.9 * luma);
        // Far off, the meadow's own grain: tufts too far to draw still leave
        // the ground mottled light and dark at about their spacing, and
        // drier and lusher drifts across it, so the tint does not read as a
        // painted board where the foliage thins out. The fine term fades as
        // its cells shrink toward a pixel, before it can shimmer.
        float px = length(fwidth(vWorldPos.xz));
        float fine = 1.0 - smoothstep(0.4, 1.4, px);
        float tuft = mix(0.5, tNoise(vWorldPos.xz * 0.95) * 0.6 + tNoise(vWorldPos.xz * 2.9) * 0.4, fine);
        float clump = tNoise(vWorldPos.xz * 0.21 + 5.0);
        float drift = tNoise(vWorldPos.xz * 0.05 + 31.0);
        vec3 grain = tint * (0.8 + 0.28 * tuft + 0.16 * clump);
        grain = mix(grain, grain * vec3(1.16, 1.05, 0.74), smoothstep(0.5, 0.85, drift) * 0.6);
        grain = mix(grain, grain * vec3(0.86, 0.98, 0.86), smoothstep(0.5, 0.15, drift) * 0.5);
        tint = mix(tint, grain, far);
        albedo = mix(albedo, tint, k);
    }

    // Normal-offset shadows: the shadow is looked up from a point lifted off
    // the surface along its true (facet) normal, by about a shadow texel —
    // which grows with distance as the cascades coarsen. The slope-scaled
    // bias alone is sized for flat floors; on the long, gentle, curving
    // slopes of smooth ground the outer PCF taps land deeper than it allows,
    // and the ground shadows itself in fine stripes that alias into rings.
    vec3 geoN = gl_FrontFacing ? faceNormal : -faceNormal;
    vec3 shadowAt = vWorldPos + geoN * (0.03 + vViewDepth * 0.0025);
    // Earth and rock: a rough dielectric (specular.glsl). The view vector is
    // the surface's own, not the lifted shadow point's.
    PBRSurface ps = pbrSurface(vWorldPos, normal, 0.8, 0.0, 0.5, albedo);
    vec3 spec = vec3(0.0);
    vec3 lighting = biomeLightingSpec(shadowAt, normal, vViewDepth, receiveShadows != 0, false, ps, spec);
    spec += pbrEnvSpecular(ps, vWorldPos, normal, biomeAmbientRef, 1.0);
    // Scene units, as fragment.glsl shades (gfx/hdr.go; 1 in LDR).
    vec3 color = giDebugAlbedo(albedo) * clamp(lighting, 0.0, hdrLightMax()) + clamp(spec, 0.0, hdrLightMax());
    color = applyFogScene(color, vWorldPos);
    outputColor = vec4(color, 1.0);
    outputStylize = styRamp.w == 0.0 ? uvec4(0u) : stylizePack(normal, normal, vViewDepth, 1.0);
}
