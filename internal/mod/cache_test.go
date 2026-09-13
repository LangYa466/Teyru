package mod

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenUsesTheEnvironment(t *testing.T) {
	t.Setenv(EnvPath, "/nowhere/teyru")
	c, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if c.Root != "/nowhere/teyru" {
		t.Errorf("Root = %q", c.Root)
	}
	if got, want := c.ModRoot(), "/nowhere/teyru/pkg/mod"; got != want {
		t.Errorf("ModRoot = %q, want %q", got, want)
	}
	if got, want := c.Dir("example.com/a", "v1.0.0"), "/nowhere/teyru/pkg/mod/example.com/a@v1.0.0"; got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
	if got, want := c.PackageDir("example.com/a", "v1.0.0", "pkg/sub"), "/nowhere/teyru/pkg/mod/example.com/a@v1.0.0/pkg/sub"; got != want {
		t.Errorf("PackageDir = %q, want %q", got, want)
	}
	// The module root is the module itself, whether or not it is spelled.
	if got, want := c.PackageDir("example.com/a", "v1.0.0", ""), c.Dir("example.com/a", "v1.0.0"); got != want {
		t.Errorf("PackageDir = %q, want %q", got, want)
	}
}

func TestOpenWithoutEnvAndHome(t *testing.T) {
	t.Setenv(EnvPath, "")
	t.Setenv("HOME", t.TempDir())
	c, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(c.Root, filepath.Join(".teyru")) {
		t.Errorf("Root = %q, want the default under the home directory", c.Root)
	}
}

// A module version is immutable, so a second fetch of one the cache already
// holds can only produce a copy that differs -- and the checksum re-checked at
// every build is what proves the first was right.
func TestFetchSkipsWhatIsCached(t *testing.T) {
	c := testCache(t)
	f := &countFetcher{inner: &LocalFetcher{Root: fixturesDir(t)}}
	dir, err := c.Fetch(f, fixtureModule, fixtureVersion)
	if err != nil {
		t.Fatal(err)
	}
	if f.calls != 0 {
		t.Errorf("the cache fetched a module it already held (%d calls)", f.calls)
	}
	if dir != c.Dir(fixtureModule, fixtureVersion) {
		t.Errorf("Dir = %q", dir)
	}
	if !c.Has(fixtureModule, fixtureVersion) {
		t.Error("the cached module is not there")
	}
	if !HasSources(filepath.Join(dir, "text")) {
		t.Errorf("%s holds no sources", dir)
	}
}

func TestFetchInstallsIntoAFreshCache(t *testing.T) {
	t.Setenv(EnvPath, t.TempDir())
	c, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	f := &countFetcher{inner: &LocalFetcher{Root: fixturesDir(t)}}
	if _, err := c.Fetch(f, fixtureModule, fixtureVersion); err != nil {
		t.Fatal(err)
	}
	if f.calls != 1 {
		t.Errorf("fetched %d times, want 1", f.calls)
	}
	// The sources are there, and so is the module's own file, which is what
	// says what the module requires in turn.
	if !c.Has(fixtureModule, fixtureVersion) {
		t.Fatal("the module is not in the cache")
	}
	if !IsModuleDir(c.Dir(fixtureModule, fixtureVersion)) {
		t.Errorf("%s is not a module directory", c.Dir(fixtureModule, fixtureVersion))
	}
	// The staging directory a fetch happens in is gone: a cache entry appears
	// complete or not at all.
	entries, err := os.ReadDir(filepath.Join(c.ModRoot(), "cache"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("the staging directory %s was left behind", e.Name())
	}
}

func TestFetchFailureLeavesNoEntry(t *testing.T) {
	t.Setenv(EnvPath, t.TempDir())
	c, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	f := &countFetcher{inner: &LocalFetcher{Root: fixturesDir(t)}, err: errors.New("no network")}
	if _, err := c.Fetch(f, fixtureModule, fixtureVersion); err == nil {
		t.Fatal("a failed fetch reported success")
	}
	if c.Has(fixtureModule, fixtureVersion) {
		t.Error("a failed fetch left a module in the cache")
	}
	if got, err := c.List(); err != nil || len(got) != 0 {
		t.Errorf("List = %v, %v", got, err)
	}
}

// A module path is also a directory name in the cache, so the check that a
// fetch makes has to be the one that keeps the path inside it.
func TestFetchRefusesAPathOutsideTheCache(t *testing.T) {
	t.Setenv(EnvPath, t.TempDir())
	c, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	f := &LocalFetcher{Root: fixturesDir(t)}
	for _, mod := range []string{"../evil", "example.com/../../evil", "/etc/passwd", ""} {
		if _, err := c.Fetch(f, mod, "v1.0.0"); err == nil {
			t.Errorf("fetching %q was accepted", mod)
		}
	}
	outside := filepath.Join(filepath.Dir(c.Root), "evil@v1.0.0")
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("%s was created outside the cache", outside)
	}
}

// List reads the cache the way `teyru mod tidy` has to: from the directory
// names alone, without a network and without reading a module file.
func TestList(t *testing.T) {
	c := testCache(t)
	// a second version of the same module, and a second module
	if err := copyTree(c.Dir(fixtureModule, fixtureVersion), c.Dir(fixtureModule, "v0.2.0")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(c.Dir(fixtureModule, fixtureVersion), c.Dir("example.com/other", "v1.0.0")); err != nil {
		t.Fatal(err)
	}
	got, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, in := range got {
		keys = append(keys, in.Module+"@"+in.Text)
	}
	want := []string{"example.com/greeting@v0.1.0", "example.com/greeting@v0.2.0", "example.com/other@v1.0.0"}
	if strings.Join(keys, " ") != strings.Join(want, " ") {
		t.Errorf("List = %v, want %v", keys, want)
	}
	if got[0].Dir != c.Dir(fixtureModule, fixtureVersion) {
		t.Errorf("Dir = %q", got[0].Dir)
	}
}

func TestLocalFetcherReportsAMissingModule(t *testing.T) {
	f := &LocalFetcher{Root: fixturesDir(t)}
	err := f.Fetch("example.com/absent", "v1.0.0", t.TempDir())
	if err == nil {
		t.Fatal("fetching a module that is not there succeeded")
	}
	if !strings.Contains(err.Error(), "example.com/absent@v1.0.0") {
		t.Errorf("the error does not name the module: %v", err)
	}
}

// The command line's fetcher is a git clone of the module path at the version
// tag, with $TEYRU_GIT_URL standing in for a mirror.
func TestGitFetcherURL(t *testing.T) {
	t.Setenv(EnvGitURL, "")
	f := NewGitFetcher()
	if got, want := f.url("example.com/a"), "https://example.com/a"; got != want {
		t.Errorf("url = %q, want %q", got, want)
	}
	t.Setenv(EnvGitURL, "file:///srv/mirror/")
	f = NewGitFetcher()
	if got, want := f.url("example.com/a"), "file:///srv/mirror/example.com/a"; got != want {
		t.Errorf("url = %q, want %q", got, want)
	}
}
