package mod

import "testing"

func TestParseVersion(t *testing.T) {
	ok := map[string]Version{
		"v0.1.0":      {0, 1, 0, ""},
		"v1.2.3":      {1, 2, 3, ""},
		"v2.0.0":      {2, 0, 0, ""},
		"v1.0.0-rc1":  {1, 0, 0, "rc1"},
		"v1.0.0-rc.1": {1, 0, 0, "rc.1"},
		"v10.20.30":   {10, 20, 30, ""},
	}
	for in, want := range ok {
		got, err := ParseVersion(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%s parsed to %+v, want %+v", in, got, want)
		}
		if got.String() != in {
			t.Errorf("%s round tripped to %s", in, got.String())
		}
	}
	// A version is also a directory name in the cache and a git tag, so the
	// spellings that name the same tag twice are refused rather than folded.
	for _, in := range []string{
		"", "1.0.0", "v1", "v1.0", "v1.0.0.0", "v01.2.3", "v1.02.3", "v1.2.3-",
		"v1.2.3+build", "v-1.0.0", "v1.2.x", "v 1.0.0", "V1.0.0", "v1.0.-1",
	} {
		if v, err := ParseVersion(in); err == nil {
			t.Errorf("parsed %q as %+v", in, v)
		}
	}
}

func TestCompare(t *testing.T) {
	// want is the sign of the comparison, -1, 0 or 1.
	cases := map[[2]string]int{
		{"v1.0.0", "v1.0.0"}:         0,
		{"v1.0.0", "v1.0.1"}:         -1,
		{"v1.2.0", "v1.1.9"}:         1,
		{"v2.0.0", "v1.99.99"}:       1,
		{"v1.0.0-rc1", "v1.0.0"}:     -1,
		{"v1.0.0", "v1.0.0-rc1"}:     1,
		{"v1.0.0-rc1", "v1.0.0-rc2"}: -1,
		// numeric identifiers compare as numbers, not as bytes
		{"v1.0.0-rc.2", "v1.0.0-rc.10"}: -1,
		// ...and only when they are identifiers of their own: `rc2` is one
		// alphanumeric identifier, so it compares as text and `rc10` is less
		{"v1.0.0-rc10", "v1.0.0-rc2"}: -1,
		// numeric identifiers sort before alphanumeric ones
		{"v1.0.0-1", "v1.0.0-alpha"}:       -1,
		{"v1.0.0-alpha", "v1.0.0-beta"}:    -1,
		{"v1.0.0-alpha.1", "v1.0.0-alpha"}: 1,
	}
	for pair, want := range cases {
		a, err := ParseVersion(pair[0])
		if err != nil {
			t.Fatal(err)
		}
		b, err := ParseVersion(pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if got := Compare(a, b); got != want {
			t.Errorf("Compare(%s, %s) = %d, want %d", pair[0], pair[1], got, want)
		}
		if got := Compare(b, a); got != -want {
			t.Errorf("Compare(%s, %s) = %d, want %d", pair[1], pair[0], got, -want)
		}
	}
}

// From v2 on a module path carries its major version. Without the rule, `v2`
// and `v1` of one module would occupy the same import path and a program could
// not use both.
func TestCheckImportPath(t *testing.T) {
	ok := [][2]string{
		{"example.com/a", "v0.1.0"},
		{"example.com/a", "v1.9.9"},
		{"example.com/a/v2", "v2.0.0"},
		{"example.com/a/v10", "v10.1.0"},
	}
	for _, c := range ok {
		if err := CheckImportPath(c[0], c[1]); err != nil {
			t.Errorf("%s@%s: %v", c[0], c[1], err)
		}
	}
	for _, c := range [][2]string{
		{"example.com/a", "v2.0.0"},
		{"example.com/a/v2", "v1.0.0"},
		{"example.com/a/v3", "v2.0.0"},
	} {
		if err := CheckImportPath(c[0], c[1]); err == nil {
			t.Errorf("%s@%s was accepted", c[0], c[1])
		}
	}
	// A version that is not one at all is reported the same way, so the
	// command line has one check to call.
	if err := CheckImportPath("example.com/a", "1.0.0"); err == nil {
		t.Error("a version with no v was accepted")
	}
}

func TestValidPath(t *testing.T) {
	for _, p := range []string{"a", "example.com/a", "example.com/a/b", "a-b/c_d", "a.b~c/d"} {
		if !ValidPath(p) {
			t.Errorf("%q was rejected", p)
		}
	}
	for _, p := range []string{"", "/a", "a/", "a//b", "a b", "a/b!", "a/../b", "a\\b"} {
		if ValidPath(p) {
			t.Errorf("%q was accepted", p)
		}
	}
}

func TestDottedPath(t *testing.T) {
	if got := DottedPath("example.com/dep/pkg"); got != "example.com.dep.pkg" {
		t.Errorf("DottedPath = %q", got)
	}
	if got := DottedPath("example.com"); got != "example.com" {
		t.Errorf("DottedPath = %q", got)
	}
}
