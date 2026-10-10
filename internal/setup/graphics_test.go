package setup

import (
	"strings"
	"testing"
)

func TestGraphicsWarningNamesTheDriverToGet(t *testing.T) {
	for _, c := range []struct {
		name     string
		info     GraphicsInfo
		wantMsg  bool
		wantURL  string
		contains string
	}{
		{"not probed", GraphicsInfo{}, false, "", ""},
		{"fine", GraphicsInfo{Probed: true, CoreOK: true, Adapter: "NVIDIA GeForce RTX 3060"}, false, "", ""},
		{"old nvidia", GraphicsInfo{Probed: true, Adapter: "NVIDIA GeForce GT 610", Renderer: "GeForce GT 610/PCIe/SSE2", Version: "3.3.0"},
			true, "nvidia.com", "GeForce GT 610"},
		{"radeon", GraphicsInfo{Probed: true, Adapter: "AMD Radeon(TM) Graphics"}, true, "amd.com", ""},
		{"intel", GraphicsInfo{Probed: true, Adapter: "Intel(R) UHD Graphics 620"}, true, "intel.com", ""},
		{"basic display", GraphicsInfo{Probed: true, Adapter: "Microsoft Basic Display Adapter", Renderer: "GDI Generic", Version: "1.1.0"},
			true, "", "built-in display driver"},
		{"no context", GraphicsInfo{Probed: true, Err: "wglCreateContext failed"}, true, "", "unavailable (wglCreateContext failed)"},
	} {
		msg, url := GraphicsWarning("Test Game", c.info)
		if (msg != "") != c.wantMsg || !strings.Contains(url, c.wantURL) || (c.wantURL == "") != (url == "") {
			t.Errorf("%s: msg %q, url %q", c.name, msg, url)
		}
		if c.contains != "" && !strings.Contains(msg, c.contains) {
			t.Errorf("%s: %q does not mention %q", c.name, msg, c.contains)
		}
	}
}

// The player hears about a driver that cannot run the game before being asked
// to play it, and is offered the maker's download page.
func TestInstallWarnsAboutTheGraphicsDriver(t *testing.T) {
	h := newHost(t)
	h.graphics = GraphicsInfo{Probed: true, Adapter: "Intel(R) HD Graphics 3000", Version: "3.1.0"}
	in := openInstaller(t, makeInstaller(t, "Test Game", "1.0.0", gameFiles("intro.fbsp")))
	if err := Install(h, in, Options{}); err != nil {
		t.Fatal(err)
	}
	warned := -1
	for i, q := range h.asked {
		if strings.Contains(q, "OpenGL 4.3") {
			warned = i
		}
	}
	if warned < 0 || warned == len(h.asked)-1 {
		t.Errorf("asked %q: want the driver warning before the last question", h.asked)
	}
	if !strings.Contains(h.opened, "intel.com") {
		t.Errorf("opened %q, want Intel's driver page", h.opened)
	}

	silent := newHost(t)
	silent.graphics = h.graphics
	in = openInstaller(t, makeInstaller(t, "Test Game", "1.0.0", gameFiles("intro.fbsp")))
	if err := Install(silent, in, Options{Silent: true}); err != nil {
		t.Fatal(err)
	}
	if len(silent.asked)+len(silent.told) != 0 || silent.opened != "" {
		t.Errorf("a silent install spoke: asked %q, told %q", silent.asked, silent.told)
	}
}
