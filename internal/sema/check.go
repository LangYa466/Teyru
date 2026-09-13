package sema

import (
	"fmt"
	"sort"
	"strings"

	"github.com/LangYa466/Teyru/internal/ast"
	"github.com/LangYa466/Teyru/internal/source"
	"github.com/LangYa466/Teyru/internal/util"
)

// methodCtx carries the local scope of one method body.
type methodCtx struct {
	c               *Checker
	cl              *ast.Class
	m               *ast.Method
	env             *typeEnv
	scopes          []map[string]*ast.Var
	loops           int
	sw              *ast.Switch
	lambda          *ast.Lambda
	staticImports   []*ast.Field
	staticMethods   map[string][]*ast.Method
	props           map[ast.Expr]ast.Expr
	pendingTypeArgs []ast.Type
}

func (c *Checker) checkBodies(cl *ast.Class) {
	if cl.Decl == nil {
		return
	}
	for _, mem := range cl.Decl.Members {
		switch d := mem.(type) {
		case *ast.MethodDecl:
			if d.Sym == nil || d.Body == nil {
				continue
			}
			// a compact constructor already carries the record components as
			// its parameters, so its body binds those names directly
			ctx := c.newCtx(cl, d.Sym)
			ctx.checkBlock(d.Body, false)
			if d.Sym.Result != ast.TVoid && !d.Sym.IsCtor && !endsWithReturn(d.Body) {
				ctx.errf(d.Body.End, "TY-TYP-0020", "missing return statement")
			}
		case *ast.FieldDecl:
			for _, vd := range d.Vars {
				if vd.Init == nil {
					continue
				}
				ctx := c.newCtx(cl, nil)
				var want ast.Type
				if vd.Fld != nil {
					want = vd.Fld.Type
				}
				ctx.checkExpr(vd.Init, want)
				if want != nil {
					ctx.convertTo(vd.Init, want)
				}
				if f := vd.Fld; f != nil && !f.IsProp && f.Mods.Has(ast.ModFinal) {
					f.ConstVal = c.constFieldInit(isStaticField(f), vd.Init)
				}
			}
			for _, acc := range d.Accessor {
				if acc.Body == nil || acc.Sym == nil {
					continue
				}
				ctx := c.newCtx(cl, acc.Sym)
				if f := acc.Sym.Prop; f != nil && f.Storage {
					fv := ctx.declare("field", f.Type, acc.Pos)
					fv.Field = f
				}
				ctx.checkBlock(acc.Body, false)
			}
		case *ast.InitBlock:
			ctx := c.newCtx(cl, nil)
			ctx.checkBlock(d.Body, false)
		}
	}
	// compiler-synthesized bodies (annotation processing, records, enums)
	for _, m := range sortedSynthMethods(cl) {
		if m.Body == nil || m.Checked {
			continue
		}
		m.Checked = true
		ctx := c.newCtx(cl, m)
		ctx.checkBlock(m.Body, false)
		if m.Result != ast.TVoid && !m.IsCtor && !endsWithReturn(m.Body) {
			ctx.errf(m.Body.End, "TY-TYP-0020", "missing return statement")
		}
	}
	c.checkAbstracts(cl)
}

// constFieldInit records the compile-time value of a constant variable: only a
// final field with a constant initializer may be inlined at use sites (JLS 4.12.4).
func (c *Checker) constFieldInit(isStatic bool, e ast.Expr) any {
	if !isStatic {
		return nil
	}
	cv := c.constEval(e)
	if cv.ok {
		return cv
	}
	return nil
}

func isStaticField(f *ast.Field) bool { return f.Mods.Has(ast.ModStatic) }

func endsWithReturn(b *ast.Block) bool {
	if len(b.Stmts) == 0 {
		return false
	}
	switch s := b.Stmts[len(b.Stmts)-1].(type) {
	case *ast.Return:
		return true
	case *ast.Block:
		return endsWithReturn(s)
	case *ast.If:
		return s.Else != nil && endsWithReturn(fromStmt(s.Then)) && endsWithReturn(fromStmt(s.Else))
	case *ast.Switch:
		if s.Kind == ast.SwitchType {
			return true
		}
	case *ast.Try:
		// A finally block that never completes normally decides the outcome
		// on its own; otherwise the statement exits when the body does and
		// every catch does too (JLS 14.21).
		if s.Finally != nil && exitsAlways(s.Finally) {
			return true
		}
		if !exitsAlways(s.Body) {
			return false
		}
		if len(s.Catches) == 0 {
			return true
		}
		for _, cat := range s.Catches {
			if !exitsAlways(cat.Body) {
				return false
			}
		}
		return true
	case *ast.ExprStmt:
		if _, ok := s.X.(*ast.Call); ok {
			return false
		}
	case *ast.Sync:
		return endsWithReturn(s.Body)
	case *ast.Labeled:
		return endsWithReturn(fromStmt(s.Body))
	}
	return false
}

// endsWithThrow reports whether the block always exits by throwing.
func endsWithThrow(b *ast.Block) bool {
	if len(b.Stmts) == 0 {
		return false
	}
	switch s := b.Stmts[len(b.Stmts)-1].(type) {
	case *ast.Throw:
		return true
	case *ast.Block:
		return endsWithThrow(s)
	case *ast.If:
		if s.Else != nil && exitsAlways(fromStmt(s.Then)) && exitsAlways(fromStmt(s.Else)) {
			return true
		}
	case *ast.Try:
		if s.Finally != nil && exitsAlways(s.Finally) {
			return true
		}
		if !exitsAlways(s.Body) {
			return false
		}
		if len(s.Catches) == 0 {
			return true
		}
		for _, cat := range s.Catches {
			if !exitsAlways(cat.Body) {
				return false
			}
		}
		return true
	case *ast.Sync:
		return endsWithThrow(s.Body)
	case *ast.Labeled:
		return endsWithThrow(fromStmt(s.Body))
	}
	return false
}

// exitsAlways reports whether a block always leaves through a return or a
// throw, so that code after it is unreachable.
func exitsAlways(b *ast.Block) bool {
	return endsWithReturn(b) || endsWithThrow(b)
}

func fromStmt(s ast.Stmt) *ast.Block {
	if b, ok := s.(*ast.Block); ok {
		return b
	}
	return &ast.Block{Stmts: []ast.Stmt{s}}
}

func (c *Checker) newCtx(cl *ast.Class, m *ast.Method) *methodCtx {
	ctx := &methodCtx{c: c, cl: cl, m: m, env: c.classEnv(cl)}
	if m != nil && len(m.TypeParams) > 0 {
		// the body of a generic method sees its own type parameters
		env := &typeEnv{cls: cl, tvars: map[string]*ast.TypeVar{}, parent: ctx.env, file: cl.File}
		for _, tv := range m.TypeParams {
			env.tvars[tv.Name] = tv
		}
		ctx.env = env
	}
	if cl.File != nil {
		ctx.staticImports = cl.File.StaticImports
		ctx.staticMethods = cl.File.StaticMethods
	}
	ctx.props = c.Props
	ctx.push()
	if m != nil {
		for i, p := range m.ParamNames {
			v := ctx.declare(p, m.Params[i], m.Pos)
			m.ParamVars = append(m.ParamVars, v)
		}
	}
	return ctx
}

func (ctx *methodCtx) push() { ctx.scopes = append(ctx.scopes, map[string]*ast.Var{}) }
func (ctx *methodCtx) pop()  { ctx.scopes = ctx.scopes[:len(ctx.scopes)-1] }

func (ctx *methodCtx) errf(pos source.Pos, code, format string, args ...any) {
	ctx.c.errf(pos, code, format, args...)
}

// declareUnnamed creates a binding for `_`, which is never referenced.
func (ctx *methodCtx) declareUnnamed(pos source.Pos) *ast.Var {
	return ctx.declare("_$"+fmt.Sprint(ctx.c.varID), ast.ErrorType{}, pos)
}

// declare adds a local variable to the innermost scope.
func (ctx *methodCtx) declare(name string, t ast.Type, pos source.Pos) *ast.Var {
	if name == "_" {
		// `_` is the unnamed variable (JEP 456): it never collides and is
		// never referenced, so give it a unique internal name.
		name = "_$" + fmt.Sprint(ctx.c.varID)
	}
	cur := ctx.scopes[len(ctx.scopes)-1]
	if prev := cur[name]; prev != nil {
		ctx.errf(pos, "TY-TYP-0021", "duplicate local variable %s", name)
	}
	id := ctx.c.varID
	ctx.c.varID++
	v := &ast.Var{Name: name, Type: t, Pos: pos, ID: id, Owner: ctx.m}
	if ctx.m != nil {
		ctx.m.Locals = append(ctx.m.Locals, v)
	}
	cur[name] = v
	return v
}

func (ctx *methodCtx) lookupLocal(name string) *ast.Var {
	for i := len(ctx.scopes) - 1; i >= 0; i-- {
		if v := ctx.scopes[i][name]; v != nil {
			return v
		}
	}
	return nil
}

// lookupField searches the class chain for a field, then static imports.
func (ctx *methodCtx) lookupField(name string) *ast.Field {
	for cl := ctx.cl; cl != nil; cl = cl.Outer {
		for k := cl; k != nil; {
			if f := k.FieldMap[name]; f != nil {
				return f
			}
			if !k.Resolved || k.Super == nil {
				break
			}
			k = k.Super.Class
		}
		if cl.Outer == nil {
			break
		}
	}
	return nil
}

func isStaticCtx(cl *ast.Class) bool {
	return cl.Decl != nil && cl.Decl.Implicit
}

func (ctx *methodCtx) inStatic() bool { return ctx.m == nil || ctx.m.IsStatic() }

// expectType gives the declared type of a field declarator.
// ---------------------------------------------------------------- statements

func (ctx *methodCtx) checkBlock(b *ast.Block, scoped bool) {
	if scoped {
		ctx.push()
		defer ctx.pop()
	}
	for i := 0; i < len(b.Stmts); i++ {
		// @Cleanup turns the rest of the block into a try-with-resources so the
		// resource is closed on every exit path.
		if lv, ok := b.Stmts[i].(*ast.LocalVar); ok && hasAnno(lv.Annos, "Cleanup") != nil {
			rest := append([]ast.Stmt(nil), b.Stmts[i+1:]...)
			t := &ast.Try{Pos: lv.Pos, Resources: []ast.Stmt{lv}, Body: &ast.Block{Stmts: rest}}
			// the transformation is reflected in the tree so code generation
			// emits the try-with-resources
			b.Stmts = append(b.Stmts[:i], t)
			ctx.checkTry(t)
			return
		}
		ctx.checkStmt(b.Stmts[i])
	}
}

func (ctx *methodCtx) checkStmt(s ast.Stmt) {
	c := ctx.c
	switch v := s.(type) {
	case *ast.Block:
		ctx.checkBlock(v, true)
	case *ast.Empty:
	case *ast.LocalVar:
		ctx.checkLocalVar(v)
	case *ast.LocalClass:
		cd := v.Decl
		if cd.Sym == nil {
			cl := c.declareClass(ctx.cl.File, cd, ctx.cl)
			cl.LocalOwner = ctx.m
			c.resolveHeader(cl)
			c.resolveMembers(cl)
			c.layout(cl)
			c.checkBodies(cl)
		}
	case *ast.ExprStmt:
		ctx.checkExpr(v.X, nil)
	case *ast.If:
		ctx.checkCond(v.Cond)
		ctx.checkStmt(v.Then)
		if v.Else != nil {
			ctx.checkStmt(v.Else)
		}
	case *ast.While:
		ctx.checkCond(v.Cond)
		ctx.loops++
		ctx.checkStmt(v.Body)
		ctx.loops--
	case *ast.DoWhile:
		ctx.loops++
		ctx.checkStmt(v.Body)
		ctx.loops--
		ctx.checkCond(v.Cond)
	case *ast.For:
		ctx.push()
		for _, s := range v.Init {
			ctx.checkStmt(s)
		}
		if v.Cond != nil {
			ctx.checkCond(v.Cond)
		}
		for _, u := range v.Update {
			ctx.checkExpr(u, nil)
		}
		ctx.loops++
		ctx.checkStmt(v.Body)
		ctx.loops--
		ctx.pop()
	case *ast.ForEach:
		ctx.checkForEach(v)
	case *ast.Return:
		ctx.checkReturn(v)
	case *ast.Break:
		if ctx.loops == 0 && ctx.sw == nil || v.Label != "" {
			if v.Label == "" {
				ctx.errf(v.Pos, "TY-TYP-0022", "break outside of loop or switch")
			}
		}
	case *ast.Continue:
		if ctx.loops == 0 {
			ctx.errf(v.Pos, "TY-TYP-0023", "continue outside of loop")
		}
	case *ast.Throw:
		ctx.checkExpr(v.X, nil)
		if t := v.X.GetType(); t != nil && !c.isSubtype(t, &ast.ClassType{Class: c.b.Throwable}) && !ast.IsError(t) {
			if _, isNull := t.(ast.NullType); !isNull {
				ctx.errf(v.Pos, "TY-TYP-0024", "thrown value must be a Throwable, found %s", t)
			}
		}
	case *ast.Try:
		ctx.checkTry(v)
	case *ast.Switch:
		ctx.checkSwitch(v, false)
	case *ast.Yield:
		ctx.checkExpr(v.X, nil)
	case *ast.Labeled:
		ctx.checkStmt(v.Body)
	case *ast.Assert:
		ctx.checkCond(v.Cond)
		if v.Msg != nil {
			ctx.checkExpr(v.Msg, nil)
		}
	case *ast.Sync:
		ctx.checkExpr(v.Lock, nil)
		if lt := v.Lock.GetType(); lt != nil && ast.IsPrim(lt, ast.Void) {
			ctx.errf(v.Pos, "TY-TYP-0025", "cannot synchronize on void")
		}
		ctx.checkBlock(v.Body, true)
	}
}

func (ctx *methodCtx) checkLocalVar(v *ast.LocalVar) {
	c := ctx.c
	explicit := v.Type.Name != "var" && v.Type.Name != "val"
	var base ast.Type
	if explicit {
		base = c.resolveType(ctx.env, v.Type)
	}
	if !explicit && v.Mods.Has(ast.ModFinal) {
		ctx.errf(v.Pos, "TY-TYP-0026", "local variables cannot be declared final; use 'val'")
	}
	for i, vd := range v.Vars {
		t := base
		for k := 0; k < vd.Dims; k++ {
			t = &ast.ArrayType{Elem: t}
		}
		if !explicit {
			if vd.Init == nil {
				ctx.errf(vd.Pos, "TY-TYP-0027", "'%s' requires an initializer", v.Type.Name)
				vd.Sym = ctx.declare(vd.Name, ast.ErrorType{}, vd.Pos)
				continue
			}
			ctx.checkExpr(vd.Init, nil)
			it := vd.Init.GetType()
			if isNullType(it) {
				ctx.errf(vd.Pos, "TY-TYP-0028", "'%s' cannot infer a type from null", v.Type.Name)
				it = ast.ErrorType{}
			}
			if it != nil {
				if _, isLam := vd.Init.(*ast.Lambda); isLam {
					ctx.errf(vd.Pos, "TY-TYP-0029", "'%s' cannot infer a functional interface type; declare it explicitly", v.Type.Name)
					it = ast.ErrorType{}
				}
			}
			t = it
		} else if vd.Init != nil {
			ctx.checkExpr(vd.Init, t)
			ctx.convertTo(vd.Init, t)
		}
		v2 := ctx.declare(vd.Name, t, vd.Pos)
		if v.Type.Name == "val" || v.Mods.Has(ast.ModFinal) {
			v2.Final = true
		}
		vd.Sym = v2
		_ = i
	}
}

func (ctx *methodCtx) checkForEach(v *ast.ForEach) {
	c := ctx.c
	ctx.push()
	defer ctx.pop()
	ctx.checkExpr(v.X, nil)
	xt := v.X.GetType()
	var elem ast.Type
	if arr, ok := xt.(*ast.ArrayType); ok {
		elem = arr.Elem
		v.Iterable = false
	} else {
		iter := &ast.ClassType{Class: c.b.Iterable}
		if xt != nil && c.isSubtype(xt, iter) {
			v.Iterable = true
			if ct, ok := xt.(*ast.ClassType); ok {
				if sup := c.asSuper(ct, c.b.Iterable); sup != nil && len(sup.Args) == 1 {
					elem = sup.Args[0]
				}
			}
		} else if xt != nil && !ast.IsError(xt) {
			ctx.errf(v.Var.Pos, "TY-TYP-0030", "for-each requires an array or Iterable, found %s", xt)
			elem = ast.ErrorType{}
		} else {
			elem = ast.ErrorType{}
		}
	}
	declared := v.Var.Type.Name != "var" && v.Var.Type.Name != "val"
	if declared {
		dt := c.resolveType(ctx.env, v.Var.Type)
		// `for (int v : listOfInteger)` unboxes, like any other assignment
		if !ast.IsError(elem) && !c.assignableTo(elem, dt) {
			ctx.errf(v.Var.Pos, "TY-TYP-0031", "incompatible types: %s is not assignable to %s", elem, dt)
		}
		// the element type stays what the sequence yields; code generation
		// converts it to the declared type of the loop variable
		v.Var.Sym = ctx.declare(v.Var.Name, dt, v.Var.Pos)
		v.Elem = elem
	} else {
		v.Elem = elem
		v.Var.Sym = ctx.declare(v.Var.Name, elem, v.Var.Pos)
	}
	if v.Var.Name == "_" {
		v.Var.Unnamed = true
	}
	if v.Var.Type != nil && v.Var.Type.Name == "val" {
		v.Var.Sym.Final = true
	}
	ctx.checkStmt(v.Body)
}

func (ctx *methodCtx) checkReturn(v *ast.Return) {
	m := ctx.m
	if m == nil {
		return
	}
	want := m.Result
	if m.IsCtor {
		want = ast.TVoid
	}
	if v.X == nil {
		if want != nil && !ast.IsPrim(want, ast.Void) {
			ctx.errf(v.Pos, "TY-TYP-0032", "return value required for %s", m.Name)
		}
		return
	}
	// the declared result type is the target type of a lambda or method
	// reference in the return expression
	target := want
	if target != nil && !ast.IsRef(target) {
		target = nil
	}
	ctx.checkExpr(v.X, target)
	if want == nil || ast.IsPrim(want, ast.Void) {
		if !m.IsCtor {
			ctx.errf(v.Pos, "TY-TYP-0033", "cannot return a value from a void method")
		}
		return
	}
	ctx.convertTo(v.X, want)
}

func (ctx *methodCtx) checkTry(v *ast.Try) {
	c := ctx.c
	ctx.push()
	defer ctx.pop()
	for _, r := range v.Resources {
		ctx.checkStmt(r)
		if lv, ok := r.(*ast.LocalVar); ok {
			for _, vd := range lv.Vars {
				if vd.Sym != nil {
					vd.Sym.Final = true
				}
			}
		}
	}
	ctx.checkBlock(v.Body, true)
	for _, cat := range v.Catches {
		ctx.push()
		for _, te := range cat.Types {
			t := c.resolveType(ctx.env, te)
			if !c.isSubtype(t, &ast.ClassType{Class: c.b.Throwable}) && !ast.IsError(t) {
				ctx.errf(te.Pos, "TY-TYP-0034", "catch type must be a Throwable, found %s", t)
			}
		}
		ct := c.resolveType(ctx.env, cat.Types[0])
		cat.Sym = ctx.declare(cat.Name, ct, cat.Pos)
		if cat.Name == "_" {
			cat.Unnamed = true
		}
		ctx.checkBlock(cat.Body, true)
		ctx.pop()
	}
	if v.Finally != nil {
		ctx.checkBlock(v.Finally, true)
	}
}

func (ctx *methodCtx) checkSwitch(s *ast.Switch, expr bool) {
	c := ctx.c
	ctx.checkExpr(s.X, nil)
	xt := s.X.GetType()
	hasPattern := false
	for _, cs := range s.Cases {
		if cs.Pattern != nil || cs.Null {
			hasPattern = true
		}
		if cs.Null && !ast.IsRef(xt) {
			ctx.errf(cs.Pos, "TY-TYP-0089", "'case null' requires a reference selector")
		}
	}
	switch {
	case hasPattern && ast.IsRef(xt):
		s.Kind = ast.SwitchType
	case isNumericType(xt) || ast.IsPrim(xt, ast.Char):
		s.Kind = ast.SwitchInt
	case c.isSubtype(xt, c.strType):
		s.Kind = ast.SwitchString
	case isEnumType(xt):
		s.Kind = ast.SwitchEnum
	default:
		if xt != nil && !ast.IsError(xt) {
			ctx.errf(s.Pos, "TY-TYP-0035", "switch selector must be an integral, String or enum type, found %s", xt)
		}
		s.Kind = ast.SwitchInt
	}
	seen := map[string]bool{}
	hasDefault := false
	for _, cs := range s.Cases {
		if cs.Default {
			if hasDefault {
				ctx.errf(cs.Pos, "TY-TYP-0036", "duplicate default label")
			}
			hasDefault = true
		}
		if cs.Pattern != nil {
			ctx.push()
			t := c.resolveType(ctx.env, cs.Pattern.Type)
			if prim, isPrim := t.(*ast.PrimType); isPrim {
				ctx.checkPrimitivePattern(cs.Pattern.Pos, prim, xt, cs.Pattern)
			} else if !c.isSubtype(t, xt) && !c.isSubtype(xt, t) {
				ctx.errf(cs.Pattern.Pos, "TY-TYP-0037", "incompatible pattern type %s for switch on %s", t, xt)
			}
			ctx.declarePattern(cs.Pattern, t)
		}
		if isEnumType(xt) {
			if et, ok := xt.(*ast.ClassType); ok {
				for _, l := range cs.Labels {
					if id, ok2 := l.(*ast.Ident); ok2 && id.Ref == nil {
						if f := et.Class.FieldMap[id.Name]; f != nil && f.Mods.Has(ast.ModStatic) {
							id.Ref = f
							id.SetType(f.Type)
						}
					}
				}
			}
		}
		for _, l := range cs.Labels {
			ctx.checkExpr(l, nil)
			ctx.convertTo(l, xt)
			cv := c.constEval(l)
			if !cv.ok {
				ctx.errf(l.GetPos(), "TY-TYP-0038", "case label must be a constant expression")
				continue
			}
			key := cv.s
			if cv.kind != ast.LitString {
				key = fmt.Sprintf("%d", cv.i)
				if cv.kind == ast.LitDouble || cv.kind == ast.LitFloat {
					key = fmt.Sprintf("%v", cv.f)
				}
			}
			if seen[key] {
				ctx.errf(l.GetPos(), "TY-TYP-0039", "duplicate case label")
			}
			seen[key] = true
		}
		if cs.Guard != nil {
			ctx.checkCond(cs.Guard)
		}
		prev := ctx.sw
		ctx.sw = s
		ctx.push()
		if cs.ArrowX != nil {
			ctx.checkExpr(cs.ArrowX, nil)
		}
		for _, st := range cs.Body {
			ctx.checkStmt(st)
		}
		ctx.pop()
		ctx.sw = prev
		if cs.Pattern != nil {
			ctx.pop()
		}
	}
}

func isEnumType(t ast.Type) bool {
	ct, ok := t.(*ast.ClassType)
	return ok && ct.Class.Kind == ast.KindEnum
}

// ---------------------------------------------------------------- expressions

func (ctx *methodCtx) checkCond(e ast.Expr) {
	ctx.checkExpr(e, ast.TBoolean)
	ctx.convertTo(e, ast.TBoolean)
}

func (ctx *methodCtx) checkExpr(e ast.Expr, want ast.Type) {
	c := ctx.c
	switch v := e.(type) {
	case *ast.Literal:
		switch v.Kind {
		case ast.LitInt:
			v.SetType(ast.TInt)
		case ast.LitLong:
			v.SetType(ast.TLong)
		case ast.LitFloat:
			v.SetType(ast.TFloat)
		case ast.LitDouble:
			v.SetType(ast.TDouble)
		case ast.LitChar:
			v.SetType(ast.TChar)
		case ast.LitString:
			v.SetType(c.strType)
		case ast.LitBool:
			v.SetType(ast.TBoolean)
		case ast.LitNull:
			v.SetType(ast.NullType{})
		}
	case *ast.Ident:
		ctx.checkIdent(v, want)
	case *ast.Select:
		ctx.checkSelect(v, want)
	case *ast.Index:
		ctx.checkExpr(v.X, nil)
		ctx.checkExpr(v.Index, ast.TInt)
		ctx.convertTo(v.Index, ast.TInt)
		xt := v.X.GetType()
		if arr, ok := xt.(*ast.ArrayType); ok {
			v.SetType(arr.Elem)
		} else if ast.IsError(xt) || xt == nil {
			v.SetType(ast.ErrorType{})
		} else {
			ctx.errf(v.Pos, "TY-TYP-0040", "array required, found %s", xt)
			v.SetType(ast.ErrorType{})
		}
	case *ast.Call:
		ctx.checkCall(v, want)
	case *ast.New:
		ctx.checkNew(v, want)
	case *ast.NewArray:
		ctx.checkNewArray(v)
	case *ast.ArrayInit:
		ctx.checkArrayInit(v, want)
	case *ast.Unary:
		ctx.checkUnary(v)
	case *ast.Binary:
		ctx.checkBinary(v, want)
	case *ast.Assign:
		ctx.checkAssign(v)
	case *ast.Cond:
		ctx.checkCond2(v, want)
	case *ast.Cast:
		ctx.checkExpr(v.X, nil)
		t := c.resolveType(ctx.env, v.Type)
		for i := 0; i < v.Type.Dims; i++ {
		}
		xt := v.X.GetType()
		if xt != nil && !c.isCastable(xt, t) {
			ctx.errf(v.Pos, "TY-TYP-0041", "inconvertible types: %s cannot be cast to %s", xt, t)
		}
		v.SetType(t)
	case *ast.InstanceOf:
		ctx.checkExpr(v.X, nil)
		t := c.resolveType(ctx.env, v.Type)
		if _, isPrim := t.(*ast.PrimType); isPrim && v.Binding == nil {
			// JEP 507: there is nothing to test without a binding
			ctx.errf(v.Pos, "TY-TYP-0092", "a primitive pattern needs a name to bind the value to")
		}
		if v.Binding != nil {
			xt := v.X.GetType()
			if prim, isPrim := t.(*ast.PrimType); isPrim {
				// JEP 507: a primitive type pattern asks whether the value
				// survives the conversion, not whether it has the type
				ctx.checkPrimitivePattern(v.Pos, prim, xt, v.Binding)
				if sv, ok := xt.(*ast.PrimType); ok {
					// the test reads a boxed value, so a primitive selector is
					// boxed first
					if box := c.b.Boxes[sv.Kind]; box != nil {
						v.X = ctx.convertWith(v.X, &ast.ClassType{Class: box}, xt)
					}
				}
			} else if !c.isSubtype(t, xt) && !c.isSubtype(xt, t) && !ast.IsError(xt) {
				ctx.errf(v.Pos, "TY-TYP-0042", "incompatible pattern type %s for %s", t, xt)
			}
			ctx.push()
			ctx.declarePattern(v.Binding, t)
		}
		v.SetType(ast.TBoolean)
	case *ast.Lambda:
		ctx.checkLambda(v, want)
	case *ast.MethodRef:
		ctx.checkMethodRef(v, want)
	case *ast.This:
		if v.Qual != "" {
			oc := ctx.lookupOuter(v.Qual)
			if oc == nil {
				ctx.errf(v.Pos, "TY-TYP-0043", "not an enclosing class: %s", v.Qual)
				v.SetType(ast.ErrorType{})
				return
			}
			v.Qual = oc.Full
			v.SetType(&ast.ClassType{Class: oc, Args: typeVarArgs(oc)})
			return
		}
		if ctx.lambda != nil {
			ctx.lambda.CapThis = true
		}
		v.SetType(&ast.ClassType{Class: ctx.cl, Args: typeVarArgs(ctx.cl)})
	case *ast.SuperExpr:
		if ctx.cl.Super == nil {
			ctx.errf(v.Pos, "TY-TYP-0044", "no superclass")
			v.SetType(ast.ErrorType{})
			return
		}
		v.SetType(ctx.cl.Super)
	case *ast.SwitchExpr:
		ctx.checkSwitch(v.S, true)
		var rt ast.Type
		for _, cs := range v.S.Cases {
			var t ast.Type
			switch {
			case cs.ArrowX != nil:
				t = cs.ArrowX.GetType()
			case len(cs.Body) == 1:
				if y, ok := cs.Body[0].(*ast.Yield); ok {
					t = y.X.GetType()
				} else if th, ok := cs.Body[0].(*ast.Throw); ok {
					_ = th
					continue
				} else if b, ok := cs.Body[0].(*ast.Block); ok {
					t = blockYieldType(b)
					if t == nil {
						continue
					}
				} else {
					continue
				}
			default:
				continue
			}
			if rt == nil {
				rt = t
			} else if !sameType(rt, t) {
				if c.isSubtype(t, rt) {
				} else if c.isSubtype(rt, t) {
					rt = t
				} else {
					rt = c.lub(rt, t)
				}
			}
		}
		if rt == nil {
			rt = ast.ErrorType{}
		}
		v.SetType(rt)
	case *ast.ClassLit:
		t := c.resolveType(ctx.env, v.Type)
		v.SetType(&ast.ClassType{Class: c.b.Object})
		if _, ok := t.(*ast.ClassType); ok {
			v.SetType(&ast.ClassType{Class: c.b.Object})
		}
		v.Type.Resolved = t
	default:
		ctx.errf(e.GetPos(), "TY-INT-0002", "unsupported expression %T", e)
		e.SetType(ast.ErrorType{})
	}
}

func blockYieldType(b *ast.Block) ast.Type {
	for _, s := range b.Stmts {
		if y, ok := s.(*ast.Yield); ok {
			return y.X.GetType()
		}
	}
	return nil
}

// lub returns a common supertype.
func (c *Checker) lub(a, b ast.Type) ast.Type {
	if p, ok := a.(*ast.PrimType); ok {
		if q, ok := b.(*ast.PrimType); ok {
			return numericPromote(p, q)
		}
		return c.objType
	}
	ca, ok1 := a.(*ast.ClassType)
	cb, ok2 := b.(*ast.ClassType)
	if !ok1 || !ok2 {
		if ast.IsRef(a) && ast.IsRef(b) {
			return c.objType
		}
		return ast.ErrorType{}
	}
	sup := c.asSuper(cb, ca.Class)
	if sup != nil {
		if len(ca.Args) == 0 || len(sup.Args) == 0 {
			return &ast.ClassType{Class: ca.Class}
		}
		return ca
	}
	sup = c.asSuper(ca, cb.Class)
	if sup != nil {
		if len(cb.Args) == 0 || len(sup.Args) == 0 {
			return &ast.ClassType{Class: cb.Class}
		}
		return cb
	}
	return c.objType
}

func (ctx *methodCtx) lookupOuter(name string) *ast.Class {
	for cl := ctx.cl.Outer; cl != nil; cl = cl.Outer {
		if cl.Name == name || cl.Full == name {
			return cl
		}
	}
	return nil
}

func (ctx *methodCtx) checkIdent(v *ast.Ident, want ast.Type) {
	c := ctx.c
	if v.Ref != nil {
		ctx.setRefType(v, v.Ref)
		// the reference was resolved in an enclosing scope: when the lambda
		// body reuses it, the variable still has to be captured
		if lv, ok := v.Ref.(*ast.Var); ok {
			ctx.noteCapture(lv)
		}
		return
	}
	if lv := ctx.lookupLocal(v.Name); lv != nil {
		v.Ref = lv
		v.SetType(lv.Type)
		ctx.noteCapture(lv)
		return
	}
	// enclosing method locals (local/anonymous classes)
	for cl := ctx.cl; cl != nil; cl = cl.Outer {
		if cl.LocalOwner == nil {
			continue
		}
		oc := c.newCtx(cl.Outer, cl.LocalOwner)
		oc.scopes = append(append([]map[string]*ast.Var{}, cl.LocalScopes...), oc.scopes...)
		if lv := oc.lookupLocal(v.Name); lv != nil {
			v.Ref = lv
			v.SetType(lv.Type)
			ctx.captureOuter(cl, lv)
			ctx.noteCapture(lv)
			return
		}
	}
	if f := ctx.lookupField(v.Name); f != nil {
		if ctx.inStatic() && !f.Mods.Has(ast.ModStatic) && ctx.m != nil {
			ctx.errf(v.Pos, "TY-TYP-0045", "cannot access instance field %s from a static context", v.Name)
		}
		if f.Mods.Has(ast.ModPrivate) && !sameNest(f.Owner, ctx.cl) {
			ctx.errf(v.Pos, "TY-TYP-0046", "%s has private access in %s", f.Name, f.Owner.Name)
		}
		v.Ref = f
		v.SetType(f.Type)
		ctx.rewriteProp(v, f)
		return
	}
	// static field of enclosing/imported types
	if f := ctx.lookupStaticField(v.Name); f != nil {
		v.Ref = f
		v.SetType(f.Type)
		ctx.rewriteProp(v, f)
		return
	}
	// a type name used as a qualifier for a static member
	if cl := c.lookupClassName(ctx.env, v.Name); cl != nil {
		v.Ref = cl
		v.SetType(&ast.ClassType{Class: cl})
		return
	}
	ctx.errf(v.Pos, "TY-TYP-0048", "cannot find symbol %s", v.Name)
	v.SetType(ast.ErrorType{})
}

// rewriteProp turns a field read into a getter call for native properties.
func (ctx *methodCtx) rewriteProp(v ast.Expr, f *ast.Field) {
	if !f.IsProp {
		return
	}
	if id, ok := v.(*ast.Ident); ok {
		if id.Name == "field" {
			return
		}
	}
	if f.Getter == nil {
		ctx.errf(v.GetPos(), "TY-PROP-0005", "property %s has no getter; use 'field' inside an accessor", f.Name)
		v.SetType(ast.ErrorType{})
		return
	}
	recv := ast.Expr(&ast.This{ExprBase: ast.ExprBase{Pos: v.GetPos(), T: &ast.ClassType{Class: ctx.cl, Args: typeVarArgs(ctx.cl)}}})
	if f.Mods.Has(ast.ModStatic) {
		recv = nil
	}
	ctx.props[v] = &ast.Call{ExprBase: ast.ExprBase{Pos: v.GetPos(), T: f.Type}, Recv: recv, Name: f.Getter.Name, Args: []ast.Expr{}, Method: f.Getter}
}

func (ctx *methodCtx) lookupStaticField(name string) *ast.Field {
	for _, f := range ctx.staticImports {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// addCaptureParams appends one constructor parameter per variable captured by
// an anonymous class body. The parameters come after the forwarded ones so the
// superclass call keeps its argument positions.
func (c *Checker) addCaptureParams(sub *ast.Class, ctor *ast.Method) {
	for _, v := range capturedVars(sub) {
		ctor.Params = append(ctor.Params, v.Type)
		ctor.ParamNames = append(ctor.ParamNames, v.Name)
	}
}

// CapturedVars lists the variables an anonymous class captures, in a stable
// order shared by the checker and code generation.
func capturedVars(cl *ast.Class) []*ast.Var {
	var out []*ast.Var
	for v := range cl.CapFields {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// noteCapture marks a local as captured when referenced from a lambda or inner class.
func (ctx *methodCtx) noteCapture(v *ast.Var) {
	if ctx.lambda == nil {
		return
	}
	// variables of the enclosing method are copied into the lambda object;
	// the lambda's own parameters and locals stay on the C stack
	if v.Owner == ctx.m {
		return
	}
	for _, c := range ctx.lambda.Captures {
		if c == v {
			return
		}
	}
	ctx.lambda.Captures = append(ctx.lambda.Captures, v)
	v.Captured = true
}

func (ctx *methodCtx) captureOuter(target *ast.Class, v *ast.Var) {
	// mark every class between ctx.cl and target as capturing v
	for cl := ctx.cl; cl != nil; cl = cl.Outer {
		if cl.CapFields[v] == nil {
			f := &ast.Field{Name: "_cap$" + v.Name, Type: v.Type, Mods: ast.ModPrivate | ast.ModFinal, Pos: v.Pos, Storage: true, Owner: cl}
			cl.CapFields[v] = f
		}
		if cl == target {
			break
		}
	}
}

func (ctx *methodCtx) convertWith(e ast.Expr, target, src ast.Type) ast.Expr {
	c := ctx.c
	if target == nil || src == nil || ast.IsError(target) || ast.IsError(src) {
		return e
	}
	if sameType(target, src) {
		return e
	}
	if _, ok := src.(ast.NullType); ok && ast.IsRef(target) {
		return e
	}
	if isPrimType(src) && ast.IsRef(target) {
		// boxing
		if _, ok := c.unboxed(target); ok {
			return &ast.Conv{ExprBase: ast.ExprBase{Pos: e.GetPos(), T: target}, X: e}
		}
	}
	if ast.IsRef(src) && isPrimType(target) {
		if _, ok := c.unboxed(src); ok {
			return &ast.Conv{ExprBase: ast.ExprBase{Pos: e.GetPos(), T: target}, X: e}
		}
	}
	return e
}

func isPrimType(t ast.Type) bool {
	_, ok := t.(*ast.PrimType)
	return ok
}

// convertTo checks assignability and records conversions.
func (ctx *methodCtx) convertTo(e ast.Expr, target ast.Type) {
	c := ctx.c
	src := e.GetType()
	if target == nil || src == nil || ast.IsError(target) || ast.IsError(src) {
		return
	}
	if sameType(src, target) {
		return
	}
	if _, ok := src.(ast.NullType); ok {
		if isPrimType(target) {
			ctx.errf(e.GetPos(), "TY-TYP-0049", "null is not assignable to %s", target)
		}
		return
	}
	if isPrimType(src) && isPrimType(target) {
		sp := src.(*ast.PrimType)
		tp := target.(*ast.PrimType)
		if sp.IsNumeric() && tp.IsNumeric() {
			// narrowing needs a cast, except when the source is a constant in range
			if widening(sp, tp) {
				return
			}
			if cv := c.constEval(e); cv.ok {
				if fitsConstant(cv, tp) {
					return
				}
			}
			ctx.errf(e.GetPos(), "TY-TYP-0050", "possible lossy conversion from %s to %s", sp, tp)
			return
		}
		ctx.errf(e.GetPos(), "TY-TYP-0051", "incompatible types: %s cannot be converted to %s", src, target)
		return
	}
	if isPrimType(src) && ast.IsRef(target) {
		if _, ok := c.unboxed(target); ok {
			return
		}
		if ct, ok := target.(*ast.ClassType); ok && ct.Class.Special == "Object" {
			return
		}
		ctx.errf(e.GetPos(), "TY-TYP-0051", "incompatible types: %s cannot be converted to %s", src, target)
		return
	}
	if ast.IsRef(src) && isPrimType(target) {
		if bp, ok := c.unboxed(src); ok {
			if widening(bp, target.(*ast.PrimType)) {
				return
			}
		}
		ctx.errf(e.GetPos(), "TY-TYP-0051", "incompatible types: %s cannot be converted to %s", src, target)
		return
	}
	if ast.IsRef(src) && ast.IsRef(target) {
		if c.isSubtype(src, target) {
			return
		}
		// unchecked: erasure match (List<String> -> List)
		if ct, ok := src.(*ast.ClassType); ok {
			if tt, ok2 := target.(*ast.ClassType); ok2 && tt.Class.Special != "box" {
				if c.asSuper(ct, tt.Class) != nil {
					return
				}
			}
		}
		ctx.errf(e.GetPos(), "TY-TYP-0051", "incompatible types: %s cannot be converted to %s", src, target)
	}
}

func widening(from, to *ast.PrimType) bool {
	if from.Kind == to.Kind {
		return true
	}
	order := map[ast.PrimKind]int{ast.Byte: 1, ast.Short: 2, ast.Char: 2, ast.Int: 3, ast.Long: 4, ast.Float: 5, ast.Double: 6}
	if !from.IsNumeric() || !to.IsNumeric() {
		return false
	}
	if from.Kind == ast.Char && to.Kind == ast.Short {
		return false
	}
	if from.Kind == ast.Char && to.Kind == ast.Byte {
		return false
	}
	return order[from.Kind] <= order[to.Kind]
}

func fitsConstant(cv constValue, to *ast.PrimType) bool {
	if cv.kind == ast.LitDouble || cv.kind == ast.LitFloat {
		return to.Kind == ast.Float || to.Kind == ast.Double
	}
	switch to.Kind {
	case ast.Byte:
		return cv.i >= -128 && cv.i <= 127
	case ast.Char:
		return cv.i >= 0 && cv.i <= 0xFFFF
	case ast.Short:
		return cv.i >= -32768 && cv.i <= 32767
	case ast.Int:
		return cv.i >= -2147483648 && cv.i <= 2147483647
	case ast.Long:
		return true
	case ast.Float, ast.Double:
		return true
	}
	return false
}

func isNullType(t ast.Type) bool { _, ok := t.(ast.NullType); return ok }

func (ctx *methodCtx) checkUnary(v *ast.Unary) {
	c := ctx.c
	ctx.checkExpr(v.X, nil)
	t := v.X.GetType()
	switch v.Op {
	case "!", "~":
		if v.Op == "!" {
			if t != nil && !isBooleanType(t) && !ast.IsError(t) {
				if _, ok := c.unboxed(t); !ok {
					ctx.errf(v.Pos, "TY-TYP-0052", "operator '!' cannot be applied to %s", t)
					v.SetType(ast.ErrorType{})
					return
				}
			}
			v.SetType(ast.TBoolean)
			return
		}
		if _, ok := t.(*ast.PrimType); !ok {
			ctx.errf(v.Pos, "TY-TYP-0053", "operator '~' requires an integral operand")
			v.SetType(ast.ErrorType{})
			return
		}
		p := unboxOrPrim(c, t)
		if p == nil || !p.IsIntegral() {
			ctx.errf(v.Pos, "TY-TYP-0053", "operator '~' requires an integral operand")
			v.SetType(ast.ErrorType{})
			return
		}
		v.SetType(promoteUnary(p))
	case "+", "-":
		p := unboxOrPrim(c, t)
		if p == nil || !p.IsNumeric() {
			ctx.errf(v.Pos, "TY-TYP-0054", "operator '%s' requires a numeric operand", v.Op)
			v.SetType(ast.ErrorType{})
			return
		}
		v.SetType(promoteUnary(p))
	case "++", "--":
		p := unboxOrPrim(c, t)
		if p == nil || !p.IsNumeric() {
			ctx.errf(v.Pos, "TY-TYP-0055", "operator '%s' requires a numeric operand", v.Op)
			v.SetType(ast.ErrorType{})
			return
		}
		if !ctx.assignable(v.X) {
			ctx.errf(v.Pos, "TY-TYP-0056", "cannot apply '%s' to a non-assignable expression", v.Op)
		}
		v.SetType(t)
		ctx.checkFinalAssign(v.X)
	}
}

func promoteUnary(p *ast.PrimType) ast.Type {
	switch p.Kind {
	case ast.Byte, ast.Short, ast.Char:
		return ast.TInt
	case ast.Long:
		return ast.TLong
	case ast.Float:
		return ast.TFloat
	case ast.Double:
		return ast.TDouble
	}
	return ast.TInt
}

func unboxOrPrim(c *Checker, t ast.Type) *ast.PrimType {
	if p, ok := t.(*ast.PrimType); ok {
		return p
	}
	if p, ok := c.unboxed(t); ok {
		return p
	}
	return nil
}

func (ctx *methodCtx) assignable(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.Ident, *ast.Index, *ast.This:
		return true
	case *ast.Select:
		if _, ok := v.Ref.(*ast.Field); ok {
			return true
		}
		if v.Ref == "length" {
			return false
		}
		return false
	}
	return false
}

func (ctx *methodCtx) checkFinalAssign(target ast.Expr) {
	id, ok := target.(*ast.Ident)
	if !ok {
		return
	}
	if lv, ok := id.Ref.(*ast.Var); ok && lv.Final && lv.Assigns > 0 {
		ctx.errf(target.GetPos(), "TY-TYP-0057", "cannot assign a value to final variable %s", lv.Name)
	}
	if f, ok := id.Ref.(*ast.Field); ok && f.Mods.Has(ast.ModFinal) && f.Owner != ctx.cl {
		ctx.errf(target.GetPos(), "TY-TYP-0058", "cannot assign a value to final field %s", f.Name)
	}
}

func (ctx *methodCtx) checkBinary(v *ast.Binary, want ast.Type) {
	c := ctx.c
	ctx.checkExpr(v.X, nil)
	ctx.checkExpr(v.Y, nil)
	xt, yt := v.X.GetType(), v.Y.GetType()
	switch v.Op {
	case "&&", "||":
		ctx.convertTo(v.X, ast.TBoolean)
		ctx.convertTo(v.Y, ast.TBoolean)
		v.SetType(ast.TBoolean)
		return
	case "==", "!=":
		if isPrimType(xt) && isPrimType(yt) {
			xp, yp := xt.(*ast.PrimType), yt.(*ast.PrimType)
			if xp.Kind == ast.Boolean || yp.Kind == ast.Boolean {
				if xp.Kind != yp.Kind {
					ctx.errf(v.Pos, "TY-TYP-0059", "cannot compare %s and %s", xt, yt)
				}
				v.SetType(ast.TBoolean)
				return
			}
			v.OpType = numericPromote(xp, yp)
			v.SetType(ast.TBoolean)
			return
		}
		if isPrimType(xt) != isPrimType(yt) {
			// one primitive, one reference: unbox the reference
			ctx.convertTo(v.Y, xt)
			ctx.convertTo(v.X, yt)
			if unboxOrPrim(c, xt) != nil && unboxOrPrim(c, yt) != nil {
				v.SetType(ast.TBoolean)
				return
			}
			ctx.errf(v.Pos, "TY-TYP-0059", "incompatible operand types %s and %s", xt, yt)
			v.SetType(ast.TBoolean)
			return
		}
		if !ast.IsRef(xt) || !ast.IsRef(yt) {
			ctx.errf(v.Pos, "TY-TYP-0059", "incompatible operand types %s and %s", xt, yt)
			v.SetType(ast.TBoolean)
			return
		}
		if !c.isCastable(xt, yt) && !c.isCastable(yt, xt) && !ast.IsError(xt) && !ast.IsError(yt) {
			ctx.errf(v.Pos, "TY-TYP-0060", "incomparable types: %s and %s", xt, yt)
		}
		v.SetType(ast.TBoolean)
		return
	case "<", ">", "<=", ">=":
		if isRefType(xt) {
			if bt := unboxOrPrim(c, xt); bt != nil {
				xt = bt
			}
		}
		if isRefType(yt) {
			if bt := unboxOrPrim(c, yt); bt != nil {
				yt = bt
			}
		}
		xp, ok1 := xt.(*ast.PrimType)
		yp, ok2 := yt.(*ast.PrimType)
		if !ok1 || !ok2 || !xp.IsNumeric() || !yp.IsNumeric() {
			ctx.errf(v.Pos, "TY-TYP-0061", "operator '%s' cannot be applied to %s and %s", v.Op, xt, yt)
			v.SetType(ast.ErrorType{})
			return
		}
		v.OpType = numericPromote(xp, yp)
		v.SetType(ast.TBoolean)
		return
	case "&", "|", "^":
		xp := unboxOrPrim(c, xt)
		yp := unboxOrPrim(c, yt)
		if xp == nil || yp == nil {
			ctx.errf(v.Pos, "TY-TYP-0062", "operator '%s' requires integral or boolean operands", v.Op)
			v.SetType(ast.ErrorType{})
			return
		}
		if xp.Kind == ast.Boolean || yp.Kind == ast.Boolean {
			if xp.Kind != yp.Kind {
				ctx.errf(v.Pos, "TY-TYP-0062", "operator '%s' requires both operands to be boolean", v.Op)
				v.SetType(ast.ErrorType{})
				return
			}
			v.SetType(ast.TBoolean)
			return
		}
		if !xp.IsIntegral() || !yp.IsIntegral() {
			ctx.errf(v.Pos, "TY-TYP-0062", "operator '%s' requires integral operands", v.Op)
			v.SetType(ast.ErrorType{})
			return
		}
		v.OpType = numericPromote(xp, yp)
		v.SetType(v.OpType)
		return
	case "<<", ">>", ">>>":
		xp := unboxOrPrim(c, xt)
		yp := unboxOrPrim(c, yt)
		if xp == nil || !xp.IsIntegral() || yp == nil || !yp.IsIntegral() {
			ctx.errf(v.Pos, "TY-TYP-0063", "operator '%s' requires integral operands", v.Op)
			v.SetType(ast.ErrorType{})
			return
		}
		v.OpType = promoteUnary(xp)
		v.SetType(v.OpType)
		return
	case "+":
		if c.isSubtype(xt, c.strType) || c.isSubtype(yt, c.strType) || isNullType(xt) && c.isSubtype(yt, c.strType) || isNullType(yt) && c.isSubtype(xt, c.strType) {
			v.SetType(c.strType)
			return
		}
	}
	// arithmetic
	xp := unboxOrPrim(c, xt)
	yp := unboxOrPrim(c, yt)
	if xp == nil || yp == nil || !xp.IsNumeric() || !yp.IsNumeric() {
		if ast.IsError(xt) || ast.IsError(yt) {
			v.SetType(ast.ErrorType{})
			return
		}
		ctx.errf(v.Pos, "TY-TYP-0064", "operator '%s' cannot be applied to %s and %s", v.Op, xt, yt)
		v.SetType(ast.ErrorType{})
		return
	}
	v.OpType = numericPromote(xp, yp)
	v.SetType(v.OpType)
}

func isRefType(t ast.Type) bool { return ast.IsRef(t) }

func (ctx *methodCtx) checkAssign(v *ast.Assign) {
	c := ctx.c
	ctx.checkExpr(v.X, nil)
	if f := ctx.propertyOf(v.X); f != nil {
		ctx.lowerPropAssign(v, f)
		return
	}
	if !ctx.assignable(v.X) {
		ctx.errf(v.Pos, "TY-TYP-0065", "left-hand side of an assignment must be a variable")
	}
	ctx.checkFinalAssign(v.X)
	if lv, ok := v.X.(*ast.Ident); ok {
		if vr, ok := lv.Ref.(*ast.Var); ok {
			vr.Assigns++
		}
	}
	xt := v.X.GetType()
	ctx.checkExpr(v.Y, xt)
	if v.Op == "=" {
		ctx.convertTo(v.Y, xt)
		v.SetType(xt)
		return
	}
	// compound: implicit cast back to the target type
	yt := v.Y.GetType()
	xp := unboxOrPrim(c, xt)
	yp := unboxOrPrim(c, yt)
	if v.Op == "+=" && (c.isSubtype(xt, c.strType) || isNullType(xt)) {
		v.SetType(xt)
		return
	}
	if xp == nil || yp == nil || ast.IsError(xt) || ast.IsError(yt) {
		v.SetType(ast.ErrorType{})
		return
	}
	switch {
	case xp.Kind == ast.Boolean:
		if v.Op != "&=" && v.Op != "|=" && v.Op != "^=" {
			ctx.errf(v.Pos, "TY-TYP-0066", "operator '%s' cannot be applied to boolean", v.Op)
		}
		v.SetType(xt)
	case v.Op == "<<=" || v.Op == ">>=" || v.Op == ">>>=":
		if !xp.IsIntegral() || !yp.IsIntegral() {
			ctx.errf(v.Pos, "TY-TYP-0063", "operator '%s' requires integral operands", v.Op)
		}
		v.SetType(xt)
	case v.Op == "&=" || v.Op == "|=" || v.Op == "^=":
		if !xp.IsIntegral() || !yp.IsIntegral() {
			ctx.errf(v.Pos, "TY-TYP-0062", "operator '%s' requires integral operands", v.Op)
		}
		v.SetType(xt)
	default:
		if !xp.IsNumeric() || !yp.IsNumeric() {
			ctx.errf(v.Pos, "TY-TYP-0064", "operator '%s' cannot be applied to %s and %s", v.Op, xt, yt)
			v.SetType(ast.ErrorType{})
			return
		}
		v.OpType = numericPromote(xp, yp)
		v.SetType(xt)
	}
}

// declarePattern binds the variables of a (possibly record) pattern.
func (ctx *methodCtx) declarePattern(p *ast.Param, t ast.Type) {
	if len(p.Decomp) > 0 {
		cl := recordOf(t)
		if cl == nil {
			ctx.errf(p.Pos, "TY-TYP-0087", "record pattern requires a record type, found %s", t)
			return
		}
		comps := cl.RecordComps()
		if len(comps) != len(p.Decomp) {
			ctx.errf(p.Pos, "TY-TYP-0088", "record pattern for %s needs %d components, found %d", cl.Name, len(comps), len(p.Decomp))
			return
		}
		p.Comps = comps
		for i, sub := range p.Decomp {
			ctx.declarePattern(sub, comps[i].Type)
		}
		return
	}
	if p.Unnamed {
		p.Sym = ctx.declareUnnamed(p.Pos)
		return
	}
	if p.Name != "" {
		p.Sym = ctx.declare(p.Name, t, p.Pos)
	}
}

// checkPrimitivePattern validates a primitive type pattern (JEP 507). The
// selector may be a reference type, in which case the test is a run time
// question, or a primitive whose value might convert exactly.
func (ctx *methodCtx) checkPrimitivePattern(pos source.Pos, prim *ast.PrimType, xt ast.Type, p *ast.Param) {
	if p.Name == "_" {
		ctx.errf(pos, "TY-TYP-0092", "a primitive pattern needs a name to bind the value to")
	}
	if xt == nil || ast.IsError(xt) {
		return
	}
	if sv, ok := xt.(*ast.PrimType); ok {
		if sv.Kind == ast.Boolean || prim.Kind == ast.Boolean {
			if sv.Kind != prim.Kind {
				ctx.errf(pos, "TY-TYP-0093", "boolean cannot be converted to %s", prim)
			}
		}
		return
	}
	if !ast.IsRef(xt) {
		ctx.errf(pos, "TY-TYP-0094", "primitive pattern %s needs a boxed value, found %s", prim, xt)
	}
}

// recordOf returns the record class behind a type, if any.
func recordOf(t ast.Type) *ast.Class {
	ct, ok := t.(*ast.ClassType)
	if !ok || ct.Class.Kind != ast.KindRecord {
		return nil
	}
	return ct.Class
}

// propertyOf returns the property behind an assignable expression.
func (ctx *methodCtx) propertyOf(x ast.Expr) *ast.Field {
	var ref any
	switch v := x.(type) {
	case *ast.Ident:
		ref = v.Ref
	case *ast.Select:
		ref = v.Ref
	}
	if f, ok := ref.(*ast.Field); ok && f.IsProp {
		return f
	}
	return nil
}

// lowerPropAssign rewrites `x.p = v` (and compound forms) into setter calls.
func (ctx *methodCtx) lowerPropAssign(v *ast.Assign, f *ast.Field) {
	if f.Setter == nil {
		ctx.errf(v.Pos, "TY-PROP-0007", "property %s has no setter", f.Name)
		v.SetType(f.Type)
		return
	}
	ctx.checkExpr(v.Y, f.Type)
	if v.Op != "=" {
		if f.Getter == nil {
			ctx.errf(v.Pos, "TY-PROP-0008", "property %s needs a getter for compound assignment", f.Name)
			v.SetType(f.Type)
			return
		}
		getCall := ctx.getterCall(v.X, f)
		bin := &ast.Binary{ExprBase: ast.ExprBase{Pos: v.Pos}, Op: v.Op[:len(v.Op)-1], X: getCall, Y: v.Y}
		bin.SetType(f.Type)
		v.Y = bin
	}
	recv := ctx.propReceiver(v.X, f)
	ctx.props[v] = &ast.Call{ExprBase: ast.ExprBase{Pos: v.Pos, T: f.Type}, Recv: recv, Name: f.Setter.Name, Args: []ast.Expr{v.Y}, Method: f.Setter}
	v.SetType(f.Type)
}

func (ctx *methodCtx) propReceiver(x ast.Expr, f *ast.Field) ast.Expr {
	if !f.Mods.Has(ast.ModStatic) {
		if sel, ok := x.(*ast.Select); ok {
			return sel.X
		}
	}
	return nil
}

func (ctx *methodCtx) getterCall(x ast.Expr, f *ast.Field) ast.Expr {
	return &ast.Call{ExprBase: ast.ExprBase{Pos: x.GetPos(), T: f.Type}, Recv: ctx.propReceiver(x, f), Name: f.Getter.Name, Args: []ast.Expr{}, Method: f.Getter}
}

func (ctx *methodCtx) checkCond2(v *ast.Cond, want ast.Type) {
	ctx.checkExpr(v.C, ast.TBoolean)
	ctx.convertTo(v.C, ast.TBoolean)
	ctx.checkExpr(v.X, want)
	ctx.checkExpr(v.Y, want)
	xt, yt := v.X.GetType(), v.Y.GetType()
	if xt == nil || yt == nil {
		v.SetType(ast.ErrorType{})
		return
	}
	// A conditional expression in an assignment or argument position is a
	// poly expression: when both branches fit the target type, that is its type.
	if want != nil && !ast.IsError(want) && !ast.IsPrim(want, ast.Void) {
		if ctx.c.assignableTo(xt, want) && ctx.c.assignableTo(yt, want) {
			v.SetType(want)
			return
		}
	}
	if sameType(xt, yt) {
		v.SetType(xt)
		return
	}
	ctx.convertTo(v.X, yt)
	ctx.convertTo(v.Y, xt)
	if isPrimType(xt) && isPrimType(yt) {
		v.SetType(ctx.c.lub(xt, yt))
		return
	}
	if isNullType(xt) {
		v.SetType(yt)
		return
	}
	if isNullType(yt) {
		v.SetType(xt)
		return
	}
	v.SetType(ctx.c.lub(xt, yt))
}

func (ctx *methodCtx) checkNewArray(v *ast.NewArray) {
	c := ctx.c
	elem := c.resolveType(ctx.env, v.Elem)
	for i, d := range v.Dims {
		ctx.checkExpr(d, ast.TInt)
		ctx.convertTo(d, ast.TInt)
		if cv := c.constEval(d); cv.ok && cv.i < 0 {
			ctx.errf(d.GetPos(), "TY-TYP-0067", "array dimension must be non-negative")
		}
		_ = i
	}
	if v.Init != nil {
		v.Init.Elem = elem
		ctx.checkArrayInit(v.Init, nil)
		elem = v.Init.Elem
	}
	t := elem
	for i := 0; i < len(v.Dims)+v.Extra; i++ {
		t = &ast.ArrayType{Elem: t}
	}
	v.SetType(t)
}

func (ctx *methodCtx) checkArrayInit(v *ast.ArrayInit, want ast.Type) {
	var elem ast.Type
	if want != nil {
		if arr, ok := want.(*ast.ArrayType); ok {
			elem = arr.Elem
		}
	}
	if elem == nil {
		elem = v.Elem
	}
	if elem == nil {
		// infer from first element
		for _, e := range v.Elems {
			ctx.checkExpr(e, nil)
			elem = e.GetType()
			break
		}
	}
	for _, e := range v.Elems {
		if ai, ok := e.(*ast.ArrayInit); ok {
			ai.Elem = elem
			ctx.checkArrayInit(ai, elem)
			continue
		}
		ctx.checkExpr(e, elem)
		if elem != nil {
			ctx.convertTo(e, elem)
		}
	}
	v.Elem = elem
	v.SetType(&ast.ArrayType{Elem: elem})
}

func (ctx *methodCtx) checkNew(v *ast.New, want ast.Type) {
	c := ctx.c
	if v.Outer != nil && v.Type != nil && !strings.Contains(v.Type.Name, ".") {
		// `outer.new Inner()`: the simple name is a member type of the
		// qualifier's class, not a name in the lexical scope
		ctx.checkExpr(v.Outer, nil)
		if qct, ok := c.erasure(v.Outer.GetType()).(*ast.ClassType); ok {
			if nested := qct.Class.Nested[v.Type.Name]; nested != nil {
				v.Type.Name = qct.Class.Full + "." + v.Type.Name
			}
		}
	}
	t := c.resolveType(ctx.env, v.Type)
	if v.Type.Args != nil && len(v.Type.Args) == 0 {
		if dt, ok := t.(*diamondType); ok {
			if ct, ok2 := want.(*ast.ClassType); ok2 && ct.Class == dt.Class {
				t = ct
				v.Type.Resolved = ct
			} else {
				// fall back to erasure
				t = &ast.ClassType{Class: dt.Class}
				for _, tv := range dt.Class.TypeParams {
					t.(*ast.ClassType).Args = append(t.(*ast.ClassType).Args, c.erasure(tv.Bound))
				}
				v.Type.Resolved = t
			}
		}
	}
	ct, ok := c.erasure(t).(*ast.ClassType)
	if !ok {
		ctx.errf(v.Pos, "TY-TYP-0068", "cannot instantiate %s", t)
		v.SetType(ast.ErrorType{})
		return
	}
	cl := ct.Class
	if v.Body == nil {
		if cl.Mods.Has(ast.ModAbstract) || cl.IsInterface() {
			ctx.errf(v.Pos, "TY-TYP-0069", "%s is abstract; cannot be instantiated", cl.Name)
		}
	}
	if v.Body != nil {
		if !cl.IsInterface() && !cl.Mods.Has(ast.ModAbstract) && cl.Mods.Has(ast.ModFinal) {
			ctx.errf(v.Pos, "TY-TYP-0070", "cannot extend final class %s", cl.Name)
		}
		if cl.Kind == ast.KindEnum {
			ctx.errf(v.Pos, "TY-TYP-0070", "cannot subclass enum %s", cl.Name)
		}
	}
	// resolve the constructor against the parameterised type so that type
	// arguments substitute into the constructor signature
	ctorType := ct
	if pt, ok := t.(*ast.ClassType); ok && len(pt.Args) > 0 {
		ctorType = pt
	}
	ctor := c.resolveCtor(ctx, ctorType, v, cl)
	_ = ctor
	// outer instance for inner classes
	if cl.Inner {
		if v.Outer != nil {
			ctx.checkExpr(v.Outer, nil)
		} else if !ctx.inStatic() {
			if findEnclosing(ctx.cl, cl) == nil {
				ctx.errf(v.Pos, "TY-TYP-0071", "an enclosing instance of %s is required", cl.Name)
			}
		} else {
			ctx.errf(v.Pos, "TY-TYP-0071", "an enclosing instance of %s is required", cl.Name)
		}
	}
	if v.Body != nil {
		body := v.Body
		body.Name = cl.Name + "$" + fmt.Sprint(c.anonN[cl])
		c.anonN[cl]++
		sub := c.declareClass(ctx.cl.File, body, ctx.cl)
		sub.Anon = true
		delete(ctx.cl.Nested, body.Name)
		sub.Mods |= ast.ModFinal
		// An anonymous class only carries an enclosing instance when it is
		// created in an instance context (JLS 15.9.5).
		sub.Inner = !ctx.inStatic()
		if cl.IsInterface() {
			sub.Ifaces = append(sub.Ifaces, ct)
		} else {
			sub.Super = ct
			cl.Subclasses = append(cl.Subclasses, sub)
		}
		sub.Resolved = true
		sub.LocalOwner = ctx.m
		// the body of an anonymous class sees the locals in scope where it is
		// written; the ones it actually uses become captured fields
		sub.LocalScopes = ctx.scopes
		c.resolveMembers(sub)
		// The constructor of the anonymous class mirrors the target's
		// signature and forwards to it; drop the synthesized default ctor.
		var kept []*ast.Method
		for _, k := range sub.Ctors {
			if k.SynthKind != "default-ctor" {
				kept = append(kept, k)
			}
		}
		sub.Ctors = kept
		anonCtor := &ast.Method{
			Name: "<init>", Owner: sub, IsCtor: true, Mods: ast.ModPublic,
			Result: ast.TVoid, Pos: v.Pos, SynthKind: "anon-ctor", Forward: ctor,
		}
		if ctor != nil {
			anonCtor.Params = ctor.Params
			anonCtor.ParamNames = ctor.ParamNames
			anonCtor.Varargs = ctor.Varargs
		}
		c.addCtor(sub, anonCtor)
		c.layout(sub)
		c.checkBodies(sub)
		// captured locals become extra constructor parameters, passed by the
		// `new` expression that created the class
		c.addCaptureParams(sub, anonCtor)
		t = &ast.ClassType{Class: sub}
		v.Body = body
		v.Ctor = anonCtor
	} else {
		v.Ctor = ctor
	}
	v.SetType(t)
}

// sameNest reports whether two classes belong to the same nest, that is, they
// are declared within the same top level class. Java grants access to private
// members across a whole nest (JLS 8.8.10).
func sameNest(a, b *ast.Class) bool {
	if a == nil || b == nil {
		return false
	}
	return nestHost(a) == nestHost(b)
}

// nestHost walks up to the outermost enclosing class.
func nestHost(cl *ast.Class) *ast.Class {
	for cl.Outer != nil {
		cl = cl.Outer
	}
	return cl
}

func findEnclosing(from, target *ast.Class) *ast.Class {
	for cl := from; cl != nil; cl = cl.Outer {
		if c := findClass(cl, target); c != nil {
			return c
		}
	}
	return nil
}

func findClass(scope, target *ast.Class) *ast.Class {
	for cl := scope; cl != nil; cl = cl.Outer {
		if cl == target {
			return cl
		}
	}
	// nested classes of scope
	var found *ast.Class
	var walk func(cl *ast.Class)
	walk = func(cl *ast.Class) {
		if cl == nil || found != nil {
			return
		}
		for _, n := range cl.Nested {
			if n == target {
				found = n
				return
			}
			walk(n)
		}
	}
	walk(scope)
	return found
}

// resolveCtor picks a constructor and checks arguments.
func (c *Checker) resolveCtor(ctx *methodCtx, ct *ast.ClassType, v *ast.New, cl *ast.Class) *ast.Method {
	if cl.IsInterface() {
		// interfaces have no constructors; an anonymous class implements one
		for _, a := range v.Args {
			ctx.checkExpr(a, nil)
		}
		return nil
	}
	var cands []*ast.Method
	cands = append(cands, cl.Ctors...)
	if len(cands) == 0 {
		cands = append(cands, &ast.Method{Name: "<init>", IsCtor: true, Owner: cl, Mods: ast.ModPublic, Result: ast.TVoid})
	}
	best, score := ctx.pickOverload(ct, cands, v.Args)
	if best == nil {
		ctx.errf(v.Pos, "TY-TYP-0072", "no suitable constructor found for %s(%s)", cl.Name, argTypes(v.Args))
		for _, e := range v.Args {
			ctx.checkExpr(e, nil)
		}
		return nil
	}
	ctx.bindArgs(best, score, v.Args, ct)
	v.Varargs = score.varargs
	return best
}

func argTypes(args []ast.Expr) string {
	var parts []string
	for _, a := range args {
		t := a.GetType()
		if t == nil {
			parts = append(parts, "?")
			continue
		}
		parts = append(parts, t.String())
	}
	return strings.Join(parts, ", ")
}

type ovScore struct {
	total    int
	varargs  int
	method   *ast.Method
	instArgs []ast.Type
	targs    map[*ast.TypeVar]ast.Type // inferred method type arguments
	// directVarargs records that a varargs method was called with the array
	// itself rather than with individual arguments
	directVarargs bool
}

// pickOverload chooses the most specific applicable method.
func (ctx *methodCtx) pickOverload(recv *ast.ClassType, cands []*ast.Method, args []ast.Expr) (*ast.Method, ovScore) {
	// Check arguments once with no target to obtain their types. Lambdas and
	// method references need a target type, so they are checked after the
	// overload is chosen, in bindArgs.
	for _, a := range args {
		if a.GetType() == nil && !isLambdaLike(a) {
			ctx.checkExpr(a, nil)
		}
	}
	best := ovScore{total: 1 << 30}
	for _, m := range cands {
		if !ctx.accessible(m) {
			continue
		}
		s, ok := ctx.applicable(recv, m, args)
		if !ok {
			continue
		}
		if best.method == nil || s.total < best.total {
			best = s
		}
	}
	if best.method == nil {
		return nil, best
	}
	return best.method, best
}

func (ctx *methodCtx) accessible(m *ast.Method) bool {
	if m.Owner == nil || m.Owner.Builtin {
		return true
	}
	if m.Mods.Has(ast.ModPublic) {
		return true
	}
	if m.Mods.Has(ast.ModPrivate) {
		return sameNest(m.Owner, ctx.cl)
	}
	if m.Mods.Has(ast.ModProtected) {
		if ctx.cl == m.Owner {
			return true
		}
		return ctx.c.isSubclass(ctx.cl, m.Owner) || samePackage(ctx.cl.File, m.Owner.File)
	}
	// package private
	return samePackage(ctx.cl.File, m.Owner.File)
}

func samePackage(a, b *ast.File) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Package == b.Package
}

// applicable reports whether args can be passed to m together with a cost.
func (ctx *methodCtx) applicable(recv *ast.ClassType, m *ast.Method, args []ast.Expr) (ovScore, bool) {
	c := ctx.c
	// substitute type variables from the receiver
	bind := map[*ast.TypeVar]ast.Type{}
	if m.Owner != nil && len(m.Owner.TypeParams) > 0 {
		if recv != nil {
			if sup := c.asSuper(recv, m.Owner); sup != nil {
				bind = bindings(m.Owner, sup.Args)
			} else if recv.Class == m.Owner {
				bind = bindings(m.Owner, recv.Args)
			}
		}
	}
	params := make([]ast.Type, len(m.Params))
	for i, p := range m.Params {
		params[i] = c.subst(p, bind)
	}
	// method-level type variables: explicit arguments win, otherwise infer
	mbind := map[*ast.TypeVar]ast.Type{}
	for _, tv := range m.TypeParams {
		mbind[tv] = nil
	}
	if len(ctx.pendingTypeArgs) == len(m.TypeParams) && len(m.TypeParams) > 0 {
		for i, tv := range m.TypeParams {
			mbind[tv] = ctx.pendingTypeArgs[i]
		}
	}
	n := len(params)
	if m.Varargs {
		n--
	}
	if len(args) < n {
		return ovScore{}, false
	}
	if !m.Varargs && len(args) != len(params) {
		return ovScore{}, false
	}
	// A varargs method also accepts the array itself in place of the arguments.
	if m.Varargs && len(args) == len(params) {
		direct := true
		total := 0
		for i, a := range args {
			cost, ok := ctx.convCost(a.GetType(), params[i])
			if !ok {
				direct = false
				break
			}
			total += cost
		}
		if direct {
			return ovScore{method: m, instArgs: params, targs: mbind, total: total, directVarargs: true}, true
		}
	}
	s := ovScore{method: m, instArgs: params}
	for i, a := range args {
		var pt ast.Type
		switch {
		case i < n:
			pt = params[i]
		case m.Varargs:
			elem := params[len(params)-1]
			if arr, ok := elem.(*ast.ArrayType); ok {
				pt = arr.Elem
			} else {
				pt = elem
			}
		}
		if isLambdaLike(a) && a.GetType() == nil {
			// any functional interface will do; the argument is checked once the
			// overload is known
			if pt != nil {
				if i < len(params) {
					params[i] = c.subst(pt, mbind)
				}
				s.total += 1
				continue
			}
		}
		if len(mbind) > 0 {
			pt = c.inferTypeArg(pt, a.GetType(), mbind)
			if i < len(params) {
				params[i] = pt
			}
		}
		cost, ok := ctx.convCost(a.GetType(), pt)
		if !ok {
			if m.Varargs && i >= n && cost == 0 {
				return ovScore{}, false
			}
			return ovScore{}, false
		}
		s.total += cost
	}
	if m.Varargs {
		s.varargs = -1
		if m.Varargs && len(args) != len(params) {
			s.varargs = len(args)
		}
	}
	s.targs = mbind
	return s, true
}

// assignableTo reports whether a value of type t may be used where target is
// expected, without reporting diagnostics.
func (c *Checker) assignableTo(t, target ast.Type) bool {
	if t == nil || target == nil || ast.IsError(t) || ast.IsError(target) {
		return true
	}
	if sameType(t, target) {
		return true
	}
	if _, ok := t.(ast.NullType); ok {
		return ast.IsRef(target)
	}
	if p, ok := t.(*ast.PrimType); ok {
		if q, ok2 := target.(*ast.PrimType); ok2 {
			return widening(p, q)
		}
		if _, ok2 := c.unboxed(target); ok2 {
			return true
		}
		if ct, ok2 := target.(*ast.ClassType); ok2 {
			return ct.Class.Special == "Object"
		}
		return false
	}
	if q, ok := target.(*ast.PrimType); ok {
		if bp, ok2 := c.unboxed(t); ok2 {
			return widening(bp, q)
		}
		return false
	}
	if _, ok := t.(*ast.ArrayType); ok {
		if ct, ok2 := target.(*ast.ClassType); ok2 {
			switch ct.Class.Special {
			case "Object", "Cloneable", "Serializable":
				return true
			}
			return false
		}
	}
	return c.isSubtype(t, target)
}

// isLambdaLike reports whether an expression needs a target type.
func isLambdaLike(e ast.Expr) bool {
	switch e.(type) {
	case *ast.Lambda, *ast.MethodRef:
		return true
	}
	return false
}

func (c *Checker) inferTypeArg(param, arg ast.Type, bind map[*ast.TypeVar]ast.Type) ast.Type {
	if param == nil || arg == nil {
		return param
	}
	switch p := param.(type) {
	case *ast.TypeVarType:
		if _, ok := bind[p.Var]; ok {
			if bind[p.Var] == nil {
				// a primitive argument boxes when it becomes a type argument
				if util.IsPrim(arg) {
					bind[p.Var] = c.boxed(arg)
				} else {
					bind[p.Var] = c.erasure(arg)
				}
			}
			return bind[p.Var]
		}
		return param
	case *ast.ClassType:
		if len(p.Args) == 0 {
			return param
		}
		act, ok := arg.(*ast.ClassType)
		if !ok {
			if a, isArr := arg.(*ast.ArrayType); isArr {
				_ = a
				return p
			}
			return p
		}
		sup := c.asSuper(act, p.Class)
		if sup == nil {
			return p
		}
		nt := &ast.ClassType{Class: p.Class}
		for i, a := range p.Args {
			if i < len(sup.Args) {
				nt.Args = append(nt.Args, c.inferTypeArg(a, sup.Args[i], bind))
			} else {
				nt.Args = append(nt.Args, a)
			}
		}
		return nt
	case *ast.ArrayType:
		if at, ok := arg.(*ast.ArrayType); ok {
			return &ast.ArrayType{Elem: c.inferTypeArg(p.Elem, at.Elem, bind)}
		}
		return param
	}
	return param
}

// convCost returns 0 for identity, 1 for widening/upcast, 2 for boxing, 3 for unboxing.
func (ctx *methodCtx) convCost(src, target ast.Type) (int, bool) {
	c := ctx.c
	if src == nil || target == nil {
		return 0, true
	}
	if ast.IsError(src) || ast.IsError(target) {
		return 0, true
	}
	if _, ok := src.(ast.NullType); ok {
		if ast.IsRef(target) {
			return 1, true
		}
		return 0, false
	}
	if sameType(src, target) {
		return 0, true
	}
	if sp, ok := src.(*ast.PrimType); ok {
		if tp, ok2 := target.(*ast.PrimType); ok2 {
			if widening(sp, tp) {
				if sp.Kind == tp.Kind {
					return 0, true
				}
				return 1, true
			}
			return 0, false
		}
		if _, ok2 := c.unboxed(target); ok2 {
			return 2, true
		}
		if ct, ok2 := target.(*ast.ClassType); ok2 && ct.Class.Special == "Object" {
			return 3, true
		}
		return 0, false
	}
	if tp, ok := target.(*ast.PrimType); ok {
		if bp, ok2 := c.unboxed(src); ok2 {
			if widening(bp, tp) {
				return 3, true
			}
			return 0, false
		}
		return 0, false
	}
	if tv, ok := target.(*ast.TypeVarType); ok {
		if tv.Var.Bound == nil || tv.Var.Bound == c.objType {
			if ast.IsRef(src) {
				return 1, true
			}
			return 0, false
		}
		return ctx.convCost(src, tv.Var.Bound)
	}
	if ast.IsRef(src) && ast.IsRef(target) {
		if c.isSubtype(src, target) {
			return 1, true
		}
		return 0, false
	}
	return 0, false
}

// bindArgs records conversions for the chosen overload.
func (ctx *methodCtx) bindArgs(m *ast.Method, s ovScore, args []ast.Expr, recv *ast.ClassType) {
	if s.directVarargs {
		for i, a := range args {
			if i < len(s.instArgs) {
				ctx.convertTo(a, s.instArgs[i])
			}
		}
		return
	}
	fixed := len(m.Params)
	if m.Varargs {
		fixed--
	}
	for i, a := range args {
		var pt ast.Type
		if i < fixed {
			if i < len(s.instArgs) {
				pt = s.instArgs[i]
			} else if i < len(m.Params) {
				pt = m.Params[i]
			}
		} else if m.Varargs && len(m.Params) > 0 {
			// trailing arguments are elements of the varargs array
			last := len(s.instArgs) - 1
			if last >= 0 && last < len(s.instArgs) {
				if arr, ok := s.instArgs[last].(*ast.ArrayType); ok {
					pt = arr.Elem
				}
			}
			if pt == nil {
				if arr, ok := m.Params[len(m.Params)-1].(*ast.ArrayType); ok {
					pt = arr.Elem
				}
			}
		}
		if pt != nil {
			if isLambdaLike(a) && a.GetType() == nil {
				ctx.checkExpr(a, pt)
			}
			ctx.convertTo(a, pt)
		}
	}
}

func (ctx *methodCtx) checkCall(v *ast.Call, want ast.Type) {
	if len(v.TypeArgs) > 0 {
		var ts []ast.Type
		for _, te := range v.TypeArgs {
			ts = append(ts, ctx.c.resolveType(ctx.env, te))
		}
		ctx.pendingTypeArgs = ts
		defer func() { ctx.pendingTypeArgs = nil }()
	}
	if v.ThisCtor {
		ctx.checkThisCtor(v)
		return
	}
	if v.Qual != "" {
		ctx.checkQualifiedSuper(v)
		return
	}
	var rt ast.Type
	if v.Recv != nil {
		ctx.checkExpr(v.Recv, nil)
		rt = v.Recv.GetType()
	}
	for _, a := range v.Args {
		if a.GetType() == nil && !isLambdaLike(a) {
			ctx.checkExpr(a, nil)
		}
	}
	v.RecvType = rt
	if rt != nil {
		if _, ok := rt.(*ast.ArrayType); ok {
			if ctx.checkArrayCall(v, rt) {
				return
			}
			// not an array specific method: fall through, checkArrayObjCall
			// resolves the Object methods an array inherits
		}
	}
	if rt == nil {
		ctx.checkUnqualifiedCall(v, want)
		return
	}
	ctx.checkMethodCall(v, rt, want)
}

func (ctx *methodCtx) checkArrayCall(v *ast.Call, rt ast.Type) bool {
	switch v.Name {
	case "clone":
		if len(v.Args) != 0 {
			ctx.errf(v.Pos, "TY-TYP-0073", "array clone takes no arguments")
		}
		v.SetType(rt)
		return true
	case "toString", "hashCode", "equals":
		// handled as the Object methods an array inherits; leave the type to
		// the caller instead of failing here
		return false
	}
	return false
}

// checkQualifiedSuper resolves `Interface.super.method(...)`, which binds
// statically to that interface's default implementation.
func (ctx *methodCtx) checkQualifiedSuper(v *ast.Call) {
	c := ctx.c
	iface := c.lookupClassName(ctx.env, v.Qual)
	if iface == nil || !iface.IsInterface() {
		ctx.errf(v.Pos, "TY-TYP-0090", "%s does not name a super interface", v.Qual)
		v.SetType(ast.ErrorType{})
		return
	}
	implements := false
	c.eachInterface(ctx.cl, func(i *ast.Class) bool {
		if i == iface {
			implements = true
			return false
		}
		return true
	})
	if !implements {
		ctx.errf(v.Pos, "TY-TYP-0091", "%s is not a super interface of %s", iface.Name, ctx.cl.Name)
		v.SetType(ast.ErrorType{})
		return
	}
	recv := &ast.ClassType{Class: iface, Args: typeVarArgs(iface)}
	m, sc := ctx.pickOverload(recv, ctx.methodsOf(iface, v.Name), v.Args)
	if m == nil {
		ctx.errf(v.Pos, "TY-TYP-0076", "cannot find method %s(%s) in %s", v.Name, argTypes(v.Args), iface.Name)
		v.SetType(ast.ErrorType{})
		return
	}
	ctx.bindArgs(m, sc, v.Args, recv)
	if sc.directVarargs {
		ctx.c.Direct[v] = true
	}
	v.Method = m
	v.SetType(m.Result)
}

func (ctx *methodCtx) checkThisCtor(v *ast.Call) {
	if ctx.m == nil || !ctx.m.IsCtor {
		ctx.errf(v.Pos, "TY-TYP-0074", "constructor call must be the first statement of a constructor")
	}
	if v.Super {
		if ctx.cl.Super == nil {
			ctx.errf(v.Pos, "TY-TYP-0044", "no superclass to call")
			v.SetType(ast.TVoid)
			return
		}
		m, s := ctx.pickOverload(ctx.cl.Super, ctx.cl.Super.Class.Ctors, v.Args)
		if m == nil {
			ctx.errf(v.Pos, "TY-TYP-0072", "no suitable constructor found for %s(%s)", ctx.cl.Super.Class.Name, argTypes(v.Args))
		} else {
			ctx.bindArgs(m, s, v.Args, ctx.cl.Super)
			v.Method = m
		}
	} else {
		m, s := ctx.pickOverload(&ast.ClassType{Class: ctx.cl}, ctx.cl.Ctors, v.Args)
		if m == nil {
			ctx.errf(v.Pos, "TY-TYP-0072", "no suitable constructor found for %s(%s)", ctx.cl.Name, argTypes(v.Args))
		} else {
			if m != ctx.m {
				ctx.errf(v.Pos, "TY-TYP-0075", "recursive constructor invocation")
			}
			ctx.bindArgs(m, s, v.Args, &ast.ClassType{Class: ctx.cl})
			v.Method = m
		}
	}
	v.SetType(ast.TVoid)
}

func (ctx *methodCtx) checkUnqualifiedCall(v *ast.Call, want ast.Type) {
	// methods of the enclosing class chain
	recv := &ast.ClassType{Class: ctx.cl, Args: typeVarArgs(ctx.cl)}
	if m, s := ctx.pickOverload(recv, ctx.methodsOf(ctx.cl, v.Name), v.Args); m != nil {
		ctx.bindArgs(m, s, v.Args, recv)
		if s.directVarargs {
			ctx.c.Direct[v] = true
		}
		v.Method = m
		v.Static = m.IsStatic()
		v.SetType(ctx.c.subst(m.Result, s.targs))
		return
	}
	// static imports
	for _, m := range ctx.staticMethods[v.Name] {
		if s, ok := ctx.applicable(recv, m, v.Args); ok {
			ctx.bindArgs(m, s, v.Args, nil)
			if s.directVarargs {
				ctx.c.Direct[v] = true
			}
			v.Method = m
			v.Static = true
			v.SetType(ctx.c.subst(m.Result, s.targs))
			return
		}
	}
	// enclosing class (inner class calling outer method)
	for cl := ctx.cl.Outer; cl != nil; cl = cl.Outer {
		orecv := &ast.ClassType{Class: cl, Args: typeVarArgs(cl)}
		if m, s := ctx.pickOverload(orecv, ctx.methodsOf(cl, v.Name), v.Args); m != nil {
			ctx.bindArgs(m, s, v.Args, orecv)
			if s.directVarargs {
				ctx.c.Direct[v] = true
			}
			v.Method = m
			v.Static = m.IsStatic()
			v.Recv = &ast.This{ExprBase: ast.ExprBase{Pos: v.Pos, T: orecv}, Qual: cl.Full, Var: ctx.thisVar(cl)}
			v.SetType(m.Result)
			return
		}
	}
	// Compact source files implicitly import java.io.IO, so bare
	// print/println/readln calls resolve there (JEP 512).
	if ctx.cl != nil && ctx.cl.Decl != nil && ctx.cl.Decl.Implicit {
		if io := ctx.c.programClass("IO"); io != nil {
			recv := &ast.ClassType{Class: io}
			if m, s := ctx.pickOverload(recv, ctx.methodsOf(io, v.Name), v.Args); m != nil {
				ctx.bindArgs(m, s, v.Args, recv)
				v.Method = m
				v.Static = true
				v.SetType(m.Result)
				return
			}
		}
	}
	ctx.errf(v.Pos, "TY-TYP-0076", "cannot find method %s(%s)", v.Name, argTypes(v.Args))
	v.SetType(ast.ErrorType{})
}

func (ctx *methodCtx) thisVar(cl *ast.Class) *ast.Var {
	if ctx.m != nil && ctx.m.ThisVar == nil {
		ctx.m.ThisVar = &ast.Var{Name: "this", Type: &ast.ClassType{Class: cl, Args: typeVarArgs(cl)}, ID: -1}
	}
	if ctx.m != nil {
		return ctx.m.ThisVar
	}
	return nil
}

func (ctx *methodCtx) methodsOf(cl *ast.Class, name string) []*ast.Method {
	var out []*ast.Method
	for k := cl; k != nil; {
		out = append(out, k.Methods[name]...)
		if !k.Resolved || k.Super == nil {
			break
		}
		k = k.Super.Class
	}
	ctx.c.eachInterface(cl, func(i *ast.Class) bool {
		out = append(out, i.Methods[name]...)
		return true
	})
	return out
}

func (ctx *methodCtx) checkMethodCall(v *ast.Call, rt ast.Type, want ast.Type) {
	// A type name receiver: static call or nested class field
	if cl := ctx.typeOf(v.Recv); cl != nil {
		if m, s := ctx.pickOverload(&ast.ClassType{Class: cl, Args: typeVarArgs(cl)}, ctx.methodsOf(cl, v.Name), v.Args); m != nil {
			if !m.IsStatic() {
				ctx.errf(v.Pos, "TY-TYP-0077", "non-static method %s cannot be referenced from a type name", v.Name)
			}
			ctx.bindArgs(m, s, v.Args, &ast.ClassType{Class: cl})
			if s.directVarargs {
				ctx.c.Direct[v] = true
			}
			v.Method = m
			v.Static = true
			v.SetType(ctx.c.subst(m.Result, s.targs))
			return
		}
		ctx.errf(v.Pos, "TY-TYP-0076", "cannot find method %s(%s) in %s", v.Name, argTypes(v.Args), cl.Name)
		v.SetType(ast.ErrorType{})
		return
	}
	// auto-dereference: obj.field.m()
	ctx.derefFields(v)
	rt = v.Recv.GetType()
	recvCT := ctx.recvClass(rt)
	if recvCT == nil {
		if arr, ok := rt.(*ast.ArrayType); ok {
			if ctx.checkArrayObjCall(v, arr) {
				return
			}
		}
		if !ast.IsError(rt) && rt != nil {
			ctx.errf(v.Pos, "TY-TYP-0078", "cannot invoke %s on %s", v.Name, rt)
		}
		v.SetType(ast.ErrorType{})
		return
	}
	cands := ctx.c.methodsFor(recvCT, v.Name)
	if len(cands) == 0 {
		if ctx.tryExtensionMethod(v, rt) {
			return
		}
		ctx.errf(v.Pos, "TY-TYP-0076", "cannot find method %s(%s) in %s", v.Name, argTypes(v.Args), recvCT.Class.Name)
		v.SetType(ast.ErrorType{})
		return
	}
	m, s := ctx.pickOverload(recvCT, cands, v.Args)
	if m == nil {
		ctx.errf(v.Pos, "TY-TYP-0076", "cannot find method %s(%s) in %s", v.Name, argTypes(v.Args), recvCT.Class.Name)
		v.SetType(ast.ErrorType{})
		return
	}
	ctx.bindArgs(m, s, v.Args, recvCT)
	v.Method = m
	v.Static = m.IsStatic()
	if s.directVarargs {
		ctx.c.Direct[v] = true
	}
	res := ctx.c.subst(m.Result, s.targs)
	if len(m.Owner.TypeParams) > 0 {
		if sup := ctx.c.asSuper(recvCT, m.Owner); sup != nil {
			res = ctx.c.subst(res, bindings(m.Owner, sup.Args))
		}
	}
	v.SetType(res)
	if !v.Static && !ctx.accessibleInstance(m, rt) {
		ctx.errf(v.Pos, "TY-TYP-0079", "%s has %s access in %s", m.Name, visName(m.Mods), m.Owner.Name)
	}
}

// tryExtensionMethod rewrites `a.foo(b)` into `Extensions.foo(a, b)` for
// classes named in @ExtensionMethod on the enclosing class.
func (ctx *methodCtx) tryExtensionMethod(v *ast.Call, rt ast.Type) bool {
	c := ctx.c
	exts := c.extensions[ctx.cl]
	if len(exts) == 0 || rt == nil {
		return false
	}
	for _, ext := range exts {
		recv := &ast.ClassType{Class: ext}
		args := append([]ast.Expr{v.Recv}, v.Args...)
		if m, s := ctx.pickOverload(recv, ctx.methodsOf(ext, v.Name), args); m != nil {
			ctx.bindArgs(m, s, args, recv)
			v.Static = true
			v.Method = m
			v.Args = args
			v.Recv = nil
			v.SetType(m.Result)
			return true
		}
	}
	return false
}

func (ctx *methodCtx) accessibleInstance(m *ast.Method, rt ast.Type) bool {
	if m.Mods.Has(ast.ModPublic) || m.Owner == nil || m.Owner.Builtin {
		return true
	}
	if m.Mods.Has(ast.ModPrivate) {
		return sameNest(m.Owner, ctx.cl)
	}
	if m.Mods.Has(ast.ModProtected) {
		return ctx.c.isSubclass(ctx.cl, m.Owner) || samePackage(ctx.cl.File, m.Owner.File) || ctx.cl == m.Owner
	}
	return samePackage(ctx.cl.File, m.Owner.File)
}

func visName(m ast.Mods) string {
	switch {
	case m.Has(ast.ModPrivate):
		return "private"
	case m.Has(ast.ModProtected):
		return "protected"
	}
	return "package"
}

// recvClass unwraps a type into a receiver class type (boxing primitives).
func (ctx *methodCtx) recvClass(t ast.Type) *ast.ClassType {
	c := ctx.c
	switch v := t.(type) {
	case *ast.ClassType:
		return v
	case *ast.TypeVarType:
		if v.Var.Bound != nil {
			if ct, ok := c.erasure(v.Var.Bound).(*ast.ClassType); ok {
				return ct
			}
		}
		return c.objType
	case *ast.PrimType:
		if cl := c.b.Boxes[v.Kind]; cl != nil {
			return &ast.ClassType{Class: cl}
		}
	case *ast.NullType:
		return c.objType
	case ast.ErrorType:
		return nil
	case *ast.ArrayType:
		return nil
	}
	return nil
}

func (ctx *methodCtx) checkArrayObjCall(v *ast.Call, arr *ast.ArrayType) bool {
	obj := &ast.ClassType{Class: ctx.c.b.Object}
	switch v.Name {
	case "equals":
		if len(v.Args) == 1 {
			ctx.checkExpr(v.Args[0], obj)
			v.SetType(ast.TBoolean)
			return true
		}
	case "hashCode":
		if len(v.Args) == 0 {
			v.SetType(ast.TInt)
			return true
		}
	case "toString":
		if len(v.Args) == 0 {
			v.SetType(ctx.c.strType)
			return true
		}
	case "clone":
		if len(v.Args) == 0 {
			v.SetType(arr)
			return true
		}
	}
	return false
}

// derefFields auto-dereferences property/field receivers: `a.b.c()` where b is a field.
func (ctx *methodCtx) derefFields(v *ast.Call) {
	for {
		sel, ok := v.Recv.(*ast.Select)
		if !ok {
			return
		}
		if sel.Ref == nil {
			return
		}
		return
	}
}

// typeOf returns the class denoted by an expression used as a type name.
func (ctx *methodCtx) typeOf(e ast.Expr) *ast.Class {
	switch v := e.(type) {
	case *ast.Ident:
		if _, isVar := v.Ref.(*ast.Var); isVar {
			return nil
		}
		if _, isVar := v.Ref.(*ast.Field); isVar {
			return nil
		}
		return ctx.c.lookupClassName(ctx.env, v.Name)
	case *ast.Select:
		if v.Ref != nil {
			return nil
		}
		return ctx.c.lookupClassName(ctx.env, exprTypeName(v))
	}
	return nil
}

func exprTypeName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.Select:
		return exprTypeName(v.X) + "." + v.Name
	}
	return ""
}

func (ctx *methodCtx) checkSelect(v *ast.Select, want ast.Type) {
	if v.Ref != nil {
		ctx.setRefType(v, v.Ref)
		return
	}
	// type name?
	if cl := ctx.typeOfName(v); cl != nil {
		return
	}
	ctx.checkExpr(v.X, nil)
	if q := typeQualifier(v.X); q != nil {
		f := ctx.c.findStaticField(q, v.Name)
		if f == nil {
			ctx.errf(v.Pos, "TY-TYP-0080", "cannot find static symbol %s in %s", v.Name, q.Name)
			v.SetType(ast.ErrorType{})
			return
		}
		v.Ref = f
		v.SetType(f.Type)
		ctx.rewriteSelectProp(v, f)
		return
	}
	xt := v.X.GetType()
	if ast.IsError(xt) {
		v.SetType(ast.ErrorType{})
		return
	}
	// array length
	if _, ok := xt.(*ast.ArrayType); ok {
		if v.Name == "length" {
			v.Ref = "length"
			v.SetType(ast.TInt)
			return
		}
		ctx.errf(v.Pos, "TY-TYP-0080", "cannot find symbol %s on array", v.Name)
		v.SetType(ast.ErrorType{})
		return
	}
	ct := ctx.recvClass(xt)
	if ct == nil {
		ctx.errf(v.Pos, "TY-TYP-0080", "cannot find symbol %s on %s", v.Name, xt)
		v.SetType(ast.ErrorType{})
		return
	}
	f := ctx.c.findField(ct, v.Name)
	if f == nil {
		ctx.errf(v.Pos, "TY-TYP-0080", "cannot find symbol %s in %s", v.Name, ct.Class.Name)
		v.SetType(ast.ErrorType{})
		return
	}
	if f.Mods.Has(ast.ModPrivate) && !sameNest(f.Owner, ctx.cl) && !f.IsProp {
		ctx.errf(v.Pos, "TY-TYP-0046", "%s has private access in %s", f.Name, f.Owner.Name)
	}
	if !f.Mods.Has(ast.ModStatic) && ctx.inStatic() && ctx.m != nil && !isTypeReceiver(v.X) {
		// instance access via an expression is fine even in static context
	}
	v.Ref = f
	v.SetType(f.Type)
	ctx.rewriteSelectProp(v, f)
}

// setRefType records the type of an expression whose symbol was resolved
// before the checker reached it (synthesized bodies carry resolved references).
func (ctx *methodCtx) setRefType(e ast.Expr, ref any) {
	switch r := ref.(type) {
	case *ast.Var:
		e.SetType(r.Type)
	case *ast.Field:
		e.SetType(r.Type)
	case *ast.Class:
		e.SetType(&ast.ClassType{Class: r, Args: typeVarArgs(r)})
	case string:
		e.SetType(ast.TInt) // array length
	}
}

// typeQualifier returns the class when e denotes a type name.
func typeQualifier(e ast.Expr) *ast.Class {
	switch v := e.(type) {
	case *ast.Ident:
		if cl, ok := v.Ref.(*ast.Class); ok {
			return cl
		}
	case *ast.Select:
		if cl, ok := v.Ref.(*ast.Class); ok {
			return cl
		}
	}
	return nil
}

// findStaticField looks up a static field, walking the superclass chain.
func (c *Checker) findStaticField(cl *ast.Class, name string) *ast.Field {
	for k := cl; k != nil; {
		if f := k.FieldMap[name]; f != nil && f.Mods.Has(ast.ModStatic) {
			return f
		}
		if k.Super == nil {
			break
		}
		k = k.Super.Class
	}
	return nil
}

func isTypeReceiver(e ast.Expr) bool {
	_, ok := e.(*ast.Ident)
	return ok
}

func (ctx *methodCtx) rewriteSelectProp(v *ast.Select, f *ast.Field) {
	if !f.IsProp {
		return
	}
	if f.Getter == nil {
		ctx.errf(v.Pos, "TY-PROP-0005", "property %s has no getter", f.Name)
		if f.Setter != nil {
			v.SetType(f.Type)
			return
		}
		v.SetType(ast.ErrorType{})
		return
	}
	recv := v.X
	if f.Mods.Has(ast.ModStatic) {
		recv = nil
	}
	ctx.props[v] = &ast.Call{ExprBase: ast.ExprBase{Pos: v.Pos, T: f.Type}, Recv: recv, Name: f.Getter.Name, Args: []ast.Expr{}, Method: f.Getter}
}

func (ctx *methodCtx) typeOfName(v *ast.Select) *ast.Class {
	if v.Ref != nil {
		return nil
	}
	if _, isVar := v.Ref.(*ast.Var); isVar {
		return nil
	}
	cl := ctx.c.lookupClassName(ctx.env, exprTypeName(v))
	if cl == nil {
		return nil
	}
	v.Ref = cl
	v.SetType(&ast.ClassType{Class: cl})
	return cl
}

// ---------------------------------------------------------------- lambdas

func (ctx *methodCtx) checkLambda(lam *ast.Lambda, want ast.Type) {
	c := ctx.c
	if want == nil || ast.IsError(want) {
		ctx.errf(lam.Pos, "TY-TYP-0081", "cannot infer the functional interface for this lambda; declare the target type")
		lam.SetType(ast.ErrorType{})
		return
	}
	ct, ok := want.(*ast.ClassType)
	if !ok || !ct.Class.IsInterface() {
		ctx.errf(lam.Pos, "TY-TYP-0082", "lambda target type must be a functional interface, found %s", want)
		lam.SetType(ast.ErrorType{})
		return
	}
	sam := c.singleAbstract(ct)
	if sam == nil {
		ctx.errf(lam.Pos, "TY-TYP-0083", "%s is not a functional interface", ct.Class.Name)
		lam.SetType(ast.ErrorType{})
		return
	}
	bind := bindings(ct.Class, ct.Args)
	params := make([]ast.Type, len(sam.Params))
	for i, p := range sam.Params {
		params[i] = c.subst(p, bind)
	}
	if len(lam.Params) != len(params) {
		ctx.errf(lam.Pos, "TY-TYP-0084", "lambda has %d parameters but %s requires %d", len(lam.Params), sam.Name, len(params))
	}
	lam.Iface = sam
	lam.SetType(ct)
	// synthesize a class implementing ct
	lamID := c.anonN[nil]
	cl := c.newClass("$Lambda"+fmt.Sprint(lamID), "teyru.Lambda$"+fmt.Sprint(lamID), ast.KindClass)
	c.anonN[nil]++
	cl.Anon = true
	cl.Mods = ast.ModFinal
	cl.File = ctx.cl.File
	cl.Resolved = true
	cl.Laidout = false
	cl.Ifaces = []*ast.ClassType{ct}
	cl.LocalOwner = ctx.m
	cl.Lambda = lam
	lam.Class = cl
	m := &ast.Method{Name: sam.Name, Owner: cl, Mods: ast.ModPublic, Result: c.subst(sam.Result, bind), Params: params, Lambda: lam, SynthKind: "lambda"}
	cl.Methods[m.Name] = append(cl.Methods[m.Name], m)
	c.addCtor(cl, &ast.Method{Name: "<init>", IsCtor: true, Owner: cl, Mods: ast.ModPublic, Result: ast.TVoid, SynthKind: "lambda-ctor"})
	// check the body in the lambda's scope
	lctx := &methodCtx{c: c, cl: ctx.cl, m: m, env: ctx.env, lambda: lam}
	lctx.push()
	outerLocals := ctx.scopes
	lctx.scopes = append(append([]map[string]*ast.Var{}, outerLocals...), map[string]*ast.Var{})
	lam.Captures = lam.PreCaptures
	for i, p := range lam.Params {
		name := p.Name
		var t ast.Type
		if i < len(params) {
			t = params[i]
		}
		if p.Type != nil && p.Type.Name != "var" {
			t = c.resolveType(ctx.env, p.Type)
		}
		if p.Name == "_" {
			p.Unnamed = true
		}
		p.Sym = lctx.declare(name, t, p.Pos)
		m.ParamVars = append(m.ParamVars, p.Sym)
	}
	if isStaticCtx(ctx.cl) || ctx.m != nil && ctx.m.IsStatic() {
		// static context lamdbdas cannot capture this
	}
	switch b := lam.Body.(type) {
	case ast.Expr:
		if ast.IsPrim(m.Result, ast.Void) {
			// a void compatible body is a statement expression (JLS 15.27.2)
			lctx.checkExpr(b, nil)
			lam.ExprStmt = true
		} else {
			lctx.checkExpr(b, m.Result)
			lctx.convertTo(b, m.Result)
		}
	case *ast.Block:
		lctx.checkBlock(b, false)
		if m.Result != ast.TVoid && !endsWithReturn(b) {
			lctx.errf(b.End, "TY-TYP-0020", "missing return statement")
		}
	}
	if lam.CapThis {
		// `this` of the enclosing class is captured as a field
		f := &ast.Field{Name: "this", Type: &ast.ClassType{Class: ctx.cl, Args: typeVarArgs(ctx.cl)},
			Mods: ast.ModPrivate | ast.ModFinal, Pos: lam.Pos, Storage: true, Owner: cl}
		cl.Fields = append(cl.Fields, f)
		cl.FieldMap["this"] = f
	}
	// captured variables become fields of the synthetic class
	for _, v := range lam.Captures {
		f := &ast.Field{Name: v.Name, Type: v.Type, Mods: ast.ModPrivate | ast.ModFinal, Pos: v.Pos, Storage: true, Owner: cl}
		cl.Fields = append(cl.Fields, f)
		cl.FieldMap[f.Name] = f
		cl.CapFields[v] = f
	}
	c.addInstanceFields(cl)
	c.layout(cl)
}

// singleAbstract finds the functional interface method.
func (c *Checker) singleAbstract(ct *ast.ClassType) *ast.Method {
	var found *ast.Method
	c.eachInterface(ct.Class, func(i *ast.Class) bool {
		for _, name := range sortedMethodNames(i) {
			for _, m := range i.Methods[name] {
				if m.IsStatic() || m.Mods.Has(ast.ModPrivate) || !m.Mods.Has(ast.ModAbstract) {
					continue
				}
				if c.objectHas(m) && i.Name != ct.Class.Name {
					continue
				}
				if found != nil && found.Name != m.Name {
					return false
				}
				if found == nil || !sameType(c.erasure(found.Result), c.erasure(m.Result)) {
					found = m
				}
			}
		}
		return true
	})
	return found
}

func (ctx *methodCtx) checkMethodRef(mr *ast.MethodRef, want ast.Type) {
	c := ctx.c
	if want == nil {
		ctx.errf(mr.Pos, "TY-TYP-0081", "cannot infer the functional interface for this method reference")
		mr.SetType(ast.ErrorType{})
		return
	}
	ct, ok := want.(*ast.ClassType)
	if !ok || !ct.Class.IsInterface() {
		ctx.errf(mr.Pos, "TY-TYP-0082", "method reference target type must be a functional interface")
		mr.SetType(ast.ErrorType{})
		return
	}
	sam := c.singleAbstract(ct)
	if sam == nil {
		ctx.errf(mr.Pos, "TY-TYP-0083", "%s is not a functional interface", ct.Class.Name)
		mr.SetType(ast.ErrorType{})
		return
	}
	bind := bindings(ct.Class, ct.Args)
	params := make([]ast.Type, len(sam.Params))
	for i, p := range sam.Params {
		params[i] = c.subst(p, bind)
	}
	res := c.subst(sam.Result, bind)
	// build an equivalent lambda
	lam := &ast.Lambda{ExprBase: ast.ExprBase{Pos: mr.Pos}, Iface: sam}
	for i := range params {
		lam.Params = append(lam.Params, &ast.Param{Pos: mr.Pos, Name: fmt.Sprintf("p%d", i)})
	}
	callee := mr.Name
	recv := mr.X
	if mr.Name == "new" {
		callee = "<new>"
	}
	// resolve the target method
	var target *ast.Method
	var recvType ast.Type
	if mr.TypeX != nil {
		recvType = c.resolveType(ctx.env, mr.TypeX)
	} else if recv != nil {
		ctx.checkExpr(recv, nil)
		recvType = recv.GetType()
	}
	if callee == "<new>" {
		ct2, ok := c.erasure(recvType).(*ast.ClassType)
		if !ok {
			ctx.errf(mr.Pos, "TY-TYP-0085", "cannot construct %s", recvType)
			mr.SetType(ast.ErrorType{})
			return
		}
		var cands []*ast.Method
		cands = append(cands, ct2.Class.Ctors...)
		m, s := ctx.matchRefParams(ct2, cands, params)
		if m == nil {
			ctx.errf(mr.Pos, "TY-TYP-0072", "no suitable constructor for %s", ct2.Class.Name)
			mr.SetType(ast.ErrorType{})
			return
		}
		_ = s
		target = m
		te := mr.TypeX
		if te == nil {
			te = &ast.TypeExpr{Pos: mr.Pos, Name: ct2.Class.Name, Resolved: ct2}
		}
		te.Resolved = ct2
		lam.Body = &ast.New{ExprBase: ast.ExprBase{Pos: mr.Pos, T: ct2}, Type: te, Ctor: target}
		mr.Lam = lam
		ctx.checkLambda(lam, want)
		mr.SetType(ct)
		return
	}
	// instance method on the receiver type, or a static method / unbound instance method
	if rt := ctx.recvClassForRef(recvType); rt != nil {
		all := c.methodsFor(rt, callee)
		// try bound form first (params as-is)
		if m, _ := ctx.matchRefParams(rt, all, params); m != nil && !m.IsStatic() {
			target = m
		}
		// an unbound reference takes its receiver from the first parameter, so it
		// only exists when the functional interface has at least one parameter
		if target == nil && len(params) > 0 {
			// unbound: first parameter is the receiver
			if m, _ := ctx.matchRefParams(rt, all, params[1:]); m != nil && !m.IsStatic() {
				target = m
			}
		}
		if target == nil {
			if m, _ := ctx.matchRefParams(rt, all, params); m != nil && m.IsStatic() {
				target = m
			}
		}
	}
	if target == nil {
		ctx.errf(mr.Pos, "TY-TYP-0076", "cannot find method %s for this functional interface", callee)
		mr.SetType(ast.ErrorType{})
		return
	}
	// build body: call
	args := make([]ast.Expr, len(lam.Params))
	for i, p := range lam.Params {
		args[i] = &ast.Ident{ExprBase: ast.ExprBase{Pos: mr.Pos}, Name: p.Name}
	}
	var callRecv ast.Expr
	if target.IsStatic() {
		// qualify the call with the declaring class so it resolves from
		// anywhere, not just from the enclosing class
		if o := target.Owner; o != nil {
			callRecv = &ast.Ident{
				ExprBase: ast.ExprBase{Pos: mr.Pos, T: &ast.ClassType{Class: o, Args: typeVarArgs(o)}},
				Name:     o.Name, Ref: o,
			}
		}
	} else if len(args) == len(target.Params) {
		callRecv = ctx.bindRefReceiver(lam, mr, recv, recvType)
	} else {
		callRecv = args[0]
		args = args[1:]
	}
	lam.Body = &ast.Call{ExprBase: ast.ExprBase{Pos: mr.Pos, T: res}, Recv: callRecv, Name: target.Name, Args: args}
	mr.Lam = lam
	ctx.checkLambda(lam, want)
	mr.SetType(ct)
}

// bindRefReceiver returns the receiver an instance method reference calls. A
// simple name is reused directly; any other expression is evaluated once, when
// the method reference is created, and captured by the closure (JLS 15.13.3).
func (ctx *methodCtx) bindRefReceiver(lam *ast.Lambda, mr *ast.MethodRef, recv ast.Expr, recvType ast.Type) ast.Expr {
	if recv == nil {
		return recv
	}
	switch r := recv.(type) {
	case *ast.Ident:
		if _, ok := r.Ref.(*ast.Var); ok {
			return recv
		}
	case *ast.This:
		return recv
	}
	v := &ast.Var{Name: fmt.Sprintf("recv%d", ctx.c.varID), Type: recvType, Owner: ctx.m}
	ctx.c.varID++
	lam.PreCaptures = append(lam.PreCaptures, v)
	lam.RecvVar = v
	lam.RecvExpr = recv
	return &ast.Ident{ExprBase: ast.ExprBase{Pos: mr.Pos, T: recvType}, Name: v.Name, Ref: v}
}

func (ctx *methodCtx) recvClassForRef(t ast.Type) *ast.ClassType {
	if t == nil {
		return nil
	}
	if arr, ok := t.(*ast.ArrayType); ok {
		_ = arr
		return ctx.c.objType
	}
	return ctx.recvClass(t)
}

func (ctx *methodCtx) matchRefParams(recv *ast.ClassType, cands []*ast.Method, params []ast.Type) (*ast.Method, ovScore) {
	best := ovScore{total: 1 << 30}
	var bestM *ast.Method
	for _, m := range cands {
		if !ctx.accessible(m) {
			continue
		}
		n := len(m.Params)
		varargs := m.Varargs
		if varargs {
			n--
			if len(params) < n {
				continue
			}
		} else if len(params) != len(m.Params) {
			continue
		}
		s := ovScore{method: m}
		ok := true
		for i, p := range params {
			var pt ast.Type
			if i < len(m.Params) {
				pt = m.Params[i]
			} else if varargs && len(m.Params) > 0 {
				if arr, isArr := m.Params[len(m.Params)-1].(*ast.ArrayType); isArr {
					pt = arr.Elem
				}
			}
			cost, good := ctx.convCost(p, pt)
			if !good {
				ok = false
				break
			}
			s.total += cost
		}
		if ok && (bestM == nil || s.total < best.total) {
			best = s
			bestM = m
		}
	}
	return bestM, best
}

// ---------------------------------------------------------------- helpers

var _ = source.Pos{}

// methodsFor returns all methods with the given name visible on ct.
func (c *Checker) methodsFor(ct *ast.ClassType, name string) []*ast.Method {
	var out []*ast.Method
	seen := map[*ast.Class]bool{}
	for k := ct.Class; k != nil; {
		out = append(out, k.Methods[name]...)
		if k.Super == nil {
			break
		}
		k = k.Super.Class
	}
	c.eachInterface(ct.Class, func(i *ast.Class) bool {
		if seen[i] {
			return true
		}
		seen[i] = true
		for _, m := range i.Methods[name] {
			if m.Mods.Has(ast.ModAbstract) && c.Implementation(ct.Class, m) != nil {
				continue
			}
			out = append(out, m)
		}
		return true
	})
	return out
}

// findField looks up a field on ct (including inherited).
func (c *Checker) findField(ct *ast.ClassType, name string) *ast.Field {
	bind := bindings(ct.Class, ct.Args)
	for k := ct.Class; k != nil; {
		if f := k.FieldMap[name]; f != nil {
			if len(bind) > 0 {
				nf := *f
				nf.Type = c.subst(f.Type, bind)
				return &nf
			}
			return f
		}
		if !k.Resolved {
			break
		}
		if k.Super == nil {
			break
		}
		bind = compose(bind, bindings(k.Super.Class, k.Super.Args))
		k = k.Super.Class
	}
	return nil
}

func compose(inner, outer map[*ast.TypeVar]ast.Type) map[*ast.TypeVar]ast.Type {
	if len(inner) == 0 {
		return outer
	}
	out := map[*ast.TypeVar]ast.Type{}
	for k, v := range outer {
		out[k] = v
	}
	for k, v := range inner {
		if tv, ok := v.(*ast.TypeVarType); ok {
			if r, ok2 := outer[tv.Var]; ok2 {
				out[k] = r
				continue
			}
		}
		out[k] = v
	}
	return out
}

func walkBlock(b *ast.Block, fn func(ast.Expr)) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		walkStmt(s, fn)
	}
}

func walkStmt(s ast.Stmt, fn func(ast.Expr)) {
	switch v := s.(type) {
	case *ast.Block:
		walkBlock(v, fn)
	case *ast.ExprStmt:
		walkExpr(v.X, fn)
	case *ast.LocalVar:
		for _, d := range v.Vars {
			if d.Init != nil {
				walkExpr(d.Init, fn)
			}
		}
	case *ast.If:
		walkExpr(v.Cond, fn)
		walkStmt(v.Then, fn)
		if v.Else != nil {
			walkStmt(v.Else, fn)
		}
	case *ast.While:
		walkExpr(v.Cond, fn)
		walkStmt(v.Body, fn)
	case *ast.DoWhile:
		walkStmt(v.Body, fn)
		walkExpr(v.Cond, fn)
	case *ast.For:
		for _, i := range v.Init {
			walkStmt(i, fn)
		}
		if v.Cond != nil {
			walkExpr(v.Cond, fn)
		}
		for _, u := range v.Update {
			walkExpr(u, fn)
		}
		walkStmt(v.Body, fn)
	case *ast.ForEach:
		walkExpr(v.X, fn)
		walkStmt(v.Body, fn)
	case *ast.Return:
		if v.X != nil {
			walkExpr(v.X, fn)
		}
	case *ast.Throw:
		walkExpr(v.X, fn)
	case *ast.Try:
		for _, r := range v.Resources {
			walkStmt(r, fn)
		}
		walkBlock(v.Body, fn)
		for _, c := range v.Catches {
			walkBlock(c.Body, fn)
		}
		if v.Finally != nil {
			walkBlock(v.Finally, fn)
		}
	case *ast.Switch:
		walkExpr(v.X, fn)
		for _, cs := range v.Cases {
			for _, l := range cs.Labels {
				walkExpr(l, fn)
			}
			if cs.Guard != nil {
				walkExpr(cs.Guard, fn)
			}
			if cs.ArrowX != nil {
				walkExpr(cs.ArrowX, fn)
			}
			for _, st := range cs.Body {
				walkStmt(st, fn)
			}
		}
	case *ast.Yield:
		walkExpr(v.X, fn)
	case *ast.Labeled:
		walkStmt(v.Body, fn)
	case *ast.Assert:
		walkExpr(v.Cond, fn)
		if v.Msg != nil {
			walkExpr(v.Msg, fn)
		}
	case *ast.Sync:
		walkExpr(v.Lock, fn)
		walkBlock(v.Body, fn)
	}
}

func walkExpr(e ast.Expr, fn func(ast.Expr)) {
	if e == nil {
		return
	}
	fn(e)
	switch v := e.(type) {
	case *ast.Unary:
		walkExpr(v.X, fn)
	case *ast.Binary:
		walkExpr(v.X, fn)
		walkExpr(v.Y, fn)
	case *ast.Assign:
		walkExpr(v.X, fn)
		walkExpr(v.Y, fn)
	case *ast.Cond:
		walkExpr(v.C, fn)
		walkExpr(v.X, fn)
		walkExpr(v.Y, fn)
	case *ast.Cast:
		walkExpr(v.X, fn)
	case *ast.Conv:
		walkExpr(v.X, fn)
	case *ast.Call:
		if v.Recv != nil {
			walkExpr(v.Recv, fn)
		}
		for _, a := range v.Args {
			walkExpr(a, fn)
		}
	case *ast.New:
		if v.Outer != nil {
			walkExpr(v.Outer, fn)
		}
		for _, a := range v.Args {
			walkExpr(a, fn)
		}
	case *ast.NewArray:
		for _, d := range v.Dims {
			walkExpr(d, fn)
		}
	case *ast.ArrayInit:
		for _, el := range v.Elems {
			walkExpr(el, fn)
		}
	case *ast.Index:
		walkExpr(v.X, fn)
		walkExpr(v.Index, fn)
	case *ast.Select:
		walkExpr(v.X, fn)
	case *ast.InstanceOf:
		walkExpr(v.X, fn)
	case *ast.Lambda:
		if b, ok := v.Body.(*ast.Block); ok {
			walkBlock(b, fn)
		} else if x, ok := v.Body.(ast.Expr); ok {
			walkExpr(x, fn)
		}
	case *ast.SwitchExpr:
		if v.S != nil {
			walkStmt(v.S, fn)
		}
	}
}
