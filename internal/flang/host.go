package flang

// host.go — the one door between the language and the world.
//
// Everything flang can do to a game goes through this interface, and no Foundry
// type appears in it: an object is a RefVal, which is a path of stable names,
// and a value is a flang Value. That is what keeps this package buildable and
// testable with no window, no GL context and no level — mapHost (maphost.go) is
// a complete implementation in a few dozen lines, and the whole language can be
// exercised against it.
//
// The boundary erodes the first time someone reaches for an engine type here.
// It is worth defending: the alternative is a scripting language that can only
// be tested by launching a game.

import "fmt"

// Host is the world, as the language sees it.
//
// Every method must tolerate a ref that no longer resolves. The level is rebuilt
// wholesale by undo and by a reload, so a script holding a ref across a frame is
// the normal case rather than the exceptional one.
type Host interface {
	// Resolve turns an ID into a ref. The bool is false when nothing has that
	// name, which a script sees as null rather than as an error.
	Resolve(id string) (RefVal, bool)

	// ResolveTag returns every object carrying a tag, in a stable order.
	ResolveTag(tag string) []RefVal

	// Self is the object whose script is running.
	Self() RefVal

	// Alive reports whether a ref still names something.
	Alive(r RefVal) bool

	// Attr and SetAttr read and write user-defined attributes. Reading a name
	// that does not exist yields ok=false, which the interpreter turns into null.
	Attr(r RefVal, name string) (Value, bool)
	SetAttr(r RefVal, name string, v Value) error

	// Prop and SetProp read and write intrinsic engine fields. Unlike an
	// attribute, an unknown property is an error: the set is fixed and known, so
	// a name outside it is a typo.
	Prop(r RefVal, name string) (Value, bool)
	SetProp(r RefVal, name string, v Value) error

	// State returns an object's transient scratch dict, creating it if needed.
	// Never saved, dropped on reload.
	State(r RefVal) *DictVal

	// Param returns the value bound to a script parameter on the object whose
	// script is running — the level's answer to a top-level `param` declaration.
	//
	// The bool is false when nothing is bound, which is not an error: the
	// interpreter falls back to the declaration's default, and then to null.
	// A `ref` param may be answered with either the RefVal itself or the str
	// naming it, whichever the host finds cheaper; the interpreter resolves a
	// str through Resolve.
	Param(name string) (Value, bool)

	// Member resolves a structural step that is neither an attribute nor a
	// property — `@sec.floor`, `@sec.edge.<id>`, `ref.id`. The bool is false
	// when the step means nothing for that kind of object.
	Member(r RefVal, name string) (Value, bool)

	// Post queues a message for later delivery. It does not deliver it: sends
	// are asynchronous, and the runtime drains the queue.
	Post(to RefVal, atom string, args []Value, pos Pos) error

	// PostAfter queues a message delayed by that many milliseconds of play
	// time — the `target <- after(ms, :atom:)` form.
	//
	// Its own method rather than a delay argument on Post, because what a host
	// needs to honour it is different in kind: a queue that outlives the frame,
	// counted in play time so that pausing the game pauses the timer.
	PostAfter(to RefVal, atom string, args []Value, delayMS float64, pos Pos) error

	// Action performs a named engine verb — play_anim, spawn, destroy. The bool
	// is false when the host does not implement that verb, which lets the
	// builtin table stay in this package while the verbs live in the engine.
	Action(name string, args []Value, pos Pos) (Value, bool, error)

	// Log receives whatever `log` prints.
	Log(msg string)
}

// PropResolver is an optional extension of Host: a handle for one property of
// one kind of object, looked up once and used for every read and write of it.
//
// A host's Prop and SetProp find the property by kind and by name on every
// access. A member expression always names the same property, and nearly
// always of the same kind of object, so the interpreter asks once per site and
// keeps the answer (propSite). A nil handle means "no such property", and the
// interpreter goes through Prop and SetProp as before, which is where the
// diagnostics for an unknown name live.
//
// A handle must behave exactly as Prop and SetProp would for that kind and
// name, side effects included.
type PropResolver interface {
	PropHandle(kind Kind_, name string) PropHandle
}

// PropHandle reads and writes one property of objects of one kind.
type PropHandle interface {
	GetProp(r RefVal) (Value, bool)
	SetProp(r RefVal, v Value) error
}

// ---------- programs ----------

// Program is everything in scope for one script: its own functions plus every
// function reached through an import.
//
// Imports merge flat, with no qualification — see SCRIPTING.md. A collision
// between two files is therefore a real conflict rather than a shadowing, and
// Link reports it instead of picking one.
type Program struct {
	Funcs map[string]*FuncGroup
	// Params are the script's tunables, merged from every file the same way
	// Funcs are. An imported library declaring one is how a shared behaviour
	// asks the level for its own configuration.
	Params map[string]*ParamDecl
	Files  []*File
}

// NewProgram returns an empty program.
func NewProgram() *Program {
	return &Program{Funcs: map[string]*FuncGroup{}, Params: map[string]*ParamDecl{}}
}

// ParamList returns the program's parameters in declaration order — files in
// import order, declarations in source order — so the editor's panel lists them
// the way the author wrote them rather than in map order.
func (p *Program) ParamList() []*ParamDecl {
	var out []*ParamDecl
	seen := map[string]bool{}
	for _, f := range p.Files {
		for _, d := range f.Params {
			if seen[d.Name] || p.Params[d.Name] != d {
				// A name two files both declared is kept once, under whichever
				// declaration Add accepted.
				continue
			}
			seen[d.Name] = true
			out = append(out, d)
		}
	}
	return out
}

// Add merges one parsed file into the program, reporting any name that two files
// both define.
func (p *Program) Add(f *File) ErrorList {
	var errs ErrorList
	if f == nil {
		return errs
	}
	p.Files = append(p.Files, f)
	if p.Params == nil {
		p.Params = map[string]*ParamDecl{}
	}
	for _, d := range f.Params {
		if prev, dup := p.Params[d.Name]; dup {
			// Same rule as a duplicate function, and for a sharper reason: two
			// declarations of one param would leave the editor drawing a widget
			// for whichever type it happened to find.
			errs = append(errs, Error{
				Pos: d.Pos_,
				Msg: fmt.Sprintf("param %q is already declared at %s; imports merge into one namespace, so two files cannot both declare it",
					d.Name, prev.Pos_),
			})
			continue
		}
		p.Params[d.Name] = d
	}
	for _, g := range f.Funcs {
		if prev, dup := p.Funcs[g.Name]; dup {
			errs = append(errs, Error{
				Pos: g.Pos_,
				Msg: fmt.Sprintf("%q is already defined at %s; imports merge into one namespace, so two files cannot both define it",
					g.Name, prev.Pos_),
			})
			continue
		}
		p.Funcs[g.Name] = g
	}
	return errs
}

// Loader reads an imported file. The path is as written in the `import(...)`,
// resolved relative to the importing file by whoever implements this — the
// language has no filesystem of its own.
type Loader interface {
	Load(fromFile, importPath string) (src []byte, resolvedPath string, err error)
}

// Link parses a file and everything it imports, returning one merged program.
//
// A cycle is a load error rather than something to break silently: two files
// importing each other is nearly always a mistake, and the alternative is a
// program whose contents depend on which file was opened first.
func Link(rootPath string, src []byte, loader Loader) (*Program, ErrorList) {
	prog := NewProgram()
	var errs ErrorList
	seen := map[string]bool{}
	var stack []string

	var load func(path string, src []byte)
	load = func(path string, src []byte) {
		// The cycle check comes first: a file already on the stack is also
		// already in `seen`, so testing `seen` first would return quietly and
		// the cycle would never be reported.
		for _, s := range stack {
			if s == path {
				errs = append(errs, Error{
					Pos: Pos{File: path},
					Msg: fmt.Sprintf("import cycle: %s imports itself through %v", path, stack),
				})
				return
			}
		}
		if seen[path] {
			return
		}
		seen[path] = true
		stack = append(stack, path)
		defer func() { stack = stack[:len(stack)-1] }()

		f, ferrs := Parse(path, src)
		errs = append(errs, ferrs...)
		if f == nil {
			return
		}
		// Imports are loaded before the importing file is merged, so a
		// diagnostic about a duplicate name points at the file that redefined
		// it rather than at the one that got there first.
		for _, imp := range f.Imports {
			if loader == nil {
				errs = append(errs, Error{Pos: imp.Pos_, Msg: "imports are not available here"})
				continue
			}
			isrc, ipath, err := loader.Load(path, imp.Path)
			if err != nil {
				errs = append(errs, Error{Pos: imp.Pos_, Msg: err.Error()})
				continue
			}
			load(ipath, isrc)
		}
		errs = append(errs, prog.Add(f)...)
	}

	load(rootPath, src)
	return prog, errs.sortAndDedupe()
}
