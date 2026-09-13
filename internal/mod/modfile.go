package mod

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// ModuleFileName is the name of the module file, the file that makes a
// directory the root of a module.
const ModuleFileName = "teyru.mod"

// LangVersion is the language version `teyru mod init` writes. It is a
// property of the file rather than of the compiler: a module says which
// language it is written against, so that a future compiler can refuse a
// module it would have to reinterpret.
const LangVersion = 1

// File is a parsed teyru.mod.
type File struct {
	// Module is the module path, the prefix every import path of this module
	// starts with.
	Module string
	// Lang is the `teyru <n>` directive; 0 when the file does not carry one.
	Lang int
	// Requires are the modules this module depends on, sorted by path.
	Requires []*Require
	// directives holds the statements in the order they were written, so that
	// comments stay where the author put them and the canonical form of a file
	// is a fixed point of the formatter.
	directives []*directive
	// tail holds comment lines after the last directive.
	tail []string
}

// Require is one line of a require block.
type Require struct {
	Path    string
	Version string
	// Comment is the `// ...` that followed the line, without the slashes.
	Comment string
	lead    []string
}

// directive is one top-level statement of a module file.
type directive struct {
	kind  string // module, teyru or require
	lead  []string
	trail string
	// text is the module path for `module`, the counted version for `teyru`.
	text  string
	lang  int
	group []*Require // require block, in the order written
}

// Error is a syntax error in a module file, reported with its position because
// a module file is edited by hand.
type Error struct {
	File string
	Line int
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Msg)
}

// New returns an empty module file for the given module path.
func New(path string) *File {
	return &File{
		Module:     path,
		Lang:       LangVersion,
		directives: []*directive{{kind: "module", text: path}, {kind: "teyru", lang: LangVersion}},
	}
}

// Parse parses a module file. name is used in error messages only.
func Parse(name string, data []byte) (*File, error) {
	f := &File{}
	errf := func(line int, format string, args ...any) error {
		return &Error{File: name, Line: line, Msg: fmt.Sprintf(format, args...)}
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	lead := []string{}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		text, comment := splitComment(line)
		text = strings.TrimSpace(text)
		if text == "" {
			if comment != "" {
				lead = append(lead, comment)
			}
			continue
		}
		fields := strings.Fields(text)
		switch fields[0] {
		case "module":
			if f.Module != "" {
				return nil, errf(i+1, "duplicate module directive")
			}
			if len(fields) != 2 || !ValidPath(fields[1]) {
				return nil, errf(i+1, "module directive wants one module path, such as example.com/myapp")
			}
			f.Module = fields[1]
			f.directives = append(f.directives, &directive{kind: "module", lead: lead, trail: comment, text: fields[1]})
			lead = nil
		case "teyru":
			if f.Lang != 0 {
				return nil, errf(i+1, "duplicate teyru directive")
			}
			if len(fields) != 2 {
				return nil, errf(i+1, "teyru directive wants one version number, such as teyru %d", LangVersion)
			}
			n, err := strconv.Atoi(fields[1])
			if err != nil || n < 1 {
				return nil, errf(i+1, "teyru %s is not a language version", fields[1])
			}
			f.Lang = n
			f.directives = append(f.directives, &directive{kind: "teyru", lead: lead, trail: comment, lang: n})
			lead = nil
		case "require":
			d := &directive{kind: "require", lead: lead, trail: comment}
			lead = nil
			if len(fields) == 3 {
				r, err := parseRequire(name, i+1, fields[1], fields[2])
				if err != nil {
					return nil, err
				}
				d.group = append(d.group, r)
				f.directives = append(f.directives, d)
				continue
			}
			if len(fields) != 2 || fields[1] != "(" {
				return nil, errf(i+1, "require wants `require ( ... )` or `require <module> <version>`")
			}
			for i++; i < len(lines); i++ {
				t, c := splitComment(lines[i])
				t = strings.TrimSpace(t)
				if t == "" {
					if c != "" {
						lead = append(lead, c)
					}
					continue
				}
				if t == ")" {
					d.trail = c
					break
				}
				// A require block's entries are module path and version, and
				// nothing else: an unknown word here is a directive Teyru does
				// not know, and dropping it would silently change what the
				// module depends on.
				fs := strings.Fields(t)
				if len(fs) != 2 {
					return nil, errf(i+1, "require line wants a module path and a version")
				}
				r, err := parseRequire(name, i+1, fs[0], fs[1])
				if err != nil {
					return nil, err
				}
				r.Comment, r.lead = c, lead
				lead = nil
				d.group = append(d.group, r)
			}
			if i >= len(lines) {
				return nil, errf(i, "require block is missing its closing )")
			}
			f.directives = append(f.directives, d)
		default:
			return nil, errf(i+1, "unknown directive %q", fields[0])
		}
	}
	f.tail = lead
	if f.Module == "" {
		return nil, errf(1, "missing module directive")
	}
	f.collect()
	return f, nil
}

// ReadFile reads and parses a module file.
func ReadFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(path, data)
}

func parseRequire(name string, line int, path, version string) (*Require, error) {
	if !ValidPath(path) {
		return nil, &Error{File: name, Line: line, Msg: fmt.Sprintf("%q is not a module path", path)}
	}
	if _, err := ParseVersion(version); err != nil {
		return nil, &Error{File: name, Line: line, Msg: err.Error()}
	}
	return &Require{Path: path, Version: version}, nil
}

// splitComment splits a line into its text and the `// comment` that follows
// it. A module file has no string literals, so the first `//` is a comment.
func splitComment(line string) (string, string) {
	i := strings.Index(line, "//")
	if i < 0 {
		return line, ""
	}
	return line[:i], strings.TrimSpace(line[i+2:])
}

// collect rebuilds Requires from the directive list.
func (f *File) collect() {
	f.Requires = nil
	for _, d := range f.directives {
		f.Requires = append(f.Requires, d.group...)
	}
}

// Get returns the require for a module path, or nil.
func (f *File) Get(path string) *Require {
	for _, r := range f.Requires {
		if r.Path == path {
			return r
		}
	}
	return nil
}

// block returns the first require directive, adding one if the file has none.
func (f *File) block() *directive {
	for _, d := range f.directives {
		if d.kind == "require" {
			return d
		}
	}
	d := &directive{kind: "require"}
	f.directives = append(f.directives, d)
	return d
}

// Add records that this module requires path at version, replacing any
// version already recorded for it. It reports whether the file changed.
func (f *File) Add(path, version string) bool {
	for _, d := range f.directives {
		for _, r := range d.group {
			if r.Path == path {
				if r.Version == version {
					return false
				}
				r.Version = version
				return true
			}
		}
	}
	f.block().group = append(f.block().group, &Require{Path: path, Version: version})
	f.collect()
	return true
}

// Remove drops the require for a module path, reporting whether it was there.
func (f *File) Remove(path string) bool {
	for _, d := range f.directives {
		for i, r := range d.group {
			if r.Path == path {
				d.group = append(d.group[:i], d.group[i+1:]...)
				f.collect()
				return true
			}
		}
	}
	return false
}

// Format renders the canonical form of the file: module and language version
// first, requires sorted in one block, aligned, one trailing newline. Running
// it twice produces the same bytes, so `teyru mod tidy` rewriting a file a
// human already formatted is not a diff.
func (f *File) Format() []byte {
	var b strings.Builder
	if f.Module == "" {
		// A file with no module directive is not a module: Format is called
		// only on files Parse accepted or New built, both of which have one.
		panic("mod: Format on a file without a module directive")
	}
	// The module path and the language version come first, in that order,
	// whatever order they were written in: they are what the file is about,
	// and a canonical form puts them where a reader looks for them.
	if mod := f.first("module"); mod != nil {
		writeLead(&b, mod.lead)
		fmt.Fprintf(&b, "module %s", mod.text)
		writeTrail(&b, mod.trail)
		b.WriteByte('\n')
	}
	if lang := f.first("teyru"); lang != nil {
		b.WriteByte('\n')
		writeLead(&b, lang.lead)
		fmt.Fprintf(&b, "teyru %d", lang.lang)
		writeTrail(&b, lang.trail)
		b.WriteByte('\n')
	}
	if entries := f.requireEntries(); len(entries) > 0 {
		b.WriteByte('\n')
		f.formatRequires(&b, entries)
	}
	writeLead(&b, f.tail)
	return []byte(b.String())
}

// first returns the first directive of a kind.
func (f *File) first(kind string) *directive {
	for _, d := range f.directives {
		if d.kind == kind {
			return d
		}
	}
	return nil
}

// requireEntries lists every require line of the file in the order written.
func (f *File) requireEntries() []*Require {
	entries := []*Require{}
	for _, d := range f.directives {
		if d.kind == "require" {
			entries = append(entries, d.group...)
		}
	}
	return entries
}

// formatRequires writes the require block: one block, sorted by path, versions
// in a column. A single-line `require x v1.0.0` becomes a block entry, because
// the next `teyru get` adds a second one and a block is what the file would
// then look like anyway.
func (f *File) formatRequires(b *strings.Builder, entries []*Require) {
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	width := 0
	for _, r := range entries {
		width = max(width, len(r.Path))
	}
	b.WriteString("require (\n")
	for _, r := range entries {
		writeLead(b, r.lead)
		fmt.Fprintf(b, "\t%-*s %s", width, r.Path, r.Version)
		writeTrail(b, r.Comment)
		b.WriteByte('\n')
	}
	b.WriteString(")\n")
}

func writeLead(b *strings.Builder, lead []string) {
	for _, c := range lead {
		b.WriteString("// ")
		b.WriteString(c)
		b.WriteByte('\n')
	}
}

func writeTrail(b *strings.Builder, trail string) {
	if trail != "" {
		b.WriteString(" // ")
		b.WriteString(trail)
	}
}

// Write writes the canonical form to path.
func (f *File) Write(path string) error {
	if err := os.WriteFile(path, f.Format(), 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	return nil
}
