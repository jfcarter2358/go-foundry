package app

import (
	"fmt"
	"foundry/internal/ai"
	"foundry/internal/audio"
	"foundry/internal/collision"
	"foundry/internal/devui"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/glctx"
	"foundry/internal/hooks"
	"foundry/internal/levelio"
	"foundry/internal/material"
	"foundry/internal/particle"
	"foundry/internal/platform"
	"foundry/internal/render"
	"foundry/internal/script"
	"foundry/internal/sim"
	"foundry/internal/texture"
	"foundry/internal/ui"
	"foundry/internal/uidraw"
	"foundry/internal/uifont"
	"foundry/internal/video"
	"foundry/internal/world"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

var shadowVertShaderPath = "shaders/shadow_vert.glsl"
var shadowFragShaderPath = "shaders/shadow_frag.glsl"
var shadowCubeVertPath = "shaders/shadow_cube_vert.glsl"
var shadowCubeFragPath = "shaders/shadow_cube_frag.glsl"

// playOrKeyboard is what drives the player this frame: a play script running
// in the editor's Claude panel if there is one, and the keyboard otherwise.
//
// A script's move is a vector and a duration rather than a set of keys that
// are down, so it cannot be delivered as a key table — see internal/play.
func playOrKeyboard(dt float32) sim.Input {
	if in, driving := devPlayInput(dt); driving {
		return in
	}
	return sim.Input{Keys: platform.KeyStates}
}

// Run starts the engine: opens the log, scans assets, stands up the window and
// GL context, loads the level, and does not return until the main loop ends.
//
// The thread-pinning that has to happen before any of this is cmd/foundry's —
// see the note on its init.
func Run() {
	gfx.Textures = make(map[string]uint32)
	// The frame loop would rather an agent wait a moment for its navigation
	// than every agent's level stall while it is built. See ai.AsyncNav.
	ai.AsyncNav = true

	executable, err := os.Executable()
	if err != nil {
		// No log yet — it goes beside the executable in a dev build — so only
		// the message box says it.
		fatalStartupf("os.Executable: %v", err)
	}
	fileutil.ExecutablePath = filepath.Dir(executable)
	// Every asset the engine reads — levels, textures, shaders, scripts,
	// game.yaml — is under a project's resources/. A tool run below names its
	// own project with -root; until the project is chosen, it is the one beside
	// the executable.
	fileutil.ProjectPath = fileutil.ExecutablePath
	fileutil.ResourcesPath = fileutil.ResourcesDir(fileutil.ExecutablePath)

	// A development binary is also its project's release builder: `foundry
	// build-release` builds and exits here, before anything opens a window or
	// truncates the log of the editor that is probably the one running it (see
	// internal/release/job.go).
	if code, ran := devRunCommand(os.Args[1:]); ran {
		os.Exit(code)
	}

	// Which project: a release build's install; for the editor, the one it was
	// asked for or opened last (internal/project). Shaders stay the
	// executable's own when it carries them — they belong to the binary.
	projectDir, projectReason := chooseProject(fileutil.ExecutablePath, os.Args[1:])
	fileutil.ProjectPath = projectDir
	fileutil.ResourcesPath = fileutil.ResourcesDir(projectDir)
	fileutil.EngineShadersDir = fileutil.EngineShadersFor(fileutil.ExecutablePath, projectDir)
	// File > Open Project ends this process and starts another on the chosen
	// folder. Deferred first so it runs last, after SDL has let the window go.
	defer func() { relaunchInto(devPendingProject()) }()

	// Whatever resources/ is missing of its folders and its manifest. Ahead of
	// startupConfigure, because a release build reads that manifest there.
	scaffolded, scaffoldErr := scaffoldResources()

	// What this binary is: the engine, or a particular game. A release build
	// reads its manifest here — before the log is opened, because the manifest
	// is what says where the log belongs — and holds any failure until
	// startupVerify below has somewhere to report it. See startup_release.go.
	tuneGC()
	startupConfigure()
	// FOUNDRY_BENCH — after the manifest, whose window size it may override,
	// and before the log, which a benchmark run keeps apart from the game's.
	benchConfigure()

	// [SCENARIO 1/4/5] Open a persistent log file in the writable state
	// directory so startup crashes are visible on Windows (where the console
	// closes instantly). That is beside the executable in a development build
	// and the per-user data directory in an installed one — an installed game
	// cannot write into its own install directory. See fileutil.StatePath.
	logPath := fileutil.StatePath("foundry.log")
	if bench.on {
		logPath = fileutil.StatePath("foundry-bench.log")
	}
	// openBootLog falls back to the temporary folder, and sends Go's own crash
	// reports here too — the only record a windowsgui build has of one.
	logFile, ferr := openBootLog(logPath)
	var logSinks io.Writer = os.Stderr
	if ferr != nil {
		log.Printf("WARNING: could not create log file %s: %v — logging to stderr only", logPath, ferr)
	} else {
		// Not io.MultiWriter: a windowsgui release has no console, and its
		// failing stderr would keep every line out of the file. See logFanout.
		logSinks = newLogFanout(os.Stderr, logFile)
		log.SetOutput(logSinks)
		// os.File writes are unbuffered; entries reach the file even after os.Exit.
	}
	log.SetFlags(log.Ltime | log.Lmsgprefix)
	log.SetPrefix("[foundry] ")
	if bootLogPath != "" && bootLogPath != logPath {
		log.Printf("WARNING: could not write %s; logging to %s instead", logPath, bootLogPath)
	}
	// A panic anywhere in startup or the main loop is logged with its stack
	// and put in front of the player — see boot_failure.go.
	defer recoverToLog()

	// Anything scaffoldResources and startupConfigure could not report for want
	// of a log — in that order, so a manifest that could not be written is
	// explained before a release build gives up for want of it.
	logScaffold(scaffolded, scaffoldErr)
	startupVerify()

	// Everything logged from here on is also readable in the log window, and a
	// script's own output joins the same sinks. Installed after the prefix and
	// flags are set, because the tee has to strip exactly what they add.
	script.InstallLogCapture(logSinks)

	gfx.TextureNames = gfx.ScanTextures(fileutil.TexturesDir())
	log.Printf("scanned %d textures", len(gfx.TextureNames))

	// Load (or, on a fresh checkout, create) the global 256-colour palette
	// every texture in the texture editor composites against.
	texture.LoadGlobalPalette()

	// Any .ftex with more than one frame plays back at runtime — see
	// gfx.AdvanceAnimatedTextures below — whether or not its editor is ever
	// opened this session.
	texture.ScanAnimatedTextures()

	// Materials, before any level opens: a brush face, sprite or particle layer
	// names one and expects it to resolve the moment it is drawn. Loading them
	// also loads the textures only they reach — see gfx.LoadMaterials.
	//
	// The documents only, here: their textures are GPU uploads, and there is
	// no GL context until the window below has one (they are loaded right
	// after StandUpWorld). This used to call gfx.LoadMaterials, which was
	// harmless only because the library's scan found no files at all
	// (material.ScanMaterialFiles, docs/canyon/PHASE_5.md).
	log.Printf("loaded %d materials", material.LoadAll())

	// Biomes, before any level opens: a terrain's palette names them, and
	// its first mesh build reads their ground texture and tint.
	nb, berrs := world.LoadBiomes(world.BiomesDir())
	for _, err := range berrs {
		log.Printf("WARNING: biome: %v", err)
	}
	log.Printf("loaded %d biome files", nb)

	log.Printf("executable directory: %s", fileutil.ExecutablePath)
	log.Printf("project: %s (%s)", fileutil.ProjectPath, projectReason)
	log.Printf("resources directory: %s", fileutil.ResourcesPath)
	if fileutil.EngineShadersDir != "" {
		log.Printf("shaders: %s, the executable's own", fileutil.EngineShadersDir)
	}
	projectOpened(fileutil.ProjectPath)

	// Before Init, or the controllers open with SDL's label-ordered buttons.
	configurePadHints()
	if err = sdl.Init(sdl.INIT_EVERYTHING); err != nil {
		fatalStartup(fmt.Sprintf("SDL could not start: %v", err),
			"Make sure SDL2.dll sits beside the game's program, and reinstall the game if it does not.")
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
	// And the player's mouse settings, which nothing about the device gates.
	loadControlSettings()
	loadGraphicsSettings()

	// Every context from here on — this window's and the UI windows' that
	// share it — is a core-profile one at the engine's required version.
	glctx.RequestCoreProfile()

	// Creates the window and its GL context together, at the highest MSAA
	// sample count the platform actually accepts — the multisample buffer is
	// part of the pixel format, so it has to be settled here and can't change
	// afterwards. See antialias.go for why this is window-level MSAA rather
	// than an offscreen resolve pass, and why the request is tiered.
	platform.Window, platform.Context, err = render.CreateAntiAliasedWindow(platform.WinTitle, platform.WinWidth, platform.WinHeight,
		sdl.WINDOW_OPENGL|sdl.WINDOW_RESIZABLE)
	if err != nil {
		fatalGL(fmt.Errorf("could not create an OpenGL %d.%d window: %w", glctx.MajorVersion, glctx.MinorVersion, err))
	}
	defer platform.Window.Destroy()
	defer sdl.GLDeleteContext(platform.Context)
	// Fullscreen, when the game asks to open that way. Ahead of the size
	// read-back below so the projection is built from the size the window
	// actually ended up with.
	startupApplyWindow()
	// The window manager can hand back a size other than the one requested
	// above (scaling, tiling, a size clamped to the display); pick up what we
	// actually got so the viewport/projection setup below uses the real one.
	// F11 (ToggleFullscreen, input.go) still switches to borderless fullscreen.
	platform.WinWidth, platform.WinHeight = platform.Window.GetSize()
	platform.WinHalfWidth = platform.WinWidth / 2
	platform.WinHalfHeight = platform.WinHeight / 2
	mainWindowID, err := platform.Window.GetID()
	if err != nil {
		fatalStartupf("Window.GetID: %v", err)
	}
	devSetMainWindowID(mainWindowID)

	if err = gl.Init(); err != nil {
		fatalGL(fmt.Errorf("gl.Init: %w", err))
	}
	if err = glctx.CheckVersion(); err != nil {
		fatalGL(err)
	}

	// Log the OpenGL and GLSL versions so we know exactly what the driver
	// exposes.
	log.Printf("OpenGL version : %s", gl.GoStr(gl.GetString(gl.VERSION)))
	log.Printf("GLSL version   : %s", gl.GoStr(gl.GetString(gl.SHADING_LANGUAGE_VERSION)))
	log.Printf("GPU renderer   : %s", gl.GoStr(gl.GetString(gl.RENDERER)))

	// The runtime UI. Four installs and one GL init, all of them the same
	// inversion the function variables above use: internal/ui, internal/uifont
	// and internal/video sit below the log window and below the script runtime,
	// so the owner hands them what they need rather than reaching up for it.
	ui.Logf = func(format string, args ...any) { log.Printf(format, args...) }
	video.Logf = func(format string, args ...any) { log.Printf(format, args...) }
	uifont.LogMissing = func(name string, err error) {
		log.Printf("ui: font %q could not be loaded (%v); falling back to the built-in bitmap face", name, err)
	}
	ui.LoadActiveTheme()
	if err := uidraw.Init(); err != nil {
		log.Printf("ui renderer: %v", err)
	}
	defer uidraw.Delete()
	// The UI renderer needs nothing from resources/, so the startup progress
	// bar can go up now, before the seconds of shader compiles and loading
	// below — see boot_splash.go.
	bootSplashBegin()
	render.BootProgress = func(step, steps int, label string) {
		bootStep(0.05+0.40*float32(step)/float32(steps), label)
	}
	defer func() { render.BootProgress = nil }()

	// Reads back what CreateAntiAliasedWindow's request above actually got
	// granted and applies AntiAliasEnabled's default (on) — see antialias.go.
	render.ApplyAntiAliasGLState()

	// Settle the swap interval rather than inheriting whatever the driver
	// defaults to, so the frame rate below is the engine's decision and F12
	// has something to flip. See platform/vsync.go.
	platform.ApplyVSync()

	// Resolves the anisotropic filtering level for world textures. Must come
	// before the first texture upload — LoadTexture reads it at upload time.
	// See texture_filter.go for why mipmapping is the half of anti-aliasing
	// MSAA above can't do.
	// The shaders, the shadow pipeline, the passes that draw level content and
	// every uniform default — in internal/render, because the headless
	// renderer and the GL tests that draw need exactly the same sequence, and
	// each copy of it that existed was missing a different piece of it.
	//
	// The shader files are checked first so a stale or pruned install says so
	// in the log rather than failing as a compile error.
	for _, p := range []string{
		fileutil.ShaderPath(gfx.VertexShaderPath),
		fileutil.ShaderPath(gfx.FragmentShaderPath),
	} {
		if info, serr := os.Stat(p); serr != nil {
			log.Printf("MISSING shader file: %s (%v)", p, serr)
		} else {
			log.Printf("shader file OK: %s (%d bytes)", p, info.Size())
		}
	}
	if err := render.StandUpWorld(platform.WinWidth, platform.WinHeight); err != nil {
		fatalStartup(err.Error(), "The graphics driver could not build the game's shaders. Updating the graphics driver usually fixes this; if the shader files are missing, reinstall the game.")
	}
	log.Printf("shader program linked OK (id=%d)", gfx.Program)
	// The textures only the materials reach, now that there is a context.
	bootStep(0.45, "Loading materials")
	gfx.EnsureMaterialTextures()

	// Default camera position; overridden by the level's spawn point when it
	// has one.
	gfx.LoadCamera(mgl32.Vec3{0, world.EyeHeight, 3}, 0.0, float32(math.Pi/2.0))

	// What the engine comes up on. A development build starts on a blank
	// in-memory level with the editor over it; a release build opens the level
	// its manifest names, with no editor to open one from. See startup_dev.go
	// and startup_release.go.
	log.Printf("%s", startupDescribeLevel())
	bootStep(0.50, "Loading level")
	level, levelPath, levelsDir, err := startupLevel(func(frac float32, detail string) {
		bootProgress(0.50+0.35*frac, detail, false)
	})
	if err != nil {
		fatalStartup(fmt.Sprintf("The first level would not load: %v", err), "Reinstalling the game may fix this.")
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
	bootStep(0.85, "Preparing level")
	world.PlayerEntity = world.NewPlayer(gfx.Cam.Position.Sub(mgl32.Vec3{0, world.EyeHeight, 0}))
	gfx.Cam.Position = world.PlayerEntity.Position.Add(mgl32.Vec3{0, world.EyeHeight, 0})
	gfx.UpdateCamMatrix()
	// The startup level was loaded above, before there was a player to give
	// its player block's viewmodel to — so it is given now. A later level load
	// does this itself.
	if err := levelio.InstallPlayerModels(&level); err != nil {
		log.Printf("%v", err)
	}
	// What installLevel prepares for a level loaded later, prepared for this
	// one before its first frame: every chunk's and compiled surface's vertex
	// buffers, and the raycast trees.
	render.SyncChunkBatches(&level)
	collision.WarmRaycasts(&level)

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

	// The developer tools — the F3 overlay a release build can draw for
	// itself, and the tilde console — behind FOUNDRY_DEBUG_ENABLED. After the
	// UI renderer, because they are drawn with it. See internal/devui.
	bootStep(0.92, "Starting up")
	devui.Init()

	// Init level editor (must be after LoadLevel and OpenGL context ready).
	// A release build has no editor: both of these are stubs there, and
	// startupEnterGame below does the work EditorToggle's play half does here.
	devInit(&level, levelPath, levelsDir)
	defer devShutdown()

	// Into gameplay, for the build that has nowhere else to go. After the theme
	// and the UI renderer above, because it puts the game's first screens up.
	startupEnterGame(&level)

	// The running game's draw list, reused every frame so a steady HUD
	// allocates nothing. One list rather than one per screen: the screens draw
	// in sequence, never at once.
	var gameUIList uidraw.List

	log.Printf("startup complete — entering game loop")

	// A benchmark run swaps in its level and goes straight into play. See
	// bench.go.
	benchStart(&level)

	// The startup level is in; collect what building it left and size the
	// memory limit from what it keeps (gc_tuning.go).
	retuneGCAfterLoad()

	// Drain any events that accumulated during startup (level load, window creation,
	// etc.) so the first game-loop poll doesn't see a huge mouse-motion delta.
	bootStep(0.98, "Drawing the first frame")
	sdl.PumpEvents()
	for e := sdl.PollEvent(); e != nil; e = sdl.PollEvent() {
	}
	// The drain took the controllers' "added" events with it.
	startPads()
	platform.LastFrameCounter = sdl.GetPerformanceCounter()

	for platform.Running {
		// The frame's profile sections (render/profile.go). Off unless the F3
		// overlay or a benchmark is reading them.
		render.ProfileEnabled = bench.on || render.DebugOverlayVisible
		render.ProfileFrameBegin()
		render.ProfileBegin("frame")
		render.ProfileBegin("update")

		// Compute delta time (seconds), clamped to avoid spiral-of-death.
		//
		// From the performance counter rather than sdl.GetTicks: whole
		// milliseconds made a 60 Hz frame alternate between 16 and 17 ms (a 6%
		// swing in everything integrated against dt) and a 144 Hz one between
		// 6 and 7 ms.
		//
		// dt only: the free-running clocks below (animated textures, the wind
		// and water) read `now`, milliseconds since SDL started, which stays
		// small enough for the float32 a shader gets. The counter's raw ticks
		// are billions a second and would make those clocks jump.
		counter := sdl.GetPerformanceCounter()
		platform.DeltaTime = float32(float64(counter-platform.LastFrameCounter) / float64(sdl.GetPerformanceFrequency()))
		platform.LastFrameCounter = counter
		now := sdl.GetTicks()
		if platform.DeltaTime < 0.001 {
			platform.DeltaTime = 0.001
		}
		if platform.DeltaTime > 0.05 {
			platform.DeltaTime = 0.05
		}

		sdl.PumpEvents()

		// --- Process events first so mouse rotation is applied before movement ---
		for event := sdl.PollEvent(); event != nil; event = sdl.PollEvent() {
			// A controller plugged in or pulled out: nothing else wants the
			// event, and whichever handler ran first might drop it.
			if padDeviceEvent(event) {
				continue
			}
			noteKeyboardMouse(event)
			// The keybinding reference answers to F1 from every mode and every
			// window — ahead of the routing below, so a secondary window's own
			// handler can't eat F1 first — and swallows the Escape that closes
			// it before a sub-editor can read it as "cancel".
			if devRouteEvent(event) {
				continue
			}
			// The developer console, when it is up, owns the keyboard
			// outright — ahead of the game's UI and of gameplay both, so that
			// a command being typed is never also a menu keystroke or a shot.
			if devui.HandleEvent(event) {
				continue
			}
			// The running game's UI. Ahead of gameplay so a click on a menu
			// button is not also a shot, and behind the window router so a
			// secondary editor window still owns its own input. Consuming the
			// event is the whole of how gameplay input is gated — see
			// ui_input.go.
			// A rebind waiting for a press takes it before anything else can
			// act on it — see captureBindingEvent.
			if captureBindingEvent(event) {
				continue
			}
			if uiHandleEvent(event) {
				continue
			}
			// What the UI left, with the cursor free, is a press on the
			// world — see worldPointerEvent.
			if worldPointerEvent(event) {
				continue
			}
			if devActive() {
				devHandleEvent(event)
			}
			HandleEvent(event)
		}

		platform.KeyStates = sdl.GetKeyboardState()
		mouseX, mouseY, mouseButtons := sdl.GetMouseState()
		platform.MouseButtons = mouseButtons

		// Consuming the events is not enough to keep a typed command out of
		// the game: the held actions — walking, crouching, sprinting, a
		// vehicle's throttle — are read from this snapshot rather than from
		// edges, so typing `destroy` would walk the player forward on the D
		// and the E. Blanking it here is one place rather than a predicate at
		// every one of those reads.
		if devui.Open() {
			platform.KeyStates = noKeys(len(platform.KeyStates))
			platform.MouseButtons = 0
		}
		// The pads, read as a snapshot beside the keyboard's, and their
		// presses routed the way the key events above were — see
		// gamepad_input.go. Before SettleKeys, which reads the pad's half.
		pollPadInput(platform.DeltaTime)
		// A release the UI or the console took still lets go of the key as
		// far as scripts are concerned.
		platform.SettleKeys(platform.KeyStates, platform.MouseButtons)

		// Point the F1 reference at the focused window before anything draws.
		devUpdateFocus()

		// Whatever was typed into the developer console last frame, run now:
		// with the level in hand, between the events and the world's own tick,
		// which is the only point where `spawn` and `level` mean what they say.
		devui.Tick(&level)

		// A screen that wants a pointer releases mouse-look, and closing it
		// gives capture back. Edge-triggered, so this joins the editor toggle
		// and the focus handler as a third claimant rather than fighting them
		// every frame.
		uiApplyCursor()
		// Where the pointer is, now that whether it is free for this frame
		// has been decided.
		updateCursorState(mouseX, mouseY)

		// A script asking to leave, drained here for the same reason the level
		// load below is: quitting from inside a script would free the runtime
		// executing the call. Ahead of everything else this frame, so a menu's
		// Quit button does not also pay for a world tick and a draw.
		//
		// A development build hands the request to the editor, which ends the
		// play session instead of the process — see devInterceptQuit.
		if hooks.TakeQuitRequest() && !devInterceptQuit() {
			platform.Running = false
			break
		}

		// A level a script asked for, and one slice of building it. While a
		// load is in flight the world does not tick — half of it belongs to the
		// outgoing level and half to the incoming one — but the UI does, which
		// is what lets the loading screen over it animate.
		// A save, a load, a checkpoint, a retry or a restart a script asked
		// for, ahead of the level load below so a save is of the world the
		// last tick left and a load joins the queue the same way. See
		// savegame.go.
		handleSaveRequests(&level)
		beginRequestedLevelLoad()
		stepLevelLoad(&level)

		// A pausing screen freezes the player, the vehicles and the entities.
		// It does not freeze scripts: the menu's own handlers have to run, and
		// so does anything a script is animating behind it.
		//
		// A script writing `@level.prop.paused` freezes all of that, and the
		// level's scripts and the entities' clips as well. The screen stack's
		// scripts still run under it, since the menu that lifts the pause is
		// made of them — see script_pause.go.
		saveSettingsWhenSettled(platform.DeltaTime)

		scriptPaused := !devActive() && script.GamePaused()
		gamePaused := scriptPaused || !devActive() && (ui.Game.Pauses() || levelLoadPending())

		render.ProfileBegin("player input")
		// This frame's mouse look, for `@level.prop.look_delta`: nothing
		// unless applyMouseLook below spends some.
		gfx.LookDelta = mgl32.Vec2{}
		if devActive() {
			// Editor owns camera and input; gameplay logic is bypassed.
			devTick(platform.KeyStates)
		} else if !gamePaused {
			// The mouse motion banked since last frame, turned into look first
			// so the move below walks the way the player is now facing.
			if platform.PlayerControlEnabled {
				applyMouseLook(platform.DeltaTime)
				// The look stick, and aim assist with it — pad_look.go.
				applyPadLook(platform.DeltaTime)
				// A shot's kick (the `recoil` verb) turns the view the same
				// way, before the move reads where the player faces.
				if gfx.ApplyRecoil(platform.DeltaTime) {
					gfx.Cam.Direction = gfx.DirectionFromAngles(gfx.Cam.Yaw, gfx.Cam.Pitch)
					gfx.UpdateCamMatrix()
				}
			} else {
				dropMouseLook()
				gfx.ResetRecoil()
			}
			// A script turning the view (the `look_at` verb). It keeps the
			// view once it has arrived only while the player has no control
			// of it — see gfx/look_at.go.
			if gfx.ApplyLookAt(platform.DeltaTime, !platform.PlayerControlEnabled) {
				gfx.Cam.Direction = gfx.DirectionFromAngles(gfx.Cam.Yaw, gfx.Cam.Pitch)
				gfx.UpdateCamMatrix()
			}
		}

		render.ProfileEnd() // player input
		// Motion banked while the view could not turn must not arrive later.
		// Nor does a pad keep shaking under a menu or in the editor.
		if devActive() || gamePaused {
			dropMouseLook()
			gfx.ResetRecoil()
			platform.StopRumble()
		} else {
			rumbleForExplosions(&level)
		}

		// Video players whose widget went away last frame are closed here, at
		// the top of the frame rather than after the tick below. The UI editor
		// previews its own document from a secondary window, which renders at
		// the very end of the frame — a sweep between the two ticks would close
		// the players one of them is still using and reopen the files a
		// microsecond later.
		uidraw.SweepVideos()

		// One frame of the world, in internal/sim: the movers, the frames they
		// are expressed in, the particles, the sound, the UI's own tick and the
		// script tick, in an order whose every step says why it is where it is.
		//
		// It is a package rather than the two hundred lines that used to sit
		// here because the app is not the only thing that runs a frame —
		// cmd/foundry sim runs the same one headlessly, and while there were two
		// copies of it the headless one drifted.
		sim.Step(&level, platform.DeltaTime, sim.Gates{
			Dev:          devActive(),
			GamePaused:   gamePaused,
			ScriptPaused: scriptPaused,
			LoadPending:  levelLoadPending(),
			ViewW:        float32(platform.WinWidth),
			ViewH:        float32(platform.WinHeight),
		}, playOrKeyboard(platform.DeltaTime))

		// --- Debug stats: update CPU & memory ---
		render.ProfileBegin("debug stats")
		// Only while someone can see them: both are throttled anyway, but a
		// read nobody looks at is still a file read and a parse.
		if render.DebugOverlayVisible {
			render.DebugUpdateCPU()
			render.DebugUpdateMemory()
		}
		render.DebugStats.FPS = 1.0 / platform.DeltaTime
		render.DebugStats.FrameTimeMs = platform.DeltaTime * 1000.0
		// Counted per frame rather than cached at load: a chunk switched off by
		// a script has had its geometry released, and an overlay that still
		// counted it would be reporting the level file rather than what is
		// actually resident. (The cached version was also left behind by
		// load_level, which replaces the level wholesale.)
		render.DebugStats.ChunksLoaded, render.DebugStats.BrushesLoaded = 0, 0
		for i := range level.Chunks {
			if !level.Chunks[i].IsEnabled() {
				continue
			}
			render.DebugStats.ChunksLoaded++
			render.DebugStats.BrushesLoaded += len(level.Chunks[i].Brushes)
		}

		// Animated textures (the texture editor's animation stack) advance on
		// the same free-running clock as the water/biome-wind uniforms below,
		// queuing a re-upload whenever a texture's clock crosses into its next
		// frame — drained into the GL texture object right after, alongside
		// every other queued texture edit.
		render.ProfileEnd() // debug stats
		render.ProfileBegin("drains")
		gfx.AdvanceAnimatedTextures(float64(now) / 1000.0)

		// A sky material is folded into the level's own Sky rather than read
		// at draw time — see world/sky_material.go for why the ease_sky
		// transition makes that the right choice — so it is the one domain
		// that has to be told when the material library moves. One integer
		// compare on a frame where it did not.
		//
		// Here rather than in sim.Step because it exists for live EDITING: a
		// level's sky is already folded in when it loads, and the headless sim
		// has no material editor to drag a coverage slider in.
		world.SyncSkyMaterial(&level)

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

		// Likewise for an Unwrap & Paint — a model's or a level's brushes' —
		// asking to open the texture editor on a freshly unwrapped atlas:
		// creating a window from inside another
		// window's draw would insert into the map being ranged over and leave
		// the wrong GL context current. See pendingTexEditor.
		devDrainWork()
		render.ProfileEnd() // drains

		// A benchmark pins the camera here: after everything that places it,
		// before the first thing that reads it to draw.
		benchPlaceCamera()
		render.ProfileEnd() // update

		// The 3D world, from its per-frame uniforms to the viewmodel: the
		// whole draw, in internal/render so that `foundry render world` and a
		// headless play session draw the same picture this frame does rather
		// than a second copy of it. The clock is the one every animated thing
		// in the frame is integrated against.
		frameViewport := [4]int32{0, 0, platform.WinWidth, platform.WinHeight}
		render.DrawWorld(&level, render.WorldDraw{
			Now:        float64(now) / 1000.0,
			Editor:     devActive(),
			Wireframe:  devWireframe(),
			Skip:       benchSkips,
			Markers:    devRenderMarkers,
			BrushGhost: devRenderBrushGhost,
			// The window, at the viewport RefreshWindowSize set: declared
			// so no pass has to ask (render/frame_target.go).
			Target: &render.FrameTarget{Viewport: frameViewport},
		})

		// --- Runtime UI ---
		// Screen space, after everything in the world and before any imgui.
		// Not drawn while the editor is active, for the same reason scripts do
		// not run there: what is on screen in the editor is the level, not the
		// game.
		render.ProfileBegin("ui")
		// What DrawWorld left GL in, said so the screen-space passes below
		// restore it without asking (gfx/known_gl_state.go). Not with the
		// editor up: its own frame draws in between.
		if !devActive() {
			gfx.KnownGLState = gfx.AfterWorldGLState(frameViewport)
		}
		if !devActive() {
			// One pass per screen, bottom to top. Each has its own canvas and
			// therefore its own scale, so they cannot share a projection.
			for _, s := range ui.Game.Screens() {
				uidraw.RenderScreen(&gameUIList, s, ui.Game.Interaction(),
					platform.WinWidth, platform.WinHeight)
			}
		}

		// Draw editor overlays and ImGui UI when editor is active
		if devActive() {
			devRender()
		}

		// Draw debug overlay and keybinding reference (both use ImGui,
		// independent of the editor — when it is active, EditorRender has
		// already drawn them inside its own frame).
		if !devActive() {
			devRenderOverlays(platform.DeltaTime)
		}

		// The developer tools, drawn with the runtime's own screen-space pass
		// rather than with imgui — which is what lets a release build have
		// them at all. Last, and over everything: an overlay the game's HUD
		// could cover would be no use on the frame that mattered.
		devui.Render(&gameUIList, platform.WinWidth, platform.WinHeight)
		gfx.KnownGLState = nil
		render.ProfileEnd() // ui
		render.ProfileEnd() // frame

		// Where a GPU-bound frame shows up on the CPU: the swap blocks until
		// the GPU has caught up.
		benchCaptureShot()
		// A screenshot Claude asked for, taken at the one moment the back
		// buffer holds the finished frame.
		devCaptureClaudeShot()
		render.ProfileBegin("swap")
		platform.Window.GLSwap()
		render.ProfileEnd()
		// The first frame is up over the startup bar; log what it all took.
		bootSplashEnd()
		// A GPU-bound frame's wait, here and named, rather than inside
		// whichever pass the driver's queue filled up in (render/frame_pace.go).
		render.ProfileBegin("gpu wait")
		render.PaceFrame()
		render.ProfileEnd()

		// Secondary windows (texture editor, etc.) render last, each in its
		// own GL/imgui context, restoring the main ones before the next
		// iteration touches world GL state again.
		devRenderSecondaryWindows(platform.DeltaTime)

		benchEndFrame()
	}
}

// fatalGL ends startup over a GL context the engine cannot run on. A release
// build has no console to log to, so the reason goes in a message box too.
//
// The usual cause on Windows is no vendor driver at all: Windows' own display
// driver ("GDI Generic", "Microsoft Basic Render Driver") offers OpenGL 1.1,
// and only NVIDIA's, AMD's or Intel's driver adds a modern one.
func fatalGL(err error) {
	fatalStartup(err.Error(), fmt.Sprintf("This game needs OpenGL %d.%d, which comes with your graphics card's driver. "+
		"Install the latest driver from NVIDIA, AMD or Intel (whichever made your graphics card), restart, and try again. "+
		"On a virtual machine or remote desktop, enable 3D acceleration or run the game on the machine itself.",
		glctx.MajorVersion, glctx.MinorVersion))
}
