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
	"sort"
	"strings"
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
foundry %[1]s sample [-level N] <out.glb>
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
	failed := 0
	for _, src := range sources {
		start := time.Now()
		res, err := CookFile(src, *dest, *force, Options{})
		if err != nil {
			fmt.Fprintf(out, "%s: %v\n", src, err)
			failed++
			continue
		}
		if res.Skipped {
			fmt.Fprintf(out, "%s: up to date\n", res.Out)
			continue
		}
		fmt.Fprintf(out, "%s: %s (%.2fs)\n", res.Out, Describe(res.Mesh), time.Since(start).Seconds())
	}
	if failed > 0 {
		return 1
	}
	return 0
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
	return fmt.Sprintf("%d triangles, %d vertices, %d clusters over %d LOD levels, fallback %d triangles, %s, size %.2f x %.2f x %.2f, slots [%s]",
		m.TriangleCount(), m.VertexCount(), len(m.Clusters), m.Levels, len(m.Fallback)/3, sdf, size[0], size[1], size[2], strings.Join(slots, " "))
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
	rest, err := cliflag.Parse(fs, args)
	if err != nil || len(rest) != 1 || *level < 0 || *level > 9 {
		usage(out)
		return 2
	}
	glb, err := model.EncodeGLB(SampleRock(*level, 0.3))
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
