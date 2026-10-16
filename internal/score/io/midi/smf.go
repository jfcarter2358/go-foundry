// Package midi reads and writes Standard MIDI Files and turns them into
// scores and back.
//
// smf.go is the file format alone: header and track chunks, delta times,
// running status, channel messages, meta and system-exclusive events, in
// formats 0, 1 and 2. import.go makes a score of a file (quantise, voices,
// instruments, key, meter, tempo), export.go a file of a score (one track
// per line, the conductor's map on track 0).
package midi

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// Kind is what an event is.
type Kind uint8

const (
	NoteOff Kind = iota
	NoteOn
	PolyPressure
	Controller
	Program
	ChannelPressure
	PitchBend
	// Meta is a meta event (tempo, time signature, a track name…).
	Meta
	// SysEx is a system-exclusive message (F0 or F7).
	SysEx
)

// Meta event types this package reads and writes.
const (
	MetaText          = 0x01
	MetaCopyright     = 0x02
	MetaTrackName     = 0x03
	MetaInstrument    = 0x04
	MetaLyric         = 0x05
	MetaMarker        = 0x06
	MetaCue           = 0x07
	MetaChannelPrefix = 0x20
	MetaEndOfTrack    = 0x2F
	MetaTempo         = 0x51
	MetaSMPTEOffset   = 0x54
	MetaTimeSignature = 0x58
	MetaKeySignature  = 0x59
)

// Controllers the importer and exporter use.
const (
	CCVolume     = 7
	CCPan        = 10
	CCExpression = 11
	CCBrightness = 74
)

// Event is one event of a track at an absolute tick.
type Event struct {
	Tick    int64
	Kind    Kind
	Channel uint8 // 0..15, channel messages only
	// A and B are a channel message's data bytes (note and velocity,
	// controller and value; a pitch bend is A | B<<7).
	A, B uint8
	// MetaType is a meta event's type; Data its bytes (and a SysEx's,
	// after the F0 or F7, which is SysExType).
	MetaType  uint8
	SysExType uint8
	Data      []byte
}

// Track is a track chunk's events in tick order.
type Track struct {
	Events []Event
}

// File is a whole Standard MIDI File.
type File struct {
	// Format is 0 (one track), 1 (tracks played together) or 2
	// (independent sequences).
	Format int
	// Division is ticks per quarter note.
	Division int
	Tracks   []Track
}

// ErrSMPTE is a file timed in SMPTE frames rather than beats.
var ErrSMPTE = errors.New("midi: SMPTE-timed files are not supported (save it with ticks per quarter note)")

// ReadFile reads a .mid file.
func ReadFile(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := Read(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// Read parses a Standard MIDI File. It is forgiving where real files are
// sloppy: a missing end-of-track, a track length past the end of the file,
// chunks of unknown types, a RIFF (.rmi) wrapper.
func Read(r io.Reader) (*File, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	// RIFF-wrapped (RMID): skip to the data chunk.
	if len(data) >= 20 && string(data[:4]) == "RIFF" && string(data[8:12]) == "RMID" {
		if i := bytes.Index(data, []byte("MThd")); i >= 0 {
			data = data[i:]
		}
	}
	if len(data) < 14 || string(data[:4]) != "MThd" {
		return nil, errors.New("midi: not a Standard MIDI File (no MThd header)")
	}
	hlen := int(binary.BigEndian.Uint32(data[4:8]))
	if hlen < 6 || 8+hlen > len(data) {
		return nil, errors.New("midi: bad header length")
	}
	f := &File{
		Format: int(binary.BigEndian.Uint16(data[8:10])),
	}
	ntracks := int(binary.BigEndian.Uint16(data[10:12]))
	div := binary.BigEndian.Uint16(data[12:14])
	if div&0x8000 != 0 {
		return nil, ErrSMPTE
	}
	if div == 0 {
		return nil, errors.New("midi: zero ticks per quarter note")
	}
	f.Division = int(div)
	if f.Format > 2 {
		return nil, fmt.Errorf("midi: unknown format %d", f.Format)
	}
	pos := 8 + hlen
	for pos+8 <= len(data) && len(f.Tracks) < max(ntracks, 1)+64 {
		id := string(data[pos : pos+4])
		n := int(binary.BigEndian.Uint32(data[pos+4 : pos+8]))
		pos += 8
		end := pos + n
		if n < 0 || end > len(data) {
			end = len(data)
		}
		if id == "MTrk" {
			t, err := readTrack(data[pos:end])
			if err != nil {
				return nil, fmt.Errorf("midi: track %d: %w", len(f.Tracks)+1, err)
			}
			f.Tracks = append(f.Tracks, t)
		}
		pos = end
	}
	if len(f.Tracks) == 0 {
		return nil, errors.New("midi: no tracks")
	}
	return f, nil
}

// channelDataLen is how many data bytes follow each channel status.
var channelDataLen = [8]int{2, 2, 2, 2, 1, 1, 2, 0}

func readTrack(b []byte) (Track, error) {
	var t Track
	var tick int64
	var status uint8
	i := 0
	vlq := func() (int64, error) {
		var v int64
		for k := 0; k < 4; k++ {
			if i >= len(b) {
				return 0, io.ErrUnexpectedEOF
			}
			c := b[i]
			i++
			v = v<<7 | int64(c&0x7F)
			if c&0x80 == 0 {
				return v, nil
			}
		}
		return 0, errors.New("variable-length number longer than four bytes")
	}
	for i < len(b) {
		dt, err := vlq()
		if err != nil {
			// A truncated last event: keep what was read.
			break
		}
		tick += dt
		if i >= len(b) {
			break
		}
		c := b[i]
		switch {
		case c == 0xFF:
			i++
			if i >= len(b) {
				return t, nil
			}
			typ := b[i]
			i++
			n, err := vlq()
			if err != nil || i+int(n) > len(b) {
				return t, nil
			}
			ev := Event{Tick: tick, Kind: Meta, MetaType: typ, Data: append([]byte(nil), b[i:i+int(n)]...)}
			i += int(n)
			// Meta and system events cancel running status.
			status = 0
			if typ == MetaEndOfTrack {
				t.Events = append(t.Events, ev)
				return t, nil
			}
			t.Events = append(t.Events, ev)
		case c == 0xF0 || c == 0xF7:
			i++
			n, err := vlq()
			if err != nil || i+int(n) > len(b) {
				return t, nil
			}
			t.Events = append(t.Events, Event{Tick: tick, Kind: SysEx, SysExType: c, Data: append([]byte(nil), b[i:i+int(n)]...)})
			i += int(n)
			status = 0
		case c >= 0xF1:
			// System common / real-time bytes do not belong in a file;
			// skip the byte.
			i++
		default:
			if c&0x80 != 0 {
				status = c
				i++
			} else if status == 0 {
				return t, fmt.Errorf("data byte 0x%02X with no running status at tick %d", c, tick)
			}
			k := (status >> 4) & 7
			n := channelDataLen[k]
			if i+n > len(b) {
				return t, nil
			}
			ev := Event{Tick: tick, Kind: Kind(k), Channel: status & 0x0F, A: b[i] & 0x7F}
			if n == 2 {
				ev.B = b[i+1] & 0x7F
			}
			i += n
			t.Events = append(t.Events, ev)
		}
	}
	return t, nil
}

// Write writes the file. Events are written in tick order (stable, so a
// track's order at one tick is kept), with running status, and each track
// ends with an end-of-track at its last event.
func (f *File) Write(w io.Writer) error {
	bw := bufio.NewWriter(w)
	var hdr [14]byte
	copy(hdr[:4], "MThd")
	binary.BigEndian.PutUint32(hdr[4:8], 6)
	binary.BigEndian.PutUint16(hdr[8:10], uint16(f.Format))
	binary.BigEndian.PutUint16(hdr[10:12], uint16(len(f.Tracks)))
	if f.Division <= 0 || f.Division > 0x7FFF {
		return fmt.Errorf("midi: division %d", f.Division)
	}
	binary.BigEndian.PutUint16(hdr[12:14], uint16(f.Division))
	bw.Write(hdr[:])
	for _, t := range f.Tracks {
		body := encodeTrack(t)
		var th [8]byte
		copy(th[:4], "MTrk")
		binary.BigEndian.PutUint32(th[4:8], uint32(len(body)))
		bw.Write(th[:])
		bw.Write(body)
	}
	return bw.Flush()
}

// WriteFile writes the file to a path.
func (f *File) WriteFile(path string) error {
	var b bytes.Buffer
	if err := f.Write(&b); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}

func putVLQ(b *bytes.Buffer, v int64) {
	if v < 0 {
		v = 0
	}
	var tmp [5]byte
	n := 0
	tmp[4] = byte(v & 0x7F)
	n++
	for v >>= 7; v > 0; v >>= 7 {
		tmp[4-n] = byte(v&0x7F) | 0x80
		n++
	}
	b.Write(tmp[5-n:])
}

func encodeTrack(t Track) []byte {
	evs := append([]Event(nil), t.Events...)
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].Tick < evs[j].Tick })
	var b bytes.Buffer
	var last int64
	var status uint8
	ended := false
	for _, e := range evs {
		if ended {
			break
		}
		putVLQ(&b, e.Tick-last)
		last = e.Tick
		switch e.Kind {
		case Meta:
			b.WriteByte(0xFF)
			b.WriteByte(e.MetaType)
			putVLQ(&b, int64(len(e.Data)))
			b.Write(e.Data)
			status = 0
			ended = e.MetaType == MetaEndOfTrack
		case SysEx:
			st := e.SysExType
			if st != 0xF7 {
				st = 0xF0
			}
			b.WriteByte(st)
			putVLQ(&b, int64(len(e.Data)))
			b.Write(e.Data)
			status = 0
		default:
			s := 0x80 | uint8(e.Kind)<<4 | e.Channel&0x0F
			if s != status {
				b.WriteByte(s)
				status = s
			}
			b.WriteByte(e.A & 0x7F)
			if channelDataLen[e.Kind&7] == 2 {
				b.WriteByte(e.B & 0x7F)
			}
		}
	}
	if !ended {
		putVLQ(&b, 0)
		b.Write([]byte{0xFF, MetaEndOfTrack, 0})
	}
	return b.Bytes()
}

// ---------- meta helpers ----------

// TextMeta is a meta event carrying text.
func TextMeta(tick int64, typ uint8, text string) Event {
	return Event{Tick: tick, Kind: Meta, MetaType: typ, Data: []byte(text)}
}

// TempoMeta sets the tempo in quarters a minute.
func TempoMeta(tick int64, qpm float64) Event {
	us := int64(60e6/qpm + 0.5)
	us = max(1, min(us, 0xFFFFFF))
	return Event{Tick: tick, Kind: Meta, MetaType: MetaTempo, Data: []byte{byte(us >> 16), byte(us >> 8), byte(us)}}
}

// Tempo reads a tempo meta as quarters a minute.
func (e Event) Tempo() (float64, bool) {
	if e.Kind != Meta || e.MetaType != MetaTempo || len(e.Data) < 3 {
		return 0, false
	}
	us := int(e.Data[0])<<16 | int(e.Data[1])<<8 | int(e.Data[2])
	if us == 0 {
		return 0, false
	}
	return 60e6 / float64(us), true
}

// TimeSignatureMeta writes num/den (den a power of two).
func TimeSignatureMeta(tick int64, num, den int) Event {
	pow := 0
	for 1<<pow < den {
		pow++
	}
	// 24 MIDI clocks a metronome click, eight 32nds a quarter.
	return Event{Tick: tick, Kind: Meta, MetaType: MetaTimeSignature, Data: []byte{byte(num), byte(pow), 24, 8}}
}

// TimeSignature reads a time signature meta.
func (e Event) TimeSignature() (num, den int, ok bool) {
	if e.Kind != Meta || e.MetaType != MetaTimeSignature || len(e.Data) < 2 || e.Data[0] == 0 || e.Data[1] > 7 {
		return 0, 0, false
	}
	return int(e.Data[0]), 1 << e.Data[1], true
}

// KeySignatureMeta writes a key: sharps (negative flats), and minor.
func KeySignatureMeta(tick int64, fifths int, minor bool) Event {
	mi := byte(0)
	if minor {
		mi = 1
	}
	return Event{Tick: tick, Kind: Meta, MetaType: MetaKeySignature, Data: []byte{byte(int8(fifths)), mi}}
}

// KeySignature reads a key signature meta.
func (e Event) KeySignature() (fifths int, minor, ok bool) {
	if e.Kind != Meta || e.MetaType != MetaKeySignature || len(e.Data) < 2 {
		return 0, false, false
	}
	f := int(int8(e.Data[0]))
	if f < -7 || f > 7 {
		return 0, false, false
	}
	return f, e.Data[1] == 1, true
}

// Text is a text meta's text.
func (e Event) Text() string { return string(e.Data) }
