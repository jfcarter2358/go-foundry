package harness

// cmd_play.go — `foundry play`, the game played from a terminal.
//
// `foundry sim` runs a level and reports what moved; it cannot look around,
// walk through a door because the door is where it looks, or take a picture of
// what it found. This can, and it exists because the thing driving it is
// usually not a person: an agent working on a level needs to be able to stand
// in it, walk to the thing it just changed and SEE it, the way the author
// would, without a window or a mouse.
//
// Two shapes, because there are two questions:
//
//	foundry play run -level level1 -script 'look 90; forward 3; shot door.png'
//
// is one run from nothing, which is what a test or a one-off check wants: it
// loads, plays the script, prints what happened and exits.
//
//	foundry play serve -level level1 &
//	foundry play do 'to 12,2,-30; look_at #enemy; fire 0.5'
//	foundry play do 'shot'
//	foundry play stop
//
// is a session that STAYS: the level keeps ticking between commands, so the
// next thing to do can be decided from what the last one showed. That is what
// playing actually is, and it is the reason this is a server rather than a
// longer flag.
//
// The server holds the session on its own goroutine and never lets an HTTP
// handler touch it, because a drawing session's GL context is current on that
// goroutine's thread and on no other (see shot.go).

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"foundry/internal/gfx"
	"foundry/internal/play"
	"foundry/internal/render"
)

// PlayCommand is the verb: `foundry play`.
const PlayCommand = "play"

// defaultPlayPort is where a session listens and where the client looks. One
// well-known port rather than a discovery file: a session is a thing you start
// on purpose, and "address already in use" is a clearer answer than a stale
// file pointing at a process that died.
const defaultPlayPort = 7327

// PlayMain runs one of the play sub-commands.
func PlayMain(root string, args []string, out io.Writer) int {
	usage := func() {
		fmt.Fprintf(out, `foundry %s — play a level, from a script instead of a keyboard.

  run    -level <name> -script '<commands>'   play a script and exit
  serve  -level <name>                        keep a session up for `+"`do`"+` to drive
  do     '<commands>'                         play a script in the running session
  state                                       what the player can see and tell, in words
  shot   [file.png]                           a picture of what the player is looking at
  stop                                        end the running session
  commands                                    the command vocabulary

Run one with -h for its flags.
`, PlayCommand)
	}
	if len(args) == 0 {
		usage()
		return 2
	}
	kind, rest := args[0], args[1:]
	switch kind {
	case "run":
		return playRun(root, rest, out)
	case "serve":
		return playServe(root, rest, out)
	case "do":
		return playClient("do", rest, out)
	case "state":
		return playClient("state", rest, out)
	case "shot":
		return playClient("shot", rest, out)
	case "stop":
		return playClient("stop", rest, out)
	case "commands":
		fmt.Fprint(out, play.Usage())
		return 0
	}
	usage()
	return 2
}

// ---------------------------------------------------------------- one run

// playFlags are the flags both `run` and `serve` take: which level, how it is
// loaded, and whether the session can draw.
type playFlags struct {
	fs      *flag.FlagSet
	project *string
	res     *string
	level   *string
	compiled,
	god,
	draw *bool
	hz      *float64
	size    *string
	shots   *string
	menus   *string
	watch   *string
	enable  *string
	seconds *float64
	gi      *string
	view    *string
}

func playFlagSet(name, root string, out io.Writer) *playFlags {
	fs := flag.NewFlagSet(PlayCommand+" "+name, flag.ContinueOnError)
	fs.SetOutput(out)
	p := &playFlags{fs: fs}
	p.project = fs.String("root", root, "project directory: the one holding resources/")
	p.res = fs.String("resources", "", "the resources/ directory itself, if it is not <root>/resources")
	p.level = fs.String("level", "", "level name (required)")
	p.compiled = fs.Bool("compiled", false, "load the level's .fbsp, as a release build does, instead of its .yaml")
	p.god = fs.Bool("god", false, "keep the player alive: health held far over anything one tick can take. "+
		"Off by default — a play test is worth less when the thing being tested cannot hurt you, and it shows in the HUD")
	p.draw = fs.Bool("draw", true, "take pictures: needs a display and a GL 4.3 driver. -draw=false plays blind")
	p.hz = fs.Float64("hz", 60, "tick rate. A run is deterministic in it")
	p.size = fs.String("size", "1280x720", "the size of the pictures, and the window the HUD is laid out against")
	p.shots = fs.String("shots", "", "where pictures go (default: a foundry-play folder in the temporary directory)")
	p.menus = fs.String("menus", "answer", "what to do about a modal screen that comes up: `answer` clicks its initial_focus, `hold` leaves it up")
	p.watch = fs.String("watch", "", "comma-separated things to report whenever they change")
	p.enable = fs.String("enable", "", "chunks/volumes to switch on before play, comma separated")
	p.seconds = fs.Float64("seconds", 0, "let the level run this long before the script starts — time for a level's own opening to play out")
	p.gi = fs.String("gi", "", "live GI tier for the pictures: off, low, medium or high (default: picked by GPU, as a new player's is)")
	p.view = fs.String("view", "normal", "the debug view pictures are drawn in: normal, lighting (the light alone, on white surfaces), "+
		"probes, field or wireframe. A script's `view` command changes it mid-run")
	return p
}

// open turns the flags into a session.
func (p *playFlags) open(out io.Writer, onEvent func(Event)) (*Session, error) {
	if *p.level == "" {
		p.fs.Usage()
		return nil, errors.New("a level is required")
	}
	w, h, err := parseSize(*p.size)
	if err != nil {
		return nil, err
	}
	if *p.gi != "" {
		q, ok := gfx.ParseGIQuality(*p.gi)
		if !ok {
			return nil, fmt.Errorf("-gi %q: want off, low, medium or high", *p.gi)
		}
		gfx.GIQuality, gfx.GIQualityChosen = q, true
	}
	note, err := render.SetView(*p.view)
	if err != nil {
		return nil, fmt.Errorf("-view: %w", err)
	}
	if note != "" {
		fmt.Fprintf(out, "note: -view %s: %s\n", *p.view, note)
	}
	s, err := Open(Options{
		Resources: *p.res,
		Project:   *p.project,
		Level:     *p.level,
		Compiled:  *p.compiled,
		Hz:        *p.hz,
		God:       *p.god,
		Menus:     *p.menus,
		Draw:      *p.draw,
		ShotW:     w,
		ShotH:     h,
		ShotDir:   *p.shots,
		OnEvent:   onEvent,
	})
	if err != nil {
		return nil, err
	}
	s.Watch(parseWatches(*p.watch))
	for _, id := range splitList(*p.enable) {
		s.Enable(id)
	}
	// A level's opening — a dropship descending, a door cycling, a script
	// putting the player somewhere — happens whether or not anyone is playing,
	// and a script that starts before it has finished is testing the wrong
	// moment.
	if *p.seconds > 0 {
		s.RunFor(*p.seconds)
	}
	return s, nil
}

func playRun(root string, args []string, out io.Writer) int {
	p := playFlagSet("run", root, out)
	script := p.fs.String("script", "", "the commands to play, separated by ';' or newlines (see `foundry play commands`)")
	limit := p.fs.Float64("limit", 120, "give the script up after this many seconds of game time")
	state := p.fs.Bool("state", true, "print what the player can see when the script ends")
	if _, code, ok := parse(p.fs, args); !ok {
		return code
	}
	s, err := p.open(out, printer(out))
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	defer s.Close()

	shots, err := s.Play(*script, *limit)
	if err != nil {
		fmt.Fprintln(out, err)
		return 2
	}
	if *state {
		fmt.Fprintf(out, "\n%s", s.State())
	}
	for _, path := range shots {
		fmt.Fprintf(out, "picture: %s\n", path)
	}
	return 0
}

// ---------------------------------------------------------------- session

// playServer is a session and the queue of work to do to it. Every request
// hands a closure to the run loop rather than touching the session, which is
// what keeps all of it — including every GL call a picture makes — on the one
// goroutine the context is current on.
type playServer struct {
	s    *Session
	work chan func()
	done chan struct{}
	once sync.Once

	mu  sync.Mutex
	log []string // events since the last command drained them
}

func playServe(root string, args []string, out io.Writer) int {
	p := playFlagSet("serve", root, out)
	port := p.fs.Int("port", defaultPlayPort, "the port to listen on, for `foundry play do`")
	idle := p.fs.Duration("idle", time.Hour, "shut down after this long with no command")
	if _, code, ok := parse(p.fs, args); !ok {
		return code
	}

	srv := &playServer{work: make(chan func(), 8), done: make(chan struct{})}
	s, err := p.open(out, func(e Event) {
		printer(out)(e)
		srv.record(e)
	})
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	srv.s = s
	defer s.Close()

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		fmt.Fprintf(out, "%v\n(a play session may already be running — `foundry play stop` ends it)\n", err)
		return 1
	}
	go http.Serve(ln, srv.routes()) //nolint:errcheck // Serve ends when the listener closes
	go func() {
		<-srv.done
		ln.Close()
	}()

	fmt.Fprintf(out, "playing %s, listening on 127.0.0.1:%d\n"+
		"  foundry play do '<commands>'   foundry play state   foundry play stop\n\n", *p.level, *port)

	// The run loop: the session's own goroutine. It does the work handed to it
	// and, between commands, keeps the world ticking — a level with a fight in
	// it does not stand still while its player is thinking about the last
	// screenshot.
	//
	// At wall-clock pace, which is not what a headless run usually does and is
	// right here: the world between commands is the world a player would be
	// standing in, and a loop ticking flat out would put twenty minutes of
	// fighting into the ten seconds someone spent reading. The pace is
	// re-anchored after every command, because a command deliberately plays
	// game time as fast as the machine can and must not leave the loop asleep
	// paying it back.
	anchor, anchorT, last := time.Now(), srv.s.T, time.Now()
	for {
		select {
		case fn := <-srv.work:
			fn()
			anchor, anchorT, last = time.Now(), srv.s.T, time.Now()
			continue
		case <-srv.done:
			fmt.Fprintln(out, "\nstopped")
			return 0
		default:
		}
		if time.Since(last) > *idle {
			fmt.Fprintln(out, "\nidle, stopping")
			return 0
		}
		if srv.s.T-anchorT < time.Since(anchor).Seconds() {
			srv.s.Step()
			continue
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (p *playServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/do", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		limit, _ := strconv.ParseFloat(r.URL.Query().Get("limit"), 64)
		shot := r.URL.Query().Get("shot") != "0"
		p.run(w, func(out io.Writer) {
			shots, err := p.s.Play(string(body), limit)
			if err != nil {
				fmt.Fprintln(out, err)
				return
			}
			p.drain(out)
			fmt.Fprintf(out, "\n%s", p.s.State())
			if shot {
				if path, err := p.s.Shot(""); err == nil {
					shots = append(shots, path)
				}
			}
			for _, path := range shots {
				fmt.Fprintf(out, "picture: %s\n", path)
			}
		})
	})
	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		p.run(w, func(out io.Writer) {
			p.drain(out)
			fmt.Fprint(out, p.s.State())
		})
	})
	mux.HandleFunc("/shot", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		p.run(w, func(out io.Writer) {
			path, err := p.s.Shot(name)
			if err != nil {
				fmt.Fprintln(out, err)
				return
			}
			fmt.Fprintf(out, "picture: %s\n", path)
		})
	})
	mux.HandleFunc("/stop", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "stopping")
		p.once.Do(func() { close(p.done) })
	})
	return mux
}

// run hands a piece of work to the session's goroutine and writes what it
// printed back to the client. The handler itself touches nothing.
func (p *playServer) run(w http.ResponseWriter, fn func(io.Writer)) {
	var b strings.Builder
	finished := make(chan struct{})
	select {
	case p.work <- func() { fn(&b); close(finished) }:
	case <-p.done:
		http.Error(w, "the session is stopping", http.StatusGone)
		return
	}
	<-finished
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(w, b.String())
}

// record keeps an event for the next command to report. A session ticks
// between commands, so a trigger can fire while nobody is asking — and that is
// exactly the thing worth telling whoever asks next.
func (p *playServer) record(e Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.log) > 500 {
		p.log = p.log[len(p.log)-400:]
	}
	line := fmt.Sprintf("%7.2f  %s", e.T, strings.TrimSpace(e.Kind+" "+e.Name+" "+e.Value))
	p.log = append(p.log, line)
}

func (p *playServer) drain(out io.Writer) {
	p.mu.Lock()
	lines := p.log
	p.log = nil
	p.mu.Unlock()
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
}

// ---------------------------------------------------------------- client

func playClient(kind string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet(PlayCommand+" "+kind, flag.ContinueOnError)
	fs.SetOutput(out)
	port := fs.Int("port", defaultPlayPort, "the port the session is listening on")
	limit := fs.Float64("limit", 120, "give the script up after this many seconds of game time")
	shot := fs.Bool("shot", true, "take a picture when the script ends (`do` only)")
	rest, code, ok := parse(fs, args)
	if !ok {
		return code
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/%s", *port, kind)
	body := ""
	switch kind {
	case "do":
		body = strings.Join(rest, " ")
		if body == "" {
			fmt.Fprintf(out, "usage: foundry play do '<commands>'\n\n%s", play.Usage())
			return 2
		}
		if _, err := play.Parse(body); err != nil {
			// Checked here as well as in the session, so a typo costs a
			// round trip rather than a command played half way.
			fmt.Fprintln(out, err)
			return 2
		}
		url += fmt.Sprintf("?limit=%g&shot=%s", *limit, boolParam(*shot))
	case "shot":
		if len(rest) > 0 {
			url += "?name=" + rest[0]
		}
	}

	resp, err := http.Post(url, "text/plain", strings.NewReader(body))
	if err != nil {
		fmt.Fprintf(out, "no play session on 127.0.0.1:%d (%v)\n"+
			"start one with: foundry play serve -level <name> &\n", *port, err)
		return 1
	}
	defer resp.Body.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func boolParam(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
