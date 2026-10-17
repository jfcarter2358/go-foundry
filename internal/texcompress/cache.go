package texcompress

// cache.go — encoded mip chains on disk, so a texture is encoded once.
//
// Encoding a 2048² texture's chain takes the better part of a second of every
// core; reading 5.6 MB of finished blocks back takes milliseconds. So every
// encode is written to a per-user cache and every load looks there first.
//
// Where: os.UserCacheDir()/foundry/texcache (~/.cache/foundry/texcache on
// Linux, %LocalAppData%\foundry\texcache on Windows), or FOUNDRY_TEXCACHE_DIR
// when set — what the tests use, so they never touch the real cache. It is a
// cache in the strict sense: deleting it costs one re-encode per texture and
// nothing else, and nothing here ever cleans it (a known gap: an edited
// texture leaves its old entry behind).
//
// The key is a SHA-256 over the source FILE's bytes (not the decoded pixels,
// which would cost a second pass over 16-64 MB), the format and
// EncoderVersion. Editing the PNG, changing the format or improving the
// encoder each change the key, so a stale entry is never served; it is simply
// never asked for again. The "skip top mips" setting is NOT in the key: the
// whole chain is cached and the upload drops levels from the front, so
// changing that setting costs no encode.
//
// File layout (<key>.ftxc), little-endian:
//
//	"FTXC"  u32 cacheFileVersion  u32 EncoderVersion  u32 format  u32 levels
//	per level:  u32 w  u32 h  u32 len  len bytes of blocks
//
// Writes go to a temp file in the same directory and are renamed into place,
// so a reader (another editor instance, a crash mid-write) sees a whole file
// or none. A file that fails to parse is treated as a miss and overwritten.

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
)

// cacheFileVersion is the container's own version, separate from the
// encoder's: bump it if the layout above changes.
const cacheFileVersion = 1

var cacheMagic = [4]byte{'F', 'T', 'X', 'C'}

// CacheDirEnv overrides the cache's location.
const CacheDirEnv = "FOUNDRY_TEXCACHE_DIR"

// CacheDir is where encoded chains are kept. Empty when no cache directory
// can be found at all, which turns caching off rather than failing loads.
func CacheDir() string {
	if d := os.Getenv(CacheDirEnv); d != "" {
		return d
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "foundry", "texcache")
}

// CacheKey is the cache key for a source file's bytes encoded in format.
func CacheKey(src []byte, format Format) string {
	h := sha256.New()
	h.Write(src)
	var tail [8]byte
	binary.LittleEndian.PutUint32(tail[0:], uint32(format))
	binary.LittleEndian.PutUint32(tail[4:], EncoderVersion)
	h.Write(tail[:])
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// CachePath is the file a key is stored in under dir.
func CachePath(dir, key string) string { return filepath.Join(dir, key+".ftxc") }

// LoadCached reads the chain stored under key in dir.
func LoadCached(dir, key string, format Format) (*Texture, error) {
	data, err := os.ReadFile(CachePath(dir, key))
	if err != nil {
		return nil, err
	}
	return parseCache(data, format)
}

func parseCache(data []byte, format Format) (*Texture, error) {
	bad := func(why string) error { return fmt.Errorf("texcompress: cache file: %s", why) }
	if len(data) < 20 || [4]byte(data[0:4]) != cacheMagic {
		return nil, bad("not a texture cache file")
	}
	u32 := func(o int) uint32 { return binary.LittleEndian.Uint32(data[o:]) }
	if u32(4) != cacheFileVersion || u32(8) != EncoderVersion {
		return nil, bad("written by another version")
	}
	if Format(u32(12)) != format {
		return nil, bad("holds another format")
	}
	n := int(u32(16))
	if n <= 0 || n > 32 {
		return nil, bad("implausible level count")
	}
	t := &Texture{Format: format}
	o := 20
	for i := 0; i < n; i++ {
		if o+12 > len(data) {
			return nil, bad("truncated")
		}
		w, h, l := int(u32(o)), int(u32(o+4)), int(u32(o+8))
		o += 12
		if w <= 0 || h <= 0 || l != LevelBytes(w, h) || o+l > len(data) {
			return nil, bad("level size mismatch")
		}
		if i > 0 {
			pw, ph := t.Levels[i-1].W, t.Levels[i-1].H
			if w != max(1, pw/2) || h != max(1, ph/2) {
				return nil, bad("levels do not halve")
			}
		}
		t.Levels = append(t.Levels, Level{W: w, H: h, Data: data[o : o+l : o+l]})
		o += l
	}
	if last := t.Levels[n-1]; last.W != 1 || last.H != 1 {
		return nil, bad("chain does not reach 1x1")
	}
	if o != len(data) {
		return nil, bad("trailing bytes")
	}
	return t, nil
}

// StoreCached writes t under key in dir, atomically.
func StoreCached(dir, key string, t *Texture) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, key+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	werr := writeCache(f, t)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, CachePath(dir, key)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func writeCache(f *os.File, t *Texture) error {
	w := bufio.NewWriterSize(f, 1<<20)
	var hdr [20]byte
	copy(hdr[0:4], cacheMagic[:])
	binary.LittleEndian.PutUint32(hdr[4:], cacheFileVersion)
	binary.LittleEndian.PutUint32(hdr[8:], EncoderVersion)
	binary.LittleEndian.PutUint32(hdr[12:], uint32(t.Format))
	binary.LittleEndian.PutUint32(hdr[16:], uint32(len(t.Levels)))
	w.Write(hdr[:])
	for _, l := range t.Levels {
		var lh [12]byte
		binary.LittleEndian.PutUint32(lh[0:], uint32(l.W))
		binary.LittleEndian.PutUint32(lh[4:], uint32(l.H))
		binary.LittleEndian.PutUint32(lh[8:], uint32(len(l.Data)))
		w.Write(lh[:])
		w.Write(l.Data)
	}
	return w.Flush()
}

// LoadOrEncode is the cached encode: src is the source file's bytes (the key),
// img their decoded pixels (what is encoded on a miss). hit reports whether
// the chain came off disk. A cache that cannot be read or written costs the
// encode, never the texture: the error is returned only when encoding itself
// fails, and a failed store is reported through storeErr for the caller to
// log.
func LoadOrEncode(src []byte, img *image.RGBA, format Format, workers int) (t *Texture, hit bool, storeErr, err error) {
	dir := CacheDir()
	key := CacheKey(src, format)
	if dir != "" {
		if t, err := LoadCached(dir, key, format); err == nil &&
			t.Levels[0].W == img.Rect.Dx() && t.Levels[0].H == img.Rect.Dy() {
			return t, true, nil, nil
		}
	}
	t, err = Encode(img, format, workers)
	if err != nil {
		return nil, false, nil, err
	}
	if dir != "" {
		storeErr = StoreCached(dir, key, t)
	}
	return t, false, storeErr, nil
}
