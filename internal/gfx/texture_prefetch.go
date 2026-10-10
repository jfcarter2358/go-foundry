package gfx

// texture_prefetch.go — decoding a level's textures on every core while the
// main thread gets on with the rest of the load.
//
// A texture load is a PNG decode and a GL upload. The upload has to happen on
// the main thread; the decode does not, and it is nearly all of the cost —
// level1's textures and their normal maps are seconds of inflate done one
// after another. PrefetchTextures starts the decodes on worker goroutines, and
// LoadTexture takes the result when the load reaches that texture, waiting if
// the worker has not finished yet. A texture nobody prefetched decodes inline,
// exactly as before.

import (
	"image"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"foundry/internal/fileutil"
)

// prefetchedTexture is one decode in flight or finished. done closes when rgba
// and err are set.
type prefetchedTexture struct {
	done chan struct{}
	rgba *image.RGBA
	err  error
}

var prefetch = struct {
	sync.Mutex
	files map[string]*prefetchedTexture
}{files: map[string]*prefetchedTexture{}}

// decodeSlots bounds how many decodes run at once to the core count: each holds
// a whole image, and more of them than cores only costs memory.
var decodeSlots = make(chan struct{}, runtime.NumCPU())

// PrefetchTextures starts decoding the named textures, and their normal maps
// when they have them, in the background. Names already resident, already in
// flight or not known to TextureNames are skipped — the latter fail loudly at
// LoadTexture, where they always have.
//
// Main thread only: it reads Textures and TextureNames.
func PrefetchTextures(names []string) {
	var files []string
	for _, name := range names {
		if name == "" {
			continue
		}
		if _, ok := Textures[name]; !ok {
			if path, ok := TextureNames[name]; ok {
				files = append(files, filepath.Join(fileutil.ResourcesPath, path))
			}
		}
		if !TextureUploadsEnabled || IsNormalMapName(name) || HasNormalMap(name) {
			continue
		}
		// The same existence test ensureNormalMapLoaded makes, made here so the
		// worker is not asked to decode a file that is not there.
		if nm := normalMapPath(name); fileExists(nm) {
			files = append(files, nm)
		}
	}

	prefetch.Lock()
	defer prefetch.Unlock()
	for _, file := range files {
		if _, busy := prefetch.files[file]; busy {
			continue
		}
		p := &prefetchedTexture{done: make(chan struct{})}
		prefetch.files[file] = p
		go func(file string) {
			decodeSlots <- struct{}{}
			p.rgba, p.err = decodeTextureFile(file)
			<-decodeSlots
			close(p.done)
		}(file)
	}
}

// takePrefetched hands over file's prefetched decode, waiting for it to finish,
// and forgets it. ok is false when nothing prefetched that file.
func takePrefetched(file string) (rgba *image.RGBA, err error, ok bool) {
	prefetch.Lock()
	p, ok := prefetch.files[file]
	delete(prefetch.files, file)
	prefetch.Unlock()
	if !ok {
		return nil, nil, false
	}
	<-p.done
	return p.rgba, p.err, true
}

// DropPrefetchedTextures forgets every decode nobody took — a load that failed
// part way, or a texture that turned out already resident — so the images are
// not held for the life of the process. Decodes still running finish into
// entries nothing references.
func DropPrefetchedTextures() {
	prefetch.Lock()
	prefetch.files = map[string]*prefetchedTexture{}
	prefetch.Unlock()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
