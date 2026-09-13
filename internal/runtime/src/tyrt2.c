/* tyrt2.c - Teyru runtime: prelude helpers, strings, math, StringBuilder. */
#include "tyrt.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <math.h>
#include <time.h>

/* The helpers below that a generated program calls but tyrt.h does not declare
   (that header is another component's): getClass's Class builder, and the
   stream-aware print helpers. The call site declares them too, inside the
   statement expression it wraps the call in. Forward declarations here keep a
   -Wmissing-prototypes build of this file quiet. */
void *ty_class_of_cls(void *o, tyclass *clscls);
void ty_ps_print_str(void *self, tystr *s);
void ty_ps_println_str(void *self, tystr *s);
void ty_ps_print_int(void *self, int64_t v);
void ty_ps_println_int(void *self, int64_t v);
void ty_ps_print_double(void *self, double v);
void ty_ps_println_double(void *self, double v);
void ty_ps_print_float(void *self, float v);
void ty_ps_println_float(void *self, float v);
void ty_ps_print_char(void *self, uint16_t c);
void ty_ps_println_char(void *self, uint16_t c);
void ty_ps_print_bool(void *self, int32_t v);
void ty_ps_println_bool(void *self, int32_t v);
void ty_ps_print_obj(void *self, void *o);
void ty_ps_println_obj(void *self, void *o);
void ty_ps_println_void(void *self);

/* ---- class initialisation --------------------------------------------- */

void ty_unimplemented(const char *what) {
  fprintf(stderr, "teyru: no implementation for %s\n", what);
  exit(70);
}

void ty_clinit(tyclass *c) {
  if (!c || (c->flags & 8)) return;
  c->flags |= TY_CLS_INIT;
  if (c->super) ty_clinit(c->super);
  for (int32_t i = 0; i < c->niface; i++) ty_clinit(c->ifaces[i]);
  if (c->clinit) ((void (*)(void))c->clinit)();
}

/* ---- Class objects ------------------------------------------------------ */

/* getClass has to hand back a real object. It used to return the tyclass
   pointer itself, and every consumer of a reference reads the object's class
   out of its first word, so `println(x.getClass())` walked a `const char*`
   name as if it were a tyclass and crashed.

   A Class object is a small wrapper: its payload is the tyclass it names, and
   its own class is the program's own teyru.Class, which the call site hands
   over (native.go emits `ty_class_of_cls(o, &cls_teyru_Class)`), because that
   struct is static in the generated C and the runtime cannot name it. With the
   real class in place, instanceof, casts, virtual dispatch and equality all
   behave like they do for any other object.

   One wrapper per tyclass is reused, so `a.getClass() == a.getClass()` holds.
   The wrappers and the buckets below are malloc'd rather than allocated from
   the runtime heap: class metadata is immortal, and memory the collector does
   not own can never be collected out from under a Class object that a program
   still refers to. */
typedef struct tyclassobj {
  tyobj obj;            /* obj.cls is teyru.Class */
  tyclass *target;      /* the class this object names */
  struct tyclassobj *next;
} tyclassobj;

#define TY_CLASS_BUCKETS 64
static tyclassobj *ty_class_objs[TY_CLASS_BUCKETS];

/* The generated teyru.Class, as passed by the call site. NULL until the first
   call, which is also what a class literal's raw handle looks like. */
static tyclass *ty_class_cls;

void *ty_class_of_cls(void *o, tyclass *clscls) {
  if (clscls) ty_class_cls = clscls;
  if (!ty_class_cls) ty_class_cls = TY_OBJECT;
  if (!o) ty_throw(ty_npe());
  tyclass *k = ((tyobj *)o)->cls;
  if (!k) return NULL;
  int32_t b = (int32_t)((((uintptr_t)k) >> 4) & (TY_CLASS_BUCKETS - 1));
  for (tyclassobj *c = ty_class_objs[b]; c; c = c->next) {
    if (c->target == k) return c;
  }
  tyclassobj *c = (tyclassobj *)calloc(1, sizeof(tyclassobj));
  if (!c) ty_throw(ty_npe());
  c->obj.cls = ty_class_cls;
  c->target = k;
  c->next = ty_class_objs[b];
  ty_class_objs[b] = c;
  return c;
}

/* tyrt.h declares this one and nothing in a generated program calls it any
   more: the getClass call site goes through ty_class_of_cls, which also hands
   over the tyclass of teyru.Class. It stays as the raw handle accessor for
   native code that wants the class pointer itself. */
void *ty_class_of(void *o) { return o ? (void *)(((tyobj *)o)->cls) : NULL; }

/* Class.getName and Class.toString. The receiver is either a Class object built
   by ty_class_of_cls, whose payload names the class, or the raw tyclass handle
   a class literal produces (codegen renders `String.class` as
   `(tyobj*)&cls_teyru_String`), which is the tyclass itself. The wrapper's own
   class tells the two apart: a tyclass's first word is its name, and a name
   can never be the address of the generated Class struct. */
tystr *ty_class_name(void *c) {
  if (!c) return NULL;
  if (ty_class_cls && ((tyobj *)c)->cls == ty_class_cls) {
    tyclass *k = ((tyclassobj *)c)->target;
    return k ? ty_str_intern(k->name) : NULL;
  }
  return ty_str_intern(((tyclass *)c)->name);
}

tystr *ty_str_ident(tystr *s) { return s; }

tystr *ty_str_copy(tystr *s) { return s ? ty_str_new(s->data, s->len) : NULL; }

/* Identity hash of an object or array, used where no user hashCode exists.
   The address is stable because the collector never moves objects. */
int32_t ty_object_hash(tyobj *o) {
  if (!o) ty_npe();
  uintptr_t p = (uintptr_t)o;
  return (int32_t)((p >> 4) ^ (p >> 32) ^ (p >> 20));
}

int32_t ty_object_equals(tyobj *a, tyobj *b) { return a == b; }

/* String.equals(Object): in Java only another String can be equal, so the class
   is checked before the payload is read as one. An unrelated object carries no
   length and data fields where a String keeps them, and reading them would
   compare against whatever bytes follow the object. */
int32_t ty_str_eq_obj(tystr *a, void *b) {
  if (b == NULL) return a == NULL;
  if (((tyobj *)b)->cls != TY_STRING) return 0;
  return ty_str_eq(a, (tystr *)b);
}

tystr *ty_str_sub_from(tystr *s, int32_t from) {
  if (!s) return NULL;
  if (from < 0 || from > s->len) ty_throw((tyobj *)ty_aioobe(from, s ? s->len : 0));
  return ty_str_sub(s, from, (int32_t)s->len);
}

int64_t ty_str_tolong(tystr *s) { return s ? strtoll(s->data, NULL, 10) : 0; }
double ty_str_todouble(tystr *s) { return s ? strtod(s->data, NULL) : 0; }
float ty_str_tofloat(tystr *s) { return s ? (float)strtod(s->data, NULL) : 0; }
int32_t ty_str_tobool(tystr *s) { return s && strcmp(s->data, "true") == 0; }

/* ---- boxing helpers ---------------------------------------------------- */

tystr *ty_int_tostr(void *o) { return ty_str_of_int(ty_unbox_int(o)); }
/* Byte.toString and Short.toString are the decimal spelling of the value, so
   they share the int formatter. */
tystr *ty_byte_tostr(void *o) { return ty_str_of_int(ty_unbox_byte(o)); }
tystr *ty_short_tostr(void *o) { return ty_str_of_int(ty_unbox_short(o)); }
tystr *ty_bool_tostr(void *o) { return ty_str_of_bool(ty_unbox_bool(o)); }
tystr *ty_char_tostr(void *o) { return ty_str_of_char(ty_unbox_char(o)); }
tystr *ty_long_tostr(void *o) { return ty_str_of_long(ty_unbox_long(o)); }
tystr *ty_double_tostr(void *o) { return ty_str_of_double(ty_unbox_double(o)); }
tystr *ty_float_tostr(void *o) { return ty_str_of_float(ty_unbox_float(o)); }

int32_t ty_int_equals(void *a, void *b) {
  if (b == NULL) return 0;
  return ty_unbox_int(a) == ty_unbox_int(b);
}
int32_t ty_long_equals(void *a, void *b) {
  if (b == NULL) return 0;
  return ty_unbox_long(a) == ty_unbox_long(b);
}
/* Java's doubleToLongBits and floatToIntBits: the bit pattern with every NaN
   collapsed to one value and the sign of zero kept. Double.equals,
   Double.hashCode and Double.compare are all defined through it, so the two
   zeros differ, NaN equals NaN, and equal values hash alike. */
static int64_t dbl_bits(double d) {
  if (d != d) return (int64_t)0x7ff8000000000000LL;
  int64_t b;
  memcpy(&b, &d, 8);
  return b;
}
static int32_t flt_bits(float f) {
  if (f != f) return (int32_t)0x7fc00000;
  int32_t b;
  memcpy(&b, &f, 4);
  return b;
}
int32_t ty_double_equals(void *a, void *b) {
  if (b == NULL) return 0;
  return dbl_bits(ty_unbox_double(a)) == dbl_bits(ty_unbox_double(b));
}
int32_t ty_float_equals(void *a, void *b) {
  if (b == NULL) return 0;
  return flt_bits(ty_unbox_float(a)) == flt_bits(ty_unbox_float(b));
}
int32_t ty_char_equals(void *a, void *b) {
  if (b == NULL) return 0;
  return ty_unbox_char(a) == ty_unbox_char(b);
}
int32_t ty_bool_equals(void *a, void *b) {
  if (b == NULL) return 0;
  return ty_unbox_bool(a) == ty_unbox_bool(b);
}
int32_t ty_int_compare(void *a, void *b) {
  int32_t x = ty_unbox_int(a), y = ty_unbox_int(b);
  return x < y ? -1 : (x > y ? 1 : 0);
}
int32_t ty_long_compare(int64_t a, int64_t b) { return a < b ? -1 : (a > b ? 1 : 0); }
int32_t ty_prim_cmp_int(int32_t a, int32_t b) { return a < b ? -1 : (a > b ? 1 : 0); }
int32_t ty_prim_cmp_long(int64_t a, int64_t b) { return a < b ? -1 : (a > b ? 1 : 0); }
int32_t ty_prim_cmp_double(double a, double b) { return a < b ? -1 : (a > b ? 1 : 0); }
/* Java's Double.compare/Float.compare: the numeric order first, and only for
   values that compare equal numerically (the two zeros, NaN) the bit order,
   which puts -0.0 below 0.0 and NaN above everything. */
int32_t ty_double_compare(double a, double b) {
  if (a < b) return -1;
  if (a > b) return 1;
  int64_t x = dbl_bits(a), y = dbl_bits(b);
  return x == y ? 0 : (x < y ? -1 : 1);
}
int32_t ty_float_compare(float a, float b) {
  if (a < b) return -1;
  if (a > b) return 1;
  int32_t x = flt_bits(a), y = flt_bits(b);
  return x == y ? 0 : (x < y ? -1 : 1);
}
/* Long.compareTo and Double.compareTo take the other box as an argument, so the
   `_obj` forms unbox it first; a null argument raises a NullPointerException,
   like an intrinsic in Java. */
int32_t ty_long_compare_obj(void *a, void *b) {
  return ty_long_compare(ty_unbox_long(a), ty_unbox_long(b));
}
int32_t ty_double_compare_obj(void *a, void *b) {
  return ty_double_compare(ty_unbox_double(a), ty_unbox_double(b));
}
int32_t ty_float_compare_obj(void *a, void *b) {
  return ty_float_compare(ty_unbox_float(a), ty_unbox_float(b));
}
int32_t ty_char_compare_obj(void *a, void *b) {
  uint16_t x = ty_unbox_char(a), y = ty_unbox_char(b);
  return x < y ? -1 : (x > y ? 1 : 0);
}
int32_t ty_long_hash(void *o) {
  int64_t v = ty_unbox_long(o);
  return (int32_t)(v ^ ((uint64_t)v >> 32));
}
int32_t ty_dhash_bits(double d) {
  int64_t bits = dbl_bits(d);
  return (int32_t)(bits ^ ((uint64_t)bits >> 32));
}
int32_t ty_double_hash(void *o) { return ty_dhash_bits(ty_unbox_double(o)); }
int32_t ty_float_hash(void *o) { return flt_bits(ty_unbox_float(o)); }
int32_t ty_char_hash(void *o) { return (int32_t)ty_unbox_char(o); }
int32_t ty_long_toint(void *o) { return (int32_t)ty_unbox_long(o); }

int32_t ty_fhash_bits(float f) { return flt_bits(f); }

/* ---- Number conversions ------------------------------------------------- */
/* Java's Number declares six conversions and every numeric wrapper implements
   all of them, narrowing the way Java narrows: `Integer.byteValue()` is
   `(byte) this.intValue()`. The receiver is always a box whose kind can be read
   from its class, so one helper per target type serves every wrapper. */
static int32_t num_kind(void *o) {
  if (!o) return 0;
  tyclass *c = ((tyobj *)o)->cls;
  if (!(c->flags & 4)) return 0; /* not a box */
  for (int32_t i = 1; i <= 8; i++) {
    if (c == TY_BOX[i]) return i;
  }
  return 0;
}

static int64_t num_int64(void *o) {
  switch (num_kind(o)) {
  case 1: return ((tyboolbox *)o)->v;
  case 2: return ((tybytebox *)o)->v;
  case 3: return ((tyshortbox *)o)->v;
  case 4: return ((tycharbox *)o)->v;
  case 5: return ((tyintbox *)o)->v;
  case 6: return ((tylongbox *)o)->v;
  case 7: return (int64_t)((tyfloatbox *)o)->v;
  case 8: return (int64_t)((tydoublebox *)o)->v;
  }
  return 0;
}

static double num_double(void *o) {
  switch (num_kind(o)) {
  case 7: return (double)((tyfloatbox *)o)->v;
  case 8: return ((tydoublebox *)o)->v;
  }
  return (double)num_int64(o);
}

int32_t ty_num_int(void *o) { return (int32_t)num_int64(o); }
int64_t ty_num_long(void *o) { return num_int64(o); }
double ty_num_double(void *o) { return num_double(o); }
float ty_num_float(void *o) { return (float)num_double(o); }
int8_t ty_num_byte(void *o) { return (int8_t)num_int64(o); }
int16_t ty_num_short(void *o) { return (int16_t)num_int64(o); }


/* ---- math -------------------------------------------------------------- */

int32_t ty_abs_int(int32_t v) { return v < 0 ? -v : v; }
int64_t ty_abs_long(int64_t v) { return v < 0 ? -v : v; }
double ty_abs_double(double v) { return fabs(v); }
int32_t ty_max_int(int32_t a, int32_t b) { return a > b ? a : b; }
int32_t ty_min_int(int32_t a, int32_t b) { return a < b ? a : b; }
int64_t ty_max_long(int64_t a, int64_t b) { return a > b ? a : b; }
int64_t ty_min_long(int64_t a, int64_t b) { return a < b ? a : b; }
double ty_max_double(double a, double b) { return a > b ? a : b; }
double ty_min_double(double a, double b) { return a < b ? a : b; }
int64_t ty_round(double v) { return (int64_t)floor(v + 0.5); }
double ty_random(void) { return (double)rand() / ((double)RAND_MAX + 1.0); }
int32_t ty_isnan(double v) { return isnan(v) ? 1 : 0; }
int32_t ty_is_digit(uint16_t c) { return c >= '0' && c <= '9'; }
int32_t ty_is_letter(uint16_t c) { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'); }
int32_t ty_is_space(uint16_t c) { return c == ' ' || c == '\t' || c == '\n' || c == '\r'; }

int64_t ty_millis(void) {
  struct timespec ts;
  clock_gettime(CLOCK_REALTIME, &ts);
  return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}
int64_t ty_nanos(void) {
  struct timespec ts;
  clock_gettime(CLOCK_MONOTONIC, &ts);
  return (int64_t)ts.tv_sec * 1000000000 + ts.tv_nsec;
}
void ty_exit(int32_t code) { exit(code); }

void ty_arraycopy(void *src, int32_t spos, void *dst, int32_t dpos, int32_t len) {
  tyarr *a = (tyarr *)src, *b = (tyarr *)dst;
  if (!a || !b) ty_throw((tyobj *)ty_npe());
  /* written so that a negative length or a huge index cannot wrap the sum */
  if (spos < 0 || dpos < 0 || len < 0 || spos > a->len - len || dpos > b->len - len) {
    ty_throw((tyobj *)ty_aioobe(spos < 0 ? spos : dpos, a->len));
  }
  /* Java requires the two arrays to have the same element type: copying a
     long[] into a byte[] is an ArrayStoreException. Without this test the copy
     below would take its byte count from the source element size and write
     past the end of the destination. */
  if (a->esize != b->esize || a->refs != b->refs) ty_throw((tyobj *)ty_arraystore());
  memmove((char *)b->data + (size_t)dpos * b->esize, (char *)a->data + (size_t)spos * a->esize,
          (size_t)len * a->esize);
}

void *ty_illarg(const char *msg) { return ty_make_ex(TY_ILLARG, msg); }
void *ty_illegal_state(const char *msg) { return ty_make_ex(TY_ILLSTATE, msg); }

/* ---- exceptions with a message ----------------------------------------- */

void *ty_make_ex(tyclass *c, const char *msg) {
  tyobj *o = (tyobj *)ty_alloc(sizeof(tyobj) + 2 * sizeof(void *));
  o->cls = c;
  ((void **)((char *)o + sizeof(tyobj)))[0] = ty_str_new(msg, (int64_t)strlen(msg));
  ((void **)((char *)o + sizeof(tyobj)))[1] = NULL;
  return o;
}

/* ---- object equality --------------------------------------------------- */

int32_t ty_obj_equal(void *a, void *b) {
  if (a == b) return 1;
  if (!a || !b) return 0;
  return ((int32_t (*)(void *, void *))((tyobj *)a)->cls->vtable[2])(a, b);
}

/* ---- enums ------------------------------------------------------------- */

int32_t ty_enum_ordinal(void *o) { return o ? ((tyEnumBase *)o)->ordinal : -1; }
void *ty_enum_name(void *o) { return o ? (void *)((tyEnumBase *)o)->name : NULL; }
int32_t ty_enum_compare(void *a, void *b) { return ty_enum_ordinal(a) - ty_enum_ordinal(b); }

/* ---- StringBuilder ----------------------------------------------------- */

void *ty_sb_new(void) {
  tySB *sb = (tySB *)ty_alloc(sizeof(tySB));
  sb->cap = 32;
  sb->len = 0;
  sb->buf = (char *)malloc((size_t)sb->cap);
  return sb;
}

static void sb_ensure(tySB *sb, int64_t extra) {
  if (sb->len + extra <= sb->cap) return;
  while (sb->len + extra > sb->cap) sb->cap *= 2;
  sb->buf = (char *)realloc(sb->buf, (size_t)sb->cap);
}

void *ty_sb_append_str(void *p, tystr *s) {
  tySB *sb = (tySB *)p;
  if (!s) return p;
  sb_ensure(sb, s->len);
  memcpy(sb->buf + sb->len, s->data, (size_t)s->len);
  sb->len += s->len;
  return p;
}
void *ty_sb_append_int(void *p, int64_t v) { return ty_sb_append_str(p, ty_str_of_long(v)); }
void *ty_sb_append_long(void *p, int64_t v) { return ty_sb_append_str(p, ty_str_of_long(v)); }
void *ty_sb_append_double(void *p, double v) { return ty_sb_append_str(p, ty_str_of_double(v)); }
void *ty_sb_append_bool(void *p, int32_t v) { return ty_sb_append_str(p, ty_str_of_bool(v)); }
void *ty_sb_append_char(void *p, uint16_t c) { return ty_sb_append_str(p, ty_str_of_char(c)); }
void *ty_sb_append_obj(void *p, void *o) {
  if (!o) return ty_sb_append_str(p, ty_str_intern("null"));
  return ty_sb_append_str(p, ty_str_of_obj((tyobj *)o));
}
tystr *ty_sb_tostring(void *p) {
  tySB *sb = (tySB *)p;
  return ty_str_new(sb->buf, sb->len);
}
int32_t ty_sb_len(void *p) { return (int32_t)((tySB *)p)->len; }


/* ---- java.io.IO -------------------------------------------------------- */

tystr *ty_readln(void) {
  static char buf[4096];
  if (!fgets(buf, (int)sizeof buf, stdin)) return NULL;
  size_t n = strlen(buf);
  while (n > 0 && (buf[n - 1] == '\n' || buf[n - 1] == '\r')) buf[--n] = 0;
  return ty_str_new(buf, (int64_t)n);
}

/* ---- PrintStream ------------------------------------------------------- */

/* A PrintStream writes to the file descriptor in its first instance field: 1 is
   stdout and 2 is stderr, so System.err reaches descriptor 2 while every other
   stream keeps writing to 1. The prelude declares the field (lib/03_io.teyru,
   `private int target`) and System's initializer is what sets it; the helpers
   below read it at the offset that follows the object header, which is where
   the field lands because it is the only instance field of the class.

   These helpers are not in tyrt.h: the generated program declares them at the
   call site (see the stream twins in internal/codegen/native.go), so the shared
   header does not have to change. Each one mirrors the stdout helper of the
   same name in tyrt.c byte for byte, down to the formatting helper it calls, so
   `System.out.println(x)` and `System.err.println(x)` produce identical text.

   Writing to stderr flushes stdout first: stdout is block buffered when it is a
   pipe, and without the flush the two streams would come out of order. */
typedef struct {
  tyobj obj;
  int32_t target;
} tyPrintStream;

static FILE *ty_ps_out(void *self) {
  if (!self) ty_throw(ty_npe());
  if (((tyPrintStream *)self)->target != 2) return stdout;
  fflush(stdout);
  return stderr;
}

void ty_ps_print_str(void *self, tystr *s) {
  FILE *f = ty_ps_out(self);
  if (s) fwrite(s->data, 1, (size_t)s->len, f);
  else fputs("null", f);
}
void ty_ps_println_str(void *self, tystr *s) {
  ty_ps_print_str(self, s);
  fputc('\n', ty_ps_out(self));
}
void ty_ps_print_int(void *self, int64_t v) { fprintf(ty_ps_out(self), "%lld", (long long)v); }
void ty_ps_println_int(void *self, int64_t v) { fprintf(ty_ps_out(self), "%lld\n", (long long)v); }
void ty_ps_print_double(void *self, double v) {
  tystr *s = ty_str_of_double(v);
  fwrite(s->data, 1, (size_t)s->len, ty_ps_out(self));
}
void ty_ps_println_double(void *self, double v) {
  ty_ps_print_double(self, v);
  fputc('\n', ty_ps_out(self));
}
void ty_ps_print_float(void *self, float v) {
  tystr *s = ty_str_of_float(v);
  fwrite(s->data, 1, (size_t)s->len, ty_ps_out(self));
}
void ty_ps_println_float(void *self, float v) {
  ty_ps_print_float(self, v);
  fputc('\n', ty_ps_out(self));
}
void ty_ps_print_char(void *self, uint16_t c) {
  FILE *f = ty_ps_out(self);
  if (c < 0x80) fputc((int)c, f);
  else fputs(ty_str_of_char(c)->data, f);
}
void ty_ps_println_char(void *self, uint16_t c) {
  ty_ps_print_char(self, c);
  fputc('\n', ty_ps_out(self));
}
void ty_ps_print_bool(void *self, int32_t v) { fputs(v ? "true" : "false", ty_ps_out(self)); }
void ty_ps_println_bool(void *self, int32_t v) { fputs(v ? "true\n" : "false\n", ty_ps_out(self)); }
void ty_ps_print_obj(void *self, void *o) { ty_ps_print_str(self, ty_str_of_obj(o)); }
void ty_ps_println_obj(void *self, void *o) {
  ty_ps_print_obj(self, o);
  fputc('\n', ty_ps_out(self));
}
void ty_ps_println_void(void *self) { fputc('\n', ty_ps_out(self)); }
