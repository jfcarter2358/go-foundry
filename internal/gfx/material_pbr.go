package gfx

// material_pbr.go — the specular's per-surface inputs (specular.glsl;
// docs/lighting/PLAN.md, phase 1): roughness, metalness, a dielectric's
// specular level and the ARM map, as SurfaceState carries them to the world
// program.
//
// The uniforms are BIASES from the defaults (material.DefaultPBRRoughness,
// DefaultPBRSpecular; metallic 0), the rule tintBias and uvScaleBias follow:
// zero — what GL gives an unwritten uniform — is a surface with no pbr effect.

import (
	"foundry/internal/material"

	"github.com/go-gl/gl/v4.3-core/gl"
)

// PBRParams is a surface's specular inputs. The zero value is NOT the default
// surface: use DefaultPBR.
type PBRParams struct {
	Roughness, Metallic, Specular float32
	// ARM names the material's own ARM map, "" for the texture's <name>_arm.
	ARM string
	// FromMaterial is whether a pbr effect set these. Without one a surface
	// whose texture has an ARM map takes the map's roughness and metalness
	// as they are (factors of 1, glTF's defaults) rather than scaled by the
	// default roughness.
	FromMaterial bool
}

// DefaultPBR is a surface without a pbr effect.
func DefaultPBR() PBRParams {
	return PBRParams{Roughness: material.DefaultPBRRoughness, Specular: material.DefaultPBRSpecular}
}

// pbrFromMaterial is the pbr effect's parameters.
func pbrFromMaterial(m material.Surface) PBRParams {
	return PBRParams{Roughness: m.Roughness, Metallic: m.Metallic, Specular: m.Specular, ARM: m.ARM, FromMaterial: true}
}

// PBRLocs is a program's specular uniform locations.
type PBRLocs struct {
	rough, metal, spec, hasArm, arm int32
}

// NewPBRLocs resolves prog's PBRLocs.
func NewPBRLocs(prog uint32) PBRLocs {
	return PBRLocs{
		rough:  UniformLoc(prog, "pbrRoughnessBias\x00"),
		metal:  UniformLoc(prog, "pbrMetallic\x00"),
		spec:   UniformLoc(prog, "pbrSpecularBias\x00"),
		hasArm: UniformLoc(prog, "hasArmMap\x00"),
		arm:    UniformLoc(prog, "armMap\x00"),
	}
}

var programPBR struct {
	l    PBRLocs
	prog uint32
	gen  uint64
	ok   bool
}

// programPBRLocs is Program's PBRLocs, resolved once a link.
func programPBRLocs() *PBRLocs {
	if !programPBR.ok || programPBR.prog != Program || programPBR.gen != linkGeneration {
		programPBR.l = NewPBRLocs(Program)
		programPBR.prog, programPBR.gen, programPBR.ok = Program, linkGeneration, true
	}
	return &programPBR.l
}

// applyPBR writes a surface's specular inputs, the ARM map of the texture it
// wears bound beside them.
//
// fx is the surface's effects: fragment.glsl never samples the ARM map under
// a triplanar or splat (its uv is not the texture's projection there), so
// such a surface neither binds nor loads one (the adventure ruins once shaded
// as bare metal for it; docs/adventure/PHASE_F.md).
//
// The factors are always sent as they are — a surface with no pbr effect is a
// dielectric at the defaults — and hasArmMap's mode says whether a sampled
// map multiplies them or stands in for them (ARMReplaces, no pbr effect). So
// a map that is bound and not sampled changes nothing.
func applyPBR(p PBRParams, texture string, fx *material.SurfaceFx, l *PBRLocs) {
	arm := ""
	if !fx.HasTriplanar && !fx.HasSplat {
		arm = ARMMapFor(texture, p.ARM)
	}
	mode := int32(ARMScales)
	if !p.FromMaterial {
		mode = ARMReplaces
	}
	gl.Uniform1f(l.rough, p.Roughness-material.DefaultPBRRoughness)
	gl.Uniform1f(l.metal, p.Metallic)
	gl.Uniform1f(l.spec, p.Specular-material.DefaultPBRSpecular)
	BindARMMap(arm, mode, l.hasArm, l.arm)
}

// ResetPBRUniforms writes a surface with no pbr effect and no ARM map, for a
// draw path with no material of its own (a line list).
func ResetPBRUniforms() {
	l := programPBRLocs()
	gl.Uniform1f(l.rough, 0)
	gl.Uniform1f(l.metal, 0)
	gl.Uniform1f(l.spec, 0)
	gl.Uniform1i(l.hasArm, 0)
}
