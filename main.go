package main

import (
	"io"
	_ "image/png"
	"log"
	"math"
	"os"
	"path/filepath"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// textureNames maps logical texture names to paths relative to executablePath.
var textureNames = map[string]string{
	"square":      "textures/square.png",
	"grass":       "textures/grass.png",
	"dirt":        "textures/dirt.png",
	"rock":        "textures/rock.png",
	"brick":       "textures/brick.png",
	"stone_floor": "textures/stone_floor.png",
	"wood_floor":  "textures/wood_floor.png",
	"concrete":    "textures/concrete.png",
	"stucco":      "textures/stucco.png",
}

var Textures = map[string]uint32{}
var vertexShaderPath = "shaders/vertex.glsl"
var fragmentShaderPath = "shaders/fragment.glsl"
var shadowVertShaderPath = "shaders/shadow_vert.glsl"
var shadowFragShaderPath = "shaders/shadow_frag.glsl"
var shadowCubeVertPath = "shaders/shadow_cube_vert.glsl"
var shadowCubeFragPath = "shaders/shadow_cube_frag.glsl"
var Running = true
var Program uint32
var KeyStates []uint8
var event sdl.Event
var WinTitle string = "Foundry Engine"
var WinWidth, WinHeight int32 = 800, 600
var WinHalfHeight int32 = WinHeight / 2
var WinHalfWidth int32 = WinWidth / 2
var Window *sdl.Window
var Context sdl.GLContext

// executablePath is the directory containing the game executable.
// Used by LoadLevel to resolve relative asset paths.
var executablePath string

// PlayerEntity points to the entity with ID "player" in the current level.
// Global so that EditorLoadLevel can update it when a new level is loaded.
var PlayerEntity *Entity

// Projection is the perspective matrix; kept global for frustum extraction.
var Projection mgl32.Mat4

// ProjectionUniform is the shader uniform location for the projection matrix.
// Kept global so RefreshWindowSize can re-upload it after a resize or fullscreen toggle.
var ProjectionUniform int32

// eyeHeight is the camera offset above the player entity's Y position.
const eyeHeight = float32(1.7)

// DeltaTime is the elapsed time in seconds since the last frame.
// Clamped to [0.001, 0.05] to prevent physics instability.
var DeltaTime float32 = 1.0 / 60.0
var lastFrameTick uint32

func main() {
	Textures = make(map[string]uint32)

	executable, err := os.Executable()
	if err != nil {
		// Can't get path — write to stderr only and exit.
		log.Fatalf("os.Executable: %v", err)
	}
	executablePath = filepath.Dir(executable)

	// [SCENARIO 1/4/5] Open a persistent log file next to the executable so
	// startup crashes are visible on Windows (where the console closes instantly).
	logPath := filepath.Join(executablePath, "foundry.log")
	logFile, ferr := os.Create(logPath)
	if ferr != nil {
		log.Printf("WARNING: could not create log file %s: %v — logging to stderr only", logPath, ferr)
	} else {
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
		// os.File writes are unbuffered; entries reach the file even after os.Exit.
	}
	log.SetFlags(log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[foundry] ")

	// Catch any unexpected panic and write it to the log before exiting.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC: %v", r)
			os.Exit(1)
		}
	}()

	log.Printf("executable directory: %s", executablePath)

	if err = sdl.Init(sdl.INIT_EVERYTHING); err != nil {
		log.Fatalf("sdl.Init: %v", err)
	}
	defer sdl.Quit()

	Window, err = sdl.CreateWindow(WinTitle, sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED,
		WinWidth, WinHeight, sdl.WINDOW_OPENGL|sdl.WINDOW_RESIZABLE)
	if err != nil {
		log.Fatalf("sdl.CreateWindow: %v", err)
	}
	defer Window.Destroy()
	Context, err = Window.GLCreateContext()
	if err != nil {
		log.Fatalf("Window.GLCreateContext: %v", err)
	}
	defer sdl.GLDeleteContext(Context)

	if err = gl.Init(); err != nil {
		log.Fatalf("gl.Init: %v", err)
	}

	// [SCENARIO 2 & 3] Log the OpenGL and GLSL versions so we know exactly what
	// the driver exposes.  A version < 4.1 will reject #version 410 shaders and
	// dFdx/dFdy may be unsupported on some older/integrated drivers.
	log.Printf("OpenGL version : %s", gl.GoStr(gl.GetString(gl.VERSION)))
	log.Printf("GLSL version   : %s", gl.GoStr(gl.GetString(gl.SHADING_LANGUAGE_VERSION)))
	log.Printf("GPU renderer   : %s", gl.GoStr(gl.GetString(gl.RENDERER)))

	gl.Enable(gl.DEPTH_TEST)
	gl.ClearColor(0.2, 0.2, 0.3, 1.0)
	gl.ClearDepth(1)
	gl.DepthFunc(gl.LEQUAL)
	gl.Viewport(0, 0, int32(WinWidth), int32(WinHeight))

	// [SCENARIO 4] Check that shader files actually exist on disk before
	// attempting to load them — catches stale/missing dist files immediately.
	vShaderFull := filepath.Join(executablePath, vertexShaderPath)
	fShaderFull := filepath.Join(executablePath, fragmentShaderPath)
	for _, p := range []string{vShaderFull, fShaderFull} {
		if info, serr := os.Stat(p); serr != nil {
			log.Printf("MISSING shader file: %s (%v)", p, serr)
		} else {
			log.Printf("shader file OK: %s (%d bytes)", p, info.Size())
		}
	}

	// [SCENARIO 1] Shader compilation / linking errors are returned here; a
	// failure causes an immediate fatal log so the reason is preserved in the
	// log file before the process exits.
	if err := LoadProgram(vShaderFull, fShaderFull); err != nil {
		log.Fatalf("LoadProgram: %v", err)
	}
	log.Printf("shader program linked OK (id=%d)", Program)
	gl.UseProgram(Program)

	// Initialize shadow mapping pipeline (depth-only shaders + FBOs).
	svShaderFull := filepath.Join(executablePath, shadowVertShaderPath)
	sfShaderFull := filepath.Join(executablePath, shadowFragShaderPath)
	scvShaderFull := filepath.Join(executablePath, shadowCubeVertPath)
	scfShaderFull := filepath.Join(executablePath, shadowCubeFragPath)
	if err := InitShadows(svShaderFull, sfShaderFull, scvShaderFull, scfShaderFull); err != nil {
		log.Fatalf("InitShadows: %v", err)
	}

	Projection = mgl32.Perspective(mgl32.DegToRad(45.0), float32(WinWidth)/float32(WinHeight), 0.1, 1000.0)
	ProjectionUniform = gl.GetUniformLocation(Program, gl.Str("projection\x00"))
	gl.UniformMatrix4fv(ProjectionUniform, 1, false, &Projection[0])

	// Default camera position; overridden after player entity is found
	LoadCamera(mgl32.Vec3{0, eyeHeight, 3}, 0.0, float32(math.Pi/2.0))

	// LoadLevel handles geometry upload and texture loading
	levelPath := filepath.Join(executablePath, "levels/test.yaml")
	log.Printf("loading level: %s", levelPath)
	level, err := LoadLevel(levelPath)
	if err != nil {
		log.Fatalf("LoadLevel: %v", err)
	}
	log.Printf("level loaded: %d chunk(s), %d entity(s), %d dir light(s), %d point light(s)",
		len(level.Chunks), len(level.Entities),
		len(level.Lights.Directional), len(level.Lights.Points))

	gl.BindFragDataLocation(Program, 0, gl.Str("outputColor\x00"))

	// [SCENARIO 5] UploadLights logs any uniform that the shader doesn't expose
	// (-1 location) so mismatches between Go-side light data and shader uniforms
	// are immediately visible in the log.
	UploadLights(level.Lights)

	// Attach camera to the player entity (ID == "player")
	for i := range level.Entities {
		if level.Entities[i].ID == "player" {
			PlayerEntity = &level.Entities[i]
			break
		}
	}
	if PlayerEntity != nil {
		Cam.Position = PlayerEntity.Position.Add(mgl32.Vec3{0, eyeHeight, 0})
		Cam.Matrix = mgl32.LookAtV(Cam.Position, Cam.Position.Add(Cam.Direction), WorldUp)
		gl.UniformMatrix4fv(Cam.Uniform, 1, false, &Cam.Matrix[0])
	}

	// Init level editor (must be after LoadLevel and OpenGL context ready)
	EditorInit(&level, levelPath)
	defer ImGuiShutdown()

	// Cache total sector count (constant after load) for debug stats.
	totalSectors := 0
	for i := range level.Chunks {
		totalSectors += len(level.Chunks[i].Sectors)
	}

	log.Printf("startup complete — entering game loop")

	// Drain any events that accumulated during startup (level load, window creation,
	// etc.) so the first game-loop poll doesn't see a huge mouse-motion delta.
	sdl.PumpEvents()
	for e := sdl.PollEvent(); e != nil; e = sdl.PollEvent() {
	}
	lastFrameTick = sdl.GetTicks()

	for Running {
		// Compute delta time (seconds), clamped to avoid spiral-of-death
		now := sdl.GetTicks()
		DeltaTime = float32(now-lastFrameTick) / 1000.0
		lastFrameTick = now
		if DeltaTime < 0.001 {
			DeltaTime = 0.001
		}
		if DeltaTime > 0.05 {
			DeltaTime = 0.05
		}

		sdl.PumpEvents()

		// --- Process events first so mouse rotation is applied before movement ---
		for event = sdl.PollEvent(); event != nil; event = sdl.PollEvent() {
			if Editor.Active {
				EditorHandleEvent(event)
			}
			HandleEvent(event)
		}

		KeyStates = sdl.GetKeyboardState()

		if Editor.Active {
			// Editor owns camera and input; gameplay logic is bypassed.
			EditorTick(KeyStates)
		} else {
			// --- Input: let HandleInput update Cam.Position normally ---
			preMovePos := Cam.Position
			HandleInput(KeyStates)

			// --- Player entity update + collision ---
			if PlayerEntity != nil {
				delta := Cam.Position.Sub(preMovePos)
				newPlayerPos := PlayerEntity.Position.Add(delta)

				// Apply pending jump impulse before gravity so the jump
				// frame isn't immediately reduced by one tick of gravity.
				if JumpRequested {
					PlayerEntity.Velocity[1] = JumpVelocity
					JumpRequested = false
					IsOnGround = false
				}

				// Apply gravity: accelerate downward, clamp to terminal velocity.
				// Scale by DeltaTime*60 to normalize to 60 FPS baseline.
				dtScale := DeltaTime * 60.0
				if GravityEnabled {
					PlayerEntity.Velocity[1] -= GravityAccel * dtScale
					if PlayerEntity.Velocity[1] < -MaxFallSpeed {
						PlayerEntity.Velocity[1] = -MaxFallSpeed
					}
				}
				newPlayerPos[1] += PlayerEntity.Velocity[1] * dtScale

				chunk, sector := FindChunkAndSectorSH(level.Spatial, level.Chunks, newPlayerPos)
				newPlayerPos = ResolveWallCollision(PlayerEntity, chunk, sector, newPlayerPos)
				newPlayerPos = ResolveSlopeCollision(PlayerEntity, chunk, sector, PlayerEntity.Position, newPlayerPos)
				// Re-find sector after slope adjustment (player may have slid to a different sector)
				chunk, sector = FindChunkAndSectorSH(level.Spatial, level.Chunks, newPlayerPos)
				hitFloor, hitCeiling := ResolveFloorCollision(PlayerEntity, chunk, sector, &newPlayerPos)

				// Entity-entity collision: push player out of overlapping entities
				for i := range level.Entities {
					other := &level.Entities[i]
					if other == PlayerEntity {
						continue
					}
					if EntityOverlap(PlayerEntity, other, newPlayerPos) {
						newPlayerPos = ResolveEntityCollision(PlayerEntity, other, newPlayerPos)
					}
				}

				// Landing/bonk: reset vertical velocity on floor or ceiling contact.
				if GravityEnabled {
					if hitFloor {
						PlayerEntity.Velocity[1] = 0
						IsOnGround = true
					} else {
						IsOnGround = false
					}
					if hitCeiling && PlayerEntity.Velocity[1] > 0 {
						PlayerEntity.Velocity[1] = 0
					}
				}

				PlayerEntity.Position = newPlayerPos
				if chunk != nil {
					PlayerEntity.CurrentChunk = chunk.ID
				}
				if sector != nil {
					PlayerEntity.CurrentSector = sector.ID
				} else {
					PlayerEntity.CurrentSector = ""
				}
				Cam.Position = PlayerEntity.Position.Add(mgl32.Vec3{0, eyeHeight, 0})
				Cam.Matrix = mgl32.LookAtV(Cam.Position, Cam.Position.Add(Cam.Direction), WorldUp)
				gl.UniformMatrix4fv(Cam.Uniform, 1, false, &Cam.Matrix[0])
			}
		}

		// --- Debug stats: update CPU & memory ---
		DebugUpdateCPU()
		DebugUpdateMemory()
		DebugStats.FPS = 1.0 / DeltaTime
		DebugStats.FrameTimeMs = DeltaTime * 1000.0
		DebugStats.ChunksLoaded = len(level.Chunks)
		DebugStats.EntitiesLoaded = len(level.Entities)
		DebugStats.SectorsLoaded = totalSectors

		// --- Shadow pass: render depth from each shadow-casting light ---
		shadowMatrices := RenderShadowPass(level.Lights, &level, Cam.Position)
		UploadShadowUniforms(level.Lights, shadowMatrices)
		RenderPointShadowPass(level.Lights, &level)
		UploadPointShadowUniforms(level.Lights)

		// --- Render ---
		gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)

		// Build frustum from current projection × camera matrices
		projView := Projection.Mul4(Cam.Matrix)
		frustum := ExtractFrustum(projView)

		// Draw world geometry: chunks → sectors → floor/ceiling/walls
		chunksDrawn := 0
		sectorsDrawn := 0
		sectorsPortalCulled := 0
		entitiesDrawn := 0
		for i := range level.Chunks {
			chunk := &level.Chunks[i]
			if !AABBInFrustum(frustum, chunk.MinBound, chunk.MaxBound) {
				continue
			}
			chunksDrawn++

			// Portal visibility: only render sectors reachable through portals from the player's sector.
			// Sectors disconnected from the portal graph (e.g. nested sectors with no shared
			// edges) bypass portal culling and fall through to frustum-only culling.
			var visibleSectors map[string]bool
			var connectedSectors map[string]bool
			if PlayerEntity != nil && chunk.ID == PlayerEntity.CurrentChunk {
				visibleSectors = PortalVisibleSectors(chunk, PlayerEntity.CurrentSector, Cam.Position)
				connectedSectors = PortalConnectedSectors(chunk, PlayerEntity.CurrentSector)
			}

			for j := range chunk.Sectors {
				sector := &chunk.Sectors[j]
				if visibleSectors != nil && connectedSectors[sector.ID] && !visibleSectors[sector.ID] {
					sectorsPortalCulled++
					continue
				}
				sectorMin := mgl32.Vec3{sector.MinBound.X(), sector.Floor.Height, sector.MinBound.Y()}
				sectorMax := mgl32.Vec3{sector.MaxBound.X(), sector.Ceiling.Height, sector.MaxBound.Y()}
				if !AABBInFrustum(frustum, sectorMin, sectorMax) {
					continue
				}
				sectorsDrawn++
				if sector.Floor.Enabled {
					sector.Floor.Geometry.Draw()
					for eg := range sector.Floor.ExtrusionGeometries {
						sector.Floor.ExtrusionGeometries[eg].Draw()
					}
				}
				if sector.Ceiling.Enabled {
					sector.Ceiling.Geometry.Draw()
					for eg := range sector.Ceiling.ExtrusionGeometries {
						sector.Ceiling.ExtrusionGeometries[eg].Draw()
					}
				}
				for k := range sector.Walls {
					w := &sector.Walls[k]
					if w.Visible && (w.DoubleSided || WallFacingCamera(*w, Cam.Position)) {
						w.Geometry.Draw()
						for eg := range w.ExtrusionGeometries {
							w.ExtrusionGeometries[eg].Draw()
						}
					}
				}
				// Draw gap-fill geometry on portals connecting sectors at different heights.
				for k := range sector.Portals {
					p := &sector.Portals[k]
					if p.Visible && p.DrawGap && (p.DoubleSided || PortalFacingCamera(p.StartPoint, p.EndPoint, Cam.Position)) {
						p.FloorGap.Draw()
						p.CeilGap.Draw()
					}
				}
			}
		}

		// Draw visible entities with their transform (position, rotation, scale)
		for i := range level.Entities {
			e := &level.Entities[i]
			if e.Visible {
				e.Geometry.DrawWithTransform(e.ModelMatrix())
				entitiesDrawn++
			}
		}

		DebugStats.ChunksDrawn = chunksDrawn
		DebugStats.SectorsDrawn = sectorsDrawn
		DebugStats.SectorsPortalCulled = sectorsPortalCulled
		DebugStats.EntitiesDrawn = entitiesDrawn

		// Draw editor overlays and ImGui UI when editor is active
		if Editor.Active {
			EditorRender()
		}

		// Draw debug overlay (uses ImGui, independent of editor)
		if DebugOverlayVisible && !Editor.Active {
			ImGuiNewFrame(DeltaTime)
			DebugRenderOverlay()
			ImGuiRender()
		}

		Window.GLSwap()
	}
}
