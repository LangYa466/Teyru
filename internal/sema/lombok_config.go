package sema

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/teyru-lang/Teyru/internal/ast"
	"github.com/teyru-lang/Teyru/internal/source"
)

// This file reads the one Lombok setting Teyru needs a configuration file for:
// @CustomLog generates a logger only if lombok.log.custom.declaration says how
// to build one, and Lombok takes that from a lombok.config file.
//
// The search rule is Lombok's: start in the directory of the source file that
// carries the annotation and walk up through its ancestors, so a project-wide
// lombok.config at the root covers every file under it. Lombok stops early at a
// directory whose lombok.config sets config.stopBubbling; Teyru does not read
// that key and always walks to the filesystem root (docs/lombok.md records the
// difference). Per key, the nearest file that sets it wins — the same as Lombok,
// which lets a closer file override one setting without repeating the rest.

// lombokConfigName is the file Lommbok's settings live in.
const lombokConfigName = "lombok.config"

// customLogDeclarationKey configures what @CustomLog generates.
const customLogDeclarationKey = "lombok.log.custom.declaration"

// configValue returns the value of key from the nearest lombok.config above the
// given source file. It reports false when no file sets the key.
func configValue(srcPath, key string) (string, bool) {
	if srcPath == "" || strings.HasPrefix(srcPath, "<") {
		// built-in sources are embedded, not files on disk: they have no
		// directory to search from
		return "", false
	}
	dir := filepath.Dir(srcPath)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	for {
		if text, err := os.ReadFile(filepath.Join(dir, lombokConfigName)); err == nil {
			if v, ok := parseLombokConfig(string(text))[key]; ok {
				return v, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// reached the filesystem root
			return "", false
		}
		dir = parent
	}
}

// parseLombokConfig reads `key = value` lines. Lombok's own parser ignores blank
// lines, `#` comments and lines without a `=`, and so does this one.
func parseLombokConfig(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.IndexByte(line, '=')
		if i < 0 {
			continue
		}
		k := strings.TrimSpace(line[:i])
		v := strings.TrimSpace(line[i+1:])
		if k != "" {
			out[k] = v
		}
	}
	return out
}

// logParam is one of the parameter kinds a log declaration can ask for. The set
// and the spelling are Lombok's.
type logParam int

const (
	logParamType  logParam = iota // TYPE: the annotated class, as a class literal
	logParamName                  // NAME: its name, as a string
	logParamTopic                 // TOPIC: the topic @CustomLog(topic = …) names
	logParamNull                  // NULL: null
)

// logDeclaration is a parsed lombok.log.custom.declaration.
//
// Lombok's grammar is `[LoggerType ]LoggerFactoryType.factoryMethod(params)`
// with an optional second `(params)` group for the call that passes a topic:
//
//	lombok.log.custom.declaration = my.Logger my.Factory.getLogger(NAME)
//	lombok.log.custom.declaration = my.Logger my.Factory.getLogger(NAME)(TOPIC)
type logDeclaration struct {
	// loggerType is the declared type of the generated field, empty when the
	// declaration does not name one.
	loggerType string
	factory    string
	method     string
	// plain and withTopic are the parameter lists to use when no topic is given
	// and when one is; at most one of each is allowed, as in Lombok.
	plain     []logParam
	withTopic []logParam
	hasTopic  bool
	hasPlain  bool
}

// Pattern of Lombok's LogDeclaration, with one addition: `%s` is accepted as a
// synonym for NAME. Lombok itself has no %s placeholder — its parameter kinds are
// TYPE, NAME, TOPIC and NULL — but `MyLogFactory.getLog(%s)` is a common way to
// write the declaration, so it reads the same as asking for the class name.
var logDeclarationPattern = regexp.MustCompile(`^(?:([^ ]+) )?([^(]+)\.([^(]+)((?:\([A-Z,%s]*\))+)$`)

// logParamsPattern splits the parameter groups of a declaration.
var logParamsPattern = regexp.MustCompile(`\(([A-Z,%s]*)\)`)

// parseLogDeclaration parses the value of lombok.log.custom.declaration. The
// error text names what is wrong, because the only other thing the user sees is
// a compiler diagnostic at the @CustomLog annotation.
func parseLogDeclaration(text string) (*logDeclaration, error) {
	m := logDeclarationPattern.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return nil, fmt.Errorf("%s must read [LoggerType ]LoggerFactoryType.factoryMethod(params) or …(params)(params), not %q",
			customLogDeclarationKey, text)
	}
	d := &logDeclaration{loggerType: m[1], factory: strings.TrimSpace(m[2]), method: strings.TrimSpace(m[3])}
	groups := logParamsPattern.FindAllStringSubmatch(m[4], -1)
	for _, g := range groups {
		params, err := parseLogParams(g[1])
		if err != nil {
			return nil, err
		}
		if hasLogParam(params, logParamTopic) {
			if d.hasTopic {
				return nil, fmt.Errorf("%s has more than one parameter list with TOPIC", customLogDeclarationKey)
			}
			d.withTopic, d.hasTopic = params, true
			continue
		}
		if d.hasPlain {
			return nil, fmt.Errorf("%s has more than one parameter list without TOPIC", customLogDeclarationKey)
		}
		d.plain, d.hasPlain = params, true
	}
	if !d.hasTopic && !d.hasPlain {
		return nil, fmt.Errorf("%s names no factory method parameters", customLogDeclarationKey)
	}
	return d, nil
}

// parseLogParams splits one comma-separated parameter list.
func parseLogParams(text string) ([]logParam, error) {
	if text == "" {
		return nil, nil
	}
	var out []logParam
	for _, name := range strings.Split(text, ",") {
		switch name {
		case "TYPE":
			out = append(out, logParamType)
		case "NAME", "%s", "%S":
			out = append(out, logParamName)
		case "TOPIC":
			out = append(out, logParamTopic)
		case "NULL":
			out = append(out, logParamNull)
		default:
			return nil, fmt.Errorf("%s: unknown parameter %q; the kinds are TYPE, NAME, TOPIC and NULL", customLogDeclarationKey, name)
		}
	}
	return out, nil
}

// hasLogParam reports whether a parameter list contains kind.
func hasLogParam(params []logParam, kind logParam) bool {
	for _, p := range params {
		if p == kind {
			return true
		}
	}
	return false
}

// customLogInit is the synthesized method that builds the @CustomLog logger.
const customLogInit = "$log$init"

// lombokCustomLog creates the `log` field @CustomLog asks for. What the field
// is holds comes from lombok.config: lombok.log.custom.declaration names the
// factory and the factory method, and which of the class name, the topic and
// null to pass to it.
//
// With no setting to read there is nothing to generate, so the annotation is
// reported as unsatisfied rather than silently ignored.
func (c *Checker) lombokCustomLog(cl *ast.Class, a *ast.Annotation) {
	if cl.FieldMap["log"] != nil {
		return
	}
	text, ok := configValue(c.srcPath(cl), customLogDeclarationKey)
	if !ok {
		c.errf(cl.Decl.Pos, "TY-INT-0006",
			"@CustomLog needs %s in a %s file in the source file's directory or above it",
			customLogDeclarationKey, lombokConfigName)
		return
	}
	d, err := parseLogDeclaration(text)
	if err != nil {
		c.errf(cl.Decl.Pos, "TY-INT-0006", "%v", err)
		return
	}
	if hasLogParam(d.plain, logParamType) || hasLogParam(d.withTopic, logParamType) {
		c.errf(cl.Decl.Pos, "TY-INT-0006",
			"@CustomLog cannot pass TYPE: Teyru types `X.class` as Object, so a class literal cannot be handed to a factory; use NAME instead")
		return
	}
	logCls, err := c.customLogType(cl, d)
	if err != nil {
		c.errf(cl.Decl.Pos, "TY-INT-0006", "%v", err)
		return
	}
	factory := c.lookupClassName(c.classEnv(cl), d.factory)
	if factory == nil {
		c.errf(cl.Decl.Pos, "TY-INT-0006",
			"@CustomLog: cannot resolve the factory class %q named by %s", d.factory, customLogDeclarationKey)
		return
	}
	params, err := customLogParams(d, annoString(a, "topic"))
	if err != nil {
		c.errf(cl.Decl.Pos, "TY-INT-0006", "%v", err)
		return
	}

	// The call lives in a synthesized method, so the ordinary checker resolves
	// the factory method and reports a bad name, a missing argument or a
	// non-static factory exactly as it would for hand-written code.
	argExprs := make([]ast.Expr, 0, len(params))
	for _, p := range params {
		switch p {
		case logParamName:
			// @Log names the logger after the class the same way.
			argExprs = append(argExprs, litAt(strLit(cl.Name), cl.Decl.Pos))
		case logParamTopic:
			argExprs = append(argExprs, litAt(strLit(annoString(a, "topic")), cl.Decl.Pos))
		case logParamNull:
			argExprs = append(argExprs, litAt(nullLit(), cl.Decl.Pos))
		}
	}
	logType := &ast.ClassType{Class: logCls}
	call := callNamed(id(factory.Name), d.method, argExprs...)
	call.Pos = cl.Decl.Pos
	init := c.newSynthMethod(cl, customLogInit, ast.ModPrivate|ast.ModStatic, logType, nil, nil,
		blockOf(returnOf(call)), "@CustomLog")
	init.Pos = cl.Decl.Pos
	c.addSynthMethod(cl, init)

	f := &ast.Field{Name: "log", Type: logType,
		Mods: ast.ModPrivate | ast.ModStatic | ast.ModFinal, Pos: cl.Decl.Pos, Storage: true, Anno: "@CustomLog"}
	c.addSynthField(cl, f)
	if cl.ClInit == nil {
		cl.ClInit = &ast.Method{Name: "<clinit>", Owner: cl, Mods: ast.ModStatic,
			Result: ast.TVoid, Pos: cl.Decl.Pos, SynthKind: "clinit"}
	}
	// A static field's initializer is emitted into <clinit> without being
	// checked (codegen/emit_static.go), so the resolved method is recorded here.
	ref := callNamed(nil, customLogInit)
	ref.Method = init
	ref.SetType(logType)
	f.InitExpr = ref
}

// customLogType resolves the field's type: the LoggerType of the declaration if
// it names one, the standard Logger otherwise. Lombok falls back to the factory
// type instead, which does not exist in Teyru (docs/lombok.md records this).
func (c *Checker) customLogType(cl *ast.Class, d *logDeclaration) (*ast.Class, error) {
	if d.loggerType == "" {
		logCls := c.global["Logger"]
		if logCls == nil {
			return nil, fmt.Errorf("@CustomLog: %s names no logger type and the standard Logger is missing", customLogDeclarationKey)
		}
		return logCls, nil
	}
	logCls := c.lookupClassName(c.classEnv(cl), d.loggerType)
	if logCls == nil {
		return nil, fmt.Errorf("@CustomLog: cannot resolve the logger type %q named by %s", d.loggerType, customLogDeclarationKey)
	}
	return logCls, nil
}

// customLogParams picks the parameter list the topic asks for, reporting the two
// mismatches Lombok reports: a topic the declaration has no list for, and a
// declaration that needs a topic the annotation does not give.
func customLogParams(d *logDeclaration, topic string) ([]logParam, error) {
	if strings.TrimSpace(topic) == "" {
		if !d.hasPlain {
			return nil, fmt.Errorf("@CustomLog requires a topic: %s has no parameter list without TOPIC", customLogDeclarationKey)
		}
		return d.plain, nil
	}
	if !d.hasTopic {
		return nil, fmt.Errorf("@CustomLog does not allow a topic: %s has no parameter list with TOPIC", customLogDeclarationKey)
	}
	return d.withTopic, nil
}

// litAt points a synthesized literal at the declaration that asked for it, so a
// diagnostic about the generated call lands on the annotation the user wrote
// instead of the start of the file.
func litAt(lit *ast.Literal, p source.Pos) *ast.Literal {
	lit.Pos = p
	return lit
}

// srcPath is the file a class was declared in, empty for built-in sources.
func (c *Checker) srcPath(cl *ast.Class) string {
	if cl.File == nil || cl.File.Src == nil {
		return ""
	}
	return cl.File.Src.Path
}
