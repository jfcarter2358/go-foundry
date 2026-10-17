package mididev

import (
	"errors"
	"testing"
	"time"
)

func TestParserRunningStatusAndRealTime(t *testing.T) {
	at := time.Unix(100, 0)
	var p Parser
	// A note on, two more by running status, a clock byte inside the
	// third, a note off by velocity 0, a program change, a sysex block that
	// must vanish whole, then a control change.
	b := []byte{
		0x90, 60, 100, 64, 90, 67, 0xF8, 80,
		67, 0,
		0xC1, 5,
		0xF0, 0x7E, 0x01, 0x02, 0xF7,
		0xB0, 64, 127,
	}
	ms := p.Feed(nil, b, at)
	want := []Message{
		{Status: 0x90, Data1: 60, Data2: 100}, {Status: 0x90, Data1: 64, Data2: 90}, {Status: 0x90, Data1: 67, Data2: 80},
		{Status: 0x90, Data1: 67, Data2: 0}, {Status: 0xC1, Data1: 5}, {Status: 0xB0, Data1: 64, Data2: 127},
	}
	if len(ms) != len(want) {
		t.Fatalf("got %d messages %v, want %d", len(ms), ms, len(want))
	}
	for i := range want {
		want[i].Time = at
		if ms[i] != want[i] {
			t.Errorf("message %d = %+v, want %+v", i, ms[i], want[i])
		}
	}
	if !ms[3].IsNoteOff() || ms[3].IsNoteOn() {
		t.Error("a note on at velocity 0 is a note off")
	}
	if ms[4].Channel() != 1 || ms[4].Kind() != ProgramChange {
		t.Errorf("program change on channel 2: %v", ms[4])
	}
	// A message split across two feeds still comes out whole.
	ms = p.Feed(nil, []byte{0x80, 60}, at)
	ms = p.Feed(ms, []byte{40}, at)
	if len(ms) != 1 || !ms[0].IsNoteOff() || ms[0].Note() != 60 {
		t.Fatalf("split message: %v", ms)
	}
	// Data with no status before it is dropped.
	var q Parser
	if ms := q.Feed(nil, []byte{60, 100}, at); len(ms) != 0 {
		t.Errorf("data with no status: %v", ms)
	}
}

func TestUnpackAndBend(t *testing.T) {
	at := time.Now()
	m, ok := Unpack(0x007F3C90, at)
	if !ok || !m.IsNoteOn() || m.Note() != 60 || m.Velocity() != 127 {
		t.Fatalf("note on: %v %v", m, ok)
	}
	m, ok = Unpack(0x00000AC3, at)
	if !ok || m.Kind() != ProgramChange || m.Data1 != 10 || m.Data2 != 0 || m.Channel() != 3 {
		t.Fatalf("program change: %+v", m)
	}
	if _, ok := Unpack(0x000000F8, at); ok {
		t.Error("a clock byte is not passed on")
	}
	if _, ok := Unpack(0x00003C10, at); ok {
		t.Error("a packed message needs a status byte")
	}
	for _, c := range []struct {
		lsb, msb byte
		want     int
	}{{0, 0, -8192}, {0, 64, 0}, {127, 127, 8191}} {
		if got := (Message{Status: PitchBend, Data1: c.lsb, Data2: c.msb}).Bend(); got != c.want {
			t.Errorf("bend %d %d = %d, want %d", c.lsb, c.msb, got, c.want)
		}
	}
	if s := (Message{Status: 0x91, Data1: 62, Data2: 70}).String(); s != "note on 62 vel 70 ch 2" {
		t.Errorf("String = %q", s)
	}
}

func TestVirtualInput(t *testing.T) {
	in := Virtual("test keyboard")
	if ms, err := in.Poll(nil); err != nil || len(ms) != 0 {
		t.Fatalf("empty poll: %v %v", ms, err)
	}
	at := time.Unix(5, 0)
	in.Send(Message{Status: NoteOn, Data1: 60, Data2: 90, Time: at}, Message{Status: NoteOff, Data1: 60})
	ms, err := in.Poll(nil)
	if err != nil || len(ms) != 2 {
		t.Fatalf("poll: %v %v", ms, err)
	}
	if !ms[0].Time.Equal(at) || ms[1].Time.IsZero() {
		t.Errorf("times: %v %v", ms[0].Time, ms[1].Time)
	}
	if ms, _ := in.Poll(nil); len(ms) != 0 {
		t.Errorf("a second poll returns what is new only: %v", ms)
	}
	// The queue keeps the newest when nobody polls.
	for i := 0; i < queueMax+10; i++ {
		in.Send(Message{Status: NoteOn, Data1: byte(i % 128), Data2: 1})
	}
	ms, _ = in.Poll(nil)
	if len(ms) != queueMax || ms[len(ms)-1].Data1 != byte((queueMax+9)%128) {
		t.Errorf("overflow kept %d, last %d", len(ms), ms[len(ms)-1].Data1)
	}
	in.Close()
	in.Close()
	if _, err := in.Poll(nil); !errors.Is(err, ErrClosed) {
		t.Errorf("poll after close: %v", err)
	}
	in.Send(Message{Status: NoteOn, Data1: 1, Data2: 1})
	if ms, _ := in.Poll(nil); len(ms) != 0 {
		t.Errorf("a closed input takes nothing: %v", ms)
	}
}

func TestFindPort(t *testing.T) {
	ports := []Port{{ID: "20:0", Name: "Keystation 61: Port 1"}, {ID: "24:0", Name: "Keystation"}, {ID: "28:0", Name: "Midi Through: Port-0"}}
	if p, ok := FindPort(ports, "Keystation"); !ok || p.ID != "24:0" {
		t.Errorf("exact name wins: %v", p)
	}
	if p, ok := FindPort(ports, "Midi Through"); !ok || p.ID != "28:0" {
		t.Errorf("prefix: %v", p)
	}
	if _, ok := FindPort(ports, "Nope"); ok {
		t.Error("no such port")
	}
	if _, ok := FindPort(ports, ""); ok {
		t.Error("an empty name finds nothing")
	}
}

// TestListPorts asks the platform. A machine with no sequencer (a CI box,
// WSL) gives an error that says why; one with ports names each.
func TestListPorts(t *testing.T) {
	ports, err := List()
	if err != nil {
		t.Logf("no MIDI ports here: %v", err)
		if _, oerr := Open("1:0"); oerr == nil {
			t.Error("opening a port where listing fails should fail too")
		}
		return
	}
	for _, p := range ports {
		if p.ID == "" || p.Name == "" {
			t.Errorf("port with no ID or name: %+v", p)
		}
		t.Logf("port %s: %s", p.ID, p.Name)
	}
}
