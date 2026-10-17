package edit

import (
	"strings"
	"testing"

	"foundry/internal/score"
	"foundry/internal/tracker/fx"
)

func kinds(chain []score.FX) string {
	var k []string
	for _, f := range chain {
		k = append(k, f.Kind)
	}
	return strings.Join(k, ",")
}

func laneNames(l *score.Line) string {
	var n []string
	for _, ln := range l.Lanes {
		n = append(n, ln.Name)
	}
	return strings.Join(n, ",")
}

func TestEffectChains(t *testing.T) {
	var c []score.FX
	for _, k := range []fx.Kind{fx.KindEQ, fx.KindDelay, fx.KindReverb} {
		if _, err := AddEffect(&c, -1, score.NewFX(k, fx.Insert)); err != nil {
			t.Fatal(err)
		}
	}
	if at, _ := AddEffect(&c, 1, score.NewFX(fx.KindDrive, fx.Insert)); at != 1 || kinds(c) != "eq,drive,delay,reverb" {
		t.Errorf("insert at 1: %d %s", at, kinds(c))
	}
	if err := MoveEffect(&c, 0, 3); err != nil || kinds(c) != "drive,delay,reverb,eq" {
		t.Errorf("move first to last: %s", kinds(c))
	}
	if err := MoveEffect(&c, 3, 1); err != nil || kinds(c) != "drive,eq,delay,reverb" {
		t.Errorf("move last to 1: %s", kinds(c))
	}
	if err := RemoveEffect(&c, 2); err != nil || kinds(c) != "drive,eq,reverb" {
		t.Errorf("remove: %s", kinds(c))
	}
	if _, err := AddEffect(&c, 0, score.FX{Kind: "flanger"}); err == nil {
		t.Error("an unknown effect went in")
	}
	for len(c) < MaxEffects {
		AddEffect(&c, -1, score.NewFX(fx.KindEQ, fx.Insert))
	}
	if _, err := AddEffect(&c, -1, score.NewFX(fx.KindEQ, fx.Insert)); err == nil {
		t.Error("a full chain took another effect")
	}
	if RemoveEffect(&c, 99) == nil || MoveEffect(&c, 0, 99) == nil {
		t.Error("out of range")
	}
}

func TestLanesFollowTheirInserts(t *testing.T) {
	l := score.NewLine("A", "x", "treble")
	l.Inserts = []score.FX{score.NewFX(fx.KindEQ, fx.Insert), score.NewFX(fx.KindDelay, fx.Insert), score.NewFX(fx.KindEQ, fx.Insert)}
	pt := []score.LanePoint{{At: score.Pos{Bar: 1}, Value: 0}}
	l.Lanes = []score.Lane{{Name: "eq.low", Points: pt}, {Name: "eq#2.high", Points: pt}, {Name: "delay.mix", Points: pt}, {Name: "volume", Points: pt}}
	if !HasEffectLanes(l) {
		t.Fatal("HasEffectLanes")
	}
	// An EQ added first: the old first EQ is now the second.
	if _, err := AddInsert(l, 0, score.NewFX(fx.KindEQ, fx.Insert)); err != nil {
		t.Fatal(err)
	}
	if got := laneNames(l); got != "eq#2.low,eq#3.high,delay.mix,volume" {
		t.Errorf("after adding an EQ first: %s", got)
	}
	// Moving the delay to the front changes no EQ's number.
	if err := MoveInsert(l, 2, 0); err != nil || kinds(l.Inserts) != "delay,eq,eq,eq" {
		t.Fatalf("move: %s %v", kinds(l.Inserts), err)
	}
	if got := laneNames(l); got != "eq#2.low,eq#3.high,delay.mix,volume" {
		t.Errorf("after moving the delay: %s", got)
	}
	// Swapping the last two EQs swaps their lanes' numbers.
	if err := MoveInsert(l, 3, 2); err != nil {
		t.Fatal(err)
	}
	if got := laneNames(l); got != "eq#3.low,eq#2.high,delay.mix,volume" {
		t.Errorf("after swapping EQs: %s", got)
	}
	// Removing an EQ with a lane takes the lane; the others renumber.
	n, err := RemoveInsert(l, 2)
	if err != nil || n != 1 {
		t.Fatalf("remove: %d %v", n, err)
	}
	if got := laneNames(l); got != "eq#2.low,delay.mix,volume" {
		t.Errorf("after removing an EQ: %s", got)
	}
	n, _ = RemoveInsert(l, 1)
	if got := laneNames(l); n != 0 || got != "eq.low,delay.mix,volume" {
		t.Errorf("after removing the first EQ: %s (%d gone)", got, n)
	}
	// Every lane still points at an effect of its own kind.
	for _, ln := range l.Lanes {
		tg, err := score.ParseLaneTarget(ln.Name)
		if err == nil && tg.Kind == score.LaneFX && l.InsertSlot(tg) < 0 {
			t.Errorf("lane %s points at nothing", ln.Name)
		}
	}
}
