package platform

// pad_hold.go — telling a pad button's hold from its press.
//
// A pad binding is a press ("Pad X") or a hold ("Hold Pad X"), and one button
// may carry one of each: reload on a tap of X, interact on holding it. That
// costs the press its immediacy, but only where a hold shares its input:
//
//	no hold on the input    the press acts as the button goes down, as ever
//	a hold on the input     the press waits; let go inside PadHoldTime and it
//	                        acts then, as a tap (down and straight back up)
//	held PadHoldTime        the hold acts, and stays down until the release;
//	                        the press never does
//
// The timing lives here, on the pad snapshot (AdvancePadHolds once a frame);
// which actions to fire is the app's (app/gamepad_input.go), and the level
// reads — Held, Value, action_down — follow the same rules (padLive).

// AdvancePadHolds counts dt onto every held pad input and returns the ones
// whose hold reached PadHoldTime in this call, in input order.
func AdvancePadHolds(dt float32) []PadInput {
	var out []PadInput
	for p := PadA; p < PadInputCount; p++ {
		if !pad.down[p] {
			continue
		}
		was := pad.heldFor[p]
		pad.heldFor[p] += dt
		if was < PadHoldTime && pad.heldFor[p] >= PadHoldTime {
			out = append(out, p)
		}
	}
	return out
}

// PadHeldFor is how long a pad input has been down, in seconds, as
// AdvancePadHolds counts it; 0 when it is up.
func PadHeldFor(p PadInput) float32 {
	if p == PadNone || p >= PadInputCount {
		return 0
	}
	return pad.heldFor[p]
}

// PadHoldReached reports whether a pad input is down and has been for
// PadHoldTime — a hold of it is live.
func PadHoldReached(p PadInput) bool {
	return PadHeld(p) && PadHeldFor(p) >= PadHoldTime
}
