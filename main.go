package main

import (
	"foundry/internal/biome"
	"foundry/internal/collision"
	"foundry/internal/detail"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/imguibackend"
	"foundry/internal/platform"
	"foundry/internal/render"
	"foundry/internal/texture"
	"foundry/internal/world"
	_ "image/png"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

// The texture editor drives a second SDL window and GL context on the same
// OS thread as the main loop (see window.go). SDL/GL calls must stay pinned
// to one OS thread — without this, the Go scheduler is free to migrate the
// goroutine between them and the "current" GL context becomes whichever
// thread last touched it.
func init() {
	runtime.LockOSThread()
}

// TransparentDraw holds a deferred draw call for a transparent geometry object.
// Collected during the opaque pass and drawn back-to-front afterward.
type TransparentDraw struct {
	Geom        *gfx.Geometry
	Model       mgl32.Mat4
	Distance    float32
	DoubleSided bool
}

var shadowVertShaderPath = "shaders/shadow_vert.glsl"
var shadowFragShaderPath = "shaders/shadow_frag.glsl"
var shadowCubeVertPath = "shaders/shadow_cube_vert.glsl"
var shadowCubeFragPath = "shaders/shadow_cube_frag.glsl"

func main() {
	gfx.Textures = make(map[string]uint32)

	executable, err := os.Executable()
	if err != nil {
		// Can't get path — write to stderr only and exit.
		log.Fatalf("os.Executable: %v", err)
	}
	fileutil.ExecutablePath = filepath.Dir(executable)

	// [SCENARIO 1/4/5] Open a persistent log file next to the executable so
	// startup crashes are visible on Windows (where the console closes instantly).
	logPath := filepath.Join(fileutil.ExecutablePath, "foundry.log")
	logFile, ferr := os.Create(logPath)
	var logSinks io.Writer = os.Stderr
	if ferr != nil {
		log.Printf("WARNING: could not create log file %s: %v — logging to stderr only", logPath, ferr)
	} else {
		logSinks = io.MultiWriter(os.Stderr, logFile)
		log.SetOutput(logSinks)
		// os.File writes are unbuffered; entries reach the file even after os.Exit.
	}
	log.SetFlags(log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[foundry] ")

	// Everything logged from here on is also readable in the log window, and a
	// script's own output joins the same sinks. Installed after the prefix and
	// flags are set, because the tee has to strip exactly what they add.
	InstallLogCapture(logSinks)

	gfx.TextureNames = gfx.ScanTextures(filepath.Join(fileutil.ExecutablePath, "textures"))
	log.Printf("scanned %d textures", len(gfx.TextureNames))

	// Merge the user's saved detail presets in over the compiled-in ones.
	detail.LoadDetailPresets()

	// Load (or, on a fresh checkout, create) the global 256-colour palette
	// every texture in the texture editor composites against.
	texture.LoadGlobalPalette()

	// Catch any unexpected panic and write it to the log before exiting.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC: %v", r)
			os.Exit(1)
		}
	}()

	log.Printf("executable directory: %s", fileutil.ExecutablePath)

	if err = sdl.Init(sdl.INIT_EVERYTHING); err != nil {
		log.Fatalf("sdl.Init: %v", err)
	}
	defer sdl.Quit()

	// Creates the window and its GL context together, at the highest MSAA
	// sample count the platform actually accepts — the multisample buffer is
	// part of the pixel format, so it has to be settled here and can't change
	// afterwards. See antialias.go for why this is window-level MSAA rather
	// than an offscreen resolve pass, and why the request is tiered.
	platform.Window, platform.Context, err = render.CreateAntiAliasedWindow(platform.WinTitle, platform.WinWidth, platform.WinHeight,
		sdl.WINDOW_OPENGL|sdl.WINDOW_RESIZABLE)
	if err != nil {
		log.Fatalf("CreateAntiAliasedWindow: %v", err)
	}
	defer platform.Window.Destroy()
	defer sdl.GLDeleteContext(platform.Context)
	// The window manager can hand back a size other than the one requested
	// above (scaling, tiling, a size clamped to the display); pick up what we
	// actually got so the viewport/projection setup below uses the real one.
	// F11 (ToggleFullscreen, input.go) still switches to borderless fullscreen.
	platform.WinWidth, platform.WinHeight = platform.Window.GetSize()
	platform.WinHalfWidth = platform.WinWidth / 2
	platform.WinHalfHeight = platform.WinHeight / 2
	if MainWindowID, err = platform.Window.GetID(); err != nil {
		log.Fatalf("Window.GetID: %v", err)
	}

	if err = gl.Init(); err != nil {
		log.Fatalf("gl.Init: %v", err)
	}

	// [SCENARIO 2 & 3] Log the OpenGL and GLSL versions so we know exactly what
	// the driver exposes.  A version < 4.1 will reject #version 410 shaders and
	// dFdx/dFdy may be unsupported on some older/integrated drivers.
	log.Printf("OpenGL version : %s", gl.GoStr(gl.GetString(gl.VERSION)))
	log.Printf("GLSL version   : %s", gl.GoStr(gl.GetString(gl.SHADING_LANGUAGE_VERSION)))
	log.Printf("GPU renderer   : %s", gl.GoStr(gl.GetString(gl.RENDERER)))

	// Reads back what CreateAntiAliasedWindow's request above actually got
	// granted and applies AntiAliasEnabled's default (on) — see antialias.go.
	render.ApplyAntiAliasGLState()

	// Resolves the anisotropic filtering level for world textures. Must come
	// before the first texture upload — LoadTexture reads it at upload time.
	// See texture_filter.go for why mipmapping is the half of anti-aliasing
	// MSAA above can't do.
	gfx.InitTextureFiltering()

	gl.Enable(gl.DEPTH_TEST)
	gl.ClearColor(0.2, 0.2, 0.3, 1.0)
	gl.ClearDepth(1)
	gl.DepthFunc(gl.LEQUAL)
	gl.Viewport(0, 0, int32(platform.WinWidth), int32(platform.WinHeight))

	// [SCENARIO 4] Check that shader files actually exist on disk before
	// attempting to load them — catches stale/missing dist files immediately.
	vShaderFull := filepath.Join(fileutil.ExecutablePath, gfx.VertexShaderPath)
	fShaderFull := filepath.Join(fileutil.ExecutablePath, gfx.FragmentShaderPath)
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
	if err := gfx.LoadProgram(vShaderFull, fShaderFull); err != nil {
		log.Fatalf("LoadProgram: %v", err)
	}
	log.Printf("shader program linked OK (id=%d)", gfx.Program)
	gl.UseProgram(gfx.Program)

	// Initialize shadow mapping pipeline (depth-only shaders + FBOs).
	svShaderFull := filepath.Join(fileutil.ExecutablePath, shadowVertShaderPath)
	sfShaderFull := filepath.Join(fileutil.ExecutablePath, shadowFragShaderPath)
	scvShaderFull := filepath.Join(fileutil.ExecutablePath, shadowCubeVertPath)
	scfShaderFull := filepath.Join(fileutil.ExecutablePath, shadowCubeFragPath)
	if err := render.InitShadows(svShaderFull, sfShaderFull, scvShaderFull, scfShaderFull); err != nil {
		log.Fatalf("InitShadows: %v", err)
	}

	gfx.Projection = mgl32.Perspective(mgl32.DegToRad(45.0), float32(platform.WinWidth)/float32(platform.WinHeight), 0.1, 1000.0)
	gfx.ProjectionUniform = gl.GetUniformLocation(gfx.Program, gl.Str("projection\x00"))
	gl.UniformMatrix4fv(gfx.ProjectionUniform, 1, false, &gfx.Projection[0])

	gfx.AlphaUniform = gl.GetUniformLocation(gfx.Program, gl.Str("alpha\x00"))
	gl.Uniform1f(gfx.AlphaUniform, 1.0) // default: fully opaque

	gfx.SmoothUniform = gl.GetUniformLocation(gfx.Program, gl.Str("smoothShading\x00"))
	gl.Uniform1i(gfx.SmoothUniform, 0) // default: flat facet normals

	gfx.AlphaSharpenUniform = gl.GetUniformLocation(gfx.Program, gl.Str("alphaSharpen\x00"))
	gl.Uniform1i(gfx.AlphaSharpenUniform, 0) // default: off; the opaque pass turns it on

	gfx.TimeUniform = gl.GetUniformLocation(gfx.Program, gl.Str("time\x00"))
	gfx.CamPosUniform = gl.GetUniformLocation(gfx.Program, gl.Str("camPos\x00"))
	gfx.IsWaterUniform = gl.GetUniformLocation(gfx.Program, gl.Str("isWater\x00"))
	gfx.WaveAmplitudeUniform = gl.GetUniformLocation(gfx.Program, gl.Str("waveAmplitude\x00"))
	gfx.WaveLengthUniform = gl.GetUniformLocation(gfx.Program, gl.Str("waveLength\x00"))
	gfx.WaveSpeedUniform = gl.GetUniformLocation(gfx.Program, gl.Str("waveSpeed\x00"))
	gfx.ReflectivityUniform = gl.GetUniformLocation(gfx.Program, gl.Str("reflectivity\x00"))
	gfx.ReflectColorUniform = gl.GetUniformLocation(gfx.Program, gl.Str("reflectColor\x00"))
	gl.Uniform1i(gfx.IsWaterUniform, 0)

	// Default camera position; overridden by the level's spawn point when it
	// has one.
	gfx.LoadCamera(mgl32.Vec3{0, world.EyeHeight, 3}, 0.0, float32(math.Pi/2.0))

	// Start with a blank in-memory level rather than loading one from disk;
	// the user can save it (or open an existing level) from the editor.
	levelsDir := filepath.Join(fileutil.ExecutablePath, "levels")
	levelPath := filepath.Join(levelsDir, "untitled.yaml")
	log.Printf("starting with blank level")
	level, err := NewBlankLevel()
	if err != nil {
		log.Fatalf("NewBlankLevel: %v", err)
	}
	log.Printf("level loaded: %d chunk(s), %d dir light(s), %d point light(s)",
		len(level.Chunks),
		len(level.Lights.Directional), len(level.Lights.Points))

	gl.BindFragDataLocation(gfx.Program, 0, gl.Str("outputColor\x00"))

	// [SCENARIO 5] UploadLights logs any uniform that the shader doesn't expose
	// (-1 location) so mismatches between Go-side light data and shader uniforms
	// are immediately visible in the log.
	world.UploadLights(level.Lights)

	// The player is created by the engine, not authored into the level — see
	// NewPlayer (entity.go). It carries the camera and is what collision is
	// resolved for; nothing draws it, since the view is from inside its head.
	world.PlayerEntity = world.NewPlayer(gfx.Cam.Position.Sub(mgl32.Vec3{0, world.EyeHeight, 0}))
	gfx.Cam.Position = world.PlayerEntity.Position.Add(mgl32.Vec3{0, world.EyeHeight, 0})
	gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position, gfx.Cam.Position.Add(gfx.Cam.Direction), gfx.WorldUp)
	gl.UniformMatrix4fv(gfx.Cam.Uniform, 1, false, &gfx.Cam.Matrix[0])

	// A spawn point in the level overrides that starting position, so a level
	// loaded straight into gameplay starts where the editor says it should.
	world.ApplySpawnPoint(&level)

	// Init level editor (must be after LoadLevel and OpenGL context ready)
	EditorInit(&level, levelPath, levelsDir)
	defer imguibackend.ImGuiShutdown()

	// Start directly in the 3D editor rather than play mode, since a fresh
	// blank level has nothing to play.
	EditorToggle()

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
	platform.LastFrameTick = sdl.GetTicks()

	for platform.Running {
		// Compute delta time (seconds), clamped to avoid spiral-of-death
		now := sdl.GetTicks()
		platform.DeltaTime = float32(now-platform.LastFrameTick) / 1000.0
		platform.LastFrameTick = now
		if platform.DeltaTime < 0.001 {
			platform.DeltaTime = 0.001
		}
		if platform.DeltaTime > 0.05 {
			platform.DeltaTime = 0.05
		}

		sdl.PumpEvents()

		// --- Process events first so mouse rotation is applied before movement ---
		for event := sdl.PollEvent(); event != nil; event = sdl.PollEvent() {
			// The keybinding reference answers to F1 from every mode and every
			// window — ahead of the routing below, so a secondary window's own
			// handler can't eat F1 first — and swallows the Escape that closes
			// it before a sub-editor can read it as "cancel".
			if KeybindHelpHandleEvent(event) {
				continue
			}
			// A secondary UIWindow (texture editor, etc.) owns its own imgui
			// context; route its events there instead of into the 3D
			// editor/gameplay handlers, which know nothing about it.
			if RouteWindowEvent(event, MainWindowID) {
				continue
			}
			if Editor.Active {
				EditorHandleEvent(event)
			}
			HandleEvent(event)
		}

		platform.KeyStates = sdl.GetKeyboardState()

		// Point the F1 reference at the focused window before anything draws.
		KeybindHelpUpdateFocus(MainWindowID)

		if Editor.Active {
			// Editor owns camera and input; gameplay logic is bypassed.
			EditorTick(platform.KeyStates)
		} else {
			// --- Input: let HandleInput update Cam.Position normally ---
			preMovePos := gfx.Cam.Position
			gfx.HandleInput(platform.KeyStates)

			// --- Player entity update + collision ---
			if world.PlayerEntity != nil {
				delta := gfx.Cam.Position.Sub(preMovePos)
				newPlayerPos := world.PlayerEntity.Position.Add(delta)

				// Apply pending jump impulse before gravity so the jump
				// frame isn't immediately reduced by one tick of gravity.
				if platform.JumpRequested {
					world.PlayerEntity.Velocity[1] = platform.JumpVelocity
					platform.JumpRequested = false
					platform.IsOnGround = false
				}

				// Apply gravity: accelerate downward, clamp to terminal velocity.
				// Scale by DeltaTime*60 to normalize to 60 FPS baseline.
				dtScale := platform.DeltaTime * 60.0
				if platform.GravityEnabled {
					world.PlayerEntity.Velocity[1] -= platform.GravityAccel * dtScale
					if world.PlayerEntity.Velocity[1] < -platform.MaxFallSpeed {
						world.PlayerEntity.Velocity[1] = -platform.MaxFallSpeed
					}
				}
				newPlayerPos[1] += world.PlayerEntity.Velocity[1] * dtScale

				chunk, sector := world.FindChunkAndSectorSH(level.Spatial, level.Chunks, newPlayerPos)
				newPlayerPos = collision.ResolveWallCollision(world.PlayerEntity, chunk, sector, newPlayerPos)
				newPlayerPos = collision.ResolveSlopeCollision(world.PlayerEntity, chunk, sector, world.PlayerEntity.Position, newPlayerPos)
				// Re-find sector after slope adjustment (player may have slid to a different sector)
				chunk, sector = world.FindChunkAndSectorSH(level.Spatial, level.Chunks, newPlayerPos)
				hitFloor, hitCeiling := collision.ResolveFloorCollision(world.PlayerEntity, chunk, sector, &newPlayerPos)

				// Landing/bonk: reset vertical velocity on floor or ceiling contact.
				if platform.GravityEnabled {
					if hitFloor {
						world.PlayerEntity.Velocity[1] = 0
						platform.IsOnGround = true
					} else {
						platform.IsOnGround = false
					}
					if hitCeiling && world.PlayerEntity.Velocity[1] > 0 {
						world.PlayerEntity.Velocity[1] = 0
					}
				}

				world.PlayerEntity.Position = newPlayerPos
				if chunk != nil {
					world.PlayerEntity.CurrentChunk = chunk.ID
				}
				if sector != nil {
					world.PlayerEntity.CurrentSector = sector.ID
				} else {
					world.PlayerEntity.CurrentSector = ""
				}
				gfx.Cam.Position = world.PlayerEntity.Position.Add(mgl32.Vec3{0, world.EyeHeight, 0})
				gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position, gfx.Cam.Position.Add(gfx.Cam.Direction), gfx.WorldUp)
				gl.UniformMatrix4fv(gfx.Cam.Uniform, 1, false, &gfx.Cam.Matrix[0])
			}
		}

		// --- Placed entities ---
		// Outside the editor/gameplay branch so clips keep playing while the
		// editor is open; motion is gated on gameplay so an entity cannot
		// drift away from where it was just placed.
		//
		// The player is handed in so it takes part in entity-against-entity
		// collision — a solid crate should be solid to walk into. Its
		// ground flag is cleared first and read back after, since being pushed
		// up onto an entity is the same as landing on a floor as far as jumping
		// is concerned.
		playerPosBefore := mgl32.Vec3{}
		if world.PlayerEntity != nil {
			playerPosBefore = world.PlayerEntity.Position
			world.PlayerEntity.Collision.OnGround = false
		}
		collision.EntityUpdate(&level, platform.DeltaTime, !Editor.Active, world.PlayerEntity)
		if !Editor.Active && world.PlayerEntity != nil {
			if world.PlayerEntity.Collision.OnGround {
				platform.IsOnGround = true
			}
			// Being pushed out of a solid entity happens after the camera was
			// placed on the player above, so put it back where the player now
			// actually is.
			if world.PlayerEntity.Position != playerPosBefore {
				gfx.Cam.Position = world.PlayerEntity.Position.Add(mgl32.Vec3{0, world.EyeHeight, 0})
				gfx.Cam.Matrix = mgl32.LookAtV(gfx.Cam.Position, gfx.Cam.Position.Add(gfx.Cam.Direction), gfx.WorldUp)
				gl.UniformMatrix4fv(gfx.Cam.Uniform, 1, false, &gfx.Cam.Matrix[0])
			}
		}

		// The script event loop, immediately after the world has settled: this
		// frame's overlaps produce this frame's `enter`, and whatever a handler
		// writes lands before the frame draws. `!Editor.Active` is the same play
		// gate EntityUpdate takes.
		ScriptTick(&level, platform.DeltaTime, !Editor.Active)

		// --- Debug stats: update CPU & memory ---
		render.DebugUpdateCPU()
		render.DebugUpdateMemory()
		render.DebugStats.FPS = 1.0 / platform.DeltaTime
		render.DebugStats.FrameTimeMs = platform.DeltaTime * 1000.0
		render.DebugStats.ChunksLoaded = len(level.Chunks)
		render.DebugStats.SectorsLoaded = totalSectors

		// Texture editor uploads (painted texels, saves) are queued rather
		// than applied immediately; drain them now, while the main GL
		// context is current, so this frame's draw calls already see them.
		gfx.DrainTextureUploads()

		// Geometry rebuilds and texture loads the script tick deferred. They
		// cannot happen inside the tick — ReloadSector ends in gl.* calls — so
		// they are applied here, where the main context is current, coalesced so
		// a hundred writes to one height cost one rebuild.
		DrainScriptWork(&level)

		// Likewise for a model editor asking to open the texture editor on a
		// freshly unwrapped atlas: creating a window from inside another
		// window's draw would insert into the map being ranged over and leave
		// the wrong GL context current. See modelPendingTexEditor.
		DrainModelTexEditorRequest()
		DrainWindowOpens()

		// Per-frame water shader inputs: animation clock and the view position
		// the fresnel/specular terms are measured against.
		gl.UseProgram(gfx.Program)
		gl.Uniform1f(gfx.TimeUniform, float32(now)/1000.0)
		gl.Uniform3f(gfx.CamPosUniform, gfx.Cam.Position.X(), gfx.Cam.Position.Y(), gfx.Cam.Position.Z())

		{
			// Keep every Water-kind biome sector's companion water surface
			// (chunk.WaterSurfaces) in step before anything reads it this
			// frame — the water draw loop below, and shadow/culling passes
			// that walk chunk bounds.
			biome.SyncBiomeWaterSurfaces(&level)

			// Latch the biome wind clock before the shadow passes read it, so
			// scatter sways identically in the depth maps and in the color pass and
			// a blade's shadow stays under the blade.
			render.BiomeWindTime = float32(now) / 1000.0

			// --- Shadow pass: render depth from each shadow-casting light ---
			shadowFrame := render.RenderShadowPass(level.Lights, &level, gfx.Cam.Matrix, gfx.Projection)
			render.UploadShadowUniforms(level.Lights, shadowFrame)
			render.RenderPointShadowPass(level.Lights, &level)
			render.UploadPointShadowUniforms(level.Lights)

			// --- Render ---
			gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)

			// The 3D editor's wireframe shading (editor_3d_view.go). Drawing
			// the world as lines is the whole of it: with no filled triangles
			// nothing hides behind anything, which is the same contract the
			// handle picker switches to in this mode. The scatter and
			// displacement passes below are skipped rather than outlined —
			// a wireframe field of grass blades is a solid mask, which is
			// precisely what the mode exists to get rid of.
			editorWire := Editor.Active && Editor.Mode == Mode3D && Editor.Wireframe
			if editorWire {
				gl.PolygonMode(gl.FRONT_AND_BACK, gl.LINE)
			}

			// Opaque pass: alpha means coverage here, so MSAA can reach the
			// edges a discard would otherwise leave hard — alpha-cutout
			// silhouettes (fragment.glsl's alphaSharpen) and the biome
			// patch's slope contour (ground_frag.glsl). Both are turned back
			// off below, before the transparent pass reads alpha as blend
			// weight again. See antialias.go.
			render.SetAlphaToCoverage(true)
			gl.UseProgram(gfx.Program)
			gl.Uniform1i(gfx.AlphaSharpenUniform, 1)

			// Build frustum from current projection × camera matrices
			projView := gfx.Projection.Mul4(gfx.Cam.Matrix)
			frustum := render.ExtractFrustum(projView)

			// Draw world geometry: chunks → sectors → floor/ceiling/walls
			// Two-pass rendering: opaque geometry first, then transparent geometry sorted back-to-front.
			chunksDrawn := 0
			sectorsDrawn := 0
			sectorsPortalCulled := 0
			var transparentDraws []TransparentDraw

			// Helper to queue or draw a geometry object based on its alpha.
			identity := mgl32.Ident4()
			drawOrQueue := func(g *gfx.Geometry, model mgl32.Mat4, doubleSided bool) {
				if g.IsTransparent() {
					pos := model.Mul4x1(mgl32.Vec4{g.Position.X(), g.Position.Y(), g.Position.Z(), 1})
					dx := pos.X() - gfx.Cam.Position.X()
					dy := pos.Y() - gfx.Cam.Position.Y()
					dz := pos.Z() - gfx.Cam.Position.Z()
					transparentDraws = append(transparentDraws, TransparentDraw{
						Geom:        g,
						Model:       model,
						Distance:    dx*dx + dy*dy + dz*dz,
						DoubleSided: doubleSided,
					})
					return
				}
				if model == identity {
					g.Draw()
				} else {
					g.DrawWithTransform(model)
				}
			}

			for i := range level.Chunks {
				chunk := &level.Chunks[i]
				if Editor.Active {
					// Editor mutations (vertex drag, extrusion/terrain edits,
					// height changes) can move geometry
					// outside the chunk's cached AABB without updating it. Keep
					// bounds honest while editing so culling doesn't hide what
					// was just added.
					chunk.RecomputeBounds()
				}
				if !render.AABBInFrustum(frustum, chunk.MinBound, chunk.MaxBound) {
					continue
				}
				chunksDrawn++

				// Portal visibility: only render sectors reachable through portals from the player's sector.
				// Sectors disconnected from the portal graph (e.g. nested sectors with no shared
				// edges) bypass portal culling and fall through to frustum-only culling.
				var visibleSectors map[string]bool
				var connectedSectors map[string]bool
				if world.PlayerEntity != nil && chunk.ID == world.PlayerEntity.CurrentChunk {
					visibleSectors = render.PortalVisibleSectors(chunk, world.PlayerEntity.CurrentSector, gfx.Cam.Position)
					connectedSectors = render.PortalConnectedSectors(chunk, world.PlayerEntity.CurrentSector)
				}

				for j := range chunk.Sectors {
					sector := &chunk.Sectors[j]
					if visibleSectors != nil && connectedSectors[sector.ID] && !visibleSectors[sector.ID] {
						sectorsPortalCulled++
						continue
					}
					sectorMin := mgl32.Vec3{sector.MinBound.X(), sector.Floor.Height, sector.MinBound.Y()}
					sectorMax := mgl32.Vec3{sector.MaxBound.X(), sector.Ceiling.Height, sector.MaxBound.Y()}
					if !render.AABBInFrustum(frustum, sectorMin, sectorMax) {
						continue
					}
					sectorsDrawn++
					if sector.Floor.Enabled && !sector.Floor.Hidden {
						// Displaced faces are drawn by RenderDetailColor via the tess
						// program instead of the flat mesh (avoids z-fighting).
						if !sector.Floor.Detail.HasDisplace() {
							drawOrQueue(&sector.Floor.Geometry, identity, sector.Floor.DoubleSided)
						}
						for eg := range sector.Floor.ExtrusionGeometries {
							drawOrQueue(&sector.Floor.ExtrusionGeometries[eg], identity, sector.Floor.DoubleSided)
						}
					}
					if sector.Ceiling.Enabled {
						if !sector.Ceiling.Detail.HasDisplace() {
							drawOrQueue(&sector.Ceiling.Geometry, identity, sector.Ceiling.DoubleSided)
						}
						for eg := range sector.Ceiling.ExtrusionGeometries {
							drawOrQueue(&sector.Ceiling.ExtrusionGeometries[eg], identity, sector.Ceiling.DoubleSided)
						}
					}
					for k := range sector.Walls {
						w := &sector.Walls[k]
						if w.Visible && (w.DoubleSided || render.WallFacingCamera(*w, gfx.Cam.Position)) {
							if !w.Detail.HasDisplace() {
								drawOrQueue(&w.Geometry, identity, w.DoubleSided)
							}
							for eg := range w.ExtrusionGeometries {
								drawOrQueue(&w.ExtrusionGeometries[eg], identity, w.DoubleSided)
							}
						}
					}
					// Draw gap-fill geometry on portals connecting sectors at different heights.
					for k := range sector.Portals {
						p := &sector.Portals[k]
						if p.Visible && p.DrawGap && (p.DoubleSided || render.PortalFacingCamera(p.StartPoint, p.EndPoint, gfx.Cam.Position)) {
							drawOrQueue(&p.FloorGap, identity, p.DoubleSided)
							drawOrQueue(&p.CeilGap, identity, p.DoubleSided)
						}
					}
				}

				// Biome-painted water surfaces (see biome_water.go). They are
				// culled against their own bounds rather than inside the sector
				// loop above: a lake's extent is unrelated to the sector its
				// centre lands in, so it would otherwise vanish as soon as that
				// one small sector left the frustum.
				for wi := range chunk.WaterSurfaces {
					w := &chunk.WaterSurfaces[wi]
					wmin, wmax := w.WorldAABB()
					if !render.AABBInFrustum(frustum, wmin, wmax) {
						continue
					}
					drawOrQueue(&w.Geometry, w.ModelMatrix(), true)
				}
			}

			// Placed entities. Unlike the spawn marker below, these are part
			// of the world and draw in gameplay as well as in the editor.
			render.RenderEntities(&level, &frustum, drawOrQueue)

			// The spawn marker is an authoring aid, not level geometry: it
			// exists to be seen and dragged in the editor and must never show
			// up in the shot the player spawns into.
			if Editor.Active && level.Spawn != nil {
				drawOrQueue(&level.Spawn.Geometry, level.Spawn.ModelMatrix(), false)
			}

			// Volumes are regions rather than objects: invisible during play,
			// and drawn here as wireframe cages so an author can see what they
			// are placing. Same rule as the spawn marker.
			//
			// An entity with no mesh yet gets the same treatment, for the same
			// reason: it has to be visible to be worked on, and invisible once
			// the editor is closed.
			if Editor.Active {
				render.RenderVolumes(&level, &frustum)
				RenderEntityMarkers(&level, &frustum)
			}

			if !editorWire {
				// --- Procedural biome scatter (GPU grass / rocks) ---
				// Drawn after opaque geometry but before the transparent pass: blades and
				// rocks are solid (depth-written) so translucent water still sorts on top.
				render.RenderBiomes(&level, projView, gfx.Cam.Position, shadowFrame)

				// --- Procedural face detail (GPU rock displacement + scatter) ---
				// Drawn after opaque geometry; reuses the main fragment shader so it
				// receives shadows, and was rendered into the shadow maps above so it casts.
				render.RenderDetailColor(&level, shadowFrame)
			}

			// End of the opaque pass — hand alpha back to blending. Done
			// unconditionally rather than inside the transparent block below,
			// so the editor overlays and ImGui that follow never inherit
			// either setting on a frame with nothing transparent in it.
			render.SetAlphaToCoverage(false)
			gl.UseProgram(gfx.Program)
			gl.Uniform1i(gfx.AlphaSharpenUniform, 0)

			// --- Transparent pass: draw collected transparent geometry back-to-front ---
			if len(transparentDraws) > 0 {
				sort.Slice(transparentDraws, func(a, b int) bool {
					return transparentDraws[a].Distance > transparentDraws[b].Distance
				})

				gl.Enable(gl.BLEND)
				gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
				gl.DepthMask(false)

				for _, td := range transparentDraws {
					if td.DoubleSided {
						// Two-sub-pass for double-sided: back faces first, then front faces
						gl.Enable(gl.CULL_FACE)
						gl.CullFace(gl.FRONT) // draw back faces
						if td.Model == identity {
							td.Geom.Draw()
						} else {
							td.Geom.DrawWithTransform(td.Model)
						}
						gl.CullFace(gl.BACK) // draw front faces
						if td.Model == identity {
							td.Geom.Draw()
						} else {
							td.Geom.DrawWithTransform(td.Model)
						}
						gl.Disable(gl.CULL_FACE)
					} else {
						if td.Model == identity {
							td.Geom.Draw()
						} else {
							td.Geom.DrawWithTransform(td.Model)
						}
					}
				}

				gl.DepthMask(true)
				gl.Disable(gl.BLEND)
			}

			// Back to filled triangles before the editor overlays and ImGui,
			// neither of which would survive being drawn as outlines.
			if editorWire {
				gl.PolygonMode(gl.FRONT_AND_BACK, gl.FILL)
			}

			render.DebugStats.ChunksDrawn = chunksDrawn
			render.DebugStats.SectorsDrawn = sectorsDrawn
			render.DebugStats.SectorsPortalCulled = sectorsPortalCulled
		}

		// Draw editor overlays and ImGui UI when editor is active
		if Editor.Active {
			EditorRender()
		}

		// Draw debug overlay and keybinding reference (both use ImGui,
		// independent of the editor — when it is active, EditorRender has
		// already drawn them inside its own frame).
		if (render.DebugOverlayVisible || KeybindHelpVisibleIn(MainWindowID)) && !Editor.Active {
			imguibackend.ImGuiNewFrame(platform.DeltaTime)
			render.DebugRenderOverlay()
			KeybindHelpRender()
			imguibackend.ImGuiRender()
		}

		platform.Window.GLSwap()

		// Secondary windows (texture editor, etc.) render last, each in its
		// own GL/imgui context, restoring the main ones before the next
		// iteration touches world GL state again.
		RenderSecondaryWindows(platform.DeltaTime)
	}
}
