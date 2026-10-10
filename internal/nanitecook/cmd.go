package nanitecook

// cmd.go — `foundry nanite`, cooking from the command line.
//
//	foundry nanite cook models/rock.glb                 cook beside the source (models/rock.fnm)
//	foundry nanite cook models/rock.glb -o models/x.fnm
//	foundry nanite cook -all                            every .gltf/.glb under resources/ that is stale
//	foundry nanite cook -force models/rock.glb          even if the cooked file is up to date
//	foundry nanite inspect models/rock.fnm              what a cooked mesh holds
//	foundry nanite sample -level 6 models/rock.glb      write the procedural test rock as a .glb
//
// The editor's Assets panel runs the same cook (editor_nanite.go); both go
// through CookFile, so a mesh cooked either way is the same file.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"foundry/internal/cliflag"
	"foundry/internal/fileutil"
	"foundry/internal/model"
	"foundry/internal/nanite"
)

// Command is the verb: `foundry nanite`.
const Command = "nanite"

// Main runs `foundry nanite`. root is the project directory.
func Main(root string, args []string, out io.Writer) int {
	if len(args) == 0 {
		usage(out)
		return 2
	}
	switch args[0] {
	case "cook":
		return cookMain(root, args[1:], out)
	case "inspect":
		return inspectMain(root, args[1:], out)
	case "sample":
		return sampleMain(root, args[1:], out)
	}
	usage(out)
	return 2
}

func usage(out io.Writer) {
	fmt.Fprintf(out, `foundry %[1]s cook <source.gltf|.glb|.obj|.fmdl>... [-o out.fnm] [-force]
foundry %[1]s cook -all [-force]      cook every glTF under resources/ that is stale
foundry %[1]s inspect <mesh.fnm>
foundry %[1]s sample [-level N] [-seed S] <out.glb>
`, Command)
}

// resolve finds a path given on the command line: as given, or under
// resources/.
func resolve(p string) string {
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return fileutil.ResolveAssetPath(p)
}

func cookMain(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("nanite cook", flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	dest := fs.String("o", "", "the cooked file (one source only); beside the source by default")
	force := fs.Bool("force", false, "cook even when the cooked file is up to date")
	all := fs.Bool("all", false, "cook every .gltf and .glb under resources/")
	rest, err := cliflag.Parse(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	fileutil.ResourcesPath = fileutil.ResourcesDir(*project)
	var sources []string
	if *all {
		sources = FindSources(fileutil.ResourcesPath)
		if len(sources) == 0 {
			fmt.Fprintf(out, "no glTF sources under %s\n", fileutil.ResourcesPath)
			return 0
		}
	}
	for _, r := range rest {
		sources = append(sources, resolve(r))
	}
	if len(sources) == 0 || (*dest != "" && len(sources) != 1) {
		usage(out)
		return 2
	}
	if failed := cookAll(sources, *dest, *force, ProjectOptions(), out); failed > 0 {
		return 1
	}
	return 0
}

// cookAll cooks each of sources (CookFile), writing a line for each to out
// in the order given, and returns how many failed.
//
// The files are cooked side by side, a file a worker (at most GOMAXPROCS of
// them, so at most that many meshes in memory at once), biggest file first.
// Each cooks with its slots and DAGs serial (Options.serial): the cook's
// longest steps, the collision hulls and the distance grid, fan out over the
// cores on their own whatever the cook does, and a big file kept on a lane
// of its own (as BuildJobs would) only made it the one still cooking long
// after the rest. A lone file cooks fully fanned out, as before. Each
// cook's line is held until every line before it is out, so the log reads
// exactly as one at a time. Two sources that cook to the same file (models/x.fmdl
// and models/x.obj both make models/x.fnm) are one job, cooked in the order
// given, so the file ends up the later one's as it always did.
func cookAll(sources []string, dest string, force bool, opt Options, out io.Writer) int {
	// The jobs: sources by the file they cook to, in first-named order.
	var jobs [][]int
	jobOf := map[string]int{}
	for i, src := range sources {
		o := dest
		if o == "" {
			o = CookedPath(src)
		}
		if abs, err := filepath.Abs(o); err == nil {
			o = abs
		}
		j, ok := jobOf[o]
		if !ok {
			j = len(jobs)
			jobOf[o] = j
			jobs = append(jobs, nil)
		}
		jobs[j] = append(jobs[j], i)
	}
	sizes := make([]int, len(jobs))
	for j, srcs := range jobs {
		for _, i := range srcs {
			if st, err := os.Stat(sources[i]); err == nil {
				sizes[j] += int(st.Size())
			}
		}
	}

	lines := make([]string, len(sources))
	bad := make([]bool, len(sources))
	// done[i] closes when line i is written; the writer below prints them
	// in order as they come in, so a long run shows its progress.
	done := make([]chan struct{}, len(sources))
	for i := range done {
		done[i] = make(chan struct{})
	}
	printed := make(chan struct{})
	go func() {
		defer close(printed)
		for i := range sources {
			<-done[i]
			io.WriteString(out, lines[i])
		}
	}()
	cook := func(j int, inner bool) {
		o := opt
		o.serial = !inner
		for _, i := range jobs[j] {
			lines[i], bad[i] = cookLine(sources[i], dest, force, o)
			close(done[i])
		}
	}
	if !parallelCook || len(jobs) == 1 {
		for j := range jobs {
			cook(j, true)
		}
	} else {
		// Biggest first, so the longest cook is not the one left running
		// alone at the end; ties in the order given.
		order := make([]int, len(jobs))
		for j := range order {
			order[j] = j
		}
		sort.SliceStable(order, func(a, b int) bool { return sizes[order[a]] > sizes[order[b]] })
		var next atomic.Int64
		var wg sync.WaitGroup
		for w := 0; w < min(runtime.GOMAXPROCS(0), len(jobs)); w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					k := int(next.Add(1) - 1)
					if k >= len(order) {
						return
					}
					cook(order[k], false)
				}
			}()
		}
		wg.Wait()
	}
	<-printed
	failed := 0
	for _, b := range bad {
		if b {
			failed++
		}
	}
	return failed
}

// cookLine cooks one source and says how it went, as `nanite cook` prints
// it; failed is whether it did not cook.
func cookLine(src, dest string, force bool, opt Options) (line string, failed bool) {
	start := time.Now()
	res, err := CookFile(src, dest, force, opt)
	if err != nil {
		return fmt.Sprintf("%s: %v\n", src, err), true
	}
	if res.Skipped {
		return fmt.Sprintf("%s: up to date\n", res.Out), false
	}
	return fmt.Sprintf("%s: %s (%.2fs)\n", res.Out, Describe(res.Mesh), time.Since(start).Seconds()), false
}

// FindSources is every glTF file under dir, sorted — what `cook -all` and
// the editor's Cook All cook.
func FindSources(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && model.IsGLTFPath(p) {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// Describe is a one-line summary of a cooked mesh.
func Describe(m *nanite.Mesh) string {
	var slots []string
	for _, s := range m.Slots {
		t := s.Texture
		if t == "" {
			t = "untextured"
		}
		slots = append(slots, fmt.Sprintf("%s=%s", s.Name, t))
	}
	sdf := "no SDF"
	if m.SDF != nil {
		sdf = fmt.Sprintf("SDF %d³", m.SDF.N)
	}
	size := m.Hi.Sub(m.Lo)
	coll := "no collision"
	if c := m.Collision; c != nil {
		coll = fmt.Sprintf("collision %d triangles / %d hulls", c.TriangleCount(), len(c.Hulls))
	}
	return fmt.Sprintf("%d triangles, %d vertices, %d clusters over %d LOD levels in %d pages, fallback %d triangles, %s, %s, size %.2f x %.2f x %.2f, slots [%s]",
		m.TriangleCount(), m.VertexCount(), len(m.Clusters), m.Levels, len(m.Pages), len(m.Fallback)/3, sdf, coll, size[0], size[1], size[2], strings.Join(slots, " "))
}

func inspectMain(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("nanite inspect", flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	rest, err := cliflag.Parse(fs, args)
	if err != nil || len(rest) != 1 {
		usage(out)
		return 2
	}
	fileutil.ResourcesPath = fileutil.ResourcesDir(*project)
	path := resolve(rest[0])
	m, err := nanite.Read(path)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	fmt.Fprintf(out, "%s: %s\n", path, Describe(m))
	fmt.Fprintf(out, "source: %s (hash %x)\n", m.Source, m.SourceHash[:8])
	cones := 0
	minT, maxT := 1<<30, 0
	for _, c := range m.Clusters {
		if c.ConeCutoff < 1 {
			cones++
		}
		minT, maxT = min(minT, int(c.TriCount)), max(maxT, int(c.TriCount))
	}
	if len(m.Clusters) > 0 {
		fmt.Fprintf(out, "clusters: %d to %d triangles, mean %.1f; %d with a normal cone\n",
			minT, maxT, float64(m.StoredTriangles())/float64(len(m.Clusters)), cones)
	}
	// The DAG, a level a line: what each level holds and how far it strays.
	for l := int32(0); l < m.Levels; l++ {
		n, tris := 0, 0
		var maxErr float32
		for i := range m.Clusters {
			if c := &m.Clusters[i]; c.Level == l {
				n++
				tris += int(c.TriCount)
				maxErr = max(maxErr, c.Error)
			}
		}
		fmt.Fprintf(out, "level %d: %d clusters, %d triangles, error up to %.4g\n", l, n, tris, maxErr)
	}
	fmt.Fprintf(out, "LOD tree: %d nodes, %d leaves; fallback cut at error %.4g: %d triangles\n",
		len(m.Nodes), m.Leaves, m.FallbackError, len(m.Fallback)/3)
	// The pages (Phase 3): how the geometry streams.
	if len(m.Pages) > 0 {
		var rootBytes uint64
		maxV, maxT := int32(0), int32(0)
		for _, p := range m.Pages {
			if p.Root {
				rootBytes += uint64(p.Size)
			}
			maxV, maxT = max(maxV, p.Vertices), max(maxT, p.Triangles)
		}
		raw := uint64(m.StoredTriangles())*12 + uint64(m.VertexCount())*32
		fmt.Fprintf(out, "pages: %d (%d root, always resident: %.1f KB packed), %.1f KB packed of %.1f KB decoded (%.0f%%); fullest %d vertices / %d triangles of %d / %d\n",
			len(m.Pages), m.RootPages(), float64(rootBytes)/1024, float64(m.PageBytes())/1024, float64(raw)/1024,
			100*float64(m.PageBytes())/float64(max(raw, 1)), maxV, maxT, nanite.PageMaxVertices, nanite.PageMaxTriangles)
		fmt.Fprintf(out, "quantisation: position step %.3g (%.3g mm), UV step %.3g, normals %d bits a component\n",
			m.Quant.Step, m.Quant.Step*1000, m.Quant.UVStep, m.Quant.NormalBits)
	}
	if c := m.Collision; c != nil {
		cs := c.Settings
		nodes, held := len(c.Nodes), "held"
		if c.Paged {
			// Paged out of the metadata (Phase 3): read when a body comes near.
			nodes, held = c.MeshNodes, "held with the mesh (the cut is read when a body comes near)"
		}
		fmt.Fprintf(out, "collision: cut at error %.3g: %d of %d triangles kept (%d degenerate dropped, %d corners welded), %d vertices, %d tree nodes, %d open edges, %d non-manifold; %.1f KB %s\n",
			cs.Error, c.TriangleCount(), c.SourceTriangles, c.Degenerate, c.Welded, c.VertexCount(), nodes, c.OpenEdges, c.NonManifold, float64(c.Bytes())/1024, held)
		verts := 0
		for i := range c.Hulls {
			verts += len(c.Hulls[i].Pts)
		}
		fmt.Fprintf(out, "hulls: %d (cap %d%s), %d vertices (cap %d a hull, %d capped), worst concavity %.3g; mode %s (auto: hulls up to %.3g across)\n",
			len(c.Hulls), cs.MaxHulls, map[bool]string{true: ", HIT", false: ""}[c.HullCapHit], verts, cs.MaxHullVerts, c.VertCapped, c.Concavity, cs.Mode, cs.AutoSize)
	}
	for si, s := range m.Slots {
		fmt.Fprintf(out, "slot %d %q: texture %q, clusters %d..%d (%d at full detail), double-sided %v\n",
			si, s.Name, s.Texture, s.First, s.First+s.Count, s.BaseCount, s.DoubleSided)
	}
	if fresh, err := UpToDate(fileutil.ResolveAssetPath(m.Source), path); err == nil {
		fmt.Fprintf(out, "up to date with its source: %v\n", fresh)
	}
	return 0
}

func sampleMain(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("nanite sample", flag.ContinueOnError)
	fs.SetOutput(out)
	level := fs.Int("level", 6, "subdivision level: 20*4^level triangles")
	seed := fs.Float64("seed", 0.3, "the rock's shape: each seed is a different rock")
	rest, err := cliflag.Parse(fs, args)
	if err != nil || len(rest) != 1 || *level < 0 || *level > 9 {
		usage(out)
		return 2
	}
	glb, err := model.EncodeGLB(SampleRock(*level, float32(*seed)))
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	if err := os.WriteFile(rest[0], glb, 0o644); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	fmt.Fprintf(out, "%s: %d triangles\n", rest[0], 20<<(2*uint(*level)))
	_ = root
	return 0
}
