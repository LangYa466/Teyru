package mod

import (
	"os"
	"path/filepath"
	"testing"
)

// Module paths and version the fixtures are about, named once so a test that
// reads badly is a test that was changed without its reader in mind.
const (
	fixtureModule  = "example.com/greeting"
	fixtureVersion = "v0.1.0"
)

// testsDir answers with a path under tests/, and says what to do when the
// directory is empty: the suite is the teyru-lang/tests repository, mounted
// here as a submodule, and a clone that skipped it fails on a missing fixture
// file rather than on the submodule it never checked out.
//
// The external test package has its own copy of this -- a Go directory holds
// two test packages and they cannot share -- so the message is the same in
// both.
func testsDir(t *testing.T, elems ...string) string {
	t.Helper()
	if _, err := os.Stat(filepath.Join("..", "..", "tests", "programs")); err != nil {
		t.Fatal("tests/ is not checked out: the suite is the teyru-lang/tests submodule, run `git submodule update --init`")
	}
	dir, err := filepath.Abs(filepath.Join(append([]string{"..", "..", "tests"}, elems...)...))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// fixturesDir is the directory the local fetcher reads: the layout of a module
// cache, kept in the repository so that no test of the module system needs a
// network.
func fixturesDir(t *testing.T) string {
	t.Helper()
	dir := testsDir(t, "modules", "fixtures")
	if !IsModuleDir(filepath.Join(dir, fixtureModule+"@"+fixtureVersion)) {
		t.Fatalf("%s holds no %s@%s", dir, fixtureModule, fixtureVersion)
	}
	return dir
}

// moduleFixture is appDir and friends: a directory of tests/modules.
func moduleFixture(t *testing.T, name string) string {
	t.Helper()
	return testsDir(t, "modules", name)
}

// testCache points the module cache at a fresh directory and installs the
// fixture module in it, the way `teyru get` would have. Every test that
// resolves an import needs the cache to hold something.
func testCache(t *testing.T) *Cache {
	t.Helper()
	t.Setenv(EnvPath, t.TempDir())
	c, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fetch(&LocalFetcher{Root: fixturesDir(t)}, fixtureModule, fixtureVersion); err != nil {
		t.Fatal(err)
	}
	return c
}

// copyModule copies a module fixture into a temporary directory. `teyru mod
// tidy` rewrites what it is given, so every test of it works on a copy.
func copyModule(t *testing.T, name string) string {
	t.Helper()
	dst := t.TempDir()
	if err := copyTree(moduleFixture(t, name), dst); err != nil {
		t.Fatal(err)
	}
	return dst
}

// countFetcher records how often it was asked to fetch, which is how the tests
// tell "the cache already had it" from "it was fetched again".
type countFetcher struct {
	inner Fetcher
	calls int
	err   error
}

func (f *countFetcher) Fetch(module, version, dst string) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	return f.inner.Fetch(module, version, dst)
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
