package editor

// editor_claude_sessions.go — Resume: picking up a conversation from before.
//
// The conversations are the CLI's, not the editor's, which is the point. A
// session started here can be resumed in a terminal and the other way round,
// because there is only one store and both are reading it.
//
// Resuming replaces the panel's process: --resume is a start-up flag, so the
// running session (if any) ends and a new one comes up on the chosen id. The
// transcript then starts empty — the CLI replays nothing on resume — which is
// why the picker shows what the conversation was about, and why the panel says
// so when it comes back.

import (
	"fmt"
	"time"

	"foundry/internal/claude"
	"foundry/internal/fileutil"
	"foundry/internal/imguibackend"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// claudeSessionListLimit is how many conversations the picker offers. Enough to
// reach back a few days of work; beyond that the CLI's own picker is better,
// because it can search.
const claudeSessionListLimit = 40

var (
	claudeShowSessions bool
	claudeSessionList  []claude.SessionInfo
)

// claudeOpenSessionPicker reads the store and opens the dialog.
func claudeOpenSessionPicker() {
	dir := fileutil.ProjectPath
	if dir == "" {
		dir = fileutil.ResourcesPath
	}
	claudeSessionList = claude.ListSessions(dir, claudeSessionListLimit)
	claudeShowSessions = true
}

// claudeDrawSessionPicker draws the dialog. Called from the panel's draw, inside
// its window.
func claudeDrawSessionPicker() {
	if !claudeShowSessions {
		return
	}
	imgui.OpenPopup("Resume a conversation##claudesessions")
	if !imgui.BeginPopupModalV("Resume a conversation##claudesessions", nil,
		imgui.WindowFlagsAlwaysAutoResize) {
		return
	}
	if len(claudeSessionList) == 0 {
		editorTextMuted("No earlier conversations for this project.")
	} else {
		editorTextMuted("Conversations Claude Code has stored for this project, newest first.")
		imgui.Spacing()
		if imgui.BeginChildV("##claudesessionlist", imgui.Vec2{X: 720, Y: 360}, true, 0) {
			for _, s := range claudeSessionList {
				title := s.Title
				if title == "" {
					title = "(no opening message)"
				}
				label := fmt.Sprintf("%s##%s", title, s.ID)
				if imguibackend.SelectableV(label, false, 0, imgui.Vec2{}) {
					claudeResume(s.ID)
				}
				imgui.SameLine()
				editorTextMuted(claudeWhen(s.Modified))
			}
		}
		imgui.EndChild()
	}
	imgui.Spacing()
	if imgui.Button("Cancel##claudesessionscancel") {
		claudeShowSessions = false
		imgui.CloseCurrentPopup()
	}
	imgui.EndPopup()
}

// claudeResume restarts the panel on an earlier conversation.
func claudeResume(id string) {
	EndClaudeSession()
	claudeState.resume = id
	claudeState.expanded = map[string]bool{}
	claudeShowSessions = false
	imgui.CloseCurrentPopup()
	if claudeStart() {
		claudeSetStatus("Resumed. Claude remembers this conversation; the panel shows it from here on.")
	}
}

// claudeWhen is a timestamp as an age, which is how anybody looking for
// yesterday's conversation actually thinks about it.
func claudeWhen(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
	return t.Format("2 Jan")
}
