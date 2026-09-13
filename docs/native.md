# 原生互通：用 C 寫 Teyru 的 native 方法

Teyru 沒有 JNI、沒有 FFI 綁定產生器，也沒有執行期解譯層。`native` 方法就是一個
C 函式：編譯器產生呼叫、產生宣告，你提供定義，兩者一起編譯成同一個執行檔。

```
program.teyru  ──┐
                 ├─► C ─► clang/LLVM ─► 原生執行檔
impl.c ──────────┘
```

## 1. 三步驟

**第一步：宣告 native 方法。** 寫在一般的類別裡，不需要 body：

```teyru
class Native {
  public static native int add(int a, int b)
  public static native String greeting(String who)
  public native int scale(int v)

  private int factor

  public Native(int factor) {
    this.factor = factor
  }
}
```

**第二步：讓編譯器產生宣告。**

```sh
teyru build --native-header native.h program.teyru -o program
```

標頭檔會在連結失敗之前就寫出來：這個命令仍然會嘗試連結，所以在你提供實作之前
它會以 `undefined reference to 'tyn_...'` 結束。看到那些符號就代表標頭檔已經產生，
接著做第三步即可。

`native.h` 只包含你必須實作的東西：

```c
#include "tyrt.h"

/* Native.add */
int32_t tyn_Native_add_I_I(int32_t a0, int32_t a1);

/* Native.greeting */
void * tyn_Native_greeting_String(void * a0);

/* Native.scale */
int32_t tyn_Native_scale_I(void *self, int32_t a0);
```

**第三步：實作，然後一起編譯。**

```c
#include "tyrt.h"
#include "native.h"

int32_t tyn_Native_add_I_I(int32_t a, int32_t b) { return a + b; }

void *tyn_Native_greeting_String(void *who) {
  tystr *prefix = ty_str_new("hello, ", 7);
  return ty_str_concat(prefix, (tystr *)who);
}

int32_t tyn_Native_scale_I(void *self, int32_t v) {
  struct { tyobj obj; int32_t f_factor; } *me = self;
  return v * me->f_factor;
}
```

```sh
teyru build --native impl.c --native-header native.h program.teyru -o program
```

`tests/native/` 有一份完整可跑的版本，`go test -run TestNative` 會編譯並執行它。

---

## 2. 符號命名

```
tyn_<類別>_<方法>_<參數型別…>
```

- 類別與方法名以 `util.Mangle` 轉寫：`.` 與 `$` 變成 `_`。
- 參數型別是描述子：`I` int、`J` long、`D` double、`F` float、`Z` boolean、
  `B` byte、`S` short、`C` char、`A` 陣列、`O` 型別變數，其他類別用其簡單名稱
  （例如 `String`）。
- 沒有參數就沒有尾綴。多載會自然得到不同的名字，不需要額外規則。

不必自己推導：`--native-header` 產生的就是這個名字。

---

## 3. 型別對應

| Teyru | C | 備註 |
|---|---|---|
| `int` | `int32_t` | |
| `long` | `int64_t` | |
| `short` / `byte` | `int16_t` / `int8_t` | |
| `char` | `uint16_t` | UTF-16 碼元 |
| `boolean` | `int32_t` | 0 或 1 |
| `float` / `double` | `float` / `double` | |
| `void` | `void` | |
| 物件、陣列、介面、泛型 | `void *` | 執行期表示法，見下節 |
| 實例方法的接收者 | `void *self`（第一個參數） | 靜態方法沒有 |

物件參數一律是 `void *`，所以 `native.h` 不依賴產生出來的 struct 名稱，
可以在任何地方 include。

---

## 4. 讀寫 Teyru 物件

物件就是 `tyobj` 開頭的 C struct，欄位依宣告順序平鋪（父類別的欄位在前）。
要讀欄位就在 C 端宣告同樣的開頭：

```c
struct { tyobj obj; int32_t f_factor; } *me = self;
int32_t f = me->f_factor;
```

`f_` 前綴是編譯器對欄位的命名，`f_factor` 就是 `private int factor`。
如果不想依賴版面，把值用一般的 Teyru getter 傳進來就好——原生的介面
越小，越不容易隨編譯器改動而失效。

執行期提供給原生程式碼的常用工具（都在 `tyrt.h`）：

| 函式 | 用途 |
|---|---|
| `ty_str_new(const char *bytes, int64_t len)` | 建立字串 |
| `ty_str_concat(tystr *a, tystr *b)` | 串接 |
| `ty_str_len(tystr *s)` | 長度 |
| `ty_array_new(int64_t len, int64_t elemsize)` | 建立陣列 |
| `ty_array_len(tyarr *a)` | 陣列長度 |
| `ty_alloc(size_t)` | 從 GC 堆積配置（會自動被回收） |
| `ty_throw(tyobj *e)` | 丟出 Teyru 例外 |
| `ty_itab(void *obj, int selector)` | 取得介面方法的函式指標 |

---

## 5. 回呼：從 C 呼叫 Teyru

`--native-header` 也會輸出介面方法的 selector（數值由編譯器配置，這裡列出的是目前的樣子）：

```c
#define TY_SEL_TRANSFORM_TRANSFORM 5

int32_t tyn_Native_apply_Transform_I(void *t, int32_t v) {
  int32_t (*fn)(void *, int32_t) =
      (int32_t (*)(void *, int32_t))ty_itab(t, TY_SEL_TRANSFORM_TRANSFORM);
  return fn(t, v);
}
```

於是 C 可以呼叫任何實作該介面的 Teyru 物件——包括 lambda：

```teyru
Transform t = x -> x * 10
System.out.println(Native.apply(t, 4))   // 40
```

---

## 6. 其他建置選項

| 旗標 | 用途 |
|---|---|
| `--native <file.c>` | 加入一個 C 檔一起編譯（可重複） |
| `--link <arg>` | 傳給連結步驟的參數，例如 `--link -lm` 或 `--link libfoo.a` |
| `--native-header <path>` | 產生 native 方法的宣告 |
| `--cc <name>` | 換 C 編譯器（預設 clang） |
| `--no-lto` | 關閉 LTO（工具鏈不支援時會自動退回） |

---

## 7. 已知限制

- **沒有自動繫結。** 標頭檔由編譯器產生，實作要自己寫；沒有 C++ 名稱修飾解析、
  沒有結構描述子、沒有記憶體佈局談判。
- **`native` 方法不能有 body。** 建構子可以是 native（`lib/02_string.teyru` 的
  `String(String original)` 就是），`--native-header` 會一併宣告它：符號是
  `tyn_<類別>__init__<參數描述子>`，實例建構子的第一個參數是 `void *self`。
- **GC 不會搬移物件，所以 C 端可以放心保存 `void *`——但只在該物件還活著的時候。**
  若要在 C 端長期持有參照，請用 `ty_gc_register_static` 註冊一個根，
  否則回收器會在下次回收時把物件收走。
- **`native.h` 會隨編譯器版本變動。** 符號命名穩定，但欄位 `f_` 版面是產生出來的；
  把它當 ABI 用就要接受這件事。
