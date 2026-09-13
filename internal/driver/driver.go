// Package driver wires the front end, semantic analysis and the C back end
// into a single compile pipeline.
package driver

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/LangYa466/Teyru/internal/ast"
	"github.com/LangYa466/Teyru/internal/codegen"
	"github.com/LangYa466/Teyru/internal/mod"
	"github.com/LangYa466/Teyru/internal/parser"
	tyrt "github.com/LangYa466/Teyru/internal/runtime"
	"github.com/LangYa466/Teyru/internal/sema"
	"github.com/LangYa466/Teyru/internal/source"
	"github.com/LangYa466/Teyru/internal/util"
	"github.com/LangYa466/Teyru/lib"
)

// Options configures a compilation.
type Options struct {
	Out      string // output executable path
	EmitC    string // if set, also write the generated C here
	CFile    string // generated C path (defaults to a sibling .c of Out)
	CC       string // C compiler (default: clang)
	Opt      string // optimisation flag (default -O2)
	EmitLLVM string // if set, also write LLVM IR here (the backend is clang/LLVM)
	// CSourceOnly stops the pipeline once the generated C is written: no C
	// compiler runs, so no executable is produced. `teyru emit` sets it to print
	// the C without leaving a binary behind.
	CSourceOnly bool
	NoLTO       bool // disable link-time optimisation (on by default)
	Verbose     bool
	ExtraCC     []string
	KeptTemp    bool
	// Native lists C sources that implement the program's native methods; they
	// are compiled together with the generated program.
	Native []string
	// Link holds extra arguments for the link step, such as -lm or a path to
	// a static library.
	Link []string
	// NativeHeader, when set, receives a C header declaring every native
	// method the program expects to be implemented.
	NativeHeader string
}

// Result reports the outcome of a compilation.
type Result struct {
	CFile    string
	LLVMFile string
	Exe      string
	// CSource is the generated C of this build. It belongs to the result rather
	// than to a file the caller has to read before the build's cleanup removes
	// it: the C itself is scaffolding and only a path the caller passed with -c
	// outlives the build.
	CSource string
	Diags   *source.Diagnostics
}

// Compile turns Teyru sources into a native executable.
func Compile(paths []string, opts Options) (*Result, error) {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	diags := &source.Diagnostics{}
	// The module a build belongs to is decided before a single file is read:
	// it says what each package's identity is and which directories belong to
	// another module, and both of those shape the walk below.
	graph := openModule(paths, diags)
	if diags.HasErrors() {
		return &Result{Diags: diags}, fmt.Errorf("module errors")
	}
	files := []string{}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if st.IsDir() {
			// A directory stands for the package tree rooted at it: every
			// .teyru file underneath belongs to the build, which is what makes
			// `teyru build ./...` and a layout of one directory per package
			// work without listing files by hand. WalkDir is lexical, so the
			// file order does not depend on the filesystem.
			err := filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				name := d.Name()
				if d.IsDir() {
					// hidden directories hold tool state, not sources
					if path != p && strings.HasPrefix(name, ".") {
						return fs.SkipDir
					}
					// A module inside a module is a different module: its
					// packages are built when its own root is the subject of
					// the build, not as part of this one.
					if graph != nil && path != p && mod.IsModuleDir(path) {
						return fs.SkipDir
					}
					return nil
				}
				if strings.HasSuffix(name, ".teyru") {
					files = append(files, path)
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		files = append(files, p)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .teyru source files found")
	}

	astFiles := parsePrelude(diags)
	program := make([]*ast.File, 0, len(files))
	for _, f := range files {
		program = append(program, parseFile(f, diags))
	}
	// The program's own files are parsed before the packages it imports,
	// because an import of a package of this very module is satisfied by files
	// that are already here, and reading them twice would declare every class
	// in them twice.
	if graph != nil {
		astFiles = append(astFiles, resolveImports(graph, program, diags)...)
	} else {
		astFiles = append(astFiles, program...)
	}
	if diags.HasErrors() {
		return &Result{Diags: diags}, fmt.Errorf("parse errors")
	}
	prog := sema.Check(astFiles, diags)
	if diags.HasErrors() {
		return &Result{Diags: diags}, fmt.Errorf("semantic errors")
	}
	if prog.Main == nil {
		return &Result{Diags: diags}, fmt.Errorf("no entry point: declare a static method named main")
	}
	if opts.NativeHeader != "" {
		if err := writeNativeHeader(opts.NativeHeader, codegen.NativeDecls(prog), codegen.InterfaceSelectors(prog)); err != nil {
			return nil, err
		}
	}
	csrc := codegen.Emit(prog)

	rtDir, err := os.MkdirTemp("", "teyru-rt-")
	if err != nil {
		return nil, err
	}
	if !opts.KeptTemp {
		defer os.RemoveAll(rtDir)
	}
	// The generated C is scaffolding: it is written into the build's temporary
	// directory unless the caller named a path. Writing it next to the output
	// (`teyru build -o impl prog.teyru` produced impl.c) silently replaced a
	// file of the user's that happened to have that name.
	cfile := opts.CFile
	if cfile == "" {
		cfile = filepath.Join(rtDir, "program.c")
	}
	dir := filepath.Dir(cfile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	rtC := writeRuntime(rtDir)
	if err := os.WriteFile(cfile, []byte(csrc), 0o644); err != nil {
		return nil, err
	}
	if opts.EmitC != "" {
		if err := os.WriteFile(opts.EmitC, []byte(csrc), 0o644); err != nil {
			return nil, err
		}
	}
	opt := opts.Opt
	if opt == "" {
		opt = "-O2"
	}
	cc := opts.CC
	if cc == "" {
		cc = findCC()
	}
	// The result reports each output as it is written, so a failure never
	// claims a file that was not produced.
	res := &Result{CFile: cfile, CSource: csrc, Diags: diags}
	// The IR is a file the caller named explicitly, so it is written on every
	// path that gets this far, including the one that skips the C compiler.
	if err := writeLLVMIR(cc, opt, cfile, rtDir, opts.EmitLLVM); err != nil {
		return res, err
	}
	res.LLVMFile = opts.EmitLLVM
	if opts.CSourceOnly {
		// Only the generated C was asked for, so the C compiler is not run at
		// all: it would produce an executable nobody looks at, and leaving one
		// in a temporary directory is exactly what a caller asking for the C
		// does not expect.
		return res, nil
	}
	exe := opts.Out
	// -fwrapv: Java's integer arithmetic wraps, and C's is undefined on
	// overflow, which a compiler is free to fold away. It did: `Integer.MIN_VALUE
	// * -1` printed 2147483648 and `-Long.MIN_VALUE` printed 0, because the
	// optimiser answered a question the language says has an answer. Telling the
	// compiler that signed overflow wraps is the same rule Java states.
	base := []string{opt, "-std=gnu11", "-fwrapv", "-fno-strict-aliasing", "-w", "-I", rtDir, cfile}
	base = append(base, strings.Fields(rtC)...)
	base = append(base, opts.Native...)
	base = append(base, "-o", exe, "-lm", "-lpthread")
	base = append(base, opts.Link...)
	base = append(base, opts.ExtraCC...)
	args := append([]string{}, base...)
	// Link-time optimisation lets clang inline runtime helpers (string ops, the
	// allocation fast path) into the generated program. It is on by default and
	// retried without it when the toolchain has no LTO support: only the link
	// flags differ between the two attempts, so a retry is a successful build
	// like any other.
	if !opts.NoLTO {
		args = append([]string{"-flto"}, base...)
	}
	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "teyru: %s %s\n", cc, strings.Join(args, " "))
	}
	cmd := exec.Command(cc, args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if opts.NoLTO {
			return res, fmt.Errorf("C backend failed: %w", err)
		}
		if opts.Verbose {
			fmt.Fprintln(os.Stderr, "teyru: retrying without -flto")
		}
		retry := exec.Command(cc, base...)
		retry.Stderr = os.Stderr
		if err2 := retry.Run(); err2 != nil {
			return res, fmt.Errorf("C backend failed: %w", err)
		}
	}
	res.Exe = exe
	return res, nil
}

// writeLLVMIR asks the C compiler for the LLVM module of the generated C. The
// backend is clang, whose middle and back end are LLVM, so the module-level IR
// can be inspected or fed to llc/opt directly. An empty path means the caller
// asked for no IR, so callers can invoke this on every path that produces an
// output without checking first.
func writeLLVMIR(cc, opt, cfile, rtDir, out string) error {
	if out == "" {
		return nil
	}
	irArgs := []string{"-S", "-emit-llvm", "-std=gnu11", "-fwrapv", "-fno-strict-aliasing", "-w",
		"-I", rtDir, cfile, "-o", out}
	if opt != "" {
		irArgs = append(irArgs, opt)
	}
	ir := exec.Command(cc, irArgs...)
	ir.Stderr = os.Stderr
	if err := ir.Run(); err != nil {
		return fmt.Errorf("LLVM IR emission failed: %w", err)
	}
	return nil
}

// parsePrelude reads the standard library, which is Teyru source shipped with
// the compiler, as the first compilation units of every program.
func parsePrelude(diags *source.Diagnostics) []*ast.File {
	files := lib.Files()
	out := make([]*ast.File, 0, len(files))
	for _, f := range files {
		out = append(out, parseSource("<lib>/"+f.Name, f.Source, diags))
	}
	return out
}

func parseFile(path string, diags *source.Diagnostics) *ast.File {
	data, err := os.ReadFile(path)
	if err != nil {
		diags.Errorf(source.Pos{}, "TY-IO-0001", "cannot read %s: %v", path, err)
		return &ast.File{Src: source.NewFile(path, "")}
	}
	return parseSource(path, string(data), diags)
}

func parseSource(path, text string, diags *source.Diagnostics) *ast.File {
	// The parser reads a module path (`example.com/dep/pkg`) as readily as a
	// dotted one, so the source goes in as it is written on disk and every
	// position in a diagnostic is the file's own.
	return parser.Parse(source.NewFile(path, text), diags)
}

// ------------------------------------------------------------- module mode
//
// A build is a module build when there is a teyru.mod above its sources. It
// then resolves an import path the way Go does -- the module path plus a
// directory inside it -- and pulls the packages it names out of the module
// cache. A build with no teyru.mod above it compiles exactly the files it was
// given, which is what a single file on a command line has always done.

// openModule finds the module a build belongs to. A build outside a module is
// not an error: the compiler has always been usable on one file with no
// project around it. A module file that is there and cannot be read is an
// error, because the build would otherwise silently ignore every import that
// names a package the cache holds.
func openModule(paths []string, diags *source.Diagnostics) *mod.Graph {
	dir := ""
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if st.IsDir() {
			dir = p
		} else {
			dir = filepath.Dir(p)
		}
		break
	}
	if dir == "" {
		dir = "."
	}
	g, err := mod.LoadGraph(dir)
	if err == nil {
		return g
	}
	var noMod *mod.NoModuleError
	if errors.As(err, &noMod) {
		return nil
	}
	diags.Errorf(source.Pos{}, "TY-IO-0101", "%v", err)
	return nil
}

// moduleResolver gives every file of a build the identity its import path
// says it has, and loads the packages the program imports from the cache.
type moduleResolver struct {
	graph *mod.Graph
	diags *source.Diagnostics
	// seen holds the files already part of the build, by absolute path. An
	// import that is satisfied by a file the walk already collected must not
	// add it a second time: the same class declared twice is a duplicate
	// definition, and the emitter would write its C twice.
	seen map[string]bool
	// dirs remembers which package each directory declares, so that one
	// directory holding two packages is reported instead of being silently
	// merged into one.
	dirs map[string]*dirPkg
	deps []*ast.File
}

type dirPkg struct {
	name string
	file string
}

// resolveImports returns the program's files followed by the files of every
// package they import, all of them positioned in the module they belong to.
func resolveImports(graph *mod.Graph, program []*ast.File, diags *source.Diagnostics) []*ast.File {
	r := &moduleResolver{graph: graph, diags: diags, seen: map[string]bool{}, dirs: map[string]*dirPkg{}}
	for _, f := range program {
		abs := absPath(f.Src.Path)
		r.seen[abs] = true
		r.place(f, abs, "")
	}
	// The queue grows while it is walked: a package pulled in for one import
	// has imports of its own, which may pull in more packages.
	queue := append([]*ast.File(nil), program...)
	for i := 0; i < len(queue); i++ {
		for _, imp := range queue[i].Imports {
			pkg := r.resolveImport(imp)
			if pkg == nil {
				continue
			}
			queue = append(queue, r.loadPackage(pkg)...)
		}
	}
	out := append([]*ast.File(nil), program...)
	return append(out, r.deps...)
}

// resolveImport turns an import of a module package into the import path the
// checker resolves names through. Anything that is not a module import -- the
// prelude's `java.*` spellings, a package of the program compiled without a
// module -- is left exactly as written.
func (r *moduleResolver) resolveImport(imp *ast.Import) *mod.Pkg {
	pkg, isModule, err := r.graph.Resolve(imp.Path)
	if err != nil {
		code := "TY-IO-0102"
		var mismatch *mod.SumMismatchError
		if errors.As(err, &mismatch) {
			code = "TY-IO-0103"
		}
		r.diags.Errorf(imp.Pos, code, "%v", err)
		return nil
	}
	if !isModule {
		return nil
	}
	// The path is rewritten to the module path with slashes: the package's own
	// files are named that way below, and the checker splits an import path at
	// its last dot to find the type in it.
	imp.Path = pkg.Canonical()
	if len(pkg.Tail) == 0 && !imp.Static {
		// `import example.com/dep/pkg` names the package, not one type in it:
		// that is an on-demand import of every name the package declares.
		imp.Star = true
	}
	return pkg
}

// loadPackage parses the sources of one package, unless they are already part
// of the build, and gives them the package's import path as their identity.
func (r *moduleResolver) loadPackage(pkg *mod.Pkg) []*ast.File {
	files, err := mod.PackageFiles(pkg.Dir)
	if err != nil {
		r.diags.Errorf(source.Pos{}, "TY-IO-0102", "cannot read package %s: %v", pkg.ImportPath, err)
		return nil
	}
	var out []*ast.File
	for _, path := range files {
		abs := absPath(path)
		if r.seen[abs] {
			continue
		}
		r.seen[abs] = true
		f := parseFile(path, r.diags)
		r.place(f, abs, pkg.ImportPath)
		out = append(out, f)
		r.deps = append(r.deps, f)
	}
	return out
}

// place gives a file the package identity its directory has: the import path
// of that directory, for a file inside the main module, or the import path of
// the package it was loaded from. A declared package name is a name, not an
// identity, and two modules may both declare `package util` -- the identity is
// what keeps them apart.
func (r *moduleResolver) place(f *ast.File, abs, importPath string) {
	if importPath == "" {
		p, ok := r.graph.MainPackagePath(filepath.Dir(abs))
		if !ok {
			// A file from outside the module: it keeps the package it declares,
			// because there is no import path that could name it.
			return
		}
		importPath = p
	}
	if name := f.Package; name != "" {
		dir := filepath.Dir(abs)
		if prev := r.dirs[dir]; prev == nil {
			r.dirs[dir] = &dirPkg{name: name, file: abs}
		} else if prev.name != name {
			r.diags.Errorf(source.Pos{}, "TY-IO-0104",
				"%s declares package %s, but %s in the same directory declares %s", abs, name, prev.file, prev.name)
		}
	}
	f.Package = importPath
}

// absPath is the key a file is held under: one file is one compilation unit,
// however it was reached.
func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return abs
}

// writeNativeHeader writes the C prototypes a program has to implement, so
// that native methods can be written against a declaration the compiler
// generated instead of a name the author has to guess.
func writeNativeHeader(path string, decls []codegen.NativeDecl, sels []codegen.SelectorDecl) error {
	var b strings.Builder
	b.WriteString("/* native methods declared by this program.\n")
	b.WriteString("   Implement each one and pass the file back with `--native <file.c>`. */\n\n")
	b.WriteString("#ifndef TEYRU_NATIVE_H\n#define TEYRU_NATIVE_H\n\n")
	b.WriteString("#include \"tyrt.h\"\n\n")
	if len(decls) == 0 {
		b.WriteString("/* this program declares no native methods */\n")
	}
	for _, d := range decls {
		fmt.Fprintf(&b, "/* %s */\n%s;\n\n", d.Method, d.Signature)
	}
	if len(sels) > 0 {
		b.WriteString("/* Dispatch selectors of interface methods. A native method can call\n")
		b.WriteString("   back into Teyru with\n")
		b.WriteString("     ((int32_t (*)(void *, int32_t)) ty_itab(obj, SEL))(obj, arg) */\n\n")
		for _, s := range sels {
			fmt.Fprintf(&b, "#define TY_SEL_%s_%s %d\n", mangleForHeader(s.Iface), mangleForHeader(s.Method), s.Selector)
		}
		b.WriteString("\n")
	}
	b.WriteString("#endif\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("cannot write %s: %w", path, err)
	}
	return nil
}

// mangleForHeader turns a Teyru name into the upper case identifier used in
// the generated header.
func mangleForHeader(s string) string {
	return strings.ToUpper(util.Mangle(s))
}

// writeRuntime materialises the C runtime next to the generated program.
func writeRuntime(dir string) string {
	// The header only has to exist next to the sources: it is found through the
	// -I on the command line, so it is written but never reported back.
	must(os.WriteFile(filepath.Join(dir, "tyrt.h"), []byte(tyrt.Header), 0o644))
	c1 := filepath.Join(dir, "tyrt.c")
	c2 := filepath.Join(dir, "tyrt2.c")
	c3 := filepath.Join(dir, "tyrt_net.c")
	must(os.WriteFile(c1, []byte(tyrt.Core), 0o644))
	must(os.WriteFile(c2, []byte(tyrt.Extra), 0o644))
	must(os.WriteFile(c3, []byte(tyrt.Net), 0o644))
	return c1 + " " + c2 + " " + c3
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func findCC() string {
	for _, c := range []string{"clang", "gcc", "cc"} {
		if _, err := exec.LookPath(c); err == nil {
			return c
		}
	}
	return "cc"
}

// Run executes a compiled program.
func Run(exe string, args []string) (int, error) {
	cmd := exec.Command(exe, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		code := ee.ExitCode()
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			// a program killed by a signal has no exit status: Go reports 255,
			// which is also what a program that really exits 255 reports. Say
			// which signal it was, and use the status a shell would.
			return 128 + int(ws.Signal()), nil
		}
		return code, nil
	}
	return 1, err
}

// Version reports the compiler version string.
func Version() string {
	return "teyru 0.2.0 (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
}
