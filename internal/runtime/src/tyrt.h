/* tyrt.h - Teyru native runtime: objects, strings, arrays, exceptions, GC. */
#ifndef TYRT_H
#define TYRT_H

#include <stdint.h>
#include <stddef.h>
#include <setjmp.h>
#include <string.h>

typedef struct tyclass tyclass;
typedef struct tyobj tyobj;

struct tyobj {
  tyclass *cls;
};

typedef struct tystr {
  tyobj obj;
  int64_t len;
  char *data;
} tystr;

typedef struct tyarr {
  tyobj obj;
  int64_t len;
  char *data;
  int32_t esize;
  int32_t refs; /* 1 when elements are object references */
} tyarr;

typedef struct tymap {
  int32_t sel;
  void *fn;
} tymap;

struct tyclass {
  const char *name;
  int32_t id;
  int32_t flags; /* 1 = interface, 2 = array, 4 = primitive wrapper */
  tyclass *super;
  int32_t niface;
  tyclass **ifaces;
  int32_t nvt;
  void **vtable;
  void *clinit;
  int32_t isize;
  int32_t isel;
  tymap *imap;
  int32_t nsub;
  tyclass **subs;
  int32_t nref;     /* number of traced reference fields */
  int32_t *refoffs; /* byte offsets of reference fields */
};

/* Class handles installed by generated startup code. */
extern tyclass *TY_STRING;
extern tyclass *TY_ARRAY;
extern tyclass *TY_BOX[9];
extern tyclass *TY_OBJECT;

/* ---- exceptions ------------------------------------------------------- */
typedef struct tycatch {
  jmp_buf buf;
  struct tycatch *prev;
  tyobj *ex;
} tycatch;

extern tycatch *ty_cur_catch;

void ty_throw(void *e) __attribute__((noreturn));
void ty_uncaught(void *e) __attribute__((noreturn));

/* Preallocated exception classes (filled by generated code at startup). */
extern tyclass *TY_NPE, *TY_AIOOBE, *TY_ARITH, *TY_CCE, *TY_NEGARR, *TY_ASSERT,
    *TY_ILLARG, *TY_ILLSTATE, *TY_NOSUCHELEM, *TY_UNSUP, *TY_ARRAYSTORE;

void *ty_npe(void);
void *ty_aioobe(int64_t idx, int64_t len);
void *ty_arith(const char *msg);
void *ty_cce(tyclass *from, tyclass *to);
void *ty_negarr(void);
void *ty_arraystore(void);
void *ty_assertfail(const char *msg);

/* ---- allocation / GC -------------------------------------------------- */

/* The fast path lives in the header so the generated program allocates with an
   inlined bump-pointer check; only a full page or a pending collection falls
   back into the runtime. */
/* class flags: the class has been initialised */
#define TY_CLS_INIT 8
/* the class describes an array: its payload is a tyarr whose element slots the
   collector has to trace when the array holds references */
#define TY_CLS_ARRAY 2
#define TY_HDR 16
#define TY_ALIGN 16
extern char *ty_bump;
extern char *ty_bump_end;
extern int64_t ty_alloc_since;
extern int64_t ty_gc_threshold;
void *ty_alloc_slow(size_t total);

static inline void *ty_alloc(size_t size) {
  size_t total = (size + TY_HDR + TY_ALIGN - 1) & ~(size_t)(TY_ALIGN - 1);
  char *p = ty_bump;
  if (p + total > ty_bump_end || ty_alloc_since > ty_gc_threshold) {
    return ty_alloc_slow(total);
  }
  ty_bump = p + total;
  ty_alloc_since += (int64_t)total;
  *(uint64_t *)p = (uint64_t)total;
  *(uint64_t *)(p + 8) = 0;
  void *obj = p + TY_HDR;
  memset(obj, 0, total - TY_HDR);
  return obj;
}
void *ty_alloc_arr(int64_t len, size_t elemsize);
void ty_gc_init(void);
void ty_gc(void);
#define TY_SHADOW_MAX (1 << 20)
extern void *ty_roots[];   /* shadow stack */
extern int64_t ty_sp;
#define TY_ROOT_PUSH(v) (ty_roots[ty_sp++] = (void *)(v))
#define TY_ROOT_POP() (--ty_sp)
void ty_gc_register_static(void *p);
void ty_free_block(void *payload, size_t total);

/* ---- strings ---------------------------------------------------------- */
tystr *ty_str_new(const char *data, int64_t len);
tystr *ty_str_intern(const char *data);
int64_t ty_str_len(tystr *s);
tystr *ty_str_concat(tystr *a, tystr *b);
int32_t ty_str_eq(tystr *a, tystr *b);
int32_t ty_str_cmp(tystr *a, tystr *b);
int32_t ty_str_hash(tystr *s);
tystr *ty_str_of_int(int64_t v);
tystr *ty_str_of_bool(int32_t v);
tystr *ty_str_of_char(uint16_t c);
tystr *ty_str_of_double(double v);
tystr *ty_str_of_float(float v);
tystr *ty_str_of_long(int64_t v);
tystr *ty_str_of_obj(void *o);
int32_t ty_obj_hash(void *o);
int32_t ty_obj_eq(void *a, void *b);
tystr *ty_str_upper(tystr *s);
tystr *ty_str_lower(tystr *s);
tystr *ty_str_trim(tystr *s);
tystr *ty_str_sub(tystr *s, int32_t from, int32_t to);
int32_t ty_str_indexof(tystr *s, tystr *sub);
int32_t ty_str_charat(tystr *s, int32_t i);
int32_t ty_str_contains(tystr *s, tystr *sub);
int32_t ty_str_starts(tystr *s, tystr *p);
int32_t ty_str_ends(tystr *s, tystr *p);
tystr *ty_str_replace(tystr *s, uint16_t a, uint16_t b);
int32_t ty_str_isempty(tystr *s);
int32_t ty_str_toint(tystr *s);

/* ---- arrays ----------------------------------------------------------- */
tyarr *ty_array_new(int64_t len, int64_t elemsize);
int64_t ty_array_len(tyarr *a);
tyarr *ty_array_clone(tyarr *a, int64_t elemsize);
void ty_array_store_ref(tyarr *a, int64_t i, void *v);
void *ty_arr_ptr(tyarr *a, int64_t i);
void *ty_arr_slot_ref(tyarr *a, int64_t i);
void *ty_arr_ref(tyarr *a, int64_t i);

/* ---- interfaces / casts ---------------------------------------------- */
void *ty_itab(void *o, int32_t sel);
int32_t ty_instanceof(void *o, tyclass *c);
void *ty_checkcast(void *o, tyclass *c);

/* ---- boxing ----------------------------------------------------------- */
void *ty_box_int(int32_t v);
void *ty_box_long(int64_t v);
void *ty_box_double(double v);
void *ty_box_float(float v);
void *ty_box_short(int16_t v);
void *ty_box_byte(int8_t v);
void *ty_box_char(uint16_t v);
void *ty_box_bool(int32_t v);
int32_t ty_unbox_int(void *o);
int64_t ty_unbox_long(void *o);
double ty_unbox_double(void *o);
float ty_unbox_float(void *o);
int16_t ty_unbox_short(void *o);
int8_t ty_unbox_byte(void *o);
uint16_t ty_unbox_char(void *o);
int32_t ty_unbox_bool(void *o);

/* Primitive type patterns (JEP 507). ty_prim_match reports whether the operand
   matches the requested primitive kind and stores the converted value through
   out. kind uses the same numbering as TY_BOX: 1 boolean, 2 byte, 3 short,
   4 char, 5 int, 6 long, 7 float, 8 double.

   boxed says the operand was a reference and therefore carries a box: JEP 507
   then requires the box to be exactly the pattern's type (an Integer matches
   `int i` but not `long l`). When the operand was a primitive, which the
   compiler boxes to get here, only the conversion has to be exact, so
   `long v = 5; v instanceof int i` matches but 5000000000L does not. */
int32_t ty_prim_match(void *o, int32_t kind, void *out, int32_t boxed);

/* ---- misc ------------------------------------------------------------- */
void ty_sync_enter(void *lock);
void ty_sync_exit(void *lock);
void ty_println_str(tystr *s);
void ty_print_str(tystr *s);
void ty_print_int(int64_t v);
void ty_println_int(int64_t v);
void ty_print_double(double v);
void ty_println_double(double v);
void ty_print_float(float v);
void ty_println_float(float v);
void ty_print_char(uint16_t c);
void ty_println_char(uint16_t c);
void ty_print_bool(int32_t v);
void ty_println_bool(int32_t v);
void ty_print_obj(void *o);
void ty_println_obj(void *o);
void ty_println_void(void);
void ty_init(void);
tystr *ty_readln(void);
void ty_unimplemented(const char *what) __attribute__((noreturn));

/* ---- prelude helpers --------------------------------------------------- */
void ty_clinit(tyclass *c);
void *ty_class_of(void *o);
tystr *ty_class_name(void *c);
tystr *ty_str_ident(tystr *s);
tystr *ty_str_copy(tystr *s);
int32_t ty_str_eq_obj(tystr *s, void *o);
tystr *ty_str_sub_from(tystr *s, int32_t from);
int64_t ty_str_tolong(tystr *s);
double ty_str_todouble(tystr *s);
float ty_str_tofloat(tystr *s);
int32_t ty_str_tobool(tystr *s);
tystr *ty_int_tostr(void *o);
tystr *ty_byte_tostr(void *o);
tystr *ty_short_tostr(void *o);
tystr *ty_bool_tostr(void *o);
tystr *ty_char_tostr(void *o);
tystr *ty_long_tostr(void *o);
tystr *ty_double_tostr(void *o);
tystr *ty_float_tostr(void *o);
int32_t ty_int_equals(void *a, void *b);
int32_t ty_long_equals(void *a, void *b);
int32_t ty_double_equals(void *a, void *b);
int32_t ty_bool_equals(void *a, void *b);
int32_t ty_int_compare(void *a, void *b);
int32_t ty_long_compare(int64_t a, int64_t b);
int32_t ty_prim_cmp_int(int32_t a, int32_t b);
int32_t ty_prim_cmp_long(int64_t a, int64_t b);
int32_t ty_prim_cmp_double(double a, double b);
int32_t ty_double_compare(double a, double b);
int32_t ty_long_compare_obj(void *a, void *b);
int32_t ty_double_compare_obj(void *a, void *b);
int32_t ty_float_compare_obj(void *a, void *b);
int32_t ty_char_compare_obj(void *a, void *b);
int32_t ty_float_equals(void *a, void *b);
int32_t ty_char_equals(void *a, void *b);
int32_t ty_float_compare(float a, float b);
int32_t ty_float_hash(void *o);
int32_t ty_char_hash(void *o);
int32_t ty_long_hash(void *o);
int32_t ty_double_hash(void *o);
int32_t ty_long_toint(void *o);
int32_t ty_dhash_bits(double d);
int32_t ty_fhash_bits(float f);
/* the six java.lang.Number conversions, for any boxed numeric receiver */
int32_t ty_num_int(void *o);
int64_t ty_num_long(void *o);
double ty_num_double(void *o);
float ty_num_float(void *o);
int8_t ty_num_byte(void *o);
int16_t ty_num_short(void *o);
int32_t ty_abs_int(int32_t v);
int64_t ty_abs_long(int64_t v);
double ty_abs_double(double v);
int32_t ty_max_int(int32_t a, int32_t b);
int32_t ty_min_int(int32_t a, int32_t b);
int64_t ty_max_long(int64_t a, int64_t b);
int64_t ty_min_long(int64_t a, int64_t b);
double ty_max_double(double a, double b);
double ty_min_double(double a, double b);
int64_t ty_round(double v);
double ty_random(void);
int32_t ty_isnan(double v);
int32_t ty_is_digit(uint16_t c);
int32_t ty_is_letter(uint16_t c);
int32_t ty_is_space(uint16_t c);
int64_t ty_millis(void);
int64_t ty_nanos(void);
void ty_exit(int32_t code);
void ty_arraycopy(void *src, int32_t spos, void *dst, int32_t dpos, int32_t len);
void *ty_illarg(const char *msg);
void *ty_illegal_state(const char *msg);
void *ty_make_ex(tyclass *c, const char *msg);
int32_t ty_obj_equal(void *a, void *b);
int32_t ty_enum_ordinal(void *o);
void *ty_enum_name(void *o);
int32_t ty_enum_compare(void *a, void *b);

typedef struct { tyobj obj; int32_t v; } tyintbox;
typedef struct { tyobj obj; int64_t v; } tylongbox;
typedef struct { tyobj obj; double v; } tydoublebox;
typedef struct { tyobj obj; float v; } tyfloatbox;
typedef struct { tyobj obj; uint16_t v; } tycharbox;
typedef struct { tyobj obj; int32_t v; } tyboolbox;
typedef struct { tyobj obj; int8_t v; } tybytebox;
typedef struct { tyobj obj; int16_t v; } tyshortbox;
typedef struct { tyobj obj; int32_t ordinal; tystr *name; } tyEnumBase;
typedef struct { tyobj obj; int64_t len, cap; char *buf; } tySB;

void *ty_sb_new(void);
void *ty_sb_append_str(void *sb, tystr *s);
void *ty_sb_append_int(void *sb, int64_t v);
void *ty_sb_append_long(void *sb, int64_t v);
void *ty_sb_append_double(void *sb, double v);
void *ty_sb_append_bool(void *sb, int32_t v);
void *ty_sb_append_obj(void *sb, void *o);
void *ty_sb_append_char(void *sb, uint16_t c);
tystr *ty_sb_tostring(void *sb);
int32_t ty_sb_len(void *sb);

/* Object methods dispatched from generated code */
tystr *ty_object_tostring(void *o);
int32_t ty_object_hash(tyobj *o);
int32_t ty_object_equals(tyobj *a, tyobj *b);

/* enum helpers */

int32_t ty_div_int(int32_t a, int32_t b);
tyarr *ty_str_split(tystr *s, tystr *sep);
int64_t ty_div_long(int64_t a, int64_t b);
int32_t ty_rem_int(int32_t a, int32_t b);
int64_t ty_rem_long(int64_t a, int64_t b);

#endif /* TYRT_H */
