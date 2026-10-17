// Package mididev reads MIDI keyboards and controllers: the ports a
// system offers, and the messages one of them sends.
//
// Each platform has its own way in, behind one small API:
//
//   - Linux: the ALSA sequencer (alsa_linux.go). libasound.so.2 is opened
//     at run time (dlopen), as the score's codecs are, so an editor built
//     on a machine with no ALSA development files still has MIDI input, and
//     one run on a machine with no ALSA at all says so instead of failing
//     to start. Every sequencer client with a readable port is listed:
//     USB keyboards, virtual ports, other programs.
//   - Windows: WinMM (winmm_windows.go): midiInOpen with a callback that
//     queues short messages in C for the reader to collect.
//   - Anywhere else, or a build without cgo (none.go): no ports, and Open
//     says MIDI input is not available here.
//
// An open port is an Input. A goroutine per input collects what the port
// sends and stamps each message with the time it arrived; Poll hands them
// over without blocking, so the editor drains them once a frame. A Virtual
// input carries messages a test (or another part of the program) sends it,
// through the same Input API.
//
// Only the editor uses this package: the release game never imports it
// (internal/layering_test.go holds it to that).
package mididev

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Port is one MIDI source the system offers.
type Port struct {
	// ID opens the port: the ALSA "client:port", the WinMM device index. It
	// can change when devices come and go; Name is what a setting keeps.
	ID string
	// Name is what the port calls itself ("USB Keystation 61: Port 1").
	Name string
}

// Message is one MIDI message: a status byte and up to two data bytes
// (running status already resolved), stamped with when it arrived.
type Message struct {
	Status, Data1, Data2 byte
	Time                 time.Time
}

// The channel messages a keyboard sends, by the status byte's high nibble.
const (
	NoteOff         = 0x80
	NoteOn          = 0x90
	PolyPressure    = 0xA0
	ControlChange   = 0xB0
	ProgramChange   = 0xC0
	ChannelPressure = 0xD0
	PitchBend       = 0xE0
)

// SustainPedal is the damper pedal's controller (CC 64).
const SustainPedal = 64

// Kind is the message's type: the status byte's high nibble (NoteOn …).
func (m Message) Kind() byte { return m.Status & 0xF0 }

// Channel is the message's channel, 0-15.
func (m Message) Channel() int { return int(m.Status & 0x0F) }

// IsNoteOn reports a key going down (a note-on with velocity 0 is a
// note-off, as running-status keyboards send it).
func (m Message) IsNoteOn() bool { return m.Kind() == NoteOn && m.Data2 > 0 }

// IsNoteOff reports a key coming up.
func (m Message) IsNoteOff() bool {
	return m.Kind() == NoteOff || (m.Kind() == NoteOn && m.Data2 == 0)
}

// Note and Velocity are a note message's key and velocity.
func (m Message) Note() int     { return int(m.Data1) }
func (m Message) Velocity() int { return int(m.Data2) }

// Bend is a pitch bend's value, -8192..8191.
func (m Message) Bend() int { return (int(m.Data1) | int(m.Data2)<<7) - 8192 }

// String describes a message for a log or a status line.
func (m Message) String() string {
	switch {
	case m.IsNoteOn():
		return fmt.Sprintf("note on %d vel %d ch %d", m.Note(), m.Velocity(), m.Channel()+1)
	case m.IsNoteOff():
		return fmt.Sprintf("note off %d ch %d", m.Note(), m.Channel()+1)
	case m.Kind() == ControlChange:
		return fmt.Sprintf("CC %d = %d ch %d", m.Data1, m.Data2, m.Channel()+1)
	case m.Kind() == PitchBend:
		return fmt.Sprintf("pitch bend %d ch %d", m.Bend(), m.Channel()+1)
	case m.Kind() == ProgramChange:
		return fmt.Sprintf("program %d ch %d", m.Data1, m.Channel()+1)
	}
	return fmt.Sprintf("status %02X %02X %02X", m.Status, m.Data1, m.Data2)
}

// dataBytes is how many data bytes a status byte takes (-1: not a channel
// or system common message this package passes on).
func dataBytes(status byte) int {
	switch status & 0xF0 {
	case NoteOff, NoteOn, PolyPressure, ControlChange, PitchBend:
		return 2
	case ProgramChange, ChannelPressure:
		return 1
	}
	return -1
}

// ErrUnavailable is Open's error where the platform or the build has no
// MIDI input.
var ErrUnavailable = errors.New("MIDI input is not available in this build")

// ErrClosed is Poll's error once an input is closed or its device is gone.
var ErrClosed = errors.New("MIDI input closed")

// Input is an open port.
type Input struct {
	// Port is what was opened.
	Port Port

	mu     sync.Mutex
	queue  []Message
	err    error
	closed bool
	// stop asks the reader to finish; done is closed when it has.
	stop chan struct{}
	done chan struct{}
	// closeFn releases the platform's handle once the reader has stopped.
	closeFn func()
}

// queueMax bounds what an input holds between polls: a minute of dense
// playing at the editor's frame rate is a few hundred messages, so this is
// only reached when nobody polls, and then the oldest go first.
const queueMax = 4096

func newInput(p Port) *Input {
	return &Input{Port: p, stop: make(chan struct{}), done: make(chan struct{})}
}

// push queues messages (the reader's side).
func (in *Input) push(ms ...Message) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.closed {
		return
	}
	in.queue = append(in.queue, ms...)
	if over := len(in.queue) - queueMax; over > 0 {
		in.queue = append(in.queue[:0], in.queue[over:]...)
	}
}

// fail records that the device went away (the reader's side).
func (in *Input) fail(err error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.err == nil {
		in.err = err
	}
}

// Poll appends the messages that have arrived since the last poll to dst,
// oldest first. The error is set once the device is gone or the input
// closed; messages that came before it are still returned.
func (in *Input) Poll(dst []Message) ([]Message, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	dst = append(dst, in.queue...)
	in.queue = in.queue[:0]
	if in.closed {
		return dst, ErrClosed
	}
	return dst, in.err
}

// Close stops the reader and releases the port. It is safe to call twice.
func (in *Input) Close() {
	in.mu.Lock()
	if in.closed {
		in.mu.Unlock()
		return
	}
	in.closed = true
	in.mu.Unlock()
	close(in.stop)
	<-in.done
	if in.closeFn != nil {
		in.closeFn()
	}
}

// Virtual is an input with no device behind it: what Send sends, Poll
// returns. Tests drive the editor's MIDI entry through one; it shows in no
// port list.
func Virtual(name string) *Input {
	in := newInput(Port{ID: "virtual", Name: name})
	close(in.done)
	return in
}

// Send queues messages on a virtual input, stamped now unless they carry a
// time. It parses nothing: each Message is one message.
func (in *Input) Send(ms ...Message) {
	now := time.Now()
	for i := range ms {
		if ms[i].Time.IsZero() {
			ms[i].Time = now
		}
	}
	in.push(ms...)
}

// Parser turns a byte stream (a serial port, a WinMM long message, a file)
// into messages, keeping running status across calls. System real-time
// bytes (clock, active sensing) inside a message are skipped without
// breaking it; system exclusive is skipped whole.
type Parser struct {
	status byte
	data   [2]byte
	n      int
	sysex  bool
}

// Feed parses bytes, appending each complete message to dst.
func (p *Parser) Feed(dst []Message, b []byte, at time.Time) []Message {
	for _, c := range b {
		switch {
		case c >= 0xF8:
			// Real time: one byte, anywhere, ignored.
			continue
		case c == 0xF0:
			p.sysex, p.status, p.n = true, 0, 0
			continue
		case c == 0xF7:
			p.sysex = false
			continue
		case c >= 0xF0:
			// System common ends running status; this package passes none on.
			p.sysex, p.status, p.n = false, 0, 0
			continue
		case c&0x80 != 0:
			p.sysex, p.status, p.n = false, c, 0
			continue
		}
		if p.sysex || p.status == 0 {
			continue
		}
		need := dataBytes(p.status)
		if need < 0 {
			continue
		}
		p.data[p.n] = c
		p.n++
		if p.n == need {
			m := Message{Status: p.status, Data1: p.data[0], Time: at}
			if need == 2 {
				m.Data2 = p.data[1]
			}
			dst = append(dst, m)
			p.n = 0
		}
	}
	return dst
}

// Unpack is a packed short message (status in the low byte, as WinMM's
// MIM_DATA hands it) as a Message, or false for one this package does not
// pass on.
func Unpack(packed uint32, at time.Time) (Message, bool) {
	st := byte(packed)
	if st&0x80 == 0 || dataBytes(st) < 0 {
		return Message{}, false
	}
	m := Message{Status: st, Data1: byte(packed>>8) & 0x7F, Time: at}
	if dataBytes(st) == 2 {
		m.Data2 = byte(packed>>16) & 0x7F
	}
	return m, true
}

// List is the ports this system offers now, or an error saying why there
// are none to ask about (no ALSA, no cgo).
func List() ([]Port, error) { return listPorts() }

// Open opens a port by its ID.
func Open(id string) (*Input, error) { return openPort(id) }

// OpenByName opens the port whose name is name (a saved setting), or the
// one whose name starts with it when none matches exactly.
func OpenByName(name string) (*Input, error) {
	ports, err := List()
	if err != nil {
		return nil, err
	}
	p, ok := FindPort(ports, name)
	if !ok {
		return nil, fmt.Errorf("no MIDI port %q is connected", name)
	}
	return Open(p.ID)
}

// FindPort is the port named name, else the first whose name starts with
// it (a device whose port numbering moved keeps its name's start).
func FindPort(ports []Port, name string) (Port, bool) {
	if name == "" {
		return Port{}, false
	}
	for _, p := range ports {
		if p.Name == name {
			return p, true
		}
	}
	for _, p := range ports {
		if len(p.Name) > len(name) && p.Name[:len(name)] == name {
			return p, true
		}
	}
	return Port{}, false
}
