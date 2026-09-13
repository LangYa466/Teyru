package mod

import "strings"

// rewriteImportLine rewrites one line if it is an import of a slashed path.
// Everything after the keyword is at most a `static` keyword, then the path,
// then whitespace and an optional comment; only the path is touched.
func rewriteImportLine(line string) string {
	rest, ok := cutImportLine(line)
	if !ok || !strings.Contains(rest, "/") {
		return line
	}
	pre := line[:len(line)-len(rest)]
	body, comment := rest, ""
	if i := strings.Index(rest, "//"); i >= 0 {
		body, comment = rest[:i], rest[i:]
	}
	// the path is the last word of the line, whatever whitespace follows it
	end := len(strings.TrimRight(body, " \t"))
	start := end
	for start > 0 && body[start-1] != ' ' && body[start-1] != '\t' {
		start--
	}
	path := body[start:end]
	// A keyword before the path is `static`, Java's spelling of "one member of
	// it"; anything else is not an import this function understands, and
	// rewriting half of it would only move the error somewhere else.
	if kw := strings.TrimSpace(body[:start]); kw != "" && kw != "static" {
		return line
	}
	if !ValidImportSpelling(path) {
		// Something the module system cannot read as a path (`import a//b`, a
		// path with a space): leave it as written and let the parser report it
		// where it is, rather than half-rewriting it into a different error.
		return line
	}
	return pre + body[:start] + strings.ReplaceAll(path, "/", ".") + body[end:] + comment
}

// cutImportLine returns what follows an `import` keyword, when the line is an
// import statement at all.
func cutImportLine(line string) (string, bool) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	rest, ok := strings.CutPrefix(line[i:], "import")
	if !ok {
		return "", false
	}
	// `importx` is an identifier, not an import
	if rest == "" || rest[0] != ' ' && rest[0] != '\t' {
		return "", false
	}
	return rest, true
}

// ValidImportSpelling reports whether a slashed import path can be rewritten:
// valid characters, no empty elements, no `.` or `..` element (which would
// name a directory outside the module).
func ValidImportSpelling(path string) bool {
	if !ValidPath(path) {
		return false
	}
	for _, elem := range strings.Split(path, "/") {
		if elem == "." || elem == ".." {
			return false
		}
	}
	return true
}

// ImportPathOfPackage is the inverse: the slashed path a package of a module
// has, given the module path and the package's directory inside it. The
// module root is the module itself.
func ImportPathOfPackage(module, rel string) string {
	if rel == "" || rel == "." {
		return module
	}
	return module + "/" + strings.TrimPrefix(rel, "/")
}

// LooksLikeModulePath reports whether a dotted import path is spelled the way
// a module path is: `example.com/dep/pkg` folded to `example.com.dep.pkg` puts
// a host name first, so the second element is a top-level domain. Java spells
// packages the other way round (`com.example.Foo`), which is why this never
// matches one.
//
// It only decides how a failure is reported: a path that matches no module is
// left to the compiler's own name resolution, and one that matches a module
// but cannot be found there deserves to say so.
func LooksLikeModulePath(dotted string) bool {
	elems := strings.Split(dotted, ".")
	if len(elems) < 3 {
		return false
	}
	return tlds[elems[1]]
}

var tlds = map[string]bool{
	"com": true, "org": true, "net": true, "io": true, "dev": true,
	"edu": true, "gov": true, "app": true, "sh": true, "cloud": true,
	"xyz": true, "info": true, "biz": true, "me": true, "in": true,
	"gg": true, "rs": true, "tw": true, "jp": true, "de": true, "uk": true,
}
