package codegen

// The members the compiler writes for a record or an enum, lowered to LLVM IR.
//
// They are not a feature of their own: a record's canonical constructor is a
// store per component, its accessors are loads, and its toString/hashCode/equals
// are the definitions JLS 8.10.3 gives them -- which is what emit_synth.go
// writes for the C back end, in the same order and with the same rules, and the
// two have to agree because a program's output is what they print.
//
// An enum is a class whose constants are instances of it, so the constants are
// built by the class's own static initializer (with their ordinal, their name,
// and the arguments they were declared with), and values()/valueOf() are the two
// methods that read them back.
//
// What is deliberately not duplicated: how two references compare. equals calls
// the runtime's ty_obj_equal and ty_str_eq, which are the runtime's own answers
// to that question, rather than a second copy of the rule here.

import (
	"fmt"

	"github.com/teyru-lang/Teyru/internal/ast"
	"github.com/teyru-lang/Teyru/internal/util"
)

// fieldByName is a field of a class by name, for the members that read a class
// other than the one being compiled: an enum's ordinal and name live on the base
// class the prelude declares.
// It walks the superclasses, because an enum's ordinal and name are declared on
// teyru.Enum and inherited by every constant's class -- which is exactly the
// layout the runtime reads them through when it casts to tyEnumBase.
func (e *llvmEmitter) fieldByName(cl *ast.Class, name string) *ast.Field {
	for c := cl; c != nil; {
		for _, f := range c.Fields {
			if f.Name == name {
				return f
			}
		}
		if c.Super == nil {
			break
		}
		c = c.Super.Class
	}
	return nil
}

// synthBody writes a synthesized record, enum or array member, and answers with
// false when the kind is one this file does not know so that the caller can
// refuse with the message it already has.
func (e *llvmEmitter) synthBody(f *fb, m *ast.Method, kind string) bool {
	cl := m.Owner
	switch kind {
	case "record-get":
		if m.Prop == nil {
			return false
		}
		f.retVal(e.load(f, e.fieldAddr(f, m.Prop, "%this", false), m.Prop.Type).v)
		return true
	case "record-toString":
		f.retVal(e.recordToString(f, cl).v)
		return true
	case "record-hashCode":
		f.retVal(e.recordHashCode(f, cl).v)
		return true
	case "record-equals":
		e.recordEquals(f, cl, "%a0")
		return true
	case "enum-values":
		f.retVal(e.enumValues(f, cl).v)
		return true
	case "enum-valueOf":
		e.enumValueOf(f, cl)
		return true
	}
	if len(kind) > 6 && kind[:6] == "array-" {
		e.arraySynth(f, kind)
		return true
	}
	return false
}

// recordAssign stores the constructor's parameters into the record's components:
// the whole of what a record's canonical constructor does.
func (e *llvmEmitter) recordAssign(f *fb, cl *ast.Class, m *ast.Method) {
	for _, rc := range cl.Decl.RecordComps {
		fd := cl.FieldMap[rc.Name]
		if fd == nil {
			continue
		}
		i := indexOfName(m.ParamNames, rc.Name)
		if i >= len(m.Params) {
			continue
		}
		e.store(f, e.fieldAddr(f, fd, "%this", false), fd.Type,
			e.coerce(f, value(fmt.Sprintf("%%a%d", i), m.Params[i]), fd.Type))
	}
	// a record is a teyru.Record, and its base is initialized before its own
	// components exist
	e.clinitIfNeeded(f, e.p.Builtins.Record)
}

// componentString renders one record component the way a `+` would, which is
// what toString shows.
func (e *llvmEmitter) componentString(f *fb, fd *ast.Field) lval {
	return value(e.stringOperandValue(f, e.load(f, e.fieldAddr(f, fd, "%this", false), fd.Type)),
		e.stringType())
}

// stringOperandValue renders an already-lowered value as a String the way a
// concatenation does, which is what a record's toString needs of a component.
func (e *llvmEmitter) stringOperandValue(f *fb, v lval) string {
	t := e.p.Erased(v.t)
	if e.isStringType(t) {
		return v.v
	}
	if p, ok := t.(*ast.PrimType); ok {
		switch p.Kind {
		case ast.Boolean:
			return e.rtCall(f, "ty_str_of_bool", e.stringType(), []lval{v}).v
		case ast.Char:
			return e.rtCall(f, "ty_str_of_char", e.stringType(), []lval{v}).v
		case ast.Float, ast.Double:
			return e.rtCall(f, "ty_str_of_double", e.stringType(),
				[]lval{e.coerce(f, v, ast.TDouble)}).v
		case ast.Byte, ast.Short, ast.Int, ast.Long:
			return e.rtCall(f, "ty_str_of_long", e.stringType(),
				[]lval{e.coerce(f, v, ast.TLong)}).v
		}
	}
	return e.rtCall(f, "ty_str_of_obj", e.stringType(), []lval{v}).v
}

// intern is a Teyru string literal as a runtime string.
func (e *llvmEmitter) intern(f *fb, s string) string {
	return e.rtCall(f, "ty_str_intern", e.stringType(),
		[]lval{value(e.cstring(s), e.refType())}).v
}

// concat2 is two strings joined, as a String operand.
func (e *llvmEmitter) concat2(f *fb, a, b string) string {
	return e.rtCall(f, "ty_str_concat", e.stringType(), []lval{
		value(a, e.stringType()), value(b, e.stringType())}).v
}

// recordToString writes `Name[a=..., b=...]`, in the order the record declares
// its components.
func (e *llvmEmitter) recordToString(f *fb, cl *ast.Class) lval {
	out := e.intern(f, cl.Name+"[")
	first := true
	for _, rc := range cl.Decl.RecordComps {
		fd := cl.FieldMap[rc.Name]
		if fd == nil {
			continue
		}
		if !first {
			out = e.concat2(f, out, e.intern(f, ", "))
		}
		first = false
		label := e.intern(f, rc.Name+"=")
		got := e.componentString(f, fd)
		out = e.concat2(f, out, e.concat2(f, label, got.v))
	}
	return value(e.concat2(f, out, e.intern(f, "]")), e.stringType())
}

// recordHashCode folds the components the way JLS 8.10.3 says: thirty-one times
// the running hash plus the component's own hash, where a long contributes its
// two halves and a reference its own hashCode through the vtable.
func (e *llvmEmitter) recordHashCode(f *fb, cl *ast.Class) lval {
	h := f.tempSlot(ast.TInt, "recHash")
	e.store(f, h, ast.TInt, value("1", ast.TInt))
	for _, rc := range cl.Decl.RecordComps {
		fd := cl.FieldMap[rc.Name]
		if fd == nil {
			continue
		}
		v := e.load(f, e.fieldAddr(f, fd, "%this", false), fd.Type)
		var add lval
		if p, ok := e.p.Erased(fd.Type).(*ast.PrimType); ok {
			switch p.Kind {
			case ast.Boolean:
				c := f.reg()
				sel := f.reg()
				f.ins(fmt.Sprintf("%s = icmp ne i32 %s, 0", c, v.v))
				f.ins(fmt.Sprintf("%s = select i1 %s, i32 1231, i32 1237", sel, c))
				add = value(sel, ast.TInt)
			case ast.Char, ast.Byte, ast.Short, ast.Int:
				add = value(e.convertTo(f, v, "i32"), ast.TInt)
			case ast.Long:
				hi := f.reg()
				x := f.reg()
				f.ins(fmt.Sprintf("%s = lshr i64 %s, 32", hi, v.v))
				f.ins(fmt.Sprintf("%s = xor i64 %s, %s", x, v.v, hi))
				add = value(e.convertTo(f, value(x, ast.TLong), "i32"), ast.TInt)
			default:
				add = value(e.convertTo(f, v, "i32"), ast.TInt)
			}
		} else {
			add = e.refHashCode(f, v)
		}
		cur := e.load(f, h, ast.TInt)
		mul := f.reg()
		sum := f.reg()
		f.ins(fmt.Sprintf("%s = mul i32 %s, 31", mul, cur.v))
		f.ins(fmt.Sprintf("%s = add i32 %s, %s", sum, mul, add.v))
		e.store(f, h, ast.TInt, value(sum, ast.TInt))
	}
	return e.load(f, h, ast.TInt)
}

// refHashCode is a component's own hashCode, through its class's vtable, and
// zero for a component that is null -- which is the runtime's ty_obj_hash rule.
func (e *llvmEmitter) refHashCode(f *fb, v lval) lval {
	isNull := f.reg()
	f.ins(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.v))
	call := f.nextLabel("rh_call")
	join := f.nextLabel("rh_join")
	from := f.cur
	f.cbr(isNull, join, call)
	f.label(call)
	cls := e.loadRaw(f, "ptr", v.v, 8)
	vtp := f.reg()
	f.ins(fmt.Sprintf("%s = getelementptr inbounds %%tyclass, ptr %s, i32 0, i32 %d",
		vtp, cls, e.tyclassIdx["vtable"]))
	vt := e.loadRaw(f, "ptr", vtp, 8)
	fn := e.loadRaw(f, "ptr", e.gepIndex(f, "ptr", vt, "1"), 8)
	got := f.reg()
	f.ins(fmt.Sprintf("%s = call i32 %s(%s)", got, fn, argOperand("ptr", v.v)))
	f.br(join)
	f.label(join)
	out := f.reg()
	f.ins(fmt.Sprintf("%s = phi i32 [ 0, %%%s ], [ %s, %%%s ]", out, from, got, call))
	return value(out, ast.TInt)
}

// recordEquals writes the record's equality: identity, then a class test, then a
// component-by-component comparison. Every failing path returns zero where it
// fails, so no path has to remember what it decided.
func (e *llvmEmitter) recordEquals(f *fb, cl *ast.Class, other string) {
	diff := f.nextLabel("req_diff")
	body := f.nextLabel("req_body")
	same := f.nextLabel("req_same")
	c := f.reg()
	f.ins(fmt.Sprintf("%s = icmp eq ptr %%this, %s", c, other))
	f.cbr(c, same, body)
	f.label(body)
	isNull := f.reg()
	notNull := f.nextLabel("req_notnull")
	f.ins(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, other))
	f.cbr(isNull, diff, notNull)
	f.label(notNull)
	e.markExn("TY_CCE", e.p.Builtins.CCE)
	inst := e.rtCall(f, "ty_instanceof", ast.TBoolean, []lval{
		value(other, e.refType()),
		value("@cls_"+util.Mangle(cl.Full), e.refType()),
	})
	ok := f.reg()
	typed := f.nextLabel("req_typed")
	f.ins(fmt.Sprintf("%s = icmp ne i32 %s, 0", ok, inst.v))
	f.cbr(ok, typed, diff)
	f.label(typed)
	for _, rc := range cl.Decl.RecordComps {
		fd := cl.FieldMap[rc.Name]
		if fd == nil {
			continue
		}
		mine := e.load(f, e.fieldAddr(f, fd, "%this", false), fd.Type)
		theirs := e.load(f, e.fieldAddr(f, fd, other, false), fd.Type)
		var match string
		if !e.isRef(fd.Type) {
			eq := f.reg()
			f.ins(fmt.Sprintf("%s = icmp eq %s %s, %s", eq, e.llvmType(fd.Type), mine.v, theirs.v))
			match = eq
		} else {
			var eq lval
			if e.isStringType(fd.Type) {
				eq = e.rtCall(f, "ty_str_eq", ast.TBoolean, []lval{
					value(mine.v, e.refType()), value(theirs.v, e.refType())})
			} else {
				eq = e.rtCall(f, "ty_obj_equal", ast.TBoolean, []lval{
					value(mine.v, e.refType()), value(theirs.v, e.refType())})
			}
			c := f.reg()
			f.ins(fmt.Sprintf("%s = icmp ne i32 %s, 0", c, eq.v))
			match = c
		}
		next := f.nextLabel("req_next")
		f.cbr(match, next, diff)
		f.label(next)
	}
	f.br(same)
	f.label(same)
	f.retVal("1")
	f.label(diff)
	f.retVal("0")
}

// enumValues builds the array values() answers with: a fresh array of the
// constants, which is what Java hands out for every call.
func (e *llvmEmitter) enumValues(f *fb, cl *ast.Class) lval {
	e.useArray()
	elem := e.classType(cl)
	arr := e.allocArray(f, elem, int64(len(cl.EnumConsts)))
	for i, fd := range cl.EnumConsts {
		cur := e.loadRaw(f, "ptr", e.staticGlobal(cl, fd), 8)
		e.store(f, e.elemAddrOf(f, arr, int64(i), elem), elem, value(cur, elem))
	}
	return value(arr, &ast.ArrayType{Elem: elem})
}

// enumValueOf looks a constant up by name and throws what Java throws when
// there is none.
func (e *llvmEmitter) enumValueOf(f *fb, cl *ast.Class) {
	name := e.fieldByName(cl, "name")
	if name == nil {
		e.refuse(noPos, "the enum %s: its base class has no name field to look a constant up by", cl.Full)
	}
	for _, fd := range cl.EnumConsts {
		cur := e.loadRaw(f, "ptr", e.staticGlobal(cl, fd), 8)
		same := e.rtCall(f, "ty_str_eq", ast.TBoolean, []lval{
			value("%a0", e.refType()),
			value(e.load(f, e.fieldAddr(f, name, cur, false), name.Type).v, e.refType()),
		})
		hit := f.nextLabel("ev_hit")
		next := f.nextLabel("ev_next")
		c := f.reg()
		f.ins(fmt.Sprintf("%s = icmp ne i32 %s, 0", c, same.v))
		f.cbr(c, hit, next)
		f.label(hit)
		f.retVal(cur)
		f.label(next)
	}
	e.markExn("TY_ILLARG", e.p.Builtins.IllArg)
	ex := e.rtCall(f, "ty_illarg", e.refType(),
		[]lval{value(e.cstring("No enum constant "+cl.Full), e.refType())})
	e.decl("ty_throw", "void", []string{"ptr"}, " noreturn")
	f.ins(fmt.Sprintf("call void @ty_throw(ptr %s)", ex.v))
	f.unreachable()
}

// enumInit builds a class's constants: one instance each, carrying its ordinal
// and its name, and the arguments the constant was declared with passed to the
// enum's own constructor -- a constant that declares arguments has a constructor
// that uses them, and dropping them is a bug the C back end's history already
// records.
func (e *llvmEmitter) enumInit(f *fb, cl *ast.Class) {
	if cl.Kind != ast.KindEnum || cl.Decl == nil {
		return
	}
	ordinal := e.fieldByName(cl, "ordinal")
	name := e.fieldByName(cl, "name")
	for i, ec := range cl.Decl.EnumConsts {
		fd := cl.FieldMap[ec.Name]
		if fd == nil {
			continue
		}
		// a constant with a body is an instance of a subclass of the enum
		cls := cl
		for _, sub := range cl.Subclasses {
			if sub.Name == cl.Name+"$"+ec.Name {
				cls = sub
			}
		}
		e.instantiate(cls)
		obj := e.allocInstance(f, cls)
		f.ins(fmt.Sprintf("store ptr @cls_%s, ptr %s", util.Mangle(cls.Full), obj))
		if ordinal != nil {
			e.store(f, e.fieldAddr(f, ordinal, obj, false), ast.TInt, value(fmt.Sprint(i), ast.TInt))
		}
		if name != nil {
			e.store(f, e.fieldAddr(f, name, obj, false), name.Type,
				value(e.intern(f, ec.Name), e.stringType()))
		}
		f.ins(fmt.Sprintf("store ptr %s, ptr %s", obj, e.staticGlobal(cl, fd)))
		ctor := enumCtor(cls, len(ec.Args))
		if ctor == nil {
			ctor = enumCtor(cl, len(ec.Args))
		}
		if ctor != nil {
			args := make([]lval, 0, len(ec.Args))
			for j, a := range ec.Args {
				var want ast.Type
				if j < len(ctor.Params) {
					want = ctor.Params[j]
				}
				args = append(args, e.coerce(f, e.expr(f, a), want))
			}
			e.directCall(f, ctor, obj, args)
		}
	}
}

// arraySynth writes one of the four methods the array class carries, which is
// what an array's own vtable slots hold: the runtime calls slot 0 on whatever it
// is handed, and an array is a thing it is handed.
func (e *llvmEmitter) arraySynth(f *fb, kind string) {
	switch kind {
	case "array-tostring":
		f.retVal(e.intern(f, "[array]"))
	case "array-hashcode":
		f.retVal("0")
	case "array-equals":
		c := f.reg()
		f.ins(fmt.Sprintf("%s = icmp eq ptr %%this, %%a0", c))
		r := f.reg()
		f.ins(fmt.Sprintf("%s = zext i1 %s to i32", r, c))
		f.retVal(r)
	case "array-clone":
		f.retVal("%this")
	default:
		e.refuse(noPos, "the synthesized array member %s", kind)
	}
}
