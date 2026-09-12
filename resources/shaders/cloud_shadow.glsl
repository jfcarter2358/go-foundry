// cloud_shadow.glsl — the shadow clouds cast on everything under them.
//
// Spliced into every stage that lights world geometry, beside fog.glsl and for
// the same reason fog is shared: fragment.glsl (see InjectCloudShadow in
// gfx/fog_shader.go) and every consumer of biome_light.glsl (the scatter, the
// ground patch and sprites — see biomeLightSnippet). A cloud's shadow that
// darkened the floor but not the grass standing on it would read as the grass
// floating.
//
// The map is baked once a frame by render/cloud_shadow.go: a top-down view
// along the sun of every shadow-casting cloud volume and the sky's deck, each
// texel holding two numbers about the line of sunlight through it —
//
//   R  how much light survives the whole line (transmittance), and
//   G  how far along it the cloud mass sits, as a 0..1 light-space depth.
//
// G is what lets a cloud shadow only what is UNDER it. A single transmittance
// would darken the top of a mountain standing in a cloud and the valley below
// it alike; comparing the receiver's own light depth against where the mass is
// puts the shadow below the cloud and nowhere above it.
//
// The uniforms are uploaded by UploadShadowUniformsTo, which every program
// that includes this already calls, and cloudShadowMap is always bound to a
// real texture (a white dummy when there are no clouds) on its own unit — see
// CloudShadowTexUnit — so a program never samples an unbound unit.

uniform int       cloudShadowOn;     // 0 = no map this frame
uniform int       cloudShadowLight;  // the directional light the map was baked for
uniform sampler2D cloudShadowMap;
uniform mat4      cloudShadowMatrix; // world -> [0,1]^3 in the bake's light space

// How much of a compiled level's BAKED light the cloud shadow takes away.
// Baked light is the sun and everything else summed offline, and which share
// of it the sun contributed is not kept, so a fixed share is removed instead:
// enough for the shadow of a passing cloud to read, not so much that a surface
// lit mostly by lamps goes dark under one.
#define CLOUD_SHADOW_BAKED_SHARE 0.55

// cloudShadowFactor is the fraction of directional light lightIdx that reaches
// worldPos past the clouds: 1 in the open, lower under cloud.
//
// textureLod rather than texture, because biome_light.glsl compiles this into a
// VERTEX stage too, where there are no derivatives to pick a mip from.
float cloudShadowFactor(int lightIdx, vec3 worldPos) {
    if (cloudShadowOn == 0 || lightIdx != cloudShadowLight) {
        return 1.0;
    }
    vec3 p = (cloudShadowMatrix * vec4(worldPos, 1.0)).xyz;
    // Faded out over the outer tenth of the map, so the edge of the baked area
    // is a gradient rather than a line across the ground.
    vec2 edge = min(p.xy, 1.0 - p.xy);
    float inside = clamp(min(edge.x, edge.y) * 10.0, 0.0, 1.0);
    if (inside <= 0.0) {
        return 1.0;
    }
    vec2 s = textureLod(cloudShadowMap, p.xy, 0.0).rg;
    float below = smoothstep(s.g - 0.01, s.g + 0.01, p.z);
    return mix(1.0, s.r, below * inside);
}
