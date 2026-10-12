#version 430

// nanite_material_frag.glsl — the material pass's main (render/
// nanite_vis.go), appended to fragment.glsl and vertex.glsl as
// materialFragmentSource makes them (render/nanite_vis_source.go).
//
// For a pixel of this draw's bin: the triangle the ID pass left there, its
// three corners fetched from the bin's own buffers and run through vertex.glsl
// (vx_main) exactly as the forward pass ran them, and everything the
// rasteriser would have interpolated reconstructed at the pixel's centre —
// then fragment.glsl's own main (nvShadeMain), on those values. So the
// shading is the forward path's, the light model, shadows, clouds and live GI
// included, once a pixel however many surfaces were drawn behind it.
//
// The interpolation is perspective-correct, from 2D homogeneous barycentrics
// (Olano and Greer): with the corners' clip-space (x, y, w) as the columns of
// M, a point (px, py) on screen is the triangle's point with barycentrics
// proportional to M⁻¹·(px, py, 1). No division by a corner's w, so a triangle
// crossing the near plane needs no special case. The derivatives
// fragment.glsl takes are the same expression's, differentiated: exact, and
// the triangle's own, where a full-screen pass's dFdx would difference this
// pixel against whatever its neighbour shows.

// The bins' depth test is what picks this bin's pixels; early, so another
// bin's never run the shading (the check in main is a safety net).
layout(early_fragment_tests) in;

struct Record {
    uvec4 a; // first index, base vertex, instance, bin
    uvec4 b;
};
layout(std430, binding = 1) readonly buffer Records {
    Record records[];
};
layout(std430, binding = 2) readonly buffer Indices {
    uint indices[];
};
layout(std430, binding = 3) readonly buffer Vertices {
    float vertexData[]; // x y z u v
};
layout(std430, binding = 4) readonly buffer Normals {
    float normalData[];
};
layout(std430, binding = 5) readonly buffer Light {
    float lightData[]; // render.LightStride (7) a vertex
};
// The pool's vertex colours, one RGBA8 word a vertex (vertexColors; bound to
// a stand-in when the bin's buffers have none, and then never read).
layout(std430, binding = 7) readonly buffer Colors {
    uint colorData[];
};
struct MatInstance {
    mat4 model;
    vec4 sphere;
    uvec4 flags;
    vec4 wind;
};
layout(std430, binding = 0) readonly buffer MatInstances {
    MatInstance matInstances[];
};

uniform usampler2D visIDs;
uniform usampler2D binIDs;
uniform uint bin;
uniform vec2 viewSize;
uniform int hasNormals;
uniform int hasLight;

void main() {
    ivec2 p = ivec2(gl_FragCoord.xy);
    if (texelFetch(binIDs, p, 0).r != bin + 1u) {
        discard;
    }
    uint id = texelFetch(visIDs, p, 0).r;
    uvec4 ra = records[(id >> 12) - 1u].a;
    uint first = ra.x + 3u * (id & 4095u);
    instanceModel = matInstances[ra.z].model;
    // Its wind (wind_common.glsl): the corners are bent as the ID pass bent them.
    instanceSphere = matInstances[ra.z].sphere;
    instanceWind = matInstances[ra.z].wind;

    vec4 clip[3];
    vec2 uv[3];
    vec3 wp[3];
    vec3 nrm[3];
    float vd[3];
    vec4 bl[3];
    vec3 bi[3];
    vec4 vc[3];
    for (int k = 0; k < 3; k++) {
        uint v = uint(int(indices[first + uint(k)]) + int(ra.y));
        vert = vec3(vertexData[5u * v], vertexData[5u * v + 1u], vertexData[5u * v + 2u]);
        vertTexCoord = vec2(vertexData[5u * v + 3u], vertexData[5u * v + 4u]);
        vertNormal = hasNormals != 0 ? vec3(normalData[3u * v], normalData[3u * v + 1u], normalData[3u * v + 2u]) : vec3(0.0);
        if (hasLight != 0) {
            vertLight = vec4(lightData[7u * v], lightData[7u * v + 1u], lightData[7u * v + 2u], lightData[7u * v + 3u]);
            vertLightIndirect = vec3(lightData[7u * v + 4u], lightData[7u * v + 5u], lightData[7u * v + 6u]);
        } else {
            // A disabled attribute's value in the forward pass.
            vertLight = vec4(0.0, 0.0, 0.0, 1.0);
            vertLightIndirect = vec3(0.0);
        }
        vertColor = vertexColors != 0 ? unpackUnorm4x8(colorData[v]) : vec4(1.0);
        vx_main();
        clip[k] = vx_Position;
        uv[k] = vx_fragTexCoord;
        wp[k] = vx_fragWorldPos;
        nrm[k] = vx_fragNormal;
        vd[k] = vx_fragViewDepth;
        bl[k] = vx_fragBakedLight;
        bi[k] = vx_fragBakedIndirect;
        vc[k] = vx_fragVertColor;
    }

    mat3 M = mat3(clip[0].xyw, clip[1].xyw, clip[2].xyw);
    mat3 Mi = inverse(M);
    vec2 ndc = gl_FragCoord.xy / viewSize * 2.0 - 1.0;
    vec3 u = Mi * vec3(ndc, 1.0);
    float s = u.x + u.y + u.z;
    vec3 b = u / s;
    vec3 dux = Mi[0] * (2.0 / viewSize.x);
    vec3 duy = Mi[1] * (2.0 / viewSize.y);
    vec3 bx = (dux - b * (dux.x + dux.y + dux.z)) / s;
    vec3 by = (duy - b * (duy.x + duy.y + duy.z)) / s;

    fragTexCoord = b.x * uv[0] + b.y * uv[1] + b.z * uv[2];
    fragWorldPos = b.x * wp[0] + b.y * wp[1] + b.z * wp[2];
    fragNormal = b.x * nrm[0] + b.y * nrm[1] + b.z * nrm[2];
    fragViewDepth = b.x * vd[0] + b.y * vd[1] + b.z * vd[2];
    fragBakedLight = b.x * bl[0] + b.y * bl[1] + b.z * bl[2];
    fragBakedIndirect = b.x * bi[0] + b.y * bi[1] + b.z * bi[2];
    fragVertColor = b.x * vc[0] + b.y * vc[1] + b.z * vc[2];
    nvDUVdx = bx.x * uv[0] + bx.y * uv[1] + bx.z * uv[2];
    nvDUVdy = by.x * uv[0] + by.y * uv[1] + by.z * uv[2];
    nvDPdx = bx.x * wp[0] + bx.y * wp[1] + bx.z * wp[2];
    nvDPdy = by.x * wp[0] + by.y * wp[1] + by.z * wp[2];
    // Counter-clockwise on screen is the front (GL's default winding): the
    // sign of the homogeneous determinant, which is the screen-space area's
    // for any corners in front of the eye.
    nvFrontFacing = determinant(M) > 0.0;
#ifdef GI_HALF_RES
    // Which pixel the half-resolution probe light is read back for
    // (light_model.glsl's giHalfResIrradiance).
    giHalfResPixel = gl_FragCoord.xy;
#endif
    nvShadeMain();
}
