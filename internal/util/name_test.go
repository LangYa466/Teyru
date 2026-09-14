package util

import (
	"testing"

	"github.com/teyru-lang/Teyru/internal/ast"
)

func TestMangle(t *testing.T) {
	cases := map[string]string{
		"Foo":          "Foo",
		"a.b.C":        "a_b_C",
		"Outer$Inner":  "Outer_Inner",
		"Lambda$0":     "Lambda_0",
		"<init>":       "_init_",
		"":             "_",
		"with spaces":  "with_spaces",
		"_under_score": "_under_score",
	}
	for in, want := range cases {
		if got := Mangle(in); got != want {
			t.Errorf("Mangle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCapitalize(t *testing.T) {
	cases := map[string]string{
		"name": "Name",
		"Name": "Name",
		"URL":  "URL",
		"a":    "A",
		"":     "",
	}
	for in, want := range cases {
		if got := Capitalize(in); got != want {
			t.Errorf("Capitalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDescriptor(t *testing.T) {
	str := &ast.ClassType{Class: &ast.Class{Name: "String"}}
	cases := []struct {
		t    ast.Type
		want string
	}{
		{ast.TInt, "I"},
		{ast.TLong, "J"},
		{ast.TDouble, "D"},
		{ast.TBoolean, "Z"},
		{ast.TChar, "C"},
		{ast.TVoid, "V"},
		{&ast.ArrayType{Elem: ast.TInt}, "A"},
		{str, "String"},
	}
	for _, c := range cases {
		if got := Descriptor(c.t); got != c.want {
			t.Errorf("Descriptor(%v) = %q, want %q", c.t, got, c.want)
		}
	}
	if got := Signature("println", []ast.Type{ast.TInt}); got != "println(I)" {
		t.Errorf("Signature = %q", got)
	}
	if got := Signature("m", []ast.Type{ast.TInt, ast.TLong}); got != "m(I,J)" {
		t.Errorf("Signature = %q", got)
	}
}

func TestFieldLayout(t *testing.T) {
	owner := &ast.Class{Name: "C"}
	fields := []*ast.Field{
		{Name: "a", Type: ast.TInt, Owner: owner},
		{Name: "b", Type: &ast.ClassType{Class: &ast.Class{Name: "String"}}, Owner: owner},
		{Name: "c", Type: ast.TLong, Owner: owner},
	}
	refs, size := FieldLayout(fields, nil)
	if len(refs) != 1 || refs[0] != 16 {
		t.Errorf("ref offsets = %v, want [16]", refs)
	}
	if size != 32 {
		t.Errorf("size = %d, want 32", size)
	}
}

func TestAlignAndSize(t *testing.T) {
	if got := Align(9, 8); got != 16 {
		t.Errorf("Align(9,8) = %d", got)
	}
	if got := Align(8, 8); got != 8 {
		t.Errorf("Align(8,8) = %d", got)
	}
	if SizeOf(ast.TByte) != 1 || SizeOf(ast.TShort) != 2 || SizeOf(ast.TDouble) != 8 {
		t.Error("primitive sizes are wrong")
	}
	if SizeOf(&ast.ClassType{Class: &ast.Class{Name: "X"}}) != 8 {
		t.Error("reference size must be 8")
	}
}
