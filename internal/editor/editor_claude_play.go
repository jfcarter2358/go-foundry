package editor

// editor_claude_play.go — letting Claude play the level in the editor's own
// window.
//
// The viewport screenshot (editor_claude_shot.go) let Claude see the level
// being built. This is the other half: standing in it. A tool call presses
// Play, drives the player through the frame loop for a few seconds — walking,
// looking, shooting, pressing use — and answers with what the player can see
// and a picture of it. The author watches it happen in the window, which is
// most of the point: a play test you can watch is one you can argue with.
//
// The vocabulary and the driving are internal/play, shared with the headless
// `foundry play`. What is here is the three things only the editor can do:
// getting into play mode, letting the frame loop pump the script, and taking
// the picture at the one moment in the frame when there is one to take.
//
// THE FRAME LOOP is why this is a driver rather than a loop. A tool runs on its
// own goroutine; the world only moves when app.Run ticks it. So the tool
// installs a runner and waits, and app.Run asks for its input once a frame
// (ClaudePlayInput) — which is also what makes the script play at the rate the
// author is watching rather than as fast as the machine can go.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"foundry/internal/claude"
	"foundry/internal/gfx"
	"foundry/internal/harness"
	"foundry/internal/play"
	"foundry/internal/render"
	"foundry/internal/sim"
	"foundry/internal/world"
)

// claudePlayMaxSeconds bounds a script however long its own commands add up
// to: a tool call that never returns is worse than one that gives up.
const claudePlayMaxSeconds = 120.0

var claudePlay struct {
	mu     sync.Mutex
	runner *play.Runner
	// done is closed when the script finishes, so the tool's goroutine can
	// wait on the frame loop rather than poll it.
	done chan struct{}
	// shot is a `shot` command waiting for the end of a frame, and where it
	// goes. The capture cannot happen inside the tick — the frame has not been
	// drawn yet — so it is taken by ClaudePlayCaptureIfRequested below.
	shot string
	// started is when Play was pressed, which is the clock the state report
	// is stamped with: one tool call's report should read as later than the
	// last one's.
	started time.Time
	// view is the debug view the author had when Play was pressed. A script's
	// `view` lasts the run and play_stop puts this back, so the author is not
	// left looking at a lighting-only editor they never asked for.
	view string
}

// claudePlayTarget is the editor as something a play script can drive: the open
// level, a log the tool answers with, and a screenshot that has to wait for the
// end of the frame.
type claudePlayTarget struct {
	mu  sync.Mutex
	log []string
}

func (t *claudePlayTarget) Level() *world.Level { return Editor.level }

func (t *claudePlayTarget) Emit(kind, name, value string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.log = append(t.log, strings.TrimSpace(kind+" "+name+" "+value))
}

func (t *claudePlayTarget) Read(path string) string {
	return harness.ReadPath(Editor.level, path)
}

// Shot queues a picture for the end of this frame. It answers the path before
// the file exists, which is honest enough: the frame it is taken from is the
// one now being drawn, a few milliseconds away.
func (t *claudePlayTarget) Shot(name string) (string, error) {
	if name == "" {
		name = fmt.Sprintf("play-%d.png", time.Now().Unix())
	}
	if !strings.HasSuffix(name, ".png") {
		name += ".png"
	}
	path := name
	if !filepath.IsAbs(name) {
		path = filepath.Join(os.TempDir(), "foundry-play", name)
	}
	claudePlay.mu.Lock()
	claudePlay.shot = path
	claudePlay.mu.Unlock()
	return path, nil
}

func (t *claudePlayTarget) lines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := append([]string(nil), t.log...)
	t.log = nil
	return out
}

// ClaudePlayInput is the frame loop asking whether a script is driving the
// player this frame, and what it is asking for. Called once a frame, before
// the world is stepped.
func ClaudePlayInput(dt float32) (sim.Input, bool) {
	claudePlay.mu.Lock()
	r, done := claudePlay.runner, claudePlay.done
	claudePlay.mu.Unlock()
	if r == nil {
		return sim.Input{}, false
	}
	in := r.Tick(float64(dt))
	if r.Done() {
		claudePlay.mu.Lock()
		if claudePlay.runner == r {
			claudePlay.runner = nil
			close(done)
		}
		claudePlay.mu.Unlock()
	}
	return in, true
}

// ClaudePlayCaptureIfRequested writes a picture a script's `shot` asked for.
// Called from the frame loop where the screenshot tool's capture is: after
// everything has been drawn, before the swap.
func ClaudePlayCaptureIfRequested() {
	claudePlay.mu.Lock()
	path := claudePlay.shot
	claudePlay.shot = ""
	claudePlay.mu.Unlock()
	if path == "" {
		return
	}
	png := claudeCaptureBackBuffer()
	if png == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, png, 0o644)
}

// claudePlayTools are the two tools: play a script, and stop playing.
func claudePlayTools() []claude.Tool {
	return []claude.Tool{
		{
			Name: "play",
			Description: "Play the open level in the editor, driving the player from a short " +
				"script: walk, look, shoot, press use, take a picture. Presses Play first if the " +
				"editor is not already playing. Use it to check that something actually works — " +
				"that the door opens, that the enemy notices, that the room looks right from " +
				"inside it — rather than reasoning about the level file. Answers with what the " +
				"player can see when the script ends, and a screenshot from their eyes. The " +
				"level keeps playing between calls, so several scripts in a row continue one " +
				"run; play_stop ends it and puts the editor back.\n\n" +
				play.Usage(),
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"script": map[string]any{
						"type": "string",
						"description": "The commands, separated by ';' or newlines. " +
							"For example: look 90 -10; forward 3; use; wait 1",
					},
				},
				"required": []any{"script"},
			},
			Run: func(args json.RawMessage) (claude.ToolResult, error) {
				var a struct {
					Script string `json:"script"`
				}
				if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Script) == "" {
					return claude.ToolResult{}, errors.New("a script is required")
				}
				return claudePlayScript(a.Script)
			},
		},
		{
			Name: "play_stop",
			Description: "Stop playing and put the editor back as it was. The level is restored " +
				"from the snapshot Play took, so nothing a script did to it while playing is kept.",
			Run: func(json.RawMessage) (claude.ToolResult, error) {
				text, err := onMainThread(claudePlayLeave)
				return claude.ToolResult{Text: text}, err
			},
		},
	}
}

// claudePlayScript runs one script and answers with the log, the state and a
// picture.
func claudePlayScript(script string) (claude.ToolResult, error) {
	cmds, err := play.Parse(script)
	if err != nil {
		return claude.ToolResult{Text: err.Error(), IsError: true}, nil
	}
	claudePlay.mu.Lock()
	busy := claudePlay.runner != nil
	claudePlay.mu.Unlock()
	if busy {
		return claude.ToolResult{}, errors.New("a script is already playing")
	}

	if msg, err := onMainThread(claudePlayEnter); err != nil {
		return claude.ToolResult{}, err
	} else if msg != "" {
		return claude.ToolResult{Text: msg, IsError: true}, nil
	}

	target := &claudePlayTarget{}
	done := make(chan struct{})
	claudePlay.mu.Lock()
	claudePlay.runner, claudePlay.done = play.New(target, cmds), done
	r := claudePlay.runner
	claudePlay.mu.Unlock()

	select {
	case <-done:
	case <-time.After(time.Duration(claudePlayMaxSeconds) * time.Second):
		claudePlay.mu.Lock()
		if claudePlay.runner == r {
			claudePlay.runner = nil
		}
		claudePlay.mu.Unlock()
		// Stop on the frame loop's thread: it releases the trigger and the
		// movement keys, which are the player's, not this goroutine's.
		_, _ = onMainThread(func() string { r.Stop(); return "" })
		target.Emit("TIMEOUT", "", fmt.Sprintf("stopped after %.0f seconds", claudePlayMaxSeconds))
	}

	text, err := onMainThread(func() string {
		return strings.Join(target.lines(), "\n") + "\n\n" +
			play.State(Editor.level, time.Since(claudePlay.started).Seconds())
	})
	if err != nil {
		return claude.ToolResult{}, err
	}
	png, shotErr := claudeRequestShot()
	if shotErr != nil {
		return claude.ToolResult{Text: text + "\n(no screenshot: " + shotErr.Error() + ")"}, nil
	}
	return claude.ToolResult{Text: text, Image: png, ImageMIME: "image/png"}, nil
}

// claudePlayEnter presses Play if the editor is not already playing, and
// answers a reason if it cannot. Runs on the main thread.
//
// The mouse is given back straight afterwards: Play grabs it for a person
// looking around with it, and a script does not need it — while an author
// whose cursor has just been captured by a tool call very much does.
func claudePlayEnter() string {
	if Editor.level == nil {
		return "no level is open"
	}
	if Editor.Active {
		EditorToggle()
		claudePlay.mu.Lock()
		claudePlay.started = time.Now()
		claudePlay.view = render.ViewName()
		claudePlay.mu.Unlock()
	}
	gfx.SetMouseLock(false)
	return ""
}

// claudePlayLeave goes back to the editor. Runs on the main thread.
func claudePlayLeave() string {
	claudePlay.mu.Lock()
	r := claudePlay.runner
	claudePlay.runner = nil
	view := claudePlay.view
	claudePlay.view = ""
	claudePlay.mu.Unlock()
	if r != nil {
		r.Stop()
	}
	if view != "" {
		_, _ = render.SetView(view)
	}
	if Editor.Active {
		return "the editor is not playing"
	}
	EditorToggle()
	return "stopped playing; the level is back as it was authored"
}
