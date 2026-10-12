// Package phcli is `foundry polyhaven`: the Poly Haven library from the
// command line — search the catalog, show an asset, import models and
// texture sets into the project — running exactly the importer the
// Generator's Poly Haven stages and the editor's browser run
// (internal/polyhaven/phimport). docs/canyon/PHASE_4.md.
//
//	foundry polyhaven search [-t models|textures] [-c category] [-tags a,b] [-sort downloads] [words...]
//	foundry polyhaven info <id>
//	foundry polyhaven import [-res 2k] [-max-size N] [-max-triangles N] [-lite N] [-back auto] [-plant]
//	                         [-collision auto] [-no-cook] [-force] [-offline] <id>...
//	foundry polyhaven import -textures [-res 2k] [-max-size N] <id>...
//	foundry polyhaven cache    where the cache is and how big
package phcli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"foundry/internal/cliflag"
	"foundry/internal/fileutil"
	"foundry/internal/polyhaven"
	"foundry/internal/polyhaven/phimport"
)

// Command is the CLI word.
const Command = "polyhaven"

// Main runs the command.
func Main(root string, args []string, out io.Writer) int {
	if len(args) == 0 {
		usage(out)
		return 2
	}
	switch args[0] {
	case "search":
		return searchMain(args[1:], out)
	case "info":
		return infoMain(args[1:], out)
	case "import":
		return importMain(root, args[1:], out)
	case "cache":
		return cacheMain(out)
	}
	usage(out)
	return 2
}

func usage(out io.Writer) {
	fmt.Fprintf(out, `foundry %[1]s search [-t models|textures] [-c category] [-tags a,b] [-sort name|downloads|date|polycount] [words...]
foundry %[1]s info <id>
foundry %[1]s import [-res 2k] [-max-size N] [-max-triangles N] [-lite N] [-back auto] [-plant] [-collision auto] [-no-cook] [-force] <id>...
foundry %[1]s import -textures [-res 2k] [-max-size N] <id>...
foundry %[1]s cache
%[2]s
`, Command, polyhaven.Credit)
}

func client(offline bool) *polyhaven.Client {
	c := polyhaven.New()
	c.Offline = c.Offline || offline
	return c
}

func interruptible() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

func searchMain(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("polyhaven search", flag.ContinueOnError)
	fs.SetOutput(out)
	typ := fs.String("t", polyhaven.TypeModels, "models or textures")
	cat := fs.String("c", "", "a category")
	tags := fs.String("tags", "", "tags, all required (comma separated)")
	sortBy := fs.String("sort", "name", "name, downloads, date or polycount")
	offline := fs.Bool("offline", false, "the cache only")
	rest, err := cliflag.Parse(fs, args)
	if err != nil {
		return 2
	}
	ctx, cancel := interruptible()
	defer cancel()
	cat0, err := client(*offline).Assets(ctx, *typ, *cat)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	q := polyhaven.Query{Text: strings.Join(rest, " ")}
	if *tags != "" {
		q.Tags = strings.Split(*tags, ",")
	}
	as := polyhaven.Filter(cat0, q)
	polyhaven.Sort(as, *sortBy)
	for _, a := range as {
		dims := a.SizeMetres()
		size := ""
		if len(dims) > 0 {
			size = fmt.Sprintf("%.1f m", dims[0])
		}
		fmt.Fprintf(out, "%-32s %-34s %9d tris %7s  %s\n", a.ID, a.Name, a.Polycount, size, strings.Join(a.Categories, ", "))
	}
	fmt.Fprintf(out, "%d assets. %s\n", len(as), polyhaven.Credit)
	return 0
}

func infoMain(args []string, out io.Writer) int {
	if len(args) != 1 {
		usage(out)
		return 2
	}
	ctx, cancel := interruptible()
	defer cancel()
	c := client(false)
	a, err := c.Info(ctx, args[0])
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	fmt.Fprintf(out, "%s — %s\n  %s\n  authors: %s\n  categories: %s\n  tags: %s\n  triangles: %d\n  size: %v m\n  licence: %s\n  page: https://polyhaven.com/a/%s\n",
		a.ID, a.Name, a.Description, strings.Join(a.AuthorList(), ", "), strings.Join(a.Categories, ", "),
		strings.Join(a.Tags, ", "), a.Polycount, a.SizeMetres(), polyhaven.License, a.ID)
	if files, err := c.Files(ctx, a.ID); err == nil {
		key := "gltf"
		if a.IsTexture() {
			key = "Diffuse"
		}
		fmt.Fprintf(out, "  resolutions: %s\n", strings.Join(files.Resolutions(key), " "))
	}
	return 0
}

func importMain(root string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet("polyhaven import", flag.ContinueOnError)
	fs.SetOutput(out)
	project := fs.String("root", root, "project directory: the one holding resources/")
	res := fs.String("res", "2k", "texture resolution: 1k, 2k, 4k, 8k")
	maxSize := fs.Int("max-size", 0, "scale textures down to at most this many pixels a side")
	maxTris := fs.Int("max-triangles", 0, "decimate to this budget before the cook")
	lite := fs.Int("lite", 0, "also make a light copy at this budget")
	back := fs.String("back", "auto", "auto, none, -z, +z, -x, +x or degrees")
	plant := fs.Bool("plant", false, "a plant: no back, no collision")
	collision := fs.String("collision", "auto", "auto, hulls, mesh or none")
	noCook := fs.Bool("no-cook", false, "write .glb instead of cooking")
	force := fs.Bool("force", false, "recook and rewrite textures")
	textures := fs.Bool("textures", false, "the IDs are texture sets")
	offline := fs.Bool("offline", false, "the cache only")
	rest, err := cliflag.Parse(fs, args)
	if err != nil || len(rest) == 0 {
		usage(out)
		return 2
	}
	resources := fileutil.ResourcesDir(*project)
	fileutil.ResourcesPath = resources
	ctx, cancel := interruptible()
	defer cancel()
	c := client(*offline)
	logf := func(f string, a ...any) { fmt.Fprintf(out, f+"\n", a...) }
	status := 0
	for _, id := range rest {
		t0 := time.Now()
		if *textures {
			ts, err := phimport.ImportTextures(ctx, c, id, phimport.TextureOptions{Resources: resources, Resolution: *res,
				MaxSize: *maxSize, Force: *force, Log: logf})
			if err != nil {
				fmt.Fprintf(out, "%s: %v\n", id, err)
				status = 1
				continue
			}
			fmt.Fprintf(out, "%s: %s at %s (%.1f m a repeat): %s %s %s %s %s %s — download %.1f s (%.1f MB), textures %.1f s\n",
				id, ts.Name, ts.Resolution, ts.Size, ts.Albedo, ts.Normal, ts.ARM, ts.Rough, ts.AO, ts.Height,
				ts.DownloadMS/1000, float64(ts.Downloaded)/(1<<20), ts.TextureMS/1000)
			continue
		}
		imp, err := phimport.ImportModel(ctx, c, id, phimport.ModelOptions{Resources: resources, Resolution: *res, MaxSize: *maxSize,
			MaxTriangles: *maxTris, LiteTriangles: *lite, Back: *back, Plant: *plant, Collision: *collision,
			Cook: !*noCook, Force: *force, Log: logf})
		if err != nil {
			fmt.Fprintf(out, "%s: %v\n", id, err)
			status = 1
			continue
		}
		fmt.Fprintf(out, "%s: %s, %d triangles scanned, back %v (confidence %.2f, yaw %.0f°), cutout %v; download %.1f s (%.1f MB), textures %.1f s, cook %.1f s, total %.1f s\n",
			id, imp.Asset.Name, imp.SourceTriangles, imp.Frame.HasBack, imp.Frame.Confidence, imp.Frame.Yaw*180/3.14159265,
			imp.Cutout, imp.DownloadMS/1000, float64(imp.Downloaded)/(1<<20), imp.TextureMS/1000, imp.CookMS/1000, time.Since(t0).Seconds())
		for _, v := range imp.Variants {
			fmt.Fprintf(out, "  %s: %d triangles, %.2f×%.2f×%.2f m, %s, %s\n", v.Model, v.Meta.Triangles,
				v.Meta.Extent[0], v.Meta.Extent[1], v.Meta.Extent[2], v.Meta.PlaceCollision, map[bool]string{true: "back -Z", false: "no back"}[v.Meta.Back != nil])
		}
	}
	return status
}

func cacheMain(out io.Writer) int {
	dir := polyhaven.DefaultCacheDir()
	var n int
	var size int64
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if st, err := d.Info(); err == nil {
				n++
				size += st.Size()
			}
		}
		return nil
	})
	fmt.Fprintf(out, "%s: %d files, %.1f MB\n", dir, n, float64(size)/(1<<20))
	return 0
}
