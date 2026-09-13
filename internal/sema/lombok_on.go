package sema

import (
	"strings"

	"github.com/LangYa466/Teyru/internal/ast"
	"github.com/LangYa466/Teyru/internal/util"
)

// This file implements Lombok's onX family: the options that copy an annotation
// onto the members another annotation generates. `@Getter(onMethod_ =
// @Deprecated)` puts @Deprecated on every generated getter, `@Setter(onParam_ =
// @NonNull)` puts @NonNull on the setter's parameter, and
// `@AllArgsConstructor(onConstructor_ = @Deprecated)` puts @Deprecated on the
// generated constructor.
//
// Two spellings are read. Lombok's own is the annotation argument whose name
// ends in an underscore (javac7 needed the annotation wrapped in @__(...), which
// is unwrapped here); the bare `@onMethod_Deprecated` spelling next to @Getter is
// accepted as well, so the family does not have to be an argument at all.
//
// Reference: https://projectlombok.org/features/experimental/onX

// The option and prefix names Lombok uses. The trailing underscore is part of
// the javac8+ spelling; the argument is also read without it.
const (
	onMethod      = "onMethod_"
	onParam       = "onParam_"
	onConstructor = "onConstructor_"
)

// onSite is the declaration a generating annotation sits on: the annotation
// itself plus the annotation list of the declaration that carries it. The onX
// options are read from the annotation, the bare spelling from its neighbours,
// which is why the whole list is kept.
type onSite struct {
	gen   *ast.Annotation
	annos []*ast.Annotation
}

// onSiteOf pairs a generating annotation with the annotation list of the
// declaration that carries it, which is where the bare `@onMethod_X` spelling is
// looked for.
func onSiteOf(a *ast.Annotation, annos []*ast.Annotation) onSite {
	return onSite{gen: a, annos: annos}
}

// onOption returns the annotations named by one onX option, for example
// `onMethod_ = @Deprecated` or its javac7 spelling `onMethod = @__(@Deprecated)`.
//
// Only a single annotation is understood: the parser drops the annotations of an
// array-valued argument (it keeps one entry per element but not the annotation
// itself), so `onMethod_ = {@A, @B}` cannot be read back. docs/lombok.md records
// this as a limitation.
func (s onSite) onOption(key string) []*ast.Annotation {
	if s.gen == nil {
		return nil
	}
	arg := s.gen.Arg(key)
	if arg == nil && strings.HasSuffix(key, "_") {
		arg = s.gen.Arg(strings.TrimSuffix(key, "_"))
	}
	if arg == nil || arg.Anno == nil {
		return nil
	}
	return unwrapOnAnno(arg.Anno)
}

// bare returns the annotations written next to the generating annotation under
// an onX name: `@onMethod_Deprecated` means @Deprecated on the generated member.
// The prefix is stripped, and the arguments are kept.
func (s onSite) bare(prefix string) []*ast.Annotation {
	var out []*ast.Annotation
	for _, a := range s.annos {
		name := simpleAnnoName(a.Name)
		if !strings.HasPrefix(name, prefix) || len(name) == len(prefix) {
			continue
		}
		out = append(out, &ast.Annotation{Pos: a.Pos, Name: name[len(prefix):], Args: a.Args})
	}
	return out
}

// memberAnnos are the annotations the site puts on the member it generates.
func (s onSite) memberAnnos() []*ast.Annotation {
	return append(s.onOption(onMethod), s.bare(onMethod)...)
}

// paramAnnos are the annotations the site puts on the parameters of the member
// it generates.
func (s onSite) paramAnnos() []*ast.Annotation {
	return append(s.onOption(onParam), s.bare(onParam)...)
}

// ctorAnnos are the annotations the site puts on the constructor it generates.
func (s onSite) ctorAnnos() []*ast.Annotation {
	return append(s.onOption(onConstructor), s.bare(onConstructor)...)
}

// unwrapOnAnno removes the @__ wrapper of the javac7 spelling, which exists
// because javac7 annotations could not hold an annotation.
func unwrapOnAnno(a *ast.Annotation) []*ast.Annotation {
	if !isUnderscoreName(simpleAnnoName(a.Name)) {
		return []*ast.Annotation{a}
	}
	if v := a.Value(); v != nil && v.Anno != nil {
		return unwrapOnAnno(v.Anno)
	}
	return nil
}

// isUnderscoreName reports whether a name is made only of underscores, which is
// what Lombok's @__ wrapper is.
func isUnderscoreName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if name[i] != '_' {
			return false
		}
	}
	return true
}

// simpleAnnoName strips a qualified annotation name down to its last segment.
func simpleAnnoName(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// onCopies is what a generating site asked to be copied onto one member: the
// annotations for the member itself, and the annotations for each of its
// parameters.
type onCopies struct {
	member []*ast.Annotation
	params []*ast.Annotation
}

// generatedOn records the copies placed on the members a compilation generated.
//
// ast.Method has one annotation slot — the `Anno` metadata string, already used
// for the generator's name — and none for its parameters, so the copies need a
// home of their own. A compilation runs one at a time (driver.Compile), so
// package state is enough; the table is emptied when the pass starts, so it never
// outlives the program that filled it.
var generatedOn = map[*ast.Method]*onCopies{}

// onCopiesOf returns the annotations copied onto a generated member, nil if none.
func onCopiesOf(m *ast.Method) *onCopies { return generatedOn[m] }

// placeOn copies the annotations of a generating site onto a member that was
// just generated, and then does to that member what Teyru does to a hand-written
// one carrying the same annotations.
//
// Teyru acts on exactly one annotation that can be copied: @NonNull, which
// becomes a check on every parameter it lands on. Everything else is recorded as
// it stands — @Deprecated, for instance, is inert in Teyru whether it is
// hand-written or generated, because nothing in the compiler reports it.
func (c *Checker) placeOn(m *ast.Method, member, params []*ast.Annotation) {
	if m == nil || (len(member) == 0 && len(params) == 0) {
		return
	}
	generatedOn[m] = &onCopies{member: member, params: params}
	c.onNonNullParams(m, params)
}

// onNonNullParams inserts the null check of a copied @NonNull on every parameter
// of a generated member (Lombok documents onParam_ as applying to the generated
// method's parameters). The message names the parameter, exactly as it does for
// a hand-written parameter.
func (c *Checker) onNonNullParams(m *ast.Method, params []*ast.Annotation) {
	if m.Body == nil || hasAnno(params, "NonNull") == nil || len(m.ParamNames) == 0 {
		return
	}
	checks := make([]ast.Stmt, 0, len(m.ParamNames))
	for i, name := range m.ParamNames {
		if i < len(m.Params) && util.IsPrim(m.Params[i]) {
			// Lombok warns that @NonNull is meaningless on a primitive and skips
			// it; Teyru has no warning channel, so it skips it without one.
			continue
		}
		checks = append(checks, ifOf(isNull(id(name)),
			throwOf(newObj(c.b.NPE, strLit(name+" is marked non-null but is null"))), nil))
	}
	if len(checks) == 0 {
		return
	}
	m.Body.Stmts = insertAfterCtorChain(m.Body.Stmts, checks)
}

// insertAfterCtorChain inserts statements after a leading this(...)/super(...)
// call, which has to stay first. Lombok puts the checks @NonNull generates in
// the same place, immediately after the chained call.
func insertAfterCtorChain(stmts []ast.Stmt, insert []ast.Stmt) []ast.Stmt {
	at := 0
	if len(stmts) > 0 {
		if es, ok := stmts[0].(*ast.ExprStmt); ok {
			if call, ok := es.X.(*ast.Call); ok && call.ThisCtor {
				at = 1
			}
		}
	}
	out := make([]ast.Stmt, 0, len(stmts)+len(insert))
	out = append(out, stmts[:at]...)
	out = append(out, insert...)
	return append(out, stmts[at:]...)
}
