/* The C side of tests/native/program.teyru.
   Every symbol is the one `teyru build --native-header` printed. */
#include "tyrt.h"
#include "native.h"

int32_t tyn_Native_add_I_I(int32_t a, int32_t b) { return a + b; }

int64_t tyn_Native_fib_I(int32_t n) {
  int64_t a = 0, b = 1;
  for (int32_t i = 0; i < n; i++) {
    int64_t t = a + b;
    a = b;
    b = t;
  }
  return a;
}

void *tyn_Native_greeting_String(void *who) {
  tystr *prefix = ty_str_new("hello, ", 7);
  return ty_str_concat(prefix, (tystr *)who);
}

double tyn_Native_half_D(double v) { return v / 2.0; }

int32_t tyn_Native_scale_I(void *self, int32_t v) {
  /* the receiver is an ordinary Teyru object: read its field directly */
  struct { tyobj obj; int32_t f_factor; } *me = self;
  return v * me->f_factor;
}

int32_t tyn_Native_apply_Transform_I(void *t, int32_t v) {
  /* call the Teyru object back through its interface table */
  int32_t (*fn)(void *, int32_t) =
      (int32_t (*)(void *, int32_t))ty_itab(t, TY_SEL_TRANSFORM_TRANSFORM_I);
  return fn(t, v);
}
