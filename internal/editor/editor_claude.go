package editor

// editor_claude.go — the Claude Code panel's state and lifecycle.
//
// A window of its own, like Git and Release, and for the same reason: a turn
// takes as long as it takes, and the level has to stay in reach while it runs.
// It can also sit on a second monitor beside the thing being built, which is how
// most of the work with it actually goes.
//
// The window owns a claude.Session — one `claude` process, one conversation —
// and nothing else in the editor knows the process exists. What the rest of the
// editor sees is the files that change underneath it, which
// editor_claude_reload.go turns back into reloads.
//
// Everything expensive happens off the frame: the session's goroutines read the
// process, the window copies a Snapshot at the top of a draw and reads only
// that. The one thing the draw does do is answer permissions, which is a channel
// send and never blocks.

import (
	"fmt"
	"log"
	"strings"
	"time"

	"foundry/internal/claude"
	"foundry/internal/fileutil"
	"foundry/internal/uiwin"
)

const (
	claudeWindowW = 900
	claudeWindowH = 760
)

// claudeModels are the choices in the model picker. Aliases rather than full
// names, so the CLI resolves each to whatever is current and this list does not
// have to be maintained against model releases.
var claudeModels = []string{"default", "opus", "sonnet", "haiku"}

// claudeModes are the permission modes, with the label the panel shows.
var claudeModes = []struct{ Mode, Label, Tip string }{
	{"", "Ask", "Ask before every file change and command."},
	{"acceptEdits", "Accept edits", "File edits go through; commands still ask."},
	{"plan", "Plan", "Read and plan only — nothing is changed until you leave plan mode."},
}

// claudePanelState is everything the window keeps. One panel, one conversation:
// a second conversation is a second window's worth of machinery for a case that
// has not come up, and the sessions the CLI stores mean the old one is never
// lost.
type claudePanelState struct {
	win  *uiwin.UIWindow
	sess *claude.Session

	// What the controls edit.
	input    string
	model    string
	mode     string
	denyNote string

	// Where the CLI is, and what it said when asked.
	binary  string
	version string
	binErr  error

	// Drawing state.
	expanded map[string]bool // tool call id → showing its detail
	// showMore is how many rows beyond the default tail the transcript draws,
	// raised by the "show earlier" button.
	showMore    int
	scrollToEnd bool
	lastSeq     uint64
	status      string
	statusAt    time.Time
	// noContext switches off the note about what the editor is looking at.
	// Inverted so that the zero value includes it, which is what it should do.
	noContext bool
	// resume is the session id to continue on the next start, set by the
	// session picker.
	resume string
}

var claudeState claudePanelState

// claudeTailRows is how much of a conversation is drawn without being asked for.
// Enough to cover the exchange in hand several times over; a day's work is not.
const claudeTailRows = 120

// shown is how many rows of the transcript to draw.
func (s *claudePanelState) shown() int { return claudeTailRows + s.showMore }

// ClaudeWindowOpen reports whether the panel is up.
func ClaudeWindowOpen() bool { return claudeState.win != nil }

// OpenClaudeWindow opens the panel, or raises it if it is already up.
func OpenClaudeWindow() {
	if claudeState.win != nil {
		claudeState.win.Win.Raise()
		return
	}
	if claudeState.expanded == nil {
		claudeState.expanded = map[string]bool{}
	}
	claudeFindCLI()
	w, err := uiwin.NewUIWindow("Claude Code", claudeWindowW, claudeWindowH,
		drawClaudeWindow, onClaudeWindowClose)
	if err != nil {
		log.Printf("open Claude Code window: %v", err)
		return
	}
	w.ClearColor = [4]float32{0.09, 0.09, 0.11, 1}
	claudeState.win = w
}

// CloseClaudeWindow asks for the window to go away at the end of the frame.
func CloseClaudeWindow() {
	if claudeState.win != nil {
		claudeState.win.CloseRequested = true
	}
}

// onClaudeWindowClose runs with this window's GL context current.
//
// The session is deliberately left running. Closing the window is not ending the
// conversation — the turn in flight is usually the reason the window was in the
// way — and reopening finds it where it was. EndClaudeSession is the explicit
// way to stop, and editor shutdown calls it.
func onClaudeWindowClose() {
	claudeState.win = nil
}

// EndClaudeSession stops the conversation and its process. Called when the
// editor shuts down, as CancelGitOperation and CancelReleaseBuild are.
func EndClaudeSession() {
	if claudeState.sess != nil {
		claudeState.sess.Close()
		claudeState.sess = nil
	}
}

// ClaudeSessionRunning reports whether a conversation is live, so the editor can
// say so before quitting.
func ClaudeSessionRunning() bool {
	return claudeState.sess != nil && claudeState.sess.Running()
}

// claudeFindCLI locates the binary and checks its version. Cheap enough to do on
// open, and doing it then means the panel can say what is wrong before anything
// is typed rather than after.
func claudeFindCLI() {
	bin, err := claude.Locate(claudeConfiguredBinary())
	if err != nil {
		claudeState.binary, claudeState.version, claudeState.binErr = "", "", err
		return
	}
	version, verr := claude.CheckVersion(bin)
	claudeState.binary, claudeState.version, claudeState.binErr = bin, version, verr
}

// claudeConfiguredBinary is the path from the project's settings, if one is set.
// Empty means search.
func claudeConfiguredBinary() string { return claudeSettings().Binary }

// claudeStart brings a session up. Called on the first send rather than on open,
// so opening the panel to read an old conversation costs nothing.
func claudeStart() bool {
	if claudeState.sess != nil {
		if claudeState.sess.Running() {
			return true
		}
		// A session whose process has gone still holds pipes and goroutines.
		// Starting a second one beside it would leak them for as long as the
		// editor is up.
		claudeState.sess.Close()
		claudeState.sess = nil
	}
	if claudeState.binErr != nil || claudeState.binary == "" {
		return false
	}
	dir := fileutil.ProjectPath
	if dir == "" {
		dir = fileutil.ResourcesPath
	}
	opts := claude.Options{
		Binary:         claudeState.binary,
		Dir:            dir,
		Model:          claudeModelArg(),
		PermissionMode: claudeState.mode,
		Resume:         claudeState.resume,
		AppendSystem:   claudePrimer(),
		AutoAllow:      claudeAutoAllow,
	}
	if cfg, err := claudeMCPConfig(); err == nil && cfg != "" {
		opts.MCPConfig = cfg
	} else if err != nil {
		log.Printf("claude: the editor's own tools are unavailable: %v", err)
	}
	sess, err := claude.Start(opts)
	if err != nil {
		claudeSetStatus("Could not start Claude Code: %v", err)
		return false
	}
	claudeState.sess = sess
	claudeState.resume = ""
	claudeState.scrollToEnd = true
	return true
}

// claudeModelArg is the --model value, or "" for whatever the CLI defaults to.
func claudeModelArg() string {
	if claudeState.model == "" || claudeState.model == "default" {
		return ""
	}
	return claudeState.model
}

// claudeSend starts a turn, bringing a session up if this is the first one.
func claudeSend() {
	text := strings.TrimSpace(claudeState.input)
	if text == "" {
		return
	}
	if !claudeStart() {
		return
	}
	claudeState.sess.Send(claudeCompose(text))
	claudeState.input = ""
	claudeState.scrollToEnd = true
}

// claudeCompose is the message actually sent: what was typed, and what the
// editor is looking at.
//
// Claude reads files from disk and cannot see the editor's state otherwise, and
// "this brush" is how anyone actually refers to the thing under the cursor. The
// note goes first so that the instruction is the last thing read.
func claudeCompose(text string) string {
	// A mentioned region is sent whatever the context switch says: the user
	// named it in the message itself, which is not something to second-guess.
	hint := claudeContextVolumeHint(claudeMentionedContextVolumes(text))
	if hint != "" {
		text += "\n\n" + hint
	}
	if claudeState.noContext {
		return text
	}
	ctx := claudeContextNote()
	if ctx == "" {
		return text
	}
	return ctx + "\n\n" + text
}

// claudeStop interrupts the turn in flight.
func claudeStop() {
	if claudeState.sess != nil {
		claudeState.sess.Interrupt()
	}
}

// claudeNewConversation ends this one and starts fresh. The old one is still in
// the CLI's own session store and can be resumed from the picker.
func claudeNewConversation() {
	EndClaudeSession()
	claudeState.resume = ""
	claudeState.input = ""
	claudeState.expanded = map[string]bool{}
	claudeState.showMore = 0
	claudeSetStatus("New conversation.")
}

// claudeSetMode changes the permission mode. A live session is told directly, so
// the mode picker does not restart the process mid-conversation.
func claudeSetMode(mode string) {
	claudeState.mode = mode
	if claudeState.sess != nil && claudeState.sess.Running() {
		m := mode
		if m == "" {
			// The CLI's name for "ask about everything".
			m = "default"
		}
		claudeState.sess.SetPermissionMode(m)
	}
}

func claudeSetStatus(format string, args ...any) {
	claudeState.status = fmt.Sprintf(format, args...)
	claudeState.statusAt = time.Now()
}

// claudeSnapshot is the session as of this frame, or a zero snapshot when there
// is none yet.
func claudeSnapshot() claude.Snapshot {
	if claudeState.sess == nil {
		return claude.Snapshot{Status: claude.StatusIdle}
	}
	return claudeState.sess.Snapshot()
}
