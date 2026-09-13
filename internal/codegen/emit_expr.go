package codegen

import (
	"fmt"
	"strings"

	"github.com/LangYa466/Teyru/internal/ast"
	"github.com/LangYa466/Teyru/internal/util"
)

// cstr returns a C string literal for s.
func (e *Emitter) cstr(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString("\\\"")
		case c == '\\':
			b.WriteString("\\\\")
		case c == '\n':
			b.WriteString("\\n")
		case c == '\r':
			b.WriteString("\\r")
		case c == '\t':
			b.WriteString("\\t")
		case c < 32 || c > 126:
			fmt.Fprintf(&b, "\\%03o", c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// tmpRef materialises an expression into a statement-expression so it can be
// referenced more than once without double evaluation.
func (e *Emitter) tmpRef(x string) string {
	n := e.tmpName()
	return "({ __typeof__(" + x + ") " + n + " = " + x + "; " + n + "; })"
}

// cond renders a boolean condition.
func (e *Emitter) cond(x ast.Expr) string {
	return e.expr(x)
}

// refExpr renders an expression that is being used as an object reference.
func (e *Emitter) refExpr(x ast.Expr) string {
	return e.expr(x)
}

// coerce converts an emitted value from src to dst type.
func (e *Emitter) coerce(v string, src, dst ast.Type) string {
	if src == nil || dst == nil || dst == ast.TVoid || ast.IsError(src) || ast.IsError(dst) {
		return v
	}
	// generics are erased at runtime
	if _, ok := dst.(*ast.TypeVarType); ok {
		dst = e.prog.Erased(dst)
	}
	if _, ok := src.(*ast.TypeVarType); ok {
		src = e.prog.Erased(src)
	}
	_, sp := src.(*ast.PrimType)
	_, dp := dst.(*ast.PrimType)
	switch {
	case sp && dp:
		return v // C handles numeric conversions implicitly
	case sp && !dp:
		return e.boxCall(v, src.(*ast.PrimType), dst)
	case !sp && dp:
		return e.unboxCall(v, src, dst.(*ast.PrimType))
	case !sp && !dp:
		if e.isRef(src) && e.isRef(dst) {
			return "(" + e.ctype(dst) + ")" + v
		}
	}
	return v
}

func (e *Emitter) boxCall(v string, p *ast.PrimType, dst ast.Type) string {
	fn := boxFn(p.Kind)
	if fn == "" {
		return v
	}
	switch d := dst.(type) {
	case *ast.ClassType:
		if d.Class.Special == "box" || d.Class.Special == "Object" {
			return "(" + cname(d.Class) + "*)" + fn + "(" + v + ")"
		}
	case *ast.TypeVarType:
		return "(void*)" + fn + "(" + v + ")"
	}
	return v
}

func boxFn(k ast.PrimKind) string {
	switch k {
	case ast.Boolean:
		return "ty_box_bool"
	case ast.Byte:
		return "ty_box_byte"
	case ast.Short:
		return "ty_box_short"
	case ast.Char:
		return "ty_box_char"
	case ast.Int:
		return "ty_box_int"
	case ast.Long:
		return "ty_box_long"
	case ast.Float:
		return "ty_box_float"
	case ast.Double:
		return "ty_box_double"
	}
	return ""
}

func (e *Emitter) unboxCall(v string, src ast.Type, p *ast.PrimType) string {
	if ct, ok := src.(*ast.ClassType); ok {
		if _, ok2 := e.prog.Builtins.Unbox[ct.Class]; ok2 {
			recv := v
			switch p.Kind {
			case ast.Boolean:
				return "ty_unbox_bool((void*)" + recv + ")"
			case ast.Byte:
				return "ty_unbox_byte((void*)" + recv + ")"
			case ast.Short:
				return "ty_unbox_short((void*)" + recv + ")"
			case ast.Char:
				return "ty_unbox_char((void*)" + recv + ")"
			case ast.Int:
				return "ty_unbox_int((void*)" + recv + ")"
			case ast.Long:
				return "ty_unbox_long((void*)" + recv + ")"
			case ast.Float:
				return "ty_unbox_float((void*)" + recv + ")"
			case ast.Double:
				return "ty_unbox_double((void*)" + recv + ")"
			}
		}
	}
	return v
}

// ---------------------------------------------------------------- expressions

func (e *Emitter) expr(x ast.Expr) string {
	if x == nil {
		return "0"
	}
	if v, ok := e.prog.Lowered(x); ok {
		return e.expr(v)
	}
	switch v := x.(type) {
	case *ast.Literal:
		return e.literal(v)
	case *ast.Ident:
		return e.ident(v)
	case *ast.Select:
		return e.selectExpr(v)
	case *ast.Index:
		return e.indexExpr(v)
	case *ast.Call:
		if v.ThisCtor {
			return ""
		}
		return e.callExpr(v)
	case *ast.New:
		return e.newExpr(v)
	case *ast.NewArray:
		return e.newArray(v)
	case *ast.ArrayInit:
		return e.arrayInit(v)
	case *ast.Unary:
		return e.unary(v)
	case *ast.Binary:
		return e.binary(v)
	case *ast.Assign:
		return e.assign(v)
	case *ast.Cond:
		return "(" + e.cond(v.C) + " ? " + e.coerce(e.expr(v.X), v.X.GetType(), v.GetType()) +
			" : " + e.coerce(e.expr(v.Y), v.Y.GetType(), v.GetType()) + ")"
	case *ast.Cast:
		return e.cast(v)
	case *ast.InstanceOf:
		return e.instanceOf(v)
	case *ast.Conv:
		return e.coerce(e.expr(v.X), v.X.GetType(), v.GetType())
	case *ast.This:
		if e.curLambda != nil && v.Qual == "" && e.curLambda.CapThis {
			return "((" + e.ctype(v.GetType()) + ")this->cap_this)"
		}
		if v.Qual != "" {
			if cl := e.prog.LookupClass(v.Qual); cl != nil {
				return "(" + cname(cl) + "*)" + e.outerAccess(cl)
			}
			return "((void*)this)"
		}
		return "this"
	case *ast.SuperExpr:
		return "((void*)this)"
	case *ast.ClassLit:
		t := v.Type.Resolved
		if ct, ok := t.(*ast.ClassType); ok {
			return "((tyobj*)&cls_" + mangle(ct.Class.Full) + ")"
		}
		return "((tyobj*)&cls_" + mangle(e.prog.Builtins.Object.Full) + ")"
	case *ast.Lambda:
		return e.lambdaExpr(v)
	case *ast.MethodRef:
		if v.Lam != nil {
			return e.lambdaExpr(v.Lam)
		}
		return "NULL"
	case *ast.SwitchExpr:
		n := e.tmpName()
		ct := e.ctype(v.GetType())
		inner := e.capture(func() {
			e.line("%s %s = %s;\n", ct, n, zeroOf(ct))
			e.switchStmt(v.S, n)
		})
		return "({ " + inner + " " + n + "; })"
	}
	return "0"
}

func (e *Emitter) literal(v *ast.Literal) string {
	switch v.Kind {
	case ast.LitInt:
		i := int32(v.Int)
		if i == -2147483648 {
			return "(-2147483647 - 1)"
		}
		return fmt.Sprintf("%d", i)
	case ast.LitLong:
		return fmt.Sprintf("%dLL", int64(v.Int))
	case ast.LitFloat:
		return util.FloatLiteral(v.Flt, true)
	case ast.LitDouble:
		return util.FloatLiteral(v.Flt, false)
	case ast.LitChar:
		return fmt.Sprintf("((uint16_t)%d)", uint16(v.Int))
	case ast.LitString:
		return e.strLit(v.Str)
	case ast.LitBool:
		if v.Bool {
			return "1"
		}
		return "0"
	case ast.LitNull:
		return "NULL"
	}
	return "0"
}

// strLit interns a string literal into a static global.
func (e *Emitter) strLit(s string) string {
	if id, ok := e.strings[s]; ok {
		return fmt.Sprintf("((tystr*)&S%d)", id)
	}
	id := len(e.strOrder)
	e.strings[s] = id
	e.strOrder = append(e.strOrder, s)
	data := e.cstr(s)
	fmt.Fprintf(&e.data, "static tystr S%d = {{&cls_%s}, %d, %s};\n", id,
		mangle(e.prog.Builtins.String.Full), len(s), data)
	return fmt.Sprintf("((tystr*)&S%d)", id)
}

// finishStrings rewrites static string globals so their data pointer is valid.
func (e *Emitter) stringGlobals() string { return "" }

func (e *Emitter) ident(v *ast.Ident) string {
	switch r := v.Ref.(type) {
	case *ast.Field:
		if r == nil {
			return "0"
		}
		// a static final constant is inlined at every use site
		if r.Mods.Has(ast.ModStatic) && r.ConstVal != nil {
			if s, ok := e.prog.ConstantString(r); ok {
				return e.strLit(s)
			}
			if lit, ok := e.prog.ConstantLiteral(r); ok {
				return lit
			}
		}
		if e.curClass != nil && r.Owner != nil && r.Owner != e.curClass && !r.Mods.Has(ast.ModStatic) {
			return e.outerFieldAccess(r)
		}
		return e.fieldAccess(r, "this")
	case *ast.Var:
		if r == nil {
			return "0"
		}
		if r.Field != nil {
			return "this->f_" + mangle(r.Field.Name)
		}
		return e.localName(r)
	}
	return "0"
}

// outerAccess walks the enclosing-instance chain to the class that owns an
// outer object, for references from a nested or inner class.
func (e *Emitter) outerAccess(target *ast.Class) string {
	recv := "this"
	for cl := e.curClass; cl != nil && cl != target; cl = cl.Outer {
		if cl.OuterField == nil || cl.Outer == nil {
			return "((void*)this)"
		}
		recv = "((" + cname(cl.Outer) + "*)" + recv + "->f_" + mangle(cl.OuterField.Name) + ")"
	}
	return recv
}

// outerFieldAccess reads a field that belongs to an enclosing class.
func (e *Emitter) outerFieldAccess(f *ast.Field) string {
	return "((" + cname(f.Owner) + "*)" + e.outerAccess(f.Owner) + ")->f_" + mangle(f.Name)
}

// nativeIsFinal reports whether the runtime helper a native method maps to is
// the implementation that will actually run.
//
// Object.toString, hashCode and equals are native, but they are also the three
// methods a class is most likely to override. Calling the runtime helper
// directly for a receiver whose static type is Object would skip the override,
// so an overridable method of a class that has subclasses is dispatched through
// the vtable instead, and the vtable slot holds the runtime wrapper.
func (e *Emitter) nativeIsFinal(m *ast.Method) bool {
	if m == nil {
		return true
	}
	if m.IsStatic() || m.Mods.Has(ast.ModFinal) || m.Mods.Has(ast.ModPrivate) {
		return true
	}
	// a receiver typed as a class with no subclasses cannot dispatch anywhere else
	return m.Owner == nil || len(m.Owner.Subclasses) == 0
}

// clinitCall initializes a class before its static state is touched, matching
// Java's lazy class initialization. The initialized flag is tested inline, so
// the common case is a load and a branch instead of a call, and a class whose
// hierarchy has no initializer at all needs no code.
func (e *Emitter) clinitCall(cl *ast.Class) string {
	if cl == nil || cl == e.prog.Builtins.Object || !needsClinit(cl, map[*ast.Class]bool{}) {
		return ""
	}
	return "(void)((cls_" + mangle(cl.Full) + ".flags & TY_CLS_INIT) ? 0 : ty_clinit(&cls_" + mangle(cl.Full) + ")), "
}

// clinitStmt is clinitCall as a statement, for the places that initialise a
// class before allocating one of its instances.
func (e *Emitter) clinitStmt(cl *ast.Class) string {
	if cl == nil || !needsClinit(cl, map[*ast.Class]bool{}) {
		return ""
	}
	n := mangle(cl.Full)
	return "if (!(cls_" + n + ".flags & TY_CLS_INIT)) ty_clinit(&cls_" + n + ");\n"
}

// needsClinit reports whether a class or any class it extends starts with a
// static initializer that has to run.
func needsClinit(cl *ast.Class, seen map[*ast.Class]bool) bool {
	if cl == nil || seen[cl] {
		return false
	}
	seen[cl] = true
	if cl.ClInit != nil {
		return true
	}
	if cl.Super != nil && needsClinit(cl.Super.Class, seen) {
		return true
	}
	for _, i := range cl.Ifaces {
		if needsClinit(i.Class, seen) {
			return true
		}
	}
	return false
}

// fieldAccess renders a field read through a receiver expression.
func (e *Emitter) fieldAccess(f *ast.Field, recv string) string {
	if f.Owner == nil {
		return "0"
	}
	name := "f_" + mangle(f.Name)
	if f.Mods.Has(ast.ModStatic) {
		return "(" + e.clinitCall(f.Owner) + "G_" + mangle(f.Owner.Full) + "_" + mangle(f.Name) + ")"
	}
	ct := cname(f.Owner)
	return "((" + ct + "*)" + recv + ")->" + name
}

func (e *Emitter) selectExpr(v *ast.Select) string {
	if r, ok := v.Ref.(*ast.Class); ok {
		return "((tyobj*)&cls_" + mangle(r.Full) + ")"
	}
	if s, ok := v.Ref.(string); ok && s == "length" {
		arr := e.tmpRef(e.expr(v.X))
		return "(" + arr + " ? " + arr + "->len : (int64_t)ty_aioobe(0, 0))"
	}
	if f, ok := v.Ref.(*ast.Field); ok {
		if f.Mods.Has(ast.ModStatic) {
			return e.fieldAccess(f, "")
		}
		recv := e.expr(v.X)
		return e.fieldAccess(f, e.tmpRef(recv))
	}
	return "0"
}

// boundCheck wraps an array access with an inline range test.
func (e *Emitter) boundCheck(a, i string) string {
	n := e.tmpName()
	return "({ tyarr* " + n + " = (tyarr*)" + a + "; int64_t _i = (int64_t)(" + i + ");" +
		" (_i < 0 || _i >= " + n + "->len) ? (int64_t)(intptr_t)ty_aioobe(_i, " + n + "->len), (int64_t)0 : _i; })"
}

func (e *Emitter) indexExpr(v *ast.Index) string {
	a := e.tmpRef(e.expr(v.X))
	i := e.expr(v.Index)
	elem := v.GetType()
	cast := e.ctype(elem)
	idx := e.boundCheck(a, i)
	if e.isRef(elem) {
		return "(((" + cast + ")((void**)((tyarr*)" + a + ")->data)[" + idx + "]))"
	}
	return "((((" + cast + "*)((tyarr*)" + a + ")->data)[" + idx + "]))"
}

func (e *Emitter) cast(v *ast.Cast) string {
	src := v.X.GetType()
	dst := v.Type.Resolved
	_, sp := src.(*ast.PrimType)
	_, dp := dst.(*ast.PrimType)
	inner := e.expr(v.X)
	switch {
	case sp && dp:
		return "((" + e.ctype(dst) + ")(" + inner + "))"
	case sp && !dp:
		return e.boxCall(inner, src.(*ast.PrimType), dst)
	case !sp && dp:
		return e.unboxCall(inner, src, dst.(*ast.PrimType))
	}
	if ct, ok := dst.(*ast.ClassType); ok {
		return "((" + cname(ct.Class) + "*)ty_checkcast((tyobj*)" + e.refExpr(v.X) + ", &cls_" + mangle(ct.Class.Full) + "))"
	}
	if arr, ok := dst.(*ast.ArrayType); ok {
		_ = arr
		return "((tyarr*)ty_checkcast((tyobj*)" + e.refExpr(v.X) + ", &cls_" + mangle(e.prog.ArrayClass().Full) + "))"
	}
	return "(" + e.ctype(dst) + ")(" + inner + ")"
}

func (e *Emitter) instanceOf(v *ast.InstanceOf) string {
	if e.patternVars != nil {
		if ok, isPrim := e.patternOK[v]; isPrim {
			return "(" + ok + " != 0)"
		}
		if name, ok := e.patternVars[v]; ok {
			return "(" + name + " != NULL)"
		}
	}
	if prim, isPrim := e.prog.Erased(v.Type.Resolved).(*ast.PrimType); isPrim {
		// a primitive pattern used as a value rather than as a condition: the
		// binding is not needed here, only the answer
		return e.primMatchExpr(v, prim)
	}
	dst := v.Type.Resolved
	target := ""
	if ct, ok := dst.(*ast.ClassType); ok {
		target = "&cls_" + mangle(ct.Class.Full)
	} else {
		target = "&cls_" + mangle(e.prog.ArrayClass().Full)
	}
	return "ty_instanceof((tyobj*)" + e.refExpr(v.X) + ", " + target + ")"
}

// primMatchExpr renders the run time question a primitive type pattern asks,
// as a statement expression that yields a boolean.
func (e *Emitter) primMatchExpr(v *ast.InstanceOf, prim *ast.PrimType) string {
	src := e.expr(v.X)
	val := e.tmpName()
	ok := e.tmpName()
	inner := e.capture(func() {
		e.line("%s %s = 0;\n", e.ctype(prim), val)
		e.line("int32_t %s = ty_prim_match((void*)%s, %d, &%s);\n", ok, src, prim.Kind, val)
	})
	return "({ " + inner + " " + ok + " != 0; })"
}

func (e *Emitter) unary(v *ast.Unary) string {
	if v.Op == "++" || v.Op == "--" {
		if pre := e.targetClinit(v.X); pre != "" {
			return "(" + pre + e.unaryInner(v) + ")"
		}
	}
	return e.unaryInner(v)
}

func (e *Emitter) unaryInner(v *ast.Unary) string {
	x := e.expr(v.X)
	xt := v.X.GetType()
	switch v.Op {
	case "+":
		return "(" + x + ")"
	case "-":
		return "(-(" + x + "))"
	case "!":
		if _, ok := xt.(*ast.PrimType); !ok {
			return "(!ty_unbox_bool((void*)(" + x + ")))"
		}
		return "(!(" + x + "))"
	case "~":
		if _, ok := xt.(*ast.PrimType); !ok {
			return "(~(int32_t)ty_unbox_int((void*)(" + x + ")))"
		}
		return "(~(" + x + "))"
	case "++", "--":
		op := "+ 1"
		if v.Op == "--" {
			op = "- 1"
		}
		if v.Postfix {
			return "((" + e.lvalue(v.X) + ") += " + op + ", (" + e.lvalue(v.X) + ") - (" + op + "))"
		}
		return "((" + e.lvalue(v.X) + ") += " + op + ")"
	}
	return x
}

// lvalue renders an assignable expression. Static targets are returned without
// the class-initialization wrapper because a comma expression is not assignable;
// assignClinit adds it around the whole assignment instead.
func (e *Emitter) lvalue(x ast.Expr) string {
	switch v := x.(type) {
	case *ast.Ident:
		if f, ok := v.Ref.(*ast.Field); ok {
			if f.Mods.Has(ast.ModStatic) {
				return "G_" + mangle(f.Owner.Full) + "_" + mangle(f.Name)
			}
			return e.fieldAccess(f, "this")
		}
		return e.ident(v)
	case *ast.Select:
		if f, ok := v.Ref.(*ast.Field); ok {
			if f.Mods.Has(ast.ModStatic) {
				return "G_" + mangle(f.Owner.Full) + "_" + mangle(f.Name)
			}
			return e.fieldAccess(f, e.tmpRef(e.expr(v.X)))
		}
		return "0"
	case *ast.Index:
		a := e.tmpRef(e.expr(v.X))
		i := e.expr(v.Index)
		elem := v.GetType()
		idx := e.boundCheck(a, i)
		if e.isRef(elem) {
			return "((((void**)((tyarr*)" + a + ")->data)[" + idx + "]))"
		}
		return "((((" + e.ctype(elem) + "*)((tyarr*)" + a + ")->data)[" + idx + "]))"
	}
	return e.expr(x)
}

// isFloatingLiteral reports whether a literal is a floating-point value.
func isFloatingLiteral(l *ast.Literal) bool {
	return l.Kind == ast.LitDouble || l.Kind == ast.LitFloat
}

// foldBinary evaluates a binary expression over literals at compile time.
func (e *Emitter) foldBinary(v *ast.Binary) (string, bool) {
	xl, xok := v.X.(*ast.Literal)
	yl, yok := v.Y.(*ast.Literal)
	if !xok || !yok {
		return "", false
	}
	// string concatenation of two literals
	if v.Op == "+" && xl.Kind == ast.LitString && yl.Kind == ast.LitString {
		return e.strLit(xl.Str + yl.Str), true
	}
	pt, isPrim := v.OpType.(*ast.PrimType)
	if !isPrim || !pt.IsNumeric() {
		return "", false
	}
	wide := pt.Kind == ast.Long
	isFloat := isFloatingLiteral(xl) || isFloatingLiteral(yl)
	if isFloat {
		x, y := xl.Flt, yl.Flt
		if !isFloatingLiteral(xl) {
			x = float64(int64(xl.Int))
		}
		if !isFloatingLiteral(yl) {
			y = float64(int64(yl.Int))
		}
		switch v.Op {
		case "+":
			return e.literal(&ast.Literal{Kind: ast.LitDouble, Flt: x + y}), true
		case "-":
			return e.literal(&ast.Literal{Kind: ast.LitDouble, Flt: x - y}), true
		case "*":
			return e.literal(&ast.Literal{Kind: ast.LitDouble, Flt: x * y}), true
		case "/":
			if y != 0 {
				return e.literal(&ast.Literal{Kind: ast.LitDouble, Flt: x / y}), true
			}
		}
		return "", false
	}
	x, y := int64(xl.Int), int64(yl.Int)
	var r int64
	switch v.Op {
	case "+":
		r = x + y
	case "-":
		r = x - y
	case "*":
		r = x * y
	case "/":
		if y == 0 {
			return "", false
		}
		r = x / y
	case "%":
		if y == 0 {
			return "", false
		}
		r = x % y
	case "&":
		r = x & y
	case "|":
		r = x | y
	case "^":
		r = x ^ y
	case "<<":
		r = x << (uint(y) & 63)
	case ">>":
		r = x >> (uint(y) & 63)
	default:
		return "", false
	}
	if !wide {
		r = int64(int32(r))
	}
	kind := ast.LitInt
	if wide {
		kind = ast.LitLong
	}
	return e.literal(&ast.Literal{Kind: kind, Int: uint64(r)}), true
}

func (e *Emitter) binary(v *ast.Binary) string {
	if s, ok := e.foldBinary(v); ok {
		return s
	}
	// `>>>` is an unsigned shift, which C spells with an unsigned operand
	if v.Op == ">>>" {
		ut := "uint32_t"
		if ast.IsPrim(v.OpType, ast.Long) {
			ut = "uint64_t"
		}
		if ast.IsPrim(v.OpType, ast.Byte) || ast.IsPrim(v.OpType, ast.Short) || ast.IsPrim(v.OpType, ast.Char) {
			ut = "uint32_t"
		}
		return "(" + e.ctype(v.GetType()) + ")((" + ut + ")(" + e.expr(v.X) + ") >> " + e.expr(v.Y) + ")"
	}
	lt, rt := v.X.GetType(), v.Y.GetType()
	switch v.Op {
	case "==", "!=":
		return e.equality(v, lt, rt)
	case "&&":
		return "(" + e.cond(v.X) + " && " + e.cond(v.Y) + ")"
	case "||":
		return "(" + e.cond(v.X) + " || " + e.cond(v.Y) + ")"
	case "+":
		if e.isStringType(v.GetType()) {
			return e.concat(v)
		}
	case "/":
		return e.divExpr(v)
	case "%":
		return e.remExpr(v)
	}
	x := e.operand(v.X, v.OpType)
	y := e.operand(v.Y, v.OpType)
	return "(" + x + " " + v.Op + " " + y + ")"
}

func (e *Emitter) isStringType(t ast.Type) bool {
	ct, ok := t.(*ast.ClassType)
	return ok && ct.Class.Special == "String"
}

// operand coerces an operand of a numeric/bitwise operation.
func (e *Emitter) operand(x ast.Expr, op ast.Type) string {
	v := e.expr(x)
	if op == nil {
		return v
	}
	if _, ok := x.GetType().(*ast.PrimType); !ok {
		return e.unboxCall(v, x.GetType(), op.(*ast.PrimType))
	}
	return v
}

func (e *Emitter) equality(v *ast.Binary, lt, rt ast.Type) string {
	ls, lsOk := lt.(*ast.PrimType)
	rs, rsOk := rt.(*ast.PrimType)
	_ = ls
	_ = rs
	switch {
	case lsOk && rsOk:
		return "(" + e.expr(v.X) + " " + v.Op + " " + e.expr(v.Y) + ")"
	case lsOk != rsOk:
		// boxed/unboxed comparison
		x, y := e.expr(v.X), e.expr(v.Y)
		if lsOk {
			y = e.unboxCall(y, rt, ls)
		} else {
			x = e.unboxCall(x, lt, rs)
		}
		return "(" + x + " " + v.Op + " " + y + ")"
	}
	// `==` on two String references is a reference comparison, as in Java: two
	// strings built separately are equal only under equals(). Literals are
	// interned per program by strLit, so `a == "abc"` is still true, and so is a
	// comparison against a concatenation of literals folded at compile time.
	return "(" + e.expr(v.X) + " " + v.Op + " " + e.expr(v.Y) + ")"
}

// divExpr uses a runtime helper for integral division, which throws on a zero
// divisor; floating point division follows IEEE 754 and yields an infinity or
// NaN instead.
func (e *Emitter) divExpr(v *ast.Binary) string {
	x, y := e.operand(v.X, v.OpType), e.operand(v.Y, v.OpType)
	switch {
	case ast.IsPrim(v.OpType, ast.Long):
		return "ty_div_long(" + x + ", " + y + ")"
	case isFloating(v.OpType):
		return "((" + x + ") / (" + y + "))"
	}
	return "ty_div_int(" + x + ", " + y + ")"
}

func (e *Emitter) remExpr(v *ast.Binary) string {
	x, y := e.operand(v.X, v.OpType), e.operand(v.Y, v.OpType)
	switch {
	case ast.IsPrim(v.OpType, ast.Long):
		return "ty_rem_long(" + x + ", " + y + ")"
	case isFloating(v.OpType):
		return "fmod(" + x + ", " + y + ")"
	}
	return "ty_rem_int(" + x + ", " + y + ")"
}

// isFloating reports whether a type is float or double.
func isFloating(t ast.Type) bool {
	return ast.IsPrim(t, ast.Double) || ast.IsPrim(t, ast.Float)
}

// concat renders Java string concatenation.
func (e *Emitter) concat(v *ast.Binary) string {
	parts := []string{}
	var collect func(ast.Expr)
	collect = func(x ast.Expr) {
		if b, ok := x.(*ast.Binary); ok && b.Op == "+" && e.isStringType(b.GetType()) {
			collect(b.X)
			collect(b.Y)
			return
		}
		parts = append(parts, e.stringOperand(x))
	}
	collect(v.X)
	collect(v.Y)
	out := parts[0]
	for _, p := range parts[1:] {
		out = "ty_str_concat(" + out + ", " + p + ")"
	}
	return out
}

func (e *Emitter) stringOperand(x ast.Expr) string {
	t := x.GetType()
	if e.isStringType(t) {
		return "(tystr*)" + e.expr(x)
	}
	if p, ok := t.(*ast.PrimType); ok {
		switch p.Kind {
		case ast.Boolean:
			return "ty_str_of_bool(" + e.expr(x) + ")"
		case ast.Char:
			return "ty_str_of_char(" + e.expr(x) + ")"
		case ast.Float:
			return "ty_str_of_float(" + e.expr(x) + ")"
		case ast.Double:
			return "ty_str_of_double(" + e.expr(x) + ")"
		case ast.Byte, ast.Short, ast.Int, ast.Long:
			return "ty_str_of_long(" + e.expr(x) + ")"
		}
	}
	return "ty_str_of_obj((tyobj*)" + e.expr(x) + ")"
}

// targetClinit returns the class-initialization prefix for a static target.
func (e *Emitter) targetClinit(x ast.Expr) string {
	var ref any
	switch t := x.(type) {
	case *ast.Ident:
		ref = t.Ref
	case *ast.Select:
		ref = t.Ref
	}
	if f, ok := ref.(*ast.Field); ok && f.Mods.Has(ast.ModStatic) && f.Owner != nil {
		return e.clinitCall(f.Owner)
	}
	return ""
}

func (e *Emitter) assign(v *ast.Assign) string {
	pre := e.targetClinit(v.X)
	lv := e.lvalue(v.X)
	if pre != "" {
		return "(" + pre + e.assignInner(v, lv) + ")"
	}
	return e.assignInner(v, lv)
}

func (e *Emitter) assignInner(v *ast.Assign, lv string) string {
	if v.Op == "=" {
		return "(" + lv + " = " + e.coerce(e.expr(v.Y), v.Y.GetType(), v.X.GetType()) + ")"
	}
	if v.Op == "+=" && e.isStringType(v.X.GetType()) {
		// string += is concatenation
		return "(" + lv + " = (tystr*)" + e.concat(&ast.Binary{
			ExprBase: ast.ExprBase{Pos: v.Pos, T: v.X.GetType()}, Op: "+", X: v.X, Y: v.Y}) + ")"
	}
	op := v.Op[:len(v.Op)-1]
	if op == ">>>" {
		ut := "uint32_t"
		if ast.IsPrim(v.X.GetType(), ast.Long) {
			ut = "uint64_t"
		}
		return "(" + lv + " = (" + e.ctype(v.X.GetType()) + ")((" + ut + ")" + lv + " >> " + e.expr(v.Y) + "))"
	}
	if op == "/" || op == "%" {
		xt := v.X.GetType()
		if isFloating(v.OpType) {
			// floating point division and remainder never throw
			call := "((" + lv + ") " + op + " (" + e.expr(v.Y) + "))"
			if op == "%" {
				call = "fmod(" + lv + ", " + e.expr(v.Y) + ")"
			}
			return "(" + lv + " = (" + e.ctype(xt) + ")" + call + ")"
		}
		fn := "ty_div_int"
		if op == "%" {
			fn = "ty_rem_int"
		}
		if ast.IsPrim(xt, ast.Long) {
			if op == "%" {
				fn = "ty_rem_long"
			} else {
				fn = "ty_div_long"
			}
		}
		return "(" + lv + " = (" + e.ctype(xt) + ")" + fn + "(" + lv + ", " + e.expr(v.Y) + "))"
	}
	return "(" + lv + " " + op + "= " + e.operand(v.Y, v.OpType) + ")"
}

// ---------------------------------------------------------------- calls

func (e *Emitter) callExpr(v *ast.Call) string {
	m := v.Method
	if m == nil {
		// arrays have Object-like methods but no method symbol
		if arr, ok := v.Recv.GetType().(ast.Type); ok {
			if at, isArr := arr.(*ast.ArrayType); isArr {
				switch v.Name {
				case "clone":
					return "((tyarr*)ty_array_clone((tyarr*)" + e.expr(v.Recv) + ", " + e.elemSize(at.Elem) + "))"
				case "toString":
					return "((tystr*)ty_str_intern(\"[array]\"))"
				case "hashCode":
					return "ty_object_hash((void*)" + e.expr(v.Recv) + ")"
				case "equals":
					a := e.tmpRef(e.expr(v.Recv))
					var other string
					if len(v.Args) > 0 {
						other = e.expr(v.Args[0])
					} else {
						other = "NULL"
					}
					return "((void*)" + a + " == (void*)" + other + ")"
				}
			}
		}
		return "0"
	}
	recv := ""
	if !m.IsStatic() {
		switch {
		case v.Recv == nil:
			recv = "this"
		case v.Super:
			recv = "((void*)this)"
		default:
			recv = e.expr(v.Recv)
		}
	}
	if _, ok := nativeTable[nativeKey(m)]; ok && e.nativeIsFinal(m) {
		return e.nativeCall(m, recv, v.Args)
	}
	name := e.cfunc(m)
	a := e.argsFor(recv, v.Args, m, v)
	if m.External {
		return m.Native + "(" + a + ")"
	}
	if m.IsStatic() && !v.Super && v.Recv != nil {
		// touching a static member initializes its class first
		return "(" + e.clinitCall(m.Owner) + name + "(" + a + "))"
	}
	if v.Static || m.IsStatic() || v.Super {
		// super calls bind statically to the superclass implementation
		return name + "(" + a + ")"
	}
	if v.Recv == nil {
		if m.Selector >= 0 || m.VIndex >= 0 {
			return e.virtCall(m, cname(m.Owner)+"*", "this", v.Args)
		}
		return name + "(" + a + ")"
	}
	if (m.Selector >= 0 || m.VIndex >= 0) && !m.Mods.Has(ast.ModPrivate) {
		return e.virtCall(m, cname(m.Owner)+"*", recv, v.Args)
	}
	return name + "(" + a + ")"
}

// virtCall dispatches through the vtable or, for interface receivers, the itable.
func (e *Emitter) virtCall(m *ast.Method, recvT, recv string, args []ast.Expr) string {
	if recvT == "" {
		recvT = "void*"
	}
	if m.Selector >= 0 {
		fn := "((void*)ty_itab((tyobj*)" + recv + ", " + fmt.Sprint(m.Selector) + "))"
		return e.indirect(m, fn, "void*", recv, args)
	}
	fn := "((" + recv + ")->obj.cls->vtable[" + fmt.Sprint(m.VIndex) + "])"
	return e.indirect(m, fn, recvT, recv, args)
}

// indirect builds a call through a runtime-resolved function pointer.
func (e *Emitter) indirect(m *ast.Method, fn, recvT, recv string, args []ast.Expr) string {
	ret := e.ctype(m.Result)
	if ret == "void" {
		ret = "void"
	}
	var ps []string
	if !m.IsStatic() {
		ps = append(ps, recvT)
	}
	for _, p := range m.Params {
		ps = append(ps, e.ctype(p))
	}
	if len(ps) == 0 {
		ps = append(ps, "void")
	}
	a := e.args(recv, args, m)
	sig := "(( " + ret + "(*)(" + strings.Join(ps, ", ") + "))" + fn + ")"
	if ret == "void" {
		if a == "" {
			return sig + "()"
		}
		return sig + "(" + a + ")"
	}
	return sig + "(" + a + ")"
}

// ---------------------------------------------------------------- new

func (e *Emitter) newExpr(v *ast.New) string {
	ct, ok := v.GetType().(*ast.ClassType)
	if !ok {
		if ct2, ok2 := e.prog.Erased(v.GetType()).(*ast.ClassType); ok2 {
			ct, ok = ct2, true
		} else {
			return "NULL"
		}
	}
	cl := ct.Class
	// a native constructor is implemented by a runtime helper
	if v.Ctor != nil {
		if nf, ok := nativeTable[nativeKey(v.Ctor)]; ok && nf.fn != "" {
			args := make([]string, 0, len(v.Args))
			for i, a := range v.Args {
				var want ast.Type
				if i < len(v.Ctor.Params) {
					want = v.Ctor.Params[i]
				}
				args = append(args, e.coerce(e.expr(a), a.GetType(), want))
			}
			if nf.recv != "" && len(args) > 0 {
				args[0] = "(" + nf.recv + ")" + args[0]
			}
			return "(" + e.ctype(ct) + ")" + nf.fn + "(" + strings.Join(args, ", ") + ")"
		}
	}
	if fn, ok := specialNew[cl.Name]; ok && len(v.Args) == 0 {
		p := cname(cl) + "*"
		return "({ " + p + " _o = (" + p + ")" + fn + "(); _o->obj.cls = &cls_" + mangle(cl.Full) + "; _o; })"
	}
	n := e.tmpName()
	var b strings.Builder
	fmt.Fprintf(&b, "({ %s %s = (%s)ty_alloc(sizeof(%s)); %s->obj.cls = &cls_%s;",
		cname(cl)+"*", n, cname(cl)+"*", cname(cl), n, mangle(cl.Full))
	if cl.Inner && cl.OuterField != nil {
		fmt.Fprintf(&b, " %s->f_%s = (%s*)%s;", n, mangle(cl.OuterField.Name), cname(cl.Outer), e.outerArg(v))
	}
	if init := e.clinitStmt(cl); init != "" {
		fmt.Fprintf(&b, " %s", strings.TrimSuffix(init, "\n"))
	}
	if v.Ctor != nil {
		fmt.Fprintf(&b, " %s(%s);", e.cfunc(v.Ctor), e.argsWithCaptures(n, v, cl))
	}
	fmt.Fprintf(&b, " %s; })", n)
	return b.String()
}

// argsWithCaptures builds the argument list of a constructor call. An
// anonymous class captures the locals its body uses, and receives them as
// trailing parameters after the ones forwarded to the superclass constructor.
func (e *Emitter) argsWithCaptures(n string, v *ast.New, cl *ast.Class) string {
	a := e.args(n, v.Args, v.Ctor)
	if !cl.Anon {
		return a
	}
	for _, cv := range e.prog.CapturedVars(cl) {
		if a != "" {
			a += ", "
		}
		a += e.coerce(e.localName(cv), cv.Type, cv.Type)
	}
	return a
}

func (e *Emitter) outerArg(v *ast.New) string {
	if v.Outer != nil {
		return e.expr(v.Outer)
	}
	return "this"
}

func (e *Emitter) newArray(v *ast.NewArray) string {
	elem := v.Elem.Resolved
	es := e.elemSize(elem)
	refs := "0"
	if e.isRefElem(elem) {
		refs = "1"
	}
	if v.Init != nil {
		return e.arrayInitOf(v.Init, elem)
	}
	if len(v.Dims) == 0 {
		return "({ tyarr* _a = ty_array_new(0, " + es + "); _a->refs = " + refs + "; _a; })"
	}
	dim := e.expr(v.Dims[0])
	if len(v.Dims) == 1 {
		return "({ tyarr* _a = ty_array_new(" + dim + ", " + es + "); _a->refs = " + refs + "; _a; })"
	}
	// multi-dimensional creation allocates the inner arrays as well
	elemDims := &ast.NewArray{ExprBase: ast.ExprBase{Pos: v.Pos, T: v.GetType()}, Elem: v.Elem, Dims: v.Dims[1:], Extra: v.Extra}
	inner := e.newArray(elemDims)
	n := e.tmpName()
	i := e.tmpName()
	var b strings.Builder
	fmt.Fprintf(&b, "({ tyarr* %s = ty_array_new(%s, 8); %s->refs = 1;", n, dim, n)
	fmt.Fprintf(&b, " for (int64_t %s = 0; %s < %s->len; %s++) ((void**)%s->data)[%s] = (void*)(%s);", i, i, n, i, n, i, inner)
	fmt.Fprintf(&b, " %s; })", n)
	return b.String()
}

func (e *Emitter) arrayInit(v *ast.ArrayInit) string {
	return e.arrayInitOf(v, v.Elem)
}

func (e *Emitter) arrayInitOf(v *ast.ArrayInit, elem ast.Type) string {
	es := e.elemSize(elem)
	refs := "0"
	if e.isRefElem(elem) {
		refs = "1"
	}
	var b strings.Builder
	n := e.tmpName()
	fmt.Fprintf(&b, "({ tyarr* %s = ty_array_new(%d, %s); %s->refs = %s;", n, len(v.Elems), es, n, refs)
	for i, el := range v.Elems {
		val := e.arrayElemValue(el, elem)
		if e.isRefElem(elem) {
			fmt.Fprintf(&b, " ((void**)%s->data)[%d] = (void*)%s;", n, i, val)
		} else {
			fmt.Fprintf(&b, " ((%s*)%s->data)[%d] = %s;", e.ctype(elem), n, i, val)
		}
	}
	fmt.Fprintf(&b, " %s; })", n)
	return b.String()
}

func (e *Emitter) arrayElemValue(el ast.Expr, elem ast.Type) string {
	if ai, ok := el.(*ast.ArrayInit); ok {
		// a nested initializer builds an array of the element type one level
		// deeper, which is the type of the initializer itself
		if at, ok2 := el.GetType().(*ast.ArrayType); ok2 {
			return e.arrayInitOf(ai, at.Elem)
		}
		return e.arrayInitOf(ai, elem)
	}
	return e.coerce(e.expr(el), el.GetType(), elem)
}

// ---------------------------------------------------------------- lambdas

func (e *Emitter) lambdaExpr(lam *ast.Lambda) string {
	cl := lam.Class
	if cl == nil {
		return "NULL"
	}
	n := e.tmpName()
	var b strings.Builder
	fmt.Fprintf(&b, "({ %s %s = (%s)ty_alloc(sizeof(%s)); %s->obj.cls = &cls_%s;",
		cname(cl)+"*", n, cname(cl)+"*", cname(cl), n, mangle(cl.Full))
	for _, v := range e.prog.CapturedVars(cl) {
		if lam.RecvVar == v && lam.RecvExpr != nil {
			// the receiver of a bound method reference is evaluated here and
			// once only, so the closure sees a stable object
			fmt.Fprintf(&b, " %s->cap_%s = %s;", n, mangle(v.Name), e.refExpr(lam.RecvExpr))
			continue
		}
		fmt.Fprintf(&b, " %s->cap_%s = %s;", n, mangle(v.Name), e.localName(v))
	}
	if lam.CapThis {
		fmt.Fprintf(&b, " %s->cap_this = (%s*)this;", n, cname(cl))
	}
	fmt.Fprintf(&b, " %s; })", n)
	return b.String()
}

func (e *Emitter) emitLambdaMethod(cl *ast.Class, m *ast.Method) {
	lam := m.Lambda
	if lam == nil || m.IsCtor {
		return
	}
	fmt.Fprintf(&e.fns, "static %s;\n", e.signature(m))
	e.indent = 0
	fmt.Fprintf(e.code, "static %s {\n", e.signature(m))
	e.indent++
	for i, pv := range m.ParamVars {
		e.locals[pv] = fmt.Sprintf("a%d", i)
	}
	// captured locals live in fields of the synthetic lambda class
	e.curLambda = lam
	for v, f := range cl.CapFields {
		e.locals[v] = "this->cap_" + mangle(f.Name)
	}
	switch b := lam.Body.(type) {
	case ast.Expr:
		if lam.ExprStmt {
			e.line("%s;\n", e.expr(b))
		} else {
			e.line("return %s;\n", e.coerce(e.expr(b), b.GetType(), m.Result))
		}
	case *ast.Block:
		e.emitBlockInner(b)
	}
	e.indent--
	e.code.WriteString("}\n\n")
}
