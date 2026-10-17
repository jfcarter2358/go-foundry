//go:build cgo

package audioenc

// dl.go — OGG Vorbis and MP3 through the reference encoders, loaded at run
// time.
//
// libvorbisenc (with libvorbis and libogg, BSD-licensed) and LAME's
// libmp3lame (LGPL) are opened with dlopen on Linux and macOS and
// LoadLibrary on Windows the first time an export asks for them, and
// looked for beside the editor first — in $FOUNDRY_CODECS, then the
// codecs/ folder next to the executable, then next to the executable
// itself — and then wherever the system keeps its libraries. Nothing
// links against them: a build without them runs, and the release game
// (which never imports this package; internal/layering_test.go holds it
// to that) cannot contain them. Using LAME as a separately loaded library
// a user can replace is what the LGPL asks of a program that does not
// publish its own source under it.
//
// The encode loops are C, calling through the function pointers looked
// up here: each takes interleaved float samples and returns the whole
// file in a buffer this package frees, so no C keeps a pointer into Go
// memory and no Go callback runs on a C stack. The library's structs the
// loops only pass around (vorbis_info, vorbis_dsp_state, ogg_stream_state
// …) are opaque blocks, allocated far larger than any version of them; the
// two whose fields are read (ogg_page, ogg_packet) are declared as libogg
// declares them, `long` and all, so they match on every platform.

/*
#cgo linux LDFLAGS: -ldl
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifdef _WIN32
#define WIN32_LEAN_AND_MEAN 1
#include <windows.h>
static void* fdlOpen(const char* path, int full) {
	if (full) {
		// The library's own dependencies are looked for beside it.
		return (void*)LoadLibraryExA(path, NULL, LOAD_WITH_ALTERED_SEARCH_PATH);
	}
	return (void*)LoadLibraryA(path);
}
static void* fdlSym(void* h, const char* name) { return (void*)GetProcAddress((HMODULE)h, name); }
static void fdlClose(void* h) { FreeLibrary((HMODULE)h); }
static const char* fdlError(void) {
	static char buf[64];
	snprintf(buf, sizeof buf, "Windows error %lu", (unsigned long)GetLastError());
	return buf;
}
#else
#include <dlfcn.h>
static void* fdlOpen(const char* path, int full) { (void)full; return dlopen(path, RTLD_NOW | RTLD_LOCAL); }
static void* fdlSym(void* h, const char* name) { return dlsym(h, name); }
static void fdlClose(void* h) { dlclose(h); }
static const char* fdlError(void) { const char* e = dlerror(); return e ? e : ""; }
#endif

// fdlBuf is a growing output buffer.
typedef struct {
	unsigned char* data;
	size_t len, cap;
	int oom;
} fdlBuf;

static void fdlAppend(fdlBuf* b, const unsigned char* p, long n) {
	if (n <= 0 || b->oom) {
		return;
	}
	if (b->len + (size_t)n > b->cap) {
		size_t c = b->cap ? b->cap * 2 : 65536;
		while (c < b->len + (size_t)n) {
			c *= 2;
		}
		unsigned char* d = (unsigned char*)realloc(b->data, c);
		if (!d) {
			b->oom = 1;
			return;
		}
		b->data = d;
		b->cap = c;
	}
	memcpy(b->data + b->len, p, (size_t)n);
	b->len += (size_t)n;
}

static void fdlFree(void* p) { free(p); }

// As libogg declares them.
typedef struct {
	unsigned char* header;
	long header_len;
	unsigned char* body;
	long body_len;
} fdlOggPage;

typedef struct {
	unsigned char* packet;
	long bytes;
	long b_o_s;
	long e_o_s;
	int64_t granulepos;
	int64_t packetno;
} fdlOggPacket;

// The opaque structs: generous blocks (the largest, ogg_stream_state, is
// under 512 bytes on every platform).
#define FDL_OPAQUE 16384

typedef struct {
	void (*info_init)(void*);
	int (*encode_init_vbr)(void*, long, long, float);
	void (*comment_init)(void*);
	void (*comment_add_tag)(void*, const char*, const char*);
	int (*analysis_init)(void*, void*);
	int (*block_init)(void*, void*);
	int (*analysis_headerout)(void*, void*, fdlOggPacket*, fdlOggPacket*, fdlOggPacket*);
	float** (*analysis_buffer)(void*, int);
	int (*analysis_wrote)(void*, int);
	int (*analysis_blockout)(void*, void*);
	int (*analysis)(void*, fdlOggPacket*);
	int (*bitrate_addblock)(void*);
	int (*bitrate_flushpacket)(void*, fdlOggPacket*);
	int (*block_clear)(void*);
	void (*dsp_clear)(void*);
	void (*comment_clear)(void*);
	void (*info_clear)(void*);
	int (*stream_init)(void*, int);
	int (*stream_packetin)(void*, fdlOggPacket*);
	int (*stream_pageout)(void*, fdlOggPage*);
	int (*stream_flush)(void*, fdlOggPage*);
	int (*page_eos)(const fdlOggPage*);
	int (*stream_clear)(void*);
} fdlVorbis;

#define FDL_VORBIS_SYMS 23

static void fdlVorbisSet(fdlVorbis* v, void** s) {
	void** f = (void**)v;
	for (int i = 0; i < FDL_VORBIS_SYMS; i++) {
		f[i] = s[i];
	}
}

static void fdlPage(fdlBuf* out, fdlOggPage* og) {
	fdlAppend(out, og->header, og->header_len);
	fdlAppend(out, og->body, og->body_len);
}

// fdlVorbisEncode encodes interleaved frames as one Ogg Vorbis stream.
// It returns 0, or -1 when the encoder refuses the settings, -2 when out
// of memory.
static int fdlVorbisEncode(fdlVorbis* v, const float* pcm, long frames, int channels, long rate, float quality,
		char** tags, int ntags, int serial, fdlBuf* out) {
	void* vi = calloc(1, FDL_OPAQUE);
	void* vc = calloc(1, FDL_OPAQUE);
	void* vd = calloc(1, FDL_OPAQUE);
	void* vb = calloc(1, FDL_OPAQUE);
	void* os = calloc(1, FDL_OPAQUE);
	int ret = 0;
	if (!vi || !vc || !vd || !vb || !os) {
		ret = -2;
		goto freeall;
	}
	v->info_init(vi);
	if (v->encode_init_vbr(vi, channels, rate, quality) != 0) {
		v->info_clear(vi);
		ret = -1;
		goto freeall;
	}
	v->comment_init(vc);
	for (int i = 0; i + 1 < ntags; i += 2) {
		v->comment_add_tag(vc, tags[i], tags[i + 1]);
	}
	v->analysis_init(vd, vi);
	v->block_init(vd, vb);
	v->stream_init(os, serial);
	{
		fdlOggPacket h1, h2, h3, op;
		fdlOggPage og;
		v->analysis_headerout(vd, vc, &h1, &h2, &h3);
		v->stream_packetin(os, &h1);
		v->stream_packetin(os, &h2);
		v->stream_packetin(os, &h3);
		// The headers on pages of their own, as the format asks.
		while (v->stream_flush(os, &og) != 0) {
			fdlPage(out, &og);
		}
		long at = 0;
		int eos = 0, ended = 0;
		while (!eos) {
			long n = frames - at;
			if (n > 1024) {
				n = 1024;
			}
			if (n > 0) {
				float** buf = v->analysis_buffer(vd, (int)n);
				for (long i = 0; i < n; i++) {
					for (int c = 0; c < channels; c++) {
						buf[c][i] = pcm[(at + i) * channels + c];
					}
				}
				v->analysis_wrote(vd, (int)n);
				at += n;
			} else if (!ended) {
				v->analysis_wrote(vd, 0);
				ended = 1;
			} else {
				// Everything is out: the last page is the stream's end.
				while (v->stream_flush(os, &og) != 0) {
					fdlPage(out, &og);
				}
				break;
			}
			while (v->analysis_blockout(vd, vb) == 1) {
				v->analysis(vb, NULL);
				v->bitrate_addblock(vb);
				while (v->bitrate_flushpacket(vd, &op)) {
					v->stream_packetin(os, &op);
					while (!eos) {
						if (v->stream_pageout(os, &og) == 0) {
							break;
						}
						fdlPage(out, &og);
						if (v->page_eos(&og)) {
							eos = 1;
						}
					}
				}
			}
		}
	}
	v->stream_clear(os);
	v->block_clear(vb);
	v->dsp_clear(vd);
	v->comment_clear(vc);
	v->info_clear(vi);
	if (out->oom) {
		ret = -2;
	}
freeall:
	free(vi);
	free(vc);
	free(vd);
	free(vb);
	free(os);
	return ret;
}

typedef struct {
	void* (*init)(void);
	int (*set_num_channels)(void*, int);
	int (*set_in_samplerate)(void*, int);
	int (*set_out_samplerate)(void*, int);
	int (*set_VBR)(void*, int);
	int (*set_VBR_q)(void*, int);
	int (*set_brate)(void*, int);
	int (*set_quality)(void*, int);
	int (*set_mode)(void*, int);
	int (*set_bWriteVbrTag)(void*, int);
	int (*init_params)(void*);
	int (*encode_interleaved_float)(void*, const float*, int, unsigned char*, int);
	int (*encode_float)(void*, const float*, const float*, int, unsigned char*, int);
	int (*encode_flush)(void*, unsigned char*, int);
	size_t (*get_lametag_frame)(void*, unsigned char*, size_t);
	int (*close)(void*);
} fdlLame;

#define FDL_LAME_SYMS 16

static void fdlLameSet(fdlLame* l, void** s) {
	void** f = (void**)l;
	for (int i = 0; i < FDL_LAME_SYMS; i++) {
		f[i] = s[i];
	}
}

// fdlLameEncode encodes interleaved frames (one or two channels) as MP3:
// VBR at quality vbrq (0..9) when kbps is 0, else constant bit rate. It
// returns 0, or a negative LAME error.
static int fdlLameEncode(fdlLame* l, const float* pcm, long frames, int channels, int rate, int vbrq, int kbps, fdlBuf* out) {
	void* g = l->init();
	if (!g) {
		return -100;
	}
	l->set_num_channels(g, channels);
	l->set_in_samplerate(g, rate);
	l->set_out_samplerate(g, rate);
	l->set_mode(g, channels == 1 ? 3 : 1); // MONO, JOINT_STEREO
	l->set_quality(g, 2);
	l->set_bWriteVbrTag(g, 1);
	if (kbps > 0) {
		l->set_VBR(g, 0); // vbr_off
		l->set_brate(g, kbps);
	} else {
		l->set_VBR(g, 4); // vbr_mtrh, LAME's default VBR
		l->set_VBR_q(g, vbrq);
	}
	int ret = l->init_params(g);
	if (ret < 0) {
		l->close(g);
		return ret;
	}
	const long chunk = 4096;
	int cap = (int)(chunk * 5 / 4 + 7200);
	unsigned char* mp3 = (unsigned char*)malloc((size_t)cap);
	if (!mp3) {
		l->close(g);
		return -101;
	}
	for (long at = 0; at < frames && ret >= 0; at += chunk) {
		long n = frames - at;
		if (n > chunk) {
			n = chunk;
		}
		int w;
		if (channels == 2) {
			w = l->encode_interleaved_float(g, pcm + at * 2, (int)n, mp3, cap);
		} else {
			w = l->encode_float(g, pcm + at, pcm + at, (int)n, mp3, cap);
		}
		if (w < 0) {
			ret = w;
			break;
		}
		fdlAppend(out, mp3, w);
	}
	if (ret >= 0) {
		int w = l->encode_flush(g, mp3, cap);
		if (w < 0) {
			ret = w;
		} else {
			fdlAppend(out, mp3, w);
			ret = 0;
		}
	}
	if (ret == 0 && !out->oom) {
		// The VBR header frame, written now that the stream's length is
		// known, over the placeholder LAME put first.
		unsigned char tag[4096];
		size_t n = l->get_lametag_frame(g, tag, sizeof tag);
		if (n > 0 && n <= out->len) {
			memcpy(out->data, tag, n);
		}
	}
	free(mp3);
	l->close(g);
	if (out->oom) {
		return -102;
	}
	return ret;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"
)

// searchSystem lets a library come from the system's own search path;
// tests turn it off to see what an editor without one says.
var searchSystem = true

// CodecDirs are the directories searched for a codec library before the
// system's: $FOUNDRY_CODECS, then codecs/ beside the executable, then the
// executable's own directory.
func CodecDirs() []string {
	var out []string
	if d := os.Getenv("FOUNDRY_CODECS"); d != "" {
		out = append(out, d)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		out = append(out, filepath.Join(dir, "codecs"), dir)
	}
	return out
}

// libNames are a library's file names on this platform, in the order
// tried.
func libNames(lib string) []string {
	switch runtime.GOOS {
	case "windows":
		return map[string][]string{
			"ogg":       {"libogg-0.dll", "libogg.dll", "ogg.dll"},
			"vorbis":    {"libvorbis-0.dll", "libvorbis.dll", "vorbis.dll"},
			"vorbisenc": {"libvorbisenc-2.dll", "libvorbisenc.dll", "vorbisenc.dll"},
			"mp3lame":   {"libmp3lame-0.dll", "libmp3lame.dll", "mp3lame.dll"},
		}[lib]
	case "darwin":
		return map[string][]string{
			"ogg":       {"libogg.0.dylib", "libogg.dylib"},
			"vorbis":    {"libvorbis.0.dylib", "libvorbis.dylib"},
			"vorbisenc": {"libvorbisenc.2.dylib", "libvorbisenc.dylib"},
			"mp3lame":   {"libmp3lame.0.dylib", "libmp3lame.dylib"},
		}[lib]
	}
	return map[string][]string{
		"ogg":       {"libogg.so.0", "libogg.so"},
		"vorbis":    {"libvorbis.so.0", "libvorbis.so"},
		"vorbisenc": {"libvorbisenc.so.2", "libvorbisenc.so"},
		"mp3lame":   {"libmp3lame.so.0", "libmp3lame.so"},
	}[lib]
}

// openLib opens a library: a file of one of its names in the codec
// directories, else (searchSystem) by name from the system's path. It
// returns the handle, what it opened, and everything it tried.
func openLib(lib string) (unsafe.Pointer, string, []string, string) {
	var tried []string
	last := ""
	try := func(path string, full bool) unsafe.Pointer {
		tried = append(tried, path)
		cp := C.CString(path)
		defer C.free(unsafe.Pointer(cp))
		f := C.int(0)
		if full {
			f = 1
		}
		h := C.fdlOpen(cp, f)
		if h == nil {
			last = C.GoString(C.fdlError())
		}
		return h
	}
	for _, dir := range CodecDirs() {
		for _, n := range libNames(lib) {
			p := filepath.Join(dir, n)
			if _, err := os.Stat(p); err != nil {
				continue
			}
			if h := try(p, true); h != nil {
				return h, p, tried, ""
			}
		}
	}
	if searchSystem {
		for _, n := range libNames(lib) {
			if h := try(n, false); h != nil {
				return h, n, tried, ""
			}
		}
	}
	return nil, "", tried, last
}

// syms looks up symbols in a handle.
func syms(h unsafe.Pointer, names []string) ([]unsafe.Pointer, error) {
	out := make([]unsafe.Pointer, len(names))
	for i, n := range names {
		cn := C.CString(n)
		out[i] = C.fdlSym(h, cn)
		C.free(unsafe.Pointer(cn))
		if out[i] == nil {
			return nil, fmt.Errorf("it has no %s", n)
		}
	}
	return out, nil
}

// Libraries reports where each codec library was loaded from, for an
// About box or `foundry score codecs`: "" for one not found.
func Libraries() map[string]string {
	out := map[string]string{}
	if v, err := loadVorbis(); err == nil {
		out["libvorbisenc"] = v.path
	} else {
		out["libvorbisenc"] = ""
	}
	if l, err := loadLAME(); err == nil {
		out["libmp3lame"] = l.path
	} else {
		out["libmp3lame"] = ""
	}
	return out
}

type vorbisLib struct {
	fns  C.fdlVorbis
	path string
}

type lameLib struct {
	fns  C.fdlLame
	path string
}

var (
	vorbisMu  sync.Mutex
	vorbisLd  *vorbisLib
	vorbisErr error
	lameMu    sync.Mutex
	lameLd    *lameLib
	lameErr   error
)

// resetLibraries forgets what was loaded or not (tests).
func resetLibraries() {
	vorbisMu.Lock()
	vorbisLd, vorbisErr = nil, nil
	vorbisMu.Unlock()
	lameMu.Lock()
	lameLd, lameErr = nil, nil
	lameMu.Unlock()
}

var vorbisSyms = [][2]string{
	{"vorbis", "vorbis_info_init"}, {"vorbisenc", "vorbis_encode_init_vbr"}, {"vorbis", "vorbis_comment_init"},
	{"vorbis", "vorbis_comment_add_tag"}, {"vorbis", "vorbis_analysis_init"}, {"vorbis", "vorbis_block_init"},
	{"vorbis", "vorbis_analysis_headerout"}, {"vorbis", "vorbis_analysis_buffer"}, {"vorbis", "vorbis_analysis_wrote"},
	{"vorbis", "vorbis_analysis_blockout"}, {"vorbis", "vorbis_analysis"}, {"vorbis", "vorbis_bitrate_addblock"},
	{"vorbis", "vorbis_bitrate_flushpacket"}, {"vorbis", "vorbis_block_clear"}, {"vorbis", "vorbis_dsp_clear"},
	{"vorbis", "vorbis_comment_clear"}, {"vorbis", "vorbis_info_clear"}, {"ogg", "ogg_stream_init"},
	{"ogg", "ogg_stream_packetin"}, {"ogg", "ogg_stream_pageout"}, {"ogg", "ogg_stream_flush"},
	{"ogg", "ogg_page_eos"}, {"ogg", "ogg_stream_clear"},
}

func loadVorbis() (*vorbisLib, error) {
	vorbisMu.Lock()
	defer vorbisMu.Unlock()
	if vorbisLd != nil || vorbisErr != nil {
		return vorbisLd, vorbisErr
	}
	// libogg and libvorbis first, so libvorbisenc finds them loaded.
	handles := map[string]unsafe.Pointer{}
	var allTried []string
	path := ""
	for _, lib := range []string{"ogg", "vorbis", "vorbisenc"} {
		h, p, tried, reason := openLib(lib)
		allTried = append(allTried, tried...)
		if h == nil {
			for _, o := range handles {
				C.fdlClose(o)
			}
			vorbisErr = &MissingLibraryError{Format: OGG, Library: "lib" + lib, Tried: allTried, Reason: reason}
			return nil, vorbisErr
		}
		handles[lib] = h
		path = p
	}
	ptrs := make([]unsafe.Pointer, len(vorbisSyms))
	for i, s := range vorbisSyms {
		got, err := syms(handles[s[0]], []string{s[1]})
		if err != nil {
			vorbisErr = &MissingLibraryError{Format: OGG, Library: "lib" + s[0], Tried: allTried, Reason: err.Error()}
			return nil, vorbisErr
		}
		ptrs[i] = got[0]
	}
	v := &vorbisLib{path: path}
	cp := C.malloc(C.size_t(len(ptrs)) * C.size_t(unsafe.Sizeof(uintptr(0))))
	defer C.free(cp)
	arr := unsafe.Slice((*unsafe.Pointer)(cp), len(ptrs))
	copy(arr, ptrs)
	C.fdlVorbisSet(&v.fns, (*unsafe.Pointer)(cp))
	vorbisLd = v
	return v, nil
}

var lameSyms = []string{
	"lame_init", "lame_set_num_channels", "lame_set_in_samplerate", "lame_set_out_samplerate",
	"lame_set_VBR", "lame_set_VBR_q", "lame_set_brate", "lame_set_quality", "lame_set_mode",
	"lame_set_bWriteVbrTag", "lame_init_params", "lame_encode_buffer_interleaved_ieee_float",
	"lame_encode_buffer_ieee_float", "lame_encode_flush", "lame_get_lametag_frame", "lame_close",
}

func loadLAME() (*lameLib, error) {
	lameMu.Lock()
	defer lameMu.Unlock()
	if lameLd != nil || lameErr != nil {
		return lameLd, lameErr
	}
	h, p, tried, reason := openLib("mp3lame")
	if h == nil {
		lameErr = &MissingLibraryError{Format: MP3, Library: "libmp3lame", Tried: tried, Reason: reason}
		return nil, lameErr
	}
	ptrs, err := syms(h, lameSyms)
	if err != nil {
		// LAME before 3.99.5 has no float input.
		C.fdlClose(h)
		lameErr = &MissingLibraryError{Format: MP3, Library: "libmp3lame (3.99.5 or later)", Tried: tried, Reason: err.Error()}
		return nil, lameErr
	}
	l := &lameLib{path: p}
	cp := C.malloc(C.size_t(len(ptrs)) * C.size_t(unsafe.Sizeof(uintptr(0))))
	defer C.free(cp)
	arr := unsafe.Slice((*unsafe.Pointer)(cp), len(ptrs))
	copy(arr, ptrs)
	C.fdlLameSet(&l.fns, (*unsafe.Pointer)(cp))
	lameLd = l
	return l, nil
}

// takeBuf copies a C output buffer into Go memory and frees it.
func takeBuf(b *C.fdlBuf) []byte {
	if b.data == nil {
		return nil
	}
	out := C.GoBytes(unsafe.Pointer(b.data), C.int(b.len))
	C.fdlFree(unsafe.Pointer(b.data))
	b.data = nil
	return out
}

// EncodeVorbis writes interleaved audio as an Ogg Vorbis file at a
// quality (-1..10; 6 is about 192 kb/s for stereo at 48 kHz).
func EncodeVorbis(data []float32, channels, rate int, quality float32, tags Tags) ([]byte, error) {
	v, err := loadVorbis()
	if err != nil {
		return nil, err
	}
	if channels < 1 || channels > 255 || rate < 1 {
		return nil, fmt.Errorf("ogg: %d channels at %d Hz", channels, rate)
	}
	if quality < -1 || quality > 10 {
		return nil, fmt.Errorf("ogg: quality %g (want -1..10)", quality)
	}
	frames := len(data) / channels
	pcm := clipped(data[:frames*channels])
	kv := append([][2]string{{"ENCODER", Vendor}}, tags.pairs()...)
	ctags := make([]*C.char, 0, 2*len(kv))
	for _, p := range kv {
		ctags = append(ctags, C.CString(p[0]), C.CString(p[1]))
	}
	defer func() {
		for _, c := range ctags {
			C.free(unsafe.Pointer(c))
		}
	}()
	// The tag pointers go to C in C memory.
	tp := C.malloc(C.size_t(max(1, len(ctags))) * C.size_t(unsafe.Sizeof(uintptr(0))))
	defer C.free(tp)
	copy(unsafe.Slice((**C.char)(tp), max(1, len(ctags))), ctags)
	var out C.fdlBuf
	var pp *C.float
	if len(pcm) > 0 {
		pp = (*C.float)(unsafe.Pointer(&pcm[0]))
	}
	// A fixed serial number: the same audio makes the same file.
	serial := C.int(0x46534352)
	ret := C.fdlVorbisEncode(&v.fns, pp, C.long(frames), C.int(channels), C.long(rate), C.float(quality),
		(**C.char)(tp), C.int(len(ctags)), serial, &out)
	b := takeBuf(&out)
	switch ret {
	case 0:
		return b, nil
	case -1:
		return nil, fmt.Errorf("ogg: libvorbis cannot encode %d channels at %d Hz, quality %g", channels, rate, quality)
	}
	return nil, errors.New("ogg: out of memory")
}

// mp3Quality is a VBR quality as LAME's 0..9.
func mp3Quality(q float32) int { return max(0, min(9, int(q+0.5))) }

// EncodeMP3 writes interleaved audio (one or two channels) as MP3 with
// LAME: VBR at o.Quality, or o.Bitrate kb/s constant. Tags go in an ID3v2
// tag at the front.
func EncodeMP3(data []float32, channels, rate int, o Options) ([]byte, error) {
	l, err := loadLAME()
	if err != nil {
		return nil, err
	}
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("mp3: %d channels (MP3 holds one or two)", channels)
	}
	switch rate {
	case 32000, 44100, 48000, 16000, 22050, 24000, 8000, 11025, 12000:
	default:
		return nil, fmt.Errorf("mp3: %d Hz is not an MP3 sample rate (44100 or 48000)", rate)
	}
	if o.Bitrate != 0 && (o.Bitrate < 32 || o.Bitrate > 320) {
		return nil, fmt.Errorf("mp3: %d kb/s (want 32..320)", o.Bitrate)
	}
	frames := len(data) / channels
	pcm := clipped(data[:frames*channels])
	var out C.fdlBuf
	var pp *C.float
	if len(pcm) > 0 {
		pp = (*C.float)(unsafe.Pointer(&pcm[0]))
	}
	ret := C.fdlLameEncode(&l.fns, pp, C.long(frames), C.int(channels), C.int(rate), C.int(mp3Quality(o.Quality)), C.int(o.Bitrate), &out)
	b := takeBuf(&out)
	if ret != 0 {
		return nil, fmt.Errorf("mp3: LAME refused the encoding (error %d)", int(ret))
	}
	return append(id3v2(o.Tags), b...), nil
}

// clipped is the samples in -1..1 with NaNs silenced, as the encoders
// expect them.
func clipped(data []float32) []float32 {
	out := make([]float32, len(data))
	for i, v := range data {
		switch {
		case v != v:
			v = 0
		case v > 1:
			v = 1
		case v < -1:
			v = -1
		}
		out[i] = v
	}
	return out
}

// id3v2 is an ID3v2.3 tag of the title, artist, album, comment and
// encoder, or nothing when there is no tag but the encoder.
func id3v2(t Tags) []byte {
	type frame struct{ id, text string }
	var frames []frame
	for _, f := range []frame{{"TIT2", t.Title}, {"TPE1", t.Artist}, {"TALB", t.Album}} {
		if f.text != "" {
			frames = append(frames, f)
		}
	}
	frames = append(frames, frame{"TSSE", Vendor})
	var body []byte
	for _, f := range frames {
		// Text as UTF-16 with a byte-order mark (encoding 1), which every
		// reader of v2.3 understands.
		txt := []byte{1, 0xFF, 0xFE}
		for _, r := range strings.ToValidUTF8(f.text, "?") {
			if r > 0xFFFF {
				r = '?'
			}
			txt = append(txt, byte(r), byte(r>>8))
		}
		n := len(txt)
		body = append(body, f.id...)
		body = append(body, byte(n>>24), byte(n>>16), byte(n>>8), byte(n), 0, 0)
		body = append(body, txt...)
	}
	if t.Comment != "" {
		txt := []byte{1, 'e', 'n', 'g', 0xFF, 0xFE, 0, 0, 0xFF, 0xFE}
		for _, r := range t.Comment {
			if r > 0xFFFF {
				r = '?'
			}
			txt = append(txt, byte(r), byte(r>>8))
		}
		n := len(txt)
		body = append(body, "COMM"...)
		body = append(body, byte(n>>24), byte(n>>16), byte(n>>8), byte(n), 0, 0)
		body = append(body, txt...)
	}
	n := len(body)
	hdr := []byte{'I', 'D', '3', 3, 0, 0, byte(n >> 21 & 0x7F), byte(n >> 14 & 0x7F), byte(n >> 7 & 0x7F), byte(n & 0x7F)}
	return append(hdr, body...)
}
