package mod

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSum(t *testing.T) {
	src := "example.com/b v1.0.0 h1:bbbb\n" +
		"example.com/a v1.2.3/teyru.mod h1:cc\n" +
		"example.com/a v1.2.3 h1:aa\n" +
		"// a comment is not an entry\n"
	s, err := ParseSum(SumFileName, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Lookup("example.com/a", "v1.2.3", false); !ok || got != "h1:aa" {
		t.Errorf("tree hash = %q, %v", got, ok)
	}
	if got, ok := s.Lookup("example.com/a", "v1.2.3", true); !ok || got != "h1:cc" {
		t.Errorf("module file hash = %q, %v", got, ok)
	}
	if _, ok := s.Lookup("example.com/a", "v1.2.4", false); ok {
		t.Error("a version with no entry was found")
	}
	// The module file's line sorts after the tree's, so a changed module reads
	// as one entry in a diff rather than two.
	want := "example.com/a v1.2.3 h1:aa\n" +
		"example.com/a v1.2.3/teyru.mod h1:cc\n" +
		"example.com/b v1.0.0 h1:bbbb\n"
	if got := string(s.Format()); got != want {
		t.Errorf("--- want ---\n%s--- got ---\n%s", want, got)
	}
	if got := s.Modules(); len(got) != 2 || got[0] != "example.com/a@v1.2.3" || got[1] != "example.com/b@v1.0.0" {
		t.Errorf("Modules = %v", got)
	}
}

// A checksum line that does not parse has to stop the file: a build that
// ignored the line it could not read is a build that skipped a check.
func TestParseSumRejectsWhatItDoesNotKnow(t *testing.T) {
	cases := map[string]string{
		"two fields":      "example.com/a v1.0.0\n",
		"no hash":         "example.com/a v1.0.0 h1:aa extra\n",
		"not a hash":      "example.com/a v1.0.0 deadbeef\n",
		"not a path":      "exam!ple.com/a v1.0.0 h1:aa\n",
		"not a version":   "example.com/a 1.0.0 h1:aa\n",
		"bad modfile tag": "example.com/a v1.0.0/go.mod h1:aa\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSum(SumFileName, []byte(src)); err == nil {
				t.Fatalf("parsed %q", src)
			} else if !strings.Contains(err.Error(), SumFileName+":") {
				t.Errorf("error does not name the file and line: %v", err)
			}
		})
	}
}

// A missing checksum file is an empty one: a module with no requirements has
// nothing to record, and it is the build that decides what a missing line
// means.
func TestLoadSumOfAMissingFile(t *testing.T) {
	s, err := LoadSum(filepath.Join(t.TempDir(), SumFileName))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Modules()) != 0 {
		t.Errorf("Modules = %v", s.Modules())
	}
}

func TestSumSetAndDrop(t *testing.T) {
	s := &Sum{}
	s.Set("example.com/a", "v1.0.0", "h1:one", false)
	s.Set("example.com/b", "v1.0.0", "h1:two", false)
	// a second Set of the same module version replaces, never duplicates
	s.Set("example.com/a", "v1.0.0", "h1:again", false)
	if got, _ := s.Lookup("example.com/a", "v1.0.0", false); got != "h1:again" {
		t.Errorf("hash = %q", got)
	}
	if len(s.lines) != 2 {
		t.Errorf("lines = %d, want 2", len(s.lines))
	}
	s.DropModule("example.com/a", "v1.0.0")
	if _, ok := s.Lookup("example.com/a", "v1.0.0", false); ok {
		t.Error("the dropped entry is still there")
	}
	if _, ok := s.Lookup("example.com/b", "v1.0.0", false); !ok {
		t.Error("dropping one module version dropped another")
	}
}

// A tree hash has to be a function of the sources: the same bytes hash the
// same however they were reached, and one changed byte changes it. It also has
// to ignore the VCS directory a fetch leaves behind, or a module fetched by a
// newer git than the one someone else used would not verify.
func TestHashDir(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, text string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.teyru", "class A {}\n")
	write("sub/b.teyru", "class B {}\n")
	first, err := HashDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := HashDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Errorf("hashing the same tree twice differs: %s != %s", first, again)
	}
	// The VCS directory of a fresh clone is not part of the module.
	write(".git/HEAD", "ref: refs/heads/main\n")
	write(".git/objects/ab/cdef", "not a real object\n")
	if got, _ := HashDir(dir); got != first {
		t.Errorf("a .git directory changed the hash: %s != %s", got, first)
	}
	write("a.teyru", "class A { } // one space\n")
	changed, err := HashDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Error("a changed source file did not change the hash")
	}
	if !strings.HasPrefix(changed, "h1:") {
		t.Errorf("hash = %q, want an h1: prefix", changed)
	}
	// Renaming a file changes the hash even when its bytes do not: the hash
	// covers the tree, not just its contents.
	if err := os.Rename(filepath.Join(dir, "a.teyru"), filepath.Join(dir, "z.teyru")); err != nil {
		t.Fatal(err)
	}
	renamed, err := HashDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if renamed == changed {
		t.Error("a renamed file did not change the hash")
	}
}

// TestTeyruSumMatchesFixture keeps tests/modules/app/teyru.sum in step with
// the module it covers. Editing the fixture without re-running this leaves the
// build failing on TY-IO-0103, so the failure prints the lines to paste.
func TestTeyruSumMatchesFixture(t *testing.T) {
	dir := filepath.Join("..", "..", "tests", "modules", "fixtures", "example.com", "greeting@v0.1.0")
	tree, modFile, err := TreeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if modFile == "" {
		t.Fatalf("%s ships no %s", dir, ModuleFileName)
	}
	sums, err := LoadSum(filepath.Join("..", "..", "tests", "modules", "app", SumFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		modFile bool
		hash    string
		suffix  string
	}{{false, tree, ""}, {true, modFile, "/" + ModuleFileName}} {
		got, ok := sums.Lookup("example.com/greeting", "v0.1.0", want.modFile)
		if !ok || got != want.hash {
			t.Errorf("example.com/greeting v0.1.0: %s records %q, the fixture hashes to %s\n"+
				"write this line into tests/modules/app/%s:\n%s",
				SumFileName, got, want.hash, SumFileName,
				"example.com/greeting v0.1.0"+want.suffix+" "+want.hash)
		}
	}
}
