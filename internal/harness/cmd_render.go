package harness

// cmd_render.go — `foundry render`, a picture of something without playing the
// game to see it.
//
//	foundry render model models/marine.fmdl -pose walk:0.25 -o walk.png
//	foundry render screen hud -o hud.png
//	foundry render viewmodel -level level1 -clip rifle_fire -at 0.1 -o fire.png
//
// Three kinds, because three kinds were what kept being rebuilt by hand: a model
// to check a mesh or a clip, a .fui to check a HUD's layout, and the player's own
// arms to check a viewmodel. Each goes through the engine's own code — the model
// through internal/preview (which poses with the engine's pose maths), the screen
// through uidraw.RenderScreen, the arms through render.RenderViewmodel — so what
// comes out is what the game draws rather than an approximation of it.
//
// WHAT IS MISSING is the world itself: there is no `render world` yet, because
// the frame's DRAW half is still spelled out inline in app.Run the way its
// simulation half was before internal/sim. Extracting it is the same job, and
// until it is done a headless world view would be a second copy of it — which is
// the thing this whole package exists to stop.

import (
	"errors"
	"flag"
	"fmt"
	"image"
	"io"
	"strconv"
	"strings"

	"foundry/internal/cliflag"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/glctx"
	"foundry/internal/levelio"
	"foundry/internal/preview"
	"foundry/internal/render"
	"foundry/internal/ui"
	"foundry/internal/uidraw"
	"foundry/internal/world"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

// RenderCommand is the verb: `foundry render`.
const RenderCommand = "render"

// RenderMain draws one picture to a file.
func RenderMain(root string, args []string, out io.Writer) int {
	usage := func() {
		fmt.Fprintf(out, "foundry %s model|screen|viewmodel [flags] — draw something to a .png\n\n"+
			"  model <path.fmdl>   the mesh, posed, with no GL at all\n"+
			"  screen <name>       a .fui through the real UI renderer\n"+
			"  viewmodel           the player's own arms, through the real viewmodel pass\n\n"+
			"Run one with -h for its flags.\n", RenderCommand)
	}
	if len(args) == 0 {
		usage()
		return 2
	}
	kind, rest := args[0], args[1:]
	switch kind {
	case "model":
		return renderModel(root, rest, out)
	case "screen":
		return renderScreen(root, rest, out)
	case "viewmodel":
		return renderViewmodel(root, rest, out)
	}
	usage()
	return 2
}

// ---------------------------------------------------------------- model

func renderModel(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("render model", flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	dst := fs.String("o", "model.png", "the .png to write")
	pose := fs.String("pose", "", "`clip:seconds` — the pose to draw, e.g. walk:0.25")
	view := fs.String("view", "front", "front, back, left, right, top or iso")
	size := fs.String("size", "512x512", "image size")
	frame := fs.String("frame", "", "cx,cy,cz,span — the box to fill the image with, in model units")
	tex := fs.String("texture", "", "paint with this texture instead of the one the model names")
	untextured := fs.Bool("untextured", false, "draw the mesh shaded rather than painted")
	rest, code, ok := parse(fs, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprintln(out, "usage: foundry render model <path.fmdl> [flags]")
		return 2
	}
	fileutil.ResourcesPath = fileutil.ResourcesDir(*project)

	w, h, err := parseSize(*size)
	if err != nil {
		fmt.Fprintln(out, err)
		return 2
	}
	opts := preview.Options{W: w, H: h, Pose: *pose, View: *view, Texture: *tex, Untextured: *untextured}
	if *frame != "" {
		box, err := parseFrame(*frame)
		if err != nil {
			fmt.Fprintln(out, err)
			return 2
		}
		opts.Frame = &box
	}
	img, err := preview.ModelFile(rest[0], opts)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	return write(*dst, img, out)
}

// ---------------------------------------------------------------- screen

func renderScreen(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("render screen", flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	dst := fs.String("o", "screen.png", "the .png to write")
	size := fs.String("size", "1920x1080", "the framebuffer to solve the layout against")
	rest, code, ok := parse(fs, args)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		fmt.Fprintln(out, "usage: foundry render screen <name> [flags]")
		return 2
	}
	fileutil.ResourcesPath = fileutil.ResourcesDir(*project)
	w, h, err := parseSize(*size)
	if err != nil {
		fmt.Fprintln(out, err)
		return 2
	}

	if err := glctx.Acquire(int32(w), int32(h)); err != nil {
		fmt.Fprintf(out, "no GL context (%v) — a screen render needs a display and a GL 4.3 driver\n", err)
		return 1
	}
	defer glctx.Release()

	// The texture table, so a widget naming an image finds it. In GL, which is
	// why this is not the harness's own headless setup: here the uploads are
	// wanted.
	gfx.TextureNames = gfx.ScanTextures(fileutil.TexturesDir())
	ui.LoadActiveTheme()
	if err := uidraw.Init(); err != nil {
		fmt.Fprintln(out, "ui renderer:", err)
		return 1
	}
	defer uidraw.Delete()

	name := rest[0]
	screen, err := ui.LoadScreen(ui.UIPath(name))
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}

	target, err := glctx.NewTarget(int32(w), int32(h))
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	defer target.Delete()
	target.Bind()
	clearTarget()

	// Three steps, and all three are needed, which is why this is a command
	// rather than a recipe: the first pass BAKES the glyphs a label needs (a
	// glyph seen for the first time draws as a box), SyncFontAtlases queues the
	// pages that changed and DrainTextureUploads puts them on the GPU, and the
	// second pass is the one that comes out with text in it.
	var list uidraw.List
	uidraw.RenderScreen(&list, screen, &ui.Interaction{}, int32(w), int32(h))
	uidraw.SyncFontAtlases()
	gfx.DrainTextureUploads()
	clearTarget()
	uidraw.RenderScreen(&list, screen, &ui.Interaction{}, int32(w), int32(h))
	img := target.Image()
	target.Unbind()
	return write(*dst, img, out)
}

// ---------------------------------------------------------------- viewmodel

func renderViewmodel(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("render viewmodel", flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	dst := fs.String("o", "viewmodel.png", "the .png to write")
	level := fs.String("level", "", "the level whose player block carries the arms (required)")
	clip := fs.String("clip", "", "the clip to pose the arms with")
	at := fs.Float64("at", 0, "how far into the clip, in seconds")
	size := fs.String("size", "1280x720", "image size")
	if _, code, ok := parse(fs, args); !ok {
		return code
	}
	if *level == "" {
		fmt.Fprintln(out, "usage: foundry render viewmodel -level <name> [flags]")
		return 2
	}
	w, h, err := parseSize(*size)
	if err != nil {
		fmt.Fprintln(out, err)
		return 2
	}

	if err := glctx.Acquire(int32(w), int32(h)); err != nil {
		fmt.Fprintf(out, "no GL context (%v) — a viewmodel render needs a display and a GL 4.3 driver\n", err)
		return 1
	}
	defer glctx.Release()

	fileutil.ResourcesPath = fileutil.ResourcesDir(*project)
	gfx.TextureNames = gfx.ScanTextures(fileutil.TexturesDir())
	// The world program and every pass that draws through it, in the engine's
	// own sequence — including the shadow samplers, without which every draw is
	// GL_INVALID_OPERATION and nothing rasterises at all.
	if err := render.StandUpWorld(int32(w), int32(h)); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}

	// A real load, with the uploads ON: the arms have to reach the GPU.
	lvl, err := levelio.LoadLevel(levelio.LevelPath(*level))
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	world.PlayerEntity = world.NewPlayer(mgl32.Vec3{})
	if err := levelio.InstallPlayerModels(&lvl); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	if len(world.PlayerEntity.Models) == 0 {
		fmt.Fprintf(out, "level %s has no player models to draw\n", *level)
		return 1
	}
	// The pose, if one was asked for: set the clip through the engine's own Play
	// — the name may be an ANIMATION on the attachment rather than a clip in the
	// file, and only Play knows the difference — then park its clock at the
	// moment wanted and pose once.
	if *clip != "" {
		posed := false
		for i := range world.PlayerEntity.Models {
			m := &world.PlayerEntity.Models[i]
			if !m.Play(*clip) && !playClipDirect(m, *clip) {
				continue
			}
			m.Anim.Time, m.Anim.Playing = float32(*at), false
			m.UpdatePose(0, world.PlayerEntity.ID, true)
			posed = true
		}
		if !posed {
			fmt.Fprintf(out, "no attachment of the player has a clip or animation named %q\n", *clip)
			return 1
		}
	}
	gfx.LoadCamera(mgl32.Vec3{0, world.EyeHeight, 0}, 0, 0)
	gfx.UpdateCamMatrix()
	world.UploadLights(lvl.Lights)

	target, err := glctx.NewTarget(int32(w), int32(h))
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	defer target.Delete()
	target.Bind()
	clearTarget()
	render.RenderViewmodel()
	img := target.Image()
	target.Unbind()
	return write(*dst, img, out)
}

// playClipDirect sets a clip by its name in the .fmdl, for the many clips a
// viewmodel has that no `animations:` entry names — a fitting tool writes
// rifle_fire_ads into the file and the level never mentions it.
func playClipDirect(m *world.EntityModel, clip string) bool {
	if m.Anim == nil {
		return false
	}
	return m.Anim.SetClip(clip, 0)
}

// ---------------------------------------------------------------- shared

// clearTarget wipes the target to a flat backdrop. Transparent black would be
// truer to a HUD drawn over a world, but a PNG of it is unreadable in every
// viewer that shows transparency as white.
func clearTarget() {
	gl.ClearColor(0.08, 0.09, 0.11, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
}

// parse fills the flag set from args wherever the flags appear — a path may come
// before them, which is how anyone types it — and answers the arguments that are
// not flags.
func parse(fs *flag.FlagSet, args []string) (rest []string, code int, ok bool) {
	rest, err := cliflag.Parse(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, 0, false
		}
		return nil, 2, false
	}
	return rest, 0, true
}

func parseSize(s string) (int, int, error) {
	w, h, ok := strings.Cut(strings.ToLower(s), "x")
	if !ok {
		return 0, 0, fmt.Errorf("size %q: want WxH", s)
	}
	wi, err1 := strconv.Atoi(strings.TrimSpace(w))
	hi, err2 := strconv.Atoi(strings.TrimSpace(h))
	if err1 != nil || err2 != nil || wi <= 0 || hi <= 0 {
		return 0, 0, fmt.Errorf("size %q: want WxH", s)
	}
	return wi, hi, nil
}

func parseFrame(s string) ([4]float32, error) {
	var box [4]float32
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return box, fmt.Errorf("frame %q: want cx,cy,cz,span", s)
	}
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
		if err != nil {
			return box, fmt.Errorf("frame %q: %w", s, err)
		}
		box[i] = float32(f)
	}
	return box, nil
}

func write(path string, img image.Image, out io.Writer) int {
	if err := glctx.SavePNG(path, img); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	b := img.Bounds()
	fmt.Fprintf(out, "wrote %s (%dx%d)\n", path, b.Dx(), b.Dy())
	return 0
}
