//go:build windows

package main

// graphics_windows.go — what OpenGL this machine offers, asked of the driver
// the way the game will ask it: a hidden window, a WGL context, and then the
// core context at the version the engine needs.
//
// The probe runs in a child process (`Setup.exe -probe-graphics`), never in
// Setup itself. A broken driver can fault inside wglCreateContext, and it must
// take down a throwaway process rather than an installer that has just
// written the game to disk.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"foundry/internal/setup"
)

var (
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	opengl32 = syscall.NewLazyDLL("opengl32.dll")

	procCreateWindowExW       = user32.NewProc("CreateWindowExW")
	procDestroyWindow         = user32.NewProc("DestroyWindow")
	procGetDC                 = user32.NewProc("GetDC")
	procReleaseDC             = user32.NewProc("ReleaseDC")
	procEnumDisplayDevicesW   = user32.NewProc("EnumDisplayDevicesW")
	procChoosePixelFormat     = gdi32.NewProc("ChoosePixelFormat")
	procSetPixelFormat        = gdi32.NewProc("SetPixelFormat")
	procWglCreateContext      = opengl32.NewProc("wglCreateContext")
	procWglMakeCurrent        = opengl32.NewProc("wglMakeCurrent")
	procWglDeleteContext      = opengl32.NewProc("wglDeleteContext")
	procWglGetProcAddress     = opengl32.NewProc("wglGetProcAddress")
	procGlGetString           = opengl32.NewProc("glGetString")
	procShellExecuteW         = shell32.NewProc("ShellExecuteW")
	createDefaultErrorMode    = uint32(0x04000000)
	displayDevicePrimary      = uint32(0x4)
	displayDeviceAttached     = uint32(0x1)
	glVendor, glRenderer      = uintptr(0x1F00), uintptr(0x1F01)
	glVersion                 = uintptr(0x1F02)
	wglContextMajorVersionARB = int32(0x2091)
	wglContextMinorVersionARB = int32(0x2092)
	wglContextProfileMaskARB  = int32(0x9126)
	wglContextCoreProfileBit  = int32(0x1)
)

type pixelFormatDescriptor struct {
	Size, Version                                  uint16
	Flags                                          uint32
	PixelType, ColorBits                           byte
	RedBits, RedShift, GreenBits, GreenShift       byte
	BlueBits, BlueShift, AlphaBits, AlphaShift     byte
	AccumBits, AccumRed, AccumGreen, AccumBlue     byte
	AccumAlpha, DepthBits, StencilBits, AuxBuffers byte
	LayerType, Reserved                            byte
	LayerMask, VisibleMask, DamageMask             uint32
}

type displayDevice struct {
	Cb           uint32
	DeviceName   [32]uint16
	DeviceString [128]uint16
	StateFlags   uint32
	DeviceID     [128]uint16
	DeviceKey    [128]uint16
}

// probeGraphicsFlag is how Setup runs itself as the probe.
const probeGraphicsFlag = "-probe-graphics"

// runGraphicsProbe is the child's whole job: probe, print JSON, exit.
func runGraphicsProbe() {
	_ = json.NewEncoder(os.Stdout).Encode(probeGraphics())
	os.Exit(0)
}

// ProbeGraphics runs the probe in a child process and reads its answer. A
// child that crashes, hangs or says nothing is reported as not probed: Setup
// warns only about what it actually saw.
func (host) ProbeGraphics() setup.GraphicsInfo {
	exe, err := os.Executable()
	if err != nil {
		return setup.GraphicsInfo{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, probeGraphicsFlag)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return setup.GraphicsInfo{}
	}
	var info setup.GraphicsInfo
	if json.Unmarshal(out, &info) != nil {
		return setup.GraphicsInfo{}
	}
	return info
}

func probeGraphics() setup.GraphicsInfo {
	runtime.LockOSThread()
	info := setup.GraphicsInfo{Probed: true, Adapter: primaryAdapter()}

	class, _ := syscall.UTF16PtrFromString("STATIC")
	title, _ := syscall.UTF16PtrFromString("foundry probe")
	const wsPopup = 0x80000000
	hwnd, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)),
		wsPopup, 0, 0, 16, 16, 0, 0, 0, 0)
	if hwnd == 0 {
		info.Err = "could not create a window"
		return info
	}
	defer procDestroyWindow.Call(hwnd)
	hdc, _, _ := procGetDC.Call(hwnd)
	if hdc == 0 {
		info.Err = "could not get a device context"
		return info
	}
	defer procReleaseDC.Call(hwnd, hdc)

	const pfdDrawToWindow, pfdSupportOpenGL, pfdDoubleBuffer = 0x4, 0x20, 0x1
	pfd := pixelFormatDescriptor{Version: 1, Flags: pfdDrawToWindow | pfdSupportOpenGL | pfdDoubleBuffer,
		ColorBits: 32, DepthBits: 24, StencilBits: 8}
	pfd.Size = uint16(unsafe.Sizeof(pfd))
	format, _, _ := procChoosePixelFormat.Call(hdc, uintptr(unsafe.Pointer(&pfd)))
	if format == 0 {
		info.Err = "no OpenGL pixel format"
		return info
	}
	if r, _, _ := procSetPixelFormat.Call(hdc, format, uintptr(unsafe.Pointer(&pfd))); r == 0 {
		info.Err = "could not set an OpenGL pixel format"
		return info
	}

	legacy, _, _ := procWglCreateContext.Call(hdc)
	if legacy == 0 {
		info.Err = "wglCreateContext failed"
		return info
	}
	defer procWglDeleteContext.Call(legacy)
	if r, _, _ := procWglMakeCurrent.Call(hdc, legacy); r == 0 {
		info.Err = "wglMakeCurrent failed"
		return info
	}
	info.Vendor = glString(glVendor)
	info.Renderer = glString(glRenderer)
	info.Version = glString(glVersion)

	name, _ := syscall.BytePtrFromString("wglCreateContextAttribsARB")
	create, _, _ := procWglGetProcAddress.Call(uintptr(unsafe.Pointer(name)))
	if create == 0 {
		// No way to ask for a core context at all: an OpenGL 1.1 driver.
		procWglMakeCurrent.Call(0, 0)
		return info
	}
	attribs := []int32{
		wglContextMajorVersionARB, setup.GLMajor,
		wglContextMinorVersionARB, setup.GLMinor,
		wglContextProfileMaskARB, wglContextCoreProfileBit,
		0,
	}
	core, _, _ := syscall.SyscallN(create, hdc, 0, uintptr(unsafe.Pointer(&attribs[0])))
	procWglMakeCurrent.Call(0, 0)
	if core != 0 {
		if r, _, _ := procWglMakeCurrent.Call(hdc, core); r != 0 {
			info.CoreOK = true
			procWglMakeCurrent.Call(0, 0)
		}
		procWglDeleteContext.Call(core)
	}
	return info
}

// glString reads glGetString's NUL-terminated answer.
func glString(name uintptr) string {
	r, _, _ := procGlGetString.Call(name)
	if r == 0 {
		return ""
	}
	p := *(*unsafe.Pointer)(unsafe.Pointer(&r))
	var b []byte
	for i := 0; i < 512; i++ {
		c := *(*byte)(unsafe.Add(p, i))
		if c == 0 {
			break
		}
		b = append(b, c)
	}
	return string(b)
}

// primaryAdapter names the display adapter driving the primary monitor, or
// the first one attached to the desktop.
func primaryAdapter() string {
	first := ""
	for i := uintptr(0); i < 16; i++ {
		var dd displayDevice
		dd.Cb = uint32(unsafe.Sizeof(dd))
		if r, _, _ := procEnumDisplayDevicesW.Call(0, i, uintptr(unsafe.Pointer(&dd)), 0); r == 0 {
			break
		}
		name := syscall.UTF16ToString(dd.DeviceString[:])
		if dd.StateFlags&displayDevicePrimary != 0 {
			return name
		}
		if first == "" && dd.StateFlags&displayDeviceAttached != 0 {
			first = name
		}
	}
	return first
}

func (host) OpenURL(url string) error {
	verb, _ := syscall.UTF16PtrFromString("open")
	u, err := syscall.UTF16PtrFromString(url)
	if err != nil {
		return err
	}
	const swShowNormal = 1
	r, _, _ := procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(u)), 0, 0, swShowNormal)
	if r <= 32 {
		return fmt.Errorf("ShellExecute returned %d", r)
	}
	return nil
}
