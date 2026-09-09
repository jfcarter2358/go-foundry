package platform

import "testing"

// The toggle has to work with no window, because that is the state every unit
// test and every headless tool runs in — and because ApplyVSync calling into
// SDL with no current GL context is exactly the crash that guard exists to
// avoid.
func TestToggleVSyncWithoutWindow(t *testing.T) {
	defer func(enabled bool, interval int, settable bool) {
		VSyncEnabled, VSyncInterval, VSyncSettable = enabled, interval, settable
	}(VSyncEnabled, VSyncInterval, VSyncSettable)

	Window, Context = nil, nil
	VSyncEnabled = true

	ToggleVSync()
	if VSyncEnabled {
		t.Fatal("ToggleVSync did not flip VSyncEnabled")
	}
	ToggleVSync()
	if !VSyncEnabled {
		t.Fatal("ToggleVSync did not flip VSyncEnabled back")
	}
}

// VSyncActive is what a readout shows, and it has to report the driver rather
// than the request — the two disagreeing (vsync forced on outside the program)
// is the case it exists for.
func TestVSyncActiveReportsTheDriverNotTheRequest(t *testing.T) {
	defer func(enabled bool, interval int) {
		VSyncEnabled, VSyncInterval = enabled, interval
	}(VSyncEnabled, VSyncInterval)

	VSyncEnabled = false
	VSyncInterval = 1
	if !VSyncActive() {
		t.Error("driver reports interval 1 but VSyncActive says swaps are not waiting")
	}
	// Adaptive: waits unless the frame is already late, so it is still a wait.
	VSyncInterval = -1
	if !VSyncActive() {
		t.Error("adaptive vsync (-1) should count as active")
	}
	VSyncEnabled = true
	VSyncInterval = 0
	if VSyncActive() {
		t.Error("driver reports interval 0 but VSyncActive says swaps are waiting")
	}
}
