// Package source holds source files, positions and diagnostics.
package source

import (
	"fmt"
	"sort"
	"strings"
)

// File is an in-memory Teyru source file.
type File struct {
	Path  string
	Text  string
	lines []int
}

// NewFile creates a file and indexes its line starts.
func NewFile(path, text string) *File {
	f := &File{Path: path, Text: text}
	f.lines = append(f.lines, 0)
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\n':
			f.lines = append(f.lines, i+1)
		case '\r':
			if i+1 < len(text) && text[i+1] == '\n' {
				i++
			}
			f.lines = append(f.lines, i+1)
		}
	}
	return f
}

// Position converts a byte offset into a 1-based line and column.
func (f *File) Position(off int) (line, col int) {
	if f == nil {
		return 0, 0
	}
	i := sort.Search(len(f.lines), func(i int) bool { return f.lines[i] > off }) - 1
	if i < 0 {
		i = 0
	}
	return i + 1, off - f.lines[i] + 1
}

// Pos is a location inside a file.
type Pos struct {
	File *File
	Off  int
}

func (p Pos) String() string {
	if p.File == nil {
		return "<builtin>"
	}
	l, c := p.File.Position(p.Off)
	return fmt.Sprintf("%s:%d:%d", p.File.Path, l, c)
}

// Severity of a diagnostic.
type Severity int

const (
	Error Severity = iota
	Warning
)

// Diagnostic is a compiler message with a stable code.
type Diagnostic struct {
	Pos      Pos
	End      int
	Severity Severity
	Code     string
	Message  string
}

func (d Diagnostic) String() string {
	sev := "error"
	if d.Severity == Warning {
		sev = "warning"
	}
	return fmt.Sprintf("%s: %s[%s]: %s", d.Pos, sev, d.Code, d.Message)
}

// Diagnostics collects messages.
type Diagnostics struct {
	List []Diagnostic
}

// Errorf records an error.
func (d *Diagnostics) Errorf(pos Pos, code, format string, args ...any) {
	d.List = append(d.List, Diagnostic{Pos: pos, End: pos.Off, Code: code, Message: fmt.Sprintf(format, args...)})
}

// HasErrors reports whether any error was recorded.
func (d *Diagnostics) HasErrors() bool {
	for _, x := range d.List {
		if x.Severity == Error {
			return true
		}
	}
	return false
}

func (d *Diagnostics) String() string {
	var b strings.Builder
	for _, x := range d.List {
		b.WriteString(x.String())
		b.WriteByte('\n')
	}
	return b.String()
}
