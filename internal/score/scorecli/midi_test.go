package scorecli

import (
	"bytes"
	"strings"
	"testing"

	"foundry/internal/mididev"
)

// `score midi` lists the ports, or says why there are none; a bad flag is
// a usage error.
func TestMIDIVerb(t *testing.T) {
	var out bytes.Buffer
	code := midiCmd(nil, &out)
	ports, err := mididev.List()
	switch {
	case err != nil:
		if code != 1 || !strings.Contains(out.String(), err.Error()) {
			t.Errorf("no MIDI here: code %d, %q", code, out.String())
		}
	case code != 0:
		t.Errorf("listing %d ports: code %d, %q", len(ports), code, out.String())
	default:
		for _, p := range ports {
			if !strings.Contains(out.String(), p.Name) {
				t.Errorf("port %q not listed: %q", p.Name, out.String())
			}
		}
	}
	out.Reset()
	if code := midiCmd([]string{"-nope"}, &out); code != 2 {
		t.Errorf("bad flag: code %d", code)
	}
}
