package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// ShadowMapSize is the resolution of each shadow map texture (square).
const ShadowMapSize = 2048

// PointShadowMapSize is the resolution of each cubemap face for point lights.
const PointShadowMapSize = 1024

// ShadowOrthoHalfSize controls the extent of the orthographic projection
// used to render the shadow map for directional lights.
const ShadowOrthoHalfSize = 50.0

// DefaultShadowBias is the depth bias used to reduce shadow acne.
const DefaultShadowBias = 0.0005

// ShadowMap holds GPU resources for a single directional-light shadow map.
type ShadowMap struct {
	FBO     uint32
	Texture uint32
}

// PointShadowMap holds GPU resources for a single point-light cubemap shadow.
type PointShadowMap struct {
	FBO     uint32
	Cubemap uint32
}

// ShadowState holds the shadow mapping pipeline state.
type ShadowState struct {
	// Directional light shadow program
	Program      uint32
	ModelUniform int32
	LSMUniform   int32
	Maps         []ShadowMap

	// Point light cubemap shadow program
	CubeProgram      uint32
	CubeModelUniform int32
	CubeLSMUniform   int32
	CubeLightPos     int32
	CubeFarPlane     int32
	PointMaps        []PointShadowMap

	// DummyCubemap is a 1x1 cubemap bound to unused pointShadowMap units so
	// that samplerCube uniforms never reference a texture unit holding a 2D
	// texture (which is an invalid operation in OpenGL and causes undefined
	// behavior on many drivers).
	DummyCubemap uint32
}

var Shadows ShadowState

// InitShadows compiles the depth-only shaders and prepares the shadow pipeline.
func InitShadows(shadowVertPath, shadowFragPath, cubeVertPath, cubeFragPath string) error {
	if err := loadShadowProgram(shadowVertPath, shadowFragPath); err != nil {
		return fmt.Errorf("shadow program: %w", err)
	}
	Shadows.ModelUniform = gl.GetUniformLocation(Shadows.Program, gl.Str("model\x00"))
	Shadows.LSMUniform = gl.GetUniformLocation(Shadows.Program, gl.Str("lightSpaceMatrix\x00"))
	log.Printf("dir shadow program linked OK (id=%d)", Shadows.Program)

	if err := loadCubeShadowProgram(cubeVertPath, cubeFragPath); err != nil {
		return fmt.Errorf("cube shadow program: %w", err)
	}
	Shadows.CubeModelUniform = gl.GetUniformLocation(Shadows.CubeProgram, gl.Str("model\x00"))
	Shadows.CubeLSMUniform = gl.GetUniformLocation(Shadows.CubeProgram, gl.Str("lightSpaceMatrix\x00"))
	Shadows.CubeLightPos = gl.GetUniformLocation(Shadows.CubeProgram, gl.Str("lightPos\x00"))
	Shadows.CubeFarPlane = gl.GetUniformLocation(Shadows.CubeProgram, gl.Str("farPlane\x00"))
	log.Printf("cube shadow program linked OK (id=%d)", Shadows.CubeProgram)

	// Create a 1x1 dummy cubemap so unused pointShadowMap sampler uniforms
	// always reference a valid cubemap texture instead of defaulting to unit 0
	// (which holds a sampler2D). A samplerCube reading from a unit with a 2D
	// texture bound is an invalid GL operation and causes undefined behavior.
	Shadows.DummyCubemap = createDummyCubemap()
	log.Printf("dummy cubemap created (id=%d)", Shadows.DummyCubemap)

	return nil
}

func loadShadowProgram(vertPath, fragPath string) error {
	return linkProgram(vertPath, fragPath, &Shadows.Program)
}

func loadCubeShadowProgram(vertPath, fragPath string) error {
	return linkProgram(vertPath, fragPath, &Shadows.CubeProgram)
}

func linkProgram(vertPath, fragPath string, out *uint32) error {
	vertSrc, err := readShaderFile(vertPath)
	if err != nil {
		return err
	}
	fragSrc, err := readShaderFile(fragPath)
	if err != nil {
		return err
	}
	vs, err := CompileShader(vertSrc, gl.VERTEX_SHADER)
	if err != nil {
		return err
	}
	fs, err := CompileShader(fragSrc, gl.FRAGMENT_SHADER)
	if err != nil {
		return err
	}
	*out = gl.CreateProgram()
	gl.AttachShader(*out, vs)
	gl.AttachShader(*out, fs)
	gl.LinkProgram(*out)

	var status int32
	gl.GetProgramiv(*out, gl.LINK_STATUS, &status)
	if status == gl.FALSE {
		var logLen int32
		gl.GetProgramiv(*out, gl.INFO_LOG_LENGTH, &logLen)
		infoLog := strings.Repeat("\x00", int(logLen+1))
		gl.GetProgramInfoLog(*out, logLen, nil, gl.Str(infoLog))
		return fmt.Errorf("program link: %s", infoLog)
	}
	gl.DeleteShader(vs)
	gl.DeleteShader(fs)
	return nil
}

func readShaderFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(strings.TrimRight(string(data), "\n"), "\r\n", "\n"), nil
}

// ---------- Directional shadow maps ----------

func EnsureShadowMaps(count int) {
	for len(Shadows.Maps) < count {
		sm := createShadowMap()
		Shadows.Maps = append(Shadows.Maps, sm)
		log.Printf("created dir shadow map %d (tex=%d, fbo=%d)", len(Shadows.Maps)-1, sm.Texture, sm.FBO)
	}
	for len(Shadows.Maps) > count {
		sm := Shadows.Maps[len(Shadows.Maps)-1]
		gl.DeleteFramebuffers(1, &sm.FBO)
		gl.DeleteTextures(1, &sm.Texture)
		Shadows.Maps = Shadows.Maps[:len(Shadows.Maps)-1]
	}
}

func createShadowMap() ShadowMap {
	var sm ShadowMap
	gl.GenTextures(1, &sm.Texture)
	gl.BindTexture(gl.TEXTURE_2D, sm.Texture)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.DEPTH_COMPONENT24,
		ShadowMapSize, ShadowMapSize, 0,
		gl.DEPTH_COMPONENT, gl.FLOAT, nil)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_BORDER)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_BORDER)
	borderColor := [4]float32{1, 1, 1, 1}
	gl.TexParameterfv(gl.TEXTURE_2D, gl.TEXTURE_BORDER_COLOR, &borderColor[0])

	gl.GenFramebuffers(1, &sm.FBO)
	gl.BindFramebuffer(gl.FRAMEBUFFER, sm.FBO)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT, gl.TEXTURE_2D, sm.Texture, 0)
	gl.DrawBuffer(gl.NONE)
	gl.ReadBuffer(gl.NONE)
	if st := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); st != gl.FRAMEBUFFER_COMPLETE {
		log.Printf("WARNING: dir shadow FBO incomplete (status=0x%X)", st)
	}
	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
	return sm
}

// createDummyCubemap allocates a 1x1 cubemap with depth=1.0 on all faces.
func createDummyCubemap() uint32 {
	var tex uint32
	gl.GenTextures(1, &tex)
	gl.BindTexture(gl.TEXTURE_CUBE_MAP, tex)
	pixel := []float32{1.0}
	for face := 0; face < 6; face++ {
		gl.TexImage2D(uint32(gl.TEXTURE_CUBE_MAP_POSITIVE_X+face), 0, gl.DEPTH_COMPONENT24,
			1, 1, 0, gl.DEPTH_COMPONENT, gl.FLOAT, gl.Ptr(pixel))
	}
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_R, gl.CLAMP_TO_EDGE)
	gl.BindTexture(gl.TEXTURE_CUBE_MAP, 0)
	return tex
}

// ---------- Point light cubemap shadow maps ----------

func EnsurePointShadowMaps(count int) {
	for len(Shadows.PointMaps) < count {
		pm := createPointShadowMap()
		Shadows.PointMaps = append(Shadows.PointMaps, pm)
		log.Printf("created point shadow map %d (cube=%d, fbo=%d)", len(Shadows.PointMaps)-1, pm.Cubemap, pm.FBO)
	}
	for len(Shadows.PointMaps) > count {
		pm := Shadows.PointMaps[len(Shadows.PointMaps)-1]
		gl.DeleteFramebuffers(1, &pm.FBO)
		gl.DeleteTextures(1, &pm.Cubemap)
		Shadows.PointMaps = Shadows.PointMaps[:len(Shadows.PointMaps)-1]
	}
}

func createPointShadowMap() PointShadowMap {
	var pm PointShadowMap
	gl.GenTextures(1, &pm.Cubemap)
	gl.BindTexture(gl.TEXTURE_CUBE_MAP, pm.Cubemap)
	for face := 0; face < 6; face++ {
		gl.TexImage2D(uint32(gl.TEXTURE_CUBE_MAP_POSITIVE_X+face), 0, gl.DEPTH_COMPONENT24,
			PointShadowMapSize, PointShadowMapSize, 0,
			gl.DEPTH_COMPONENT, gl.FLOAT, nil)
	}
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_CUBE_MAP, gl.TEXTURE_WRAP_R, gl.CLAMP_TO_EDGE)

	gl.GenFramebuffers(1, &pm.FBO)
	gl.BindFramebuffer(gl.FRAMEBUFFER, pm.FBO)
	// Attach the first face initially; we'll switch per-face during rendering.
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT,
		gl.TEXTURE_CUBE_MAP_POSITIVE_X, pm.Cubemap, 0)
	gl.DrawBuffer(gl.NONE)
	gl.ReadBuffer(gl.NONE)
	if st := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); st != gl.FRAMEBUFFER_COMPLETE {
		log.Printf("WARNING: point shadow FBO incomplete (status=0x%X)", st)
	}
	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
	return pm
}

// cubeFaceViews returns the 6 view matrices for a cubemap centered at pos.
// Order: +X, -X, +Y, -Y, +Z, -Z (matches GL_TEXTURE_CUBE_MAP_POSITIVE_X + i).
func cubeFaceViews(pos mgl32.Vec3) [6]mgl32.Mat4 {
	return [6]mgl32.Mat4{
		mgl32.LookAtV(pos, pos.Add(mgl32.Vec3{1, 0, 0}), mgl32.Vec3{0, -1, 0}),
		mgl32.LookAtV(pos, pos.Add(mgl32.Vec3{-1, 0, 0}), mgl32.Vec3{0, -1, 0}),
		mgl32.LookAtV(pos, pos.Add(mgl32.Vec3{0, 1, 0}), mgl32.Vec3{0, 0, 1}),
		mgl32.LookAtV(pos, pos.Add(mgl32.Vec3{0, -1, 0}), mgl32.Vec3{0, 0, -1}),
		mgl32.LookAtV(pos, pos.Add(mgl32.Vec3{0, 0, 1}), mgl32.Vec3{0, -1, 0}),
		mgl32.LookAtV(pos, pos.Add(mgl32.Vec3{0, 0, -1}), mgl32.Vec3{0, -1, 0}),
	}
}

// ---------- Light-space matrix helpers ----------

func DirLightMatrix(dir mgl32.Vec3, focusPoint mgl32.Vec3) mgl32.Mat4 {
	lightDir := dir.Normalize()
	lightPos := focusPoint.Sub(lightDir.Mul(ShadowOrthoHalfSize))
	lightView := mgl32.LookAtV(lightPos, focusPoint, mgl32.Vec3{0, 1, 0})
	if abs32(lightDir.Y()) > 0.99 {
		lightView = mgl32.LookAtV(lightPos, focusPoint, mgl32.Vec3{0, 0, 1})
	}
	h := float32(ShadowOrthoHalfSize)
	lightProj := mgl32.Ortho(-h, h, -h, h, 0.1, ShadowOrthoHalfSize*3)
	return lightProj.Mul4(lightView)
}

// ---------- Shadow passes ----------

// RenderShadowPass renders directional shadow maps.
// Returns light-space matrices indexed to match the directional light array.
func RenderShadowPass(lights LightSet, level *Level, focusPoint mgl32.Vec3) []mgl32.Mat4 {
	matrices := make([]mgl32.Mat4, len(lights.Directional))

	shadowCount := 0
	for _, dl := range lights.Directional {
		if dl.CastShadows {
			shadowCount++
		}
	}
	EnsureShadowMaps(shadowCount)

	if shadowCount == 0 {
		for i := range matrices {
			matrices[i] = mgl32.Ident4()
		}
		return matrices
	}

	gl.UseProgram(Shadows.Program)
	var prevViewport [4]int32
	gl.GetIntegerv(gl.VIEWPORT, &prevViewport[0])
	gl.Viewport(0, 0, ShadowMapSize, ShadowMapSize)
	gl.Disable(gl.CULL_FACE)
	gl.Enable(gl.POLYGON_OFFSET_FILL)
	gl.PolygonOffset(1.1, 4.0)

	mapIdx := 0
	for i, dl := range lights.Directional {
		if !dl.CastShadows {
			matrices[i] = mgl32.Ident4()
			continue
		}
		if mapIdx >= len(Shadows.Maps) {
			break
		}

		lsm := DirLightMatrix(dl.Direction, focusPoint)
		matrices[i] = lsm

		sm := Shadows.Maps[mapIdx]
		gl.BindFramebuffer(gl.FRAMEBUFFER, sm.FBO)
		gl.Clear(gl.DEPTH_BUFFER_BIT)
		gl.UniformMatrix4fv(Shadows.LSMUniform, 1, false, &lsm[0])

		renderSceneShadow(level, Shadows.ModelUniform)
		mapIdx++
	}

	gl.Disable(gl.POLYGON_OFFSET_FILL)
	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
	gl.Viewport(prevViewport[0], prevViewport[1], prevViewport[2], prevViewport[3])
	gl.UseProgram(Program)

	return matrices
}

// RenderPointShadowPass renders cubemap shadow maps for point lights.
func RenderPointShadowPass(lights LightSet, level *Level) {
	shadowCount := 0
	for _, pl := range lights.Points {
		if pl.CastShadows {
			shadowCount++
		}
	}
	EnsurePointShadowMaps(shadowCount)

	if shadowCount == 0 {
		return
	}

	gl.UseProgram(Shadows.CubeProgram)
	var prevViewport [4]int32
	gl.GetIntegerv(gl.VIEWPORT, &prevViewport[0])
	gl.Viewport(0, 0, PointShadowMapSize, PointShadowMapSize)
	gl.Disable(gl.CULL_FACE)
	gl.Enable(gl.POLYGON_OFFSET_FILL)
	gl.PolygonOffset(1.1, 4.0)

	mapIdx := 0
	for _, pl := range lights.Points {
		if !pl.CastShadows {
			continue
		}
		if mapIdx >= len(Shadows.PointMaps) {
			break
		}

		pm := Shadows.PointMaps[mapIdx]
		farPlane := pl.Radius
		proj := mgl32.Perspective(mgl32.DegToRad(90.0), 1.0, 0.1, farPlane)
		views := cubeFaceViews(pl.Position)

		gl.Uniform3f(Shadows.CubeLightPos, pl.Position[0], pl.Position[1], pl.Position[2])
		gl.Uniform1f(Shadows.CubeFarPlane, farPlane)

		gl.BindFramebuffer(gl.FRAMEBUFFER, pm.FBO)
		for face := 0; face < 6; face++ {
			gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.DEPTH_ATTACHMENT,
				uint32(gl.TEXTURE_CUBE_MAP_POSITIVE_X+face), pm.Cubemap, 0)
			gl.Clear(gl.DEPTH_BUFFER_BIT)

			lsm := proj.Mul4(views[face])
			gl.UniformMatrix4fv(Shadows.CubeLSMUniform, 1, false, &lsm[0])

			renderSceneShadow(level, Shadows.CubeModelUniform)
		}
		mapIdx++
	}

	gl.Disable(gl.POLYGON_OFFSET_FILL)
	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
	gl.Viewport(prevViewport[0], prevViewport[1], prevViewport[2], prevViewport[3])
	gl.UseProgram(Program)
}

// renderSceneShadow draws all scene geometry into the currently bound FBO
// using the given model uniform location.
func renderSceneShadow(level *Level, modelUniform int32) {
	identity := mgl32.Ident4()
	gl.UniformMatrix4fv(modelUniform, 1, false, &identity[0])

	for ci := range level.Chunks {
		chunk := &level.Chunks[ci]
		for si := range chunk.Sectors {
			sector := &chunk.Sectors[si]
			if sector.Floor.Enabled {
				drawGeometryShadow(&sector.Floor.Geometry)
				for eg := range sector.Floor.ExtrusionGeometries {
					drawGeometryShadow(&sector.Floor.ExtrusionGeometries[eg])
				}
			}
			if sector.Ceiling.Enabled {
				drawGeometryShadow(&sector.Ceiling.Geometry)
				for eg := range sector.Ceiling.ExtrusionGeometries {
					drawGeometryShadow(&sector.Ceiling.ExtrusionGeometries[eg])
				}
			}
			for wi := range sector.Walls {
				w := &sector.Walls[wi]
				if w.Visible {
					drawGeometryShadow(&w.Geometry)
					for eg := range w.ExtrusionGeometries {
						drawGeometryShadow(&w.ExtrusionGeometries[eg])
					}
				}
			}
			for pi := range sector.Portals {
				p := &sector.Portals[pi]
				if p.Visible && p.DrawGap {
					drawGeometryShadow(&p.FloorGap)
					drawGeometryShadow(&p.CeilGap)
				}
			}
		}
	}

	// Entities (dynamic objects)
	for ei := range level.Entities {
		e := &level.Entities[ei]
		if e.Visible {
			model := e.ModelMatrix()
			gl.UniformMatrix4fv(modelUniform, 1, false, &model[0])
			drawGeometryShadow(&e.Geometry)
		}
	}

	// Primitives (static procedural objects)
	for ci := range level.Chunks {
		chunk := &level.Chunks[ci]
		for pi := range chunk.Primitives {
			p := &chunk.Primitives[pi]
			model := p.ModelMatrix()
			gl.UniformMatrix4fv(modelUniform, 1, false, &model[0])
			drawGeometryShadow(&p.Geometry)
		}
	}
}

func drawGeometryShadow(g *Geometry) {
	if len(g.Vertices) == 0 {
		return
	}
	gl.BindVertexArray(g.Vao)
	gl.DrawArrays(gl.TRIANGLES, 0, int32(len(g.Vertices)/5))
}

// ---------- Uniform upload ----------

// UploadShadowUniforms sends directional shadow data to the main shader.
func UploadShadowUniforms(lights LightSet, matrices []mgl32.Mat4) {
	gl.UseProgram(Program)
	gl.Uniform1f(uniformLoc("shadowBias\x00"), DefaultShadowBias)

	mapIdx := 0
	for i, dl := range lights.Directional {
		if i >= MaxDirLights {
			break
		}
		lsmName := fmt.Sprintf("lightSpaceMatrix[%d]\x00", i)
		m := matrices[i]
		gl.UniformMatrix4fv(uniformLoc(lsmName), 1, false, &m[0])

		castName := fmt.Sprintf("dirLightCastShadows[%d]\x00", i)
		castVal := int32(0)
		if dl.CastShadows {
			castVal = 1
		}
		gl.Uniform1i(uniformLoc(castName), castVal)

		strengthName := fmt.Sprintf("dirLightShadowStrength[%d]\x00", i)
		gl.Uniform1f(uniformLoc(strengthName), dl.ShadowStrength)

		if dl.CastShadows && mapIdx < len(Shadows.Maps) {
			texUnit := int32(1 + i)
			gl.ActiveTexture(uint32(gl.TEXTURE0 + texUnit))
			gl.BindTexture(gl.TEXTURE_2D, Shadows.Maps[mapIdx].Texture)
			smName := fmt.Sprintf("shadowMap[%d]\x00", i)
			gl.Uniform1i(uniformLoc(smName), texUnit)
			mapIdx++
		}
	}
	gl.ActiveTexture(gl.TEXTURE0)
}

// UploadPointShadowUniforms sends point light shadow cubemaps to the main shader.
// Texture units start after directional shadow maps (1..MaxDirLights are dir, then point).
func UploadPointShadowUniforms(lights LightSet) {
	gl.UseProgram(Program)

	baseUnit := int32(1 + MaxDirLights) // units 1-4 reserved for dir shadow maps

	// Bind the dummy cubemap to ALL pointShadowMap slots (not just active
	// lights) so that every samplerCube uniform references a unit with a valid
	// cubemap. Unassigned samplerCube uniforms default to unit 0 which holds
	// the diffuse sampler2D — a type mismatch that causes undefined behavior.
	for i := 0; i < MaxPointLights; i++ {
		texUnit := baseUnit + int32(i)
		gl.ActiveTexture(uint32(gl.TEXTURE0 + texUnit))
		gl.BindTexture(gl.TEXTURE_CUBE_MAP, Shadows.DummyCubemap)
		smName := fmt.Sprintf("pointShadowMap[%d]\x00", i)
		gl.Uniform1i(uniformLoc(smName), texUnit)
	}

	numPt := len(lights.Points)
	if numPt > MaxPointLights {
		numPt = MaxPointLights
	}
	mapIdx := 0
	for i := 0; i < numPt; i++ {
		pl := lights.Points[i]
		castName := fmt.Sprintf("pointLightCastShadows[%d]\x00", i)
		castVal := int32(0)
		if pl.CastShadows {
			castVal = 1
		}
		gl.Uniform1i(uniformLoc(castName), castVal)

		strengthName := fmt.Sprintf("pointLightShadowStrength[%d]\x00", i)
		gl.Uniform1f(uniformLoc(strengthName), pl.ShadowStrength)

		farName := fmt.Sprintf("pointLightFarPlane[%d]\x00", i)
		gl.Uniform1f(uniformLoc(farName), pl.Radius)

		if pl.CastShadows && mapIdx < len(Shadows.PointMaps) {
			texUnit := baseUnit + int32(i)
			gl.ActiveTexture(uint32(gl.TEXTURE0 + texUnit))
			gl.BindTexture(gl.TEXTURE_CUBE_MAP, Shadows.PointMaps[mapIdx].Cubemap)
			mapIdx++
		}
	}
	gl.ActiveTexture(gl.TEXTURE0)
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}
