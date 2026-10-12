package editor

// editor_polyhaven_jobs.go — the Poly Haven browser's work off the UI
// thread (docs/canyon/PHASE_4.md): the catalog, an asset's file listing, the
// thumbnails and the Download & Import. Everything that talks to the network
// or decodes runs on goroutines through the one internal/polyhaven client
// (its cache, its md5 checks, its four-at-a-time cap); the UI thread only
// reads results under phJobs.mu and does the GL half (thumbnail uploads,
// the texture rescan after an import) in phStepJobs, called from a Draw.
//
// Thumbnails are fetched only for cells that are on screen (phThumb asks;
// a queue of at most phThumbQueueMax waits for phThumbWorkers goroutines),
// decoded off-thread, uploaded once and kept for the session: the Generator
// window's id-list widget draws from the same cache.

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"sort"
	"strings"
	"sync"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/polyhaven"
	"foundry/internal/polyhaven/phimport"
)

// phClientFn is the client the browser uses (tests use the fake).
var phClientFn = func() *polyhaven.Client { return polyhaven.New() }

var phClientOnce struct {
	sync.Mutex
	c *polyhaven.Client
}

func phClient() *polyhaven.Client {
	phClientOnce.Lock()
	defer phClientOnce.Unlock()
	if phClientOnce.c == nil {
		phClientOnce.c = phClientFn()
	}
	return phClientOnce.c
}

const (
	phThumbWorkers  = 4
	phThumbQueueMax = 64
	phThumbPx       = 128 // decoded (and uploaded) at this size
)

type phThumbState int

const (
	phThumbQueued phThumbState = iota + 1
	phThumbReady
	phThumbFailed
)

type phThumbEntry struct {
	state phThumbState
	rgba  *image.RGBA // decoded, waiting for the upload
	tex   uint32
}

// phJobs is everything the goroutines hand back.
var phJobs struct {
	mu sync.Mutex

	catalog    map[string][]*polyhaven.Asset // type → assets
	catLoading map[string]bool
	catErr     map[string]error

	files      map[string]polyhaven.Files
	filesBusy  map[string]bool
	filesErr   map[string]error
	thumbs     map[string]*phThumbEntry
	thumbQueue chan string
	started    bool

	imp phImportJob
}

// phImportJob is the Download & Import in flight (one at a time).
type phImportJob struct {
	running bool
	id, typ string
	step    string
	frac    float32
	log     []string
	err     error
	done    bool // finished, result not yet acknowledged by the UI
	cancel  context.CancelFunc
	started time.Time
	result  string
	// rescan asks the UI thread to pick up the new textures.
	rescan bool
}

func phInitJobs() {
	if phJobs.started {
		return
	}
	phJobs.started = true
	phJobs.catalog = map[string][]*polyhaven.Asset{}
	phJobs.catLoading = map[string]bool{}
	phJobs.catErr = map[string]error{}
	phJobs.files = map[string]polyhaven.Files{}
	phJobs.filesBusy = map[string]bool{}
	phJobs.filesErr = map[string]error{}
	phJobs.thumbs = map[string]*phThumbEntry{}
	phJobs.thumbQueue = make(chan string, phThumbQueueMax)
	for i := 0; i < phThumbWorkers; i++ {
		go phThumbWorker()
	}
}

// phCatalog is the type's assets (nil while loading; it starts the load).
func phCatalog(typ string) ([]*polyhaven.Asset, bool, error) {
	phJobs.mu.Lock()
	defer phJobs.mu.Unlock()
	phInitJobs()
	if as, ok := phJobs.catalog[typ]; ok {
		return as, false, nil
	}
	if err := phJobs.catErr[typ]; err != nil {
		return nil, false, err
	}
	if !phJobs.catLoading[typ] {
		phJobs.catLoading[typ] = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			cat, err := phClient().Assets(ctx, typ, "")
			var list []*polyhaven.Asset
			for _, a := range cat {
				list = append(list, a)
			}
			sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
			phJobs.mu.Lock()
			defer phJobs.mu.Unlock()
			phJobs.catLoading[typ] = false
			if err != nil {
				phJobs.catErr[typ] = err
				return
			}
			phJobs.catalog[typ] = list
		}()
	}
	return nil, true, nil
}

// phReloadCatalog forgets the type's catalog (the next draw refetches,
// from the client's cache while it is fresh).
func phReloadCatalog(typ string) {
	phJobs.mu.Lock()
	defer phJobs.mu.Unlock()
	phInitJobs()
	delete(phJobs.catalog, typ)
	delete(phJobs.catErr, typ)
}

// phFiles is an asset's file listing (nil while loading).
func phFiles(id string) (polyhaven.Files, error) {
	phJobs.mu.Lock()
	defer phJobs.mu.Unlock()
	phInitJobs()
	if f, ok := phJobs.files[id]; ok {
		return f, nil
	}
	if err := phJobs.filesErr[id]; err != nil {
		return nil, err
	}
	if !phJobs.filesBusy[id] {
		phJobs.filesBusy[id] = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			f, err := phClient().Files(ctx, id)
			phJobs.mu.Lock()
			defer phJobs.mu.Unlock()
			phJobs.filesBusy[id] = false
			if err != nil {
				phJobs.filesErr[id] = err
				return
			}
			phJobs.files[id] = f
		}()
	}
	return nil, nil
}

// phThumb is id's thumbnail texture, 0 until it is ready (asking queues it).
func phThumb(id string) uint32 {
	if id == "" || polyhaven.ValidID(id) != nil {
		return 0
	}
	phJobs.mu.Lock()
	defer phJobs.mu.Unlock()
	phInitJobs()
	e := phJobs.thumbs[id]
	if e == nil {
		select {
		case phJobs.thumbQueue <- id:
			phJobs.thumbs[id] = &phThumbEntry{state: phThumbQueued}
		default: // the queue is full: ask again next frame
		}
		return 0
	}
	return e.tex
}

func phThumbWorker() {
	for id := range phJobs.thumbQueue {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		data, err := phClient().Thumbnail(ctx, id, polyhaven.ThumbnailWidth)
		cancel()
		var rgba *image.RGBA
		if err == nil {
			rgba, err = phDecodeThumb(data)
		}
		phJobs.mu.Lock()
		e := phJobs.thumbs[id]
		if e != nil {
			if err != nil {
				e.state = phThumbFailed
			} else {
				e.rgba = rgba
			}
		}
		phJobs.mu.Unlock()
	}
}

// phDecodeThumb decodes a PNG to RGBA at phThumbPx (nearest-scaled down).
func phDecodeThumb(data []byte) (*image.RGBA, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	src := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(src, src.Rect, img, b.Min, draw.Src)
	if b.Dx() <= phThumbPx && b.Dy() <= phThumbPx {
		return src, nil
	}
	out := image.NewRGBA(image.Rect(0, 0, phThumbPx, phThumbPx*b.Dy()/max(b.Dx(), 1)))
	for y := 0; y < out.Rect.Dy(); y++ {
		sy := y * b.Dy() / out.Rect.Dy()
		for x := 0; x < out.Rect.Dx(); x++ {
			sx := x * b.Dx() / out.Rect.Dx()
			copy(out.Pix[y*out.Stride+4*x:y*out.Stride+4*x+4], src.Pix[sy*src.Stride+4*sx:])
		}
	}
	return out, nil
}

// phStepJobs is the UI thread's half: thumbnails up, textures rescanned
// after an import. Called from the browser's and the Generator's Draw.
func phStepJobs() {
	phJobs.mu.Lock()
	phInitJobs()
	var up []*phThumbEntry
	for _, e := range phJobs.thumbs {
		if e.rgba != nil && e.tex == 0 {
			up = append(up, e)
		}
	}
	rescan := phJobs.imp.rescan
	phJobs.imp.rescan = false
	phJobs.mu.Unlock()
	for i, e := range up {
		if i >= 8 {
			break // a few a frame
		}
		tex := genUploadTexture(e.rgba.Rect.Dx(), e.rgba.Rect.Dy(), e.rgba.Pix)
		phJobs.mu.Lock()
		e.tex, e.rgba, e.state = tex, nil, phThumbReady
		phJobs.mu.Unlock()
	}
	if rescan && gfx.TextureNames != nil {
		// The import wrote new textures/ph_*.png: let every picker and
		// EnsureTextureLoaded see them (gfx.TextureNames is otherwise a
		// startup scan).
		if r := gfx.RescanTextures(); r.Changed() {
			editorSetStatus("Poly Haven: %s", r.Summary())
		}
	}
}

// phImportOptions are the browser's import settings.
type phImportOptions struct {
	Resolution    string
	Plant         bool
	Back          string
	MaxTriangles  int
	LiteTriangles int
	MaxSize       int
}

// phStartImport starts a Download & Import (false when one is running).
func phStartImport(a *polyhaven.Asset, opt phImportOptions) bool {
	phJobs.mu.Lock()
	defer phJobs.mu.Unlock()
	if phJobs.imp.running {
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	typ := polyhaven.TypeModels
	if a.IsTexture() {
		typ = polyhaven.TypeTextures
	}
	phJobs.imp = phImportJob{running: true, id: a.ID, typ: typ, step: "starting", cancel: cancel, started: time.Now()}
	res := fileutil.ResourcesPath
	go func() {
		defer func() {
			if r := recover(); r != nil {
				phImportFinish("", fmt.Errorf("import panicked: %v", r))
			}
		}()
		logf := func(f string, args ...any) {
			phJobs.mu.Lock()
			phJobs.imp.log = append(phJobs.imp.log, fmt.Sprintf(f, args...))
			phJobs.mu.Unlock()
		}
		stage := func(step string, done float32) {
			phJobs.mu.Lock()
			phJobs.imp.step, phJobs.imp.frac = step, done
			phJobs.mu.Unlock()
		}
		c := phClient()
		if typ == polyhaven.TypeTextures {
			ts, err := phimport.ImportTextures(ctx, c, a.ID, phimport.TextureOptions{Resources: res, Resolution: opt.Resolution,
				MaxSize: opt.MaxSize, Log: logf, Stage: stage})
			if err != nil {
				phImportFinish("", err)
				return
			}
			phImportFinish(fmt.Sprintf("%s: textures %s (+ normal, ARM, rough, AO, height) at %s", a.ID, ts.Albedo, ts.Resolution), nil)
			return
		}
		imp, err := phimport.ImportModel(ctx, c, a.ID, phimport.ModelOptions{Resources: res, Resolution: opt.Resolution,
			MaxSize: opt.MaxSize, Plant: opt.Plant, Back: opt.Back, MaxTriangles: opt.MaxTriangles,
			LiteTriangles: opt.LiteTriangles, Cook: true, Log: logf, Stage: stage})
		if err != nil {
			phImportFinish("", err)
			return
		}
		var models []string
		for _, v := range imp.Variants {
			models = append(models, v.Model)
		}
		phImportFinish(fmt.Sprintf("%s: %s", a.ID, strings.Join(models, ", ")), nil)
	}()
	return true
}

func phImportFinish(result string, err error) {
	phJobs.mu.Lock()
	defer phJobs.mu.Unlock()
	phJobs.imp.running, phJobs.imp.done = false, true
	phJobs.imp.result, phJobs.imp.err = result, err
	phJobs.imp.rescan = err == nil
	if phJobs.imp.cancel != nil {
		phJobs.imp.cancel()
	}
}

// phCancelImport cancels the running import.
func phCancelImport() {
	phJobs.mu.Lock()
	defer phJobs.mu.Unlock()
	if phJobs.imp.running && phJobs.imp.cancel != nil {
		phJobs.imp.cancel()
	}
}

// phImportSnapshot is a copy of the import's state for drawing.
func phImportSnapshot() phImportJob {
	phJobs.mu.Lock()
	defer phJobs.mu.Unlock()
	j := phJobs.imp
	j.log = append([]string(nil), phJobs.imp.log...)
	return j
}

// phImported reports whether id has been imported into this project
// (its provenance record exists).
func phImported(a *polyhaven.Asset) bool {
	dir := phimport.ModelDir(a.ID)
	if a.IsTexture() {
		dir = phimport.TextureSetsDir + "/" + a.ID
	}
	_, err := phimport.ReadProvenance(fileutil.ResolveAssetPath(dir + "/" + phimport.ProvenanceFile))
	return err == nil
}
