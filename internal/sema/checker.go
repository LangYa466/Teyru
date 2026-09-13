// Package sema performs name resolution, type checking and desugaring of
// Teyru programs, producing a closed-world program model for code generation.
package sema

import (
	"sort"
	"strings"

	"github.com/LangYa466/Teyru/internal/ast"
	"github.com/LangYa466/Teyru/internal/source"
)

// Program is the checked, whole-program model.
type Program struct {
	Files      []*ast.File
	Classes    []*ast.Class // in dependency-friendly order (supers first)
	Main       *ast.Method
	Selectors  int // number of interface method selectors
	Builtins   *Builtins
	StringLits map[string]int
	c          *Checker
}

// Builtins gives quick access to prelude classes.
type Builtins struct {
	Object, String, Enum, Record, Throwable, Iterable, Iterator, StringBuilder *ast.Class
	AutoCloseable, Cloneable, Comparable                                       *ast.Class
	IllArg, IllState, NoSuchElem, Unsup, ArrayStore                            *ast.Class
	NPE, AIOOBE, Arith, CCE, NegArr, Assertion                                 *ast.Class
	Boxes                                                                      map[ast.PrimKind]*ast.Class
	Unbox                                                                      map[*ast.Class]ast.PrimKind
}

// Checker holds global analysis state.
type Checker struct {
	diags      *source.Diagnostics
	files      []*ast.File
	classes    []*ast.Class
	global     map[string]*ast.Class // simple and full names
	b          *Builtins
	nextID     int
	varID      int
	tvID       int
	anonN      map[*ast.Class]int
	selector   int
	todo       []func()
	Props      map[ast.Expr]ast.Expr
	Direct     map[ast.Expr]bool // varargs calls that pass the array itself
	program    *Program
	objType    *ast.ClassType
	strType    *ast.ClassType
	arrCls     *ast.Class
	extensions map[*ast.Class][]*ast.Class
}

// Check analyses the prelude plus user files.
func Check(files []*ast.File, diags *source.Diagnostics) *Program {
	c := &Checker{diags: diags, files: files, global: map[string]*ast.Class{}, anonN: map[*ast.Class]int{}, Props: map[ast.Expr]ast.Expr{}, Direct: map[ast.Expr]bool{}}
	defer func() { c.program.c = c }()
	c.program = &Program{Files: files, StringLits: map[string]int{}}
	for _, f := range files {
		for _, cd := range f.Types {
			c.declareClass(f, cd, nil)
		}
	}
	c.initBuiltins()
	if diags.HasErrors() {
		return c.program
	}
	for _, cl := range append([]*ast.Class(nil), c.classes...) {
		c.resolveHeader(cl)
	}
	for _, cl := range append([]*ast.Class(nil), c.classes...) {
		c.resolveMembers(cl)
	}
	env0 := &typeEnv{tvars: map[string]*ast.TypeVar{}}
	for _, f := range files {
		if f.StaticMethods == nil {
			f.StaticMethods = map[string][]*ast.Method{}
		}
		c.resolveImports(env0, f)
	}
	c.applyLombokToProgram()
	for _, cl := range append([]*ast.Class(nil), c.classes...) {
		c.layout(cl)
	}
	for _, cl := range append([]*ast.Class(nil), c.classes...) {
		c.checkBodies(cl)
	}
	for len(c.todo) > 0 {
		t := c.todo[0]
		c.todo = c.todo[1:]
		t()
	}
	// the array class is created on demand, by whichever code path asks for it
	// first. Force it here, before the list is snapshotted: the emitter writes
	// `&cls_teyru_Array` for every array cast, instanceof and array literal, so
	// a program that only creates one -- `(Object) new int[1]` -- used to emit a
	// reference to a class that was never declared (the C backend then failed
	// with "use of undeclared identifier 'cls_teyru_Array'").
	c.arrayClass()
	for _, cl := range c.classes {
		c.layout(cl)
	}
	c.program.Classes = c.orderedClasses()
	c.program.Selectors = c.selector
	c.program.Builtins = c.b
	c.findMain()
	return c.program
}

// resolveImports fills in static imports and star imports for a file.
func (c *Checker) resolveImports(env *typeEnv, f *ast.File) {
	for _, imp := range f.Imports {
		if !imp.Static {
			if imp.Star {
				f.StarImports = append(f.StarImports, imp.Path)
			}
			continue
		}
		if imp.Star {
			if cl := c.lookupClassName(env, imp.Path); cl != nil {
				for name, ms := range cl.Methods {
					for _, m := range ms {
						if m.IsStatic() {
							f.StaticMethods[name] = append(f.StaticMethods[name], m)
						}
					}
				}
				for _, fl := range cl.Fields {
					if fl.Mods.Has(ast.ModStatic) {
						f.StaticImports = append(f.StaticImports, fl)
					}
				}
			}
			continue
		}
		i := strings.LastIndexByte(imp.Path, '.')
		if i < 0 {
			continue
		}
		owner, member := imp.Path[:i], imp.Path[i+1:]
		cl := c.lookupClassName(env, owner)
		if cl == nil {
			c.errf(imp.Pos, "TY-TYP-0086", "cannot resolve static import %s", imp.Path)
			continue
		}
		if fl := cl.FieldMap[member]; fl != nil && fl.Mods.Has(ast.ModStatic) {
			f.StaticImports = append(f.StaticImports, fl)
		}
		for _, m := range cl.Methods[member] {
			if m.IsStatic() {
				f.StaticMethods[member] = append(f.StaticMethods[member], m)
			}
		}
	}
}

func (c *Checker) errf(pos source.Pos, code, format string, args ...any) {
	c.diags.Errorf(pos, code, format, args...)
}

func (c *Checker) orderedClasses() []*ast.Class {
	seen := map[*ast.Class]bool{}
	var out []*ast.Class
	var visit func(cl *ast.Class)
	visit = func(cl *ast.Class) {
		if seen[cl] {
			return
		}
		seen[cl] = true
		if cl.Super != nil {
			visit(cl.Super.Class)
		}
		out = append(out, cl)
	}
	for _, cl := range c.classes {
		visit(cl)
	}
	for i, cl := range out {
		cl.ID = i
	}
	return out
}

func (c *Checker) findMain() {
	var cands []*ast.Method
	for _, f := range c.files {
		if f.Src != nil && strings.HasPrefix(f.Src.Path, "<lib>") {
			continue
		}
		var visit func(cl *ast.Class)
		visit = func(cl *ast.Class) {
			for _, m := range cl.Methods["main"] {
				if m.Result == ast.TVoid && (len(m.Params) == 0 || len(m.Params) == 1 && isStringArray(m.Params[0])) {
					cands = append(cands, m)
				}
			}
			for _, n := range sortedNested(cl) {
				visit(n)
			}
		}
		for _, cd := range f.Types {
			if cd.Sym != nil {
				visit(cd.Sym)
			}
		}
	}
	// prefer static main(String[]) then others
	sort.SliceStable(cands, func(i, j int) bool {
		return cands[i].IsStatic() && !cands[j].IsStatic()
	})
	if len(cands) > 0 {
		c.program.Main = cands[0]
		cands[0].Used = true
	}
}

func sortedNested(cl *ast.Class) []*ast.Class {
	var out []*ast.Class
	for _, n := range cl.Nested {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func isStringArray(t ast.Type) bool {
	a, ok := t.(*ast.ArrayType)
	if !ok {
		return false
	}
	ct, ok := a.Elem.(*ast.ClassType)
	return ok && ct.Class.Special == "String"
}

// ---------------------------------------------------------------- declaration

func (c *Checker) newClass(name, full string, kind ast.ClassKind) *ast.Class {
	cl := &ast.Class{
		Name: name, Full: full, Kind: kind,
		FieldMap: map[string]*ast.Field{}, Methods: map[string][]*ast.Method{}, Nested: map[string]*ast.Class{},
		CapFields: map[*ast.Var]*ast.Field{},
	}
	c.classes = append(c.classes, cl)
	return cl
}

func (c *Checker) declareClass(f *ast.File, cd *ast.ClassDecl, outer *ast.Class) *ast.Class {
	full := cd.Name
	if outer != nil {
		full = outer.Full + "$" + cd.Name
	} else if f.Package != "" {
		full = f.Package + "." + cd.Name
	}
	cl := c.newClass(cd.Name, full, cd.Kind)
	cl.Decl = cd
	cl.File = f
	cl.Mods = cd.Mods
	cl.Outer = outer
	cd.Sym = cl
	if f.Src != nil && strings.HasPrefix(f.Src.Path, "<lib>") {
		cl.Builtin = true
	}
	if cd.Kind == ast.KindInterface || cd.Kind == ast.KindEnum || cd.Kind == ast.KindRecord {
		cl.Mods |= ast.ModStatic
	}
	if outer != nil {
		if outer.IsInterface() {
			cl.Mods |= ast.ModStatic
		}
		cl.Inner = !cl.Mods.Has(ast.ModStatic)
		if dup := outer.Nested[cd.Name]; dup != nil {
			c.errf(cd.Pos, "TY-TYP-0001", "duplicate nested type %s", cd.Name)
		}
		outer.Nested[cd.Name] = cl
	} else {
		if prev := c.global[cd.Name]; prev != nil {
			if prev.Builtin && !cl.Builtin {
				// user type shadows prelude type of the same simple name
			} else {
				c.errf(cd.Pos, "TY-TYP-0001", "duplicate type %s (also declared at %s)", cd.Name, prev.Decl.Pos)
			}
		}
		if prev := c.global[cd.Name]; prev == nil || prev.Builtin {
			c.global[cd.Name] = cl
		}
		c.global[full] = cl
	}
	for _, m := range cd.Members {
		if n, ok := m.(*ast.ClassDecl); ok {
			c.declareClass(f, n, cl)
		}
	}
	return cl
}

func (c *Checker) initBuiltins() {
	get := func(n string) *ast.Class {
		cl := c.global[n]
		if cl == nil || !cl.Builtin {
			for _, x := range c.classes {
				if x.Builtin && x.Name == n && x.Outer == nil {
					return x
				}
			}
			c.errf(source.Pos{}, "TY-INT-0001", "prelude is missing class %s", n)
			return c.newClass(n, n, ast.KindClass)
		}
		return cl
	}
	b := &Builtins{
		Object: get("Object"), String: get("String"), Enum: get("Enum"), Record: get("Record"),
		Throwable: get("Throwable"), Iterable: get("Iterable"), Iterator: get("Iterator"),
		StringBuilder: get("StringBuilder"), AutoCloseable: get("AutoCloseable"),
		Cloneable: get("Cloneable"), Comparable: get("Comparable"),
		IllArg: get("IllegalArgumentException"), IllState: get("IllegalStateException"),
		NoSuchElem: get("NoSuchElementException"), Unsup: get("UnsupportedOperationException"),
		ArrayStore: get("ArrayStoreException"),
		NPE:        get("NullPointerException"), AIOOBE: get("ArrayIndexOutOfBoundsException"),
		Arith: get("ArithmeticException"), CCE: get("ClassCastException"),
		NegArr: get("NegativeArraySizeException"), Assertion: get("AssertionError"),
		Boxes: map[ast.PrimKind]*ast.Class{}, Unbox: map[*ast.Class]ast.PrimKind{},
	}
	b.Object.Special = "Object"
	b.String.Special = "String"
	b.StringBuilder.Special = "sb"
	for k, n := range map[ast.PrimKind]string{ast.Boolean: "Boolean", ast.Byte: "Byte", ast.Short: "Short", ast.Char: "Character", ast.Int: "Integer", ast.Long: "Long", ast.Float: "Float", ast.Double: "Double"} {
		cl := get(n)
		b.Boxes[k] = cl
		b.Unbox[cl] = k
		cl.Special = "box"
	}
	c.b = b
	c.objType = &ast.ClassType{Class: b.Object}
	c.strType = &ast.ClassType{Class: b.String}
}

// ---------------------------------------------------------------- type resolution

// typeEnv resolves simple type names in a lexical context.
type typeEnv struct {
	cls    *ast.Class
	tvars  map[string]*ast.TypeVar
	parent *typeEnv
	file   *ast.File
	locals map[string]*ast.Class
}

func (e *typeEnv) lookupTV(name string) *ast.TypeVar {
	for x := e; x != nil; x = x.parent {
		if tv := x.tvars[name]; tv != nil {
			return tv
		}
	}
	return nil
}

func (c *Checker) classEnv(cl *ast.Class) *typeEnv {
	var parent *typeEnv
	if cl.Outer != nil {
		parent = c.classEnv(cl.Outer)
	}
	env := &typeEnv{cls: cl, tvars: map[string]*ast.TypeVar{}, parent: parent, file: cl.File}
	if cl.LocalOwner != nil && cl.LocalOwner.Decl != nil {
		// method type params visible to local classes
		for _, tv := range cl.LocalOwner.TypeParams {
			env.tvars[tv.Name] = tv
		}
	}
	for _, tv := range cl.TypeParams {
		env.tvars[tv.Name] = tv
	}
	return env
}

func (c *Checker) lookupClassName(env *typeEnv, name string) *ast.Class {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		// qualified: outer class chain or package path
		if cl := c.global[name]; cl != nil {
			return cl
		}
		head := c.lookupClassName(env, name[:i])
		rest := name[i+1:]
		if head == nil {
			// strip package segments (java.util.List, teyru.util.List)
			parts := strings.Split(name, ".")
			for k := 1; k < len(parts); k++ {
				if cl := c.global[parts[k]]; cl != nil && (k == len(parts)-1) {
					return cl
				}
				if cl := c.global[parts[k]]; cl != nil {
					return c.nestedPath(cl, parts[k+1:])
				}
			}
			return nil
		}
		return c.nestedPath(head, strings.Split(rest, "."))
	}
	for e := env; e != nil; e = e.parent {
		if e.locals != nil {
			if cl := e.locals[name]; cl != nil {
				return cl
			}
		}
		if e.cls != nil {
			for k := e.cls; k != nil; {
				if k.Name == name && k.Outer == nil && k.LocalOwner == nil {
					return k
				}
				if n := c.findNested(k, name, map[*ast.Class]bool{}); n != nil {
					return n
				}
				break
			}
			if e.cls.Name == name {
				return e.cls
			}
		}
	}
	return c.global[name]
}

func (c *Checker) nestedPath(cl *ast.Class, parts []string) *ast.Class {
	for _, p := range parts {
		if cl == nil {
			return nil
		}
		cl = c.findNested(cl, p, map[*ast.Class]bool{})
	}
	return cl
}

// findNested finds a member type in cl or its supertypes.
func (c *Checker) findNested(cl *ast.Class, name string, seen map[*ast.Class]bool) *ast.Class {
	if cl == nil || seen[cl] {
		return nil
	}
	seen[cl] = true
	if n := cl.Nested[name]; n != nil {
		return n
	}
	if cl.Decl != nil && !cl.Resolved {
		// headers may not be resolved yet; look at syntax
		for _, e := range append(append([]*ast.TypeExpr{}, cl.Decl.Extends...), cl.Decl.Implements...) {
			env := c.classEnv(cl)
			env.cls = cl.Outer
			if cl.Outer == nil {
				env.cls = nil
			}
			if s := c.lookupClassName(env, e.Name); s != nil && s != cl {
				if n := c.findNested(s, name, seen); n != nil {
					return n
				}
			}
		}
		return nil
	}
	if cl.Super != nil {
		if n := c.findNested(cl.Super.Class, name, seen); n != nil {
			return n
		}
	}
	for _, i := range cl.Ifaces {
		if n := c.findNested(i.Class, name, seen); n != nil {
			return n
		}
	}
	return nil
}

func (c *Checker) resolveType(env *typeEnv, te *ast.TypeExpr) ast.Type {
	if te == nil {
		return ast.ErrorType{}
	}
	t := c.resolveTypeNoDims(env, te)
	for i := 0; i < te.Dims; i++ {
		t = &ast.ArrayType{Elem: t}
	}
	te.Resolved = t
	return t
}

func (c *Checker) resolveTypeNoDims(env *typeEnv, te *ast.TypeExpr) ast.Type {
	if te.Wildcard != 0 {
		w := &ast.WildcardType{}
		if te.Bound != nil {
			w.Bound = c.resolveType(env, te.Bound)
			w.Super = te.Wildcard == 3
		}
		return w
	}
	if p := ast.PrimByName[te.Name]; p != nil {
		return p
	}
	if tv := env.lookupTV(te.Name); tv != nil {
		if len(te.Args) > 0 {
			c.errf(te.Pos, "TY-TYP-0002", "type variable %s cannot have type arguments", te.Name)
		}
		return &ast.TypeVarType{Var: tv}
	}
	cl := c.lookupClassName(env, te.Name)
	if cl == nil {
		c.errf(te.Pos, "TY-TYP-0003", "cannot find type %s", te.Name)
		return ast.ErrorType{}
	}
	ct := &ast.ClassType{Class: cl}
	if te.Args != nil && len(te.Args) == 0 {
		// diamond: filled by inference at the use site
		ct.Args = nil
		return &diamondType{ct}
	}
	if len(te.Args) > 0 {
		c.ensureTypeParams(cl)
		if len(te.Args) != len(cl.TypeParams) {
			c.errf(te.Pos, "TY-TYP-0004", "type %s expects %d type arguments, found %d", cl.Name, len(cl.TypeParams), len(te.Args))
		}
		for _, a := range te.Args {
			at := c.resolveType(env, a)
			if p, ok := at.(*ast.PrimType); ok {
				c.errf(a.Pos, "TY-TYP-0005", "primitive type %s cannot be a type argument; use its box type", p)
				at = &ast.ClassType{Class: c.b.Boxes[p.Kind]}
			}
			ct.Args = append(ct.Args, at)
		}
	} else {
		c.ensureTypeParams(cl)
		if len(cl.TypeParams) > 0 {
			// raw type: use bounds
			for _, tv := range cl.TypeParams {
				ct.Args = append(ct.Args, c.erasure(tv.Bound))
			}
		}
	}
	return ct
}

// diamondType marks `new C<>()` before inference.
type diamondType struct{ *ast.ClassType }

func (c *Checker) ensureTypeParams(cl *ast.Class) {
	if cl.TypeParams != nil || cl.Decl == nil || len(cl.Decl.TypeParams) == 0 {
		return
	}
	for _, tp := range cl.Decl.TypeParams {
		tv := &ast.TypeVar{Name: tp.Name, ID: c.tvID}
		c.tvID++
		tp.Sym = tv
		cl.TypeParams = append(cl.TypeParams, tv)
	}
	env := c.classEnv(cl)
	for _, tp := range cl.Decl.TypeParams {
		if len(tp.Bounds) > 0 {
			tp.Sym.Bound = c.resolveType(env, tp.Bounds[0])
		} else {
			tp.Sym.Bound = c.objType
		}
	}
}

func (c *Checker) resolveHeader(cl *ast.Class) {
	if cl.Resolved {
		return
	}
	cl.Resolved = true
	cd := cl.Decl
	c.ensureTypeParams(cl)
	if cd == nil {
		return
	}
	env := c.classEnv(cl)
	switch cd.Kind {
	case ast.KindClass:
		if len(cd.Extends) > 0 {
			if st, ok := c.resolveType(env, cd.Extends[0]).(*ast.ClassType); ok {
				c.resolveHeader(st.Class)
				if st.Class.IsInterface() {
					c.errf(cd.Extends[0].Pos, "TY-TYP-0006", "class cannot extend interface %s", st.Class.Name)
				} else if st.Class.Mods.Has(ast.ModFinal) && !cl.Builtin {
					c.errf(cd.Extends[0].Pos, "TY-TYP-0007", "cannot extend final class %s", st.Class.Name)
				} else if c.isSubclass(st.Class, cl) {
					c.errf(cd.Extends[0].Pos, "TY-TYP-0008", "cyclic inheritance involving %s", cl.Name)
				} else {
					cl.Super = st
				}
			}
		}
	case ast.KindInterface, ast.KindAnnotation:
		for _, e := range cd.Extends {
			if st, ok := c.resolveType(env, e).(*ast.ClassType); ok {
				c.resolveHeader(st.Class)
				cl.Ifaces = append(cl.Ifaces, st)
			}
		}
	case ast.KindEnum:
		cl.Super = &ast.ClassType{Class: c.b.Enum, Args: []ast.Type{&ast.ClassType{Class: cl}}}
		cl.Mods |= ast.ModFinal
	case ast.KindRecord:
		cl.Super = &ast.ClassType{Class: c.b.Record}
		cl.Mods |= ast.ModFinal
	}
	if cl.Super == nil && cl != c.b.Object && !cl.IsInterface() {
		cl.Super = c.objType
	}
	for _, e := range cd.Implements {
		if st, ok := c.resolveType(env, e).(*ast.ClassType); ok {
			c.resolveHeader(st.Class)
			if !st.Class.IsInterface() {
				c.errf(e.Pos, "TY-TYP-0009", "%s is not an interface", st.Class.Name)
				continue
			}
			cl.Ifaces = append(cl.Ifaces, st)
		}
	}
	if cl.Super != nil {
		cl.Super.Class.Subclasses = append(cl.Super.Class.Subclasses, cl)
	}
}

// arrayClass returns the synthetic Object subclass that represents arrays.
func (c *Checker) arrayClass() *ast.Class {
	if c.arrCls == nil {
		c.arrCls = c.newClass("Array", "teyru.Array", ast.KindClass)
		c.arrCls.Builtin = true
		c.arrCls.Resolved = true
		c.arrCls.Special = "array"
		c.arrCls.Super = c.objType
		c.arrCls.Mods = ast.ModFinal
		m := &ast.Method{Name: "toString", Owner: c.arrCls, Mods: ast.ModPublic, Result: c.strType, SynthKind: "array-tostring"}
		c.addMethod(c.arrCls, m)
		m2 := &ast.Method{Name: "hashCode", Owner: c.arrCls, Mods: ast.ModPublic, Result: ast.TInt, SynthKind: "array-hashcode"}
		c.addMethod(c.arrCls, m2)
		m3 := &ast.Method{Name: "equals", Owner: c.arrCls, Mods: ast.ModPublic, Result: ast.TBoolean, Params: []ast.Type{c.objType}, ParamNames: []string{"o"}, SynthKind: "array-equals"}
		c.addMethod(c.arrCls, m3)
		m4 := &ast.Method{Name: "clone", Owner: c.arrCls, Mods: ast.ModPublic, Result: c.objType, SynthKind: "array-clone"}
		c.addMethod(c.arrCls, m4)
		c.layout(c.arrCls)
	}
	return c.arrCls
}

func (c *Checker) isSubclass(a, b *ast.Class) bool {
	for x := a; x != nil; {
		if x == b {
			return true
		}
		if x.Super == nil {
			return false
		}
		x = x.Super.Class
	}
	return false
}
