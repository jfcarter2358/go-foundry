package musicxml

// xmlw.go — the small XML writer export.go writes with.
//
// encoding/xml's Encoder would do, but MusicXML wants its elements in a
// fixed order with a fixed indentation and nothing else: the same score
// always writes the same bytes (a diff of two exports in the Git window
// shows only what changed), and an element is written where the writer
// is, not where a struct's fields put it.

import (
	"bytes"
	"encoding/xml"
	"strconv"
	"strings"
)

// xw writes indented XML.
type xw struct {
	b     bytes.Buffer
	stack []string
}

// attr is one attribute; empty values are left out.
type attr struct{ k, v string }

func (w *xw) indent() {
	w.b.WriteString(strings.Repeat("  ", len(w.stack)))
}

func (w *xw) attrs(as []attr) {
	for _, a := range as {
		if a.v == "" {
			continue
		}
		w.b.WriteByte(' ')
		w.b.WriteString(a.k)
		w.b.WriteString(`="`)
		xml.EscapeText(&w.b, []byte(a.v))
		w.b.WriteByte('"')
	}
}

// open starts an element and goes inside it.
func (w *xw) open(name string, as ...attr) {
	w.indent()
	w.b.WriteByte('<')
	w.b.WriteString(name)
	w.attrs(as)
	w.b.WriteString(">\n")
	w.stack = append(w.stack, name)
}

// close ends the element open last.
func (w *xw) close() {
	name := w.stack[len(w.stack)-1]
	w.stack = w.stack[:len(w.stack)-1]
	w.indent()
	w.b.WriteString("</")
	w.b.WriteString(name)
	w.b.WriteString(">\n")
}

// leaf writes an element holding text.
func (w *xw) leaf(name, text string, as ...attr) {
	w.indent()
	w.b.WriteByte('<')
	w.b.WriteString(name)
	w.attrs(as)
	w.b.WriteByte('>')
	xml.EscapeText(&w.b, []byte(text))
	w.b.WriteString("</")
	w.b.WriteString(name)
	w.b.WriteString(">\n")
}

// empty writes an element with nothing inside.
func (w *xw) empty(name string, as ...attr) {
	w.indent()
	w.b.WriteByte('<')
	w.b.WriteString(name)
	w.attrs(as)
	w.b.WriteString("/>\n")
}

// num writes a number for MusicXML: no exponent, no trailing zeros, at most
// four decimals.
func num(v float64) string {
	s := strconv.FormatFloat(v, 'f', 4, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "-0" || s == "" {
		return "0"
	}
	return s
}

func itoa(v int) string { return strconv.Itoa(v) }
