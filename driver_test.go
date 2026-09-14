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
func TestDiagnostics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		code string
	}{
		{"semicolon", "class A {\n  public static void main(String[] args) {\n    int x = 1;\n  }\n}\n", "TY-SYN-0001"},
		{"type", "class A {\n  public static void main(String[] args) {\n    int x = \"s\"\n  }\n}\n", "TY-TYP-0051"},
		{"unknownName", "class A {\n  public static void main(String[] args) {\n    System.out.println(missing)\n  }\n}\n", "TY-TYP-0048"},
		{"abstractMissing", "abstract class B {\n  abstract int f()\n}\nclass A extends B {\n  public static void main(String[] args) {\n  }\n}\n", "TY-TYP-0019"},
		{"recursiveCtor", "class A {\n  A(int n) {\n    this(1)\n  }\n  A() {\n    this(2)\n  }\n}\nclass Main {\n  public static void main(String[] args) {\n    new A()\n  }\n}\n", "TY-TYP-0075"},
		{"doubleSwitch", "class Main {\n  public static void main(String[] args) {\n    double d = 0.5\n    switch (d) {\n      case 1.5 -> System.out.println(\"x\")\n      default -> System.out.println(\"y\")\n    }\n  }\n}\n", "TY-TYP-0035"},
		{"notExhaustive", "class Main {\n  public static void main(String[] args) {\n    int n = 7\n    String s = switch (n) {\n      case 1 -> \"one\"\n      case 2 -> \"two\"\n    }\n    System.out.println(s)\n  }\n}\n", "TY-TYP-0096"},
		{"longSelector", "class Main {\n  public static void main(String[] args) {\n    long v = 1\n    switch (v) {\n      case 1 -> System.out.println(\"one\")\n      default -> System.out.println(\"other\")\n    }\n  }\n}\n", "TY-TYP-0035"},
		{"missingBean", "import teyru.Service\nimport teyru.Autowired\n\ninterface Repo { String ping() }\n\n@Service\nclass Svc {\n  @Autowired Repo repo\n}\nclass Main {\n  public static void main(String[] args) {\n  }\n}\n", "TY-TYP-0103"},
		{"circularBeans", "import teyru.Service\nimport teyru.Autowired\n\n@Service\nclass A {\n  @Autowired B b\n}\n@Service\nclass B {\n  @Autowired A a\n}\nclass Main {\n  public static void main(String[] args) {\n  }\n}\n", "TY-TYP-0107"},
		{"ambiguousBeans", "import teyru.Service\nimport teyru.Component\nimport teyru.Autowired\n\ninterface G { String g() }\n@Component class G1 implements G { public String g() { return \"1\" } }\n@Component class G2 implements G { public String g() { return \"2\" } }\n@Service class S { @Autowired G g }\nclass Main {\n  public static void main(String[] args) {\n  }\n}\n", "TY-TYP-0104"},
		{"lambdaThisInStatic", "import java.util.function.Supplier\n\nclass Main {\n  int n() { return 3 }\n  public static void main(String[] args) {\n    Supplier<Integer> s = () -> n() + 1\n    System.out.println(s.get())\n  }\n}\n", "TY-TYP-0098"},
		// An expression continued on the next line is a new statement, so this
		// is what keeps the second line from being a silent unary plus.
		{"noEffectStatement", "class Main {\n  public static void main(String[] args) {\n    int a = 1\n    int b = 2\n    long x = 100L + a\n             + b\n    System.out.println(x)\n  }\n}\n", "TY-TYP-0114"},
		// The implicit close is an interface call, so a class that merely has a
		// close() method would dispatch into nothing at run time.
		{"resourceNotCloseable", "class P {\n  public void close() { }\n}\nclass Main {\n  public static void main(String[] args) {\n    try (P p = new P()) { System.out.println(\"in\") }\n  }\n}\n", "TY-TYP-0113"},
		// The check stopped at any target that was not a bare identifier, so a
		// final field reached through a receiver was writable from anywhere and
		// the modifier meant nothing outside the class that declared it. All
		// three forms go through the same check.
		{"finalFieldAssigned", "class F {\n  public final int k = 1\n}\nclass Main {\n  public static void main(String[] args) {\n    F o = new F()\n    o.k = 9\n    System.out.println(o.k)\n  }\n}\n", "TY-TYP-0058"},
		{"finalFieldCompound", "class F {\n  public final int k = 1\n}\nclass Main {\n  public static void main(String[] args) {\n    F o = new F()\n    o.k += 1\n    System.out.println(o.k)\n  }\n}\n", "TY-TYP-0058"},
		{"finalFieldUpdate", "class F {\n  public final int k = 1\n}\nclass Main {\n  public static void main(String[] args) {\n    F o = new F()\n    o.k++\n    System.out.println(o.k)\n  }\n}\n", "TY-TYP-0058"},
		// The declaration was not counted as the assignment it is, so the
		// counter was still zero at the first reassignment of a `val` and the
		// error waited for the second one.
		{"valReassigned", "class Main {\n  public static void main(String[] args) {\n    val x = 1\n    x = 2\n    System.out.println(x)\n  }\n}\n", "TY-TYP-0057"},
		// A final local with no initializer does get its one assignment, which
		// is Java's rule, so the second is the one to reject.
		{"finalLocalTwice", "class Main {\n  public static void main(String[] args) {\n    final int x\n    x = 3\n    x = 4\n    System.out.println(x)\n  }\n}\n", "TY-TYP-0057"},
		// A property's storage field is private for every property, so the
		// accessor's modifiers are what decides who may use it -- and the check
		// was skipped for properties, which left a private one readable and
		// writable from anywhere.
		{"privateProperty", "class C {\n  private int v {\n    get { return field + 7 }\n    set { field = value }\n  }\n}\nclass Main {\n  public static void main(String[] args) {\n    C c = new C()\n    c.v = 3\n    System.out.println(c.v)\n  }\n}\n", "TY-TYP-0046"},
		// A name resolves by its simple name whatever package is written in
		// front of it, so this import used to be accepted and the List it
		// really meant was picked anyway.
		{"misspelledImport", "import java.utli.List\n\nclass Main {\n  public static void main(String[] args) {\n    List<String> l = new ArrayList<String>()\n    System.out.println(l.size())\n  }\n}\n", "TY-TYP-0115"},
		{"unknownImport", "import com.example.Nothing\n\nclass Main {\n  public static void main(String[] args) {\n  }\n}\n", "TY-TYP-0115"},
		// The `extends` check runs before Lombok, so @Value's `final` arrived
		// too late to stop a subclass: `class Ext extends V` compiled where
		// Lombok says "cannot inherit from final".
		{"extendsLombokValue", "import lombok.Value\n\n@Value\nclass V {\n  int x\n}\nclass Ext extends V {\n  Ext() {\n    super(1)\n  }\n}\nclass Main {\n  public static void main(String[] args) {\n    System.out.println(new Ext().getX())\n  }\n}\n", "TY-TYP-0007"},
		{"extendsLombokUtility", "import lombok.experimental.UtilityClass\n\n@UtilityClass\nclass U {\n  int f() { return 1 }\n}\nclass Ext extends U {\n}\nclass Main {\n  public static void main(String[] args) {\n    System.out.println(U.f())\n  }\n}\n", "TY-TYP-0007"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tc.name+".teyru")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			res, err := driver.Compile([]string{path}, driver.Options{Out: filepath.Join(dir, "out"), Opt: "-O0"})
			if err == nil {
				t.Fatalf("expected failure")
			}
			if res == nil || res.Diags == nil {
				t.Fatalf("expected diagnostics, got %v", err)
			}
			if !strings.Contains(res.Diags.String(), tc.code) {
				t.Errorf("expected code %s in:\n%s", tc.code, res.Diags)
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
