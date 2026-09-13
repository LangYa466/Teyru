package mod

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindModuleDirWalksUp(t *testing.T) {
	root := t.TempDir()
	if err := New("example.com/app").Write(filepath.Join(root, ModuleFileName)); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok, err := FindModuleDir(sub)
	if err != nil || !ok {
		t.Fatalf("FindModuleDir(%s) = %q, %v, %v", sub, got, ok, err)
	}
	if got != root {
		t.Errorf("dir = %q, want %q", got, root)
	}
	// A directory with no teyru.mod above it is not an error: the compiler has
	// always been usable on one file with no project around it.
	if _, ok, err := FindModuleDir(t.TempDir()); err != nil || ok {
		t.Errorf("a directory with no module: ok = %v, err = %v", ok, err)
	}
}

// A build resolves an import of a required module out of the cache with no
// flags at all, and the identity it gives the package is its import path -- not
// the package name its own files declare.
func TestResolveFromTheCache(t *testing.T) {
	c := testCache(t)
	g, err := LoadGraph(moduleFixture(t, "app"))
	if err != nil {
		t.Fatal(err)
	}
	if g.Main.Path != "example.com/app" {
		t.Fatalf("main module = %q", g.Main.Path)
	}
	if got := g.ModulePaths(); len(got) != 2 || got[0] != "example.com/app" || got[1] != fixtureModule {
		t.Fatalf("build list = %v", got)
	}

	// The import as the parser read it: the module path folded onto dots.
	pkg, isModule, err := g.Resolve("example.com.greeting.text")
	if err != nil {
		t.Fatal(err)
	}
	if !isModule {
		t.Fatal("a module import was left to the compiler's own resolution")
	}
	if pkg.ImportPath != "example.com/greeting/text" {
		t.Errorf("ImportPath = %q", pkg.ImportPath)
	}
	if pkg.Canonical() != "example.com/greeting/text" {
		t.Errorf("Canonical = %q", pkg.Canonical())
	}
	if len(pkg.Tail) != 0 {
		t.Errorf("Tail = %v, want none: the import names the package", pkg.Tail)
	}
	if want := c.PackageDir(fixtureModule, fixtureVersion, "text"); pkg.Dir != want {
		t.Errorf("Dir = %q, want %q", pkg.Dir, want)
	}
	if pkg.Module != fixtureModule || pkg.Version != fixtureVersion || pkg.Main {
		t.Errorf("pkg = %+v", pkg)
	}

	// A single-type import names one type of the package, and the package is
	// still what gets loaded.
	pkg, _, err = g.Resolve("example.com.greeting.text.Text")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.ImportPath != "example.com/greeting/text" || len(pkg.Tail) != 1 || pkg.Tail[0] != "Text" {
		t.Errorf("pkg = %+v", pkg)
	}

	// The prelude and the JDK spellings are not module paths at all, and are
	// left to the checker.
	for _, imp := range []string{"java.util.List", "tyru.String", "com.example.Foo"} {
		if pkg, isModule, err := g.Resolve(imp); err != nil || isModule {
			t.Errorf("Resolve(%q) = %v, %v, %v", imp, pkg, isModule, err)
		}
	}

	// A path that names a module the build does not require says so, and says
	// what to do about it.
	_, _, err = g.Resolve("example.com.absent.pkg")
	if err == nil || !strings.Contains(err.Error(), "cannot resolve") {
		t.Errorf("Resolve of an unknown module = %v", err)
	}
}

// An import inside a module the build already holds, spelled with the package
// of this module, is a package of this module and not a requirement.
func TestResolveOwnPackages(t *testing.T) {
	testCache(t)
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	writeFile(t, filepath.Join(app, ModuleFileName), "module example.com/app\n\nteyru 1\n")
	writeFile(t, filepath.Join(app, "sub", "Sub.teyru"), "package sub\npublic class Sub {}\n")
	g, err := LoadGraph(app)
	if err != nil {
		t.Fatal(err)
	}
	pkg, isModule, err := g.Resolve("example.com.app.sub")
	if err != nil {
		t.Fatal(err)
	}
	if !isModule {
		t.Fatal("a package of the main module was not resolved")
	}
	if pkg.ImportPath != "example.com/app/sub" || !pkg.Main {
		t.Errorf("pkg = %+v", pkg)
	}
	if pkg.Dir != filepath.Join(app, "sub") {
		t.Errorf("Dir = %q", pkg.Dir)
	}
}

func TestResolveNotCached(t *testing.T) {
	t.Setenv(EnvPath, t.TempDir()) // an empty cache
	g, err := LoadGraph(moduleFixture(t, "app"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = g.Resolve("example.com.greeting.text")
	var notCached *NotCachedError
	if !errors.As(err, &notCached) {
		t.Fatalf("Resolve = %v, want a NotCachedError", err)
	}
	// The message has to name the command that fixes it.
	if !strings.Contains(err.Error(), "teyru get example.com/greeting@v0.1.0") {
		t.Errorf("the error does not say what to run: %v", err)
	}
	// A requirement nothing imports is not an error: a module file lists what
	// the author might need, and a build should not fail over an unused line.
	if _, err := os.Stat(g.Cache.Dir(fixtureModule, fixtureVersion)); err == nil {
		t.Fatal("the cache was not empty")
	}
}

func TestResolveMissingChecksum(t *testing.T) {
	testCache(t)
	app := copyModule(t, "app")
	// The same module, with the checksum file the build needs emptied.
	writeFile(t, filepath.Join(app, SumFileName), "")
	g, err := LoadGraph(app)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = g.Resolve("example.com.greeting.text")
	var missing *MissingSumError
	if !errors.As(err, &missing) {
		t.Fatalf("Resolve = %v, want a MissingSumError", err)
	}
	if !strings.Contains(err.Error(), SumFileName) {
		t.Errorf("the error does not name the checksum file: %v", err)
	}
}

// The one error in the module system that names a security problem: the same
// version, with different contents than the ones the module file was written
// against. The build has to stop rather than compile them.
func TestResolveChecksumMismatch(t *testing.T) {
	testCache(t)
	app := copyModule(t, "app")
	sums := readFile(t, filepath.Join(app, SumFileName))
	writeFile(t, filepath.Join(app, SumFileName), strings.Replace(sums, "h1:", "h1:AAAA", 1))
	g, err := LoadGraph(app)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = g.Resolve("example.com.greeting.text")
	var mismatch *SumMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("Resolve = %v, want a SumMismatchError", err)
	}
	if mismatch.Want == mismatch.Got {
		t.Errorf("the two hashes are the same: %v", mismatch)
	}
	if !strings.Contains(err.Error(), "nothing was built") {
		t.Errorf("the error does not say the build stopped: %v", err)
	}
}

func TestMainPackagePath(t *testing.T) {
	testCache(t)
	g, err := LoadGraph(moduleFixture(t, "app"))
	if err != nil {
		t.Fatal(err)
	}
	root := g.Main.Dir
	cases := map[string]string{
		root:                     "example.com/app",
		filepath.Join(root, "x"): "example.com/app/x",
	}
	for dir, want := range cases {
		got, ok := g.MainPackagePath(dir)
		if !ok || got != want {
			t.Errorf("MainPackagePath(%q) = %q, %v, want %q", dir, got, ok, want)
		}
	}
	// A directory outside the module has no import path, whatever it holds.
	if _, ok := g.MainPackagePath(filepath.Dir(root)); ok {
		t.Error("a directory outside the module was given an import path")
	}
}

// A package is one directory: a subdirectory is another package, and a
// dependency's `cmd` directory holding a second entry point must not be
// compiled into a program that never asked for it.
func TestPackageFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "b.teyru"), "class B {}\n")
	writeFile(t, filepath.Join(dir, "a.teyru"), "class A {}\n")
	writeFile(t, filepath.Join(dir, "notes.txt"), "not a source\n")
	writeFile(t, filepath.Join(dir, "sub", "c.teyru"), "class C {}\n")
	got, err := PackageFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "a.teyru"), filepath.Join(dir, "b.teyru")}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("PackageFiles = %v, want %v", got, want)
	}
	if !HasSources(dir) || HasSources(filepath.Join(dir, "sub", "empty")) {
		t.Error("HasSources disagrees with PackageFiles")
	}
	// What follows the package is a type name, not a directory: `a.b.Widget`
	// names Widget in the package at the module root.
	rel, tail, err := findPackage(dir, []string{"Widget"})
	if err != nil || rel != "" || len(tail) != 1 || tail[0] != "Widget" {
		t.Errorf("findPackage = %q, %v, %v", rel, tail, err)
	}
	// A directory that is there but holds no sources is reported as such...
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := findPackage(dir, []string{"empty"}); err == nil || !strings.Contains(err.Error(), "holds no") {
		t.Errorf("findPackage of a directory with no sources = %v", err)
	}
	// ...and a module whose root holds no sources has no package at all.
	if _, _, err := findPackage(t.TempDir(), []string{"missing"}); err == nil || !strings.Contains(err.Error(), "no such package") {
		t.Errorf("findPackage in an empty directory = %v", err)
	}
}

// One build cannot hold two versions of one module: a requirement that raises
// the version of a module a package was already compiled from is reported
// instead of silently changing what the program was built against.
func TestVersionConflict(t *testing.T) {
	g := &Graph{deps: map[string]*Dep{}, avail: map[string][]Installed{}, names: []string{"example.com/a"}}
	g.deps["example.com/a"] = &Dep{Path: "example.com/a", Version: "v1.0.0", used: true}
	if err := g.pendingConflict(); err != nil {
		t.Fatalf("a graph with no conflict reported one: %v", err)
	}
	g.require("example.com/a", "v1.1.0")
	err := g.pendingConflict()
	if err == nil {
		t.Fatal("raising the version of a used module was not reported")
	}
	if !strings.Contains(err.Error(), "v1.0.0") || !strings.Contains(err.Error(), "v1.1.0") {
		t.Errorf("the error does not name both versions: %v", err)
	}
	// reported once: a broken build should not say the same thing twice
	if err := g.pendingConflict(); err != nil {
		t.Errorf("the conflict was reported twice: %v", err)
	}
	// a higher requirement for a module nothing uses yet is selection, not a
	// conflict: the highest version asked for wins
	g.require("example.com/b", "v1.0.0")
	g.require("example.com/b", "v1.2.0")
	if err := g.pendingConflict(); err != nil {
		t.Errorf("minimal version selection was reported as a conflict: %v", err)
	}
	if got := g.deps["example.com/b"].Version; got != "v1.2.0" {
		t.Errorf("selected version = %s, want v1.2.0", got)
	}
	// and a lower one never lowers what is already selected
	g.require("example.com/b", "v1.1.0")
	if got := g.deps["example.com/b"].Version; got != "v1.2.0" {
		t.Errorf("selected version = %s, want v1.2.0", got)
	}
}
