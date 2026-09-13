package mod

import (
	"path/filepath"
	"strings"
	"testing"
)

// tidy returns what tidy said, for the tests to read.
func tidy(t *testing.T, dir string) []string {
	t.Helper()
	g, err := LoadGraph(dir)
	if err != nil {
		t.Fatal(err)
	}
	notes, err := Tidy(g)
	if err != nil {
		t.Fatalf("%v (after %v)", err, notes)
	}
	return notes
}

func hasNote(notes []string, want string) bool {
	for _, n := range notes {
		if strings.Contains(n, want) {
			return true
		}
	}
	return false
}

// A module that already agrees with its sources is not rewritten: `teyru mod
// tidy` running twice in a row is not a diff.
func TestTidyLeavesAConsistentModuleAlone(t *testing.T) {
	testCache(t)
	app := copyModule(t, "app")
	modBefore := readFile(t, filepath.Join(app, ModuleFileName))
	sumBefore := readFile(t, filepath.Join(app, SumFileName))
	if notes := tidy(t, app); len(notes) != 0 {
		t.Errorf("tidy of a consistent module said:\n%s", strings.Join(notes, "\n"))
	}
	if got := readFile(t, filepath.Join(app, ModuleFileName)); got != modBefore {
		t.Errorf("%s changed:\n--- before ---\n%s--- after ---\n%s", ModuleFileName, modBefore, got)
	}
	if got := readFile(t, filepath.Join(app, SumFileName)); got != sumBefore {
		t.Errorf("%s changed:\n--- before ---\n%s--- after ---\n%s", SumFileName, sumBefore, got)
	}
}

func TestTidyDropsWhatNothingImports(t *testing.T) {
	testCache(t)
	app := copyModule(t, "app")
	writeFile(t, filepath.Join(app, "Main.teyru"), `class Main {
  public static void main(String[] args) {
    System.out.println("no imports here")
  }
}
`)
	notes := tidy(t, app)
	if !hasNote(notes, "dropped requirement example.com/greeting v0.1.0") {
		t.Errorf("tidy did not report the drop:\n%s", strings.Join(notes, "\n"))
	}
	want := "module example.com/app\n\nteyru 1\n"
	if got := readFile(t, filepath.Join(app, ModuleFileName)); got != want {
		t.Errorf("--- want ---\n%s--- got ---\n%s", want, got)
	}
	// The checksum of a module nothing requires is not left behind either: the
	// file covers the build list and nothing else.
	if got := readFile(t, filepath.Join(app, SumFileName)); strings.Contains(got, "greeting") {
		t.Errorf("%s still covers the dropped module:\n%s", SumFileName, got)
	}
	if !hasNote(notes, "dropped the "+SumFileName+" entry for example.com/greeting v0.1.0") {
		t.Errorf("tidy dropped the checksum without saying so:\n%s", strings.Join(notes, "\n"))
	}
	// ...and a second run has nothing left to do.
	if notes := tidy(t, app); len(notes) != 0 {
		t.Errorf("tidy of a tidied module said:\n%s", strings.Join(notes, "\n"))
	}
}

func TestTidyAddsWhatTheSourcesImport(t *testing.T) {
	c := testCache(t)
	app := copyModule(t, "app")
	writeFile(t, filepath.Join(app, ModuleFileName), "module example.com/app\n\nteyru 1\n")
	writeFile(t, filepath.Join(app, SumFileName), "")
	notes := tidy(t, app)
	if !hasNote(notes, "added requirement example.com/greeting v0.1.0") {
		t.Errorf("tidy did not report the addition:\n%s", strings.Join(notes, "\n"))
	}
	if !hasNote(notes, "recorded the checksum of example.com/greeting v0.1.0") {
		t.Errorf("tidy did not report the checksum:\n%s", strings.Join(notes, "\n"))
	}
	want := "module example.com/app\n\nteyru 1\n\nrequire (\n\texample.com/greeting v0.1.0\n)\n"
	if got := readFile(t, filepath.Join(app, ModuleFileName)); got != want {
		t.Errorf("--- want ---\n%s--- got ---\n%s", want, got)
	}
	// The module the require was added for now builds: the checksums tidy wrote
	// are the ones a build checks, not placeholders.
	sums, err := LoadSum(filepath.Join(app, SumFileName))
	if err != nil {
		t.Fatal(err)
	}
	tree, modFile, err := TreeHash(c.Dir(fixtureModule, fixtureVersion))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := sums.Lookup(fixtureModule, fixtureVersion, false); got != tree {
		t.Errorf("tree hash = %q, want %q", got, tree)
	}
	if got, _ := sums.Lookup(fixtureModule, fixtureVersion, true); got != modFile {
		t.Errorf("module file hash = %q, want %q", got, modFile)
	}
	g, err := LoadGraph(app)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.Resolve("example.com.greeting.text"); err != nil {
		t.Errorf("the tidied module does not resolve: %v", err)
	}
}

// A requirement the cache cannot satisfy is reported, never fetched: `teyru mod
// tidy` has to be runnable in a sandbox and readable in a review, and a command
// that quietly reaches the network is neither.
func TestTidyReportsWhatTheCacheCannotSatisfy(t *testing.T) {
	t.Setenv(EnvPath, t.TempDir()) // an empty cache
	app := copyModule(t, "app")
	sumBefore := readFile(t, filepath.Join(app, SumFileName))
	modBefore := readFile(t, filepath.Join(app, ModuleFileName))
	notes := tidy(t, app)
	if !hasNote(notes, "module example.com/greeting@v0.1.0 is not in the cache") {
		t.Errorf("tidy did not report the missing module:\n%s", strings.Join(notes, "\n"))
	}
	if !hasNote(notes, "teyru get example.com/greeting@v0.1.0") {
		t.Errorf("tidy did not say what to run:\n%s", strings.Join(notes, "\n"))
	}
	// Nothing was fetched and nothing was dropped: the module file still says
	// what it said, and its recorded checksum is kept for the fetch that is
	// still missing.
	if got := readFile(t, filepath.Join(app, ModuleFileName)); got != modBefore {
		t.Errorf("%s changed:\n%s", ModuleFileName, got)
	}
	if got := readFile(t, filepath.Join(app, SumFileName)); got != sumBefore {
		t.Errorf("%s changed:\n--- before ---\n%s--- after ---\n%s", SumFileName, sumBefore, got)
	}
}

// An import of a module the cache holds but no require names is added at the
// version the cache can satisfy it with; a version the require already names is
// never raised behind the author's back.
func TestTidyKeepsARecordedVersion(t *testing.T) {
	c := testCache(t)
	app := copyModule(t, "app")
	// a newer version of the same module, which tidy must not select on its own
	if err := copyTree(c.Dir(fixtureModule, fixtureVersion), c.Dir(fixtureModule, "v0.2.0")); err != nil {
		t.Fatal(err)
	}
	notes := tidy(t, app)
	if !hasNote(notes, "kept the requirement example.com/greeting v0.1.0") {
		t.Errorf("tidy did not report the version it kept:\n%s", strings.Join(notes, "\n"))
	}
	want := "module example.com/app\n\nteyru 1\n\nrequire (\n\texample.com/greeting v0.1.0\n)\n"
	if got := readFile(t, filepath.Join(app, ModuleFileName)); got != want {
		t.Errorf("--- want ---\n%s--- got ---\n%s", want, got)
	}
	// A module the cache holds, that no require names, is added at the highest
	// version the cache has.
	writeFile(t, filepath.Join(app, "Main.teyru"), `import example.com.other.pkg

class Main {
  public static void main(String[] args) {
    System.out.println("other")
  }
}
`)
	other := c.Dir("example.com/other", "v0.1.0")
	writeFile(t, filepath.Join(other, ModuleFileName), "module example.com/other\n\nteyru 1\n")
	writeFile(t, filepath.Join(other, "pkg", "Pkg.teyru"), "package pkg\n\npublic class Pkg {}\n")
	notes = tidy(t, app)
	if !hasNote(notes, "added requirement example.com/other v0.1.0") {
		t.Errorf("tidy did not add the module the cache holds:\n%s", strings.Join(notes, "\n"))
	}
	// An import of a module path the cache does not hold at all is reported and
	// nothing is invented for it.
	writeFile(t, filepath.Join(app, "Main.teyru"), "import example.com.absent.pkg\n\nclass Main {\n  public static void main(String[] args) {\n    System.out.println(\"absent\")\n  }\n}\n")
	notes = tidy(t, app)
	if !hasNote(notes, "example.com.absent.pkg is imported but no module the cache holds provides it") {
		t.Errorf("tidy did not report the unresolvable import:\n%s", strings.Join(notes, "\n"))
	}
}

// An import inside a string or a comment is not an import, and a requirement
// dropped because of one would break the build the file belongs to.
func TestModuleImportsReadsTheParser(t *testing.T) {
	app := filepath.Join(t.TempDir(), "app")
	writeFile(t, filepath.Join(app, ModuleFileName), "module example.com/app\n\nteyru 1\n")
	writeFile(t, filepath.Join(app, "Main.teyru"), `// import example.com/commented.out
class Main {
  public static void main(String[] args) {
    System.out.println("import example.com.in.a.string")
  }
}
`)
	writeFile(t, filepath.Join(app, "Real.teyru"), "import example.com/real.pkg\n\nclass Real {}\n")
	// A nested module is a different module: its imports are not this one's.
	writeFile(t, filepath.Join(app, "nested", ModuleFileName), "module example.com/nested\n\nteyru 1\n")
	writeFile(t, filepath.Join(app, "nested", "Other.teyru"), "import example.com.nested.pkg\n\nclass Other {}\n")
	// A hidden directory holds tool state, not sources.
	writeFile(t, filepath.Join(app, ".cache", "Hidden.teyru"), "import example.com.hidden.pkg\n\nclass Hidden {}\n")

	m, err := LoadModule(app)
	if err != nil {
		t.Fatal(err)
	}
	imports, err := ModuleImports(m)
	if err != nil {
		t.Fatal(err)
	}
	// The paths are in the spelling the parser read -- the module path folded
	// onto dots -- because that is the spelling resolution takes.
	want := "example.com.real.pkg"
	if strings.Join(imports, " ") != want {
		t.Errorf("ModuleImports = %v, want %v", imports, want)
	}
}
