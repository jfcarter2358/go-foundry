import sys, os
srcd, dstd, which = sys.argv[1], sys.argv[2], sys.argv[3]
files = {}
def rep(f, a, b, n=1):
    if f not in files: files[f] = open(os.path.join(srcd, f)).read()
    s = files[f]
    assert s.count(a) >= 1, (f, a)
    files[f] = s.replace(a, b) if n == 0 else s.replace(a, b, n)
H = {
 "A": [("fragment.glsl", "void main() {\n", "void main() {\n    outputColor = vec4(texture(tex, fragTexCoord).rgb, 1.0); if (fragTexCoord.x > -1e30) return;\n")],
 "B": [("fragment.glsl", "float shadow = calcDirShadow(i, geoNormal, ldir);", "float shadow = 0.0;")],
 "D": [("fragment.glsl", "        : applyFogScene(surface, fragWorldPos);", "        : surface;")],
 "G": [("specular.glsl", "vec3 pbrSpecular(vec3 n, vec3 l, PBRSurface s) {\n", "vec3 pbrSpecular(vec3 n, vec3 l, PBRSurface s) {\n    if (true) return vec3(0.0);\n"),
       ("specular.glsl", "vec3 pbrEnvSpecular(PBRSurface s, vec3 p, vec3 n, vec3 ambientRef, float specOcc) {\n", "vec3 pbrEnvSpecular(PBRSurface s, vec3 p, vec3 n, vec3 ambientRef, float specOcc) {\n    if (true) return vec3(0.0);\n"),
       ("fragment.glsl", "if (waterCausticCount > 0 && isWater == 0 && unlit == 0)", "if (false)"),
       ("fragment.glsl", "if (hasArmMap != 0 && isWater == 0", "if (false && isWater == 0")],
 "C": [("fragment.glsl", "ambientLight(globalLightColor * globalLightBrightness, fragWorldPos, normal)", "(globalLightColor * globalLightBrightness)")],
 "C2": [("light_model.glsl", "float ambientVolumeFactor(vec3 p, vec3 n) {\n", "float ambientVolumeFactor(vec3 p, vec3 n) {\n    if (true) return 1.0;\n")],
 "M": [("scene_block.glsl", "\n#endif", "\nfloat gAmbVolF = 1.0;\nfloat gAmbFogPath = 1.0;\n#endif"),
       ("light_model.glsl", "ambientAt(ambient, n) * ambientVolumeFactor(p, n);", "ambientAt(ambient, n) * gAmbVolF;"),
       ("fog.glsl", "    float amb = ambVolFogPath(worldPos);", "    float amb = gAmbFogPath;"),
       ("fragment.glsl", "    vec3 geoNormal = normal;\n", "    vec3 geoNormal = normal;\n    gAmbVolF = ambientVolumeFactor(fragWorldPos, normal);\n    gAmbFogPath = ambVolFogPath(fragWorldPos);\n")],
 "M1": [("scene_block.glsl", "\n#endif", "\nfloat gAmbVolF = 1.0;\nfloat gAmbFogPath = 1.0;\n#endif"),
       ("fog.glsl", "    float amb = ambVolFogPath(worldPos);", "    float amb = gAmbFogPath;"),
       ("fragment.glsl", "    vec3 geoNormal = normal;\n", "    vec3 geoNormal = normal;\n    gAmbFogPath = ambVolFogPath(fragWorldPos);\n")],
 "N": [("light_model.glsl", "if (i >= numAmbVolumes) {", "if (i >= min(numAmbVolumes, 2)) {"),
       ("fog.glsl", "if (i >= numAmbVolumes) {", "if (i >= min(numAmbVolumes, 2)) {")],
 "U": [("light_model.glsl", "    for (int i = 0; i < MAX_AMBIENT_VOLUMES; ++i) {", "#pragma optionNV(unroll all)\n    for (int i = 0; i < MAX_AMBIENT_VOLUMES; ++i) {"),
       ("fog.glsl", "    for (int i = 0; i < MAX_AMBIENT_VOLUMES; ++i) {", "#pragma optionNV(unroll all)\n    for (int i = 0; i < MAX_AMBIENT_VOLUMES; ++i) {")],
 "P": [("vsm.glsl", "float s = softShadowsOn != 0 ? vsmSampleLevelPCSS(worldPos, normal, lightDir, l)", "float s = false ? vsmSampleLevelPCSS(worldPos, normal, lightDir, l)")],
 "Q": [("specular.glsl", "vec2 ssaoAt(vec3 p) {\n", "vec2 ssaoAt(vec3 p) {\n    if (true) return vec2(1.0);\n")],
 "W1": [("surface_fx.glsl", "    if (anti < 0.5) {\n        fxSplatRead", "    if (true) {\n        fxSplatRead")],
 "W2": [("surface_fx.glsl", "    if (fxSplatBedding.x > 0.0) {", "    if (false) {")],
 "W3": [("surface_fx.glsl", "    if (trail) {\n        fxTrailAt", "    if (false) {\n        fxTrailAt")],
 "W4": [("surface_fx.glsl", "    if (fxSplatRipple.x > 0.0) {", "    if (false) {")],
 "W5": [("surface_fx.glsl", "    if (fxStrataParams.z > 0.0) {", "    if (false) {")],
 "W9": [("surface_fx.glsl", "                    inout float ao) {\n    vec2 wuv", "                    inout float ao) {\n    if (fxSplatLayer.w > -100.0) { vec4 ah9, na9; fxSplatTriSum(fxSplatLayer.w, fxSplatInvScale.w, 0.0, p, N, dPdx, dPdy, ah9, na9); albedo = vec4(ah9.rgb, albedo.a); normal = na9.xyz; return; }\n    vec2 wuv")],
 "W8": [("surface_fx.glsl", "                    inout float ao) {\n    vec2 wuv", "                    inout float ao) {\n    if (fxSplatLayer.w > -100.0) { vec4 ah9, na9; fxSplatTriSum(fxSplatLayer.w, fxSplatInvScale.w, 1.0, p, N, dPdx, dPdy, ah9, na9); albedo = vec4(ah9.rgb, albedo.a); normal = na9.xyz; return; }\n    vec2 wuv")],
 "PL": [("fragment.glsl", "for (int i = 0; i < numPointLights; i++) {", "for (int i = 0; i < 0; i++) {")],
 "D1": [("fog.glsl", "    float amb = ambVolFogPath(worldPos);", "    float amb = 1.0;")],
 "D2": [("fog.glsl", "    if (fogAtmos != 0) {\n        vec3 T;\n        vec3 add", "    if (false) {\n        vec3 T;\n        vec3 add")],
 "D3": [("fog.glsl", "    color = applyFogVolumes(color, worldPos);\n    float amb", "    float amb")],
}
for h in which.split(","):
    if h == "none": continue
    for f, a, b in H[h]:
        rep(f, a, b, 0)
for f, s in files.items():
    open(os.path.join(dstd, f), "w").write(s)
print("hacked", list(files))
