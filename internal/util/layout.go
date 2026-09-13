package util

import "github.com/LangYa466/Teyru/internal/ast"

// Primitive and reference sizes in the generated C, in bytes.
const (
	SizeByte   = 1
	SizeShort  = 2
	SizeChar   = 2
	SizeInt    = 4
	SizeFloat  = 4
	SizeLong   = 8
	SizeDouble = 8
	SizeRef    = 8
)

// SizeOf returns the storage size of a type as laid out in generated C.
func SizeOf(t ast.Type) int64 {
	switch v := t.(type) {
	case *ast.PrimType:
		switch v.Kind {
		case ast.Byte:
			return SizeByte
		case ast.Short, ast.Char:
			return SizeShort
		case ast.Boolean, ast.Int, ast.Float:
			return SizeInt
		case ast.Long, ast.Double:
			return SizeLong
		}
		return SizeInt
	}
	return SizeRef
}

// AlignOf returns the alignment the C compiler uses for a type.
func AlignOf(t ast.Type) int64 {
	switch v := t.(type) {
	case *ast.PrimType:
		switch v.Kind {
		// boolean is int32_t in the generated C, so it aligns like an int;
		// aligning it as one byte would shift every field after it and the
		// collector would trace the wrong slots
		case ast.Byte:
			return 1
		case ast.Short, ast.Char:
			return 2
		case ast.Long, ast.Double:
			return 8
		}
		return 4
	}
	return 8
}

// Align rounds off up to the next multiple of a.
func Align(off, a int64) int64 {
	if a <= 1 {
		return off
	}
	return (off + a - 1) / a * a
}

// FieldLayout assigns each field its byte offset using the C struct rules and
// returns the offsets of the fields the garbage collector must trace plus the
// total size of the struct.
//
// The generator and the runtime must agree exactly: a missing offset makes the
// collector free a live object, an extra offset makes it dereference garbage.
func FieldLayout(fields []*ast.Field, extraRefs []ast.Type) (refOffsets []int64, size int64) {
	off := int64(SizeRef) // every object starts with `tyobj obj`
	for _, f := range fields {
		off = Align(off, AlignOf(f.Type))
		if IsRef(f.Type) {
			refOffsets = append(refOffsets, off)
		}
		off += SizeOf(f.Type)
	}
	for _, t := range extraRefs {
		off = Align(off, AlignOf(t))
		if IsRef(t) {
			refOffsets = append(refOffsets, off)
		}
		off += SizeOf(t)
	}
	return refOffsets, Align(off, SizeRef)
}

// IsRef reports whether a value of this type is an object reference at run time.
func IsRef(t ast.Type) bool {
	switch t.(type) {
	case *ast.ClassType, *ast.ArrayType, ast.NullType, *ast.WildcardType, *ast.TypeVarType:
		return true
	}
	return false
}

// IsPrim reports whether a value of this type is a primitive at run time.
func IsPrim(t ast.Type) bool {
	_, ok := t.(*ast.PrimType)
	return ok
}
