package sema

import (
	"sort"

	"github.com/teyru-lang/Teyru/internal/ast"
	"github.com/teyru-lang/Teyru/internal/source"
)

// Gson's object binding, resolved by the compiler.
//
// Gson binds an object to JSON by reflection: `gson.fromJson(s, Pet.class)`
// asks the runtime to look at Pet's fields. Teyru has no reflection, so the
// compiler does the looking. When a program binds a class, the pass generates a
// reader and a writer for it -- ordinary Teyru methods that walk the fields --
// and rewrites the call to use them. The call site keeps Gson's spelling; what
// changes is that the result already has the right type, so the cast Gson's
// generic signature would have needed is not written.
//
// A field whose type has no mapping is reported here, at the line that binds
// the class, rather than as an IllegalArgumentException from inside a reflective
// adapter at run time.

// jsonReaderName and jsonWriterName are the generated methods. The spelling is
// one no user method is likely to pick, and a class that declares one itself is
// refused rather than silently shadowed.
const (
	jsonReaderName = "__teyruJsonRead"
	jsonWriterName = "__teyruJsonWrite"
)

// jsonAdapterPair is one class's generated binding.
type jsonAdapterPair struct {
	reader *ast.Method
	writer *ast.Method
}

// tryJsonCall rewrites a Gson binding call whose class the compiler can see.
// It answers whether it handled the call.
//
// The receiver has already been checked, so its type is known: only a call on a
// Gson is rewritten, and a program that declares its own fromJson keeps it.
func (ctx *methodCtx) tryJsonCall(v *ast.Call, rt ast.Type, want ast.Type) bool {
	c := ctx.c
	if v.Recv == nil || rt == nil {
		return false
	}
	ct, ok := c.erasure(rt).(*ast.ClassType)
	if !ok || ct.Class == nil {
		return false
	}
	if ct.Class.Special != gsonClass && ct.Class.Name != "Gson" {
		return false
	}
	switch {
	case v.Name == "fromJson" && len(v.Args) == 2:
		lit, ok := v.Args[1].(*ast.ClassLit)
		if !ok {
			return false
		}
		// The class literal has not been checked yet -- arguments are checked
		// after the receiver -- so its type is resolved here. The node is then
		// dropped with the rewrite, which is why it must not be left for the
		// ordinary path to type twice.
		target, ok := c.resolveType(ctx.env, lit.Type).(*ast.ClassType)
		if !ok || target.Class == nil {
			return false
		}
		pair := c.jsonAdapterFor(target.Class, lit.Pos)
		if pair == nil || pair.reader == nil {
			return false
		}
		// gson.fromJson(s, Pet.class)  ->  Pet.__teyruJsonRead(JsonParser.parseString(s))
		ctx.checkExpr(v.Args[0], c.strType)
		parsed := callNamed(id("JsonParser"), "parseString", v.Args[0])
		nc := callNamed(id(target.Class.Full), pair.reader.Name, parsed)
		ctx.adoptCall(v, nc, want)
		return true
	case v.Name == "toJson" && len(v.Args) == 1:
		// Gson's toJson(Object) takes anything. A concrete class the compiler
		// can map is bound here, at the call; a primitive, a box, a String, or a
		// reference whose static type is not a class the compiler can name all
		// become the tree Gson would have built, so what is left to write is
		// the one method the library has.
		ctx.checkExpr(v.Args[0], nil)
		at := v.Args[0].GetType()
		if at == nil {
			return false
		}
		if k, ok := c.jsonPrimKind(at); ok {
			if k == ast.Void {
				return false
			}
			nc := callNamed(v.Recv, "toJson", newObjNamed("JsonPrimitive", v.Args[0]))
			ctx.adoptCall(v, nc, want)
			return true
		}
		ac, ok := c.erasure(at).(*ast.ClassType)
		if !ok || ac.Class == nil {
			return false
		}
		// The rewrite's own output is a JsonElement, and leaving this call alone
		// when it already is one is what keeps the rewrite from matching itself
		// forever: the library's toJson(JsonElement) is the method the whole
		// rewrite exists to reach.
		if je := c.global["JsonElement"]; je != nil && c.isSubtype(at, &ast.ClassType{Class: je}) {
			return false
		}
		if ac.Class.Special == "String" {
			nc := callNamed(v.Recv, "toJson", newObjNamed("JsonPrimitive", v.Args[0]))
			ctx.adoptCall(v, nc, want)
			return true
		}
		if ac.Class.Builtin || ac.Class == c.b.Object {
			// the one case reflection has no compile-time equivalent for: the
			// runtime table answers by the object's own class
			c.jsonBindProgramLater()
			nc := callNamed(v.Recv, "toJson", callNamed(id("JsonBinding"), "writeTo", v.Args[0]))
			ctx.adoptCall(v, nc, want)
			return true
		}
		pair := c.jsonAdapterFor(ac.Class, v.Pos)
		if pair == nil || pair.writer == nil {
			return false
		}
		// gson.toJson(x)  ->  gson.toJson(Pet.__teyruJsonWrite(x))
		nc := callNamed(v.Recv, "toJson", callNamed(id(ac.Class.Full), pair.writer.Name, v.Args[0]))
		ctx.adoptCall(v, nc, want)
		return true
	}
	return false
}

// adoptCall checks the replacement call and moves its resolution onto the
// original node, so the tree the emitter sees is the rewritten one.
func (ctx *methodCtx) adoptCall(v, nc *ast.Call, want ast.Type) {
	ctx.checkExpr(nc, want)
	v.Recv = nc.Recv
	v.Name = nc.Name
	v.Method = nc.Method
	v.Static = nc.Static
	v.Args = nc.Args
	v.SetType(nc.GetType())
}

// gsonClass is the prelude class a binding call is made on. It is matched by
// name rather than by identity because the library may be spelled either way.
const gsonClass = "teyru.Gson"

// jsonAdapterFor answers with the class's generated reader and writer,
// generating them on first use.
//
// Generating during checking rather than in a pass is what lets the binding be
// driven by the call sites: a program that never binds a class never pays for
// an adapter, and the fields walked are the ones the class has at that point --
// after Lombok has added whatever it adds.
func (c *Checker) jsonAdapterFor(cl *ast.Class, pos source.Pos) *jsonAdapterPair {
	if cl == nil {
		return nil
	}
	if c.jsonAdapters == nil {
		c.jsonAdapters = map[*ast.Class]*jsonAdapterPair{}
	}
	if p, ok := c.jsonAdapters[cl]; ok {
		return p
	}
	if cl.Decl == nil {
		return nil
	}
	if cl.Builtin {
		if !c.jsonSpeculative {
			c.errf(pos, "TY-TYP-0108", "%s is a prelude class; it has no generated JSON binding", cl.Name)
		}
		return nil
	}
	// The pair is recorded before the bodies are built, so that a field of the
	// class's own type -- a linked list node -- finds the adapter already there
	// instead of generating one forever.
	pair := &jsonAdapterPair{}
	c.jsonAdapters[cl] = pair

	if cl.Kind == ast.KindEnum {
		// An enum has no fields to walk: the generic binding below would write
		// `{}` and could not read a name back, where Gson writes the constant's
		// name and reads one by matching it.
		pair.reader = c.synthJsonEnumReader(cl)
		pair.writer = c.synthJsonEnumWriter(cl)
		c.addMethod(cl, pair.reader)
		c.checkSynthBody(cl, pair.reader)
		c.addMethod(cl, pair.writer)
		c.checkSynthBody(cl, pair.writer)
		c.synthJsonRegister(cl, pair.reader, pair.writer)
		return pair
	}

	// Gson allocates without calling a constructor, through Unsafe. Teyru has
	// no such thing, so a bound *class* needs a no-argument constructor to be
	// read: the reader has to build the object before it can fill the fields.
	// A record is the exception -- it is read through its canonical
	// constructor, which is the only way to make one -- and it is also the type
	// most worth binding, so it is handled below rather than refused.
	//
	// The writer needs no constructor, so a class that cannot be read can still
	// be written: only the reader is skipped, and the binding the runtime table
	// gets has a null one.
	fields := c.jsonFieldsOf(cl, pos)
	var reader *ast.Method
	if cl.Kind == ast.KindRecord || c.hasNoArgCtor(cl) {
		reader = c.synthJsonReader(cl, fields)
	} else if !c.jsonSpeculative {
		c.errf(pos, "TY-TYP-0112",
			"%s is bound from JSON but has no no-argument constructor; add one, or bind a class that has one", cl.Name)
	}
	// The writer is built only when the reader was: a field the reader could not
	// map is one the writer cannot map either, and each of them would report the
	// same field. A speculative binding is the exception -- a class with no
	// no-argument constructor cannot be read but can still be written, and it is
	// not reported either way.
	var writer *ast.Method
	if reader != nil || c.jsonSpeculative {
		writer = c.synthJsonWriter(cl, fields)
	}
	pair.reader, pair.writer = reader, writer
	if reader == nil && writer == nil {
		delete(c.jsonAdapters, cl)
		return nil
	}
	if reader != nil {
		c.addMethod(cl, reader)
		c.checkSynthBody(cl, reader)
	}
	if writer != nil {
		c.addMethod(cl, writer)
		c.checkSynthBody(cl, writer)
	}
	c.synthJsonRegister(cl, reader, writer)
	return pair
}

// checkSynthBody type-checks one generated body in the class's own context and
// marks it checked: checkBodies would otherwise check it a second time, and the
// second pass leaves every identifier pointing at the variable the first pass
// declared while declaring a fresh one for the declaration -- two different
// symbols for one local, which the emitter then names differently at the
// declaration and at every use.
func (c *Checker) checkSynthBody(cl *ast.Class, m *ast.Method) {
	ctx := c.newCtx(cl, m)
	ctx.checkBlock(m.Body, false)
	m.Checked = true
}

// synthJsonRegister adds the method that puts this binding in the runtime
// table, and the static field whose initializer calls it.
//
// The table is what a binding falls back on when the type was not known where
// the call was written -- `toJson(someObject)` with an Object-typed reference,
// or a field declared as Object. That is the one case Gson's reflection has no
// compile-time equivalent for, and the answer is the same one Gson gives: the
// object's own class decides.
//
// The call is registered from the bound class's own static initializer rather
// than from the container's, so the two features stay independent and a program
// that uses one does not depend on the other.
func (c *Checker) synthJsonRegister(cl *ast.Class, reader, writer *ast.Method) {
	name := "__teyruJsonRegister"
	if _, dup := cl.Methods[name]; dup {
		return
	}
	// The result is boolean because the static field that calls it is: the field
	// is what puts the call into the class's initializer, and a field's
	// initializer has to have the field's type.
	m := &ast.Method{Name: name, Owner: cl, Mods: ast.ModPublic | ast.ModStatic,
		Result: ast.TBoolean, Pos: pos(), SynthKind: "json-register"}
	m.Decl = &ast.MethodDecl{Pos: pos(), Name: name, Mods: m.Mods}
	// A class that cannot be read -- one with no no-argument constructor -- is
	// still worth writing, so one side may be missing. The entry keeps the null,
	// and a call that needs it says there is no binding rather than crashing.
	var readArg ast.Expr = nullLit()
	if reader != nil {
		readArg = &ast.Lambda{
			ExprBase: ast.ExprBase{Pos: pos()},
			Params:   []*ast.Param{{Pos: pos(), Name: "e"}},
			Body:     callNamed(id(cl.Full), reader.Name, id("e")),
		}
	}
	var writeArg ast.Expr = nullLit()
	if writer != nil {
		writeArg = &ast.Lambda{
			ExprBase: ast.ExprBase{Pos: pos()},
			Params:   []*ast.Param{{Pos: pos(), Name: "v"}},
			Body:     callNamed(id(cl.Full), writer.Name, castTo(id("v"), cl)),
		}
	}
	m.Body = blockOf(
		exprStmtOf(callNamed(id("JsonBinding"), "bind", strLit(cl.Full), readArg, writeArg)),
		returnOf(boolLit(true)))
	c.addMethod(cl, m)
	c.checkSynthBody(cl, m)

	// the static field is what makes the call run: a class's <clinit> is filled
	// from its static fields' initializers, and the entry sequence initializes
	// every class the program declares
	ref := callNamed(nil, name)
	ref.Method = m
	ref.Static = true
	ref.SetType(ast.TBoolean)
	f := &ast.Field{Name: "__teyruJsonBound", Type: ast.TBoolean,
		Mods: ast.ModPrivate | ast.ModStatic | ast.ModFinal, Pos: pos(), Storage: true, Owner: cl}
	f.InitExpr = ref
	c.addSynthField(cl, f)
	if cl.ClInit == nil {
		cl.ClInit = &ast.Method{Name: "<clinit>", Owner: cl, Mods: ast.ModStatic,
			Result: ast.TVoid, Pos: pos(), SynthKind: "clinit"}
	}
}

// jsonBindProgram generates a binding for every class the program declares, so
// that a binding call the compiler cannot see through still finds one.
//
// Gson binds by reflection at the call: `toJson(someObject)` writes whatever
// the object turns out to be, and an Object-typed reference can carry any class
// in the program. There is no type at that call site to generate for, so every
// class is bound and the runtime table's getClass() lookup picks -- the same
// answer reflection gives, arrived at earlier.
//
// What is generated here is speculative, because the call site did not name the
// class: one that cannot be bound -- an interface, a class with no
// no-argument constructor to read through, a field with no mapping -- is left
// out rather than reported, and a program that asks for it at run time is told
// there is no binding, which is what it already would have been told.
func (c *Checker) jsonBindProgram() {
	if c.jsonProgramBound {
		return
	}
	c.jsonProgramBound = true
	c.jsonProgramQueued = false
	for _, cl := range append([]*ast.Class(nil), c.classes...) {
		if !c.jsonBindCandidate(cl) {
			continue
		}
		c.jsonSpeculative = true
		c.jsonAdapterFor(cl, pos())
		c.jsonSpeculative = false
	}
}

// jsonBindProgramLater schedules the program-wide binding for after the bodies
// have been checked.
//
// A class the program names at a binding call of its own has to be bound by
// that call site, with its own diagnostics, before this sweep can decide it
// cannot be bound and quietly skip it -- so the sweep waits, and by then every
// class it should not touch is already in the table.
func (c *Checker) jsonBindProgramLater() {
	if c.jsonProgramBound || c.jsonProgramQueued {
		return
	}
	c.jsonProgramQueued = true
	c.todo = append(c.todo, c.jsonBindProgram)
}

// jsonBindCandidate reports whether the program-wide binding should try a
// class at all. What is left out here could not be generated: an interface has
// no fields to walk, an abstract or inner class cannot be built with `new Cl()`
// from a static method, and a generic class's fields are type variables, which
// have no mapping of their own.
func (c *Checker) jsonBindCandidate(cl *ast.Class) bool {
	if cl == nil || cl.Decl == nil || cl.Builtin || cl.Anon || cl.Inner || cl.LocalOwner != nil {
		return false
	}
	if cl.IsInterface() || cl.Mods.Has(ast.ModAbstract) || len(cl.TypeParams) != 0 {
		return false
	}
	_, bound := c.jsonAdapters[cl]
	return !bound
}

// hasNoArgCtor reports whether a class can be built with no arguments. An
// absent constructor list means the default one, which counts.
func (c *Checker) hasNoArgCtor(cl *ast.Class) bool {
	if len(cl.Ctors) == 0 {
		return true
	}
	for _, ctor := range cl.Ctors {
		if len(ctor.Params) == 0 {
			return true
		}
	}
	return false
}

// jsonField is one field of a bound class: the name it has in the JSON and the
// field it maps to.
type jsonField struct {
	field *ast.Field
	name  string
	alts  []string
}

// jsonFieldsOf lists the fields a binding walks. Static fields, synthetic ones
// and properties backed by an accessor are left out, as Gson leaves out
// statics; everything else is bound, which is Gson's default too.
func (c *Checker) jsonFieldsOf(cl *ast.Class, pos source.Pos) []jsonField {
	var out []jsonField
	seen := map[string]bool{}
	for _, f := range cl.Fields {
		if f == nil || f.Mods.Has(ast.ModStatic) || f.Storage == false {
			continue
		}
		if f.Name == "" || f.Name == "this" {
			continue
		}
		jf := jsonField{field: f, name: f.Name}
		if a := c.fieldAnno(cl, f, "SerializedName"); a != nil {
			if n := annoText(a, "value"); n != "" {
				jf.name = n
			}
			jf.alts = annoStringList(a, "alternate")
		}
		if seen[jf.name] {
			if !c.jsonSpeculative {
				c.errf(pos, "TY-TYP-0109", "two fields of %s both map to the JSON name %s", cl.Name, jf.name)
			}
			continue
		}
		seen[jf.name] = true
		out = append(out, jf)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].field.Index < out[j].field.Index })
	return out
}

// fieldAnno reads one annotation written on a field declaration.
func (c *Checker) fieldAnno(cl *ast.Class, f *ast.Field, name string) *ast.Annotation {
	return hasAnno(c.fieldAnnos(cl, f), name)
}

// synthJsonReader builds `static T __teyruJsonRead(JsonElement e)`.
func (c *Checker) synthJsonReader(cl *ast.Class, fields []jsonField) *ast.Method {
	m := &ast.Method{Name: jsonReaderName, Owner: cl, Mods: ast.ModPublic | ast.ModStatic,
		Result: &ast.ClassType{Class: cl}, Pos: pos(), SynthKind: "json-reader"}
	pe := &ast.Param{Pos: pos(), Name: "e", Type: &ast.TypeExpr{Pos: pos(), Name: "JsonElement"}}
	m.Decl = &ast.MethodDecl{Pos: pos(), Name: m.Name, Mods: m.Mods, Params: []*ast.Param{pe}}
	m.Params = []ast.Type{c.classType("JsonElement")}
	m.ParamNames = []string{"e"}

	var stmts []ast.Stmt
	// A null or JSON null reads as a null reference, which is what Gson's
	// adapter answers for a null document.
	stmts = append(stmts, ifOf(
		orOf(isNull(id("e")), callNamed(id("e"), "isJsonNull")),
		blockOf(returnOf(&ast.Literal{ExprBase: ast.ExprBase{Pos: pos(), T: ast.NullType{}}, Kind: ast.LitNull})), nil))
	// The document has to be an object: a binding reads a class's fields by
	// name, and an array or a number has none.
	stmts = append(stmts, ifOf(
		notOf(callNamed(id("e"), "isJsonObject")),
		blockOf(throwOf(newObjNamed("JsonParseException",
			strLit("expected a JSON object, found ")))), nil))

	stmts = append(stmts, &ast.LocalVar{
		Pos:  pos(),
		Type: &ast.TypeExpr{Pos: pos(), Name: "JsonObject"},
		Vars: []*ast.VarDeclarator{{Pos: pos(), Name: "o", Init: callNamed(id("e"), "getAsJsonObject")}},
	})
	if cl.Kind == ast.KindRecord {
		// A record has no no-argument constructor and no mutable fields: the
		// only way to make one is its canonical constructor, so the components
		// are read into the argument list rather than assigned afterwards. An
		// absent member takes the component's zero value, since a record has no
		// field initializer to fall back on.
		var args []ast.Expr
		for _, jf := range fields {
			e := c.jsonReadValue(jf)
			if e == nil {
				return nil
			}
			args = append(args, absentOr(jf, e, zeroOfType(jf.field.Type)))
		}
		stmts = append(stmts, returnOf(newObj(cl, args...)))
		m.Body = blockOf(stmts...)
		return m
	}
	stmts = append(stmts, &ast.LocalVar{
		Pos:  pos(),
		Type: &ast.TypeExpr{Pos: pos(), Name: cl.Full, Resolved: &ast.ClassType{Class: cl}},
		Vars: []*ast.VarDeclarator{{Pos: pos(), Name: "v", Init: newObj(cl)}},
	})
	for _, jf := range fields {
		if e := c.jsonReadExpr(jf, cl); e != nil {
			stmts = append(stmts, exprStmtOf(assignTo(sel(id("v"), jf.field.Name), e)))
		}
	}
	stmts = append(stmts, returnOf(id("v")))
	m.Body = blockOf(stmts...)
	return m
}

// synthJsonWriter builds `static JsonElement __teyruJsonWrite(T self)`.
func (c *Checker) synthJsonWriter(cl *ast.Class, fields []jsonField) *ast.Method {
	m := &ast.Method{Name: jsonWriterName, Owner: cl, Mods: ast.ModPublic | ast.ModStatic,
		Result: c.classType("JsonElement"), Pos: pos(), SynthKind: "json-writer"}
	ps := &ast.Param{Pos: pos(), Name: "self", Type: &ast.TypeExpr{Pos: pos(), Name: cl.Full, Resolved: &ast.ClassType{Class: cl}}}
	m.Decl = &ast.MethodDecl{Pos: pos(), Name: m.Name, Mods: m.Mods, Params: []*ast.Param{ps}}
	m.Params = []ast.Type{&ast.ClassType{Class: cl}}
	m.ParamNames = []string{"self"}

	var stmts []ast.Stmt
	stmts = append(stmts, &ast.LocalVar{
		Pos:  pos(),
		Type: &ast.TypeExpr{Pos: pos(), Name: "JsonObject"},
		Vars: []*ast.VarDeclarator{{Pos: pos(), Name: "o", Init: newObjName("JsonObject")}},
	})
	for _, jf := range fields {
		if st := c.jsonWriteStmt(jf); st != nil {
			stmts = append(stmts, st)
		} else {
			// A field the binding cannot write has to fail the writer rather
			// than be left out of the object: a member silently missing from
			// the JSON is worse than an error that says which field it is.
			return nil
		}
	}
	stmts = append(stmts, returnOf(id("o")))
	m.Body = blockOf(stmts...)
	return m
}

// synthJsonEnumReader builds `static Cl __teyruJsonRead(JsonElement e)` for an
// enum: Gson writes a constant as its name, and reads one back by matching the
// name against the constants.
func (c *Checker) synthJsonEnumReader(cl *ast.Class) *ast.Method {
	m := &ast.Method{Name: jsonReaderName, Owner: cl, Mods: ast.ModPublic | ast.ModStatic,
		Result: &ast.ClassType{Class: cl}, Pos: pos(), SynthKind: "json-reader"}
	pe := &ast.Param{Pos: pos(), Name: "e", Type: &ast.TypeExpr{Pos: pos(), Name: "JsonElement"}}
	m.Decl = &ast.MethodDecl{Pos: pos(), Name: m.Name, Mods: m.Mods, Params: []*ast.Param{pe}}
	m.Params = []ast.Type{c.classType("JsonElement")}
	m.ParamNames = []string{"e"}

	stmts := []ast.Stmt{
		ifOf(orOf(isNull(id("e")), callNamed(id("e"), "isJsonNull")),
			blockOf(returnOf(nullLit())), nil),
		&ast.LocalVar{Pos: pos(),
			Type: &ast.TypeExpr{Pos: pos(), Name: "String"},
			Vars: []*ast.VarDeclarator{{Pos: pos(), Name: "s", Init: callNamed(id("e"), "getAsString")}}},
	}
	stmts = append(stmts, enumLookupChain(cl, id("s"))...)
	stmts = append(stmts, throwOf(newObjNamed("JsonParseException",
		concatStr(strLit("no "+cl.Name+" constant named "), id("s")))))
	m.Body = blockOf(stmts...)
	return m
}

// synthJsonEnumWriter builds `static JsonElement __teyruJsonWrite(Cl self)`:
// the constant's name, which is the whole of what an enum has in JSON.
func (c *Checker) synthJsonEnumWriter(cl *ast.Class) *ast.Method {
	m := &ast.Method{Name: jsonWriterName, Owner: cl, Mods: ast.ModPublic | ast.ModStatic,
		Result: c.classType("JsonElement"), Pos: pos(), SynthKind: "json-writer"}
	ps := &ast.Param{Pos: pos(), Name: "self", Type: &ast.TypeExpr{Pos: pos(), Name: cl.Full, Resolved: &ast.ClassType{Class: cl}}}
	m.Decl = &ast.MethodDecl{Pos: pos(), Name: m.Name, Mods: m.Mods, Params: []*ast.Param{ps}}
	m.Params = []ast.Type{&ast.ClassType{Class: cl}}
	m.ParamNames = []string{"self"}
	m.Body = blockOf(
		ifOf(isNull(id("self")),
			blockOf(returnOf(sel(id("JsonNull"), "INSTANCE"))), nil),
		returnOf(newObjNamed("JsonPrimitive", callNamed(id("self"), "name"))))
	return m
}

// jsonPrimKind answers with the primitive a field's type reads as, following a
// box to the primitive inside it.
func (c *Checker) jsonPrimKind(t ast.Type) (ast.PrimKind, bool) {
	if p, ok := c.erasure(t).(*ast.PrimType); ok {
		return p.Kind, true
	}
	if ct, ok := c.erasure(t).(*ast.ClassType); ok && ct.Class != nil {
		if k, ok := c.b.Unbox[ct.Class]; ok {
			return k, true
		}
	}
	return ast.Void, false
}

// jsonReadValue is the expression that reads one member's value out of the
// object, without the guard for a member the document does not carry.
func (c *Checker) jsonReadValue(jf jsonField) ast.Expr {
	return c.jsonReadValue0(jf, true)
}

// jsonReadExpr is the expression that reads one field out of the object, or nil
// for a field whose type has no mapping.
func (c *Checker) jsonReadExpr(jf jsonField, owner *ast.Class) ast.Expr {
	return c.jsonReadValue0(jf, false)
}

func (c *Checker) jsonReadValue0(jf jsonField, bare bool) ast.Expr {
	// JsonObject answers with the element; the accessor belongs to the element,
	// which is Gson's shape too (JsonObject.getAsString does not exist there
	// either).
	get := func(method string) ast.Expr {
		return callNamed(callNamed(id("o"), "get", strLit(jf.name)), method)
	}
	if k, ok := c.jsonPrimKind(jf.field.Type); ok {
		var value ast.Expr
		switch k {
		case ast.Int:
			value = get("getAsInt")
		case ast.Long:
			value = get("getAsLong")
		case ast.Double:
			value = get("getAsDouble")
		case ast.Float:
			value = get("getAsFloat")
		case ast.Boolean:
			value = get("getAsBoolean")
		case ast.Short:
			value = castPrim(get("getAsInt"), "short")
		case ast.Byte:
			value = castPrim(get("getAsInt"), "byte")
		case ast.Char:
			// Gson's char adapter reads the member as text and takes its first
			// character, which is what a char was written as: reading it as an
			// int would reject this binding's own output, `"c":"z"`.
			value = get("getAsCharacter")
		default:
			c.jsonUnmapped(jf)
			return nil
		}
		return guardOrBare(jf, value, bare)
	}
	ct, ok := c.erasure(jf.field.Type).(*ast.ClassType)
	if !ok || ct.Class == nil {
		c.jsonUnmapped(jf)
		return nil
	}
	switch {
	case ct.Class.Special == "String":
		return guardOrBare(jf, get("getAsString"), bare)
	case ct.Class == c.b.Object:
		// an Object field keeps the tree as it arrived, which is what Gson's
		// ObjectTypeAdapter answers with
		return guardOrBare(jf, callNamed(id("o"), "get", strLit(jf.name)), bare)
	}
	pair := c.jsonAdapterFor(ct.Class, jf.field.Pos)
	if pair == nil || pair.reader == nil {
		return nil
	}
	// the nested reader takes the element itself, so this one does not read a
	// value out of it first
	return guardOrBare(jf, callNamed(id(ct.Class.Full), pair.reader.Name,
		callNamed(id("o"), "get", strLit(jf.name))), bare)
}

// jsonUnmapped reports a field the binding cannot walk, unless the binding is
// speculative: a class the compiler bound on a guess is skipped rather than
// reported, because the call site did not name it.
func (c *Checker) jsonUnmapped(jf jsonField) {
	if c.jsonSpeculative {
		return
	}
	c.errf(jf.field.Pos, "TY-TYP-0110", "%s has no JSON mapping for its type %s", jf.field.Name, jf.field.Type)
}

// guardOrBare wraps a read in the question of whether the document carries the
// member, or answers the bare read when the caller wants the value alone.
func guardOrBare(jf jsonField, value ast.Expr, bare bool) ast.Expr {
	if bare {
		return value
	}
	return absentGuard(jf, value)
}

// absentGuard leaves a field alone when the document does not carry it, which is
// what Gson does: the value the constructor gave it stays.
func absentGuard(jf jsonField, value ast.Expr) ast.Expr {
	return &ast.Cond{ExprBase: ast.ExprBase{Pos: pos()},
		C: callNamed(id("o"), "has", strLit(jf.name)), X: value, Y: sel(id("v"), jf.field.Name)}
}

// absentOr picks the value a member takes when the document does not carry it.
func absentOr(jf jsonField, value, fallback ast.Expr) ast.Expr {
	return &ast.Cond{ExprBase: ast.ExprBase{Pos: pos()},
		C: callNamed(id("o"), "has", strLit(jf.name)), X: value, Y: fallback}
}

// zeroOfType is the value a member of a type takes when nothing supplies one:
// what Java's `new int[1]` or a field with no initializer would give.
func zeroOfType(t ast.Type) ast.Expr {
	if p, ok := t.(*ast.PrimType); ok {
		switch p.Kind {
		case ast.Boolean:
			return boolLit(false)
		case ast.Int, ast.Long, ast.Short, ast.Byte, ast.Char:
			return intLit(0)
		case ast.Float, ast.Double:
			return &ast.Literal{ExprBase: ast.ExprBase{Pos: pos(), T: ast.TDouble}, Kind: ast.LitDouble}
		}
	}
	return &ast.Literal{ExprBase: ast.ExprBase{Pos: pos(), T: ast.NullType{}}, Kind: ast.LitNull}
}

// jsonWriteStmt is the statement that writes one field into the object.
func (c *Checker) jsonWriteStmt(jf jsonField) ast.Stmt {
	read := sel(id("self"), jf.field.Name)
	add := func(v ast.Expr) ast.Stmt {
		return exprStmtOf(callNamed(id("o"), "addProperty", strLit(jf.name), v))
	}
	if _, ok := c.jsonPrimKind(jf.field.Type); ok {
		return add(read)
	}
	ct, ok := c.erasure(jf.field.Type).(*ast.ClassType)
	if !ok || ct.Class == nil {
		return nil
	}
	if ct.Class.Special == "String" {
		return add(read)
	}
	if ct.Class == c.b.Object {
		// whatever the object is, its own class answers at run time
		c.jsonBindProgramLater()
		return exprStmtOf(callNamed(id("o"), "add", strLit(jf.name),
			callNamed(id("JsonBinding"), "writeTo", read)))
	}
	pair := c.jsonAdapterFor(ct.Class, jf.field.Pos)
	if pair == nil || pair.writer == nil {
		return nil
	}
	// a null reference is written as JSON null rather than left out, which is
	// what Gson does for a field of an object type
	return ifOf(notOf(isNull(read)),
		blockOf(exprStmtOf(callNamed(id("o"), "add", strLit(jf.name),
			callNamed(id(ct.Class.Full), pair.writer.Name, read)))),
		blockOf(exprStmtOf(callNamed(id("o"), "add", strLit(jf.name), sel(id("JsonNull"), "INSTANCE")))))
}

// castPrim renders a cast to a primitive type.
func castPrim(x ast.Expr, name string) ast.Expr {
	return &ast.Cast{ExprBase: ast.ExprBase{Pos: pos()},
		Type: &ast.TypeExpr{Pos: pos(), Name: name, Resolved: ast.PrimByName[name]}, X: x}
}

// newObjName builds `new Name()` for a prelude class named at run time.
func newObjName(name string) ast.Expr {
	return &ast.New{ExprBase: ast.ExprBase{Pos: pos()}, Type: &ast.TypeExpr{Pos: pos(), Name: name}}
}

// newObjNamed builds `new Name(arg)`.
func newObjNamed(name string, arg ast.Expr) ast.Expr {
	n := newObjName(name)
	n.(*ast.New).Args = []ast.Expr{arg}
	return n
}

// orOf / notOf build the small boolean expressions the generated code needs.
func orOf(a, b ast.Expr) ast.Expr {
	return &ast.Binary{ExprBase: ast.ExprBase{Pos: pos(), T: ast.TBoolean}, Op: "||", X: a, Y: b}
}

func notOf(a ast.Expr) ast.Expr {
	return &ast.Unary{ExprBase: ast.ExprBase{Pos: pos(), T: ast.TBoolean}, Op: "!", X: a}
}
