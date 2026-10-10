package claude

// session.go — one conversation with the CLI, as a child process.
//
// Shaped like release.Job and the Git window's state: goroutines own the pipes,
// everything they learn goes into one struct behind a mutex, and the editor takes
// a Snapshot at the top of a frame and draws from that. Nothing here blocks a
// frame and nothing here touches ImGui.
//
// The process is long-lived: one `claude` per conversation, not one per turn.
// That matters because resuming a session costs a full re-read of the transcript,
// and because an interrupt has to reach a process that is still running.
//
// Ordering is the subtle part. The CLI sends a can_use_tool control request and
// then *waits* for the answer before the tool runs, so a permission left
// unanswered stalls the turn — which is fine and is exactly what the UI wants,
// but it means answers must never be written from the goroutine that reads
// stdout, or a slow write would deadlock against a full pipe. Writes go through
// a channel to one writer goroutine instead.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// Status is what the session is doing.
type Status int

const (
	// StatusIdle is a live process with no turn in flight.
	StatusIdle Status = iota
	// StatusWorking is a turn in flight.
	StatusWorking
	// StatusWaiting is a turn stalled on a permission the user has not answered.
	StatusWaiting
	// StatusExited is a process that has stopped, cleanly or not.
	StatusExited
)

func (s Status) String() string {
	switch s {
	case StatusIdle:
		return "Ready"
	case StatusWorking:
		return "Working"
	case StatusWaiting:
		return "Needs permission"
	default:
		return "Stopped"
	}
}

// EntryKind is what one row of the transcript is.
type EntryKind int

const (
	EntryUser EntryKind = iota
	EntryAssistant
	EntryThinking
	EntryTool
	EntryPermission
	EntryNotice
)

// Entry is one row of the transcript.
type Entry struct {
	Kind EntryKind
	Text string
	At   time.Time
	// Live marks text still streaming in, so the UI can draw a cursor and know
	// the row will be replaced when the finished message arrives.
	Live bool
	Tool *ToolCall
	Perm *Permission
}

// ToolCall is a tool the assistant used, and how it went. The result is filled
// in later, when the tool_result comes back, so the UI shows the call as soon as
// it is made rather than when it finishes.
type ToolCall struct {
	ID        string
	Name      string
	Summary   string // one line: the file, the command, the pattern
	InputJSON string
	Result    string
	IsError   bool
	Done      bool
	At        time.Time
}

// Decision is an answer to a permission request.
type Decision int

const (
	DecisionAllow Decision = iota
	// DecisionAllowAlways allows the call and hands the CLI's own suggestions
	// back, which is what stops it asking for that kind of call again.
	DecisionAllowAlways
	DecisionDeny
)

// Permission is a pending or answered permission request.
type Permission struct {
	RequestID   string
	ToolName    string
	DisplayName string
	Description string
	InputJSON   string
	Suggestions []json.RawMessage
	At          time.Time

	Answered bool
	Decision Decision
	// AlwaysLabel is what an "always allow" button should say, taken from the
	// CLI's suggestion. Empty when it offered none, and the button is then hidden
	// rather than guessed at.
	AlwaysLabel string
}

// Options is how a session is started.
type Options struct {
	Binary string // required: the CLI, from Locate
	Dir    string // working directory: the project root

	Model          string // "", "sonnet", "opus", a full name
	PermissionMode string // "", "acceptEdits", "plan", …
	// AppendSystem is extra system prompt — the Foundry primer. Start writes it
	// to a file and passes the path rather than the text: a command-line
	// argument is limited to 8191 characters on Windows, and a `.cmd` shim (what
	// an npm install leaves on PATH there) re-parses the command line with
	// cmd.exe's own unquoting rules, which Go explicitly does not produce. A
	// multi-line argument through one of those arrives mangled.
	AppendSystem string
	// appendSystemFile is where Start put it.
	appendSystemFile string
	MCPConfig        string // path to an --mcp-config file
	StrictMCP        bool   // ignore every MCP server but ours
	Resume           string // a session id to continue
	Env              []string

	// AutoAllow answers a permission without asking the user. The panel uses it
	// for the editor's own MCP tools: they are the editor's own code, reached
	// through a socket only this process knows about, and prompting for them
	// would make the integration useless.
	AutoAllow func(toolName string) bool
}

// Args builds the command line. Pure, so a test can check it without a binary.
func Args(o Options) []string {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		// Permission prompts come to us over stdio as control requests. Without
		// this the CLI has no one to ask and denies anything that would prompt.
		"--permission-prompt-tool", "stdio",
	}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.PermissionMode != "" {
		args = append(args, "--permission-mode", o.PermissionMode)
	}
	if o.appendSystemFile != "" {
		args = append(args, "--append-system-prompt-file", o.appendSystemFile)
	}
	if o.MCPConfig != "" {
		args = append(args, "--mcp-config", o.MCPConfig)
		if o.StrictMCP {
			args = append(args, "--strict-mcp-config")
		}
	}
	if o.Resume != "" {
		args = append(args, "--resume", o.Resume)
	}
	return args
}

// Session is one running conversation.
type Session struct {
	opts Options
	cmd  *exec.Cmd
	out  chan []byte // to the writer goroutine
	done chan struct{}

	mu        sync.Mutex
	entries   []Entry
	status    Status
	sessionID string
	model     string
	version   string
	mcp       []mcpState
	cost      float64
	tokens    int
	err       error
	exited    bool
	stderr    []string
	pending   map[string]*Permission // unanswered, by request id
	tools     map[string]int         // tool_use id → index in entries
	// touched is every file a tool wrote, in order, waiting for the editor to
	// reload them. The editor drains it.
	touched []string
	// ranCommand records that a Bash call succeeded, which means files may have
	// changed in ways no tool input names.
	ranCommand bool
	// liveText is the index of the entry currently streaming, or -1.
	liveText int
	// seq counts changes, so a UI can tell "nothing happened" cheaply.
	seq uint64

	writeOnce sync.Once
}

// Snapshot is the session as of one moment: what the UI draws from.
type Snapshot struct {
	Entries   []Entry
	Status    Status
	SessionID string
	Model     string
	Version   string
	Cost      float64
	Tokens    int
	Err       error
	Pending   []*Permission
	Seq       uint64
}

// Start launches the CLI and performs the initialize handshake.
func Start(o Options) (*Session, error) {
	if o.Binary == "" {
		return nil, errors.New("no Claude Code binary")
	}
	if o.AppendSystem != "" {
		f, err := os.CreateTemp("", "foundry-primer-*.md")
		if err != nil {
			return nil, fmt.Errorf("write the system prompt: %w", err)
		}
		if _, err := f.WriteString(o.AppendSystem); err != nil {
			f.Close()
			os.Remove(f.Name())
			return nil, fmt.Errorf("write the system prompt: %w", err)
		}
		f.Close()
		o.appendSystemFile = f.Name()
	}
	cmd := exec.Command(o.Binary, Args(o)...)
	cmd.Dir = o.Dir
	if len(o.Env) > 0 {
		cmd.Env = append(processEnv(), o.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	isolateProcessTree(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", o.Binary, err)
	}
	s := &Session{
		opts:     o,
		cmd:      cmd,
		out:      make(chan []byte, 64),
		done:     make(chan struct{}),
		pending:  map[string]*Permission{},
		tools:    map[string]int{},
		liveText: -1,
		status:   StatusIdle,
	}
	go s.writeLoop(stdin)
	go s.readLoop(stdout)
	go s.readStderr(stderr)
	go s.wait()

	// The handshake. The CLI answers with its commands and agents; we do not need
	// the answer, but without the request it does not treat us as a host.
	s.sendControl("initialize", initializeRequest{Subtype: "initialize", Hooks: map[string]interface{}{}})
	return s, nil
}

// Send starts a turn.
func (s *Session) Send(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.mu.Lock()
	s.entries = append(s.entries, Entry{Kind: EntryUser, Text: text, At: time.Now()})
	s.status = StatusWorking
	s.seq++
	s.mu.Unlock()
	s.write(userInput{Type: "user", Message: userMessage{Role: "user", Content: text}})
}

// Interrupt stops the turn in flight. The CLI acknowledges and ends the turn with
// an error result, which the transcript shows as a notice.
func (s *Session) Interrupt() {
	s.sendControl("interrupt", interruptRequest{Subtype: "interrupt"})
}

// SetPermissionMode changes the mode without restarting the process.
func (s *Session) SetPermissionMode(mode string) {
	s.sendControl("set_mode", setPermissionModeRequest{Subtype: "set_permission_mode", Mode: mode})
}

// Answer resolves a pending permission request.
func (s *Session) Answer(requestID string, d Decision, denyMessage string) {
	s.mu.Lock()
	p := s.pending[requestID]
	if p == nil || p.Answered {
		s.mu.Unlock()
		return
	}
	p.Answered, p.Decision = true, d
	delete(s.pending, requestID)
	if len(s.pending) == 0 && s.status == StatusWaiting {
		s.status = StatusWorking
	}
	s.seq++
	input := p.InputJSON
	sugg := p.Suggestions
	s.mu.Unlock()

	var resp interface{}
	switch d {
	case DecisionDeny:
		if denyMessage == "" {
			denyMessage = "The editor's user declined this."
		}
		resp = permissionDeny{Behavior: "deny", Message: denyMessage}
	case DecisionAllowAlways:
		resp = permissionAllow{Behavior: "allow", UpdatedInput: json.RawMessage(input), UpdatedPermissions: sugg}
	default:
		resp = permissionAllow{Behavior: "allow", UpdatedInput: json.RawMessage(input)}
	}
	s.write(controlResponseOut{
		Type:     "control_response",
		Response: controlResponseIn{Subtype: "success", RequestID: requestID, Response: resp},
	})
}

// Snapshot copies the session for one frame of drawing.
//
// A deep copy, and it has to be. A tool row fills in when its result arrives and
// a permission row changes when it is answered, both from the goroutine reading
// the process — so handing the UI the same ToolCall and Permission the session is
// still writing would be a data race on every turn, read as a flicker at best
// and a torn string at worst. The copies are small and there are a few dozen of
// them; this is not a place worth being clever.
func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{
		Entries:   make([]Entry, len(s.entries)),
		Status:    s.status,
		SessionID: s.sessionID,
		Model:     s.model,
		Version:   s.version,
		Cost:      s.cost,
		Tokens:    s.tokens,
		Err:       s.err,
		Seq:       s.seq,
	}
	copy(snap.Entries, s.entries)
	for i := range snap.Entries {
		if c := snap.Entries[i].Tool; c != nil {
			dup := *c
			snap.Entries[i].Tool = &dup
		}
		if p := snap.Entries[i].Perm; p != nil {
			dup := *p
			snap.Entries[i].Perm = &dup
		}
	}
	for _, p := range s.pending {
		dup := *p
		snap.Pending = append(snap.Pending, &dup)
	}
	// Map order is random. Two prompts outstanding would otherwise swap places
	// between frames, under the mouse.
	sort.Slice(snap.Pending, func(i, j int) bool { return snap.Pending[i].At.Before(snap.Pending[j].At) })
	return snap
}

// DrainTouched takes the files tools have written since the last call, and
// whether a command ran that may have changed others. The editor uses it to
// reload what it is holding.
func (s *Session) DrainTouched() ([]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	paths, ran := s.touched, s.ranCommand
	s.touched, s.ranCommand = nil, false
	return paths, ran
}

// Stderr is what the process wrote to stderr — empty in a healthy session, and
// the first place to look when one dies on startup.
func (s *Session) Stderr() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.stderr...)
}

// Close ends the session. Safe to call twice.
func (s *Session) Close() {
	s.writeOnce.Do(func() { close(s.out) })
	select {
	case <-s.done:
		return
	case <-time.After(2 * time.Second):
		// It did not stop when its stdin closed; take the tree down. A `claude`
		// mid-turn holds an HTTP request open and will not notice EOF until it
		// finishes.
	}
	killProcessTree(s.cmd)
}

// Running reports whether the process is still up.
func (s *Session) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.exited
}

// --- plumbing ---------------------------------------------------------------

func (s *Session) write(v interface{}) {
	b, err := json.Marshal(v)
	if err != nil {
		s.notice("could not encode a message for Claude: " + err.Error())
		return
	}
	defer func() {
		// The writer channel is closed by Close; a send racing with that would
		// panic, and a session being torn down has nothing to say anyway.
		_ = recover()
	}()
	select {
	case s.out <- append(b, '\n'):
	case <-s.done:
	}
}

func (s *Session) sendControl(id string, req interface{}) {
	b, err := json.Marshal(req)
	if err != nil {
		return
	}
	s.write(controlRequestOut{
		Type:      "control_request",
		RequestID: fmt.Sprintf("foundry_%s_%d", id, time.Now().UnixNano()),
		Request:   b,
	})
}

func (s *Session) writeLoop(w io.WriteCloser) {
	defer w.Close()
	for b := range s.out {
		if _, err := w.Write(b); err != nil {
			return
		}
	}
}

// readLoop is the only goroutine that appends to the transcript.
//
// A bufio.Reader rather than a Scanner: one line carries a whole file when
// Claude writes one, and a Scanner's token limit would turn that into a silent
// truncation.
func (s *Session) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		b, err := readLine(br)
		if len(b) > 0 {
			s.handle(b)
		}
		if err != nil {
			return
		}
	}
}

// readLine reads one newline-terminated line however long it is.
func readLine(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		buf = append(buf, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return []byte(strings.TrimRight(string(buf), "\r\n")), err
	}
}

func (s *Session) readStderr(r io.Reader) {
	br := bufio.NewScanner(r)
	for br.Scan() {
		s.mu.Lock()
		if len(s.stderr) < 200 {
			s.stderr = append(s.stderr, br.Text())
		}
		s.mu.Unlock()
	}
}

func (s *Session) wait() {
	err := s.cmd.Wait()
	if s.opts.appendSystemFile != "" {
		_ = os.Remove(s.opts.appendSystemFile)
	}
	s.mu.Lock()
	s.exited = true
	s.status = StatusExited
	if err != nil && s.err == nil {
		s.err = fmt.Errorf("claude exited: %w", err)
	}
	// What it said on the way out. A CLI that refuses a flag, or is not logged
	// in, says so here and nowhere else — and without this the panel would show
	// nothing but "Not running", which is the least useful thing it could say.
	said := strings.TrimSpace(strings.Join(s.stderr, "\n"))
	s.seq++
	s.mu.Unlock()
	if said != "" {
		s.notice("Claude Code stopped: " + said)
	}
	close(s.done)
}

func (s *Session) notice(text string) {
	s.mu.Lock()
	s.entries = append(s.entries, Entry{Kind: EntryNotice, Text: text, At: time.Now()})
	s.seq++
	s.mu.Unlock()
}
