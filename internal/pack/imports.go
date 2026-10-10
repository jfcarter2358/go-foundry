package pack

// imports.go — every DLL a packed Windows program loads is either part of
// Windows or packed beside it.
//
// The Windows loader resolves a program's imports before a single line of it
// runs, so a missing one is a program that cannot log, cannot show a message
// box, and — started from another Go program, whose error mode it inherits —
// cannot even get Windows to say which file it wanted. It just exits. Two
// releases went out like that: an editor that imported mingw's libgcc and
// libstdc++ DLLs from the machine that built it, and games built by an
// installed editor with no SDL2.dll in their project. Both are a pack-time
// error now.

import (
	"debug/pe"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// windowsSystemDLLs are the libraries every supported Windows has in
// System32, lowercased: what the Go runtime, SDL2 and the engine's own cgo
// import, and their near neighbours.
var windowsSystemDLLs = map[string]bool{
	"advapi32.dll": true, "bcrypt.dll": true, "cfgmgr32.dll": true, "comctl32.dll": true,
	"comdlg32.dll": true, "crypt32.dll": true, "d3d11.dll": true, "d3d9.dll": true,
	"dbghelp.dll": true, "dinput8.dll": true, "dsound.dll": true, "dwmapi.dll": true,
	"dxgi.dll": true, "gdi32.dll": true, "hid.dll": true, "imm32.dll": true,
	"iphlpapi.dll": true, "kernel32.dll": true, "msvcrt.dll": true, "mswsock.dll": true,
	"ntdll.dll": true, "ole32.dll": true, "oleaut32.dll": true, "opengl32.dll": true,
	"powrprof.dll": true, "rpcrt4.dll": true, "secur32.dll": true, "setupapi.dll": true,
	"shell32.dll": true, "shlwapi.dll": true, "ucrtbase.dll": true, "user32.dll": true,
	"userenv.dll": true, "uxtheme.dll": true, "version.dll": true, "winhttp.dll": true,
	"wininet.dll": true, "winmm.dll": true, "winspool.drv": true, "ws2_32.dll": true,
	"xinput1_3.dll": true, "xinput1_4.dll": true, "xinput9_1_0.dll": true,
}

// IsWindowsSystemDLL reports whether a DLL ships with Windows itself.
func IsWindowsSystemDLL(name string) bool {
	lower := strings.ToLower(name)
	return windowsSystemDLLs[lower] ||
		strings.HasPrefix(lower, "api-ms-win-") || strings.HasPrefix(lower, "ext-ms-")
}

// importedDLLs lists the DLLs a PE file imports. ok is false for a file that is
// not a PE image at all, which this check has nothing to say about. A variable
// so a test can stand in for a mingw cross-compiler.
var importedDLLs = peImports

func peImports(path string) (names []string, ok bool, err error) {
	f, err := pe.Open(path)
	if err != nil {
		return nil, false, nil
	}
	defer f.Close()
	// Not ImportedLibraries, which debug/pe leaves unimplemented (always nil).
	// Each symbol is "name:library".
	syms, err := f.ImportedSymbols()
	if err != nil {
		return nil, true, fmt.Errorf("reading the imports of %s: %w", filepath.Base(path), err)
	}
	seen := map[string]bool{}
	for _, s := range syms {
		i := strings.LastIndexByte(s, ':')
		if i < 0 || seen[s[i+1:]] {
			continue
		}
		seen[s[i+1:]] = true
		names = append(names, s[i+1:])
	}
	return names, true, nil
}

// MissingDLLs returns the DLLs that exe, or a DLL beside it that it loads,
// imports and that neither Windows nor exe's directory provides — sorted,
// as the import tables spell them.
func MissingDLLs(exe string) ([]string, error) {
	dir := filepath.Dir(exe)
	present := map[string]bool{}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				present[strings.ToLower(e.Name())] = true
			}
		}
	}
	missing := map[string]bool{}
	seen := map[string]bool{}
	queue := []string{exe}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		names, ok, err := importedDLLs(path)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for _, n := range names {
			lower := strings.ToLower(n)
			switch {
			case IsWindowsSystemDLL(lower):
			case present[lower]:
				if !seen[lower] {
					seen[lower] = true
					queue = append(queue, filepath.Join(dir, n))
				}
			default:
				missing[n] = true
			}
		}
	}
	out := make([]string, 0, len(missing))
	for n := range missing {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// supplyDLLs copies, from the first of dirs holding it, each DLL the packed
// program imports that is not in the tree yet. What none of them has is left
// for checkDLLs to report.
func (p *packer) supplyDLLs(exe string, dirs []string) error {
	missing, err := MissingDLLs(exe)
	if err != nil {
		return err
	}
	for _, name := range missing {
		for _, d := range dirs {
			if d == "" {
				continue
			}
			src := filepath.Join(d, name)
			if _, err := os.Stat(src); err != nil {
				continue
			}
			if err := p.copyTo(src, name); err != nil {
				return err
			}
			p.logf("%s: from %s", name, d)
			break
		}
	}
	return nil
}

// checkDLLs fails a Windows pack whose program would not load.
func checkDLLs(exe string, where string) error {
	missing, err := MissingDLLs(exe)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}
	for _, n := range missing {
		if lower := strings.ToLower(n); strings.HasPrefix(lower, "libgcc") ||
			strings.HasPrefix(lower, "libstdc++") || strings.HasPrefix(lower, "libwinpthread") {
			where = `mingw's runtime belongs inside the program: build it with -ldflags '-extldflags "-static-libgcc -static-libstdc++"' (the Taskfile's WINDOWS_STATIC).`
			break
		}
	}
	return fmt.Errorf("%s imports %s, which Windows does not provide and which is not in the package — "+
		"it would exit on a player's machine before it could log anything. %s",
		filepath.Base(exe), strings.Join(missing, ", "), where)
}
