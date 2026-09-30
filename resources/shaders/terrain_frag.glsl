#version 410

// terrain_frag.glsl — shading for a chunk's heightfield ground.
//
// Four texture layers blended by per-vertex weights, then lit and fogged
// through the same block the world shader uses: biomeLighting() from
// biome_light.glsl (every directional light with its cascade shadow, 5x5 PCF
// and the cascade cross-fade; every point light with its cubemap) and
// applyFog() from fog.glsl. The ground meets brush floors, cliffs and ramps
// edge to edge, and anything it shaded differently would show as a seam.
//
// The normal follows fragment.glsl's rule too: the interpolated vertex normal
// while Smooth Lighting is on, the triangle's own facet otherwise — so the
// player's graphics setting reaches the ground as it reaches every wall.

in vec3 vWorldPos;
in vec3 vNormal;
in vec4 vWeights;
in vec2 vUV;
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

out vec4 outputColor;

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
        albedo = vec3(0.0);
        // Skip the lookups a layer's zero weight would throw away. The
        // samples stay outside any divergent branch's derivative needs:
        // texture() inside the if would take its mip level from neighbours
        // that skipped it, so textureGrad carries the derivatives in.
        vec2 dUVdx = dFdx(vUV);
        vec2 dUVdy = dFdy(vUV);
        if (w.x > 0.0) albedo += w.x * textureGrad(layer0, vUV, dUVdx, dUVdy).rgb;
        if (w.y > 0.0) albedo += w.y * textureGrad(layer1, vUV, dUVdx, dUVdy).rgb;
        if (w.z > 0.0) albedo += w.z * textureGrad(layer2, vUV, dUVdx, dUVdy).rgb;
        if (w.w > 0.0) albedo += w.w * textureGrad(layer3, vUV, dUVdx, dUVdy).rgb;
    }

    vec3 lighting = biomeLighting(vWorldPos, normal, vViewDepth, receiveShadows != 0, false);
    vec3 color = albedo * clamp(lighting, 0.0, 1.0);
    color = applyFog(color, vWorldPos);
    outputColor = vec4(color, 1.0);
}
