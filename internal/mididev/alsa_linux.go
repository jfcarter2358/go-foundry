//go:build linux && cgo

package mididev

// alsa_linux.go — MIDI input through the ALSA sequencer.
//
// libasound.so.2 is opened with dlopen the first time a port is listed or
// opened, and its functions are called through pointers looked up then:
// nothing links against it, so the editor builds without ALSA's headers
// (CI has none) and runs without the library (it says MIDI input needs
// it). The sequencer's ABI is stable — snd_seq_event_t has been the same
// 28 bytes since ALSA 0.9 — and its layout is declared here; where the
// headers are installed the offsets are checked against them at compile
// time.
//
// The editor is a sequencer client ("Foundry Score") with one writable
// port; opening a source connects that source to it, and the system's
// announce port too, so a keyboard unplugged mid-session is noticed (its
// client or port exits) rather than going quiet. A reader goroutine waits
// in poll() with a short timeout, drains every pending event into a C
// array of packed short messages and pushes them as Messages.

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <errno.h>
#include <poll.h>
#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// The sequencer event as libasound declares it (alsa/seq_event.h).
typedef struct {
	unsigned char type, flags, tag, queue;
	unsigned int time[2];
	unsigned char src_client, src_port, dst_client, dst_port;
	union {
		struct { unsigned char channel, note, velocity, off_velocity; unsigned int duration; } note;
		struct { unsigned char channel, unused[3]; unsigned int param; int value; } control;
		struct { unsigned char client, port; } addr;
		unsigned char raw8[12];
	} data;
} fmidiSeqEvent;

#if defined(__has_include)
#if __has_include(<alsa/asoundlib.h>)
#include <alsa/asoundlib.h>
_Static_assert(sizeof(fmidiSeqEvent) == sizeof(snd_seq_event_t), "snd_seq_event_t size");
_Static_assert(offsetof(fmidiSeqEvent, data) == offsetof(snd_seq_event_t, data), "snd_seq_event_t data");
_Static_assert(offsetof(fmidiSeqEvent, src_client) == offsetof(snd_seq_event_t, source), "snd_seq_event_t source");
_Static_assert(offsetof(fmidiSeqEvent, data.control.value) == offsetof(snd_seq_event_t, data.control.value), "ctrl value");
#endif
#endif

typedef struct {
	void* lib;
	int (*open)(void**, const char*, int, int);
	int (*close)(void*);
	int (*set_client_name)(void*, const char*);
	int (*client_id)(void*);
	int (*create_simple_port)(void*, const char*, unsigned int, unsigned int);
	int (*connect_from)(void*, int, int, int);
	int (*event_input)(void*, fmidiSeqEvent**);
	int (*event_input_pending)(void*, int);
	int (*poll_descriptors_count)(void*, short);
	int (*poll_descriptors)(void*, struct pollfd*, unsigned int, short);
	int (*client_info_malloc)(void**);
	void (*client_info_free)(void*);
	void (*client_info_set_client)(void*, int);
	int (*client_info_get_client)(const void*);
	const char* (*client_info_get_name)(void*);
	int (*query_next_client)(void*, void*);
	int (*port_info_malloc)(void**);
	void (*port_info_free)(void*);
	void (*port_info_set_client)(void*, int);
	void (*port_info_set_port)(void*, int);
	int (*port_info_get_port)(const void*);
	const char* (*port_info_get_name)(const void*);
	unsigned int (*port_info_get_capability)(const void*);
	unsigned int (*port_info_get_type)(const void*);
	int (*query_next_port)(void*, void*);
} fmidiAlsa;

static fmidiAlsa fmidiA;

static const char* fmidiAlsaNames[] = {
	"snd_seq_open", "snd_seq_close", "snd_seq_set_client_name", "snd_seq_client_id",
	"snd_seq_create_simple_port", "snd_seq_connect_from", "snd_seq_event_input",
	"snd_seq_event_input_pending", "snd_seq_poll_descriptors_count", "snd_seq_poll_descriptors",
	"snd_seq_client_info_malloc", "snd_seq_client_info_free", "snd_seq_client_info_set_client",
	"snd_seq_client_info_get_client", "snd_seq_client_info_get_name", "snd_seq_query_next_client",
	"snd_seq_port_info_malloc", "snd_seq_port_info_free", "snd_seq_port_info_set_client",
	"snd_seq_port_info_set_port", "snd_seq_port_info_get_port", "snd_seq_port_info_get_name",
	"snd_seq_port_info_get_capability", "snd_seq_port_info_get_type", "snd_seq_query_next_port",
};

// fmidiAlsaQuiet is ALSA's error handler: silent.
static void fmidiAlsaQuiet(const char* file, int line, const char* fn, int err, const char* fmt, ...) {
	(void)file; (void)line; (void)fn; (void)err; (void)fmt;
}

// fmidiAlsaLoad opens libasound and looks every function up. It returns
// NULL, or what went wrong (a static string, or dlerror's).
static const char* fmidiAlsaLoad(void) {
	if (fmidiA.lib) {
		return NULL;
	}
	void* lib = dlopen("libasound.so.2", RTLD_NOW | RTLD_LOCAL);
	if (!lib) {
		lib = dlopen("libasound.so", RTLD_NOW | RTLD_LOCAL);
	}
	if (!lib) {
		const char* e = dlerror();
		return e ? e : "libasound.so.2 not found";
	}
	void** f = (void**)&fmidiA.open;
	size_t n = sizeof fmidiAlsaNames / sizeof fmidiAlsaNames[0];
	for (size_t i = 0; i < n; i++) {
		f[i] = dlsym(lib, fmidiAlsaNames[i]);
		if (!f[i]) {
			dlclose(lib);
			memset(&fmidiA, 0, sizeof fmidiA);
			return fmidiAlsaNames[i];
		}
	}
	fmidiA.lib = lib;
	// ALSA prints its own errors to stderr; Go reports them instead.
	int (*set_handler)(void*) = (int (*)(void*))dlsym(lib, "snd_lib_error_set_handler");
	if (set_handler) {
		set_handler((void*)fmidiAlsaQuiet);
	}
	return NULL;
}

#define FMIDI_OPEN_INPUT 2
#define FMIDI_NONBLOCK 1
#define FMIDI_CAP_READ (1u << 0)
#define FMIDI_CAP_WRITE (1u << 1)
#define FMIDI_CAP_SUBS_READ (1u << 5)
#define FMIDI_CAP_SUBS_WRITE (1u << 6)
#define FMIDI_CAP_NO_EXPORT (1u << 7)
#define FMIDI_TYPE_MIDI_GENERIC (1u << 1)
#define FMIDI_TYPE_APPLICATION (1u << 20)

static void* fmidiAlsaOpen(int* err) {
	void* seq = NULL;
	int r = fmidiA.open(&seq, "default", FMIDI_OPEN_INPUT, FMIDI_NONBLOCK);
	if (r < 0) {
		*err = r;
		return NULL;
	}
	fmidiA.set_client_name(seq, "Foundry Score");
	*err = 0;
	return seq;
}

// One port found by fmidiAlsaList.
typedef struct {
	int client, port;
	char name[160];
} fmidiAlsaPort;

// fmidiAlsaList fills out with up to max readable ports of other clients
// and returns how many, or a negative ALSA error.
static int fmidiAlsaList(fmidiAlsaPort* out, int max) {
	int err;
	void* seq = fmidiAlsaOpen(&err);
	if (!seq) {
		return err;
	}
	int self = fmidiA.client_id(seq);
	void *ci = NULL, *pi = NULL;
	fmidiA.client_info_malloc(&ci);
	fmidiA.port_info_malloc(&pi);
	int n = 0;
	fmidiA.client_info_set_client(ci, -1);
	while (n < max && fmidiA.query_next_client(seq, ci) >= 0) {
		int c = fmidiA.client_info_get_client(ci);
		if (c == 0 || c == self) {
			continue; // the system's timer and announce ports; ourselves
		}
		const char* cname = fmidiA.client_info_get_name(ci);
		fmidiA.port_info_set_client(pi, c);
		fmidiA.port_info_set_port(pi, -1);
		while (n < max && fmidiA.query_next_port(seq, pi) >= 0) {
			unsigned int caps = fmidiA.port_info_get_capability(pi);
			if ((caps & (FMIDI_CAP_READ | FMIDI_CAP_SUBS_READ)) != (FMIDI_CAP_READ | FMIDI_CAP_SUBS_READ) || (caps & FMIDI_CAP_NO_EXPORT)) {
				continue;
			}
			out[n].client = c;
			out[n].port = fmidiA.port_info_get_port(pi);
			const char* pname = fmidiA.port_info_get_name(pi);
			snprintf(out[n].name, sizeof out[n].name, "%s: %s", cname ? cname : "?", pname ? pname : "?");
			n++;
		}
	}
	fmidiA.port_info_free(pi);
	fmidiA.client_info_free(ci);
	fmidiA.close(seq);
	return n;
}

// An open input: the sequencer, our port, and the source it listens to.
typedef struct {
	void* seq;
	int port, client, src_port;
	struct pollfd fds[8];
	int nfds;
} fmidiAlsaIn;

static fmidiAlsaIn* fmidiAlsaConnect(int client, int port, int* err) {
	void* seq = fmidiAlsaOpen(err);
	if (!seq) {
		return NULL;
	}
	int me = fmidiA.create_simple_port(seq, "Input", FMIDI_CAP_WRITE | FMIDI_CAP_SUBS_WRITE, FMIDI_TYPE_MIDI_GENERIC | FMIDI_TYPE_APPLICATION);
	if (me < 0) {
		*err = me;
		fmidiA.close(seq);
		return NULL;
	}
	int r = fmidiA.connect_from(seq, me, client, port);
	if (r < 0) {
		*err = r;
		fmidiA.close(seq);
		return NULL;
	}
	// The announce port: client and port exits.
	fmidiA.connect_from(seq, me, 0, 1);
	fmidiAlsaIn* in = (fmidiAlsaIn*)calloc(1, sizeof *in);
	in->seq = seq;
	in->port = me;
	in->client = client;
	in->src_port = port;
	int n = fmidiA.poll_descriptors_count(seq, POLLIN);
	if (n > 8) {
		n = 8;
	}
	in->nfds = fmidiA.poll_descriptors(seq, in->fds, (unsigned int)n, POLLIN);
	*err = 0;
	return in;
}

static void fmidiAlsaClose(fmidiAlsaIn* in) {
	fmidiA.close(in->seq);
	free(in);
}

#define FMIDI_GONE (-1000000)

// fmidiAlsaRead waits up to timeout ms for events and turns each one this
// package passes on into a packed short message (status, data1 << 8,
// data2 << 16). It returns how many, or FMIDI_GONE when the source left.
static int fmidiAlsaRead(fmidiAlsaIn* in, uint32_t* out, int max, int timeout) {
	if (in->nfds > 0 && fmidiA.event_input_pending(in->seq, 0) <= 0) {
		int r = poll(in->fds, (nfds_t)in->nfds, timeout);
		if (r <= 0) {
			return 0;
		}
	}
	int n = 0;
	while (n < max) {
		fmidiSeqEvent* ev = NULL;
		int r = fmidiA.event_input(in->seq, &ev);
		if (r == -EAGAIN || r == -ENOSPC) {
			if (r == -ENOSPC) {
				continue; // the input buffer overran: what is left is still good
			}
			break;
		}
		if (r < 0 || !ev) {
			break;
		}
		unsigned char ch = ev->data.note.channel & 15;
		switch (ev->type) {
		case 6: // NOTEON
			out[n++] = (uint32_t)(0x90 | ch) | (uint32_t)(ev->data.note.note & 127) << 8 | (uint32_t)(ev->data.note.velocity & 127) << 16;
			break;
		case 7: // NOTEOFF
			out[n++] = (uint32_t)(0x80 | ch) | (uint32_t)(ev->data.note.note & 127) << 8 | (uint32_t)(ev->data.note.off_velocity & 127) << 16;
			break;
		case 8: // KEYPRESS
			out[n++] = (uint32_t)(0xA0 | ch) | (uint32_t)(ev->data.note.note & 127) << 8 | (uint32_t)(ev->data.note.velocity & 127) << 16;
			break;
		case 10: // CONTROLLER
			ch = ev->data.control.channel & 15;
			out[n++] = (uint32_t)(0xB0 | ch) | (uint32_t)(ev->data.control.param & 127) << 8 | (uint32_t)(ev->data.control.value & 127) << 16;
			break;
		case 11: // PGMCHANGE
			ch = ev->data.control.channel & 15;
			out[n++] = (uint32_t)(0xC0 | ch) | (uint32_t)(ev->data.control.value & 127) << 8;
			break;
		case 12: // CHANPRESS
			ch = ev->data.control.channel & 15;
			out[n++] = (uint32_t)(0xD0 | ch) | (uint32_t)(ev->data.control.value & 127) << 8;
			break;
		case 13: { // PITCHBEND, -8192..8191
			ch = ev->data.control.channel & 15;
			int v = ev->data.control.value + 8192;
			if (v < 0) v = 0;
			if (v > 16383) v = 16383;
			out[n++] = (uint32_t)(0xE0 | ch) | (uint32_t)(v & 127) << 8 | (uint32_t)((v >> 7) & 127) << 16;
			break;
		}
		case 61: // CLIENT_EXIT
			if (ev->data.addr.client == in->client) {
				return n > 0 ? n : FMIDI_GONE;
			}
			break;
		case 64: // PORT_EXIT
			if (ev->data.addr.client == in->client && ev->data.addr.port == in->src_port) {
				return n > 0 ? n : FMIDI_GONE;
			}
			break;
		}
	}
	return n;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	alsaOnce sync.Once
	alsaErr  error
	// alsaMu serialises the library's setup and every list and open (the
	// readers each own their sequencer handle).
	alsaMu sync.Mutex
)

func alsaLoad() error {
	alsaOnce.Do(func() {
		if e := C.fmidiAlsaLoad(); e != nil {
			alsaErr = fmt.Errorf("MIDI input needs ALSA (libasound.so.2): %s", C.GoString(e))
		}
	})
	return alsaErr
}

// alsaError is an ALSA return code as an error.
func alsaError(what string, code C.int) error {
	if code == 0 {
		return nil
	}
	errno := syscall.Errno(-int(code))
	if errno == syscall.ENOENT || errno == syscall.ENODEV {
		return fmt.Errorf("%s: no ALSA sequencer (/dev/snd/seq; is the snd-seq module loaded?)", what)
	}
	return fmt.Errorf("%s: %v", what, errno)
}

func listPorts() ([]Port, error) {
	if err := alsaLoad(); err != nil {
		return nil, err
	}
	alsaMu.Lock()
	defer alsaMu.Unlock()
	const max = 64
	buf := (*[max]C.fmidiAlsaPort)(C.calloc(max, C.size_t(unsafe.Sizeof(C.fmidiAlsaPort{}))))
	defer C.free(unsafe.Pointer(buf))
	n := C.fmidiAlsaList(&buf[0], max)
	if n < 0 {
		return nil, alsaError("listing MIDI ports", n)
	}
	out := make([]Port, 0, int(n))
	for i := 0; i < int(n); i++ {
		p := buf[i]
		out = append(out, Port{ID: fmt.Sprintf("%d:%d", int(p.client), int(p.port)), Name: C.GoString(&p.name[0])})
	}
	return out, nil
}

// parseAddr reads "client:port".
func parseAddr(id string) (client, port int, err error) {
	c, p, ok := strings.Cut(id, ":")
	if ok {
		client, err = strconv.Atoi(c)
		if err == nil {
			port, err = strconv.Atoi(p)
		}
	}
	if !ok || err != nil || client < 0 || port < 0 {
		return 0, 0, fmt.Errorf("bad ALSA port %q (want client:port)", id)
	}
	return client, port, nil
}

func openPort(id string) (*Input, error) {
	if err := alsaLoad(); err != nil {
		return nil, err
	}
	client, port, err := parseAddr(id)
	if err != nil {
		return nil, err
	}
	name := id
	if ports, err := listPorts(); err == nil {
		for _, p := range ports {
			if p.ID == id {
				name = p.Name
			}
		}
	}
	alsaMu.Lock()
	var code C.int
	h := C.fmidiAlsaConnect(C.int(client), C.int(port), &code)
	alsaMu.Unlock()
	if h == nil {
		return nil, alsaError("opening MIDI port "+id, code)
	}
	in := newInput(Port{ID: id, Name: name})
	in.closeFn = func() { C.fmidiAlsaClose(h) }
	go alsaRead(in, h)
	return in, nil
}

// alsaRead is the reader: wait, drain, push, until Close or the device
// leaves.
func alsaRead(in *Input, h *C.fmidiAlsaIn) {
	defer close(in.done)
	const max = 256
	buf := (*[max]C.uint32_t)(C.calloc(max, 4))
	defer C.free(unsafe.Pointer(buf))
	var ms []Message
	for {
		select {
		case <-in.stop:
			return
		default:
		}
		n := C.fmidiAlsaRead(h, &buf[0], max, 50)
		if n == C.FMIDI_GONE {
			in.fail(errors.New("the MIDI device was disconnected"))
			return
		}
		if n <= 0 {
			continue
		}
		now := time.Now()
		ms = ms[:0]
		for i := 0; i < int(n); i++ {
			if m, ok := Unpack(uint32(buf[i]), now); ok {
				ms = append(ms, m)
			}
		}
		in.push(ms...)
	}
}
