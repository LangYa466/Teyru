package codegen

import (
	"fmt"

	"github.com/LangYa466/Teyru/internal/ast"
)

// staticName is the C global holding a class's static field.
func staticName(cl *ast.Class, f *ast.Field) string {
	return "G_" + mangle(cl.Full) + "_" + mangle(f.Name)
}

// emitStaticFields declares the C globals for a class's static fields.
func (e *Emitter) emitStaticFields(cl *ast.Class) {
	for _, f := range cl.Fields {
		if !f.Mods.Has(ast.ModStatic) {
			continue
		}
		ct := e.ctype(f.Type)
		fmt.Fprintf(&e.data, "static %s %s = %s;\n", ct, staticName(cl, f), zeroOf(ct))
	}
}

// clinitRefs lists the static field addresses the collector has to treat as
// roots. The address of the field itself is registered, because the collector
// reads *(void**)address to find the object: one extra level of indirection
// would make it mark the address of the global instead of what the global
// points at, and an object reachable only from a static would be collected.
func (e *Emitter) clinitRefs() string {
	var b []byte
	for _, cl := range e.prog.Classes {
		for _, f := range cl.Fields {
			if f.Mods.Has(ast.ModStatic) {
				b = append(b, []byte("\tty_gc_register_static((void*)&"+staticName(cl, f)+");\n")...)
			}
		}
	}
	return string(b)
}

// emitClInit writes a class's static initializer.
func (e *Emitter) emitClInit(cl *ast.Class) {
	if cl.ClInit == nil {
		return
	}
	m := cl.ClInit
	fmt.Fprintf(&e.fns, "static %s;\n", e.signature(m))
	e.indent = 0
	fmt.Fprintf(e.code, "static %s {\n", e.signature(m))
	e.indent++
	if cl.Super != nil {
		e.line("ty_clinit(&cls_%s);\n", mangle(cl.Super.Class.Full))
	}
	if cl.Decl != nil {
		for _, mem := range cl.Decl.Members {
			fd, ok := mem.(*ast.FieldDecl)
			if !ok {
				continue
			}
			for _, vd := range fd.Vars {
				f := vd.Fld
				if f == nil || !f.Mods.Has(ast.ModStatic) || vd.Init == nil {
					continue
				}
				e.line("%s = %s;\n", staticName(cl, f), e.coerce(e.expr(vd.Init), vd.Init.GetType(), f.Type))
			}
		}
		for _, mem := range cl.Decl.Members {
			if ib, ok := mem.(*ast.InitBlock); ok && ib.Static {
				e.emitBlockInner(ib.Body)
			}
		}
	}
	// synthesized static initializers (@Log and friends)
	for _, f := range cl.Fields {
		if f.InitExpr != nil && f.Mods.Has(ast.ModStatic) && (f.Decl == nil || f.Decl.Init == nil) {
			e.line("%s = %s;\n", staticName(cl, f), e.coerce(e.expr(f.InitExpr), f.InitExpr.GetType(), f.Type))
		}
	}
	e.emitEnumInit(cl)
	e.indent--
	e.code.WriteString("}\n\n")
}

// emitEnumInit materialises the enum constants.
func (e *Emitter) emitEnumInit(cl *ast.Class) {
	if cl.Kind != ast.KindEnum {
		return
	}
	for _, ec := range cl.Decl.EnumConsts {
		f := cl.FieldMap[ec.Name]
		if f == nil {
			continue
		}
		cls := cl
		for _, sub := range cl.Subclasses {
			if sub.Name == cl.Name+"$"+ec.Name {
				cls = sub
			}
		}
		g := staticName(cl, f)
		e.line("%s = (%s)ty_alloc(sizeof(%s));\n", g, e.ctype(f.Type), cname(cls))
		e.line("%s->obj.cls = &cls_%s;\n", g, mangle(cls.Full))
		e.line("((tyEnumBase*)%s)->ordinal = %d;\n", g, f.EnumOrd)
		e.line("((tyEnumBase*)%s)->name = ty_str_intern(%s);\n", g, e.cstr(ec.Name))
		if len(ec.Args) > 0 || ec.Body != nil {
			e.line("/* enum constant arguments are evaluated in the enum constructor */\n")
		}
	}
}
