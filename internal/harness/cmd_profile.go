package harness

// cmd_profile.go — `foundry profile-load`, where a level's load time goes.
//
//	foundry profile-load -level level1 -compiled
//	foundry profile-load -level level1 -compiled -cpuprofile load.prof
//
// The load is a job stepped a slice at a time (levelio.LoadJob) so the loading
// screen over it can animate, and each slice reports which stage it is in. That
// makes the measurement easy and it is why this is three dozen lines rather than
// a profiler: step the job, and every time the stage label changes, print how
// long the last one took.
//
// It was a throwaway command the last time the load was made faster, which is
// exactly the kind of thing this package is for. The numbers are honest about
// what they are: the GL uploads are off here, so what is measured is the CPU half
// — parsing, decoding, building meshes — and not the driver's.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/pprof"
	"strings"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/levelio"
)

// ProfileLoadCommand is the verb: `foundry profile-load`.
const ProfileLoadCommand = "profile-load"

// ProfileLoadMain times a level load, stage by stage.
func ProfileLoadMain(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet(ProfileLoadCommand, flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	res := fs.String("resources", "", "the resources/ directory itself, if it is not <root>/resources")
	level := fs.String("level", "", "level name (required)")
	compiled := fs.Bool("compiled", false, "load the .fbsp, as a release build does, instead of the .yaml")
	cpuProfile := fs.String("cpuprofile", "", "write a CPU profile of the load to this file (go tool pprof)")
	fs.Usage = func() {
		fmt.Fprintf(out, "foundry %s — time a level load, stage by stage.\n\nFlags:\n", ProfileLoadCommand)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *level == "" {
		fs.Usage()
		return 2
	}
	dir := *res
	if dir == "" {
		dir = fileutil.ResourcesDir(*project)
	}
	fileutil.ResourcesPath = dir

	// Headless, so nothing here waits on a driver — and so this runs on a
	// machine with no display at all.
	gfx.GeometryUploadsEnabled, gfx.TextureUploadsEnabled = false, false
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}
	gfx.TextureNames = gfx.ScanTextures(fileutil.TexturesDir())

	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			fmt.Fprintln(out, "cpuprofile:", err)
			return 1
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintln(out, "cpuprofile:", err)
			return 1
		}
		defer pprof.StopCPUProfile()
	}

	path := levelio.LevelPath(*level)
	var job *levelio.LoadJob
	var err error
	t0 := time.Now()
	if *compiled {
		job, err = levelio.BeginLoadCompiled(strings.TrimSuffix(path, ".yaml") + ".fbsp")
	} else {
		job, err = levelio.BeginLoad(path)
	}
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	began := time.Since(t0)
	fmt.Fprintf(out, "%-28s %8.1f ms\n", "begin (parse/decode)", float64(began.Microseconds())/1000)

	// One Step is one slice of work. Stepping until the label changes is what
	// gives a per-stage time; steps are counted too, because "one stage, four
	// thousand steps" is a different problem from "one stage, one slow step" and
	// the frame-paced load that used to take twenty seconds was the former.
	stage, stageStart, steps, total := "", time.Now(), 0, 0
	for !job.Done() {
		if _, label := job.Progress(); label != stage {
			if stage != "" {
				fmt.Fprintf(out, "%-28s %8.1f ms  (%d steps)\n", stage,
					float64(time.Since(stageStart).Microseconds())/1000, steps)
			}
			stage, stageStart, steps = label, time.Now(), 0
		}
		job.Step()
		steps++
		total++
		if job.Err() != nil {
			fmt.Fprintln(out, job.Err())
			return 1
		}
	}
	if stage != "" {
		fmt.Fprintf(out, "%-28s %8.1f ms  (%d steps)\n", stage,
			float64(time.Since(stageStart).Microseconds())/1000, steps)
	}
	fmt.Fprintf(out, "%-28s %8.1f ms  (%d steps)\n", "TOTAL",
		float64(time.Since(t0).Microseconds())/1000, total)
	return 0
}
