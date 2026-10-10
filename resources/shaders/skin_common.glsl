// skin_common.glsl — GPU linear blend skinning, shared by every pass that draws
// an animated entity.
//
// This is injected after the #version line (injectAfterVersion, detail_render.go)
// into vertex.glsl, shadow_vert.glsl and shadow_cube_vert.glsl. All three, not
// just the colour pass: an entity whose shadow were still drawn from its rest
// pose would cast a stationary silhouette while the model moved.
//
// The editor's own model_vert.glsl deliberately does NOT include this. The model
// editor skins on the CPU, into the vertex array its wireframe and picking
// already read (editor_model_pose.go), so a second implementation there would be
// a second thing to keep in step for no gain.
//
// ---------- why `skinned` is a uniform and not inferred ----------
//
// When a VAO does not enable a vertex attribute, GL supplies the *default*
// generic value for its type — and for a vec4 that default is (0, 0, 0, 1), not
// zero. Inferring "unskinned" from a zero weight sum would therefore read every
// ordinary wall as fully bound to bone 0 with weight 1 and collapse the level
// onto that bone's delta. The uniform is the only safe gate.

#define MAX_BONES 48

// Bone indices and weights, four each — the budget modelMaxInfluences was
// chosen to fit. Enabled only on submeshes built from a skinned model.
layout(location = 3) in vec4 boneIdx;
layout(location = 4) in vec4 boneWt;

// The palette: one delta per bone, taking that bone's rest space to where the
// pose puts it. Matches PoseDeltas (model_pose.go).
uniform mat4 bones[MAX_BONES];
uniform int  skinned;

// skinMatrix blends this vertex's influences into one transform.
//
// Blending the matrices and applying once — rather than transforming the
// position by each and averaging the results — is the same arithmetic for
// positions, and it is what lets the normal reuse the result instead of running
// the whole loop a second time.
mat4 skinMatrix() {
    mat4 m = mat4(0.0);
    float total = 0.0;
    for (int i = 0; i < 4; ++i) {
        float w = boneWt[i];
        if (w <= 0.0) {
            continue;
        }
        int b = int(boneIdx[i]);
        if (b < 0 || b >= MAX_BONES) {
            continue;
        }
        m += bones[b] * w;
        total += w;
    }
    if (total <= 0.0) {
        return mat4(1.0);
    }
    // Rescaling by the total covers weights that do not sum to 1 instead of
    // dragging the vertex toward the origin — the same guard skinVerts applies
    // on the CPU side (model_skin.go).
    return m * (1.0 / total);
}

vec4 skinPosition(vec3 p) {
    if (skinned == 0) {
        return vec4(p, 1.0);
    }
    return skinMatrix() * vec4(p, 1.0);
}

// skinNormal rotates a normal by the same blend. mat3 of the blend rather than
// its inverse-transpose: a bone delta is a rigid motion plus whatever stretch
// the pose asked for, and the caller already applies the model matrix's own
// inverse-transpose afterwards.
vec3 skinNormal(vec3 n) {
    if (skinned == 0) {
        return n;
    }
    return mat3(skinMatrix()) * n;
}
