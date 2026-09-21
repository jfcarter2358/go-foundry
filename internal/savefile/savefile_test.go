package savefile

import (
	"errors"
	"testing"
	"time"

	"foundry/internal/fileutil"
)

func withTempState(t *testing.T) {
	t.Helper()
	prev := fileutil.UserDataRoot
	fileutil.UserDataRoot = t.TempDir()
	t.Cleanup(func() { fileutil.UserDataRoot = prev })
}

// A load screen reads each slot's header without its body, and lists the
// newest first.
func TestSlotsListNewestFirstFromTheirHeaders(t *testing.T) {
	withTempState(t)
	old := &Snapshot{Header: Header{Level: "level1", Saved: time.Unix(1000, 0)}}
	newer := &Snapshot{Header: Header{Level: "level2", Kind: KindCheckpoint, Checkpoint: "gate", Saved: time.Unix(2000, 0)}}
	if err := Write("slot1", old); err != nil {
		t.Fatal(err)
	}
	if err := Write(AutosaveSlot, newer); err != nil {
		t.Fatal(err)
	}

	slots := List()
	if len(slots) != 2 || slots[0].Name != AutosaveSlot || slots[1].Name != "slot1" {
		t.Fatalf("listed %+v, want autosave then slot1", slots)
	}
	h, err := ReadHeader(AutosaveSlot)
	if err != nil {
		t.Fatal(err)
	}
	if h.Level != "level2" || h.Kind != KindCheckpoint || h.Checkpoint != "gate" || h.Version != Version {
		t.Errorf("header came back as %+v", h)
	}
	s, err := Read("slot1")
	if err != nil || s.Level != "level1" {
		t.Errorf("read %+v, %v", s, err)
	}
}

func TestAnEmptySlotSaysSo(t *testing.T) {
	withTempState(t)
	if _, err := Read("slot9"); !errors.Is(err, ErrNoSave) {
		t.Errorf("reading an empty slot: %v, want ErrNoSave", err)
	}
	if _, err := ReadHeader("slot9"); !errors.Is(err, ErrNoSave) {
		t.Errorf("reading an empty slot's header: %v, want ErrNoSave", err)
	}
	if err := Delete("slot9"); err != nil {
		t.Errorf("deleting an empty slot: %v", err)
	}
}

// A slot name comes from a script, and a script does not get to write
// anywhere but the saves folder.
func TestASlotNameCannotBeAPath(t *testing.T) {
	withTempState(t)
	for _, bad := range []string{"", "../settings", "a/b", `a\b`, ".hidden"} {
		if err := Write(bad, &Snapshot{}); err == nil {
			t.Errorf("wrote a save to slot %q", bad)
		}
	}
}

// A save a checkpoint rides inside survives the file format with it.
func TestTheLastCheckpointRidesInsideASave(t *testing.T) {
	withTempState(t)
	s := &Snapshot{Header: Header{Level: "level1"}, LastCheckpoint: &Snapshot{
		Header: Header{Level: "level1", Kind: KindCheckpoint, Checkpoint: "lz"},
	}}
	if err := Write("slot1", s); err != nil {
		t.Fatal(err)
	}
	back, err := Read("slot1")
	if err != nil {
		t.Fatal(err)
	}
	if back.LastCheckpoint == nil || back.LastCheckpoint.Checkpoint != "lz" {
		t.Errorf("last checkpoint came back as %+v", back.LastCheckpoint)
	}
}
