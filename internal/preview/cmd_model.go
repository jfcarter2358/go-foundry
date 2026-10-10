package preview

// cmd_model.go — `foundry model`, measuring a .fmdl from the command line.
//
//	foundry model inspect models/marine.fmdl
//	foundry model inspect models/marine.fmdl -pose walk:0.25 -bone foot.L,foot.R
//
// This is the half of the old Python model tooling that was not a picture:
// "where is the ankle in this clip", "how far apart are the feet", "what does
// this rig even have in it". Doing it here rather than in a script outside the
// engine is the whole point — the numbers come from the engine's own pose maths,
// so a measurement is the pose the engine draws rather than a second
// implementation's opinion of it.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"foundry/internal/cliflag"
	"foundry/internal/fileutil"
	"foundry/internal/model"

	"github.com/go-gl/mathgl/mgl32"
)

// ModelCommand is the verb: `foundry model`.
const ModelCommand = "model"

// ModelMain answers a question about a .fmdl.
func ModelMain(root string, args []string, out io.Writer) int {
	if len(args) == 0 || args[0] != "inspect" {
		fmt.Fprintf(out, "foundry %s inspect <path.fmdl> [flags] — measure a model\n", ModelCommand)
		return 2
	}
	fs := flag.NewFlagSet("model inspect", flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	pose := fs.String("pose", "", "`clip:seconds` — measure this pose instead of the rest one")
	bones := fs.String("bone", "", "comma-separated bone names to report; all of them by default")
	verts := fs.Bool("verts", false, "also report the vertex bounds")
	rest, err := cliflag.Parse(fs, args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if len(rest) != 1 {
		fmt.Fprintln(out, "usage: foundry model inspect <path.fmdl> [flags]")
		return 2
	}
	fileutil.ResourcesPath = fileutil.ResourcesDir(*project)

	m, err := model.LoadModel(rest[0])
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	fmt.Fprintf(out, "%s: %d verts, %d faces, %d bones, %d clips\n",
		m.ID, len(m.Verts), len(m.Faces), len(m.Bones), len(m.Animations))
	if m.Texture != "" {
		fmt.Fprintf(out, "texture: %s\n", m.Texture)
	}
	if m.Skeleton != "" {
		// Named before the clips, because it is where they came from: a clip
		// that looks wrong here is a clip to fix in the .fskl, and every other
		// model on that rig will change with it.
		fmt.Fprintf(out, "skeleton: %s (shared with: %s)\n", m.Skeleton,
			describeSkeletonSharers(m.Skeleton, rest[0]))
	}
	if len(m.Animations) > 0 {
		names := m.ClipNames()
		sort.Strings(names)
		fmt.Fprintf(out, "clips: %s\n", strings.Join(names, ", "))
	}

	// The posed armature, through PoseDeltas and PosedBones — the same pair the
	// renderer skins with.
	posed, err := posedBones(m, *pose)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	want := map[string]bool{}
	for _, n := range strings.Split(*bones, ",") {
		if n = strings.TrimSpace(n); n != "" {
			want[n] = true
		}
	}
	if len(posed) > 0 {
		label := "rest pose"
		if *pose != "" {
			label = *pose
		}
		fmt.Fprintf(out, "\nbones (%s) — head and tail, in model space:\n", label)
		for i := range posed {
			b := &posed[i]
			if len(want) > 0 && !want[b.Name] {
				continue
			}
			fmt.Fprintf(out, "  %-18s head %s  tail %s\n", b.Name, vecStr(b.Head), vecStr(b.Tail))
		}
	}

	if *verts {
		vs, err := posedVerts(m, *pose)
		if err != nil {
			fmt.Fprintln(out, err)
			return 1
		}
		lo, hi := bounds(vs)
		fmt.Fprintf(out, "\nbounds: %s .. %s  (size %s)\n", vecStr(lo), vecStr(hi), vecStr(hi.Sub(lo)))
	}
	return 0
}

// posedBones answers the armature as the pose leaves it: the file's own bones
// for the rest pose, or PosedBones for a clip at a time.
func posedBones(m *model.ModelDoc, pose string) ([]model.ModelBone, error) {
	if len(m.Bones) == 0 {
		return nil, nil
	}
	if pose == "" {
		return m.Bones, nil
	}
	p, err := samplePose(m, pose)
	if err != nil {
		return nil, err
	}
	return m.PosedBones(m.PoseDeltas(p)), nil
}

func bounds(vs []mgl32.Vec3) (lo, hi mgl32.Vec3) {
	if len(vs) == 0 {
		return
	}
	lo, hi = vs[0], vs[0]
	for _, v := range vs[1:] {
		for i := 0; i < 3; i++ {
			lo[i], hi[i] = minf(lo[i], v[i]), maxf(hi[i], v[i])
		}
	}
	return lo, hi
}

// vecStr prints a point at millimetre precision, which is the precision these
// measurements are argued at: a gait 7 cm too wide, a socket 2 cm off.
func vecStr(v mgl32.Vec3) string {
	return fmt.Sprintf("<%7.3f, %7.3f, %7.3f>", v.X(), v.Y(), v.Z())
}

// describeSkeletonSharers lists the other models on the rig, for the line
// above. "nothing else" rather than an empty list: the question being answered
// is "what else does editing this break", and silence would read as unknown.
func describeSkeletonSharers(name, selfPath string) string {
	others := model.ModelsUsingSkeleton(name, selfPath)
	if len(others) == 0 {
		return "nothing else"
	}
	return strings.Join(others, ", ")
}
