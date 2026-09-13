package codegen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/LangYa466/Teyru/internal/ast"
	"github.com/LangYa466/Teyru/internal/sema"
)

// nativeFn maps a prelude native method onto a runtime helper.
//
// Convention: helpers that take or return object values use `void*`; the
// emitter casts at the call site, so the generated C stays warning-free.
type nativeFn struct {
	fn   string // C helper
	recv string // C cast applied to the receiver ("" for static methods)
}

// specialNew maps classes whose allocation is owned by the runtime.
var specialNew = map[string]string{
	"StringBuilder": "ty_sb_new",
}

var nativeTable = map[string]nativeFn{
	// ---- Object
	"Object.toString()":     {fn: "ty_object_tostring", recv: "void*"},
	"Object.hashCode()":     {fn: "ty_obj_hash", recv: "void*"},
	"Object.equals(Object)": {fn: "ty_obj_eq", recv: "void*"},
	"Object.getClass()":     {fn: "ty_class_of", recv: "void*"},
	"Class.getName()":       {fn: "ty_class_name", recv: "void*"},
	"Class.toString()":      {fn: "ty_class_name", recv: "void*"},

	// ---- String
	"String.length()":           {fn: "ty_str_len", recv: "tystr*"},
	"String.isEmpty()":          {fn: "ty_str_isempty", recv: "tystr*"},
	"String.charAt(I)":          {fn: "ty_str_charat", recv: "tystr*"},
	"String.equals(Object)":     {fn: "ty_str_eq_obj", recv: "tystr*"},
	"String.hashCode()":         {fn: "ty_str_hash", recv: "tystr*"},
	"String.indexOf(String)":    {fn: "ty_str_indexof", recv: "tystr*"},
	"String.substring(I)":       {fn: "ty_str_sub_from", recv: "tystr*"},
	"String.substring(I,I)":     {fn: "ty_str_sub", recv: "tystr*"},
	"String.toUpperCase()":      {fn: "ty_str_upper", recv: "tystr*"},
	"String.toLowerCase()":      {fn: "ty_str_lower", recv: "tystr*"},
	"String.trim()":             {fn: "ty_str_trim", recv: "tystr*"},
	"String.contains(String)":   {fn: "ty_str_contains", recv: "tystr*"},
	"String.split(String)":      {fn: "ty_str_split", recv: "tystr*"},
	"String.startsWith(String)": {fn: "ty_str_starts", recv: "tystr*"},
	"String.endsWith(String)":   {fn: "ty_str_ends", recv: "tystr*"},
	"String.replace(C,C)":       {fn: "ty_str_replace", recv: "tystr*"},
	"String.compareTo(String)":  {fn: "ty_str_cmp", recv: "tystr*"},
	"String.concat(String)":     {fn: "ty_str_concat", recv: "tystr*"},
	"String.toString()":         {fn: "ty_str_ident", recv: "tystr*"},
	// constructors are named <init> by the parser
	"String.<init>(String)":  {fn: "ty_str_copy", recv: "tystr*"},
	"String.valueOf(I)":      {fn: "ty_str_of_int"},
	"String.valueOf(J)":      {fn: "ty_str_of_long"},
	"String.valueOf(D)":      {fn: "ty_str_of_double"},
	"String.valueOf(F)":      {fn: "ty_str_of_float"},
	"String.valueOf(Z)":      {fn: "ty_str_of_bool"},
	"String.valueOf(C)":      {fn: "ty_str_of_char"},
	"String.valueOf(Object)": {fn: "ty_str_of_obj"},

	// ---- boxed primitives
	"Byte.valueOf(B)":  {fn: "ty_box_byte"},
	"Byte.byteValue()": {fn: "ty_unbox_byte", recv: "void*"},
	"Byte.hashCode()":  {fn: "ty_unbox_byte", recv: "void*"},
	"Byte.toString()":  {fn: "ty_byte_tostr", recv: "void*"},

	"Short.valueOf(S)":   {fn: "ty_box_short"},
	"Short.shortValue()": {fn: "ty_unbox_short", recv: "void*"},
	"Short.hashCode()":   {fn: "ty_unbox_short", recv: "void*"},
	"Short.toString()":   {fn: "ty_short_tostr", recv: "void*"},

	"Integer.intValue()":         {fn: "ty_unbox_int", recv: "void*"},
	"Integer.valueOf(I)":         {fn: "ty_box_int"},
	"Integer.parseInt(String)":   {fn: "ty_str_toint", recv: "tystr*"},
	"Integer.toString()":         {fn: "ty_int_tostr", recv: "void*"},
	"Integer.toString(I)":        {fn: "ty_str_of_int"},
	"Integer.hashCode()":         {fn: "ty_unbox_int", recv: "void*"},
	"Integer.equals(Object)":     {fn: "ty_int_equals", recv: "void*"},
	"Integer.compareTo(Integer)": {fn: "ty_int_compare", recv: "void*"},
	"Integer.compare(I,I)":       {fn: "ty_prim_cmp_int"},
	"Integer.max(I,I)":           {fn: "ty_max_int"},
	"Integer.min(I,I)":           {fn: "ty_min_int"},

	"Long.longValue()":       {fn: "ty_unbox_long", recv: "void*"},
	"Long.intValue()":        {fn: "ty_long_toint", recv: "void*"},
	"Long.valueOf(J)":        {fn: "ty_box_long"},
	"Long.parseLong(String)": {fn: "ty_str_tolong", recv: "tystr*"},
	"Long.toString()":        {fn: "ty_long_tostr", recv: "void*"},
	"Long.toString(J)":       {fn: "ty_str_of_long"},
	"Long.hashCode()":        {fn: "ty_long_hash", recv: "void*"},
	"Long.equals(Object)":    {fn: "ty_long_equals", recv: "void*"},
	"Long.compareTo(Long)":   {fn: "ty_long_compare_obj", recv: "void*"},
	"Long.compare(J,J)":      {fn: "ty_prim_cmp_long"},
	"Long.max(J,J)":          {fn: "ty_max_long"},
	"Long.min(J,J)":          {fn: "ty_min_long"},

	"Double.doubleValue()":       {fn: "ty_unbox_double", recv: "void*"},
	"Double.valueOf(D)":          {fn: "ty_box_double"},
	"Double.valueOf(F)":          {fn: "ty_box_double"},
	"Double.parseDouble(String)": {fn: "ty_str_todouble", recv: "tystr*"},
	"Double.toString()":          {fn: "ty_double_tostr", recv: "void*"},
	"Double.toString(D)":         {fn: "ty_str_of_double"},
	"Double.hashCode()":          {fn: "ty_double_hash", recv: "void*"},
	"Double.equals(Object)":      {fn: "ty_double_equals", recv: "void*"},
	"Double.compareTo(Double)":   {fn: "ty_double_compare_obj", recv: "void*"},
	"Double.compare(D,D)":        {fn: "ty_double_compare"},
	"Double.isNaN(D)":            {fn: "ty_isnan"},

	"Float.floatValue()":       {fn: "ty_unbox_float", recv: "void*"},
	"Float.valueOf(F)":         {fn: "ty_box_float"},
	"Float.toString()":         {fn: "ty_float_tostr", recv: "void*"},
	"Float.parseFloat(String)": {fn: "ty_str_tofloat", recv: "tystr*"},

	"Boolean.booleanValue()":       {fn: "ty_unbox_bool", recv: "void*"},
	"Boolean.valueOf(Z)":           {fn: "ty_box_bool"},
	"Boolean.toString()":           {fn: "ty_bool_tostr", recv: "void*"},
	"Boolean.parseBoolean(String)": {fn: "ty_str_tobool", recv: "tystr*"},
	"Boolean.hashCode()":           {fn: "ty_unbox_bool", recv: "void*"},
	"Boolean.equals(Object)":       {fn: "ty_bool_equals", recv: "void*"},

	"Character.charValue()":     {fn: "ty_unbox_char", recv: "void*"},
	"Character.valueOf(C)":      {fn: "ty_box_char"},
	"Character.isDigit(C)":      {fn: "ty_is_digit"},
	"Character.isLetter(C)":     {fn: "ty_is_letter"},
	"Character.isWhitespace(C)": {fn: "ty_is_space"},
	"Character.toString()":      {fn: "ty_char_tostr", recv: "void*"},

	// ---- Math
	"Math.abs(I)":   {fn: "ty_abs_int"},
	"Math.abs(J)":   {fn: "ty_abs_long"},
	"Math.abs(D)":   {fn: "ty_abs_double"},
	"Math.max(I,I)": {fn: "ty_max_int"},
	"Math.min(I,I)": {fn: "ty_min_int"},
	"Math.max(J,J)": {fn: "ty_max_long"},
	"Math.min(J,J)": {fn: "ty_min_long"},
	"Math.max(D,D)": {fn: "ty_max_double"},
	"Math.min(D,D)": {fn: "ty_min_double"},
	"Math.sqrt(D)":  {fn: "sqrt"},
	"Math.pow(D,D)": {fn: "pow"},
	"Math.floor(D)": {fn: "floor"},
	"Math.ceil(D)":  {fn: "ceil"},
	"Math.round(D)": {fn: "ty_round"},
	"Math.random()": {fn: "ty_random"},

	// ---- System
	"System.currentTimeMillis()":            {fn: "ty_millis"},
	"System.nanoTime()":                     {fn: "ty_nanos"},
	"System.exit(I)":                        {fn: "ty_exit"},
	"System.arraycopy(Object,I,Object,I,I)": {fn: "ty_arraycopy"},

	// ---- PrintStream
	"PrintStream.println()":       {fn: "ty_println_void"},
	"PrintStream.println(String)": {fn: "ty_println_str"},
	"PrintStream.println(I)":      {fn: "ty_println_int"},
	"PrintStream.println(J)":      {fn: "ty_println_int"},
	"PrintStream.println(D)":      {fn: "ty_println_double"},
	"PrintStream.println(F)":      {fn: "ty_println_float"},
	"PrintStream.println(Z)":      {fn: "ty_println_bool"},
	"PrintStream.println(C)":      {fn: "ty_println_char"},
	"PrintStream.println(Object)": {fn: "ty_println_obj"},
	"PrintStream.print(String)":   {fn: "ty_print_str"},
	"PrintStream.print(I)":        {fn: "ty_print_int"},
	"PrintStream.print(J)":        {fn: "ty_print_int"},
	"PrintStream.print(D)":        {fn: "ty_print_double"},
	"PrintStream.print(F)":        {fn: "ty_print_float"},
	"PrintStream.print(Z)":        {fn: "ty_print_bool"},
	"PrintStream.print(C)":        {fn: "ty_print_char"},
	"PrintStream.print(Object)":   {fn: "ty_print_obj"},

	// ---- java.io.IO (implicitly imported in compact source files)
	"IO.println()":       {fn: "ty_println_void"},
	"IO.println(String)": {fn: "ty_println_str"},
	"IO.println(I)":      {fn: "ty_println_int"},
	"IO.println(J)":      {fn: "ty_println_int"},
	"IO.println(D)":      {fn: "ty_println_double"},
	"IO.println(F)":      {fn: "ty_println_float"},
	"IO.println(Z)":      {fn: "ty_println_bool"},
	"IO.println(C)":      {fn: "ty_println_char"},
	"IO.println(Object)": {fn: "ty_println_obj"},
	"IO.print(String)":   {fn: "ty_print_str"},
	"IO.print(I)":        {fn: "ty_print_int"},
	"IO.print(J)":        {fn: "ty_print_int"},
	"IO.print(D)":        {fn: "ty_print_double"},
	"IO.print(F)":        {fn: "ty_print_float"},
	"IO.print(Z)":        {fn: "ty_print_bool"},
	"IO.print(C)":        {fn: "ty_print_char"},
	"IO.print(Object)":   {fn: "ty_print_obj"},
	"IO.readln()":        {fn: "ty_readln"},

	// ---- StringBuilder
	"StringBuilder.append(String)": {fn: "ty_sb_append_str", recv: "void*"},
	"StringBuilder.append(Object)": {fn: "ty_sb_append_obj", recv: "void*"},
	"StringBuilder.append(I)":      {fn: "ty_sb_append_int", recv: "void*"},
	"StringBuilder.append(J)":      {fn: "ty_sb_append_long", recv: "void*"},
	"StringBuilder.append(C)":      {fn: "ty_sb_append_char", recv: "void*"},
	"StringBuilder.append(D)":      {fn: "ty_sb_append_double", recv: "void*"},
	"StringBuilder.append(Z)":      {fn: "ty_sb_append_bool", recv: "void*"},
	"StringBuilder.toString()":     {fn: "ty_sb_tostring", recv: "void*"},
	"StringBuilder.length()":       {fn: "ty_sb_len", recv: "void*"},

	// ---- Enum
	"Enum.ordinal()":      {fn: "ty_enum_ordinal", recv: "void*"},
	"Enum.name()":         {fn: "ty_enum_name", recv: "void*"},
	"Enum.toString()":     {fn: "ty_enum_name", recv: "void*"},
	"Enum.hashCode()":     {fn: "ty_enum_ordinal", recv: "void*"},
	"Enum.equals(Object)": {fn: "ty_obj_eq", recv: "void*"},
	"Enum.compareTo(O)":   {fn: "ty_enum_compare", recv: "void*"},
}

// nativeCall renders a call to a prelude native method.
func (e *Emitter) nativeCall(m *ast.Method, recv string, args []ast.Expr) string {
	nf, ok := nativeTable[nativeKey(m)]
	if !ok {
		return "0"
	}
	var parts []string
	vals := make([]string, 0, len(args))
	for i, a := range args {
		var want ast.Type
		if i < len(m.Params) {
			want = m.Params[i]
		}
		vals = append(vals, e.coerce(e.expr(a), a.GetType(), want))
	}
	if m.IsStatic() {
		// for static natives the cast describes the first argument
		if nf.recv != "" && len(vals) > 0 {
			vals[0] = "(" + nf.recv + ")" + vals[0]
		}
	} else if nf.recv != "" {
		parts = append(parts, "("+nf.recv+")"+recv)
	}
	parts = append(parts, vals...)
	call := nf.fn + "(" + strings.Join(parts, ", ") + ")"
	if e.isRef(m.Result) {
		return "(" + e.ctype(m.Result) + ")" + call
	}
	return call
}

// nativeCallArgs is kept for the simple path used by exprStmt.
func nativeCall(m *ast.Method, args string) string {
	key := m.Owner.Name + "." + m.Name
	if fn, ok := nativeTable[key]; ok {
		return fn.fn + "(" + args + ")"
	}
	return m.Native + "(" + args + ")"
}

// nativeSignature is the C prototype of a method implemented outside the
// generated program: an instance method receives its receiver first, and every
// parameter keeps the C type of its Teyru type.
func (e *Emitter) nativeSignature(m *ast.Method) string {
	// object parameters and results are void*: the C side sees the runtime
	// representation, and the header stays independent of generated types
	ret := e.ctype(m.Result)
	if e.isRef(m.Result) {
		ret = "void *"
	}
	var params []string
	if !m.IsStatic() {
		params = append(params, "void *self")
	}
	for i, p := range m.Params {
		t := e.ctype(p)
		if e.isRef(p) {
			t = "void *"
		}
		params = append(params, t+" a"+fmt.Sprint(i))
	}
	return ret + " " + m.Native + "(" + strings.Join(params, ", ") + ")"
}

// NativeDecl describes one method a program has to implement in C. It is what
// `teyru build --native-header` writes out.
type NativeDecl struct {
	Signature string // the C prototype to define
	Method    string // the Teyru method, for the comment above it
}

// SelectorDecl names one interface method and the dispatch selector the
// compiler assigned to it, so that native code can call back into Teyru.
type SelectorDecl struct {
	Iface    string
	Method   string
	Selector int
}

// InterfaceSelectors lists every interface method of a program with its
// selector, in a stable order.
func InterfaceSelectors(p *sema.Program) []SelectorDecl {
	var out []SelectorDecl
	for _, cl := range p.Classes {
		if !cl.Builtin && !cl.IsInterface() {
			continue
		}
		if !cl.IsInterface() {
			continue
		}
		names := make([]string, 0, len(cl.Methods))
		for name := range cl.Methods {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			for _, m := range cl.Methods[name] {
				if m.Selector < 0 || m.IsStatic() || m.IsCtor {
					continue
				}
				out = append(out, SelectorDecl{Iface: cl.Name, Method: m.Name, Selector: m.Selector})
			}
		}
	}
	return out
}

// NativeDecls lists the native methods a program has to implement in C.
func NativeDecls(p *sema.Program) []NativeDecl {
	e := &Emitter{prog: p}
	return e.nativeDecls()
}

// nativeDecls lists every native method of a program that is not part of the
// standard library, in a stable order.
func (e *Emitter) nativeDecls() []NativeDecl {
	var out []NativeDecl
	seen := map[string]bool{}
	for _, cl := range e.prog.Classes {
		if cl.Builtin {
			continue
		}
		names := make([]string, 0, len(cl.Methods))
		for name := range cl.Methods {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			for _, m := range cl.Methods[name] {
				if !m.External || seen[m.Native] {
					continue
				}
				seen[m.Native] = true
				out = append(out, NativeDecl{
					Signature: e.nativeSignature(m),
					Method:    cl.Full + "." + m.Name,
				})
			}
		}
	}
	return out
}
