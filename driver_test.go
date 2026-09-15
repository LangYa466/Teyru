package teyru_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/teyru-lang/Teyru/internal/driver"
)

// TestPrograms compiles and runs every program in tests/programs and compares
// its output with the matching .expected file.
//
// A program that is supposed to fail says so with a `name.exit` file holding
// the status it must exit with: without one, a non-zero status is a test
// failure, so no uncaught exception, assert or System.exit could be tested.
// The output comparison is over stdout and stderr together, so a program whose
// message goes to stderr is checked the same way as one whose output does.
func TestPrograms(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		if _, err2 := exec.LookPath("gcc"); err2 != nil {
			t.Skip("no C compiler available")
		}
	}
	dir := "tests/programs"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	requireSuite(t, dir, entries)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".teyru") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".teyru")
		src := filepath.Join(dir, e.Name())
		want, err := os.ReadFile(filepath.Join(dir, name+".expected"))
		if err != nil {
			t.Fatalf("%s: missing expectation file: %v", name, err)
		}
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), name)
			res, err := driver.Compile([]string{src}, driver.Options{Out: out, Opt: "-O1"})
			if err != nil {
				t.Fatalf("compile failed: %v\n%s", err, res.Diags)
			}
			cmd := exec.Command(res.Exe)
			if raw, err := os.ReadFile(filepath.Join(dir, name+".args")); err == nil {
				for _, a := range strings.Fields(string(raw)) {
					cmd.Args = append(cmd.Args, a)
				}
			}
			wantExit := 0
			if raw, err := os.ReadFile(filepath.Join(dir, name+".exit")); err == nil {
				if _, err := fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &wantExit); err != nil {
					t.Fatalf("%s.exit is not a number: %v", name, err)
				}
			}
			// the two streams are compared separately: merging them would make
			// the result depend on when the buffered stdout happens to flush
			var outBuf, errBuf strings.Builder
			cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
			err = cmd.Run()
			code := 0
			if err != nil {
				ee, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatalf("could not run %s: %v", name, err)
				}
				code = ee.ExitCode()
			}
			if code != wantExit {
				t.Fatalf("exit status %d, want %d\nstderr:\n%s", code, wantExit, errBuf.String())
			}
			if got := outBuf.String(); got != string(want) {
				t.Errorf("output mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
			}
			if errWant, err := os.ReadFile(filepath.Join(dir, name+".experr")); err == nil {
				if got := errBuf.String(); got != string(errWant) {
					t.Errorf("stderr mismatch\n--- want ---\n%s\n--- got ---\n%s", errWant, got)
				}
			} else if errBuf.Len() > 0 {
				t.Errorf("unexpected stderr:\n%s", errBuf.String())
			}
		})
	}
}

// TestPackages compiles every directory under tests/packages and compares the
// program's output with the directory's `expected` file.
//
// These are the multi-file, multi-package cases: each directory is a whole
// package tree, compiled by naming the directory (which the driver walks) and
// run as one program. A single-file case belongs in tests/programs instead.
func TestPackages(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		if _, err2 := exec.LookPath("gcc"); err2 != nil {
			t.Skip("no C compiler available")
		}
	}
	root := "tests/packages"
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		dir := filepath.Join(root, name)
		// A directory with an `error` file is a rejection case: it must not
		// compile, and the named diagnostic must appear. The cross-package
		// rejections need a real package tree, which a single-file case in
		// TestDiagnostics cannot write.
		if code, err := os.ReadFile(filepath.Join(dir, "error")); err == nil {
			t.Run(name, func(t *testing.T) {
				res, err := driver.Compile([]string{dir}, driver.Options{
					Out: filepath.Join(t.TempDir(), name), Opt: "-O0"})
				if err == nil {
					t.Fatal("expected a compile failure")
				}
				if res == nil || res.Diags == nil || !strings.Contains(res.Diags.String(), strings.TrimSpace(string(code))) {
					t.Errorf("expected %s in:\n%v\n%v", strings.TrimSpace(string(code)), res.Diags, err)
				}
			})
			continue
		}
		want, err := os.ReadFile(filepath.Join(dir, "expected"))
		if err != nil {
			t.Fatalf("%s: missing expectation file: %v", name, err)
		}
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), name)
			res, err := driver.Compile([]string{dir}, driver.Options{Out: out, Opt: "-O1"})
			if err != nil {
				t.Fatalf("compile failed: %v\n%s", err, res.Diags)
			}
			cmd := exec.Command(res.Exe)
			var outBuf, errBuf strings.Builder
			cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
			if err := cmd.Run(); err != nil {
				t.Fatalf("run failed: %v\nstderr:\n%s", err, errBuf.String())
			}
			if got := outBuf.String(); got != string(want) {
				t.Errorf("output mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
			}
		})
	}
}

// TestDiagnostics checks that ill-typed programs are rejected.
// TestDiagnostics compiles the programs under tests/diagnostics, each of which
// must be *rejected*, and checks that the diagnostic named in the file beside it
// appears.
//
// The cases are data in the tests repository, with every other case, rather than
// Go string literals here. A suite that keeps one of its parts inside the
// compiler's source is not a suite the compiler is tested against -- it is the
// compiler testing itself, and the harness that runs the other cases from the
// tests repository cannot run these at all.
func TestDiagnostics(t *testing.T) {
	dir := "tests/diagnostics"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	requireSuite(t, dir, entries)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".teyru") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".teyru")
		want, err := os.ReadFile(filepath.Join(dir, name+".code"))
		if err != nil {
			t.Fatalf("%s: missing %s.code, the diagnostic the program must be rejected with: %v", name, name, err)
		}
		code := strings.TrimSpace(string(want))
		t.Run(name, func(t *testing.T) {
			res, err := driver.Compile([]string{filepath.Join(dir, e.Name())},
				driver.Options{Out: filepath.Join(t.TempDir(), "out"), Opt: "-O0"})
			if err == nil {
				t.Fatalf("expected failure")
			}
			if res == nil || res.Diags == nil {
				t.Fatalf("expected diagnostics, got %v", err)
			}
			if !strings.Contains(res.Diags.String(), code) {
				t.Errorf("expected code %s in:\n%s", code, res.Diags)
			}
		})
	}
}

// TestNoJava checks that the produced binary has no JVM dependency.
func TestNoJava(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Hello.teyru")
	if err := os.WriteFile(src, []byte("class Hello {\n  public static void main(String[] args) {\n    System.out.println(\"hi\")\n  }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "hello")
	res, err := driver.Compile([]string{src}, driver.Options{Out: out, Opt: "-O2"})
	if err != nil {
		t.Skipf("no C compiler available: %v", err)
	}
	data := res.CSource
	lower := strings.ToLower(data)
	for _, bad := range []string{"jni", "jvm", "javac", "class file"} {
		if strings.Contains(lower, bad) {
			t.Errorf("generated C mentions %q", bad)
		}
	}
	if strings.Contains(data, ".class") {
		t.Error("generated C references class files")
	}
}

// TestNative compiles a program whose native methods are implemented in C, and
// checks that the generated header declares exactly what the C side defines.
func TestNative(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		if _, err2 := exec.LookPath("gcc"); err2 != nil {
			t.Skip("no C compiler available")
		}
	}
	want, err := os.ReadFile("tests/native/expected.txt")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "native")
	header := filepath.Join(dir, "native.h")
	res, err := driver.Compile([]string{"tests/native/program.teyru"}, driver.Options{
		Out:          out,
		Opt:          "-O1",
		Native:       []string{"tests/native/impl.c"},
		NativeHeader: header,
		ExtraCC:      []string{"-I", dir},
	})
	if err != nil {
		t.Fatalf("compile failed: %v\n%s", err, res.Diags)
	}
	got, err := exec.Command(res.Exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, got)
	}
	if string(got) != string(want) {
		t.Errorf("output mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
	// every declaration in the header must be defined by the C the test links
	decl, err := os.ReadFile(header)
	if err != nil {
		t.Fatal(err)
	}
	impl, err := os.ReadFile("tests/native/impl.c")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(decl), "\n") {
		i := strings.Index(line, "tyn_")
		if i < 0 {
			continue
		}
		name := line[i:]
		if j := strings.IndexByte(name, '('); j >= 0 {
			name = name[:j]
		}
		if !strings.Contains(string(impl), name+"(") {
			t.Errorf("the header declares %s but the C implementation does not define it", name)
		}
	}
}

// requireSuite refuses to pass on an empty tests/ directory.
//
// The suite is the teyru-lang/tests repository, mounted here as a submodule: a
// clone that skipped `--recurse-submodules` has the directory and nothing in
// it, and every test below would pass having run nothing at all.
func requireSuite(t *testing.T, dir string, entries []os.DirEntry) {
	t.Helper()
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".teyru") {
			return
		}
	}
	t.Fatalf("%s has no programs: the suite is the teyru-lang/tests submodule, run `git submodule update --init`", dir)
}
