package aitree

// file.go — the .fai document format.
//
// The conventions are the rest of the engine's: a name table rather than a raw
// enum int, so a reordered constant cannot silently reinterpret an old file;
// omitempty on everything with a meaningful default, so a document says only
// what it decided; and WriteFileAtomic, so a crash mid-save cannot leave half a
// tree behind.
//
// The one thing this format does that the others do not is write a node's verb
// and fields as a flat map in the *verb's own field order*:
//
//	do: {verb: send, to: '@self', event: burst_round, after: '?burst_gap', args: [target]}
//
// rather than nesting the fields under a `fields:` key. A node is a verb and
// its form, and that is what it should look like in the file — the diff of a
// tree someone edited should read like a sentence that changed. Fields the
// verb does not declare are still round-tripped, after the ones it does, so
// opening a document written by a newer build and saving it does not quietly
// throw away the part this build did not understand.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/fileutil"

	"gopkg.in/yaml.v2"
)

// AIDirName is where decision trees live, beside models/, particles/ and
// scripts/.
const AIDirName = "ai"

// AIExt is the native document extension.
const AIExt = ".fai"

// AIDir is where the editor's open and save dialogs look.
func AIDir() string { return filepath.Join(fileutil.ResourcesPath, AIDirName) }

// AIPath resolves a bare tree name to a full path. A name that already carries
// the extension, or a path with a directory in it, is taken as written — the
// same latitude ScriptPath gives a script.
func AIPath(name string) string {
	if name == "" {
		return ""
	}
	if !strings.HasSuffix(name, AIExt) {
		name += AIExt
	}
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(AIDir(), name)
}

// AIName is the inverse: the bare name a level or an entity type spells, from a
// path.
func AIName(path string) string {
	if path == "" {
		return ""
	}
	rel, err := filepath.Rel(AIDir(), path)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = filepath.Base(path)
	}
	rel = filepath.ToSlash(rel)
	return strings.TrimSuffix(rel, AIExt)
}

// ScanAIFiles lists every tree in the ai folder, as bare names.
func ScanAIFiles() []string {
	var out []string
	for _, p := range fileutil.ScanFilesByExt(AIDir(), AIExt) {
		out = append(out, AIName(p))
	}
	sort.Strings(out)
	return out
}

// ---------- the file ----------

// RawParam is one declared tunable.
type RawParam struct {
	Name  string `yaml:"name"`
	Type  string `yaml:"type"`
	Value string `yaml:"value,omitempty"`
	Doc   string `yaml:"doc,omitempty"`
}

// RawNode is one box.
type RawNode struct {
	ID   string `yaml:"id"`
	Kind string `yaml:"kind"`
	// At is the position on the canvas, as [x, y]. A pair rather than a
	// {x: , y: } map because a node has a lot of them and they are noise.
	At    []float32 `yaml:"at,omitempty"`
	Label string    `yaml:"label,omitempty"`
	Note  string    `yaml:"note,omitempty"`

	Check *Call  `yaml:"check,omitempty"`
	Then  string `yaml:"then,omitempty"`
	Else  string `yaml:"else,omitempty"`

	Do   *Call  `yaml:"do,omitempty"`
	Next string `yaml:"next,omitempty"`
}

// RawTree is one entry point.
type RawTree struct {
	Event string    `yaml:"event,omitempty"`
	Name  string    `yaml:"name,omitempty"`
	Args  []string  `yaml:"args,omitempty"`
	Doc   string    `yaml:"doc,omitempty"`
	Root  string    `yaml:"root,omitempty"`
	Nodes []RawNode `yaml:"nodes,omitempty"`
}

// RawDoc is the file.
type RawDoc struct {
	ID          string     `yaml:"id"`
	Description string     `yaml:"description,omitempty"`
	Params      []RawParam `yaml:"params,omitempty"`
	Trees       []RawTree  `yaml:"trees,omitempty"`
}

// ---------- a call, as YAML ----------

// MarshalYAML writes a call as `{verb: x, field: y, ..., args: [...]}`, fields
// in the verb's own order.
func (c Call) MarshalYAML() (any, error) {
	out := yaml.MapSlice{{Key: "verb", Value: c.Verb}}
	written := map[string]bool{}
	if v := VerbOf(c.Verb); v != nil {
		for _, f := range v.Fields {
			if t := c.Fields[f.Name]; t != "" {
				out = append(out, yaml.MapItem{Key: f.Name, Value: t})
				written[f.Name] = true
			}
		}
	}
	// Anything the table does not know about, kept rather than dropped, in a
	// stable order so a round trip through a build that does not understand it
	// is still a no-op.
	var extra []string
	for k, t := range c.Fields {
		if !written[k] && t != "" {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		out = append(out, yaml.MapItem{Key: k, Value: c.Fields[k]})
	}
	if len(c.Args) > 0 {
		out = append(out, yaml.MapItem{Key: "args", Value: c.Args})
	}
	return out, nil
}

// UnmarshalYAML reads that back. Every field is taken as text whatever YAML
// decided its type was, so `after: 470` and `after: "470"` are the same
// expression — which they are, since both parse to the same flang integer.
func (c *Call) UnmarshalYAML(unmarshal func(any) error) error {
	var ms yaml.MapSlice
	if err := unmarshal(&ms); err != nil {
		return err
	}
	c.Verb, c.Fields, c.Args = "", nil, nil
	for _, item := range ms {
		key, _ := item.Key.(string)
		switch key {
		case "verb":
			c.Verb = scalar(item.Value)
		case "args":
			list, ok := item.Value.([]any)
			if !ok {
				return fmt.Errorf("args must be a list")
			}
			for _, a := range list {
				c.Args = append(c.Args, scalar(a))
			}
		case "":
			// A key that is not a string cannot be a field name; ignore it
			// rather than failing the whole document over it.
		default:
			c.Set(key, scalar(item.Value))
		}
	}
	return nil
}

// scalar renders one decoded YAML value as the text an author would have
// typed. A float keeps the spelling YAML gave it rather than Go's %v, so 0.35
// does not become 0.34999999999999998 on a round trip.
func scalar(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%v", t), "0"), ".")
	}
	return fmt.Sprintf("%v", v)
}

// ---------- to and from the document ----------

// ToRaw turns a document into what the file holds.
func ToRaw(d *Doc) *RawDoc {
	r := &RawDoc{ID: d.ID, Description: d.Description}
	for _, p := range d.Params {
		r.Params = append(r.Params, RawParam{Name: p.Name, Type: p.Type, Value: p.Value, Doc: p.Doc})
	}
	for _, t := range d.Trees {
		rt := RawTree{Event: t.Event, Name: t.Name, Args: t.Args, Doc: t.Doc, Root: t.Root}
		for _, n := range t.Nodes {
			rn := RawNode{ID: n.ID, Kind: NodeKindName(n.Kind), Label: n.Label, Note: n.Note}
			if n.X != 0 || n.Y != 0 {
				rn.At = []float32{n.X, n.Y}
			}
			// Only the half the node's kind uses is written. The other half is
			// kept in memory so a flip and a flip back loses nothing, but a
			// saved file should not carry a condition nobody can reach.
			switch n.Kind {
			case NodeCondition:
				c := n.Check.Clone()
				rn.Check, rn.Then, rn.Else = &c, n.Then, n.Else
			case NodeAction:
				c := n.Do.Clone()
				rn.Do, rn.Next = &c, n.Next
			}
			rt.Nodes = append(rt.Nodes, rn)
		}
		r.Trees = append(r.Trees, rt)
	}
	return r
}

// FromRaw is the inverse.
func FromRaw(r *RawDoc) *Doc {
	d := &Doc{ID: r.ID, Description: r.Description}
	for _, p := range r.Params {
		d.Params = append(d.Params, Param{Name: p.Name, Type: p.Type, Value: p.Value, Doc: p.Doc})
	}
	for i := range r.Trees {
		rt := &r.Trees[i]
		t := &Tree{Event: rt.Event, Name: rt.Name, Args: rt.Args, Doc: rt.Doc, Root: rt.Root}
		for j := range rt.Nodes {
			rn := &rt.Nodes[j]
			n := &Node{
				ID:    rn.ID,
				Kind:  NodeKindFromName(rn.Kind),
				Label: rn.Label,
				Note:  rn.Note,
				Then:  rn.Then,
				Else:  rn.Else,
				Next:  rn.Next,
			}
			if len(rn.At) > 0 {
				n.X = rn.At[0]
			}
			if len(rn.At) > 1 {
				n.Y = rn.At[1]
			}
			if rn.Check != nil {
				n.Check = *rn.Check
			}
			if rn.Do != nil {
				n.Do = *rn.Do
			}
			t.Nodes = append(t.Nodes, n)
		}
		d.Trees = append(d.Trees, t)
	}
	return d
}

// LoadAI reads a document from disk.
func LoadAI(path string) (*Doc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw RawDoc
	if err := yaml.UnmarshalStrict(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	d := FromRaw(&raw)
	if d.ID == "" {
		d.ID = AIName(path)
	}
	d.Path = path
	return d, nil
}

// SaveAI writes a document, creating the ai folder on the first one.
func SaveAI(d *Doc, path string) error {
	data, err := yaml.Marshal(ToRaw(d))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data)
}

// Fingerprint hashes a document as its file would store it, for the editor's
// "Saved"/"Unsaved Changes" chip. It hashes the raw form rather than the
// document so that anything not written — Rev, Path — cannot make a saved
// document look edited.
func Fingerprint(d *Doc) uint64 { return fileutil.Fingerprint(ToRaw(d)) }
