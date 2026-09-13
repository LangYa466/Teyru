package codegen

import (
	"fmt"
	"os"
	"reflect"

	"github.com/LangYa466/Teyru/internal/ast"
)

// absent reports whether a node is missing, including a typed nil such as the
// else branch of an `if` without one.
func absent(x any) bool {
	if x == nil {
		return true
	}
	v := reflect.ValueOf(x)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Func, reflect.Chan:
		return v.IsNil()
	}
	return false
}

// This file decides which objects may live on the C stack instead of the heap.
//
// A local object that never leaves its method can be declared as a C local
// variable. That matters far beyond the allocation itself: once the object has
// a known address in the frame, LLVM can promote its fields to registers and
// delete the object entirely, which is what makes a loop such as
//
//	for (int i = 0 : i < 20000000 : i++) { Cell c = new Cell(i); sum += c.value() }
//
// as cheap in Teyru as it is on a JVM with escape analysis.
//
// The analysis is deliberately conservative: an object is promoted only when
// every single use of its variable is one that provably keeps the reference
// inside the frame. Anything the walker does not recognise counts as an escape.

// leakState records what is known about a method's receiver.
type leakState int8

const (
	leakUnknown leakState = iota
	leakNo
	leakYes
	// leakBusy marks a method currently being analysed: a cycle is treated as
	// leaking rather than looping forever.
	leakBusy
)

// findStackLocals returns the local variables of a method body whose object can
// be allocated on the C stack, mapped to the creation expression that fills it.
func (e *Emitter) findStackLocals(body *ast.Block) map[*ast.Var]*ast.New {
	if body == nil {
		return nil
	}
	cands := map[*ast.Var]*ast.New{}
	collectCandidates(body, cands)
	if len(cands) == 0 {
		return nil
	}
	out := map[*ast.Var]*ast.New{}
	for v, nw := range cands {
		if !e.promotable(nw) || !sameCreatedClass(v, nw) {
			debugf("%s: heap, the created class is not the declared one", v.Name)
			continue
		}
		w := &escWalk{e: e, target: v}
		w.stmt(body)
		if !w.escapes {
			out[v] = nw
			debugf("%s: on the stack", v.Name)
		}
	}
	return out
}

// collectCandidates finds `T x = new T(...)` in the body, including in nested
// blocks, and ignoring nested methods, which have their own frames.
func collectCandidates(s ast.Stmt, out map[*ast.Var]*ast.New) {
	switch v := s.(type) {
	case *ast.Block:
		for _, st := range v.Stmts {
			collectCandidates(st, out)
		}
	case *ast.LocalVar:
		for _, d := range v.Vars {
			nw, ok := d.Init.(*ast.New)
			if !ok || d.Sym == nil || len(v.Vars) != 1 {
				continue
			}
			out[d.Sym] = nw
		}
	case *ast.If:
		collectCandidates(v.Then, out)
		if v.Else != nil {
			collectCandidates(v.Else, out)
		}
	case *ast.While:
		collectCandidates(v.Body, out)
	case *ast.DoWhile:
		collectCandidates(v.Body, out)
	case *ast.For:
		for _, i := range v.Init {
			collectCandidates(i, out)
		}
		collectCandidates(v.Body, out)
	case *ast.ForEach:
		collectCandidates(v.Body, out)
	case *ast.Try:
		for _, r := range v.Resources {
			collectCandidates(r, out)
		}
		collectCandidates(v.Body, out)
		for _, c := range v.Catches {
			collectCandidates(c.Body, out)
		}
		if v.Finally != nil {
			collectCandidates(v.Finally, out)
		}
	case *ast.Switch:
		for _, cs := range v.Cases {
			for _, st := range cs.Body {
				collectCandidates(st, out)
			}
		}
	case *ast.Sync:
		collectCandidates(v.Body, out)
	case *ast.Labeled:
		collectCandidates(v.Body, out)
	}
}

// sameCreatedClass reports whether a variable is declared with exactly the
// class it is created as. A variable of a supertype or interface could hold a
// different object, and the C local has to be the right size.
func sameCreatedClass(v *ast.Var, nw *ast.New) bool {
	declared, ok := v.Type.(*ast.ClassType)
	if !ok {
		return false
	}
	created, ok := nw.GetType().(*ast.ClassType)
	if !ok {
		return false
	}
	return declared.Class == created.Class
}

// promotable reports whether an object created by this expression can be a C
// local at all: the class must have a struct the compiler lays out, and the
// constructor must be one the compiler calls directly.
func (e *Emitter) promotable(nw *ast.New) bool {
	ct, ok := e.prog.Erased(nw.GetType()).(*ast.ClassType)
	if !ok {
		return false
	}
	cl := ct.Class
	if cl == nil || cl.Special != "" || cl.Anon || cl.Name == "" {
		return false
	}
	if nw.Body != nil || nw.Ctor == nil {
		return false
	}
	if _, special := specialNew[cl.Name]; special {
		return false
	}
	if _, native := nativeTable[nativeKey(nw.Ctor)]; native {
		return false
	}
	return true
}

// ---------------------------------------------------------------- walker

// escWalk reports whether a variable's object can be reached after the method
// returns.
type escWalk struct {
	e       *Emitter
	target  *ast.Var
	escapes bool
	reason  string
}

// mark records the reason the object was judged to escape.
func (w *escWalk) mark(why string) {
	if w.escapes {
		return
	}
	w.escapes = true
	w.reason = why
	debugf("%s: heap, %s", w.target.Name, why)
}

// debugf reports an escape decision when TEYRU_ESC_DEBUG is set.
func debugf(format string, args ...any) {
	if os.Getenv("TEYRU_ESC_DEBUG") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "escape: "+format+"\n", args...)
}

func (w *escWalk) isTarget(x ast.Expr) bool {
	id, ok := x.(*ast.Ident)
	if !ok {
		return false
	}
	v, ok := id.Ref.(*ast.Var)
	return ok && v == w.target
}

func (w *escWalk) stmt(s ast.Stmt) {
	if absent(s) {
		return
	}
	switch v := s.(type) {
	case nil:
	case *ast.Block:
		for _, st := range v.Stmts {
			w.stmt(st)
		}
	case *ast.Empty, *ast.Break, *ast.Continue:
	case *ast.LocalVar:
		for _, d := range v.Vars {
			w.expr(d.Init)
		}
	case *ast.ExprStmt:
		w.expr(v.X)
	case *ast.If:
		w.expr(v.Cond)
		w.stmt(v.Then)
		w.stmt(v.Else)
	case *ast.While:
		w.expr(v.Cond)
		w.stmt(v.Body)
	case *ast.DoWhile:
		w.stmt(v.Body)
		w.expr(v.Cond)
	case *ast.For:
		for _, i := range v.Init {
			w.stmt(i)
		}
		w.expr(v.Cond)
		for _, u := range v.Update {
			w.expr(u)
		}
		w.stmt(v.Body)
	case *ast.ForEach:
		w.expr(v.X)
		w.stmt(v.Body)
	case *ast.Return, *ast.Throw, *ast.Yield:
		w.expr(stmtValue(s))
	case *ast.Try:
		for _, r := range v.Resources {
			w.stmt(r)
		}
		w.stmt(v.Body)
		for _, c := range v.Catches {
			w.stmt(c.Body)
		}
		w.stmt(v.Finally)
	case *ast.Switch:
		w.expr(v.X)
		for _, cs := range v.Cases {
			w.expr(cs.Guard)
			for _, st := range cs.Body {
				w.stmt(st)
			}
		}
	case *ast.Sync:
		w.expr(v.Lock)
		w.stmt(v.Body)
	case *ast.Labeled:
		w.stmt(v.Body)
	case *ast.Assert:
		w.expr(v.Cond)
		w.expr(v.Msg)
	default:
		// an unhandled statement may keep a reference: assume the worst
		w.mark(fmt.Sprintf("unhandled statement %T", s))
	}
}

func stmtValue(s ast.Stmt) ast.Expr {
	switch v := s.(type) {
	case *ast.Return:
		return v.X
	case *ast.Throw:
		return v.X
	case *ast.Yield:
		return v.X
	}
	return nil
}

// receiver walks an expression that is only read through: reading a field of
// the object does not let the object itself escape.
func (w *escWalk) receiver(x ast.Expr) {
	if w.isTarget(x) {
		return
	}
	w.expr(x)
}

func (w *escWalk) expr(x ast.Expr) {
	if absent(x) {
		return
	}
	switch v := x.(type) {
	case nil:
	case *ast.Ident:
		if w.isTarget(v) {
			w.escapes = true
		}
	case *ast.Select:
		if w.isTarget(v.X) {
			// Reading a field of the object is safe only when the field cannot
			// hold the object again. `c.next` where next is a Cell is the object
			// itself, and handing that to anybody lets it outlive the frame.
			if w.canHoldTarget(v) {
				w.mark("read a reference field that can hold the object")
			}
			return
		}
		w.expr(v.X)
	case *ast.Index:
		w.expr(v.X)
		w.expr(v.Index)
	case *ast.Call:
		if w.isTarget(v.Recv) {
			// handing the object to a method is safe only if that method
			// cannot let its receiver outlive the call
			if w.e.leaksThis(v.Method) {
				w.mark("passed to " + callName(v))
			}
		} else {
			w.receiver(v.Recv)
		}
		for _, a := range v.Args {
			w.expr(a)
		}
		w.closureTargets(v.Args)
	case *ast.New:
		for _, a := range v.Args {
			w.expr(a)
		}
		w.receiver(v.Outer)
		w.closureTargets(v.Args)
		if v.Body != nil && w.capturesTarget(v.GetType()) {
			w.mark("captured by an anonymous class")
		}
		_ = v
	case *ast.NewArray:
		for _, d := range v.Dims {
			w.expr(d)
		}
		w.expr(v.Init)
	case *ast.ArrayInit:
		for _, e := range v.Elems {
			w.expr(e)
		}
	case *ast.Unary:
		// ++/-- write back, which can only happen through a field or an index
		w.receiver(v.X)
	case *ast.Binary:
		w.expr(v.X)
		w.expr(v.Y)
	case *ast.Assign:
		w.assign(v)
	case *ast.Cond:
		w.expr(v.C)
		w.expr(v.X)
		w.expr(v.Y)
	case *ast.Cast:
		w.expr(v.X)
	case *ast.Conv:
		w.expr(v.X)
	case *ast.InstanceOf:
		w.receiver(v.X)
	case *ast.This, *ast.SuperExpr, *ast.ClassLit, *ast.Literal:
	case *ast.Lambda:
		if w.lambdaCaptures(v) {
			w.escapes = true
		}
		if b, ok := v.Body.(*ast.Block); ok {
			// the lambda body runs elsewhere; only its captures matter, and a
			// capture of another local says nothing about this object
			_ = b
		}
	case *ast.MethodRef:
		w.expr(v.X)
		if v.Lam != nil && w.lambdaCaptures(v.Lam) {
			w.escapes = true
		}
	case *ast.SwitchExpr:
		w.stmt(v.S)
	default:
		w.mark(fmt.Sprintf("unhandled expression %T", x))
	}
}

// canHoldTarget reports whether the value of a field read could be the object
// itself: the field's type is the object's class, one of its supertypes, or an
// interface or type variable it could satisfy.
func (w *escWalk) canHoldTarget(sel *ast.Select) bool {
	f, ok := sel.Ref.(*ast.Field)
	if !ok || f == nil {
		return true // unknown: assume it could
	}
	if !w.e.isRef(f.Type) {
		return false
	}
	return w.e.prog.IsSubtype(sel.GetType(), w.target.Type) || w.e.prog.IsSubtype(w.target.Type, sel.GetType())
}

// assign walks `X = Y` (or a compound assignment).
func (w *escWalk) assign(v *ast.Assign) {
	// the destination is written through: `c.f = x` keeps the object in place
	w.receiver(assignTarget(v.X))
	if w.isTarget(v.Y) {
		// storing the object itself is fine only when it is stored into itself
		sel, ok := v.X.(*ast.Select)
		if !ok || !w.isTarget(sel.X) {
			w.mark("stored into another object")
		}
	} else {
		w.expr(v.Y)
	}
}

func assignTarget(x ast.Expr) ast.Expr {
	if sel, ok := x.(*ast.Select); ok {
		return sel.X
	}
	return x
}

// closureTargets reports an escape when a lambda or method reference passed as
// an argument captures the object.
func (w *escWalk) closureTargets(args []ast.Expr) {
	for _, a := range args {
		switch v := a.(type) {
		case *ast.Lambda:
			if w.lambdaCaptures(v) {
				w.mark("captured by a lambda")
			}
		case *ast.MethodRef:
			if v.Lam != nil && w.lambdaCaptures(v.Lam) {
				w.mark("captured by a method reference")
			}
		}
	}
}

func callName(v *ast.Call) string {
	if v.Method == nil {
		return v.Name + " (unresolved)"
	}
	owner := ""
	if v.Method.Owner != nil {
		owner = v.Method.Owner.Name + "."
	}
	return owner + v.Method.Name
}

func (w *escWalk) lambdaCaptures(lam *ast.Lambda) bool {
	for _, c := range lam.Captures {
		if c == w.target {
			return true
		}
	}
	for _, c := range lam.PreCaptures {
		if c == w.target {
			return true
		}
	}
	return false
}

// capturesTarget reports whether an anonymous class captured the object.
func (w *escWalk) capturesTarget(t ast.Type) bool {
	ct, ok := t.(*ast.ClassType)
	if !ok || ct.Class == nil {
		return true
	}
	for v := range ct.Class.CapFields {
		if v == w.target {
			return true
		}
	}
	return true
}

// ---------------------------------------------------------------- this

// leaksThis reports whether a method can make its receiver outlive the call,
// by storing it, returning it, capturing it or handing it to another method
// that does.
func (e *Emitter) leaksThis(m *ast.Method) bool {
	if m == nil {
		return true
	}
	switch e.leakCache[m] {
	case leakNo:
		return false
	case leakYes, leakBusy:
		return true
	}
	e.leakCache[m] = leakBusy
	leak := e.computeleaksThis(m)
	if leak {
		e.leakCache[m] = leakYes
	} else {
		e.leakCache[m] = leakNo
	}
	return leak
}

func (e *Emitter) computeleaksThis(m *ast.Method) bool {
	body := bodyOf(m)
	if body == nil {
		// a native method is implemented by the runtime, which never retains a
		// receiver; anything else without a body may do anything
		return !m.Mods.Has(ast.ModNative)
	}
	w := &thisWalk{e: e, static: m.IsStatic()}
	w.stmt(body)
	return w.leaks
}

// thisWalk reports whether `this` can outlive the method being walked.
type thisWalk struct {
	e      *Emitter
	static bool
	leaks  bool
}

func (w *thisWalk) isThis(x ast.Expr) bool {
	switch x.(type) {
	case *ast.This, *ast.SuperExpr:
		return !w.static
	}
	return false
}

func (w *thisWalk) stmt(s ast.Stmt) {
	if absent(s) {
		return
	}
	switch v := s.(type) {
	case nil:
	case *ast.Block:
		for _, st := range v.Stmts {
			w.stmt(st)
		}
	case *ast.Empty, *ast.Break, *ast.Continue:
	case *ast.LocalVar:
		for _, d := range v.Vars {
			w.expr(d.Init)
		}
	case *ast.ExprStmt:
		w.expr(v.X)
	case *ast.If:
		w.expr(v.Cond)
		w.stmt(v.Then)
		w.stmt(v.Else)
	case *ast.While:
		w.expr(v.Cond)
		w.stmt(v.Body)
	case *ast.DoWhile:
		w.stmt(v.Body)
		w.expr(v.Cond)
	case *ast.For:
		for _, i := range v.Init {
			w.stmt(i)
		}
		w.expr(v.Cond)
		for _, u := range v.Update {
			w.expr(u)
		}
		w.stmt(v.Body)
	case *ast.ForEach:
		w.expr(v.X)
		w.stmt(v.Body)
	case *ast.Return, *ast.Throw, *ast.Yield:
		w.expr(stmtValue(s))
	case *ast.Try:
		for _, r := range v.Resources {
			w.stmt(r)
		}
		w.stmt(v.Body)
		for _, c := range v.Catches {
			w.stmt(c.Body)
		}
		w.stmt(v.Finally)
	case *ast.Switch:
		w.expr(v.X)
		for _, cs := range v.Cases {
			w.expr(cs.Guard)
			for _, st := range cs.Body {
				w.stmt(st)
			}
		}
	case *ast.Sync:
		// locking on this does not retain it
		w.receiver(v.Lock)
		w.stmt(v.Body)
	case *ast.Labeled:
		w.stmt(v.Body)
	case *ast.Assert:
		w.expr(v.Cond)
		w.expr(v.Msg)
	case *ast.LocalClass:
		w.leaks = true
	default:
		w.leaks = true
	}
}

// receiver walks the target of a field access or a lock: reading through this
// does not leak it.
func (w *thisWalk) receiver(x ast.Expr) {
	if w.isThis(x) {
		return
	}
	w.expr(x)
}

func (w *thisWalk) expr(x ast.Expr) {
	if absent(x) {
		return
	}
	switch v := x.(type) {
	case nil:
	case *ast.This, *ast.SuperExpr:
		if w.isThis(v) {
			w.leaks = true
		}
	case *ast.Ident:
		// a bare name is a local, a parameter, a static, or a field read
		// through this; none of them hands the receiver out
	case *ast.Select:
		w.receiver(v.X)
	case *ast.Index:
		w.expr(v.X)
		w.expr(v.Index)
	case *ast.Call:
		if v.Recv == nil {
			// an unqualified call is a call on this
			if !v.Static && w.e.leaksThis(v.Method) {
				w.leaks = true
			}
		} else if w.isThis(v.Recv) {
			if w.e.leaksThis(v.Method) {
				w.leaks = true
			}
		} else {
			w.receiver(v.Recv)
		}
		for _, a := range v.Args {
			w.expr(a)
		}
	case *ast.New:
		for _, a := range v.Args {
			w.expr(a)
		}
		w.receiver(v.Outer)
		if v.Body != nil {
			// an anonymous class holds a reference to its enclosing instance
			w.leaks = true
		}
	case *ast.NewArray:
		for _, d := range v.Dims {
			w.expr(d)
		}
		w.expr(v.Init)
	case *ast.ArrayInit:
		for _, el := range v.Elems {
			w.expr(el)
		}
	case *ast.Unary:
		w.receiver(v.X)
	case *ast.Binary:
		w.expr(v.X)
		w.expr(v.Y)
	case *ast.Assign:
		w.receiver(assignTarget(v.X))
		w.expr(v.Y)
	case *ast.Cond:
		w.expr(v.C)
		w.expr(v.X)
		w.expr(v.Y)
	case *ast.Cast, *ast.Conv:
		w.expr(exprInner(x))
	case *ast.InstanceOf:
		w.receiver(v.X)
	case *ast.ClassLit, *ast.Literal:
	case *ast.Lambda:
		if v.CapThis || lambdaMentionsThis(v) {
			w.leaks = true
		}
	case *ast.MethodRef:
		w.expr(v.X)
		if v.Lam != nil && (v.Lam.CapThis || lambdaMentionsThis(v.Lam)) {
			w.leaks = true
		}
	case *ast.SwitchExpr:
		w.stmt(v.S)
	default:
		w.leaks = true
	}
}

func exprInner(x ast.Expr) ast.Expr {
	switch v := x.(type) {
	case *ast.Cast:
		return v.X
	case *ast.Conv:
		return v.X
	}
	return nil
}

// lambdaMentionsThis reports whether a lambda body refers to this at all.
func lambdaMentionsThis(lam *ast.Lambda) bool {
	found := false
	var walkS func(ast.Stmt)
	var walkE func(ast.Expr)
	walkE = func(x ast.Expr) {
		switch v := x.(type) {
		case nil:
		case *ast.This, *ast.SuperExpr:
			found = true
		case *ast.Select:
			walkE(v.X)
		case *ast.Index:
			walkE(v.X)
			walkE(v.Index)
		case *ast.Call:
			walkE(v.Recv)
			for _, a := range v.Args {
				walkE(a)
			}
		case *ast.New:
			for _, a := range v.Args {
				walkE(a)
			}
			walkE(v.Outer)
		case *ast.NewArray:
			for _, d := range v.Dims {
				walkE(d)
			}
			walkE(v.Init)
		case *ast.ArrayInit:
			for _, e := range v.Elems {
				walkE(e)
			}
		case *ast.Unary:
			walkE(v.X)
		case *ast.Binary:
			walkE(v.X)
			walkE(v.Y)
		case *ast.Assign:
			walkE(v.X)
			walkE(v.Y)
		case *ast.Cond:
			walkE(v.C)
			walkE(v.X)
			walkE(v.Y)
		case *ast.Cast, *ast.Conv:
			walkE(exprInner(x))
		case *ast.InstanceOf:
			walkE(v.X)
		case *ast.SwitchExpr:
			walkS(v.S)
		}
	}
	walkS = func(s ast.Stmt) {
		switch v := s.(type) {
		case nil:
		case *ast.Block:
			for _, st := range v.Stmts {
				walkS(st)
			}
		case *ast.ExprStmt:
			walkE(v.X)
		case *ast.LocalVar:
			for _, d := range v.Vars {
				walkE(d.Init)
			}
		case *ast.If:
			walkE(v.Cond)
			walkS(v.Then)
			walkS(v.Else)
		case *ast.While:
			walkE(v.Cond)
			walkS(v.Body)
		case *ast.DoWhile:
			walkS(v.Body)
			walkE(v.Cond)
		case *ast.For:
			for _, i := range v.Init {
				walkS(i)
			}
			walkE(v.Cond)
			for _, u := range v.Update {
				walkE(u)
			}
			walkS(v.Body)
		case *ast.ForEach:
			walkE(v.X)
			walkS(v.Body)
		case *ast.Return, *ast.Throw, *ast.Yield:
			walkE(stmtValue(s))
		case *ast.Try:
			for _, r := range v.Resources {
				walkS(r)
			}
			walkS(v.Body)
			for _, c := range v.Catches {
				walkS(c.Body)
			}
			walkS(v.Finally)
		case *ast.Switch:
			walkE(v.X)
			for _, cs := range v.Cases {
				walkE(cs.Guard)
				for _, st := range cs.Body {
					walkS(st)
				}
			}
		case *ast.Sync:
			walkE(v.Lock)
			walkS(v.Body)
		case *ast.Labeled:
			walkS(v.Body)
		case *ast.Assert:
			walkE(v.Cond)
			walkE(v.Msg)
		}
	}
	switch b := lam.Body.(type) {
	case ast.Expr:
		walkE(b)
	case *ast.Block:
		walkS(b)
	}
	return found
}
