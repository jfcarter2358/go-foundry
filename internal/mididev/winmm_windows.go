//go:build windows && cgo

package mididev

// winmm_windows.go — MIDI input through WinMM.
//
// Every Windows has winmm.dll, so this links against it. midiInOpen takes
// a C callback (CALLBACK_FUNCTION) that WinMM calls on its own thread for
// every short message; it queues the packed message in a ring the reader
// goroutine drains, and signals an event the reader waits on with a short
// timeout. No Go runs on WinMM's thread and no C keeps a pointer into Go
// memory. The ring is single-producer single-consumer: the callback only
// moves its tail, the reader only its head. A device unplugged mid-session
// is reported by WinMM as MIM_CLOSE, which the callback marks for the
// reader to see.
//
// Port IDs are device indexes, which shift when devices come and go; the
// editor keeps a port's name and finds it again by that.

/*
#cgo LDFLAGS: -lwinmm
#define WIN32_LEAN_AND_MEAN 1
#include <windows.h>
#include <mmsystem.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define FMIDI_RING 4096

typedef struct {
	HMIDIIN h;
	HANDLE ev;
	volatile LONG head, tail;
	volatile LONG gone;
	DWORD msg[FMIDI_RING];
} fmidiWin;

static void CALLBACK fmidiWinProc(HMIDIIN h, UINT wMsg, DWORD_PTR inst, DWORD_PTR p1, DWORD_PTR p2) {
	(void)h; (void)p2;
	fmidiWin* w = (fmidiWin*)inst;
	switch (wMsg) {
	case MIM_DATA: {
		LONG t = w->tail;
		LONG n = (t + 1) % FMIDI_RING;
		if (n != w->head) {
			w->msg[t] = (DWORD)p1;
			MemoryBarrier();
			w->tail = n;
		}
		SetEvent(w->ev);
		break;
	}
	case MIM_CLOSE:
		w->gone = 1;
		SetEvent(w->ev);
		break;
	}
}

static int fmidiWinCount(void) { return (int)midiInGetNumDevs(); }

// fmidiWinName writes device i's name as UTF-8; 0, or the MMRESULT.
static int fmidiWinName(int i, char* out, int cap) {
	MIDIINCAPSW caps;
	MMRESULT r = midiInGetDevCapsW((UINT_PTR)i, &caps, sizeof caps);
	if (r != MMSYSERR_NOERROR) {
		return (int)r;
	}
	int n = WideCharToMultiByte(CP_UTF8, 0, caps.szPname, -1, out, cap, NULL, NULL);
	if (n <= 0) {
		out[0] = 0;
	}
	return 0;
}

static fmidiWin* fmidiWinOpen(int i, int* err) {
	fmidiWin* w = (fmidiWin*)calloc(1, sizeof *w);
	if (!w) {
		*err = MMSYSERR_NOMEM;
		return NULL;
	}
	w->ev = CreateEventA(NULL, FALSE, FALSE, NULL);
	MMRESULT r = midiInOpen(&w->h, (UINT)i, (DWORD_PTR)fmidiWinProc, (DWORD_PTR)w, CALLBACK_FUNCTION);
	if (r != MMSYSERR_NOERROR) {
		CloseHandle(w->ev);
		free(w);
		*err = (int)r;
		return NULL;
	}
	midiInStart(w->h);
	*err = 0;
	return w;
}

static void fmidiWinClose(fmidiWin* w) {
	midiInStop(w->h);
	midiInReset(w->h);
	midiInClose(w->h);
	CloseHandle(w->ev);
	free(w);
}

// fmidiWinRead waits up to timeout ms and moves what the ring holds to
// out. It returns how many, or -1 when the device has gone.
static int fmidiWinRead(fmidiWin* w, uint32_t* out, int max, int timeout) {
	if (w->head == w->tail && !w->gone) {
		WaitForSingleObject(w->ev, (DWORD)timeout);
	}
	int n = 0;
	while (n < max && w->head != w->tail) {
		MemoryBarrier();
		out[n++] = (uint32_t)w->msg[w->head];
		w->head = (w->head + 1) % FMIDI_RING;
	}
	if (n == 0 && w->gone) {
		return -1;
	}
	return n;
}

static void fmidiWinError(int code, char* out, int cap) {
	WCHAR wbuf[MAXERRORLENGTH];
	if (midiInGetErrorTextW((MMRESULT)code, wbuf, MAXERRORLENGTH) != MMSYSERR_NOERROR) {
		snprintf(out, cap, "WinMM error %d", code);
		return;
	}
	if (WideCharToMultiByte(CP_UTF8, 0, wbuf, -1, out, cap, NULL, NULL) <= 0) {
		snprintf(out, cap, "WinMM error %d", code);
	}
}
*/
import "C"

import (
	"errors"
	"fmt"
	"strconv"
	"time"
	"unsafe"
)

func winError(what string, code C.int) error {
	var buf [256]C.char
	C.fmidiWinError(code, &buf[0], C.int(len(buf)))
	return fmt.Errorf("%s: %s", what, C.GoString(&buf[0]))
}

func listPorts() ([]Port, error) {
	n := int(C.fmidiWinCount())
	out := make([]Port, 0, n)
	var buf [256]C.char
	for i := 0; i < n; i++ {
		if C.fmidiWinName(C.int(i), &buf[0], C.int(len(buf))) != 0 {
			continue
		}
		out = append(out, Port{ID: strconv.Itoa(i), Name: C.GoString(&buf[0])})
	}
	return out, nil
}

func openPort(id string) (*Input, error) {
	i, err := strconv.Atoi(id)
	if err != nil || i < 0 {
		return nil, fmt.Errorf("bad MIDI device %q (want its number)", id)
	}
	name := id
	if ports, err := listPorts(); err == nil {
		for _, p := range ports {
			if p.ID == id {
				name = p.Name
			}
		}
	}
	var code C.int
	w := C.fmidiWinOpen(C.int(i), &code)
	if w == nil {
		return nil, winError("opening MIDI device "+name, code)
	}
	in := newInput(Port{ID: id, Name: name})
	in.closeFn = func() { C.fmidiWinClose(w) }
	go winRead(in, w)
	return in, nil
}

func winRead(in *Input, w *C.fmidiWin) {
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
		n := C.fmidiWinRead(w, &buf[0], max, 50)
		if n < 0 {
			in.fail(errors.New("the MIDI device was disconnected"))
			return
		}
		if n == 0 {
			continue
		}
		now := time.Now()
		ms = ms[:0]
		for k := 0; k < int(n); k++ {
			if m, ok := Unpack(uint32(buf[k]), now); ok {
				ms = append(ms, m)
			}
		}
		in.push(ms...)
	}
}
