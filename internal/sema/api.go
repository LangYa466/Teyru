package sema

import (
	"fmt"
	"strconv"

	"github.com/teyru-lang/Teyru/internal/ast"
	"github.com/teyru-lang/Teyru/internal/util"
)

// Erased returns the runtime type of t (generics are erased at code generation).
func (p *Program) Erased(t ast.Type) ast.Type {
	if p.c == nil {
		return t
	}
	return p.c.erasure(t)
}

// AllInterfaces lists every interface implemented by cl.
func (p *Program) AllInterfaces(cl *ast.Class) []*ast.Class {
	if p.c == nil {
		return nil
	}
	return p.c.AllInterfaces(cl)
}

// InterfaceMethods lists the instance methods declared by an interface.
func (p *Program) InterfaceMethods(iface *ast.Class) []*ast.Method {
	var out []*ast.Method
	for _, name := range sortedMethodNames(iface) {
		for _, m := range iface.Methods[name] {
			if m.IsStatic() || m.IsCtor || m.SynthKind == "lambda-ctor" {
				continue
			}
			out = append(out, m)
		}
	}
	return out
}

// Implements returns the method of cl that implements iface method im.
func (p *Program) Implements(cl *ast.Class, im *ast.Method) *ast.Method {
	if p.c == nil {
		return nil
	}
	return p.c.Implementation(cl, im)
}

// SelectorOf returns the dispatch selector of an interface method.
func (p *Program) SelectorOf(m *ast.Method) int { return m.Selector }

// IsSubtype reports whether a is assignable to b without a cast.
func (p *Program) IsSubtype(a, b ast.Type) bool {
	if p.c == nil {
		return false
	}
	return p.c.isSubtype(a, b)
}

// FieldType returns a field's declared type.
func (p *Program) FieldType(f *ast.Field) ast.Type { return f.Type }

// FieldOwner returns the class declaring a field.
func (p *Program) FieldOwner(f *ast.Field) *ast.Class { return f.Owner }

// LookupClass finds a class by qualified name.
func (p *Program) LookupClass(full string) *ast.Class {
	if p.c == nil {
		return nil
	}
	return p.c.global[full]
}

// ProgramClass looks up a class from the prelude or the user program by
// simple name; code generation uses it for the implicit java.io.IO import.
func (c *Checker) programClass(name string) *ast.Class { return c.global[name] }

// Lowered returns the desugared form of an expression (property reads become
// getter calls, and so on).
func (p *Program) Lowered(x ast.Expr) (ast.Expr, bool) {
	if p.c == nil {
		return nil, false
	}
	v, ok := p.c.Props[x]
	return v, ok
}

// ConstantString returns the value of a compile-time constant String field.
// Code generation must intern it as a string object rather than inline a raw C
// string, because a Teyru string carries its class header.
func (p *Program) ConstantString(f *ast.Field) (string, bool) {
	if v, ok := f.ConstVal.(constValue); ok && v.kind == ast.LitString {
		return v.s, true
	}
	return "", false
}

// ConstantLiteral renders a compile-time constant field as a C literal.
func (p *Program) ConstantLiteral(f *ast.Field) (string, bool) {
	switch v := f.ConstVal.(type) {
	case constValue:
		switch v.kind {
		case ast.LitString:
			return `"` + escapeC(v.s) + `"`, true
		case ast.LitInt:
			return strconv.FormatInt(v.i, 10), true
		case ast.LitLong:
			// The suffix is what makes C read the literal as a long. Without
			// it a constant that fits in 32 bits is an int, and `MASK << 63`
			// then shifts at 32-bit width and prints 1 where Java prints
			// -9223372036854775808. A literal in the source carries its own
			// type (emit_expr writes `LL`); this path is the inlined value of a
			// `static final long` field, which has none of its own.
			return strconv.FormatInt(v.i, 10) + "LL", true
		case ast.LitDouble:
			return util.FloatLiteral(v.f, false), true
		case ast.LitFloat:
			return util.FloatLiteral(v.f, true), true
		}
	}
	return "", false
}

// escapeC escapes a string for a C literal.
func escapeC(s string) string {
	out := make([]byte, 0, len(s)+8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			out = append(out, '\\', '"')
		case c == '\\':
			out = append(out, '\\', '\\')
		case c == '\n':
			out = append(out, '\\', 'n')
		case c == '\t':
			out = append(out, '\\', 't')
		case c < 32 || c > 126:
			out = append(out, fmt.Sprintf("\\%03o", c)...)
		default:
			out = append(out, c)
		}
	}
	return string(out)
}

// VarargsDirect reports whether a call passes an array directly to a varargs
// method instead of individual arguments.
func (p *Program) VarargsDirect(x ast.Expr) bool {
	if p.c == nil {
		return false
	}
	return p.c.Direct[x]
}

// ConstInt evaluates a constant integer expression.
func (p *Program) ConstInt(x ast.Expr) *int64 {
	if p.c == nil {
		return nil
	}
	cv := p.c.constEval(x)
	if !cv.ok {
		return nil
	}
	v := cv.i
	return &v
}

// ArrayClass returns the synthetic class used for array values.
func (p *Program) ArrayClass() *ast.Class {
	if p.c == nil {
		return nil
	}
	return p.c.arrayClass()
}

// ObjectClass returns the root class of the hierarchy.
func (p *Program) ObjectClass() *ast.Class { return p.Builtins.Object }

// CapturedVars lists the variables a lambda or anonymous class captures, in a
// stable order shared by the checker and code generation.
func (p *Program) CapturedVars(cl *ast.Class) []*ast.Var { return capturedVars(cl) }

// WrapInOuter records that cl is an inner class holding an outer instance.
func (p *Program) OuterFieldOf(cl *ast.Class) *ast.Field { return cl.OuterField }
