package app

// bench.go — a repeatable frame-time measurement of a real level.
//
// Set FOUNDRY_BENCH and the engine loads a level, puts the camera at each of a
// list of poses, renders a fixed number of frames at each with vsync off, and
// writes where the time went — per pass, CPU and GPU, from render/profile.go —
// to a report file, then exits. Nothing in the frame is faked: scripts, AI,
// emitters and every pass run as they do in play. Only the camera is pinned,
// after the player's own camera has been placed, so the pose is exactly what is
// drawn whatever the level's scripts do with the player.
//
//	FOUNDRY_BENCH='level=level1;compiled=1;frames=300;warm=90;
//	               at=2,102.1,-1,1.71,0|1.5,102.7,-36.7,-2.36,0'
//
//	level     level name under resources/levels (required)
//	compiled  1 loads the .fbsp, as a release build does; 0 the .yaml (default 1)
//	at        poses, '|'-separated: x,y,z,yaw,pitch of the EYE; "spawn" leaves
//	          the camera to the game
//	frames    measured frames per pose (default 300)
//	warm      frames per pose before measuring, for first-use stalls and the
//	          smoothed numbers to settle (default 90)
//	out       report path (default bench.txt in the state directory)
//	size      WxH window size (default the engine's own)
//	hide      comma-separated section names to skip, for A/B runs: a hidden
//	          pass is not drawn at all (see benchSkips)
//	cpuprofile  write a Go CPU profile to this path, for go tool pprof: from
//	          the end of the first pose's warm-up to the end of the run
//	shots     a directory: the last frame of each pose is saved there as
//	          pose_<n>.png, so a run shows what it measured
//	gi_debug  a live GI debug view (lighting, probes, field), as the console's
//	          gi_debug sets it, so a run's pictures show what GI sees
//	menus     answer: a modal screen that comes up is answered with its
//	          initial_focus, as `foundry play` does, so a level that opens on
//	          a menu is measured playing rather than paused behind it

import (
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/hooks"
	"foundry/internal/levelio"
	"foundry/internal/platform"
	"foundry/internal/render"
	"foundry/internal/ui"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

type benchPose struct {
	label      string
	spawn      bool
	eye        mgl32.Vec3
	yaw, pitch float32
}

type benchConfig struct {
	level    string
	compiled bool
	poses    []benchPose
	frames   int
	warm     int
	out      string
	hide     map[string]bool
	cpuprof  string
	shots    string
	answer   bool
}

var bench struct {
	on  bool
	cfg benchConfig

	pose   int
	frame  int // within the pose, warm frames included
	last   time.Time
	frameT []float64 // measured wall frame times, ms
	counts benchCounts
	report strings.Builder
	prof   *os.File
	// answered is the modal screens menus=answer has clicked through.
	answered map[string]bool
}

// benchCounts is the overlay's scene counters, summed over the measured frames.
type benchCounts struct {
	staticDrawn, brushCalls, brushes, chunks, sprites, particles, spriteDraws, volumeDraws, scenery int
	pointShadows, pointShadowRenders                                                                int
	giBuilt, giPending, giTraced                                                                    int
}

// benchParse reads FOUNDRY_BENCH. ok is false when it is unset.
func benchParse() (benchConfig, bool, error) {
	spec := strings.TrimSpace(os.Getenv("FOUNDRY_BENCH"))
	if spec == "" {
		return benchConfig{}, false, nil
	}
	c := benchConfig{compiled: true, frames: 300, warm: 90, hide: map[string]bool{}}
	for _, kv := range strings.Split(spec, ";") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "level":
			c.level = v
		case "compiled":
			c.compiled = v != "0"
		case "frames":
			c.frames, _ = strconv.Atoi(v)
		case "warm":
			c.warm, _ = strconv.Atoi(v)
		case "out":
			c.out = v
		case "cpuprofile":
			c.cpuprof = v
		case "shots":
			c.shots = v
		case "menus":
			c.answer = v == "answer"
		case "gi_debug":
			m, ok := render.ParseGIDebugMode(v)
			if !ok {
				return c, true, fmt.Errorf("gi_debug %q: want off, lighting, probes or field", v)
			}
			render.GIDebug = m
		case "size":
			w, h, _ := strings.Cut(v, "x")
			wi, _ := strconv.Atoi(w)
			hi, _ := strconv.Atoi(h)
			if wi > 0 && hi > 0 {
				platform.WinWidth, platform.WinHeight = int32(wi), int32(hi)
				platform.WinHalfWidth, platform.WinHalfHeight = int32(wi/2), int32(hi/2)
			}
		case "hide":
			for _, n := range strings.Split(v, ",") {
				c.hide[strings.TrimSpace(n)] = true
			}
		case "at":
			for _, p := range strings.Split(v, "|") {
				p = strings.TrimSpace(p)
				if p == "spawn" {
					c.poses = append(c.poses, benchPose{label: "spawn", spawn: true})
					continue
				}
				f := strings.Split(p, ",")
				if len(f) != 5 {
					return c, true, fmt.Errorf("pose %q: want x,y,z,yaw,pitch", p)
				}
				var n [5]float32
				for i := range f {
					x, err := strconv.ParseFloat(strings.TrimSpace(f[i]), 32)
					if err != nil {
						return c, true, fmt.Errorf("pose %q: %v", p, err)
					}
					n[i] = float32(x)
				}
				c.poses = append(c.poses, benchPose{label: p, eye: mgl32.Vec3{n[0], n[1], n[2]}, yaw: n[3], pitch: n[4]})
			}
		default:
			return c, true, fmt.Errorf("unknown key %q", k)
		}
	}
	if c.level == "" {
		return c, true, fmt.Errorf("level= is required")
	}
	if len(c.poses) == 0 {
		c.poses = []benchPose{{label: "spawn", spawn: true}}
	}
	if c.out == "" {
		c.out = fileutil.StatePath("bench.txt")
	}
	return c, true, nil
}

// benchSkips reports whether the benchmark was asked to leave a pass out.
func benchSkips(name string) bool { return bench.on && bench.cfg.hide[name] }

// benchConfigure reads FOUNDRY_BENCH. Called before the window is created,
// because size= decides what it is created at.
func benchConfigure() {
	cfg, on, err := benchParse()
	if !on {
		return
	}
	if err != nil {
		log.Fatalf("FOUNDRY_BENCH: %v", err)
	}
	bench.on, bench.cfg = true, cfg
}

// benchStart loads the level and puts the engine into play on it. Called once,
// after startup has finished and before the first frame.
func benchStart(level *world.Level) {
	if !bench.on {
		return
	}
	cfg := bench.cfg
	name := cfg.level
	var built world.Level
	var err error
	t0 := time.Now()
	if cfg.compiled {
		built, err = levelio.LoadCompiledLevel(filepath.Join(levelio.LevelsDir(), name+levelio.CompiledExt))
	} else {
		var job *levelio.LoadJob
		job, err = levelio.BeginLoad(levelio.LevelPath(name))
		if err == nil {
			for !job.Step() {
			}
			err = job.Err()
			built = *job.Level()
		}
	}
	if err != nil {
		log.Fatalf("bench: loading %s: %v", name, err)
	}
	installLevel(level, &built)
	if devActive() {
		devToggle() // into play, as tilde would
	}
	fmt.Fprintf(&bench.report, "bench %s (%s) — load %.1fs, %dx%d, MSAA %dx\n",
		name, map[bool]string{true: "compiled", false: "yaml"}[cfg.compiled],
		time.Since(t0).Seconds(), platform.WinWidth, platform.WinHeight, render.AntiAliasGrantedSamples)

	platform.VSyncEnabled = false
	platform.ApplyVSync()
	gfx.SetMouseLock(false)
	render.ProfileEnabled = true
	bench.last = time.Now()
}

// benchPlaceCamera pins the camera to the current pose. Called every frame
// after the game has placed its own camera and before anything is drawn.
func benchPlaceCamera() {
	if !bench.on || bench.pose >= len(bench.cfg.poses) {
		return
	}
	if bench.cfg.answer {
		for _, sc := range ui.Game.Screens() {
			if sc.Modal && sc.InitialFocus != "" && !bench.answered[sc.ID] {
				if bench.answered == nil {
					bench.answered = map[string]bool{}
				}
				bench.answered[sc.ID] = true
				hooks.RecordUIEvent(hooks.UIEvent{Atom: "click", Screen: sc.ID, Widget: sc.InitialFocus})
			}
		}
	}
	p := bench.cfg.poses[bench.pose]
	if p.spawn {
		return
	}
	gfx.Cam.Position = p.eye
	gfx.Cam.Yaw, gfx.Cam.Pitch = p.yaw, p.pitch
	gfx.Cam.Direction = gfx.DirectionFromAngles(p.yaw, p.pitch)
	gfx.UpdateCamMatrix()
}

// benchEndFrame counts the frame just finished and moves on to the next pose,
// or ends the run, when this one has had its frames.
func benchEndFrame() {
	if !bench.on {
		return
	}
	now := time.Now()
	dt := float64(now.Sub(bench.last).Nanoseconds()) / 1e6
	bench.last = now
	bench.frame++

	cfg := &bench.cfg
	switch {
	case bench.frame == cfg.warm:
		render.ProfileResetTotals()
		bench.frameT = bench.frameT[:0]
		bench.counts = benchCounts{}
		if cfg.cpuprof != "" && bench.prof == nil {
			if f, err := os.Create(cfg.cpuprof); err != nil {
				log.Printf("bench: %v", err)
			} else if err := pprof.StartCPUProfile(f); err != nil {
				log.Printf("bench: %v", err)
				f.Close()
			} else {
				bench.prof = f
			}
		}
	case bench.frame > cfg.warm:
		bench.frameT = append(bench.frameT, dt)
		s := &render.DebugStats
		c := &bench.counts
		c.staticDrawn += s.StaticDrawn
		c.brushCalls += s.BrushDrawCalls
		c.brushes += s.BrushesDrawn
		c.chunks += s.ChunksDrawn
		c.sprites += s.Sprites
		c.particles += s.Particles
		c.spriteDraws += s.SpriteDraws
		c.volumeDraws += s.VolumeDraws
		c.scenery += s.Scenery
		c.pointShadows += s.PointShadows
		c.pointShadowRenders += s.PointShadowRenders
		g := render.GIStatsLast
		c.giBuilt += g.BricksBuilt
		c.giPending += g.BricksPending
		c.giTraced += g.ProbesTraced
	}
	if bench.frame < cfg.warm+cfg.frames {
		return
	}

	benchWritePose()
	bench.pose++
	bench.frame = 0
	if bench.pose >= len(cfg.poses) {
		if bench.prof != nil {
			pprof.StopCPUProfile()
			bench.prof.Close()
		}
		if err := os.WriteFile(cfg.out, []byte(bench.report.String()), 0o644); err != nil {
			log.Printf("bench: %v", err)
		}
		log.Printf("bench: report written to %s\n%s", cfg.out, bench.report.String())
		platform.Running = false
	}
}

func benchWritePose() {
	r := &bench.report
	p := bench.cfg.poses[bench.pose]
	ft := append([]float64(nil), bench.frameT...)
	sort.Float64s(ft)
	var sum float64
	for _, v := range ft {
		sum += v
	}
	n := float64(len(ft))
	if n == 0 {
		return
	}
	pct := func(q float64) float64 { return ft[int(q*(n-1))] }
	avg := sum / n
	fmt.Fprintf(r, "\n=== pose %s\n", p.label)
	fmt.Fprintf(r, "frame  avg %.2f ms (%.1f fps)  p50 %.2f  p95 %.2f  max %.2f\n",
		avg, 1000/avg, pct(0.5), pct(0.95), ft[len(ft)-1])
	c := bench.counts
	per := func(v int) float64 { return float64(v) / n }
	fmt.Fprintf(r, "scene  static %.0f  brushes %.0f in %.0f calls  chunks %.0f  scenery %.0f  sprites %.0f (%.0f particles, %.0f draws)  volume marches %.1f\n",
		per(c.staticDrawn), per(c.brushes), per(c.brushCalls), per(c.chunks), per(c.scenery),
		per(c.sprites), per(c.particles), per(c.spriteDraws), per(c.volumeDraws))
	fmt.Fprintf(r, "point shadows re-rendered %.2f of %.0f a frame\n", per(c.pointShadowRenders), per(c.pointShadows))
	fmt.Fprintf(r, "gi  bricks built %.0f, still waiting %.0f; probes traced %.0f a frame\n",
		per(c.giBuilt), per(c.giPending), per(c.giTraced))
	fmt.Fprintf(r, "%-34s %9s %9s %9s %9s\n", "section", "cpu avg", "cpu max", "gpu avg", "gpu max")
	for _, s := range render.ProfileSamples() {
		fmt.Fprintf(r, "%-34s %9.2f %9.2f %9.2f %9.2f\n",
			strings.Repeat("  ", s.Depth)+s.Name, s.AvgCPU, s.MaxCPU, s.AvgGPU, s.MaxGPU)
	}
}

// benchCaptureShot saves the frame about to be presented when it is the last
// one of a pose and the run asked for shots. Called before the swap: after it
// the back buffer's contents are undefined.
func benchCaptureShot() {
	if !bench.on || bench.cfg.shots == "" || bench.pose >= len(bench.cfg.poses) {
		return
	}
	if bench.frame+1 != bench.cfg.warm+bench.cfg.frames {
		return
	}
	w, h := int(platform.WinWidth), int(platform.WinHeight)
	px := make([]uint8, w*h*4)
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, 0)
	gl.ReadBuffer(gl.BACK)
	gl.PixelStorei(gl.PACK_ALIGNMENT, 1)
	glext.ReadPixels(0, 0, int32(w), int32(h), gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(px))
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		// GL's rows run bottom-up.
		src := px[(h-1-y)*w*4 : (h-y)*w*4]
		dst := img.Pix[y*img.Stride : y*img.Stride+w*4]
		copy(dst, src)
		for x := 3; x < len(dst); x += 4 {
			dst[x] = 255
		}
	}
	if err := os.MkdirAll(bench.cfg.shots, 0o755); err != nil {
		log.Printf("bench: %v", err)
		return
	}
	path := filepath.Join(bench.cfg.shots, fmt.Sprintf("pose_%d.png", bench.pose))
	f, err := os.Create(path)
	if err != nil {
		log.Printf("bench: %v", err)
		return
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Printf("bench: %v", err)
	}
}
