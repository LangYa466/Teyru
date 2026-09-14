package sema

import (
	"fmt"
	"sort"
	"strings"

	"github.com/LangYa466/Teyru/internal/ast"
	"github.com/LangYa466/Teyru/internal/source"
)

// The web half of the framework pass: controller methods become routes.
//
// Spring finds a controller's mappings by scanning annotations on the classes it
// loaded. The compiler has the same information and no scanning to do: for each
// method of a controller bean that carries a mapping annotation it generates a
// handler -- look the controller up in the context, fill the method's parameters
// from the request, call it -- and registers it under the path the annotations
// spell. The handler is a lambda with a block body, so it is ordinary checked
// code like everything else this pass emits.
//
// What the annotations mean is the caller's business to decide, and this reads
// them the way Spring does: a class-level @RequestMapping is a prefix, a method
// annotation adds a path, and the annotation's kind picks the HTTP method.

// routeSpec is one mapping: a method of a controller and the request it answers.
type routeSpec struct {
	owner   *ast.Class
	bean    string // the controller's bean name
	method  *ast.Method
	verb    string // GET, POST, ... or "*" for any
	path    string
	params  []paramSpec
	returns ast.Type
}

// paramSpec is one argument of a handler method and where its value comes from.
type paramSpec struct {
	kind string // "path", "query", "body", "header"
	name string
	want ast.Type
	def  string // the default when the value is absent
}

// mappingVerbs maps a mapping annotation to the HTTP method it answers.
var mappingVerbs = map[string]string{
	"GetMapping":    "GET",
	"PostMapping":   "POST",
	"PutMapping":    "PUT",
	"DeleteMapping": "DELETE",
	"PatchMapping":  "PATCH",
}

// applyWebRoutes finds the controllers' mappings and adds their registration to
// the container's setup, which the registry class calls.
func (c *Checker) applyWebRoutes(specs []*beanSpec) []routeSpec {
	var routes []routeSpec
	seen := map[*ast.Class]bool{}
	for _, s := range specs {
		if s.configMethod != nil || s.cl == nil || s.cl.Decl == nil {
			continue
		}
		if seen[s.cl] {
			continue
		}
		seen[s.cl] = true
		if hasAnno(s.cl.Decl.Annos, "Controller", "RestController") == nil {
			continue
		}
		base := ""
		if a := hasAnno(s.cl.Decl.Annos, "RequestMapping"); a != nil {
			base = annoText(a, "value")
		}
		routes = append(routes, c.routesOf(s, base)...)
	}
	sort.SliceStable(routes, func(i, j int) bool {
		if routes[i].path != routes[j].path {
			return routes[i].path < routes[j].path
		}
		return routes[i].method.Name < routes[j].method.Name
	})
	return routes
}

// routesOf lists the mappings of one controller class.
func (c *Checker) routesOf(s *beanSpec, base string) []routeSpec {
	var out []routeSpec
	names := make([]string, 0, len(s.cl.Methods))
	for n := range s.cl.Methods {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, m := range s.cl.Methods[n] {
			if m.Decl == nil || m.IsStatic() {
				continue
			}
			verb, path, ok := mappingOf(m)
			if !ok {
				continue
			}
			full := joinPath(base, path)
			out = append(out, routeSpec{
				owner: s.cl, bean: s.name, method: m, verb: verb, path: full,
				params: c.paramsOf(m), returns: m.Result,
			})
		}
	}
	return out
}

// mappingOf reads the mapping annotation of a method: which verb and which path.
func mappingOf(m *ast.Method) (string, string, bool) {
	if a := hasAnno(m.Decl.Annos, "RequestMapping"); a != nil {
		verb := strings.ToUpper(annoText(a, "method"))
		if verb == "" {
			verb = "*"
		}
		return verb, annoText(a, "value"), true
	}
	for name, verb := range mappingVerbs {
		if a := hasAnno(m.Decl.Annos, name); a != nil {
			return verb, annoText(a, "value"), true
		}
	}
	return "", "", false
}

// joinPath concatenates a controller's prefix with a mapping's path, so that
// "/pets" and "/{id}" make "/pets/{id}" and neither side needs a trailing slash.
func joinPath(base, path string) string {
	if base == "" {
		if path == "" {
			return "/"
		}
		return path
	}
	if path == "" {
		return base
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(path, "/")
}

// paramsOf reads where each parameter's value comes from.
func (c *Checker) paramsOf(m *ast.Method) []paramSpec {
	var out []paramSpec
	for i, p := range m.Decl.Params {
		if i >= len(m.Params) {
			break
		}
		ps := paramSpec{name: p.Name, want: m.Params[i], kind: "query"}
		switch {
		case hasAnno(p.Annos, "PathVariable") != nil:
			ps.kind = "path"
			if n := annoText(hasAnno(p.Annos, "PathVariable"), "value"); n != "" {
				ps.name = n
			}
		case hasAnno(p.Annos, "RequestBody") != nil:
			ps.kind = "body"
		case hasAnno(p.Annos, "RequestHeader") != nil:
			ps.kind = "header"
			a := hasAnno(p.Annos, "RequestHeader")
			if n := annoText(a, "value"); n != "" {
				ps.name = n
			}
			ps.def = annoText(a, "defaultValue")
		case hasAnno(p.Annos, "RequestParam") != nil:
			a := hasAnno(p.Annos, "RequestParam")
			if n := annoText(a, "value"); n != "" {
				ps.name = n
			}
			ps.def = annoText(a, "defaultValue")
		default:
			// An unannotated parameter of a handler is a query parameter of the
			// same name, which is Spring's rule for a simple type too.
			ps.def = ""
		}
		out = append(out, ps)
	}
	return out
}

// synthRoutes builds one handler per route and the statements that register
// them, for the registry class's setup method.
func (c *Checker) synthRoutes(reg *ast.Class, env *typeEnv, routes []routeSpec) []ast.Stmt {
	stmts := make([]ast.Stmt, 0, len(routes))
	for i, r := range routes {
		h := c.synthHandler(reg, env, r, i)
		c.addMethod(reg, h)
		stmts = append(stmts, exprStmtOf(callNamed(id("BeanRegistry"), "route",
			strLit(r.verb), strLit(r.path), c.routeLambda(h))))
	}
	return stmts
}

// routeLambda wraps a generated handler as a Handler, which takes the context
// as well as the request: a controller is a bean, so answering a request means
// asking the container for it rather than keeping one of one's own.
func (c *Checker) routeLambda(h *ast.Method) ast.Expr {
	params := []*ast.Param{
		{Pos: pos(), Name: "ctx"},
		{Pos: pos(), Name: "req"},
		{Pos: pos(), Name: "vars"},
	}
	return &ast.Lambda{ExprBase: ast.ExprBase{Pos: pos()}, Params: params,
		Body: callNamed(id(frameworkClass), h.Name, id("ctx"), id("req"), id("vars"))}
}

// synthHandler builds the method that answers one route: look the controller up,
// fill the parameters, call the method.
func (c *Checker) synthHandler(reg *ast.Class, env *typeEnv, r routeSpec, n int) *ast.Method {
	m := &ast.Method{Name: fmt.Sprintf("__route%d", n), Owner: reg,
		Mods: ast.ModPublic | ast.ModStatic, Result: c.objType, Pos: pos(), SynthKind: "framework-route"}
	// The map's type arguments are spelled out: a raw Map leaves the parameter
	// as the interface's own V, and `vars.get(name)` then answers V instead of
	// String -- `Integer.parseInt(V)` is not a call the checker can make.
	objs := []ast.Type{c.classType("ApplicationContext"), c.classType("HttpRequest"), c.stringMapType()}
	names := []string{"ctx", "req", "vars"}
	m.Params = objs
	m.ParamNames = names
	m.Decl = &ast.MethodDecl{Pos: pos(), Name: m.Name, Mods: m.Mods}
	for _, nm := range names {
		m.Decl.Params = append(m.Decl.Params, &ast.Param{Pos: pos(), Name: nm})
	}
	// the controller is a bean, so one instance answers every request
	recv := castTo(callNamed(id("ctx"), "getBean", strLit(r.bean)), r.owner)
	var args []ast.Expr
	for _, p := range r.params {
		args = append(args, c.paramExpr(p, r.method.Pos))
	}
	call := callNamed(recv, r.method.Name, args...)

	// A handler answers with a response the server can write, so what the
	// method returns becomes the body: a String is the body as it stands, and
	// anything else is serialized -- which is what @RestController means in
	// Spring, where the same method would have returned an object and let the
	// message converter decide.
	body := c.responseBody(r, call)
	m.Body = blockOf(body...)
	return m
}

// responseBody builds the statements that turn a handler method's result into
// the response the server writes.
func (c *Checker) responseBody(r routeSpec, call ast.Expr) []ast.Stmt {
	rt := c.erasure(r.returns)
	if ct, ok := rt.(*ast.ClassType); ok && ct.Class != nil {
		switch ct.Class.Name {
		case "HttpResponse":
			// the method built its own response, which is how an endpoint sets
			// a status other than 200 or a header of its own
			return []ast.Stmt{returnOf(call)}
		case "void":
			return []ast.Stmt{exprStmtOf(call), returnOf(newEmptyResponse())}
		}
	}
	if rt == ast.TVoid || rt == nil {
		return []ast.Stmt{exprStmtOf(call), returnOf(newEmptyResponse())}
	}
	// a String is the body as it stands; the response is built with its type
	if ct, ok := rt.(*ast.ClassType); ok && ct.Class != nil && ct.Class.Special == "String" {
		return []ast.Stmt{
			&ast.LocalVar{Pos: pos(), Type: &ast.TypeExpr{Pos: pos(), Name: "HttpResponse"},
				Vars: []*ast.VarDeclarator{{Pos: pos(), Name: "res", Init: newEmptyResponse()}}},
			exprStmtOf(assignTo(sel(id("res"), "body"), call)),
			returnOf(id("res")),
		}
	}
	// Everything else is JSON, which is what a @RestController answers with.
	// The writer is the one the Gson binding generated for the class, so a
	// controller's return type is bound by the same pass that binds any other
	// class -- and a type with no mapping is reported at the mapping rather than
	// at the first request.
	cl := ct2(rt)
	pair := c.jsonAdapterFor(cl, r.method.Pos)
	if cl == nil || pair == nil || pair.writer == nil {
		c.errf(r.method.Pos, "TY-TYP-0111",
			"%s answers with %s, which has no JSON mapping; return a String or HttpResponse, or a class the binding can walk",
			r.method.Name, r.returns)
		return []ast.Stmt{exprStmtOf(call), returnOf(newEmptyResponse())}
	}
	return []ast.Stmt{
		&ast.LocalVar{Pos: pos(),
			Type: &ast.TypeExpr{Pos: pos(), Name: cl.Full, Resolved: &ast.ClassType{Class: cl}},
			Vars: []*ast.VarDeclarator{{Pos: pos(), Name: "out", Init: call}}},
		&ast.LocalVar{Pos: pos(), Type: &ast.TypeExpr{Pos: pos(), Name: "HttpResponse"},
			Vars: []*ast.VarDeclarator{{Pos: pos(), Name: "res", Init: newEmptyResponse()}}},
		exprStmtOf(assignTo(sel(id("res"), "contentType"), strLit("application/json; charset=utf-8"))),
		exprStmtOf(assignTo(sel(id("res"), "body"),
			callNamed(callNamed(id(cl.Full), pair.writer.Name, id("out")), "toString"))),
		returnOf(id("res")),
	}
}

// ct2 is the class of a reference type, which the callers above have already
// established is one.
func ct2(t ast.Type) *ast.Class {
	ct, _ := t.(*ast.ClassType)
	if ct == nil || ct.Class == nil {
		return nil
	}
	return ct.Class
}

// newEmptyResponse builds `new HttpResponse()`.
func newEmptyResponse() ast.Expr {
	return &ast.New{ExprBase: ast.ExprBase{Pos: pos()}, Type: &ast.TypeExpr{Pos: pos(), Name: "HttpResponse"}}
}

// stringMapType is Map<String, String>, the parameters a handler is handed.
func (c *Checker) stringMapType() ast.Type {
	cl := c.global["Map"]
	if cl == nil {
		return c.objType
	}
	return &ast.ClassType{Class: cl, Args: []ast.Type{c.strType, c.strType}}
}

// jsonBodyExpr reads a @RequestBody parameter of a class type out of the
// request body.
//
// Spring picks a message converter from the request's Content-Type. Here the
// parameter's type is known at compile time, so the converter is the binding
// the compiler already generated for that class -- and a body that does not
// parse throws the same JsonSyntaxException Gson throws, from the same reader.
// A String parameter keeps the body exactly as it arrived, which is how an
// endpoint takes a payload it means to look at itself, and Object does too.
func (c *Checker) jsonBodyExpr(raw ast.Expr, want ast.Type, pos source.Pos) ast.Expr {
	ct, ok := c.erasure(want).(*ast.ClassType)
	if !ok || ct.Class == nil || ct.Class == c.b.Object || ct.Class.Special == "String" {
		return nil
	}
	pair := c.jsonAdapterFor(ct.Class, pos)
	if pair == nil || pair.reader == nil {
		return nil
	}
	return callNamed(id(ct.Class.Full), pair.reader.Name,
		callNamed(id("JsonParser"), "parseString", raw))
}

// paramExpr builds the expression that produces one handler argument.
func (c *Checker) paramExpr(p paramSpec, pos source.Pos) ast.Expr {
	var raw ast.Expr
	switch p.kind {
	case "path":
		raw = callNamed(id("vars"), "get", strLit(p.name))
	case "body":
		raw = sel(id("req"), "body")
		if e := c.jsonBodyExpr(raw, p.want, pos); e != nil {
			return e
		}
	case "header":
		// the two-argument header answers with the declared default when the
		// request carries no such header, which is what defaultValue means
		raw = callNamed(id("req"), "header", strLit(p.name), strLit(p.def))
	default:
		if p.def != "" {
			raw = callNamed(id("req"), "param", strLit(p.name), strLit(p.def))
		} else {
			raw = callNamed(id("req"), "param", strLit(p.name), strLit(""))
		}
	}
	return c.convertParam(raw, p)
}

// webConvertName is the WebConvert method that turns request text into a kind's
// value, or "" for a kind a request cannot carry.
//
// The conversions live in the library rather than in the generated call so that
// a value that does not fit is Spring's 400 instead of an uncaught
// NumberFormatException: WebConvert relabels the parse failure as a type
// mismatch, and the server answers that with 400.
func webConvertName(k ast.PrimKind) string {
	switch k {
	case ast.Int:
		return "toInt"
	case ast.Long:
		return "toLong"
	case ast.Double:
		return "toDouble"
	case ast.Float:
		return "toFloat"
	case ast.Short:
		return "toShort"
	case ast.Byte:
		return "toByte"
	}
	return ""
}

// convertParam turns the string a request carries into the parameter's type.
//
// A path variable or a query parameter arrives as text, so a numeric parameter
// has to be parsed and a reference parameter has to be cast back -- the
// alternative, letting the generated call receive a String where an int is
// declared, does not compile. A parameter whose text does not fit is the
// client's mistake, which TypeMismatchException turns into a 400.
func (c *Checker) convertParam(raw ast.Expr, p paramSpec) ast.Expr {
	rt := c.erasure(p.want)
	if pt, ok := rt.(*ast.PrimType); ok {
		if conv := webConvertName(pt.Kind); conv != "" {
			return callNamed(id("WebConvert"), conv, raw, strLit(p.name))
		}
		if pt.Kind == ast.Boolean {
			// Boolean.parseBoolean answers false for anything that is not
			// "true", as Java's does, so there is no failure to report
			return callNamed(id("Boolean"), "parseBoolean", raw)
		}
		return raw
	}
	ct, ok := rt.(*ast.ClassType)
	if !ok || ct.Class == nil || ct.Class.Special == "String" {
		return raw
	}
	if ct.Class.Kind == ast.KindEnum {
		// the constants are known here, so the name is matched against them
		// rather than cast: `(Color) "RED"` would be a ClassCastException
		return callNamed(id(ct.Class.Full), c.webEnumValue(ct.Class).Name, raw, strLit(p.name))
	}
	if k, boxed := c.b.Unbox[ct.Class]; boxed {
		if conv := webConvertName(k); conv != "" {
			return callNamed(id(ct.Class.Name), "valueOf",
				callNamed(id("WebConvert"), conv, raw, strLit(p.name)))
		}
		if k == ast.Boolean {
			return callNamed(id(ct.Class.Name), "valueOf",
				callNamed(id("Boolean"), "parseBoolean", raw))
		}
		return raw
	}
	return castTo(raw, ct.Class)
}

// webEnumValue answers with the method that turns request text into a constant
// of an enum, generating it on first use.
//
// Spring converts a path variable or a query parameter into an enum by name.
// The constants are known where the request is read, so the lookup is a chain
// of comparisons rather than a reflective valueOf: a name the enum does not
// have is the client's mistake, and fails the same way a bad number does.
func (c *Checker) webEnumValue(cl *ast.Class) *ast.Method {
	const name = "__teyruWebValue"
	if ms := cl.Methods[name]; len(ms) > 0 {
		return ms[0]
	}
	stmts := enumLookupChain(cl, id("s"))
	stmts = append(stmts, throwOf(newObjNamed("TypeMismatchException",
		callNamed(id("WebConvert"), "mismatch", id("s"), id("param"), strLit(cl.Name)))))
	m := c.newSynthMethod(cl, name, ast.ModPublic|ast.ModStatic, &ast.ClassType{Class: cl},
		[]ast.Type{c.strType, c.strType}, []string{"s", "param"}, blockOf(stmts...), "")
	c.addSynthMethod(cl, m)
	return m
}
