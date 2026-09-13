package sema

import (
	"fmt"
	"sort"
	"strings"

	"github.com/LangYa466/Teyru/internal/ast"
	"github.com/LangYa466/Teyru/internal/source"
)

// The Spring-compatible container pass.
//
// Spring resolves beans by reflection while the program starts. Teyru has no
// reflection, but it does have the whole program: every class, every field,
// every constructor is known before anything runs. So the compiler can do the
// work Spring does at startup, and do it here instead. What comes out is one
// registration per bean -- the class, a factory, an injector -- collected into
// a synthesized class whose static initializer fills the container's registry.
//
// The generated code is ordinary Teyru: it goes through the ordinary checker,
// so a bean that cannot be satisfied is a source-level diagnostic with a
// position, not a stack trace after the program is already up. That is the
// whole reason to resolve this at compile time rather than bolt a reflective
// container onto a language that has none.

// frameworkClass is the simple name of the synthesized registry class.
const frameworkClass = "__TeyruFramework"

// frameworkFull is the registry class's full name. It is reserved: a user class
// of the same name is refused rather than shadowed, because the generated
// registrations would land in the user's class and disappear.
const frameworkFull = frameworkClass

// componentStereotypes are the class annotations that make a class a bean.
// They are synonyms, which is how Spring treats them: the name says what layer
// the class belongs to, not what the container does with it.
var componentStereotypes = []string{
	"Component", "Service", "Repository", "Controller", "RestController",
}

// beanSpec is one bean the container will own.
type beanSpec struct {
	cl        *ast.Class
	name      string // bean name, "" until assigned
	primary   bool
	singleton bool
	// configMethod is the @Bean method that produces this bean, for a bean
	// defined by a @Configuration class rather than by its own type. configClass
	// is the class it is declared on.
	configMethod *ast.Method
	configClass  *ast.Class
}

// injectSpec is one member the injector fills.
type injectSpec struct {
	field *ast.Field // nil for a constructor parameter
	param int        // constructor parameter index when field is nil
	name  string     // field or parameter name, for diagnostics
	want  ast.Type   // the declared type
	qual  string     // @Qualifier value, "" when absent
	value *valueSpec // @Value, when the member takes a property instead
}

// valueSpec is a @Value property reference: the key and its optional default.
type valueSpec struct {
	key      string
	fallback string
	hasFB    bool
}

// applyFramework finds the container's beans and synthesizes their
// registration. It runs after the Lombok pass, so a member Lombok generated is
// already there to be injected into and to have its annotations read.
func (c *Checker) applyFramework() {
	if c.frameworkDone {
		return
	}
	c.frameworkDone = true
	specs := c.collectBeans()
	if len(specs) == 0 {
		return
	}
	// bean names must be unique before anything else can be checked: every
	// later diagnostic ("no bean of type X") is a lie if two beans share a name
	names := map[string]*beanSpec{}
	for _, s := range specs {
		if prev := names[s.name]; prev != nil {
			c.errf(s.cl.Decl.Pos, "TY-TYP-0100", "two beans are named %s: %s and %s", s.name, prev.cl.Name, s.cl.Name)
			continue
		}
		names[s.name] = s
	}
	c.checkInjections(specs, names)
	c.checkCycles(specs)
	c.fwSpecs = specs
	c.fwRoutes = c.applyWebRoutes(specs)
	c.synthRegistry(specs)
}

// collectBeans walks the program's classes and returns the ones the container
// owns, in a deterministic order so that two builds of one program generate the
// same code.
func (c *Checker) collectBeans() []*beanSpec {
	var specs []*beanSpec
	for _, cl := range c.classes {
		if cl.Builtin || cl.Decl == nil {
			continue
		}
		if cl.Full == frameworkFull || cl.Name == frameworkClass {
			c.errf(cl.Decl.Pos, "TY-TYP-0101", "%s is declared by the framework and cannot be redefined", frameworkClass)
			continue
		}
		if cl.Kind != ast.KindClass {
			continue
		}
		if a := hasAnno(cl.Decl.Annos, componentStereotypes...); a != nil {
			specs = append(specs, &beanSpec{
				cl:        cl,
				name:      beanName(cl, a),
				primary:   hasAnno(cl.Decl.Annos, "Primary") != nil,
				singleton: scopeOf(cl.Decl.Annos) == "singleton",
			})
			continue
		}
		if a := hasAnno(cl.Decl.Annos, "Configuration"); a != nil {
			// A @Configuration class is a bean of its own as well as the home of
			// its @Bean methods: the methods run on its instance, which means
			// the container has to build and inject it like any other bean,
			// which is what Spring does too.
			specs = append(specs, &beanSpec{
				cl:        cl,
				name:      beanName(cl, a),
				primary:   hasAnno(cl.Decl.Annos, "Primary") != nil,
				singleton: scopeOf(cl.Decl.Annos) == "singleton",
			})
			specs = append(specs, c.beanMethodsOf(cl)...)
		}
	}
	return specs
}

// beanMethodsOf returns one spec per @Bean method of a @Configuration class.
// The configuration class itself is not a bean; Spring builds it only to call
// these methods on it, and doing the same here keeps the generated factories
// identical in shape to every other one.
func (c *Checker) beanMethodsOf(cl *ast.Class) []*beanSpec {
	var out []*beanSpec
	names := make([]string, 0, len(cl.Methods))
	for n := range cl.Methods {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, m := range cl.Methods[n] {
			if m.Decl == nil {
				continue
			}
			a := hasAnno(m.Decl.Annos, "Bean")
			if a == nil {
				continue
			}
			if m.IsStatic() {
				c.errf(m.Decl.Pos, "TY-TYP-0102", "@Bean method %s must not be static", m.Name)
				continue
			}
			if m.Result == nil || m.Result == ast.TVoid {
				c.errf(m.Decl.Pos, "TY-TYP-0102", "@Bean method %s must return the bean's type", m.Name)
				continue
			}
			name := annoText(a, "value")
			if name == "" {
				name = m.Name
			}
			// A primitive result is boxed, as Spring does: a bean's type is a
			// class, and `@Bean int` is a bean of type Integer.
			beanType := c.erasure(m.Result)
			if p, ok := beanType.(*ast.PrimType); ok {
				if box := c.b.Boxes[p.Kind]; box != nil {
					beanType = &ast.ClassType{Class: box}
				}
			}
			bc, ok := beanType.(*ast.ClassType)
			if !ok || bc.Class == nil {
				c.errf(m.Decl.Pos, "TY-TYP-0102", "@Bean method %s does not return a class type", m.Name)
				continue
			}
			out = append(out, &beanSpec{
				cl:           bc.Class,
				name:         name,
				primary:      hasAnno(m.Decl.Annos, "Primary") != nil,
				singleton:    scopeOf(m.Decl.Annos) == "singleton",
				configMethod: m,
				configClass:  cl,
			})
		}
	}
	return out
}

// annoText reads an annotation's string argument written either way: the value
// form (@Value("x"), @Component("bean")) or the named one (@Value(value =
// "x")). Lombok's annoString reads only the named form, which is right for the
// annotations it handles and wrong for these, whose arguments are written both
// ways in the wild.
func annoText(a *ast.Annotation, name string) string {
	if a == nil {
		return ""
	}
	if s := annoString(a, name); s != "" {
		return s
	}
	for _, arg := range a.Args {
		// An argument written without a name binds to `value`, which is Java's
		// rule for annotations. It used to bind to whatever element the caller
		// asked for, so `@RequestMapping("/pets")` made the path the HTTP verb:
		// the route answered for the method named `/pets` and every real
		// request to it came back 405.
		if arg.Name == "" {
			if name != "value" {
				continue
			}
		} else if arg.Name != name {
			continue
		}
		if lit, ok := arg.Value.(*ast.Literal); ok && lit.Kind == ast.LitString {
			return lit.Str
		}
	}
	return ""
}

// scopeOf reads @Scope, defaulting to the singleton Spring uses.
func scopeOf(annos []*ast.Annotation) string {
	a := hasAnno(annos, "Scope")
	if a == nil {
		return "singleton"
	}
	if s := annoText(a, "value"); s != "" {
		return s
	}
	return "singleton"
}

// beanName is the name a bean is looked up by: @Component("x") if it says so,
// otherwise the simple class name with its first letter lowered, which is
// Spring's own convention.
func beanName(cl *ast.Class, a *ast.Annotation) string {
	if n := annoText(a, "value"); n != "" {
		return n
	}
	n := cl.Name
	if n == "" {
		return n
	}
	return strings.ToLower(n[:1]) + n[1:]
}

// checkInjections reports every injection the container cannot satisfy. This is
// the check Spring can only make at startup, and the reason the pass exists:
// a missing bean, two candidates with no way to choose, or an injection point
// the generated code cannot write to are all decided here.
func (c *Checker) checkInjections(specs []*beanSpec, names map[string]*beanSpec) {
	for _, s := range specs {
		for _, in := range c.injectionsOf(s) {
			if in.value != nil {
				continue
			}
			match, ambiguous := c.beansOfType(specs, in.want, in.qual)
			switch {
			case len(match) == 0:
				c.errf(c.injectPos(s, in), "TY-TYP-0103",
					"no bean of type %s to inject into %s", in.want, in.name)
			case ambiguous:
				c.errf(c.injectPos(s, in), "TY-TYP-0104",
					"%d beans of type %s: name one with @Qualifier", len(match), in.want)
			}
		}
	}
}

// checkCycles refuses a dependency loop at compile time.
//
// Spring reports this while starting up, after the program is already built and
// deployed. The whole graph is here, so a loop can be found now -- and a loop
// through a constructor could not be broken at runtime anyway, since the object
// would have to exist before its own argument did.
func (c *Checker) checkCycles(specs []*beanSpec) {
	// the edges: which beans each bean's injections resolve to
	edges := map[*beanSpec][]*beanSpec{}
	byName := map[string]*beanSpec{}
	for _, s := range specs {
		byName[s.name] = s
	}
	for _, s := range specs {
		var deps []*beanSpec
		for _, in := range c.injectionsOf(s) {
			if d := c.chosenBean(specs, in); d != nil && d != s {
				deps = append(deps, d)
			}
		}
		edges[s] = deps
	}
	const (
		white = 0
		grey  = 1
		black = 2
	)
	state := map[*beanSpec]int{}
	path := []*beanSpec{}
	reported := map[string]bool{}
	var walk func(s *beanSpec)
	walk = func(s *beanSpec) {
		state[s] = grey
		path = append(path, s)
		for _, d := range edges[s] {
			switch state[d] {
			case grey:
				// the cycle is the tail of the path that starts at d
				start := 0
				for i, p := range path {
					if p == d {
						start = i
						break
					}
				}
				loop := append(append([]*beanSpec{}, path[start:]...), d)
				names := make([]string, len(loop))
				for i, p := range loop {
					names[i] = p.name
				}
				key := strings.Join(names, ">")
				if !reported[key] {
					reported[key] = true
					c.errf(d.cl.Decl.Pos, "TY-TYP-0107",
						"circular dependency: %s", strings.Join(names, " -> "))
				}
			case white:
				walk(d)
			}
		}
		path = path[:len(path)-1]
		state[s] = black
	}
	for _, s := range specs {
		if state[s] == white {
			walk(s)
		}
	}
}

// injectPos is where a diagnostic about one injection points.
func (c *Checker) injectPos(s *beanSpec, in injectSpec) source.Pos {
	if in.field != nil {
		return in.field.Pos
	}
	if s.configMethod != nil && in.param < len(s.configMethod.Decl.Params) {
		return s.configMethod.Decl.Params[in.param].Pos
	}
	return s.cl.Decl.Pos
}

// beansOfType lists the beans assignable to a type, and whether choosing among
// them is ambiguous. @Primary settles it; a @Qualifier settles it by name.
func (c *Checker) beansOfType(specs []*beanSpec, want ast.Type, qual string) ([]*beanSpec, bool) {
	var out []*beanSpec
	for _, s := range specs {
		if s.cl == nil || !c.isSubtype(&ast.ClassType{Class: s.cl}, want) {
			continue
		}
		if qual != "" && s.name != qual {
			continue
		}
		out = append(out, s)
	}
	if qual != "" {
		return out, false
	}
	primaries := 0
	for _, s := range out {
		if s.primary {
			primaries++
		}
	}
	if primaries == 1 {
		for _, s := range out {
			if s.primary {
				return []*beanSpec{s}, false
			}
		}
	}
	return out, len(out) > 1
}

// injectionsOf lists the members a bean's injector fills: its constructor's
// parameters, then its @Autowired and @Value fields.
func (c *Checker) injectionsOf(s *beanSpec) []injectSpec {
	if s.configMethod != nil {
		return c.paramInjections(s.configMethod)
	}
	var out []injectSpec
	if ctor := c.injectableCtor(s.cl); ctor != nil {
		out = append(out, c.paramInjections(ctor)...)
	}
	for _, f := range s.cl.Fields {
		if f.Anno != "" || f.IsProp {
			continue
		}
		in := injectSpec{field: f, name: f.Name, want: f.Type}
		annos := c.fieldAnnos(s.cl, f)
		if q := hasAnno(annos, "Qualifier"); q != nil {
			in.qual = annoText(q, "value")
		}
		if v := hasAnno(annos, "Value"); v != nil {
			in.value = parseValue(annoText(v, "value"))
		} else if hasAnno(annos, "Autowired") == nil {
			continue
		}
		out = append(out, in)
	}
	return out
}

// paramInjections reads the @Autowired/@Value/@Qualifier annotations of a
// method's or constructor's parameters.
func (c *Checker) paramInjections(m *ast.Method) []injectSpec {
	if m == nil || m.Decl == nil {
		return nil
	}
	var out []injectSpec
	for i, p := range m.Decl.Params {
		if i >= len(m.Params) {
			break
		}
		in := injectSpec{param: i, name: p.Name, want: m.Params[i]}
		if q := hasAnno(p.Annos, "Qualifier"); q != nil {
			in.qual = annoText(q, "value")
		}
		if v := hasAnno(p.Annos, "Value"); v != nil {
			in.value = parseValue(annoText(v, "value"))
		} else if hasAnno(p.Annos, "Autowired") == nil {
			continue
		}
		out = append(out, in)
	}
	return out
}

// injectableCtor is the constructor the container calls: the one @Autowired
// names, or the only one there is (Spring 4.3's rule for a single constructor),
// or the no-argument one.
func (c *Checker) injectableCtor(cl *ast.Class) *ast.Method {
	var marked *ast.Method
	for _, ctor := range cl.Ctors {
		if ctor.Decl != nil && hasAnno(ctor.Decl.Annos, "Autowired") != nil {
			if marked != nil {
				c.errf(ctor.Pos, "TY-TYP-0105", "two constructors of %s are annotated @Autowired", cl.Name)
				return marked
			}
			marked = ctor
		}
	}
	if marked != nil {
		return marked
	}
	if len(cl.Ctors) == 1 {
		return cl.Ctors[0]
	}
	for _, ctor := range cl.Ctors {
		if len(ctor.Params) == 0 {
			return ctor
		}
	}
	if len(cl.Ctors) > 1 {
		c.errf(cl.Decl.Pos, "TY-TYP-0105",
			"%s has %d constructors and none is annotated @Autowired", cl.Name, len(cl.Ctors))
	}
	return nil
}

// fieldAnnos returns the annotations written on a field. A field's annotations
// belong to the declaration that introduced it -- `@Autowired String name` --
// and reach the field through the declarator Lombok also reads them from.
func (c *Checker) fieldAnnos(cl *ast.Class, f *ast.Field) []*ast.Annotation {
	if cl.Decl == nil {
		return nil
	}
	for _, mem := range cl.Decl.Members {
		fd, ok := mem.(*ast.FieldDecl)
		if !ok {
			continue
		}
		for _, v := range fd.Vars {
			if v.Fld == f {
				return fd.Annos
			}
		}
	}
	return nil
}

// parseValue reads a @Value's "${key}" or "${key:default}".
func parseValue(s string) *valueSpec {
	if !strings.HasPrefix(s, "${") || !strings.HasSuffix(s, "}") {
		return &valueSpec{key: s}
	}
	body := s[2 : len(s)-1]
	if i := strings.IndexByte(body, ':'); i >= 0 {
		return &valueSpec{key: body[:i], fallback: body[i+1:], hasFB: true}
	}
	return &valueSpec{key: body}
}

// ---------------------------------------------------------------- code generation

// synthRegistry builds the class that registers every bean. One static method
// sets the whole thing up and a static field calls it, so the ordinary class
// initialization the entry sequence already performs runs it before main's body
// and no change to the emitter's startup is needed.
func (c *Checker) synthRegistry(specs []*beanSpec) {
	file := specs[0].cl.File
	if file == nil {
		return
	}
	cd := &ast.ClassDecl{Pos: pos(), Kind: ast.KindClass, Name: frameworkClass, Mods: ast.ModFinal}
	reg := c.newClass(frameworkClass, frameworkFull, ast.KindClass)
	reg.Decl = cd
	reg.File = file
	reg.Mods = cd.Mods
	cd.Sym = reg
	reg.Super = c.objType
	reg.Resolved = true
	c.classes = append(c.classes, reg)

	env := c.classEnv(reg)
	setup := c.synthSetup(reg, env, specs)

	// The static field is what makes the initializer run: the entry sequence
	// initializes every class the program declares, and a class's <clinit> is
	// what fills its static fields. Its initializer is emitted into <clinit>
	// without being checked (codegen/emit_static.go), so the resolved method is
	// recorded here by hand -- an unresolved call would emit a bare name.
	ref := callNamed(nil, setup.Name)
	ref.Method = setup
	ref.Static = true
	ref.SetType(ast.TBoolean)
	f := &ast.Field{Name: "__beans", Type: ast.TBoolean, Mods: ast.ModPrivate | ast.ModStatic | ast.ModFinal,
		Pos: pos(), Storage: true, Owner: reg}
	f.InitExpr = ref
	c.addSynthField(reg, f)
	// The field's initializer is emitted into <clinit>, and a class without one
	// has no <clinit> at all -- the emitter returns before it looks at any
	// static field. Creating the method is what gives the assignment somewhere
	// to go.
	reg.ClInit = &ast.Method{Name: "<clinit>", Owner: reg, Mods: ast.ModStatic,
		Result: ast.TVoid, Pos: pos(), SynthKind: "clinit"}
	c.addMethod(reg, setup)
	c.resolveHeader(reg)
	c.resolveMembers(reg)
	c.layout(reg)
	c.checkBodies(reg)
}

// synthSetup builds `static boolean __setup()`, which registers every bean.
func (c *Checker) synthSetup(reg *ast.Class, env *typeEnv, specs []*beanSpec) *ast.Method {
	m := &ast.Method{Name: "__setup", Owner: reg, Mods: ast.ModPublic | ast.ModStatic,
		Result: ast.TBoolean, Pos: pos(), SynthKind: "framework-setup"}
	stmts := make([]ast.Stmt, 0, len(specs)+1)
	for i, s := range specs {
		factory := c.synthFactory(reg, env, s, i)
		c.addMethod(reg, factory)
		injector := c.synthInjector(reg, env, s, i)
		c.addMethod(reg, injector)
		stmts = append(stmts, exprStmtOf(callNamed(id("BeanRegistry"), "define",
			strLit(s.name),
			c.classLitOf(s.cl),
			c.lambdaOf("BeanFactory", factory, 1),
			c.lambdaOf("BeanInjector", injector, 2),
			boolLit(s.singleton),
			boolLit(s.primary),
			c.stringArray(env, c.beanTypeNames(s.cl)))))
	}
	stmts = append(stmts, c.synthRoutes(reg, env, c.fwRoutes)...)
	stmts = append(stmts, returnOf(boolLit(true)))
	m.Body = blockOf(stmts...)
	return m
}

// synthFactory builds the method that constructs one bean, taking its
// constructor's arguments from the context.
func (c *Checker) synthFactory(reg *ast.Class, env *typeEnv, s *beanSpec, n int) *ast.Method {
	m := &ast.Method{Name: fmt.Sprintf("__create%d", n), Owner: reg,
		Mods: ast.ModPublic | ast.ModStatic, Result: c.objType, Pos: pos(), SynthKind: "framework-factory"}
	ctx := &ast.Param{Pos: pos(), Name: "ctx", Type: &ast.TypeExpr{Pos: pos(), Name: "ApplicationContext"}}
	m.Decl = &ast.MethodDecl{Pos: pos(), Name: m.Name, Mods: m.Mods, Params: []*ast.Param{ctx}}
	m.Params = []ast.Type{c.classType("ApplicationContext")}
	m.ParamNames = []string{"ctx"}

	var args []ast.Expr
	var call ast.Expr
	if s.configMethod != nil {
		// A @Bean method runs on its configuration class's instance, which the
		// container builds and injects like any other bean -- which is what
		// Spring does too, and what makes a @Configuration class able to have
		// its own @Autowired members.
		recv := castTo(callNamed(id("ctx"), "getBean", c.classLitOf(s.configClass)), s.configClass)
		for _, in := range c.paramInjections(s.configMethod) {
			args = append(args, c.injectionExpr(in, c.chosenBean(c.fwSpecs, in)))
		}
		call = callNamed(recv, s.configMethod.Name, args...)
	} else {
		for _, in := range c.paramInjections(c.injectableCtor(s.cl)) {
			args = append(args, c.injectionExpr(in, c.chosenBean(c.fwSpecs, in)))
		}
		call = newObj(s.cl, args...)
	}
	m.Body = blockOf(returnOf(call))
	return m
}

// synthInjector builds the method that fills a bean's members and runs its
// lifecycle callback.
func (c *Checker) synthInjector(reg *ast.Class, env *typeEnv, s *beanSpec, n int) *ast.Method {
	m := &ast.Method{Name: fmt.Sprintf("__inject%d", n), Owner: reg,
		Mods: ast.ModPublic | ast.ModStatic, Result: ast.TVoid, Pos: pos(), SynthKind: "framework-injector"}
	pb := &ast.Param{Pos: pos(), Name: "bean", Type: &ast.TypeExpr{Pos: pos(), Name: "Object"}}
	pc := &ast.Param{Pos: pos(), Name: "ctx", Type: &ast.TypeExpr{Pos: pos(), Name: "ApplicationContext"}}
	m.Decl = &ast.MethodDecl{Pos: pos(), Name: m.Name, Mods: m.Mods, Params: []*ast.Param{pb, pc}}
	m.Params = []ast.Type{c.objType, c.classType("ApplicationContext")}
	m.ParamNames = []string{"bean", "ctx"}

	// a @Bean method's result is produced by the configuration class, which
	// injects its own members; there is nothing on the bean itself to fill
	if s.configMethod != nil {
		m.Body = blockOf()
		return m
	}

	self := &ast.LocalVar{
		Pos:  pos(),
		Type: &ast.TypeExpr{Pos: pos(), Name: s.cl.Full, Resolved: &ast.ClassType{Class: s.cl}},
		Vars: []*ast.VarDeclarator{{Pos: pos(), Name: "self", Init: castTo(id("bean"), s.cl)}},
	}
	stmts := []ast.Stmt{self}
	for _, in := range c.injectionsOf(s) {
		if in.field == nil {
			continue
		}
		stmts = append(stmts, exprStmtOf(assignTo(sel(id("self"), in.field.Name), c.injectionExpr(in, c.chosenBean(c.fwSpecs, in)))))
	}
	for _, post := range c.postConstructs(s.cl) {
		stmts = append(stmts, exprStmtOf(callNamed(id("self"), post.Name)))
	}
	m.Body = blockOf(stmts...)
	return m
}

// postConstructs lists the @PostConstruct methods of a class, in a
// deterministic order.
func (c *Checker) postConstructs(cl *ast.Class) []*ast.Method {
	var out []*ast.Method
	names := make([]string, 0, len(cl.Methods))
	for n := range cl.Methods {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, m := range cl.Methods[n] {
			if m.Decl == nil || hasAnno(m.Decl.Annos, "PostConstruct") == nil {
				continue
			}
			if len(m.Params) != 0 || m.Result != ast.TVoid {
				c.errf(m.Decl.Pos, "TY-TYP-0106", "@PostConstruct method %s must take no arguments and return void", m.Name)
				continue
			}
			out = append(out, m)
		}
	}
	return out
}

// injectionExpr is the expression that produces the value for one injection.
//
// The bean is chosen here, by name, rather than asked for by type at runtime.
// The compiler is the only thing that knows which classes implement an
// interface, so resolving an injection from the whole program is the step that
// turns "no bean of type Greeter" from a startup failure into a compile error,
// and it makes the runtime lookup a dictionary hit.
func (c *Checker) injectionExpr(in injectSpec, chosen *beanSpec) ast.Expr {
	if in.value != nil {
		if in.value.hasFB {
			return callNamed(id("ctx"), "getProperty", strLit(in.value.key), strLit(in.value.fallback))
		}
		return callNamed(id("ctx"), "getProperty", strLit(in.value.key))
	}
	name := ""
	if chosen != nil {
		name = chosen.name
	}
	call := callNamed(id("ctx"), "getBean", strLit(name))
	ct, _ := c.erasure(in.want).(*ast.ClassType)
	if ct != nil && ct.Class.Builtin == false {
		return castTo(call, ct.Class)
	}
	return call
}

// chosenBean is the bean an injection resolves to at compile time: the one a
// @Qualifier names, the @Primary one, or the only candidate.
func (c *Checker) chosenBean(specs []*beanSpec, in injectSpec) *beanSpec {
	match, ambiguous := c.beansOfType(specs, in.want, in.qual)
	if ambiguous || len(match) == 0 {
		return nil
	}
	return match[0]
}

// beanTypeNames lists every type name a bean satisfies: its own class, its
// superclasses and the interfaces they implement. The container matches a
// getBean(SomeType.class) against this list, which is how a lookup by interface
// works without any runtime subtyping of classes.
func (c *Checker) beanTypeNames(cl *ast.Class) []string {
	seen := map[*ast.Class]bool{}
	var out []string
	var visit func(k *ast.Class)
	visit = func(k *ast.Class) {
		if k == nil || seen[k] {
			return
		}
		seen[k] = true
		// the full name, because that is what Class.getName() answers with:
		// a prelude class is teyru.Integer, not Integer
		out = append(out, k.Full)
		if k.Super != nil {
			visit(k.Super.Class)
		}
		for _, i := range k.Ifaces {
			visit(i.Class)
		}
	}
	visit(cl)
	sort.Strings(out)
	return out
}

// stringArray builds a String[] whose elements are known at compile time. The
// emitter reads the element type off the node rather than checking it, so it is
// set here.
func (c *Checker) stringArray(env *typeEnv, vals []string) ast.Expr {
	elems := make([]ast.Expr, len(vals))
	for i, v := range vals {
		elems[i] = strLit(v)
	}
	return &ast.ArrayInit{ExprBase: ast.ExprBase{Pos: pos(), T: &ast.ArrayType{Elem: c.strType}},
		Elems: elems, Elem: c.strType}
}

// classLitOf builds `Foo.class` for a class.
func (c *Checker) classLitOf(cl *ast.Class) ast.Expr {
	te := &ast.TypeExpr{Pos: pos(), Name: cl.Full, Resolved: &ast.ClassType{Class: cl}}
	return &ast.ClassLit{ExprBase: ast.ExprBase{Pos: pos(), T: c.classLiteralType()}, Type: te}
}

// castTo builds `(Foo) x`.
func castTo(x ast.Expr, cl *ast.Class) ast.Expr {
	return &ast.Cast{
		ExprBase: ast.ExprBase{Pos: pos()},
		Type:     &ast.TypeExpr{Pos: pos(), Name: cl.Full, Resolved: &ast.ClassType{Class: cl}},
		X:        x,
	}
}

// lambdaOf wraps a static method of the registry as a lambda of a functional
// interface, so the container holds a BeanFactory rather than a method name.
func (c *Checker) lambdaOf(iface string, target *ast.Method, arity int) ast.Expr {
	// The parameters are named for the position the functional interface puts
	// them in -- BeanFactory.create(ApplicationContext) and
	// BeanInjector.inject(Object, ApplicationContext) -- because an implicitly
	// typed lambda binds its names to the interface's parameter types in order.
	var names []string
	var args []ast.Expr
	if arity == 1 {
		names = []string{"ctx"}
		args = []ast.Expr{id("ctx")}
	} else {
		names = []string{"bean", "ctx"}
		args = []ast.Expr{id("bean"), id("ctx")}
	}
	params := make([]*ast.Param, arity)
	for i := 0; i < arity; i++ {
		params[i] = &ast.Param{Pos: pos(), Name: names[i]}
	}
	return &ast.Lambda{ExprBase: ast.ExprBase{Pos: pos()}, Params: params,
		Body: callNamed(id(frameworkClass), target.Name, args...)}
}

// classType returns the prelude class type named by a simple name.
func (c *Checker) classType(name string) ast.Type {
	if cl := c.global[name]; cl != nil {
		return &ast.ClassType{Class: cl}
	}
	return c.objType
}

// frameworkDone guards against the pass running twice on one program.
var _ = fmt.Sprint
