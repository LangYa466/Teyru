package mod

import (
	"strings"
	"testing"
)

func TestValidImportSpelling(t *testing.T) {
	for _, p := range []string{"example.com/a", "example.com/a/b", "a"} {
		if !ValidImportSpelling(p) {
			t.Errorf("%q was rejected", p)
		}
	}
	for _, p := range []string{"", "a/", "/a", "a//b", "a/./b", "a/../b", "a b"} {
		if ValidImportSpelling(p) {
			t.Errorf("%q was accepted", p)
		}
	}
}

func TestImportPathOfPackage(t *testing.T) {
	cases := map[[2]string]string{
		{"example.com/a", ""}:    "example.com/a",
		{"example.com/a", "."}:   "example.com/a",
		{"example.com/a", "pkg"}: "example.com/a/pkg",
		{"example.com/a", "x/y"}: "example.com/a/x/y",
	}
	for in, want := range cases {
		if got := ImportPathOfPackage(in[0], in[1]); got != want {
			t.Errorf("ImportPathOfPackage(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

// A module path starts with a host name, so its second element is a top-level
// domain. Java spells a package the other way round, which is what keeps this
// from claiming `java.util.List` and reporting a missing module for it.
func TestLooksLikeModulePath(t *testing.T) {
	// the argument is the dotted spelling the parser reads
	for _, p := range []string{"example.com.a", "github.com.x.y.pkg", "example.io.a.b"} {
		if !LooksLikeModulePath(p) {
			t.Errorf("%q was not recognised", p)
		}
	}
	for _, p := range []string{"java.util.List", "tyru.String", "a.b", "example.com", "com.example.Foo"} {
		if LooksLikeModulePath(p) {
			t.Errorf("%q was recognised", p)
		}
	}
}

func TestMatchPath(t *testing.T) {
	paths := []string{"example.com/a", "example.com/a/pkg", "example.com/b"}
	cases := map[string]string{
		"example.com.a":            "example.com/a",
		"example.com.a.Widget":     "example.com/a",
		"example.com.a.pkg":        "example.com/a/pkg",
		"example.com.a.pkg.Widget": "example.com/a/pkg",
		"example.com.b":            "example.com/b",
		"example.com.ab":           "",
		// a package under the module, whatever its name: the module path is a
		// prefix of every import path inside it
		"example.com.a.pkgx.Widget": "example.com/a",
		"java.util.List":            "",
	}
	for in, want := range cases {
		got, ok := MatchPath(in, paths)
		if want == "" {
			if ok {
				t.Errorf("MatchPath(%q) = %q, want no match", in, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("MatchPath(%q) = %q, %v, want %q", in, got, ok, want)
		}
	}
	// The dot of the host name is not a separator: folding the match back into
	// slashes would name a module that does not exist.
	if got, _ := MatchPath("example.com.a.pkg", paths); !strings.HasPrefix(got, "example.com/") {
		t.Errorf("MatchPath gave back the dotted spelling: %q", got)
	}
}
