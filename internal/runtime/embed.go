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

// Reflect is tyrt_reflect.c: class metadata access, the field and method
// tables, and reflective invocation. It is a file of its own for the same
// reason as Net: a program that never reflects should not have to link the
// invoker thunks' callee, and a reader looking for java.lang.reflect's
// behaviour should not have to read past the allocator to find it.
//
//go:embed src/tyrt_reflect.c
var Reflect string

// Thread is tyrt_thread.c: the per-thread runtime state, the thread registry,
// the stop-the-world protocol a collection runs the other threads through, and
// the monitors `synchronized` and Object.wait are built on. It is a file of its
// own because it is the one part of the runtime that is about more than one
// thread at a time: a program that never starts a thread still links it (the
// main thread is a thread), but a reader looking for the allocator or for
// java.lang has no reason to read past it.
//
//go:embed src/tyrt_thread.c
var Thread string
