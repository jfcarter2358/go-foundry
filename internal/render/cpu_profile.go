package render

// cpu_profile.go — a Go CPU profile of the running game, started and stopped
// from inside it (F2, or the developer console's `cpuprofile`).
//
// The headless tools (`foundry sim -cpuprofile`) profile a fresh load ticking
// on its own, which is not always the frame that is slow: a play session
// accumulates state — bodies, spawned things, whatever the fight left behind —
// that a scripted run never reaches. This records the frame as it is actually
// being played, editor play mode included.

import (
	"log"
	"os"
	"runtime/pprof"
	"sync"
	"time"

	"foundry/internal/fileutil"
)

// CPUProfileDefaultSeconds is how long a recording runs when nothing says.
const CPUProfileDefaultSeconds = 10.0

// cpuProf is the recording in progress, if any. The timer that ends it fires
// off the main thread, so everything is guarded; lastPath is what the overlay
// reports once a recording has finished.
var cpuProf struct {
	sync.Mutex
	file     *os.File
	timer    *time.Timer
	until    time.Time
	lastPath string
}

// StartCPUProfile begins recording to cpu-<time>.prof in the state directory,
// stopping by itself after secs. It refuses while one is already running.
func StartCPUProfile(secs float64) (string, error) {
	cpuProf.Lock()
	defer cpuProf.Unlock()
	if cpuProf.file != nil {
		return cpuProf.file.Name(), errCPUProfileRunning
	}
	path := fileutil.StatePath("cpu-" + time.Now().Format("20060102-150405") + ".prof")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	d := time.Duration(secs * float64(time.Second))
	cpuProf.file = f
	cpuProf.until = time.Now().Add(d)
	cpuProf.timer = time.AfterFunc(d, func() { StopCPUProfile() })
	log.Printf("cpuprofile: recording %.0fs to %s", secs, path)
	return path, nil
}

// StopCPUProfile ends the recording and returns its file's path, or "" when
// none was running.
func StopCPUProfile() string {
	cpuProf.Lock()
	defer cpuProf.Unlock()
	if cpuProf.file == nil {
		return ""
	}
	pprof.StopCPUProfile()
	cpuProf.timer.Stop()
	path := cpuProf.file.Name()
	cpuProf.file.Close()
	cpuProf.file, cpuProf.timer = nil, nil
	cpuProf.lastPath = path
	log.Printf("cpuprofile: written to %s", path)
	return path
}

// ToggleCPUProfile is F2: start a default-length recording, or end the one
// that is running.
func ToggleCPUProfile() {
	if StopCPUProfile() != "" {
		return
	}
	if _, err := StartCPUProfile(CPUProfileDefaultSeconds); err != nil {
		log.Printf("cpuprofile: %v", err)
	}
}

// cpuProfileStatus is the overlay's line: what is recording and for how much
// longer, or where the last recording went, or "" if there has been none.
func cpuProfileStatus() (recording bool, path string, left time.Duration) {
	cpuProf.Lock()
	defer cpuProf.Unlock()
	if cpuProf.file != nil {
		return true, cpuProf.file.Name(), time.Until(cpuProf.until)
	}
	return false, cpuProf.lastPath, 0
}

type cpuProfileError string

func (e cpuProfileError) Error() string { return string(e) }

const errCPUProfileRunning = cpuProfileError("already recording")
