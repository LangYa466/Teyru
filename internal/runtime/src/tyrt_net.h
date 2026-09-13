/* tyrt_net.h - the declarations of the socket and file layer, for C that is
 * not tyrt_net.c.
 *
 * This header is NOT what tyrt_net.c includes, and cannot be: the driver copies
 * exactly one networking file into the directory a program is built in --
 * tyrt_net.c, the one internal/runtime/embed.go embeds -- so a header next to
 * it in the source tree does not exist at build time and including it would be
 * a fatal error. The declarations therefore live at the top of tyrt_net.c,
 * whose definitions do the same job for the one translation unit that needs
 * them. What is here is the same set for a caller: tests/native/net_c_test.c
 * and any other C that links against the layer. That test includes this header
 * after tyrt_net.c, so the compiler compares the prototypes below against the
 * definitions and a drift between the two is a build error rather than a
 * surprise at run time.
 *
 * ---- the calling convention, which is the whole of the interface ----------
 *
 * Nothing here throws a Teyru exception, because it cannot: the class object of
 * an exception declared in the prelude is never installed into a runtime
 * global, so C has no way to name IOException (see lib/15_net.teyru). The
 * convention that replaces it is the one a file descriptor already uses -- a
 * small integer whose sign carries the outcome:
 *
 *   >= 0   success. For a read, the number of bytes; for a call that returns a
 *          descriptor, the descriptor; for a call that returns a count, the
 *          count.
 *   < 0    failure, and the value is the negated errno: -EAGAIN for a timeout,
 *          -ECONNREFUSED for a refused connect, and so on. Negating keeps the
 *          failure distinguishable from every success, because errno is never 0
 *          and a descriptor and a count are never negative.
 *
 * The Teyru half of the pair turns that integer back into an exception, with
 * the message ty_net_strerror builds from the same number. Splitting it this
 * way keeps the C free of any knowledge of the Teyru class hierarchy and the
 * Teyru free of any knowledge of errno's numbering.
 *
 * Two codes are not errno at all, because the failure they describe does not
 * have one: a name that does not resolve, and a wait that ran out. Both are
 * answers a caller has to tell apart from a plain I/O failure, which is why
 * neither is folded into EIO.
 */

#ifndef TYRT_NET_H
#define TYRT_NET_H

#include <errno.h>
#include <stdint.h>

#include "tyrt.h"

/* A read, an accept or a connect ran out of time rather than failing. Distinct
   from the 0 a read returns at end of file -- a caller that treats the two the
   same reads a timeout as "the peer closed" and answers a request that is still
   coming. */
#define TY_NET_TIMEOUT (-EAGAIN)

/* The name did not resolve. Below every errno, so the two spaces cannot
   collide. */
#define TY_NET_UNKNOWN_HOST (-10001)

/* What a path names. TY_FILE_NONE is 0 because "not there" is an answer, not a
   failure; a failure is negative. */
#define TY_FILE_NONE 0
#define TY_FILE_REG 1
#define TY_FILE_DIR 2
#define TY_FILE_OTHER 3

/* ---- sockets ---------------------------------------------------------- */

/* Listen on every local interface, IPv4 only. port 0 asks the kernel for a free
   port, which ty_net_local_port then reports. reuse sets SO_REUSEADDR before
   the bind, without which a server that restarts while a connection is in
   TIME_WAIT cannot bind its own port for up to a minute. */
int32_t ty_net_listen(int32_t port, int32_t backlog, int32_t reuse);

/* Connect to host:port. timeout_ms < 0 waits as long as the kernel will, which
   is what `new Socket(host, port)` asks for. */
int32_t ty_net_connect(tystr *host, int32_t port, int32_t timeout_ms);

/* Take the next connection queued on a listening socket. timeout_ms < 0 waits
   without a deadline. A client that gave up between the poll and the accept is
   skipped rather than reported. */
int32_t ty_net_accept(int32_t fd, int32_t timeout_ms);

/* Read up to len bytes into buf[off..off+len). The count may be short: that is
   not an error and not end of file. 0 means the peer closed. */
int32_t ty_net_read(int32_t fd, tyarr *buf, int32_t off, int32_t len);

/* Write every one of len bytes, or fail. The loop is the difference between a
   server that works and one that truncates a response under load. On failure
   some prefix may already be on the wire, so the connection is unusable. */
int32_t ty_net_write_all(int32_t fd, tyarr *buf, int32_t off, int32_t len);

/* The same loop over a string's bytes, which is what a header or a body already
   is. */
int32_t ty_net_write_str(int32_t fd, tystr *s);

/* Half close: send the end of the stream while still being able to read it. */
int32_t ty_net_shutdown_write(int32_t fd);

/* Close, releasing the descriptor. A close interrupted by a signal is not
   retried: on Linux the descriptor is released even when the close reports
   EINTR, and closing it again could close whatever was handed the number
   next. */
int32_t ty_net_close(int32_t fd);

/* SO_RCVTIMEO, which is what Socket.setSoTimeout means in Java. ms <= 0 clears
   the timer; the timeout covers one read call, not a whole exchange. */
int32_t ty_net_set_timeout(int32_t fd, int32_t ms);

/* SO_REUSEADDR after the fact, for a socket not created with it. */
int32_t ty_net_set_reuse(int32_t fd, int32_t on);

/* The local port: the ephemeral one when port 0 was asked for. */
int32_t ty_net_local_port(int32_t fd);

/* "127.0.0.1:8080" for the local and the remote end. NULL when the descriptor
   is not a socket. */
tystr *ty_net_local_addr(int32_t fd);
tystr *ty_net_peer_addr(int32_t fd);

/* The message for a negative code: strerror's text plus the number, or the
   fixed text for the codes that are not errno. Never NULL. This is the one
   function here that allocates a Teyru object, and it hands it straight back to
   the caller, which is what makes it safe without a root. */
tystr *ty_net_strerror(int32_t code);

/* Whether a negative code is one of the two that are not plain failures. The
   prelude asks instead of comparing numbers, so the numbering stays in
   tyrt_net.c. */
int32_t ty_net_is_timeout(int32_t code);
int32_t ty_net_is_unknown_host(int32_t code);

/* A byte[] as a String. The bytes are not decoded or validated: they are the
 * characters, one byte each, which is what makes a UTF-8 request line come back
 * out as the UTF-8 it arrived as. A length outside the array is EINVAL rather
 * than a read past the end. */
tystr *ty_net_bytes_to_str(tyarr *b, int32_t off, int32_t len);

/* ---- files ------------------------------------------------------------ */

/* What a path names, or a negative code when the question cannot be answered.
   A path that is not there is TY_FILE_NONE, not a failure. */
int32_t ty_file_kind(tystr *path);

/* Size in bytes, or a negative code. */
int64_t ty_file_size(tystr *path);

/* Remove a file or an empty directory. A non-empty directory fails. */
int32_t ty_file_delete(tystr *path);

/* Create every missing directory in the path. */
int32_t ty_file_mkdirs(tystr *path);

/* The names of a directory's entries, sorted, as a String[], or NULL with the
   first element of err set to a positive errno. The dot entries are left out.

   `err` is not an int32_t* but a Teyru int[]: the generated code can pass an
   array into C and cannot pass a pointer back out, so the reason a call that
   returns an object failed is written into element 0 of an array the caller
   made. An array that is not an int[] of at least one element is refused --
   NULL comes back and nothing is written, rather than an errno landing in the
   array's header where the collector's class pointer lives. */
tyarr *ty_file_list(tystr *path, tyarr *err);

/* The whole file as a byte[], read to end of file rather than to the size stat
   reports, or NULL with err's first element set. */
tyarr *ty_file_read_bytes(tystr *path, tyarr *err);

/* Write len bytes of buf[off..] to a path, truncating unless append is set. */
int32_t ty_file_write_bytes(tystr *path, tyarr *buf, int32_t off, int32_t len,
                            int32_t append);
int32_t ty_file_write_str(tystr *path, tystr *s, int32_t append);

/* Create a directory nobody else will use and return its path, or NULL. */
tystr *ty_file_temp_dir(tystr *prefix);

#endif /* TYRT_NET_H */
