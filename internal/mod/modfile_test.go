package mod

import (
	"strings"
	"testing"
)

// canonical is the module file the documentation shows: module directive,
// language version, one require block.
const canonical = `module github.com/langya/myapp

teyru 1

require (
	github.com/langya/gson v0.1.0
)
`

func TestParseCanonical(t *testing.T) {
	f, err := Parse("teyru.mod", []byte(canonical))
	if err != nil {
		t.Fatal(err)
	}
	if f.Module != "github.com/langya/myapp" {
		t.Errorf("module = %q", f.Module)
	}
	if f.Lang != 1 {
		t.Errorf("teyru version = %d", f.Lang)
	}
	if len(f.Requires) != 1 || f.Requires[0].Path != "github.com/langya/gson" || f.Requires[0].Version != "v0.1.0" {
		t.Errorf("requires = %+v", f.Requires)
	}
	if got := string(f.Format()); got != canonical {
		t.Errorf("Format is not the input\n--- want ---\n%s--- got ---\n%s", canonical, got)
	}
}

// A formatter that only produces the same bytes for input it just produced is
// stable; one that also leaves an arbitrary input alone would be a no-op. What
// is required here is the first, and that the canonical form is what the
// second pass sees.
func TestFormatIsAFixedPoint(t *testing.T) {
	messy := []string{
		// a single-line require, expanded
		"module example.com/a\nrequire github.com/langya/b v1.2.3\n",
		// unsorted, unaligned, extra blank lines and a duplicate directive
		"module example.com/a\n\n\nteyru 1\n\nrequire (\n\texample.com/z v0.1.0\n\n\texample.com/aa v2.0.0\n)\n",
		// comments before and inside the block
		"// what this is\nmodule example.com/a\n\n// why\nteyru 1\n\nrequire (\n\t// the parser\n\texample.com/z v0.1.0 // pinned\n)\n",
	}
	for _, in := range messy {
		f, err := Parse("teyru.mod", []byte(in))
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		once := f.Format()
		again, err := Parse("teyru.mod", once)
		if err != nil {
			t.Fatalf("%q formatted to unparsable %q: %v", in, once, err)
		}
		if got := again.Format(); string(got) != string(once) {
			t.Errorf("Format is not idempotent\n--- once ---\n%s--- twice ---\n%s", once, got)
		}
	}
}

func TestFormatSortsAndAligns(t *testing.T) {
	f, err := Parse("teyru.mod", []byte("module example.com/a\n\nteyru 1\n\nrequire (\n\tgithub.com/langya/b v1.0.0\n\tgithub.com/langya/aaa v2.0.0\n)\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := "module example.com/a\n\nteyru 1\n\nrequire (\n\tgithub.com/langya/aaa v2.0.0\n\tgithub.com/langya/b   v1.0.0\n)\n"
	if got := string(f.Format()); got != want {
		t.Errorf("--- want ---\n%q\n--- got ---\n%q", want, got)
	}
}

func TestAddRequire(t *testing.T) {
	f, err := Parse("teyru.mod", []byte(canonical))
	if err != nil {
		t.Fatal(err)
	}
	if !f.Add("github.com/langya/new", "v1.0.0") {
		t.Fatal("Add reported no change for a new require")
	}
	if f.Add("github.com/langya/new", "v1.0.0") {
		t.Error("Add reported a change for a require that is already there")
	}
	if !f.Add("github.com/langya/new", "v1.1.0") {
		t.Error("Add reported no change for a new version")
	}
	if got := f.Get("github.com/langya/new").Version; got != "v1.1.0" {
		t.Errorf("version = %s", got)
	}
	out := string(f.Format())
	if strings.Index(out, "github.com/langya/gson") > strings.Index(out, "github.com/langya/new") {
		t.Errorf("requires are not sorted:\n%s", out)
	}
	if !f.Remove("github.com/langya/gson") {
		t.Error("Remove reported nothing to remove")
	}
	if f.Remove("github.com/langya/gson") {
		t.Error("Remove reported a change the second time")
	}
	if f.Get("github.com/langya/gson") != nil {
		t.Error("the removed require is still there")
	}
	if err := f.Write(t.TempDir() + "/teyru.mod"); err != nil {
		t.Fatal(err)
	}
}

// A directive the compiler does not know has to stop the parse. Quietly
// dropping it would let `teyru mod tidy` rewrite a file into one that means
// something else than it said.
func TestParseRejectsWhatItDoesNotKnow(t *testing.T) {
	cases := map[string]string{
		"unknown directive": "module example.com/a\nexclude github.com/langya/b v1.0.0\n",
		"missing module":    "teyru 1\n",
		"bad version":       "module example.com/a\nrequire example.com/b 1.0.0\n",
		"bad path":          "module example.com/a\nrequire not a path v1.0.0\n",
		"unclosed block":    "module example.com/a\nrequire (\n\tgithub.com/langya/b v1.0.0\n",
		"teyru not a count": "module example.com/a\nteyru one\n",
		"short require":     "module example.com/a\nrequire example.com/b\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse("teyru.mod", []byte(src)); err == nil {
				t.Fatalf("parsed %q", src)
			} else if !strings.Contains(err.Error(), "teyru.mod:") {
				t.Errorf("error does not name the file and line: %v", err)
			}
		})
	}
}

func TestNewIsCanonical(t *testing.T) {
	want := "module example.com/app\n\nteyru 1\n"
	if got := string(New("example.com/app").Format()); got != want {
		t.Errorf("--- want ---\n%q\n--- got ---\n%q", want, got)
	}
}
