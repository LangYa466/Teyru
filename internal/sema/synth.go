package sema

import (
	"github.com/LangYa466/Teyru/internal/ast"
	"github.com/LangYa466/Teyru/internal/source"
	"github.com/LangYa466/Teyru/internal/util"
)

// This file builds syntax trees for compiler-synthesized members. The bodies
// are ordinary AST, so they are type-checked and lowered exactly like code the
// user wrote; nothing here needs a private back channel into code generation.

func pos() source.Pos { return source.Pos{} }

// id builds a name reference.
func id(name string) *ast.Ident {
	return &ast.Ident{ExprBase: ast.ExprBase{Pos: pos()}, Name: name}
}

// thisField builds `this.name`, resolved as a field read.
func thisField(f *ast.Field) *ast.Select {
	return &ast.Select{
		ExprBase: ast.ExprBase{Pos: pos()},
		X:        &ast.This{ExprBase: ast.ExprBase{Pos: pos()}},
		Name:     f.Name,
		Ref:      f,
	}
}

// thisStat builds a reference to `this` of the given class.
func thisStat(cl *ast.Class) *ast.This {
	return &ast.This{ExprBase: ast.ExprBase{Pos: pos(), T: &ast.ClassType{Class: cl, Args: typeVarArgs(cl)}}}
}

// sel builds `recv.name`.
func sel(recv ast.Expr, name string) *ast.Select {
	return &ast.Select{ExprBase: ast.ExprBase{Pos: pos()}, X: recv, Name: name}
}

// callM builds a call to a known method in the same class.
func callM(recv ast.Expr, m *ast.Method, args ...ast.Expr) *ast.Call {
	return &ast.Call{ExprBase: ast.ExprBase{Pos: pos()}, Recv: recv, Name: m.Name, Args: args, Method: m}
}

// superCall builds `super.name(args)`, which binds statically to the
// superclass implementation.
func superCall(name string, args ...ast.Expr) *ast.Call {
	return &ast.Call{
		ExprBase: ast.ExprBase{Pos: pos()},
		Recv:     &ast.SuperExpr{ExprBase: ast.ExprBase{Pos: pos()}},
		Name:     name, Args: args, Super: true,
		// `super(...)` chains a constructor; `super.m(...)` does not
		ThisCtor: name == "<init>",
	}
}

// callNew builds an unqualified resolved call: the checker fills in the target.
// callNamed builds `recv.name(args...)` for the checker to resolve.
func callNamed(recv ast.Expr, name string, args ...ast.Expr) *ast.Call {
	return &ast.Call{ExprBase: ast.ExprBase{Pos: pos()}, Recv: recv, Name: name, Args: args}
}

func callNew(recv ast.Expr, name string, args ...ast.Expr) *ast.Call {
	return &ast.Call{ExprBase: ast.ExprBase{Pos: pos()}, Recv: recv, Name: name, Args: args}
}

// strLit builds a string literal.
func strLit(s string) *ast.Literal {
	return &ast.Literal{ExprBase: ast.ExprBase{Pos: pos()}, Kind: ast.LitString, Str: s}
}

// intLit builds an int literal.
func intLit(v int) *ast.Literal {
	return &ast.Literal{ExprBase: ast.ExprBase{Pos: pos()}, Kind: ast.LitInt, Int: uint64(int64(v))}
}

// nullLit builds `null`.
func nullLit() *ast.Literal {
	return &ast.Literal{ExprBase: ast.ExprBase{Pos: pos()}, Kind: ast.LitNull}
}

// boolLit builds `true`/`false`.
func boolLit(v bool) *ast.Literal {
	return &ast.Literal{ExprBase: ast.ExprBase{Pos: pos()}, Kind: ast.LitBool, Bool: v}
}

// newObj builds `new Cls(args...)`.
func newObj(cl *ast.Class, args ...ast.Expr) *ast.New {
	return &ast.New{
		ExprBase: ast.ExprBase{Pos: pos()},
		Type:     &ast.TypeExpr{Pos: pos(), Name: cl.Name, Resolved: &ast.ClassType{Class: cl}},
		Args:     args,
	}
}

// binop builds `a op b`.
func binop(op string, a, b ast.Expr) *ast.Binary {
	return &ast.Binary{ExprBase: ast.ExprBase{Pos: pos()}, Op: op, X: a, Y: b}
}

// concatStr builds `a + b` for strings.
func concatStr(parts ...ast.Expr) ast.Expr {
	if len(parts) == 0 {
		return strLit("")
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out = binop("+", out, p)
	}
	return out
}

// assignTo builds `target = value`.
func assignTo(target, value ast.Expr) *ast.Assign {
	return &ast.Assign{ExprBase: ast.ExprBase{Pos: pos()}, Op: "=", X: target, Y: value}
}

// returnOf builds `return x;`.
func returnOf(x ast.Expr) *ast.Return {
	return &ast.Return{Pos: pos(), X: x}
}

// blockOf builds a block from statements.
func blockOf(stmts ...ast.Stmt) *ast.Block {
	return &ast.Block{Pos: pos(), End: pos(), Stmts: stmts}
}

// exprStmtOf builds `x;`.
func exprStmtOf(x ast.Expr) *ast.ExprStmt {
	return &ast.ExprStmt{Pos: pos(), X: x}
}

// ifOf builds `if (cond) then else elseStmt`.
func ifOf(cond ast.Expr, then, elseStmt ast.Stmt) *ast.If {
	return &ast.If{Pos: pos(), Cond: cond, Then: then, Else: elseStmt}
}

// throwOf builds `throw x;`.
func throwOf(x ast.Expr) *ast.Throw {
	return &ast.Throw{Pos: pos(), X: x}
}

// isNull builds `x == null`.
func isNull(x ast.Expr) ast.Expr {
	return binop("==", x, nullLit())
}

// eq builds `a == b`.
func eq(a, b ast.Expr) ast.Expr { return binop("==", a, b) }

// paramOf builds a parameter with a resolved type.
func paramOf(name string, t ast.Type) *ast.Param {
	return &ast.Param{Pos: pos(), Name: name, Type: &ast.TypeExpr{Pos: pos(), Name: t.String(), Resolved: t}}
}

// newSynthMethod creates a method with a synthesized, checked body.
func (c *Checker) newSynthMethod(cl *ast.Class, name string, mods ast.Mods, result ast.Type, params []ast.Type, names []string, body *ast.Block, anno string) *ast.Method {
	m := &ast.Method{
		Name: name, Owner: cl, Mods: mods, Result: result,
		Params: params, ParamNames: names, Pos: pos(),
		Body: body, Anno: anno,
	}
	return m
}

// addSynthMethod registers a synthesized method, reporting duplicates.
func (c *Checker) addSynthMethod(cl *ast.Class, m *ast.Method) {
	for _, prev := range cl.Methods[m.Name] {
		if sameErasedParams(c, prev, m) {
			c.errf(m.Pos, "TY-TYP-0011", "duplicate method %s in %s", describeMethod(m), cl.Name)
			return
		}
	}
	m.Owner = cl
	m.VIndex = -1
	m.Selector = -1
	cl.Methods[m.Name] = append(cl.Methods[m.Name], m)
}

// addSynthCtor registers a synthesized constructor.
func (c *Checker) addSynthCtor(cl *ast.Class, m *ast.Method) {
	for _, prev := range cl.Ctors {
		if sameErasedParams(c, prev, m) {
			return // a matching constructor already exists
		}
	}
	m.Owner = cl
	m.VIndex = -1
	m.Selector = -1
	cl.Ctors = append(cl.Ctors, m)
}

// simpleCtor picks the constructor of cl that best matches the argument types.
// It is used for `new` nodes that live outside checked bodies (static
// initializers), where the ordinary resolution path does not run.
func (c *Checker) simpleCtor(cl *ast.Class, args []ast.Expr) *ast.Method {
	var fallback *ast.Method
	for _, ctor := range cl.Ctors {
		if fallback == nil {
			fallback = ctor
		}
		if len(ctor.Params) != len(args) {
			continue
		}
		ok := true
		for i, a := range args {
			at := a.GetType()
			if at == nil {
				continue
			}
			pt := ctor.Params[i]
			if util.IsPrim(at) || util.IsPrim(pt) {
				if !sameType(c.erasure(at), c.erasure(pt)) {
					ok = false
					break
				}
				continue
			}
			if !c.isSubtype(at, pt) {
				ok = false
				break
			}
		}
		if ok {
			return ctor
		}
	}
	return fallback
}

// addSynthField registers a synthesized field.
func (c *Checker) addSynthField(cl *ast.Class, f *ast.Field) {
	if prev := cl.FieldMap[f.Name]; prev != nil {
		return
	}
	f.Owner = cl
	cl.FieldMap[f.Name] = f
	cl.Fields = append(cl.Fields, f)
}

// enumLookupChain builds the `if ("RED".equals(s)) { return Cl.RED }` chain that
// finds an enum constant by name.
//
// The constants are known here, so a name is matched against them rather than
// looked up reflectively -- which is what turns the text of a web parameter or
// a JSON member into a constant. The caller appends what happens when no branch
// matches, since that differs: a bad request parameter and a bad JSON member
// are not the same failure.
func enumLookupChain(cl *ast.Class, s ast.Expr) []ast.Stmt {
	stmts := make([]ast.Stmt, 0, len(cl.EnumConsts))
	for _, ec := range cl.EnumConsts {
		stmts = append(stmts, ifOf(
			callNamed(strLit(ec.Name), "equals", s),
			blockOf(returnOf(sel(id(cl.Full), ec.Name))), nil))
	}
	return stmts
}
