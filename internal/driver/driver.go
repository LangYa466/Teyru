// Package driver wires the front end, semantic analysis and the C back end
// into a single compile pipeline.
package driver

import (
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

	diags := &source.Diagnostics{}
	astFiles := parsePrelude(diags)
	for _, f := range files {
		astFiles = append(astFiles, parseFile(f, diags))
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
	base := []string{opt, "-std=gnu11", "-fno-strict-aliasing", "-w", "-I", rtDir, cfile}
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
	irArgs := []string{"-S", "-emit-llvm", "-std=gnu11", "-fno-strict-aliasing", "-w",
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
	return parser.Parse(source.NewFile(path, text), diags)
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
	must(os.WriteFile(c1, []byte(tyrt.Core), 0o644))
	must(os.WriteFile(c2, []byte(tyrt.Extra), 0o644))
	return c1 + " " + c2
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
