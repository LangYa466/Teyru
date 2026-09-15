package codegen

// Closures: what a lambda, a method reference and a class declared inside a
// method are made of.
//
// The model is the C back end's, and it is small: a capture is a field on a
// synthesized class. The lambda's class holds one `cap_<name>` field per
// variable its body reads from the enclosing scope, laid out after the instance
// fields (util.FieldLayout takes them as its second argument for exactly this
// reason), and the closure object is allocated at the site where the lambda is
// written and filled in there. The body of the lambda then reads a captured
// variable through `this->cap_<name>` like any other field -- which is where the
// `this` of a body and the `this` of the closure come apart:
//
//   - inside a lambda body, `this` is the instance the lambda was *created* in
//     (JLS 15.27.2), not the closure object. The checker records that by adding
//     a capture named `this` (aast.Lambda.CapThis) whose value the creation site
//     stores, so a body that uses the enclosing instance reads it back through
//     `this->cap_this` and an unqualified call or field name goes to that
//     instance. Without this a bare `foo()` inside a lambda would dispatch back
//     into the lambda's own method.
//   - a class declared inside a method captures the same way but receives its
//     captures as trailing constructor parameters instead of having them written
//     at the creation site, because `new` on such a class runs a constructor.
//
// What is *not* given: any guarantee of immutability. The language's rule is the
// checker's, not this file's -- whatever internal/sema accepts is what is
// captured, and a captured variable read after the closure was built sees the
// copy the closure holds. This back end does not add a rule of its own here
// because a second rule would be a second language.

import (
	"fmt"

	"github.com/teyru-lang/Teyru/internal/ast"
	"github.com/teyru-lang/Teyru/internal/util"
)

// capTypes are the types of a class's capture fields, in the order the layout
// appends them: the order capturedVars answers in, which is also the order the C
// back end lays them out; the two back ends have to agree on the offsets,
// because the collector walks them.
func (e *llvmEmitter) capTypes(cl *ast.Class) []ast.Type {
	vars := e.p.CapturedVars(cl)
	out := make([]ast.Type, 0, len(vars))
	for _, v := range vars {
		out = append(out, v.Type)
	}
	return out
}

// capThisField is the field a lambda holds the instance its body calls `this`,
// or nil when the body does not need one.
func (e *llvmEmitter) capThisField(lam *ast.Lambda) *ast.Field {
	if lam == nil || !lam.CapThis || lam.Class == nil {
		return nil
	}
	for _, f := range lam.Class.CapFields {
		if f.Name == "this" {
			return f
		}
	}
	return nil
}

// enclosureOf is the class of the instance a lambda body's `this` denotes, or
// nil when the body does not use the enclosing instance.
func (e *llvmEmitter) enclosureOf(lam *ast.Lambda) *ast.Class {
	f := e.capThisField(lam)
	if f == nil {
		return nil
	}
	if ct, ok := f.Type.(*ast.ClassType); ok {
		return ct.Class
	}
	return nil
}

// selfOperand is the operand for `this` inside the function being written: the
// closure's captured enclosing instance when the body is a lambda's and that
// instance was captured, and the function's own receiver otherwise.
func (e *llvmEmitter) selfOperand(f *fb) string {
	fd := e.capThisField(f.lam)
	if fd == nil {
		return "%this"
	}
	return e.load(f, e.capAddr(f, fd, "%this"), fd.Type).v
}

// capAddr is the address of a capture field of an object: a fresh closure at its
// creation site, or `this` inside the body that reads it.
func (e *llvmEmitter) capAddr(f *fb, fd *ast.Field, obj string) string {
	return e.fieldAddr(f, fd, obj, false)
}

// bindCaptures points a body's captured variables at the closure's fields, so
// that a name in the body resolves to `this->cap_<name>` -- the same binding the
// C back end's bindCaptures makes. It is done by putting the address in the
// function's variable map, which is what every load and store of that variable
// goes through.
func (e *llvmEmitter) bindCaptures(f *fb, cl *ast.Class) {
	for _, v := range e.p.CapturedVars(cl) {
		fd := cl.CapFields[v]
		if fd == nil {
			continue
		}
		f.locals[v] = e.capAddr(f, fd, "%this")
	}
}

// lambdaExpr builds the closure object a lambda or method reference denotes.
//
// The captures are filled in here, once, at the site where the lambda is
// written: a bound method reference's receiver is evaluated here and only here,
// so the closure holds one object rather than re-evaluating the expression on
// every call.
func (e *llvmEmitter) lambdaExpr(f *fb, lam *ast.Lambda) lval {
	if lam == nil || lam.Class == nil {
		e.refuse(noPos, "a lambda the checker did not give a closure class")
	}
	cl := lam.Class
	e.instantiate(cl)
	obj := e.allocInstance(f, cl)
	f.ins(fmt.Sprintf("store ptr @cls_%s, ptr %s", util.Mangle(cl.Full), obj))
	for _, v := range e.p.CapturedVars(cl) {
		fd := cl.CapFields[v]
		if fd == nil {
			continue
		}
		var val lval
		switch {
		case lam.RecvVar == v && lam.RecvExpr != nil:
			// the receiver of a bound method reference, evaluated once
			val = e.expr(f, lam.RecvExpr)
		case fd.Name == "this" && lam.CapThis:
			// the enclosing instance, seen from where the lambda is written
			val = value(e.selfOperand(f), fd.Type)
		default:
			val = e.load(f, f.localSlot(v), v.Type)
		}
		e.store(f, e.capAddr(f, fd, obj), fd.Type, e.coerce(f, val, fd.Type))
	}
	return value(obj, lam.GetType())
}

// capturesForNew is the trailing arguments a class declared inside a method is
// constructed with: its captures, in the order the constructor's trailing
// parameters take them.
func (e *llvmEmitter) capturesForNew(f *fb, cl *ast.Class) []lval {
	vars := e.p.CapturedVars(cl)
	out := make([]lval, 0, len(vars))
	for _, v := range vars {
		out = append(out, e.coerce(f, e.load(f, f.localSlot(v), v.Type), v.Type))
	}
	return out
}

// bindCapturesFromParams stores the captures a constructor received as its
// trailing parameters into the closure's fields, which is what lets the methods
// of a class declared inside a method read them through `this`.
func (e *llvmEmitter) bindCapturesFromParams(f *fb, cl *ast.Class, m *ast.Method) {
	for i, v := range e.p.CapturedVars(cl) {
		fd := cl.CapFields[v]
		if fd == nil {
			continue
		}
		idx := len(m.Params) + i
		e.store(f, e.capAddr(f, fd, "%this"), fd.Type,
			e.coerce(f, value(fmt.Sprintf("%%a%d", idx), v.Type), fd.Type))
	}
}

// lambdaBody writes the body of a lambda's functional method. The method's
// receiver is the closure, the class its body was *written* in is the enclosing
// one, and a return returns from the lambda -- not from whatever method the
// lambda happens to be written inside, which is what makes the result type the
// functional method's.
func (e *llvmEmitter) lambdaBody(f *fb, m *ast.Method, lam *ast.Lambda) {
	e.bindCaptures(f, m.Owner)
	switch b := lam.Body.(type) {
	case ast.Expr:
		if lam.ExprStmt {
			// a statement expression body, whose functional method returns void
			e.expr(f, b)
			f.retVoid()
		} else {
			v := e.coerce(f, e.expr(f, b), m.Result)
			f.retVal(v.v)
		}
	case *ast.Block:
		e.block(f, b)
		e.finishBody(f)
	default:
		e.refuse(noPos, "a lambda body of type %T", lam.Body)
	}
}
