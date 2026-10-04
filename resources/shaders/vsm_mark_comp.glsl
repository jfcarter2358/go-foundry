// vsm_mark_comp.glsl — the receivers' marks for the virtual shadow map
// (render/vsm.go; Phase 5). The #version line is the Go side's, followed by
// vsm.glsl, whose level choice and window mapping this must share with the
// lookup: a pixel asks for exactly the page its shading will read.
//
// One thread a pixel of the frame's depth, after the opaque world is drawn.
// Each covered pixel's world position is rebuilt from the depth and the
// frame's (jittered) inverse view-projection, and it asks for the pages its
// lookup's filter reaches — normal offset included — on its own level and
// on the next coarser one, which is what it falls back to while its own is
// not there yet. The asks are bits, a 32-bit row of a level's window a word
// (VSM_PAGES is 32), gathered in shared memory and ORed out once per
// workgroup per word that has any.

layout(local_size_x = 16, local_size_y = 16) in;

#define VSM_REQ_WORDS (VSM_LEVELS * VSM_PAGES)

uniform sampler2D sceneDepth;
uniform mat4 invProjView;
uniform ivec2 size;

layout(std430, binding = 0) buffer Requests {
    uint req[];
};

shared uint marks[VSM_REQ_WORDS];

void markLevel(vec3 world, int l) {
    vec2 t = vsmWindowTexel(vsmRot * (world - vsmCamPos), l);
    // The filter's reach and the normal offset's, in texels, with a texel
    // to spare.
    float reach = 1.5 + vsmNormalOffset + 1.0;
    ivec2 lo = ivec2(floor((t - reach) / VSM_PAGE_TEXELS));
    ivec2 hi = ivec2(floor((t + reach) / VSM_PAGE_TEXELS));
    for (int y = lo.y; y <= hi.y; y++) {
        for (int x = lo.x; x <= hi.x; x++) {
            if (x >= 0 && y >= 0 && x < VSM_PAGES && y < VSM_PAGES) {
                atomicOr(marks[l * VSM_PAGES + y], 1u << uint(x));
            }
        }
    }
}

void main() {
    uint lid = gl_LocalInvocationIndex;
    for (uint i = lid; i < uint(VSM_REQ_WORDS); i += 256u) {
        marks[i] = 0u;
    }
    barrier();
    ivec2 p = ivec2(gl_GlobalInvocationID.xy);
    if (p.x < size.x && p.y < size.y) {
        float d = texelFetch(sceneDepth, p, 0).r;
        if (d < 1.0) {
            vec4 ndc = vec4((vec2(p) + 0.5) / vec2(size) * 2.0 - 1.0, d * 2.0 - 1.0, 1.0);
            vec4 w = invProjView * ndc;
            vec3 world = w.xyz / w.w;
            int l = vsmLevelFor(distance(world, vsmCamPos));
            markLevel(world, l);
            if (l + 1 < VSM_LEVELS) {
                markLevel(world, l + 1);
            }
        }
    }
    barrier();
    for (uint i = lid; i < uint(VSM_REQ_WORDS); i += 256u) {
        if (marks[i] != 0u) {
            atomicOr(req[i], marks[i]);
        }
    }
}
