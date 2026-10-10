package hooks

// event_capture.go — holding events back while the entity update runs in
// parallel.
//
// The entity update steps bodies on several goroutines at once
// (collision/entity_update.go). Stepping a body can record animation events (a
// clip wrapping or ending, a step clip starting) and waypoint arrivals, and the
// buffers those go into are plain slices the script tick drains in order: the
// order events arrive in is the order handlers run in, and a serial update
// produced them entity by entity, in level order.
//
// So for the length of the parallel phase, recording is diverted into a
// capture list instead — under a lock, since several goroutines append to it —
// and when the phase is over the caller puts the captured events back into the
// real buffers in level order (EndEventCapture hands them back in capture
// order; the caller sorts them by entity, stably, which is exactly the serial
// order, because each entity's events were recorded by one goroutine in its
// own program order).
//
// The editor gate is applied at record time either way, so a captured event is
// one the serial update would have kept.

import "sync"

var capture struct {
	on   bool
	mu   sync.Mutex
	anim []AnimEvent
	wp   []WaypointEvent
}

// BeginEventCapture diverts animation and waypoint events into the capture
// list until EndEventCapture. Call it from the frame loop's goroutine before
// starting the workers that will record.
func BeginEventCapture() {
	capture.on = true
	capture.anim = capture.anim[:0]
	capture.wp = capture.wp[:0]
}

// EndEventCapture stops diverting and returns what was captured, in the order
// it was recorded. The slices are the capture's own, valid until the next
// BeginEventCapture. Call it after every worker has finished.
func EndEventCapture() (anim []AnimEvent, wp []WaypointEvent) {
	capture.on = false
	return capture.anim, capture.wp
}

// captureAnim and captureWaypoint report whether the event was captured.
func captureAnim(ev AnimEvent) bool {
	if !capture.on {
		return false
	}
	capture.mu.Lock()
	capture.anim = append(capture.anim, ev)
	capture.mu.Unlock()
	return true
}

func captureWaypoint(ev WaypointEvent) bool {
	if !capture.on {
		return false
	}
	capture.mu.Lock()
	capture.wp = append(capture.wp, ev)
	capture.mu.Unlock()
	return true
}
