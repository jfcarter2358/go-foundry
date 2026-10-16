package pdf

// read.go — reading a PDF back: the classic cross-reference table and
// trailer, every object it points at, streams (deflated or not), and the
// page tree. It reads what this package writes and PDFs like it (no
// object streams, no cross-reference streams, no encryption) and checks
// as it goes: every xref entry must point at its own "n 0 obj", every
// stream's Length must land on "endstream", the trailer must name a
// catalog whose page tree counts its leaves. The tests read every file the
// writer makes this way, and rasterize.go draws the pages it finds.

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"strconv"
)

// Object is a PDF value: nil (null), bool, float64 (every number), Name,
// String, Array, Dict, Ref or *Stream.
type Object any

// Name is a PDF name, without its slash.
type Name string

// String is a PDF string's bytes.
type String []byte

// Array is a PDF array.
type Array []Object

// Dict is a PDF dictionary.
type Dict map[Name]Object

// Ref is an indirect reference.
type Ref struct{ Num, Gen int }

// Stream is a stream object: its dictionary and its data, decoded.
type Stream struct {
	Dict Dict
	Data []byte
	// Raw is the data as stored.
	Raw []byte
}

// File is a parsed PDF.
type File struct {
	Version string
	Objects map[int]Object
	// Offsets are where each object starts, from the xref table.
	Offsets map[int]int
	Trailer Dict
	// Pages are the page dictionaries in order.
	Pages []Dict
}

// Resolve follows a reference (any other object is itself).
func (f *File) Resolve(o Object) Object {
	for i := 0; i < 32; i++ {
		r, ok := o.(Ref)
		if !ok {
			return o
		}
		o = f.Objects[r.Num]
	}
	return nil
}

// Dict resolves an object to a dictionary (a stream's own dictionary
// too), or nil.
func (f *File) Dict(o Object) Dict {
	switch v := f.Resolve(o).(type) {
	case Dict:
		return v
	case *Stream:
		return v.Dict
	}
	return nil
}

// Info is the document's Info dictionary's strings.
func (f *File) Info() map[string]string {
	out := map[string]string{}
	for k, v := range f.Dict(f.Trailer["Info"]) {
		if s, ok := f.Resolve(v).(String); ok {
			out[string(k)] = decodeText(s)
		}
	}
	return out
}

// decodeText reads a text string: UTF-16BE with a byte-order mark, or
// bytes.
func decodeText(s String) string {
	if len(s) >= 2 && s[0] == 0xFE && s[1] == 0xFF {
		var rs []rune
		for i := 2; i+1 < len(s); i += 2 {
			u := rune(s[i])<<8 | rune(s[i+1])
			if u >= 0xD800 && u < 0xDC00 && i+3 < len(s) {
				l := rune(s[i+2])<<8 | rune(s[i+3])
				u = (u-0xD800)<<10 + (l - 0xDC00) + 0x10000
				i += 2
			}
			rs = append(rs, u)
		}
		return string(rs)
	}
	return string(s)
}

// Parse reads a PDF.
func Parse(data []byte) (*File, error) {
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return nil, fmt.Errorf("pdf: no %%PDF- header")
	}
	f := &File{Objects: map[int]Object{}, Offsets: map[int]int{}}
	if nl := bytes.IndexAny(data, "\r\n"); nl > 5 {
		f.Version = string(data[5:nl])
	}
	sx := bytes.LastIndex(data, []byte("startxref"))
	if sx < 0 {
		return nil, fmt.Errorf("pdf: no startxref")
	}
	if !bytes.Contains(data[sx:], []byte("%%EOF")) {
		return nil, fmt.Errorf("pdf: no %%%%EOF after startxref")
	}
	lx := &lexer{b: data, i: sx + len("startxref")}
	tok, ok := lx.number()
	if !ok {
		return nil, fmt.Errorf("pdf: startxref has no offset")
	}
	xref := int(tok)
	if xref <= 0 || xref >= len(data) || !bytes.HasPrefix(data[xref:], []byte("xref")) {
		return nil, fmt.Errorf("pdf: startxref %d does not point at an xref table", xref)
	}
	lx.i = xref + len("xref")
	type entry struct {
		off  int
		used bool
	}
	entries := map[int]entry{}
	for {
		lx.space()
		if bytes.HasPrefix(data[lx.i:], []byte("trailer")) {
			lx.i += len("trailer")
			break
		}
		first, ok1 := lx.number()
		count, ok2 := lx.number()
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("pdf: bad xref subsection at %d", lx.i)
		}
		lx.space()
		for k := 0; k < int(count); k++ {
			if lx.i+20 > len(data) {
				return nil, fmt.Errorf("pdf: xref runs off the file")
			}
			line := data[lx.i : lx.i+20]
			if line[10] != ' ' || line[16] != ' ' || (line[17] != 'n' && line[17] != 'f') || (line[18] != ' ' && line[18] != '\r') {
				return nil, fmt.Errorf("pdf: xref entry %d is not 20 bytes: %q", int(first)+k, line)
			}
			off, err := strconv.Atoi(string(line[:10]))
			if err != nil {
				return nil, fmt.Errorf("pdf: xref entry %d: %v", int(first)+k, err)
			}
			entries[int(first)+k] = entry{off: off, used: line[17] == 'n'}
			lx.i += 20
		}
	}
	tr, err := lx.object()
	if err != nil {
		return nil, fmt.Errorf("pdf: trailer: %w", err)
	}
	trailer, ok := tr.(Dict)
	if !ok {
		return nil, fmt.Errorf("pdf: trailer is not a dictionary")
	}
	f.Trailer = trailer
	size, _ := trailer["Size"].(float64)
	if int(size) != len(entries) {
		return nil, fmt.Errorf("pdf: trailer Size %d, xref has %d entries", int(size), len(entries))
	}
	for n, en := range entries {
		if !en.used {
			continue
		}
		if en.off <= 0 || en.off >= len(data) {
			return nil, fmt.Errorf("pdf: object %d at %d is outside the file", n, en.off)
		}
		ol := &lexer{b: data, i: en.off}
		num, ok1 := ol.number()
		gen, ok2 := ol.number()
		ol.space()
		if !ok1 || !ok2 || int(num) != n || int(gen) != 0 || !ol.keyword("obj") {
			return nil, fmt.Errorf("pdf: xref entry %d (offset %d) does not point at \"%d 0 obj\"", n, en.off, n)
		}
		o, err := ol.object()
		if err != nil {
			return nil, fmt.Errorf("pdf: object %d: %w", n, err)
		}
		ol.space()
		if d, isDict := o.(Dict); isDict && ol.keyword("stream") {
			st, err := ol.stream(d)
			if err != nil {
				return nil, fmt.Errorf("pdf: object %d: %w", n, err)
			}
			o = st
			ol.space()
		}
		if !ol.keyword("endobj") {
			return nil, fmt.Errorf("pdf: object %d has no endobj", n)
		}
		f.Objects[n], f.Offsets[n] = o, en.off
	}
	root := f.Dict(trailer["Root"])
	if root == nil || root["Type"] != Name("Catalog") {
		return nil, fmt.Errorf("pdf: the trailer's Root is not a catalog")
	}
	if err := f.walkPages(root["Pages"], 0); err != nil {
		return nil, err
	}
	pages := f.Dict(root["Pages"])
	if c, _ := pages["Count"].(float64); int(c) != len(f.Pages) {
		return nil, fmt.Errorf("pdf: the page tree counts %d pages and has %d", int(c), len(f.Pages))
	}
	return f, nil
}

// walkPages collects the page tree's leaves in order.
func (f *File) walkPages(o Object, depth int) error {
	if depth > 32 {
		return fmt.Errorf("pdf: page tree too deep")
	}
	d := f.Dict(o)
	if d == nil {
		return fmt.Errorf("pdf: a page tree node is missing")
	}
	switch d["Type"] {
	case Name("Pages"):
		kids, _ := f.Resolve(d["Kids"]).(Array)
		for _, k := range kids {
			if err := f.walkPages(k, depth+1); err != nil {
				return err
			}
		}
	case Name("Page"):
		f.Pages = append(f.Pages, d)
	default:
		return fmt.Errorf("pdf: page tree node of type %v", d["Type"])
	}
	return nil
}

// Contents is a page's content, its streams decoded and joined.
func (f *File) Contents(page Dict) ([]byte, error) {
	var out []byte
	add := func(o Object) error {
		st, ok := f.Resolve(o).(*Stream)
		if !ok {
			return fmt.Errorf("pdf: page contents are not a stream")
		}
		out = append(append(out, st.Data...), '\n')
		return nil
	}
	switch c := page["Contents"].(type) {
	case Array:
		for _, o := range c {
			if err := add(o); err != nil {
				return nil, err
			}
		}
	default:
		if err := add(c); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// MediaBox is a page's size in points.
func (f *File) MediaBox(page Dict) (w, h float64, err error) {
	a, ok := f.Resolve(page["MediaBox"]).(Array)
	if !ok || len(a) != 4 {
		return 0, 0, fmt.Errorf("pdf: page has no MediaBox")
	}
	var v [4]float64
	for i, o := range a {
		if v[i], ok = f.Resolve(o).(float64); !ok {
			return 0, 0, fmt.Errorf("pdf: MediaBox is not four numbers")
		}
	}
	return v[2] - v[0], v[3] - v[1], nil
}

// ---------- the lexer ----------

type lexer struct {
	b []byte
	i int
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == '\f' || c == 0
}

func isDelim(c byte) bool { return bytes.IndexByte([]byte("()<>[]{}/%"), c) >= 0 }

// space skips white space and comments.
func (l *lexer) space() {
	for l.i < len(l.b) {
		c := l.b[l.i]
		switch {
		case isSpace(c):
			l.i++
		case c == '%':
			for l.i < len(l.b) && l.b[l.i] != '\n' && l.b[l.i] != '\r' {
				l.i++
			}
		default:
			return
		}
	}
}

// keyword consumes a keyword when it is next.
func (l *lexer) keyword(k string) bool {
	l.space()
	if !bytes.HasPrefix(l.b[l.i:], []byte(k)) {
		return false
	}
	end := l.i + len(k)
	if end < len(l.b) && !isSpace(l.b[end]) && !isDelim(l.b[end]) {
		return false
	}
	l.i = end
	return true
}

// number reads a number.
func (l *lexer) number() (float64, bool) {
	l.space()
	j := l.i
	for j < len(l.b) && bytes.IndexByte([]byte("+-.0123456789"), l.b[j]) >= 0 {
		j++
	}
	if j == l.i {
		return 0, false
	}
	v, err := strconv.ParseFloat(string(l.b[l.i:j]), 64)
	if err != nil {
		return 0, false
	}
	l.i = j
	return v, true
}

// token reads a bare word (keyword or operator).
func (l *lexer) token() string {
	l.space()
	j := l.i
	for j < len(l.b) && !isSpace(l.b[j]) && !isDelim(l.b[j]) {
		j++
	}
	t := string(l.b[l.i:j])
	l.i = j
	return t
}

// object reads one object; a number followed by "g R" is a reference.
func (l *lexer) object() (Object, error) {
	l.space()
	if l.i >= len(l.b) {
		return nil, io.ErrUnexpectedEOF
	}
	c := l.b[l.i]
	switch {
	case c == '/':
		l.i++
		j := l.i
		for j < len(l.b) && !isSpace(l.b[j]) && !isDelim(l.b[j]) {
			j++
		}
		n := string(l.b[l.i:j])
		l.i = j
		return Name(n), nil
	case c == '(':
		return l.literal()
	case c == '<' && l.i+1 < len(l.b) && l.b[l.i+1] == '<':
		l.i += 2
		d := Dict{}
		for {
			l.space()
			if bytes.HasPrefix(l.b[l.i:], []byte(">>")) {
				l.i += 2
				return d, nil
			}
			k, err := l.object()
			if err != nil {
				return nil, err
			}
			name, ok := k.(Name)
			if !ok {
				return nil, fmt.Errorf("dictionary key %v is not a name", k)
			}
			v, err := l.object()
			if err != nil {
				return nil, err
			}
			d[name] = v
		}
	case c == '<':
		l.i++
		j := bytes.IndexByte(l.b[l.i:], '>')
		if j < 0 {
			return nil, fmt.Errorf("unclosed hex string")
		}
		hex := bytes.Map(func(r rune) rune {
			if isSpace(byte(r)) {
				return -1
			}
			return r
		}, l.b[l.i:l.i+j])
		l.i += j + 1
		if len(hex)%2 == 1 {
			hex = append(hex, '0')
		}
		out := make(String, len(hex)/2)
		for k := range out {
			v, err := strconv.ParseUint(string(hex[2*k:2*k+2]), 16, 8)
			if err != nil {
				return nil, fmt.Errorf("bad hex string")
			}
			out[k] = byte(v)
		}
		return out, nil
	case c == '[':
		l.i++
		var a Array
		for {
			l.space()
			if l.i < len(l.b) && l.b[l.i] == ']' {
				l.i++
				return a, nil
			}
			o, err := l.object()
			if err != nil {
				return nil, err
			}
			a = append(a, o)
		}
	case c == '+' || c == '-' || c == '.' || c >= '0' && c <= '9':
		v, ok := l.number()
		if !ok {
			return nil, fmt.Errorf("bad number at %d", l.i)
		}
		// A reference: two integers and R.
		save := l.i
		if g, ok := l.number(); ok && l.keyword("R") && v == float64(int(v)) && g == float64(int(g)) {
			return Ref{int(v), int(g)}, nil
		}
		l.i = save
		return v, nil
	}
	switch t := l.token(); t {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected %q at %d", t, l.i)
	}
}

// literal reads a (string) with its escapes.
func (l *lexer) literal() (Object, error) {
	l.i++ // (
	var out String
	depth := 1
	for l.i < len(l.b) {
		c := l.b[l.i]
		l.i++
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return out, nil
			}
		case '\\':
			if l.i >= len(l.b) {
				break
			}
			e := l.b[l.i]
			l.i++
			switch e {
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case '\r', '\n':
				continue
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for k := 0; k < 2 && l.i < len(l.b) && l.b[l.i] >= '0' && l.b[l.i] <= '7'; k++ {
						v = v*8 + int(l.b[l.i]-'0')
						l.i++
					}
					c = byte(v)
				} else {
					c = e
				}
			}
		}
		out = append(out, c)
	}
	return nil, fmt.Errorf("unclosed string")
}

// stream reads a stream's data after its "stream" keyword, by its Length.
func (l *lexer) stream(d Dict) (*Stream, error) {
	if l.i < len(l.b) && l.b[l.i] == '\r' {
		l.i++
	}
	if l.i >= len(l.b) || l.b[l.i] != '\n' {
		return nil, fmt.Errorf("stream keyword not followed by an end of line")
	}
	l.i++
	n, ok := d["Length"].(float64)
	if !ok || n < 0 || l.i+int(n) > len(l.b) {
		return nil, fmt.Errorf("stream Length %v", d["Length"])
	}
	raw := l.b[l.i : l.i+int(n)]
	l.i += int(n)
	if !l.keyword("endstream") {
		return nil, fmt.Errorf("stream Length %d does not end at endstream", int(n))
	}
	st := &Stream{Dict: d, Raw: raw, Data: raw}
	switch flt := d["Filter"].(type) {
	case nil:
	case Name:
		if flt != "FlateDecode" {
			return nil, fmt.Errorf("stream filter %s", flt)
		}
		zr, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("stream: %v", err)
		}
		data, err := io.ReadAll(zr)
		if err != nil {
			return nil, fmt.Errorf("stream: %v", err)
		}
		st.Data = data
	default:
		return nil, fmt.Errorf("stream filter %v", flt)
	}
	return st, nil
}
