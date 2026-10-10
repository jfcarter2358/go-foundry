package setup

// graphics.go — whether the machine Setup just installed onto can run what it
// installed, and what to tell the player when it cannot.
//
// Every Foundry game and the editor need an OpenGL 4.3 core context. Windows
// itself only provides OpenGL 1.1 (its "GDI Generic" software renderer); a
// modern one comes with the graphics card maker's driver, which a fresh
// Windows install, a machine on Microsoft's Basic Display Adapter or a VM
// without 3D acceleration does not have. The game says so itself when it
// starts — but a player who reads it in the installer can fix it first.
//
// Setup cannot install the driver: which one depends on the hardware, it needs
// administrator rights, and it often wants a restart. It names the adapter and
// opens the maker's download page, which is as far as it can usefully go.

import (
	"fmt"
	"strings"
)

// The context every Foundry program asks for: glctx.MajorVersion and
// MinorVersion, which this package cannot import (Setup is built without cgo).
// glctx's tests hold the two together.
const (
	GLMajor = 4
	GLMinor = 3
)

// GraphicsInfo is what a probe of the machine's OpenGL found.
type GraphicsInfo struct {
	// Probed is false when no probe could run at all — nothing is known, and
	// nothing is said.
	Probed bool
	// CoreOK is whether a GLMajor.GLMinor core context could be made.
	CoreOK bool
	// Adapter is the display adapter Windows names, e.g. "NVIDIA GeForce RTX
	// 3060" or "Microsoft Basic Display Adapter".
	Adapter string
	// Vendor, Renderer and Version are glGetString's from a plain context.
	Vendor, Renderer, Version string
	// Err is why the probe could not make any context.
	Err string
}

// Driver download pages, by the maker named in the adapter.
var driverPages = []struct {
	match []string
	url   string
}{
	{[]string{"nvidia", "geforce", "quadro", "rtx"}, "https://www.nvidia.com/Download/index.aspx"},
	{[]string{"amd", "radeon", "ati "}, "https://www.amd.com/en/support/download/drivers.html"},
	{[]string{"intel"}, "https://www.intel.com/content/www/us/en/support/detect.html"},
}

// GraphicsWarning is what to tell the player about info, and the driver page
// to offer, or "" when there is nothing to say.
func GraphicsWarning(title string, info GraphicsInfo) (msg, url string) {
	if !info.Probed || info.CoreOK {
		return "", ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s needs OpenGL %d.%d, and this PC's graphics driver does not provide it, so %s will not start yet.",
		title, GLMajor, GLMinor, title)
	b.WriteString("\n\n")
	if info.Adapter != "" {
		fmt.Fprintf(&b, "Graphics card: %s\n", info.Adapter)
	}
	switch {
	case info.Renderer != "":
		fmt.Fprintf(&b, "OpenGL: %s, version %s\n", info.Renderer, info.Version)
	case info.Err != "":
		fmt.Fprintf(&b, "OpenGL: unavailable (%s)\n", info.Err)
	}
	b.WriteString("\n")

	adapter := strings.ToLower(info.Adapter + " " + info.Vendor + " " + info.Renderer)
	for _, p := range driverPages {
		for _, m := range p.match {
			if strings.Contains(adapter, m) {
				url = p.url
				break
			}
		}
		if url != "" {
			break
		}
	}
	generic := strings.Contains(adapter, "basic display") || strings.Contains(adapter, "basic render") ||
		strings.Contains(adapter, "gdi generic")
	switch {
	case url != "":
		b.WriteString("Install the latest driver from the card's maker, restart, and it should run.")
	case generic:
		b.WriteString("Windows is using its built-in display driver, which has no modern OpenGL. " +
			"Install the driver from your graphics card's maker (NVIDIA, AMD or Intel; Windows Update's " +
			"optional driver updates may also have it), restart, and it should run. " +
			"On a virtual machine, enable 3D acceleration instead.")
	default:
		b.WriteString("Install the latest driver from your graphics card's maker (NVIDIA, AMD or Intel), restart, and it should run. " +
			"On a virtual machine or over remote desktop, enable 3D acceleration or run it on the machine itself.")
	}
	return b.String(), url
}

// warnAboutGraphics probes the machine and, when it falls short, tells the
// player and offers the driver page. Interactive installs only.
func warnAboutGraphics(h Host, title string) {
	msg, url := GraphicsWarning(title, h.ProbeGraphics())
	if msg == "" {
		return
	}
	if url == "" {
		h.Tell(msg, true)
		return
	}
	if h.Ask(msg+"\n\nOpen the driver download page now?", false) {
		if err := h.OpenURL(url); err != nil {
			h.Tell("Could not open "+url+": "+err.Error(), true)
		}
	}
}
