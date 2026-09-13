/* tests/native/net_c_test.c - the socket layer tested as C, because the
 * interesting failures need a peer that is genuinely slow, genuinely silent or
 * genuinely gone, and a language with no threads cannot be that peer while it
 * is also the client.
 *
 * The generated program can test the shape of the layer -- a request goes out,
 * an answer comes back, a timeout raises -- and tests/programs/t98_*.teyru does
 * exactly that. What it cannot test is back-pressure: the loop in
 * ty_net_write_all only runs more than once when the socket's send buffer fills
 * up, and on loopback that takes a megabyte that nobody is reading yet. Here
 * the reader is a forked child, so a megabyte really does go through the loop,
 * the child's reads really are short, and a signal really does arrive in the
 * middle of a read.
 *
 * It is not part of `go test` (the Go suite runs .teyru programs, and this is
 * not one). Run it with:
 *
 *   cc -O1 -std=gnu11 -Wall -Wextra -o /tmp/net_c_test tests/native/net_c_test.c \
 *      && /tmp/net_c_test
 *
 * The socket layer is included rather than linked, so the file compiles the
 * exact source the compiler embeds. Only four runtime symbols are referenced by
 * it -- ty_str_new, ty_alloc_arr, ty_roots and ty_sp -- and this test defines
 * them itself, minimally and without a collector, because a collector is not
 * what is under test here and the real one belongs to a generated program that
 * has a heap, classes and a startup sequence. Nothing below allocates enough to
 * want a collection.
 */

#include "../../internal/runtime/src/tyrt_net.c"
/* Included after the source so that the compiler compares this file's
   prototypes against the definitions above: a header that had drifted from
   the code would be a build error here rather than a surprise in a program
   that links against the layer. */
#include "../../internal/runtime/src/tyrt_net.h"

#include <signal.h>
#include <sys/wait.h>

/* ---- the four symbols tyrt_net.c reaches for --------------------------- */

void *ty_roots[1024];
int64_t ty_sp = 0;

tystr *ty_str_new(const char *data, int64_t len) {
  tystr *s = (tystr *)malloc(sizeof(tystr) + (size_t)len + 1);
  s->obj.cls = NULL;
  s->len = len;
  s->data = (char *)s + sizeof(tystr);
  memcpy(s->data, data, (size_t)len);
  s->data[len] = 0;
  return s;
}

void *ty_alloc_arr(int64_t len, size_t elemsize) {
  tyarr *a = (tyarr *)malloc(sizeof(tyarr) + (size_t)len * elemsize);
  a->obj.cls = NULL;
  a->len = len;
  a->data = (char *)a + sizeof(tyarr);
  a->esize = (int32_t)elemsize;
  a->refs = 0;
  a->elemcls = NULL;
  memset(a->data, 0, (size_t)len * elemsize);
  return a;
}

/* ---- small conveniences ------------------------------------------------ */

/* A Teyru string over an ordinary C string, written as a compound literal so
   that each use is a distinct object with the lifetime of the enclosing block.
   A single static would be the obvious thing to reach for and would be wrong:
   two of them in one call site are two arguments, and one shared struct would
   make them the same pointer -- which is precisely the aliasing bug a socket
   layer must not have, so the test should not model it either. The argument is
   read twice, once for its length and once for its address, so it must be a
   plain expression. */
#define TS(s) \
  (&(tystr){.obj = {NULL}, .len = (int64_t)strlen(s), .data = (char *)(s)})

/* A byte array over a plain buffer. Same contract: esize 1 and a length are all
   ty_net_read and ty_net_write_all look at. */
static tyarr bytes_of(char *data, int64_t len) {
  tyarr a;
  memset(&a, 0, sizeof a);
  a.len = len;
  a.data = data;
  a.esize = 1;
  return a;
}

static int failures = 0;

static void check(int ok, const char *what) {
  if (!ok) failures++;
  printf("%-48s %s\n", what, ok ? "ok" : "FAILED");
}

static uint32_t checksum(const char *p, size_t n) {
  uint32_t h = 2166136261u; /* FNV-1a: order matters, so a reordered echo fails */
  for (size_t i = 0; i < n; i++) {
    h ^= (uint8_t)p[i];
    h *= 16777619u;
  }
  return h;
}

/* ---- the echo, through a megabyte of back-pressure --------------------- */

#define ECHO_BYTES (1024 * 1024)
#define ECHO_CHUNK 4096

static void child_echo(int32_t port) {
  int32_t fd = ty_net_connect(TS("127.0.0.1"), port, 5000);
  if (fd < 0) _exit(10);
  char *buf = (char *)malloc(ECHO_CHUNK);
  tyarr b = bytes_of(buf, ECHO_CHUNK);
  for (;;) {
    int32_t n = ty_net_read(fd, &b, 0, ECHO_CHUNK);
    if (n < 0) _exit(11);
    if (n == 0) break;
    if (ty_net_write_all(fd, &b, 0, n) != n) _exit(12);
  }
  /* Half close: the parent sees end of file the moment the last echo is out,
     without this side having to give up its descriptor. */
  (void)ty_net_shutdown_write(fd);
  (void)ty_net_close(fd);
  free(buf);
  _exit(0);
}

static void test_echo(void) {
  int32_t ls = ty_net_listen(0, 0, 1);
  check(ls >= 0, "listen on an ephemeral port");
  if (ls < 0) return;
  int32_t port = ty_net_local_port(ls);
  check(port > 0 && port <= 65535, "getLocalPort reports the bound port");

  pid_t pid = fork();
  if (pid == 0) {
    (void)close(ls);
    child_echo(port);
  }
  int32_t c = ty_net_accept(ls, 10000);
  check(c >= 0, "accept returns the connection the child made");
  if (c < 0) {
    (void)ty_net_close(ls);
    return;
  }

  char *src = (char *)malloc(ECHO_BYTES);
  char *got = (char *)malloc(ECHO_BYTES);
  for (int i = 0; i < ECHO_BYTES; i++) src[i] = (char)(i * 31 + 7);
  tyarr sb = bytes_of(src, ECHO_BYTES);

  /* One call of a megabyte. The kernel accepts a small fraction of it and the
     loop has to keep going, which is the whole point of this test. */
  check(ty_net_write_all(c, &sb, 0, ECHO_BYTES) == ECHO_BYTES,
        "write_all sends a megabyte in one call");
  (void)ty_net_shutdown_write(c);

  size_t have = 0;
  tyarr gb = bytes_of(got, ECHO_BYTES);
  while (have < ECHO_BYTES) {
    int32_t n = ty_net_read(c, &gb, (int32_t)have, (int32_t)(ECHO_BYTES - have));
    if (n <= 0) break;
    have += (size_t)n;
  }
  check(have == ECHO_BYTES, "read got every byte back");
  check(checksum(src, ECHO_BYTES) == checksum(got, ECHO_BYTES),
        "the echoed bytes are the bytes that were sent");

  (void)ty_net_close(c);
  (void)ty_net_close(ls);
  int status = 0;
  (void)waitpid(pid, &status, 0);
  check(WIFEXITED(status) && WEXITSTATUS(status) == 0, "the echo child exited cleanly");
  free(src);
  free(got);
}

/* ---- the timeout, the deadline and the end of the stream --------------- */

static void test_timeout_and_eof(void) {
  int32_t ls = ty_net_listen(0, 8, 1);
  if (ls < 0) {
    check(0, "listen for the timeout test");
    return;
  }
  int32_t port = ty_net_local_port(ls);
  int32_t cl = ty_net_connect(TS("127.0.0.1"), port, 2000);
  int32_t sv = ty_net_accept(ls, 2000);
  check(cl >= 0 && sv >= 0, "a connection to oneself is queued and accepted");

  char one = 0;
  tyarr b = bytes_of(&one, 1);

  check(ty_net_set_timeout(cl, 200) == 0, "setSoTimeout is accepted");
  int32_t r = ty_net_read(cl, &b, 0, 1);
  check(ty_net_is_timeout(r), "a silent peer reads as a timeout");
  check(r != 0, "the timeout is not the zero an end of file returns");

  check(ty_net_write_str(sv, TS("hi")) == 2, "write_str sends both bytes");
  check(ty_net_read(cl, &b, 0, 1) == 1 && one == 'h', "the first byte arrives");
  /* The second byte has to be taken before the half close, or the end of file
     arrives behind a byte that is still in the buffer and the next read answers
     with that byte instead. A real client reads to the end of the message for
     the same reason. */
  check(ty_net_read(cl, &b, 0, 1) == 1 && one == 'i', "the second byte arrives");

  (void)ty_net_shutdown_write(sv);
  check(ty_net_read(cl, &b, 0, 1) == 0,
        "a half-closed peer reads as end of file, not as a timeout");

  (void)ty_net_close(sv);
  (void)ty_net_close(cl);
  (void)ty_net_close(ls);
}

static void test_accept_timeout(void) {
  int32_t ls = ty_net_listen(0, 8, 1);
  int32_t r = ty_net_accept(ls, 150);
  check(ty_net_is_timeout(r), "accept with nothing pending times out");
  (void)ty_net_close(ls);
}

/* ---- a signal in the middle of a read ---------------------------------- */

static void on_alarm(int sig) { (void)sig; }

static void test_eintr(void) {
  int32_t ls = ty_net_listen(0, 8, 1);
  int32_t port = ty_net_local_port(ls);
  int32_t cl = ty_net_connect(TS("127.0.0.1"), port, 2000);
  int32_t sv = ty_net_accept(ls, 2000);

  /* No SA_RESTART: the handler has to interrupt the read for the retry to be
     exercised at all. With SA_RESTART the kernel restarts the read itself and
     the loop in ty_net_read would never be entered. */
  struct sigaction sa;
  memset(&sa, 0, sizeof sa);
  sa.sa_handler = on_alarm;
  sa.sa_flags = 0;
  (void)sigaction(SIGALRM, &sa, NULL);
  struct itimerval it;
  memset(&it, 0, sizeof it);
  it.it_value.tv_usec = 100000; /* 100 ms */
  (void)setitimer(ITIMER_REAL, &it, NULL);

  /* The socket's own timer is the backstop, so the retried read ends in a
     timeout rather than blocking for ever. */
  (void)ty_net_set_timeout(cl, 300);
  char one = 0;
  tyarr b = bytes_of(&one, 1);
  int32_t r = ty_net_read(cl, &b, 0, 1);
  check(ty_net_is_timeout(r), "a signal mid-read is retried, not reported");
  check(r != -EINTR, "the interrupted read did not surface as EINTR");

  (void)ty_net_close(sv);
  (void)ty_net_close(cl);
  (void)ty_net_close(ls);
}

/* ---- the port after a time-wait ---------------------------------------- */

static void test_reuse_after_time_wait(void) {
  int32_t ls = ty_net_listen(0, 8, 1);
  int32_t port = ty_net_local_port(ls);
  int32_t cl = ty_net_connect(TS("127.0.0.1"), port, 2000);
  int32_t sv = ty_net_accept(ls, 2000);
  (void)ty_net_close(sv); /* the side that closes first is the side that waits */
  (void)ty_net_close(cl);
  (void)ty_net_close(ls);
  /* Without SO_REUSEADDR this bind fails with EADDRINUSE for as long as the
     kernel keeps the pair in TIME_WAIT. */
  int32_t again = ty_net_listen(port, 8, 1);
  check(again >= 0, "the port can be bound again immediately");
  if (again >= 0) (void)ty_net_close(again);
}

/* ---- the address text -------------------------------------------------- */

static void test_addr_text(void) {
  int32_t ls = ty_net_listen(0, 8, 1);
  int32_t port = ty_net_local_port(ls);
  int32_t cl = ty_net_connect(TS("127.0.0.1"), port, 2000);
  int32_t sv = ty_net_accept(ls, 2000);
  tystr *pa = ty_net_peer_addr(sv);
  check(pa && strncmp(pa->data, "127.0.0.1:", 10) == 0, "the server's peer is the client");
  tystr *la = ty_net_local_addr(ls);
  check(la && strchr(la->data, ':') != NULL, "the listener names its own port");
  check(ty_net_strerror(-ECONNREFUSED) != NULL, "strerror has a message for a refusal");
  check(ty_net_strerror(TY_NET_TIMEOUT) != NULL, "strerror has a message for a timeout");
  char raw[3] = {'a', 'b', 'c'};
  tyarr rb = bytes_of(raw, 3);
  tystr *as = ty_net_bytes_to_str(&rb, 1, 2);
  check(as && as->len == 2 && strcmp(as->data, "bc") == 0, "bytes become a string");
  check(ty_net_bytes_to_str(&rb, 2, 2) == NULL, "a length past the end is refused");
  (void)ty_net_close(sv);
  (void)ty_net_close(cl);
  (void)ty_net_close(ls);
}

/* ---- the file layer ---------------------------------------------------- */

static void test_files(void) {
  tystr *dir = ty_file_temp_dir(TS("net_test_"));
  check(dir != NULL, "createTempDirectory gives a private directory");
  if (!dir) return;
  /* Nothing here is written under the source tree: TMPDIR is the only place
     this test touches, which is what keeps a failed run from leaving a file
     next to the compiler. */
  check(ty_file_kind(dir) == TY_FILE_DIR, "the temp path is a directory");
  check(ty_file_kind(TS("/nonexistent/teyru/probe")) == TY_FILE_NONE,
        "a missing path answers none rather than failing");

  char path[512];
  snprintf(path, sizeof path, "%s/data.txt", dir->data);
  const char *text = "line one\nline two\nline three\n";
  check(ty_file_write_str(TS(path), TS(text), 0) == (int32_t)strlen(text),
        "write_str writes every byte");
  check(ty_file_size(TS(path)) == (int64_t)strlen(text), "length agrees");
  check(ty_file_write_str(TS(path), TS("x"), 1) == 1, "append adds one byte");

  /* The out-parameter is a one-element int[] on the Teyru side, so the test
     passes the same shape the generated code does rather than a bare int. */
  int32_t errval = 0;
  tyarr err = bytes_of((char *)&errval, 1);
  err.esize = 4;
  tyarr *all = ty_file_read_bytes(TS(path), &err);
  check(all && all->len == (int64_t)strlen(text) + 1, "read_bytes reads to the end");
  check(all && memcmp(all->data, text, strlen(text)) == 0,
        "the bytes read are the bytes written");

  /* A megabyte through the write loop, then back through the read loop. The
     read path grows its buffer rather than trusting stat, so this also covers
     the case a size that was right before the write would have got wrong. */
  char big[8192];
  for (size_t i = 0; i < sizeof big; i++) big[i] = (char)(i * 7 + 1);
  snprintf(path, sizeof path, "%s/big.bin", dir->data);
  tyarr bb = bytes_of(big, (int64_t)sizeof big);
  int ok = 1;
  for (int i = 0; i < 128; i++) {
    if (ty_file_write_bytes(TS(path), &bb, 0, (int32_t)sizeof big, i != 0) !=
        (int32_t)sizeof big) {
      ok = 0;
      break;
    }
  }
  check(ok, "128 chunks of 8 KiB written in one append loop");
  tyarr *back = ty_file_read_bytes(TS(path), &err);
  int whole = back && back->len == 128 * (int64_t)sizeof big;
  check(whole, "the megabyte came back whole");
  int same = whole;
  if (same) {
    for (int64_t off = 0; off < back->len; off += (int64_t)sizeof big) {
      if (memcmp(back->data + off, big, sizeof big) != 0) {
        same = 0;
        break;
      }
    }
  }
  check(same, "every chunk of the megabyte matches what was written");

  /* A second and a third entry, to show the listing is sorted rather than in
     readdir's order. */
  snprintf(path, sizeof path, "%s/b.txt", dir->data);
  (void)ty_file_write_str(TS(path), TS("b"), 0);
  snprintf(path, sizeof path, "%s/c.txt", dir->data);
  (void)ty_file_write_str(TS(path), TS("c"), 0);
  tyarr *names = ty_file_list(dir, &err);
  int sorted = names && names->len >= 3;
  if (sorted) {
    for (int64_t i = 1; i < names->len; i++) {
      tystr *p = (tystr *)((void **)names->data)[i - 1];
      tystr *q = (tystr *)((void **)names->data)[i];
      if (strcmp(p->data, q->data) >= 0) sorted = 0;
    }
  }
  check(sorted, "listFiles returns the entries in sorted order");

  snprintf(path, sizeof path, "%s/sub/deep", dir->data);
  check(ty_file_mkdirs(TS(path)) == 0, "mkdirs creates a path a level at a time");
  check(ty_file_kind(TS(path)) == TY_FILE_DIR, "the created path is a directory");
  check(ty_file_delete(TS(path)) == 0, "an empty directory deletes");
  check(ty_file_kind(TS(path)) == TY_FILE_NONE, "and it is gone afterwards");
}

int main(void) {
  test_echo();
  test_timeout_and_eof();
  test_accept_timeout();
  test_eintr();
  test_reuse_after_time_wait();
  test_addr_text();
  test_files();
  printf("%d failure(s)\n", failures);
  return failures != 0;
}
