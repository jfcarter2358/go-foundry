#version 410

// volume_depth_frag.glsl — the scene's depth, as distances, at the volume
// target's resolution.
//
// The march needs "how far along this ray is the first opaque thing", and it
// needs it in world units, at the resolution it is marching at. This pass turns
// the frame's depth copy (see VolumeTargets.copyDepth) into exactly that, once,
// so every volumetric draw after it is one texture read.
//
// At half resolution each target texel covers a 2x2 block of scene pixels, and
// keeps the NEAREST of the four. That is the conservative choice: a volume
// behind a thin pole is cut back a pixel rather than drawn over the pole, and
// the depth-aware upsample (volume_composite_frag.glsl) puts the lost pixel
// back from a neighbour that did not straddle the edge.

uniform sampler2D volSceneDepth; // the depth copy, one texel per viewport pixel
uniform ivec2     volDepthSize;
uniform int       volScale;      // scene pixels per target texel, per axis
uniform mat4      volInvProjView;
uniform vec3      volCamPos;

out vec4 outDist;

// The distance a cleared pixel is given: past every volume's range, so nothing
// is clipped where nothing was drawn.
#define VOL_NO_SURFACE 1e9

float sceneDistance(ivec2 px) {
    float d = texelFetch(volSceneDepth, px, 0).r;
    if (d >= 1.0) {
        return VOL_NO_SURFACE;
    }
    vec2 uv = (vec2(px) + 0.5) / vec2(volDepthSize);
    vec4 w = volInvProjView * vec4(uv * 2.0 - 1.0, d * 2.0 - 1.0, 1.0);
    return distance(w.xyz / w.w, volCamPos);
}

void main() {
    ivec2 base = ivec2(gl_FragCoord.xy) * volScale;
    float nearest = VOL_NO_SURFACE;
    for (int y = 0; y < volScale; ++y) {
        for (int x = 0; x < volScale; ++x) {
            ivec2 px = min(base + ivec2(x, y), volDepthSize - 1);
            nearest = min(nearest, sceneDistance(px));
        }
    }
    outDist = vec4(nearest, 0.0, 0.0, 1.0);
}
