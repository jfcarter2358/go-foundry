#version 410

// biome_frag.glsl — output for GPU-scattered biome scatter.
//
// Foliage (grass/wildflower/cattail) is lit per-vertex in biome_vert.glsl —
// thin blades don't show faceting either way, so Gouraud is free detail, and
// the shadow lookup that comes with it costs one tap per vertex instead of a
// filtered lookup per pixel. See the note at the top of that file.
//
// Rocks and moss are lit per-fragment here instead, with a procedural noise
// bump perturbing the normal and a small specular term: the mesh itself stays a
// simple low-poly blob (see buildRockMesh in grass.go), and the extra
// surface detail — roughness, color variation, a highlight that actually
// moves with the camera — comes entirely from the shader. That's the
// "simple geometry, shader-driven detail" look the whole biome pass goes for.
// They can afford the per-fragment path because there are two orders of
// magnitude fewer of them than blades (maxRockGridN/maxMossGridN vs
// maxGrassGridN in grass.go), and because a bumped normal is the whole point —
// interpolating it from three vertices would throw away the detail.
//
// Lighting itself is biomeLighting() from biome_light.glsl, the same block the
// ground patch and the foliage above use, on the same uniforms the main scene
// shader is fed. A rock sitting on a floor is lit by the same lights, and
// shadowed by the same cascades, as the floor.

in vec3  vColor;
in vec3  vWorldPos;
in vec3  vNormal;
in vec3  vBaseColor;
in float vViewDepth;

uniform int  shapeKind; // 0=grass, 1=rock, 2=wildflower, 3=cattail
uniform int  receiveShadows; // BiomeShadows.Receive — see biome_vert.glsl
uniform vec3 camPos;

out vec4 outputColor;

float hash13(vec3 p) {
    p = fract(p * 0.1031);
    p += dot(p, p.yzx + 19.19);
    return fract((p.x + p.y) * p.z);
}

float noise3(vec3 p) {
    vec3 i = floor(p);
    vec3 f = fract(p);
    f = f * f * (3.0 - 2.0 * f);
    float n000 = hash13(i + vec3(0.0, 0.0, 0.0));
    float n100 = hash13(i + vec3(1.0, 0.0, 0.0));
    float n010 = hash13(i + vec3(0.0, 1.0, 0.0));
    float n110 = hash13(i + vec3(1.0, 1.0, 0.0));
    float n001 = hash13(i + vec3(0.0, 0.0, 1.0));
    float n101 = hash13(i + vec3(1.0, 0.0, 1.0));
    float n011 = hash13(i + vec3(0.0, 1.0, 1.0));
    float n111 = hash13(i + vec3(1.0, 1.0, 1.0));
    float nx00 = mix(n000, n100, f.x);
    float nx10 = mix(n010, n110, f.x);
    float nx01 = mix(n001, n101, f.x);
    float nx11 = mix(n011, n111, f.x);
    float nxy0 = mix(nx00, nx10, f.y);
    float nxy1 = mix(nx01, nx11, f.y);
    return mix(nxy0, nxy1, f.z);
}

void main() {
    if (shapeKind != 1) {
        outputColor = vec4(vColor, 1.0);
        return;
    }

    vec3 n = normalize(vNormal);

    // Bump: gradient of a noise field perturbs the flat facet normal, in a
    // local tangent frame so it stays roughly on the surface.
    vec3 p = vWorldPos * 3.0;
    float e = 0.06;
    float h = noise3(p);
    vec3 grad = vec3(noise3(p + vec3(e, 0.0, 0.0)) - h,
                      noise3(p + vec3(0.0, e, 0.0)) - h,
                      noise3(p + vec3(0.0, 0.0, e)) - h) / e;
    vec3 up = abs(n.y) < 0.99 ? vec3(0.0, 1.0, 0.0) : vec3(1.0, 0.0, 0.0);
    vec3 tangent = normalize(cross(up, n));
    vec3 bitangent = cross(n, tangent);
    vec3 bumpN = normalize(n - (tangent * grad.x + bitangent * grad.z + n * grad.y) * 0.6);

    // Two-tone albedo (noise picks between a darker crevice shade and the
    // base color) plus an AO-ish darkening from the same noise value.
    vec3 albedo = mix(vBaseColor * 0.7, vBaseColor, h);
    float ao = mix(0.65, 1.0, h);

    // The shadow term uses the FLAT facet normal, not the bumped one: the bump
    // is a lighting fiction with no matching geometry in the depth map, and
    // feeding it to the slope-scaled bias would ask for a bias that suits a
    // surface the shadow map never saw.
    vec3 lighting = biomeLighting(vWorldPos, bumpN, vViewDepth, receiveShadows != 0);
    vec3 color = albedo * ao * clamp(lighting, 0.0, 1.0);

    // Blinn-Phong glint per directional light, on top of the diffuse result —
    // the highlight that reads as wet or crystalline stone as the camera moves.
    vec3 viewDir = normalize(camPos - vWorldPos);
    for (int i = 0; i < numDirLights; i++) {
        vec3 ldir = -normalize(dirLightDir[i]);
        vec3 halfV = normalize(viewDir + ldir);
        float spec = pow(max(dot(bumpN, halfV), 0.0), 24.0) * 0.15;
        color += dirLightColor[i] * dirLightBrightness[i] * spec;
    }

    outputColor = vec4(color, 1.0);
}
