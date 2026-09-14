// Package mod_test drives the module system through the compiler: a module
// whose program imports a package of a module the cache holds has to build and
// run with no flags, and a cached copy that is not the one teyru.sum records
// has to stop the build instead of being compiled.
//
// Nothing here reaches the network. The module cache is a temporary directory
// and the fetch is a copy out of tests/modules/fixtures, which is what
// mod.LocalFetcher exists for; TEYRUPATH is what points the compiler at it.
package mod_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/teyru-lang/Teyru/internal/driver"
	"github.com/teyru-lang/Teyru/internal/mod"
)

// root is the module path all of this is about, named once.
const (
	fixtureModule  = "example.com/greeting"
	fixtureVersion = "v0.1.0"
)

// repo joins a path in the repository. A test's working directory is the
// package it lives in, and the fixtures are where a reader expects them.
func repo(t *testing.T, elems ...string) string {
	t.Helper()
	if len(elems) > 0 && elems[0] == "tests" {
		if _, err := os.Stat(filepath.Join("..", "..", "tests", "programs")); err != nil {
			t.Fatal("tests/ is not checked out: the suite is the teyru-lang/tests submodule, run `git submodule update --init`")
		}
	}
	dir, err := filepath.Abs(filepath.Join(append([]string{"..", ".."}, elems...)...))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// fixtureCache points the compiler at a fresh module cache holding the fixture
// module, the way `teyru get` would have left it.
func fixtureCache(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv(mod.EnvPath, root)
	c, err := mod.Open()
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &mod.LocalFetcher{Root: repo(t, "tests", "modules", "fixtures")}
	if _, err := c.Fetch(fetcher, fixtureModule, fixtureVersion); err != nil {
		t.Fatal(err)
	}
	return root
}

// A compiler that cannot run is not a failing test: the module system is
// checked on the way to a binary, and a machine with no C compiler can still
// build the compiler.
func needCC(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		if _, err2 := exec.LookPath("gcc"); err2 != nil {
			t.Skip("no C compiler available")
		}
	}
}

// The whole point: a program that imports a package of a required module
// builds and runs, with no flags and no path told where anything is.
func TestModuleBuildAndRun(t *testing.T) {
	needCC(t)
	fixtureCache(t)
	dir := repo(t, "tests", "modules", "app")
	out := filepath.Join(t.TempDir(), "app")
	res, err := driver.Compile([]string{dir}, driver.Options{Out: out, Opt: "-O1"})
	if err != nil {
		t.Fatalf("compile failed: %v\n%s", err, res.Diags)
	}
	want, err := os.ReadFile(filepath.Join(dir, "expected"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(res.Exe)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run failed: %v\nstderr:\n%s", err, stderr.String())
	}
	if got := stdout.String(); got != string(want) {
		t.Errorf("output mismatch\n--- want ---\n%s--- got ---\n%s", want, got)
	}
}

// The same build with the module cache empty: the error names the module, the
// version and the command that fetches it, rather than failing somewhere later
// with a name the compiler could not resolve.
func TestModuleBuildWithoutTheModule(t *testing.T) {
	needCC(t)
	t.Setenv(mod.EnvPath, t.TempDir())
	dir := repo(t, "tests", "modules", "app")
	res, err := driver.Compile([]string{dir}, driver.Options{Out: filepath.Join(t.TempDir(), "app"), Opt: "-O0"})
	if err == nil {
		t.Fatal("a build with an empty module cache succeeded")
	}
	if res == nil || res.Diags == nil {
		t.Fatalf("no diagnostics: %v", err)
	}
	diags := res.Diags.String()
	if !strings.Contains(diags, "not in the module cache") || !strings.Contains(diags, fixtureModule+"@"+fixtureVersion) {
		t.Errorf("the error does not say what is missing:\n%s", diags)
	}
	if !strings.Contains(diags, "teyru get "+fixtureModule+"@"+fixtureVersion) {
		t.Errorf("the error does not say what to run:\n%s", diags)
	}
}

// A cached module whose bytes are not the ones teyru.sum records is the one
// failure that has to stop a build rather than be worked around.
func TestModuleChecksumMismatch(t *testing.T) {
	needCC(t)
	fixtureCache(t)
	dir := repo(t, "tests", "modules", "badsum")
	res, err := driver.Compile([]string{dir}, driver.Options{Out: filepath.Join(t.TempDir(), "badsum"), Opt: "-O0"})
	if err == nil {
		t.Fatal("a build against a mismatched checksum succeeded")
	}
	if res == nil || res.Diags == nil {
		t.Fatalf("no diagnostics: %v", err)
	}
	diags := res.Diags.String()
	want, err := os.ReadFile(filepath.Join(dir, "error"))
	if err != nil {
		t.Fatal(err)
	}
	if code := strings.TrimSpace(string(want)); !strings.Contains(diags, code) {
		t.Errorf("expected %s in:\n%s", code, diags)
	}
	if !strings.Contains(diags, "checksum mismatch") {
		t.Errorf("the error does not say what happened:\n%s", diags)
	}
}

// A requirement nothing imports is not an error, and it is not fetched either:
// a module file lists what the author might need, and a build that reached the
// network over an unused line would be a build nobody can run offline.
func TestUnusedRequirementDoesNotFailTheBuild(t *testing.T) {
	needCC(t)
	t.Setenv(mod.EnvPath, t.TempDir()) // an empty cache
	dir := t.TempDir()
	write(t, filepath.Join(dir, mod.ModuleFileName), "module example.com/app\n\nteyru 1\n\nrequire (\n\texample.com/absent v1.0.0\n)\n")
	write(t, filepath.Join(dir, "Main.teyru"), `class Main {
  public static void main(String[] args) {
    System.out.println("no modules involved")
  }
}
`)
	res, err := driver.Compile([]string{dir}, driver.Options{Out: filepath.Join(t.TempDir(), "app"), Opt: "-O1"})
	if err != nil {
		t.Fatalf("compile failed: %v\n%s", err, res.Diags)
	}
	cmd := exec.Command(res.Exe)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "no modules involved\n" {
		t.Errorf("output = %q", got)
	}
}

// Module mode must not disturb the imports that were never module imports: the
// prelude is compiled into every program and is spelled the way Java spells it.
func TestPreludeImportsSurviveModuleMode(t *testing.T) {
	needCC(t)
	fixtureCache(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, mod.ModuleFileName), "module example.com/app\n\nteyru 1\n\nrequire (\n\texample.com/greeting v0.1.0\n)\n")
	// the checksums of the module it requires, which a build checks on use
	sums, err := os.ReadFile(filepath.Join(repo(t, "tests", "modules", "app"), mod.SumFileName))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, mod.SumFileName), string(sums))
	write(t, filepath.Join(dir, "Main.teyru"), `import java.util.ArrayList
import java.util.List
import example.com.greeting.text

class Main {
  public static void main(String[] args) {
    List<String> xs = new ArrayList<String>()
    xs.add(Text.shout("a"))
    xs.add(Text.twice("b"))
    System.out.println(xs.get(0) + " " + xs.get(1) + " " + xs.size())
  }
}
`)
	res, err := driver.Compile([]string{dir}, driver.Options{Out: filepath.Join(t.TempDir(), "app"), Opt: "-O1"})
	if err != nil {
		t.Fatalf("compile failed: %v\n%s", err, res.Diags)
	}
	cmd := exec.Command(res.Exe)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "A! bb 2\n" {
		t.Errorf("output = %q", got)
	}
}

// Two packages of the program's own module are two packages, whatever they
// declare, because a package's identity is its import path.
func TestTwoPackagesOfOneModule(t *testing.T) {
	needCC(t)
	t.Setenv(mod.EnvPath, t.TempDir())
	dir := t.TempDir()
	write(t, filepath.Join(dir, mod.ModuleFileName), "module example.com/app\n\nteyru 1\n")
	write(t, filepath.Join(dir, "a", "A.teyru"), "package util\n\npublic class A {\n  public static String name() { return \"a\" }\n}\n")
	write(t, filepath.Join(dir, "b", "B.teyru"), "package util\n\npublic class B {\n  public static String name() { return \"b\" }\n}\n")
	write(t, filepath.Join(dir, "Main.teyru"), `import example.com.app.a
import example.com.app.b

class Main {
  public static void main(String[] args) {
    System.out.println(A.name() + B.name())
  }
}
`)
	res, err := driver.Compile([]string{dir}, driver.Options{Out: filepath.Join(t.TempDir(), "app"), Opt: "-O1"})
	if err != nil {
		t.Fatalf("compile failed: %v\n%s", err, res.Diags)
	}
	cmd := exec.Command(res.Exe)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "ab\n" {
		t.Errorf("output = %q", got)
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
