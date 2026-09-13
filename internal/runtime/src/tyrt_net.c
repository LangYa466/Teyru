/* The socket and file-descriptor primitives.
 *
 * Kept apart from tyrt.c and tyrt2.c so that the networking code is one file to
 * read, and so that a program which never opens a socket still compiles a
 * translation unit whose functions are never called. Every entry here is
 * reached from a Teyru `native` method through the table in
 * internal/codegen/native_net.go; nothing in the language core depends on it.
 */
#include "tyrt.h"
