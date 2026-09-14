package util

import "github.com/teyru-lang/Teyru/internal/ast"

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

// FieldOffsets assigns every field its byte offset using the C struct rules,
// and answers with the size of the struct. `extraRefs` are the captured locals
// a class declared inside a method keeps: they follow the declared fields and
// are laid out by the same rules.
//
// The generator and the runtime must agree exactly: a missing offset makes the
// collector free a live object, an extra one makes it dereference garbage, and
// reflection hands a wrong one to a program that then reads another field than
// the one it asked for.
func FieldOffsets(fields []*ast.Field, extraRefs []ast.Type) (offsets []int64, size int64) {
	off := int64(SizeRef) // every object starts with `tyobj obj`
	offsets = make([]int64, 0, len(fields)+len(extraRefs))
	for _, f := range fields {
		off = Align(off, AlignOf(f.Type))
		offsets = append(offsets, off)
		off += SizeOf(f.Type)
	}
	for _, t := range extraRefs {
		off = Align(off, AlignOf(t))
		offsets = append(offsets, off)
		off += SizeOf(t)
	}
	return offsets, Align(off, SizeRef)
}

// FieldLayout is FieldOffsets restricted to what the collector needs: the
// offsets of the fields that hold references, in field order, and the size of
// the struct. It is written over FieldOffsets rather than beside it, because
// two walks that disagree by one field are the bug this file exists to avoid.
func FieldLayout(fields []*ast.Field, extraRefs []ast.Type) (refOffsets []int64, size int64) {
	offsets, size := FieldOffsets(fields, extraRefs)
	for i, f := range fields {
		if IsRef(f.Type) {
			refOffsets = append(refOffsets, offsets[i])
		}
	}
	for i, t := range extraRefs {
		if IsRef(t) {
			refOffsets = append(refOffsets, offsets[len(fields)+i])
		}
	}
	return refOffsets, size
}

// IsRef reports whether a value of this type is an object reference at run time.
// The definition is ast.IsRef: util may import ast but not the other way round,
// and two copies of the list of reference kinds would be free to drift.
func IsRef(t ast.Type) bool { return ast.IsRef(t) }

// IsPrim reports whether a value of this type is a primitive at run time.
func IsPrim(t ast.Type) bool { return ast.IsPrimType(t) }
