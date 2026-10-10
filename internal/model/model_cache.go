package model

// model_cache.go — a binary copy of each parsed .fmdl and .fskl, so a model
// is read as YAML once rather than on every launch.
//
// A rigged character is a few hundred kilobytes of YAML and its skeleton
// another megabyte, nearly all of it number lists ([][]float32 vertices,
// keyframes), and yaml.v2 decodes those at a few megabytes a second: a third
// of a second per character, and the same skeleton again for every model that
// links it. The parsed form (RawModel, RawSkeleton) is plain data, so it is
// cached in internal/binenc's encoding, which reads it back in a few
// milliseconds.
//
// # Never stale
//
// An entry is named by a hash of the file's exact BYTES together with a
// fingerprint of the Go type it decodes into (field names, types and yaml
// tags, all the way down) and the encoding's version. A model saved with one
// changed vertex is a different hash; an engine whose RawModel gained a field
// is a different fingerprint, and reads the YAML again rather than a cache
// entry that has no value for it. Nothing is invalidated, because nothing can
// match that should not. And it is decoded Exact — empty lists stay empty,
// absent pointers nil — so what comes back is what yaml.Unmarshal would have
// produced (TestTheModelCacheReadsWhatYAMLDid). A cache entry that fails to
// decode is ignored and the YAML read instead.
//
// # Where
//
// Not beside the model: the editor's resources/ is the author's project, under
// version control (the Git window), and must not fill with generated files.
// Entries are written to the user's cache directory (os.UserCacheDir, e.g.
// ~/.cache/foundry/models or %LocalAppData%\foundry\models) and read from
// there, after first looking in PrebuiltCacheDirs — where a release pack can
// ship entries made at pack time, so a player's first launch is as quick as
// their second. Tests write nowhere unless they point CacheDir somewhere.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"gopkg.in/yaml.v2"

	"foundry/internal/binenc"
)

// cacheFormat changes whenever the cache's own layout or binenc's meaning
// does; it is part of every entry's name.
const cacheFormat = 1

// CacheDir is where entries are written and read, or "" for nowhere. It
// defaults to the user's cache directory — and to nowhere under `go test`,
// so a test run does not fill a developer's cache. FOUNDRY_MODEL_CACHE=off
// switches it off; any other value is used as the directory.
var CacheDir = defaultCacheDir()

// PrebuiltCacheDirs are read-only directories searched before CacheDir: a
// release pack's shipped entries.
var PrebuiltCacheDirs []string

func defaultCacheDir() string {
	switch v := os.Getenv("FOUNDRY_MODEL_CACHE"); v {
	case "off", "0", "false":
		return ""
	case "":
	default:
		return v
	}
	if testing.Testing() {
		return ""
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "foundry", "models")
}

// decodeYAMLCached is yaml.Unmarshal(data, out) by way of the cache. out must
// be a pointer to a zero value.
func decodeYAMLCached(data []byte, out any) error {
	name := CacheEntryName(data, out)
	dirs := append(append([]string(nil), PrebuiltCacheDirs...), CacheDir)
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			// Decoded into a fresh value, so a half-decoded entry cannot
			// leave anything behind in out.
			fresh := reflect.New(reflect.TypeOf(out).Elem())
			if binenc.Unmarshal(b, fresh.Interface()) == nil {
				reflect.ValueOf(out).Elem().Set(fresh.Elem())
				return nil
			}
		}
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return err
	}
	if CacheDir != "" {
		// Best effort: a cache that cannot be written is a slower next
		// launch, not a failed load.
		_ = writeCacheEntry(CacheDir, name, out)
	}
	return nil
}

// CacheEntryName is the file a parse of data into out is cached under.
func CacheEntryName(data []byte, out any) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%s-%016x-%d.bin", hex.EncodeToString(sum[:16]), typeFingerprint(reflect.TypeOf(out).Elem()), cacheFormat)
}

// WriteCacheEntry parses the YAML document data as the type out points to
// and writes its entry into dir — for a pack step building PrebuiltCacheDirs.
func WriteCacheEntry(dir string, data []byte, out any) error {
	if err := yaml.Unmarshal(data, out); err != nil {
		return err
	}
	return writeCacheEntry(dir, CacheEntryName(data, out), out)
}

func writeCacheEntry(dir, name string, v any) error {
	b, err := binenc.MarshalOptions(v, binenc.Options{Exact: true})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// A unique temporary name and a rename: two loads of one model at once
	// (the parallel prefetch) both write, and either result is the same.
	tmp, err := os.CreateTemp(dir, name+".tmp*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("writing model cache: %v %v", werr, cerr)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

var fingerprints sync.Map // reflect.Type -> uint64

// typeFingerprint hashes everything about t a decode depends on: every
// field's name, type and tag, recursively.
func typeFingerprint(t reflect.Type) uint64 {
	if v, ok := fingerprints.Load(t); ok {
		return v.(uint64)
	}
	h := fnv.New64a()
	seen := map[reflect.Type]bool{}
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		fmt.Fprintf(h, "%s|%s|", t.Kind(), t.String())
		if seen[t] {
			return
		}
		seen[t] = true
		switch t.Kind() {
		case reflect.Ptr, reflect.Slice, reflect.Array:
			if t.Kind() == reflect.Array {
				fmt.Fprintf(h, "%d|", t.Len())
			}
			walk(t.Elem())
		case reflect.Map:
			walk(t.Key())
			walk(t.Elem())
		case reflect.Struct:
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				fmt.Fprintf(h, "{%s %s}", f.Name, f.Tag)
				walk(f.Type)
			}
		}
	}
	walk(t)
	v := h.Sum64()
	fingerprints.Store(t, v)
	return v
}
