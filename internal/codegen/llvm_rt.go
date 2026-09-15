package codegen

// This file reads the runtime's own header.
//
// The module the LLVM back end writes calls into the C runtime, and a call has
// to be made with the types the callee's prototype declares: C converts an
// argument to the parameter's type on the way in -- `ty_print_int(int64_t)`
// called with an `int` is a sign extension -- and IR does not. Declaring a
// helper from the Teyru method's own signature instead would pass a 32-bit
// argument to a function that reads 64, which is wrong output rather than a
// failed build.
//
// So the prototypes are read from tyrt.h, which is the one statement of the
// ABI: the runtime is compiled from that header, so a prototype taken from it
// cannot drift from the runtime the module is linked against. A helper that is
// not declared there is one this back end does not know how to call, and the
// program is refused rather than guessed at.

import (
	"strings"

	tyrt "github.com/teyru-lang/Teyru/internal/runtime"
)

// rtProto is one runtime function's LLVM prototype.
type rtProto struct {
	ret    string
	params []string
}

var rtProtoTable map[string]rtProto

// rtProtoOf answers with the prototype of a runtime helper, and whether the
// runtime has one at all.
func rtProtoOf(name string) (rtProto, bool) {
	if rtProtoTable == nil {
		rtProtoTable = parseRTHProtos(tyrt.Header)
		// A few helpers a generated program calls are not in the header: the
		// PrintStream print twins are declared in tyrt2.c where they are
		// implemented, and ty_class_of_cls is declared by the C back end's own
		// table because only generated code calls it. Their prototypes are
		// written there, so they are read from there rather than guessed.
		for _, t := range streamTwins {
			addProto(rtProtoTable, t.proto)
		}
		for _, nf := range nativeTable {
			addProto(rtProtoTable, nf.proto)
		}
		addProto(rtProtoTable, classOfProto)
		// The prelude's Math is written over the C library's mathematics, which
		// math.h declares: the header the runtime is built from does not name
		// them, so their standard prototypes are stated here.
		for name, p := range libmProtos {
			if _, seen := rtProtoTable[name]; !seen {
				rtProtoTable[name] = p
			}
		}
	}
	p, ok := rtProtoTable[name]
	return p, ok
}

// libmProtos are the C library's mathematics, with the prototypes the C
// standard gives them. They are C's own ABI rather than this compiler's
// invention, and the prelude's Math methods are written over them by name.
var libmProtos = map[string]rtProto{
	"acos":       {"double", []string{"double"}},
	"asin":       {"double", []string{"double"}},
	"atan":       {"double", []string{"double"}},
	"atan2":      {"double", []string{"double", "double"}},
	"ceil":       {"double", []string{"double"}},
	"copysign":   {"double", []string{"double", "double"}},
	"copysignf":  {"float", []string{"float", "float"}},
	"cos":        {"double", []string{"double"}},
	"cosh":       {"double", []string{"double"}},
	"exp":        {"double", []string{"double"}},
	"floor":      {"double", []string{"double"}},
	"fma":        {"double", []string{"double", "double", "double"}},
	"hypot":      {"double", []string{"double", "double"}},
	"log":        {"double", []string{"double"}},
	"log10":      {"double", []string{"double"}},
	"nextafter":  {"double", []string{"double", "double"}},
	"nextafterf": {"float", []string{"float", "float"}},
	"pow":        {"double", []string{"double", "double"}},
	"remainder":  {"double", []string{"double", "double"}},
	"rint":       {"double", []string{"double"}},
	"sin":        {"double", []string{"double"}},
	"sinh":       {"double", []string{"double"}},
	"sqrt":       {"double", []string{"double"}},
	"tan":        {"double", []string{"double"}},
	"tanh":       {"double", []string{"double"}},
}

// addProto reads one prototype written at a call site and adds it to the table.
func addProto(table map[string]rtProto, proto string) {
	name, p, ok := parseCProto(proto)
	if !ok {
		return
	}
	if _, seen := table[name]; !seen {
		table[name] = p
	}
}

// parseCProto reads a single C declaration, the way the call-site prototypes
// are written: `tystr *ty_net_peer_addr(int32_t)`.
func parseCProto(decl string) (string, rtProto, bool) {
	open := strings.IndexByte(decl, '(')
	if open < 0 {
		return "", rtProto{}, false
	}
	name := lastIdent(decl[:open])
	close := matchParen(decl, open)
	if name == "" || close < 0 {
		return "", rtProto{}, false
	}
	ret := strings.TrimSpace(decl[:strings.LastIndex(decl[:open], name)])
	return name, rtProto{ret: cLLVMType(ret), params: cParams(decl[open+1 : close])}, true
}

// parseRTHProtos reads every function declaration out of the header.
func parseRTHProtos(text string) map[string]rtProto {
	out := map[string]rtProto{}
	for _, decl := range cDeclarations(text) {
		// A chunk that holds an inline definition ends with the declaration
		// that follows it: everything after the definition's closing brace is
		// the declaration.
		if i := strings.LastIndexByte(decl, '}'); i >= 0 {
			decl = decl[i+1:]
		}
		decl = strings.TrimSpace(decl)
		if i := strings.Index(decl, "__attribute__"); i >= 0 {
			decl = decl[:i]
		}
		name, p, ok := parseCProto(decl)
		if !ok {
			continue
		}
		out[name] = p
	}
	return out
}

// cDeclarations splits the header into the declarations it holds.
//
// A declaration ends at the first semicolon that is not inside a brace, which is
// what tells a declaration apart from a definition: the header defines a few
// helpers inline (ty_alloc, ty_safepoint, ty_itab, ty_array_store_ref), and
// splitting on the semicolon alone would let the tail of one of those bodies
// swallow the declaration that follows it.
func cDeclarations(text string) []string {
	text = stripCComments(text)
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ';':
			if depth == 0 {
				out = append(out, text[start:i])
				start = i + 1
			}
		case '#':
			// a preprocessor line is not a declaration and runs to its newline
			j := strings.IndexByte(text[i:], '\n')
			if j < 0 {
				i = len(text)
				break
			}
			if depth == 0 {
				start = i + j + 1
			}
			i += j
		}
	}
	return out
}

func stripCComments(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		switch {
		case strings.HasPrefix(text[i:], "/*"):
			j := strings.Index(text[i+2:], "*/")
			if j < 0 {
				i = len(text)
				continue
			}
			i += j + 4
		case strings.HasPrefix(text[i:], "//"):
			j := strings.IndexByte(text[i:], '\n')
			if j < 0 {
				i = len(text)
				continue
			}
			i += j
		default:
			b.WriteByte(text[i])
			i++
		}
	}
	return b.String()
}

// matchParen answers with the index of the parenthesis closing the one at open.
func matchParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// lastIdent is the identifier a parameter list's name is: the last one before
// the parenthesis.
func lastIdent(s string) string {
	s = strings.TrimRight(s, " \t\n")
	end := len(s)
	for end > 0 && isIdentByte(s[end-1]) {
		end--
	}
	return s[end:]
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// cParams splits a parameter list and maps each parameter's type.
func cParams(list string) []string {
	list = strings.TrimSpace(list)
	if list == "" || list == "void" {
		return nil
	}
	var out []string
	depth := 0
	start := 0
	for i := 0; i <= len(list); i++ {
		switch {
		case i == len(list):
			out = append(out, cLLVMType(list[start:i]))
		case list[i] == '(':
			depth++
		case list[i] == ')':
			depth--
		case list[i] == ',' && depth == 0:
			out = append(out, cLLVMType(list[start:i]))
			start = i + 1
		}
	}
	return out
}

// cLLVMType maps a C declaration's type onto the LLVM type the ABI gives it.
// Anything that names a pointer, an array or a struct the runtime defines is a
// pointer, which is all that is left once the primitives are named: the header's
// parameters are primitives, `void *`, or one of the runtime's own struct types.
func cLLVMType(c string) string {
	// a parameter is written as a type and a name; only the type matters
	t := strings.TrimSpace(cTypePart(c))
	for _, drop := range []string{"const", "restrict", "static", "inline", "volatile", "unsigned ", "signed "} {
		t = strings.ReplaceAll(t, drop, " ")
	}
	t = strings.Join(strings.Fields(t), " ")
	if t == "" {
		return "ptr"
	}
	if strings.Contains(t, "(") || strings.Contains(t, "*") || strings.Contains(t, "[") {
		return "ptr"
	}
	switch t {
	case "void":
		return "void"
	case "int8_t", "uint8_t", "char", "_Bool":
		return "i8"
	case "int16_t", "uint16_t", "short":
		return "i16"
	case "int32_t", "uint32_t", "int":
		return "i32"
	case "int64_t", "uint64_t", "long", "size_t", "ssize_t", "intptr_t", "uintptr_t":
		return "i64"
	case "float":
		return "float"
	case "double":
		return "double"
	}
	// A struct or an unknown typedef: the header hands these around by pointer,
	// and nothing here is passed by value.
	return "ptr"
}

// cTypePart drops the parameter name from a parameter's declaration: `int64_t v`
// is an int64_t, and `const char *data` is a pointer.
func cTypePart(c string) string {
	t := strings.TrimSpace(c)
	if t == "" || strings.ContainsAny(t, "([") {
		// a function pointer or an array parameter is a pointer, and its
		// spelling is not worth taking apart
		return t
	}
	end := len(t)
	for end > 0 && !isIdentByte(t[end-1]) {
		end--
	}
	head := end
	for head > 0 && isIdentByte(t[head-1]) {
		head--
	}
	if head == 0 {
		return t
	}
	return strings.TrimSpace(t[:head])
}
