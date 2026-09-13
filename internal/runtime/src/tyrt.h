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
  /* The class the array promised for its elements, recorded by the `new T[]`
     the array came from. Java's arrays are covariant but their element type is
     fixed at creation, so a store through a wider view (Object[] o = new
     String[2]) has to reject a value the array never promised; elemcls is what
     ty_array_store_ref compares against. NULL means the array carries no
     promise and accepts any reference, which is what every array the runtime
     itself creates does. */
  tyclass *elemcls;
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

/* ---- interfaces / casts ---------------------------------------------- */
/* Declared before the arrays: the array store test below calls ty_instanceof
   for the values whose class is not the one the array promised. */
void *ty_itab(void *o, int32_t sel);
int32_t ty_instanceof(void *o, tyclass *c);
void *ty_checkcast(void *o, tyclass *c);

/* ---- arrays ----------------------------------------------------------- */
tyarr *ty_array_new(int64_t len, int64_t elemsize);
int64_t ty_array_len(tyarr *a);
tyarr *ty_array_clone(tyarr *a, int64_t elemsize);

/* Store the reference v into element i of a, applying the array store check
   Java applies to a covariant store: a value whose class the array's element
   class never promised is an ArrayStoreException, not a silent write. The null
   test and the bounds test come first, in Java's order.

   selem is the element class at the store site. It lets the two common cases
   skip the test, which is what keeps the check off the hot path:

   - an array that carries no promise (elemcls == NULL) accepts anything, as it
     did before element classes were recorded;
   - an array whose promise is exactly selem cannot fail this store. The
     compiler accepted the store, so the value's static type is assignable to
     selem, and assignability is about the class hierarchy: whatever value
     arrives, its class is a subtype of selem -- which is the class this array
     promised. The test would always pass, so it is not made.

   Anything else is checked: the value's own class first, then its subtyping,
   so `Object[] o = new String[2]; o[0] = "x"` costs one compare.

   The first test is deliberately `a->elemcls != selem` rather than the two
   tests it spells out, so that the case a caller spends its time in -- a store
   into an array that really is an selem[] -- costs one load, one compare and
   one branch. Everything the first test does not answer falls into the second
   test, which is written the long way round because it has to be right, not
   quick.

   The generated code inlines this, so the fast path is a compare and a store;
   the compiler's generated store sites pass &cls_<element type> for selem. */
static inline void ty_array_store_ref(tyarr *a, tyclass *selem, int64_t i, void *v) {
  if (!a) ty_npe();
  if (i < 0 || i >= a->len) ty_aioobe(i, a->len);
  if (a->elemcls != selem && a->elemcls && v &&
      ((tyobj *)v)->cls != a->elemcls && !ty_instanceof(v, a->elemcls)) {
    ty_arraystore();
  }
  ((void **)a->data)[i] = v;
}
void *ty_arr_ptr(tyarr *a, int64_t i);
void *ty_arr_slot_ref(tyarr *a, int64_t i);
void *ty_arr_ref(tyarr *a, int64_t i);

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
float ty_abs_float(float v);
int32_t ty_max_int(int32_t a, int32_t b);
int32_t ty_min_int(int32_t a, int32_t b);
int64_t ty_max_long(int64_t a, int64_t b);
int64_t ty_min_long(int64_t a, int64_t b);
double ty_max_double(double a, double b);
double ty_min_double(double a, double b);
float ty_max_float(float a, float b);
float ty_min_float(float a, float b);
int64_t ty_round(double v);
int32_t ty_round_float(float v);
int32_t ty_floor_div_int(int32_t a, int32_t b);
int64_t ty_floor_div_long(int64_t a, int64_t b);
int32_t ty_floor_mod_int(int32_t a, int32_t b);
int64_t ty_floor_mod_long(int64_t a, int64_t b);
double ty_signum_double(double v);
float ty_signum_float(float v);
double ty_to_radians(double deg);
double ty_to_degrees(double rad);
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
void *ty_sb_init(void *sb, int64_t cap);
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
int64_t ty_div_long(int64_t a, int64_t b);
int32_t ty_rem_int(int32_t a, int32_t b);
int64_t ty_rem_long(int64_t a, int64_t b);

/* ---------------------------------------------------------------- java.lang
 *
 * The rest of java.lang. Every helper here exists because Java's answer to a
 * question differs from C's answer to the same question: rounding at the ends
 * of a range, the order of the two zeros, the radix of a number, what counts as
 * a letter, what String.format prints. The prelude declares the method and this
 * header declares the helper it runs, so a class in lib/ and its C are two
 * halves of one function.
 */

/* Math: Java's semantics where C's differ. abs and max/min for the integer
   types are the ones tyrt2.c already had, moved here because negating the most
   negative value is undefined in C and Java defines it. */
int32_t ty_math_abs_int(int32_t v);
int64_t ty_math_abs_long(int64_t v);
float ty_abs_float(float v);
double ty_math_max_double(double a, double b);
double ty_math_min_double(double a, double b);
float ty_math_max_float(float a, float b);
float ty_math_min_float(float a, float b);
int64_t ty_math_round_long(double v);
int32_t ty_math_round_int(float v);
int32_t ty_math_floor_div_int(int32_t a, int32_t b);
int64_t ty_math_floor_div_long(int64_t a, int64_t b);
int32_t ty_math_floor_mod_int(int32_t a, int32_t b);
int64_t ty_math_floor_mod_long(int64_t a, int64_t b);
double ty_signum_double(double v);
float ty_signum_float(float v);
double ty_math_to_radians(double deg);
double ty_math_to_degrees(double rad);

/* System */
tystr *ty_getenv(tystr *name);
tystr *ty_get_property(tystr *key);
int32_t ty_in_read(void *self);
tystr *ty_in_readln(void *self);

/* Character */
int32_t ty_is_whitespace(uint16_t c);
int32_t ty_is_letter_or_digit(uint16_t c);
int32_t ty_is_upper_case(uint16_t c);
int32_t ty_is_lower_case(uint16_t c);
int32_t ty_is_alphabetic(uint16_t c);
int32_t ty_char_upper(uint16_t c);
int32_t ty_char_lower(uint16_t c);
int32_t ty_char_numeric(uint16_t c);
int32_t ty_char_digit(uint16_t c, int32_t radix);
int32_t ty_char_compare(uint16_t a, uint16_t b);

/* The wrappers: parsing, radix formatting and the bit twiddling Integer and
   Long expose. A parse either succeeds or throws, which is why each type has a
   parsable test beside the conversion it guards. */
int32_t ty_str_parsable_int(tystr *s, int32_t radix);
int64_t ty_str_parsable_long(tystr *s, int32_t radix);
int32_t ty_str_parsable_double(tystr *s);
int32_t ty_str_parsable_float(tystr *s);
int32_t ty_str_toint_radix(tystr *s, int32_t radix);
int64_t ty_str_tolong_radix(tystr *s, int32_t radix);
tystr *ty_radix_string_int(int32_t v, int32_t radix);
tystr *ty_radix_string_long(int64_t v, int32_t radix);
tystr *ty_unsigned_string_int(int32_t v, int32_t radix);
tystr *ty_unsigned_string_long(int64_t v, int32_t radix);
tystr *ty_byte_tostr_val(int32_t v);
tystr *ty_short_tostr_val(int32_t v);
tystr *ty_float_tostr_val(float v);
float ty_str_tofloat_val(tystr *s);
double ty_str_todouble_val(tystr *s);
int32_t ty_int_bit_count(int32_t v);
int32_t ty_long_bit_count(int64_t v);
int32_t ty_int_nlz(int32_t v);
int32_t ty_int_ntz(int32_t v);
int32_t ty_long_nlz(int64_t v);
int32_t ty_long_ntz(int64_t v);
int32_t ty_int_highest_one(int32_t v);
int32_t ty_int_lowest_one(int32_t v);
int64_t ty_long_highest_one(int64_t v);
int64_t ty_long_lowest_one(int64_t v);
int32_t ty_int_reverse(int32_t v);
int32_t ty_int_reverse_bytes(int32_t v);
int64_t ty_long_reverse(int64_t v);
int64_t ty_long_reverse_bytes(int64_t v);
int32_t ty_int_rotate_left(int32_t v, int32_t d);
int32_t ty_int_rotate_right(int32_t v, int32_t d);
int64_t ty_long_rotate_left(int64_t v, int32_t d);
int64_t ty_long_rotate_right(int64_t v, int32_t d);
int32_t ty_int_signum(int32_t v);
int64_t ty_long_signum(int64_t v);
int32_t ty_int_sum(int32_t a, int32_t b);
int64_t ty_long_sum(int64_t a, int64_t b);
double ty_double_sum(double a, double b);
float ty_float_sum(float a, float b);
int32_t ty_int_cmp_unsigned(int32_t a, int32_t b);
int64_t ty_long_cmp_unsigned(int64_t a, int64_t b);
int64_t ty_double_bits(double v);
double ty_bits_double(int64_t bits);
int32_t ty_float_bits(float v);
float ty_bits_float(int32_t bits);
int32_t ty_double_is_infinite(double v);
int32_t ty_float_is_infinite(float v);
int32_t ty_double_is_finite(double v);
int32_t ty_float_is_finite(float v);
int32_t ty_float_isnan(float v);
int64_t ty_double_raw_bits(double v);
int32_t ty_float_raw_bits(float v);
int32_t ty_bool_compare(int32_t a, int32_t b);
int32_t ty_box_equals(void *a, void *b);
double ty_math_cbrt(double x);
int32_t ty_byte_hash_val(int32_t v);
int32_t ty_short_hash_val(int32_t v);
int32_t ty_char_hash_val(uint16_t c);
int32_t ty_int_hash_val(int32_t v);
int32_t ty_long_hash_val(int64_t v);
int32_t ty_bool_hash_val(int32_t v);
int32_t ty_bool_hash_box(void *o);
int32_t ty_double_hash_val(double v);
tystr *ty_char_tostr_val(uint16_t c);
/* System.identityHashCode: the Object hash without dispatching to an override,
   and 0 for a null, which is what Java answers. */
int32_t ty_identity_hash(void *o);

/* String */
int32_t ty_str_cmp_ic(tystr *a, tystr *b);
int32_t ty_str_eq_ic(tystr *a, tystr *b);
int32_t ty_str_starts_from(tystr *s, tystr *p, int32_t from);
int32_t ty_str_indexof_from(tystr *s, tystr *sub, int32_t from);
int32_t ty_str_indexof_ch(tystr *s, int32_t c);
int32_t ty_str_indexof_ch_from(tystr *s, int32_t c, int32_t from);
int32_t ty_str_lastindexof(tystr *s, tystr *sub);
int32_t ty_str_lastindexof_from(tystr *s, tystr *sub, int32_t from);
int32_t ty_str_lastindexof_ch(tystr *s, int32_t c);
int32_t ty_str_lastindexof_ch_from(tystr *s, int32_t c, int32_t from);
tystr *ty_str_replace_str(tystr *s, tystr *a, tystr *b);
tystr *ty_str_repeat(tystr *s, int32_t n);
tystr *ty_str_strip(tystr *s);
tystr *ty_str_strip_leading(tystr *s);
tystr *ty_str_strip_trailing(tystr *s);
int32_t ty_str_isblank(tystr *s);
tyarr *ty_str_tochararray(tystr *s);
tyarr *ty_str_getbytes(tystr *s);
tystr *ty_str_of_chars(tyarr *chars);
tystr *ty_str_of_chars_part(tyarr *chars, int32_t off, int32_t count);
tystr *ty_str_interned(tystr *s);
/* The regex-shaped methods: Teyru has no regular expression engine, so these
   answer for a pattern that is a literal -- no metacharacter can change what it
   matches -- and fail loudly for one that is not. */
/* String.format */
tystr *ty_str_format(tystr *fmt, tyarr *args);

/* StringBuilder and StringBuffer */
void *ty_sb_insert_str(void *sb, int32_t at, tystr *s);
void *ty_sb_insert_obj(void *sb, int32_t at, void *o);
void *ty_sb_insert_int(void *sb, int32_t at, int64_t v);
void *ty_sb_insert_char(void *sb, int32_t at, uint16_t c);
void *ty_sb_insert_double(void *sb, int32_t at, double v);
void *ty_sb_insert_bool(void *sb, int32_t at, int32_t v);
void *ty_sb_insert_chars(void *sb, int32_t at, tyarr *chars);
void *ty_sb_delete(void *sb, int32_t from, int32_t to);
void *ty_sb_delete_charat(void *sb, int32_t at);
void *ty_sb_replace(void *sb, int32_t from, int32_t to, tystr *s);
void *ty_sb_reverse(void *sb);
void *ty_sb_set_charat(void *sb, int32_t at, uint16_t c);
int32_t ty_sb_charat(void *sb, int32_t at);
int32_t ty_sb_capacity(void *sb);
int32_t ty_sb_indexof(void *sb, tystr *s);
int32_t ty_sb_indexof_from(void *sb, tystr *s, int32_t from);
int32_t ty_sb_lastindexof(void *sb, tystr *s);
void *ty_sb_set_length(void *sb, int32_t n);
void *ty_sb_ensure(void *sb, int32_t cap);
tystr *ty_sb_substring(void *sb, int32_t from);
tystr *ty_sb_substring_to(void *sb, int32_t from, int32_t to);
void *ty_sb_append_float(void *sb, float v);
void *ty_sb_append_chars(void *sb, tyarr *chars);
void *ty_sb_insert_long(void *sb, int32_t at, int64_t v);
void *ty_sb_insert_float(void *sb, int32_t at, float v);
int32_t ty_sb_isempty(void *sb);
tystr *ty_str_format_arg(tyarr *args, int32_t i);

#endif /* TYRT_H */
