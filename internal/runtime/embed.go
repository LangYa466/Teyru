// Package runtime embeds the C runtime that ships with the compiler.
package runtime

import _ "embed"

// Header is tyrt.h.
//
//go:embed src/tyrt.h
var Header string

// Core is tyrt.c.
//
//go:embed src/tyrt.c
var Core string

// Extra is tyrt2.c.
//
//go:embed src/tyrt2.c
var Extra string

// Net is tyrt_net.c: the socket and file-descriptor primitives. It is a file of
// its own so that the networking code does not have to be read past to find the
// allocator, and so that a program that never opens a socket still links one
// translation unit it does not call.
//
//go:embed src/tyrt_net.c
var Net string
