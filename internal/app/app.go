package app

import (
	"foundry/internal/audio"
	"foundry/internal/biome"
	"foundry/internal/collision"
	"foundry/internal/detail"
	"foundry/internal/editor"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/imguibackend"
	"foundry/internal/levelio"
	"foundry/internal/particle"
	"foundry/internal/platform"
	"foundry/internal/render"
	"foundry/internal/script"
	"foundry/internal/texture"
	"foundry/internal/ui"
	"foundry/internal/uidraw"
	"foundry/internal/uifont"
	"foundry/internal/uiwin"
	"foundry/internal/world"
	_ "image/png"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

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

// Run starts the engine: opens the log, scans assets, stands up the window and
// GL context, loads the level, and does not return until the main loop ends.
//
// The thread-pinning that has to happen before any of this is cmd/foundry's —
// see the note on its init.
func Run() {
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
	script.InstallLogCapture(logSinks)

	gfx.TextureNames = gfx.ScanTextures(filepath.Join(fileutil.ExecutablePath, "textures"))
	log.Printf("scanned %d textures", len(gfx.TextureNames))

	// Merge the user's saved detail presets in over the compiled-in ones.
	detail.LoadDetailPresets()

	// Load (or, on a fresh checkout, create) the global 256-colour palette
	// every texture in the texture editor composites against.
	texture.LoadGlobalPalette()

	// Any .ftex with more than one frame plays back at runtime — see
	// gfx.AdvanceAnimatedTextures below — whether or not its editor is ever
	// opened this session.
	texture.ScanAnimatedTextures()

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

	// Audio comes up right after SDL, because INIT_EVERYTHING above already
	// started the audio subsystem — all that is left is to open a device.
	//
	// Deliberately not fatal. The engine log.Fatalf's on a GL failure because
	// there is nothing to draw without GL; a game with no sound still runs,
	// and a machine with no sound card (or a WSL2 session with no audio
	// server) should reach the main menu rather than the log. Init has
	// already logged whatever went wrong, and every audio call becomes a
	// no-op from here.
	//
	// The defer is registered after sdl.Quit's and therefore runs before it,
	// which is the ordering that matters: a queued write still in flight when
	// SDL_Quit runs is a use-after-free down in C.
	_ = audio.Init(audio.Config{})
	defer audio.Shutdown()
	// The saved gains, after the device is up so a slider that was left at
	// zero silences the mixer rather than being overwritten by it.
	audio.LoadSettings()

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
	if uiwin.MainWindowID, err = platform.Window.GetID(); err != nil {
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

	gfx.HasBlendUniform = gl.GetUniformLocation(gfx.Program, gl.Str("hasBlend\x00"))
	gfx.BlendTexUniform = gl.GetUniformLocation(gfx.Program, gl.Str("blendTex\x00"))
	gfx.BlendMaskUniform = gl.GetUniformLocation(gfx.Program, gl.Str("blendMask\x00"))
	gl.Uniform1i(gfx.HasBlendUniform, 0) // default: no second texture

	// Default camera position; overridden by the level's spawn point when it
	// has one.
	gfx.LoadCamera(mgl32.Vec3{0, world.EyeHeight, 3}, 0.0, float32(math.Pi/2.0))

	// Start with a blank in-memory level rather than loading one from disk;
	// the user can save it (or open an existing level) from the editor.
	levelsDir := filepath.Join(fileutil.ExecutablePath, "levels")
	levelPath := filepath.Join(levelsDir, "untitled.yaml")
	log.Printf("starting with blank level")
	level, err := levelio.NewBlankLevel()
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
	gfx.UpdateCamMatrix()

	// A spawn point in the level overrides that starting position, so a level
	// loaded straight into gameplay starts where the editor says it should.
	world.ApplySpawnPoint(&level)

	// The particle simulation's world-collision seam. internal/particle sits
	// below internal/collision and cannot import it, so the owner installs the
	// function — the same inversion world.CurrentLevel and hooks.EditorActive
	// use. Left nil, particles simply do not collide, which is what every test
	// in that package runs with.
	particle.Collide = func(from, to mgl32.Vec3, r float32) (mgl32.Vec3, mgl32.Vec3, bool) {
		return collision.ParticleCollide(&level, from, to, r)
	}

	// The editor's settings panel offers fullscreen as a checkbox beside the
	// rest of the window-level toggles, and sits below this package in the
	// import graph — so hand it the toggle rather than have it reach up for
	// one. See platform.ToggleFullscreenFunc.
	platform.ToggleFullscreenFunc = ToggleFullscreen

	// The runtime UI. Three installs and one GL init, all of them the same
	// inversion the function variables above use: internal/ui and
	// internal/uifont sit below the log window and below the script runtime, so
	// the owner hands them what they need rather than reaching up for it.
	ui.Logf = func(format string, args ...any) { log.Printf(format, args...) }
	uifont.LogMissing = func(name string, err error) {
		log.Printf("ui: font %q could not be loaded (%v); falling back to the built-in bitmap face", name, err)
	}
	ui.LoadActiveTheme()
	if err := uidraw.Init(); err != nil {
		log.Printf("ui renderer: %v", err)
	}
	defer uidraw.Delete()

	// Init level editor (must be after LoadLevel and OpenGL context ready)
	editor.EditorInit(&level, levelPath, levelsDir)
	defer imguibackend.ImGuiShutdown()

	// Start directly in the 3D editor rather than play mode, since a fresh
	// blank level has nothing to play.
	editor.EditorToggle()

	// The running game's draw list, reused every frame so a steady HUD
	// allocates nothing. One list rather than one per screen: the screens draw
	// in sequence, never at once.
	var gameUIList uidraw.List

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
			if uiwin.KeybindHelpHandleEvent(event) {
				continue
			}
			// A secondary UIWindow (texture editor, etc.) owns its own imgui
			// context; route its events there instead of into the 3D
			// editor/gameplay handlers, which know nothing about it.
			if uiwin.RouteWindowEvent(event, uiwin.MainWindowID) {
				continue
			}
			// The running game's UI. Ahead of gameplay so a click on a menu
			// button is not also a shot, and behind the window router so a
			// secondary editor window still owns its own input. Consuming the
			// event is the whole of how gameplay input is gated — see
			// ui_input.go.
			if uiHandleEvent(event) {
				continue
			}
			if editor.Editor.Active {
				editor.EditorHandleEvent(event)
			}
			HandleEvent(event)
		}

		platform.KeyStates = sdl.GetKeyboardState()

		// Point the F1 reference at the focused window before anything draws.
		uiwin.KeybindHelpUpdateFocus(uiwin.MainWindowID)

		// A screen that wants a pointer releases mouse-look, and closing it
		// gives capture back. Edge-triggered, so this joins the editor toggle
		// and the focus handler as a third claimant rather than fighting them
		// every frame.
		uiApplyCursor()

		// A level a script asked for, and one slice of building it. While a
		// load is in flight the world does not tick — half of it belongs to the
		// outgoing level and half to the incoming one — but the UI does, which
		// is what lets the loading screen over it animate.
		beginRequestedLevelLoad()
		stepLevelLoad(&level)

		// A pausing screen freezes the player, the vehicles and the entities.
		// It does not freeze scripts: the menu's own handlers have to run, and
		// so does anything a script is animating behind it.
		uiPaused := !editor.Editor.Active && (ui.Game.Pauses() || levelLoadPending())

		if editor.Editor.Active {
			// Editor owns camera and input; gameplay logic is bypassed.
			editor.EditorTick(platform.KeyStates)
		} else if !uiPaused {
			// --- Input -> player -> camera ---
			// The keys say how far to move; the player is what moves, and the
			// camera is placed on it afterwards. It used to run the other way
			// round — move the camera, then difference it onto the player —
			// which cannot survive the two living in different coordinate
			// systems, and cost a re-sync every time anything else nudged the
			// body.
			// While the player is in a driver's seat these keys fill the
			// vehicle's controls instead of the player's move intent.
			intent := PlayerMoveIntent(&level, platform.KeyStates)

			// Vehicles integrate before anything inside them resolves, and
			// republish their frames as they go. A rider that resolved against
			// last frame's transform would be shoved through the deck every
			// time the ship pitched — this ordering is the fix, and it is why
			// VehicleUpdate is here rather than folded into EntityUpdate.
			//
			// Driving writes straight into the level's own entities rather than
			// going through the script host, so it never trips the runtime's
			// markDirty calls. Mark the play session dirty by hand whenever a
			// vehicle actually moved, or leaving play would leave it wherever it
			// was last driven instead of putting the level back.
			if collision.VehicleUpdate(&level, platform.DeltaTime, true) {
				script.PlaySessionMarkDirty()
			}

			if world.PlayerEntity != nil {
				onGround := collision.MovePlayer(
					world.PlayerEntity, &level,
					collision.PlayerMove{
						Intent:       intent,
						Jump:         platform.JumpRequested,
						Gravity:      platform.GravityEnabled,
						DTScale:      platform.DeltaTime * 60.0,
						GravityAccel: platform.GravityAccel,
						MaxFallSpeed: platform.MaxFallSpeed,
						JumpVelocity: platform.JumpVelocity,
					})
				platform.JumpRequested = false

				// Standing in a frame portal's doorway hands the player over to
				// the other side — the ship's ramp onto the hangar deck, or back
				// again. Position, velocity and facing all go through the same
				// relative transform, so nothing pops and nobody arrives walking
				// sideways.
				if c := collision.FindFramePortalCrossing(&level, world.PlayerEntity); c != nil && !c.Sealed {
					gfx.Cam.Yaw = collision.CrossFramePortal(&level, world.PlayerEntity, c, gfx.Cam.Yaw)
				}
				// Only meaningful with gravity on; free-fly leaves it alone
				// rather than reporting a fall that is not happening.
				if platform.GravityEnabled {
					platform.IsOnGround = onGround
				}

				// Riding in a frame puts the camera in that frame's space, with
				// its own up vector; on foot this falls through to the world
				// placement it always had.
				if !SeatedCamera(&level) {
					gfx.Cam.Up = gfx.WorldUp
					gfx.Cam.Direction = gfx.DirectionFromAngles(gfx.Cam.Yaw, gfx.Cam.Pitch)
					gfx.Cam.Position = world.PlayerEntity.Position.Add(mgl32.Vec3{0, world.EyeHeight, 0})
					gfx.UpdateCamMatrix()
				}
			} else {
				// No player yet: the intent still has to go somewhere, or the
				// camera freezes on the loading frame.
				gfx.Cam.Position = gfx.Cam.Position.Add(intent)
				gfx.UpdateCamMatrix()
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
		collision.EntityUpdate(&level, platform.DeltaTime, !editor.Editor.Active && !uiPaused, world.PlayerEntity)
		if !editor.Editor.Active && world.PlayerEntity != nil {
			if world.PlayerEntity.Collision.OnGround {
				platform.IsOnGround = true
			}
			// Being pushed out of a solid entity happens after the camera was
			// placed on the player above, so put it back where the player now
			// actually is.
			if world.PlayerEntity.Position != playerPosBefore {
				if !SeatedCamera(&level) {
					gfx.Cam.Position = world.PlayerEntity.Position.Add(mgl32.Vec3{0, world.EyeHeight, 0})
					gfx.UpdateCamMatrix()
				}
			}
		}

		// The script event loop, immediately after the world has settled: this
		// Frames follow whatever carries them, so their transforms are recomputed
		// once the entities that own them have finished moving — and before
		// anything reads them. Everything downstream this frame (the script
		// tick's world-space overlap tests, and every draw of a chunk that lives
		// in a frame) sees where things actually are rather than where they were
		// last frame.
		//
		// Walked parents-first inside RefreshFrames, so a nested frame composes
		// against a parent that is already up to date.
		world.RefreshFrames(&level, level.Frames)

		// Particles, after the frames they may be riding have been republished and
		// before the script tick, so a burst a script fires this frame lands at
		// age 0 and draws this frame.
		//
		// Not gated on the editor being closed, unlike entity motion: an effect
		// that froze the moment the editor opened could not be placed by eye. The
		// gate is handed over anyway so the simulation can tell play from
		// authoring if it ever needs to.
		world.EmitterUpdate(&level, platform.DeltaTime, !editor.Editor.Active && !uiPaused)

		// The listener, published in the same slot and for the same reasons
		// as the emitters above: after RefreshFrames, so anything riding a
		// moving frame is read where it now actually is, and before the
		// script tick, so a sound a script starts this frame is already
		// audible in the block the mixer builds next.
		//
		// Not gated on the editor being closed, again like the emitters: a
		// sound that went silent the moment the editor opened could not be
		// placed by ear, which is most of how a positional sound gets placed.
		//
		// gfx.CamUp() rather than gfx.Cam.Up: the zero value there means "no
		// tilt", and only the accessor knows that. The editor's fly camera is
		// this same camera, which is what makes auditioning work.
		world.SoundUpdate(&level, platform.DeltaTime, !editor.Editor.Active,
			gfx.Cam.Position, gfx.Cam.Direction, gfx.CamUp())
		world.MusicUpdate(&level)

		// The interact key's vehicle half, before the script tick so a mount
		// made this frame is visible to it. It consumes the press only when it
		// actually did something, so `E` still reaches a scripted door when
		// there is no seat within reach.
		if !editor.Editor.Active {
			VehicleInteractRequest(&level)
		}

		// frame's overlaps produce this frame's `enter`, and whatever a handler
		// writes lands before the frame draws. `!Editor.Active` is the same play
		// gate EntityUpdate takes.
		// The UI ticks after the world has settled, so a binding reads this
		// frame's values, and before the script tick, so a click made this
		// frame is already in the queue that tick drains to a fixpoint.
		if !editor.Editor.Active {
			ui.Game.Tick(platform.DeltaTime, float32(platform.WinWidth), float32(platform.WinHeight))
		}

		if !levelLoadPending() {
			script.ScriptTick(&level, platform.DeltaTime, !editor.Editor.Active)
		}

		// --- Debug stats: update CPU & memory ---
		render.DebugUpdateCPU()
		render.DebugUpdateMemory()
		render.DebugStats.FPS = 1.0 / platform.DeltaTime
		render.DebugStats.FrameTimeMs = platform.DeltaTime * 1000.0
		render.DebugStats.ChunksLoaded = len(level.Chunks)
		render.DebugStats.SectorsLoaded = totalSectors

		// Animated textures (the texture editor's animation stack) advance on
		// the same free-running clock as the water/biome-wind uniforms below,
		// queuing a re-upload whenever a texture's clock crosses into its next
		// frame — drained into the GL texture object right after, alongside
		// every other queued texture edit.
		gfx.AdvanceAnimatedTextures(float64(now) / 1000.0)

		// Texture editor uploads (painted texels, saves) are queued rather
		// than applied immediately; drain them now, while the main GL
		// context is current, so this frame's draw calls already see them.
		// Glyph pages baked during the UI tick above. Queued here and drained
		// on the next line, which is what makes a character seen for the first
		// time this frame resident before it is drawn — see SyncFontAtlases.
		uidraw.SyncFontAtlases()

		gfx.DrainTextureUploads()

		// Geometry rebuilds and texture loads the script tick deferred. They
		// cannot happen inside the tick — ReloadSector ends in gl.* calls — so
		// they are applied here, where the main context is current, coalesced so
		// a hundred writes to one height cost one rebuild.
		script.DrainScriptWork(&level)

		// Likewise for a model editor asking to open the texture editor on a
		// freshly unwrapped atlas: creating a window from inside another
		// window's draw would insert into the map being ranged over and leave
		// the wrong GL context current. See modelPendingTexEditor.
		editor.DrainModelTexEditorRequest()
		uiwin.DrainWindowOpens()

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
			editorWire := editor.Editor.Active && editor.Editor.Mode == editor.Mode3D && editor.Editor.Wireframe
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
					// The mesh's own origin carried out by model — NOT
					// model * g.Position. A geometry's vertices are stored
					// relative to the position it was loaded with, and every
					// caller's model matrix already contains that same
					// translation (water: chunkModel*Translate(w.Position) with
					// g.Position == w.Position; an entity attachment:
					// world*LocalMatrix() which opens with Translate(m.Offset)
					// and g.Position == m.Offset; the spawn marker likewise).
					// Multiplying it in again put the sort key at twice the
					// offset, so translucent water and attachments sorted
					// against a position nothing occupies. Structural geometry
					// loads at the zero vector, so this is bit-identical there.
					pos := model.Mul4x1(mgl32.Vec4{0, 0, 0, 1})
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
				if editor.Editor.Active {
					// Editor mutations (vertex drag, extrusion/terrain edits,
					// height changes) can move geometry
					// outside the chunk's cached AABB without updating it. Keep
					// bounds honest while editing so culling doesn't hide what
					// was just added.
					chunk.RecomputeBounds()
				}
				chunkMin, chunkMax := chunk.WorldBounds()
				if !render.AABBInFrustum(frustum, chunkMin, chunkMax) {
					continue
				}
				chunksDrawn++

				// Every chunk's geometry is authored in its own local space and
				// carried into the world (or into an owning frame's space) by
				// ModelMatrix; the identity for an unmoved, unrotated,
				// unscaled chunk is what keeps this one loop.
				chunkModel := chunk.ModelMatrix()

				// Everything below that asks "where is the camera" has to ask it
				// in the chunk's own local coordinates, or a backface test
				// compares a world-space eye against local geometry and culls
				// the wrong side of a moved or rotated chunk.
				chunkCam := gfx.Cam.Position
				if chunkModel != identity {
					r := chunkModel.Inv().Mul4x1(chunkCam.Vec4(1))
					chunkCam = mgl32.Vec3{r.X(), r.Y(), r.Z()}
				}

				// Portal visibility: only render sectors reachable through portals from the player's sector.
				// Sectors disconnected from the portal graph (e.g. nested sectors with no shared
				// edges) bypass portal culling and fall through to frustum-only culling.
				//
				// Off in the editor. The BFS starts at the PLAYER's sector but
				// tests portal facing against the camera, and in the editor
				// those are two unrelated places: the body stands wherever play
				// last left it while the eye flies anywhere it likes. Sectors
				// then dropped out of the set as the view moved — geometry
				// simply stopping being drawn as the camera passed through it.
				// A free camera has no room to be in, so there is nothing for
				// this pass to cull from.
				var visibleSectors map[string]bool
				var connectedSectors map[string]bool
				if !editor.Editor.Active && world.PlayerEntity != nil && chunk.ID == world.PlayerEntity.CurrentChunk {
					visibleSectors = render.PortalVisibleSectors(chunk, world.PlayerEntity.CurrentSector, chunkCam)
					connectedSectors = render.PortalConnectedSectors(chunk, world.PlayerEntity.CurrentSector)
				}

				for j := range chunk.Sectors {
					sector := &chunk.Sectors[j]
					if visibleSectors != nil && connectedSectors[sector.ID] && !visibleSectors[sector.ID] {
						sectorsPortalCulled++
						continue
					}
					// WorldAABB, not a hand-built Floor.Height..Ceiling.Height
					// box: LocalAABB widens that range by per-vertex offsets,
					// terrain points, extrusions and wall tops, and this pass
					// has to agree with the shadow (shadow.go) and detail
					// (detail_render.go) passes, which both cull the same
					// sector against exactly that. Culling against a tighter
					// box here dropped sectors whose extrusion was on screen
					// while their shadow was still being drawn.
					sectorMin, sectorMax := sector.WorldAABB(chunk)
					if !render.AABBInFrustum(frustum, sectorMin, sectorMax) {
						continue
					}
					sectorsDrawn++
					// The skirt closes the band under this sector's floor —
					// a storey's slab edge, or the face under an overhang. It
					// is drawn whatever the floor itself is doing: a Hidden
					// floor still has a slab, and a displaced one still needs
					// its edge. See world/skirt.go.
					if world.SkirtDrawable(sector) {
						drawOrQueue(&sector.Floor.SkirtGeometry, chunkModel, true)
					}
					if sector.Floor.Enabled && !sector.Floor.Hidden {
						// Displaced faces are drawn by RenderDetailColor via the tess
						// program instead of the flat mesh (avoids z-fighting).
						if !sector.Floor.Detail.HasDisplace() {
							drawOrQueue(&sector.Floor.Geometry, chunkModel, sector.Floor.DoubleSided)
						}
						for eg := range sector.Floor.SubmeshGeometries {
							drawOrQueue(&sector.Floor.SubmeshGeometries[eg], chunkModel, sector.Floor.DoubleSided)
						}
					}
					if sector.Ceiling.Enabled {
						if !sector.Ceiling.Detail.HasDisplace() {
							drawOrQueue(&sector.Ceiling.Geometry, chunkModel, sector.Ceiling.DoubleSided)
						}
						for eg := range sector.Ceiling.SubmeshGeometries {
							drawOrQueue(&sector.Ceiling.SubmeshGeometries[eg], chunkModel, sector.Ceiling.DoubleSided)
						}
					}
					for k := range sector.Walls {
						w := &sector.Walls[k]
						if w.Visible && (w.DoubleSided || render.WallFacingCamera(*w, chunkCam)) {
							if !w.Detail.HasDisplace() {
								drawOrQueue(&w.Geometry, chunkModel, w.DoubleSided)
							}
							for eg := range w.SubmeshGeometries {
								drawOrQueue(&w.SubmeshGeometries[eg], chunkModel, w.DoubleSided)
							}
						}
					}
					// Draw gap-fill geometry on portals connecting sectors at different heights.
					for k := range sector.Portals {
						p := &sector.Portals[k]
						if p.Visible && p.DrawGap && (p.DoubleSided || render.PortalFacingCamera(p.StartPoint, p.EndPoint, chunkCam)) {
							drawOrQueue(&p.FloorGap, chunkModel, p.DoubleSided)
							drawOrQueue(&p.CeilGap, chunkModel, p.DoubleSided)
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
					wmin, wmax := w.WorldAABB(chunk)
					if !render.AABBInFrustum(frustum, wmin, wmax) {
						continue
					}
					drawOrQueue(&w.Geometry, chunkModel.Mul4(w.ModelMatrix()), true)
				}
			}

			// Placed entities. Unlike the spawn marker below, these are part
			// of the world and draw in gameplay as well as in the editor.
			render.RenderEntities(&level, &frustum, drawOrQueue)

			// The spawn marker is an authoring aid, not level geometry: it
			// exists to be seen and dragged in the editor and must never show
			// up in the shot the player spawns into.
			if editor.Editor.Active && level.Spawn != nil {
				// A spawn aboard a vehicle is authored in that vehicle's
				// coordinates, so its marker draws under the same transform its
				// geometry does — otherwise the arrow sits at the world origin
				// while the player will start inside the ship.
				m := level.Spawn.ModelMatrix()
				if level.Spawn.Frame != "" {
					m = level.Frames.Frame(level.Spawn.Frame).World.Mul4(m)
				}
				drawOrQueue(&level.Spawn.Geometry, m, false)
			}

			// Volumes are regions rather than objects: invisible during play,
			// and drawn here as wireframe cages so an author can see what they
			// are placing. Same rule as the spawn marker.
			//
			// An entity with no mesh yet gets the same treatment, for the same
			// reason: it has to be visible to be worked on, and invisible once
			// the editor is closed.
			if editor.Editor.Active {
				render.RenderVolumes(&level, &frustum)
				editor.RenderEntityMarkers(&level, &frustum)
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

				// --- Sprites, cutout only ---
				// Inside the opaque block on purpose: a cutout sprite's alpha
				// is coverage, so the alpha-to-coverage still switched on above
				// antialiases its silhouette and it needs no sorting at all.
				// The alpha and additive modes come after the transparent pass.
				render.RenderSpritesOpaque(&level, projView, gfx.Cam.Position, &frustum, shadowFrame)
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

			// --- Sprites, alpha and additive ---
			// After the world's transparent pass rather than inside it: a
			// sprite batch is many quads under one draw, so it cannot join a
			// queue that sorts one Geometry at a time — and additive quads want
			// no sorting at all, which that queue has no way to express.
			if !editorWire {
				render.RenderSpritesTransparent(&level, projView, gfx.Cam.Position, &frustum, shadowFrame)
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

		// --- Runtime UI ---
		// Screen space, after everything in the world and before any imgui.
		// Not drawn while the editor is active, for the same reason scripts do
		// not run there: what is on screen in the editor is the level, not the
		// game.
		if !editor.Editor.Active {
			// One pass per screen, bottom to top. Each has its own canvas and
			// therefore its own scale, so they cannot share a projection.
			for _, s := range ui.Game.Screens() {
				uidraw.RenderScreen(&gameUIList, s, ui.Game.Interaction(),
					platform.WinWidth, platform.WinHeight)
			}
		}

		// Draw editor overlays and ImGui UI when editor is active
		if editor.Editor.Active {
			editor.EditorRender()
		}

		// Draw debug overlay and keybinding reference (both use ImGui,
		// independent of the editor — when it is active, EditorRender has
		// already drawn them inside its own frame).
		if (render.DebugOverlayVisible || uiwin.KeybindHelpVisibleIn(uiwin.MainWindowID)) && !editor.Editor.Active {
			imguibackend.ImGuiNewFrame(platform.DeltaTime)
			render.DebugRenderOverlay()
			uiwin.KeybindHelpRender()
			imguibackend.ImGuiRender()
		}

		platform.Window.GLSwap()

		// Secondary windows (texture editor, etc.) render last, each in its
		// own GL/imgui context, restoring the main ones before the next
		// iteration touches world GL state again.
		uiwin.RenderSecondaryWindows(platform.DeltaTime)
	}
}
