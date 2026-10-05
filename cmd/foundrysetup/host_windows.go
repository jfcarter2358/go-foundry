//go:build windows

package main

// host_windows.go — setup.Host, on Windows, with nothing but the standard
// library: message boxes from user32, the registry from advapi32, the per-user
// folders from shell32, and shortcuts from the Windows Script Host by way of
// PowerShell, which is on every Windows this engine runs on.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"foundry/internal/setup"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")

	procMessageBoxW          = user32.NewProc("MessageBoxW")
	procRegCreateKeyExW      = advapi32.NewProc("RegCreateKeyExW")
	procRegSetValueExW       = advapi32.NewProc("RegSetValueExW")
	procRegDeleteTreeW       = advapi32.NewProc("RegDeleteTreeW")
	procSHGetKnownFolderPath = shell32.NewProc("SHGetKnownFolderPath")
	procCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")
)

const (
	mbOK            = 0x0
	mbYesNo         = 0x4
	mbRetryCancel   = 0x5
	mbIconError     = 0x10
	mbIconQuestion  = 0x20
	mbIconWarning   = 0x30
	mbIconInfo      = 0x40
	mbDefButton2    = 0x100
	mbSetForeground = 0x10000
	idYes           = 6
	idRetry         = 4

	errorFileNotFound     = syscall.Errno(2)
	errorSharingViolation = syscall.Errno(32)
	createNoWindow        = 0x08000000
)

// guid is a Windows GUID, laid out as SHGetKnownFolderPath reads one.
type guid struct {
	Data1        uint32
	Data2, Data3 uint16
	Data4        [8]byte
}

var (
	folderIDPrograms = guid{0xA77F5D77, 0x2E2B, 0x44C3, [8]byte{0xA6, 0xA2, 0xAB, 0xA6, 0x01, 0x05, 0x4A, 0x51}}
	folderIDDesktop  = guid{0xB4BFCC3A, 0xDB2C, 0x424C, [8]byte{0xB0, 0x29, 0x7F, 0xE9, 0x9A, 0x87, 0xC6, 0x41}}
	folderIDLocal    = guid{0xF1B32785, 0x6FBA, 0x4FCF, [8]byte{0x9D, 0x55, 0x7B, 0x8E, 0x7F, 0x15, 0x70, 0x91}}
	folderIDRoaming  = guid{0x3EB685DB, 0x65F9, 0x4CF6, [8]byte{0xA0, 0x3A, 0xE3, 0xEF, 0x65, 0x72, 0x9F, 0x3D}}
)

type host struct{}

func (host) messageBox(text string, flags uintptr) int {
	t, _ := syscall.UTF16PtrFromString(text)
	c, _ := syscall.UTF16PtrFromString("Setup")
	r, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), flags|mbSetForeground)
	return int(r)
}

func (h host) Ask(text string, defaultNo bool) bool {
	flags := uintptr(mbYesNo | mbIconQuestion)
	if defaultNo {
		flags |= mbDefButton2
	}
	return h.messageBox(text, flags) == idYes
}

func (h host) Tell(text string, isError bool) {
	flags := uintptr(mbOK | mbIconInfo)
	if isError {
		flags = mbOK | mbIconError
	}
	h.messageBox(text, flags)
}

func (h host) Retry(text string) bool {
	return h.messageBox(text, mbRetryCancel|mbIconWarning) == idRetry
}

// LocalAppData and AppData ask the shell rather than reading the environment:
// the environment is inherited, and whatever launched Setup may have changed it.
func (host) LocalAppData() string { return knownFolderOr(&folderIDLocal, os.Getenv("LOCALAPPDATA")) }
func (host) AppData() string      { return knownFolderOr(&folderIDRoaming, os.Getenv("APPDATA")) }

// Folder asks the shell too, and there it matters: a desktop moved into
// OneDrive is not under the profile any more, and only the shell knows.
func (host) Folder(which setup.KnownFolder) (string, error) {
	switch which {
	case setup.FolderPrograms:
		return knownFolder(&folderIDPrograms)
	case setup.FolderDesktop:
		return knownFolder(&folderIDDesktop)
	}
	return "", fmt.Errorf("unknown folder %d", which)
}

func knownFolderOr(id *guid, fallback string) string {
	if p, err := knownFolder(id); err == nil {
		return p
	}
	return fallback
}

func knownFolder(id *guid) (string, error) {
	var p *uint16
	r, _, _ := procSHGetKnownFolderPath.Call(uintptr(unsafe.Pointer(id)), 0, 0, uintptr(unsafe.Pointer(&p)))
	if r != 0 {
		return "", fmt.Errorf("SHGetKnownFolderPath: HRESULT 0x%08X", uint32(r))
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return utf16PtrToString(p), nil
}

func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	n := 0
	for ptr := unsafe.Pointer(p); *(*uint16)(ptr) != 0; n++ {
		ptr = unsafe.Add(ptr, 2)
	}
	return syscall.UTF16ToString(unsafe.Slice(p, n))
}

func (host) RegistryString(key, name string) (string, bool) {
	kp, err := syscall.UTF16PtrFromString(key)
	if err != nil {
		return "", false
	}
	var k syscall.Handle
	if syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, kp, 0, syscall.KEY_READ, &k) != nil {
		return "", false
	}
	defer syscall.RegCloseKey(k)
	np, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", false
	}
	var typ, size uint32
	if syscall.RegQueryValueEx(k, np, nil, &typ, nil, &size) != nil || typ != syscall.REG_SZ || size == 0 {
		return "", false
	}
	buf := make([]uint16, size/2+1)
	if syscall.RegQueryValueEx(k, np, nil, &typ, (*byte)(unsafe.Pointer(&buf[0])), &size) != nil {
		return "", false
	}
	return syscall.UTF16ToString(buf), true
}

func (host) SetRegistry(key string, values ...setup.RegistryValue) error {
	kp, err := syscall.UTF16PtrFromString(key)
	if err != nil {
		return err
	}
	var k syscall.Handle
	r, _, _ := procRegCreateKeyExW.Call(uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(kp)),
		0, 0, 0, uintptr(syscall.KEY_ALL_ACCESS), 0, uintptr(unsafe.Pointer(&k)), 0)
	if r != 0 {
		return fmt.Errorf("creating registry key %s: %w", key, syscall.Errno(r))
	}
	defer syscall.RegCloseKey(k)

	for _, v := range values {
		np, err := syscall.UTF16PtrFromString(v.Name)
		if err != nil {
			return err
		}
		var typ, size uintptr
		var data unsafe.Pointer
		if v.IsDWORD {
			d := v.DWORD
			typ, size, data = syscall.REG_DWORD, 4, unsafe.Pointer(&d)
		} else {
			s, err := syscall.UTF16FromString(v.Str)
			if err != nil {
				return err
			}
			typ, size, data = syscall.REG_SZ, uintptr(len(s)*2), unsafe.Pointer(&s[0])
		}
		r, _, _ := procRegSetValueExW.Call(uintptr(k), uintptr(unsafe.Pointer(np)), 0, typ, uintptr(data), size)
		if r != 0 {
			return fmt.Errorf("writing %s\\%s: %w", key, v.Name, syscall.Errno(r))
		}
	}
	return nil
}

func (host) DeleteRegistryKey(key string) error {
	kp, err := syscall.UTF16PtrFromString(key)
	if err != nil {
		return err
	}
	r, _, _ := procRegDeleteTreeW.Call(uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(kp)))
	if r != 0 && syscall.Errno(r) != errorFileNotFound {
		return fmt.Errorf("deleting registry key %s: %w", key, syscall.Errno(r))
	}
	return nil
}

// shortcutScript makes a .lnk with the Windows Script Host. Everything it needs
// arrives in the environment rather than in the script, so a title with a quote
// in it cannot break the quoting.
const shortcutScript = `$s = (New-Object -ComObject WScript.Shell).CreateShortcut($env:FOUNDRY_LINK); ` +
	`$s.TargetPath = $env:FOUNDRY_TARGET; $s.WorkingDirectory = $env:FOUNDRY_WORKDIR; ` +
	`$s.IconLocation = $env:FOUNDRY_TARGET + ',0'; $s.Save()`

func (host) CreateShortcut(link, target, workDir string) error {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", shortcutScript)
	cmd.Env = append(os.Environ(), "FOUNDRY_LINK="+link, "FOUNDRY_TARGET="+target, "FOUNDRY_WORKDIR="+workDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("shortcut %s: %v %s", filepath.Base(link), err, strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(link); err != nil {
		return fmt.Errorf("shortcut %s was not made", filepath.Base(link))
	}
	return nil
}

// InUse opens the file for writing, which Windows refuses with a sharing
// violation while the program is running. Nothing is written.
func (host) InUse(path string) bool {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return errors.Is(err, errorSharingViolation)
	}
	f.Close()
	return false
}

// Launch starts the program with Windows' default error mode rather than the
// one the Go runtime set for Setup, which silences the loader's "SDL2.dll was
// not found" dialog — a game missing a DLL would otherwise just not appear.
func (host) Launch(exe, dir string) error {
	cmd := exec.Command(exe)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createDefaultErrorMode}
	return cmd.Start()
}
