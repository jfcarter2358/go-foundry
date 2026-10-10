// Package musicxml imports MusicXML 3.1 and 4.0 — what Sibelius, Finale,
// Dorico and MuseScore all export, and the way a Sibelius score reaches
// the Score editor (Avid has never published the .sib format).
//
// read.go is the file: plain .musicxml/.xml, compressed .mxl (a zip whose
// META-INF/container.xml names the score), UTF-8 or UTF-16, partwise or
// timewise (timewise is turned partwise on reading). The document is kept
// as a small ordered element tree, because a measure's meaning is in the
// order of its children (a <backup> moves time back for the next voice).
// import.go makes a score of it.
package musicxml

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"unicode/utf16"
)

// node is an element: its name, attributes, children in order and the text
// directly inside it.
type node struct {
	name  string
	attrs []xml.Attr
	kids  []*node
	text  string
}

// attr is an attribute's value, "" when absent.
func (n *node) attr(name string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// child is the first child of a name, or nil.
func (n *node) child(name string) *node {
	if n == nil {
		return nil
	}
	for _, k := range n.kids {
		if k.name == name {
			return k
		}
	}
	return nil
}

// children are the children of a name.
func (n *node) children(name string) []*node {
	if n == nil {
		return nil
	}
	var out []*node
	for _, k := range n.kids {
		if k.name == name {
			out = append(out, k)
		}
	}
	return out
}

// has reports a child of a name.
func (n *node) has(name string) bool { return n.child(name) != nil }

// str is a child's text, trimmed.
func (n *node) str(name string) string {
	if c := n.child(name); c != nil {
		return strings.TrimSpace(c.text)
	}
	return ""
}

// num is a child's text as a number, def when absent or not a number.
func (n *node) num(name string, def float64) float64 {
	s := n.str(name)
	if s == "" {
		return def
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return v
}

// integer is a child's text as an integer (a decimal is rounded).
func (n *node) integer(name string, def int) int {
	v := n.num(name, float64(def))
	if v < 0 {
		return int(v - 0.5)
	}
	return int(v + 0.5)
}

// Document is a parsed MusicXML score, always partwise.
type Document struct {
	root *node // <score-partwise>
	// Version is the file's MusicXML version ("3.1", "4.0"), "" if unsaid.
	Version string
	// Software is what wrote it (identification/encoding/software).
	Software string
}

// ReadFile reads a .musicxml, .xml or .mxl file.
func ReadFile(p string) (*Document, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	d, err := Read(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return d, nil
}

// Read parses a MusicXML document, compressed or not.
func Read(b []byte) (*Document, error) {
	if bytes.HasPrefix(b, []byte("PK\x03\x04")) {
		var err error
		if b, err = unzipScore(b); err != nil {
			return nil, err
		}
	}
	b = toUTF8(b)
	root, err := parseTree(b)
	if err != nil {
		return nil, err
	}
	switch root.name {
	case "score-partwise":
	case "score-timewise":
		root = timewiseToPartwise(root)
	case "opus":
		return nil, errors.New("musicxml: an opus is a list of scores; open one of them")
	default:
		return nil, fmt.Errorf("musicxml: <%s> is not a score (want score-partwise or score-timewise)", root.name)
	}
	d := &Document{root: root, Version: root.attr("version")}
	if enc := root.child("identification").child("encoding"); enc != nil {
		d.Software = enc.str("software")
	}
	return d, nil
}

// unzipScore finds the score in a compressed .mxl: the container's first
// rootfile, or failing that the first .musicxml/.xml outside META-INF.
func unzipScore(b []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, fmt.Errorf("musicxml: bad .mxl archive: %w", err)
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	read := func(f *zip.File) ([]byte, error) {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, 256<<20))
	}
	if c := files["META-INF/container.xml"]; c != nil {
		cb, err := read(c)
		if err == nil {
			if tree, err := parseTree(toUTF8(cb)); err == nil {
				for _, rf := range tree.child("rootfiles").children("rootfile") {
					mt := rf.attr("media-type")
					if mt != "" && !strings.Contains(mt, "musicxml") && mt != "application/xml" && mt != "text/xml" {
						continue
					}
					if f := files[path.Clean(rf.attr("full-path"))]; f != nil {
						return read(f)
					}
				}
			}
		}
	}
	for _, f := range zr.File {
		ext := strings.ToLower(path.Ext(f.Name))
		if !strings.HasPrefix(f.Name, "META-INF/") && (ext == ".musicxml" || ext == ".xml") {
			return read(f)
		}
	}
	return nil, errors.New("musicxml: the .mxl archive holds no score")
}

// toUTF8 turns a UTF-16 file (with a byte-order mark, as Finale writes
// them) into UTF-8, and drops a UTF-8 byte-order mark.
func toUTF8(b []byte) []byte {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return b[3:]
	case len(b) >= 2 && (b[0] == 0xFF && b[1] == 0xFE || b[0] == 0xFE && b[1] == 0xFF):
		le := b[0] == 0xFF
		u := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			if le {
				u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
			} else {
				u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
			}
		}
		return []byte(string(utf16.Decode(u)))
	}
	return b
}

// parseTree reads the document into nodes. Entities beyond XML's own are
// the HTML ones (some exporters write &nbsp;); the encoding declaration is
// honoured for Latin-1, and UTF-16 has already been turned into UTF-8.
func parseTree(b []byte) (*node, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = func(label string, r io.Reader) (io.Reader, error) {
		switch strings.ToLower(label) {
		case "utf-8", "utf8", "us-ascii", "ascii", "utf-16", "utf16":
			return r, nil
		case "iso-8859-1", "latin1", "latin-1", "windows-1252", "cp1252":
			raw, err := io.ReadAll(r)
			if err != nil {
				return nil, err
			}
			rs := make([]rune, len(raw))
			for i, c := range raw {
				rs[i] = rune(c)
			}
			return strings.NewReader(string(rs)), nil
		}
		return nil, fmt.Errorf("musicxml: unsupported encoding %q", label)
	}
	var stack []*node
	var root *node
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("musicxml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: t.Name.Local, attrs: append([]xml.Attr(nil), t.Attr...)}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.kids = append(p.kids, n)
			} else if root == nil {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text += string(t)
			}
		}
	}
	if root == nil {
		return nil, errors.New("musicxml: empty document")
	}
	return root, nil
}

// timewiseToPartwise turns measures-of-parts into parts-of-measures.
func timewiseToPartwise(tw *node) *node {
	pw := &node{name: "score-partwise", attrs: tw.attrs}
	parts := map[string]*node{}
	var order []string
	for _, k := range tw.kids {
		if k.name != "measure" {
			pw.kids = append(pw.kids, k)
			continue
		}
		for _, p := range k.children("part") {
			id := p.attr("id")
			part := parts[id]
			if part == nil {
				part = &node{name: "part", attrs: []xml.Attr{{Name: xml.Name{Local: "id"}, Value: id}}}
				parts[id] = part
				order = append(order, id)
			}
			part.kids = append(part.kids, &node{name: "measure", attrs: k.attrs, kids: p.kids})
		}
	}
	for _, id := range order {
		pw.kids = append(pw.kids, parts[id])
	}
	return pw
}
