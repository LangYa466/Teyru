package sema

import (
	"fmt"
	"sort"
	"strings"

	"github.com/teyru-lang/Teyru/internal/ast"
)

// The bean list the container is handed.
//
// Spring resolves beans by reflection while the program starts. So does this
// container now (lib/28_container_reflect.teyru): this pass no longer writes a
// factory or an injector, it lists the classes that carry a component
// annotation, because there is no classpath for the container to scan. Teyru has no
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
	// What the compiler hands the container is the list of classes. Everything
	// else about a bean -- its name, its scope, its constructor, its @Autowired
	// fields, its @Bean methods -- is read off the class by the container itself
	// (lib/28_container_reflect.teyru), the way Spring reads it. A class is
	// registered once however many beans it is behind: a @Configuration class
	// and its @Bean methods are one class.
	stmts := make([]ast.Stmt, 0, len(specs)+1)
	seen := map[string]bool{}
	for _, s := range specs {
		if s.cl == nil || seen[s.cl.Full] {
			continue
		}
		seen[s.cl.Full] = true
		stmts = append(stmts, exprStmtOf(callNamed(id("BeanRegistry"), "register", c.classLitOf(s.cl))))
	}
	stmts = append(stmts, c.synthRoutes(reg, env, c.fwRoutes)...)
	stmts = append(stmts, returnOf(boolLit(true)))
	m.Body = blockOf(stmts...)
	return m
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

// classType returns the prelude class type named by a simple name.
func (c *Checker) classType(name string) ast.Type {
	if cl := c.global[name]; cl != nil {
		return &ast.ClassType{Class: cl}
	}
	return c.objType
}

// frameworkDone guards against the pass running twice on one program.
var _ = fmt.Sprint
