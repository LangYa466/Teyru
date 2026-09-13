# Teyru

**繁體中文** · [简体中文](README.zh-CN.md) · [English](README.en.md) · [日本語](README.ja.md)

**Teyru 是一門獨立實作的程式語言：編譯器完全用 Go 撰寫，直接產生原生執行檔——不依賴 JVM、不依賴 javac、不產生任何 bytecode。**

Teyru 的語法對 Java 開發者高度熟悉（類別、介面、泛型、lambda、例外、record、enum、annotation），
但拿掉了分號、補上原生 property，並且用**原生機器碼**執行：編譯器把整個程式降成 C，
再交給 clang/LLVM（或 gcc）編成執行檔。執行期只有約 1500 行的 C，裡頭有自己的垃圾回收器
（conservative mark-and-sweep）、字串、陣列與例外實作，沒有任何虛擬機。

```
Teyru 原始碼 (.teyru)
      │  Go 寫的編譯器：lexer → parser → 語意分析 → C 產生器
      ▼
  generated C  ──clang（Clang 前端 + LLVM 中後端）──▶  LLVM IR  ──▶  原生執行檔
                                                          （無 JVM、無 bytecode）
```

後端是 **LLVM**：`./teyru emit-llvm` 可以直接印出 IR 模組，要接 `opt`／`llc`／自訂 pass
都沒問題；只想看 C 也可以用 `./teyru emit`。

**文件**：[語言參考](docs/language.md) · [原生互通](docs/native.md) · [Lombok 相容層](docs/lombok.md) · [診斷碼一覽](docs/diagnostics.md) · [編譯器架構](docs/architecture.md) · [工作規範](AGENTS.md)

---

## 目錄

- [為什麼比 JVM 快](#為什麼比-jvm-快)
- [快速開始](#快速開始)
- [語言速覽](#語言速覽)
- [支援的語言特性](#支援的語言特性)
- [標準程式庫](#標準程式庫)
- [編輯器與工具](#編輯器與工具)
- [專案結構](#專案結構)
- [執行期模型](#執行期模型)
- [與 Java 的差異](#與-java-的差異)
- [命令列介面](#命令列介面)
- [開發](#開發)
- [授權](#授權)

---

## 為什麼比 JVM 快

同一台機器實測（Linux x86-64、clang 22、OpenJDK 21 Temurin、每個數字取 5 次最佳）：

| 指標 | Teyru（原生） | Java（HotSpot） | 差距 |
|---|---|---|---|
| 啟動 100 次總時間 | **0.068 s**（0.68 ms/次） | 1.97 s（19.8 ms/次） | **約 29 倍快** |
| 執行檔大小 | **35 KB** | JDK 執行期約 200 MB | 約 5900 倍小 |
| 尖峰記憶體（hello） | **2.1 MB** | 50.2 MB | **約 24 倍省** |
| `bench_fib` 遞迴 | **0.0060 s** | 0.0264 s | **4.4 倍快** |
| `bench_loop` 迴圈與整數運算 | **0.0209 s** | 0.0429 s | **2.1 倍快** |
| `bench_oop` 物件與虛擬呼叫 | **0.0044 s** | 0.0253 s | **5.8 倍快** |
| `bench_string` 字串處理 | **0.0093 s** | 0.0554 s | **6.0 倍快** |
| `bench_alloc` 短命物件配置 | **0.0233 s** | 0.0308 s | **1.3 倍快** |

**為什麼會快：**

1. **沒有 JVM 啟動成本。** 沒有 class loading、沒有 JIT 暖機、沒有 GC 執行緒啟動。
   適合 CLI 工具、短命 process、容器啟動、serverless。
2. **編譯期就做完的事不留到執行期。** 泛型在編譯期抹除、方法呼叫在編譯期定址、
   字串常數靜態配置、`static final` 常數直接折疊、vtable 與介面表由編譯器填好。
3. **沒有 bytecode 解譯階段。** clang/LLVM 直接最佳化整份程式（LTO 跨模組 inline、
   常數傳播、迴圈向量化），不需要等 JIT 觀察熱點。
4. **不需要配置的物件就不配置。** 逃逸分析把「不離開所在方法」的物件放在 C 堆疊上，
   LLVM 接著把它的欄位提升成暫存器、把整個物件消除——與 JVM 的 scalar replacement
   同樣的效果，`bench_alloc` 就是靠這件事贏過 HotSpot 的。
5. **配置與邊界檢查都走行內快速路徑。** `ty_alloc` 的指標碰撞配置在標頭檔內聯，
   陣列存取只在必要時呼叫慢路徑；GC 會回收完全空掉的 chunk，類別初始化也只測一個旗標。
6. **可預測的效能。** 沒有 deopt、沒有暖機曲線、沒有 GC 調校參數，
   第一次執行就是最快速度。

**誠實的邊界。** 逃逸分析只涵蓋「不離開所在方法」的物件。會存進欄位、陣列、
回傳或交給其他物件的物件仍然走堆積與標記清除式回收，而 HotSpot 有分代假設，
所以在「物件長期存活、反覆回收」的負載上 JVM 仍可能勝出。上面的數字都含
process 啟動，絕對值都很小；重現方式見 `sh scripts/bench.sh`，五支 benchmark 程式、
啟動 100 次、執行檔大小與尖峰記憶體都由這支腳本量測（大小那一列對照的 JDK 執行期
是量測機器上安裝的執行期，不由腳本量測）。

---

## 快速開始

需要 **Go 1.26+** 與 **clang**（或 gcc）。

```sh
# 建置編譯器
go build -o teyru ./cmd/teyru

# 編譯並執行
./teyru run hello.teyru

# 產生執行檔
./teyru build -O2 -o hello hello.teyru
./hello

# 看編譯器產生的 C 程式碼
./teyru emit hello.teyru

# 看交給 LLVM 的 IR（後端是 clang/LLVM，可用 opt/llc 直接接手）
./teyru emit-llvm hello.teyru

# 版本
./teyru version
```

`hello.teyru`：

```teyru
class Hello {
  public static void main(String[] args) {
    System.out.println("Hello, Teyru!")
  }
}
```

注意：**Teyru 不用分號**。每個敘述以換行結束；`for` 標頭用兩個冒號分隔三段。

---

## 語言速覽

```teyru
interface Shape {
  double area()
  default String describe() {
    return "area=" + area()
  }
}

class Rect implements Shape {
  public double w
  public double h
  public Rect(double w, double h) {
    this.w = w
    this.h = h
  }
  public double area() {
    return w * h
  }
}

class Circle implements Shape {
  private double r
  public double radius {      // 原生 property
    get {
      return field            // field = 底層儲存
    }
    set {
      field = value < 0 ? 0 : value
    }
  }
  public Circle(double r) {
    radius = r
  }
  public double area() {
    return Math.PI * r * r
  }
}

record Point(int x, int y) {
}

enum Color {
  RED, GREEN, BLUE
}

interface Fn<R> {
  R apply(int v)
}

class Main {
  static int twice(int v) {
    return v * 2
  }

  public static void main(String[] args) {
    Shape s = new Rect(3, 4)
    System.out.println(s.describe())

    // for ( init : condition : update )
    for (int i = 0 : i < 3 : i++) {
      System.out.println(i)
    }

    int[] xs = {1, 2, 3}
    for (int x : xs) {
      System.out.print(x)
    }
    System.out.println()

    // lambda 與方法參照
    Fn<Integer> f = (v) -> v + 1
    Fn<Integer> g = Main::twice
    System.out.println(f.apply(41))
    System.out.println(g.apply(21))

    // switch 運算式與型別 pattern
    Color c = Color.GREEN
    String name = switch (c) {
      case RED -> "red"
      case GREEN -> "green"
      default -> "other"
    }
    System.out.println(name)
    System.out.println(describe(c))

    // 例外
    try {
      System.out.println(10 / 0)
    } catch (ArithmeticException e) {
      System.out.println("除以零")
    } finally {
      System.out.println("cleanup")
    }
  }

  static String describe(Object o) {
    return switch (o) {
      case String s -> "string of length " + s.length()
      case Integer i when i.intValue() > 10 -> "big int"
      case Integer i -> "small int"
      default -> "other"
    }
  }
}
```

### Java 25 語法對照

Teyru 以 Java SE 25 的最終定案語法為基準（預覽功能不算），保留 Java 的語意，
只拿掉分號並加上原生 property。已實作並有測試的 Java 25 項目：

| JEP | 功能 | 狀態 |
|---|---|---|
| 512 | 精簡原始檔、實例 `main`、隱式 `java.io.IO`（`println`／`print`／`readln`） | ✅ |
| 511 | `import module java.base`（解析後忽略，執行期沒有模組系統） | ✅ 解析 |
| 513 | 彈性建構子本體（`super()` 之前可以有敘述） | ✅ |
| 440 | Record 模式（含巢狀解構、`instanceof` 版本） | ✅ |
| 441 | switch 的模式比對與 `when` 守衛 | ✅ |
| 507 | 原生型別 pattern（`case int i`、`o instanceof int i`，精確轉換語意） | ✅ |
| 456 | 未命名變數與模式 `_` | ✅ |
| 395 | record（含精簡建構子） | ✅ |
| 394 | `instanceof` 型別模式 | ✅ |
| 409 | sealed 類別（`sealed`／`permits`／`non-sealed`） | ✅ 解析 |
| 378 | 文字區塊 | ✅ |
| 361 | switch 運算式 | ✅ |
| 286 | `var` 區域變數推斷 | ✅ |

### Lombok 相容層

編譯器內建 Lombok：標註會在語意分析階段展開成一般的 Teyru 成員，與手寫程式碼走同一條
型別檢查與程式碼產生路徑，不需要 annotation processor。

```teyru
import lombok.Data
import lombok.AllArgsConstructor
import lombok.Builder

@Data
@AllArgsConstructor
@Builder
class Person {
  private String name
  private int age
}

Person p = Person.builder().name("ada").age(36).build()
System.out.println(p.getName() + " " + p.getAge())
System.out.println(p)
```

完整清單與差異見 **[docs/lombok.md](docs/lombok.md)**：`@Getter`／`@Setter`／`@ToString`／
`@EqualsAndHashCode`／`@Data`／`@Value`／`@Builder`／`@NonNull`／`@Cleanup`／
`@SneakyThrows`／`@Synchronized`／`@With`／`@Accessors`／`@FieldDefaults`／
`@UtilityClass`／`@StandardException`／`@Log` 家族／`@ExtensionMethod`／
`@FieldNameConstants`／`@Delegate`／`@Helper`／`@Tolerate`／`@Locked`／
`@NonFinal`／`@PackagePrivate` 全部支援，包含 `@Singular`（逐項累積、整批加入、
清除、`build()` 取得副本）、`@SuperBuilder`（涵蓋整條繼承鏈的欄位）與
`@Builder.ObtainVia`。

### 支援的語言特性

| 類別 | 內容 |
|---|---|
| 型別 | 原生型別、類別、介面、enum、record、annotation type、泛型（bound／wildcard／diamond／泛型方法）、多維陣列 |
| 成員 | 欄位、方法、建構子、可變參數、靜態與實例初始化區塊、巢狀／內部／區域／匿名類別、`sealed`／`permits` |
| 陳述式 | `if`、`while`、`do-while`、基本 `for`（冒號標頭）、增強 `for`、`switch`（陳述式／運算式、箭頭／冒號、多標籤、enum、字串、型別 pattern + `when` 守衛）、`try`／`catch`／`finally`、try-with-resources、多型別 catch、`throw`、`yield`、`assert`、`synchronized`、標籤與 `break`／`continue` |
| 運算式 | 完整運算子與優先序、三元、cast、`instanceof`（含 pattern）、lambda、方法參照（靜態／綁定／未綁定／建構子）、匿名類別、物件初始化列表、字串串接、自動 boxing／unboxing |
| 原生擴充 | 無分號語法、`val`（推斷型別的不可重綁區域變數）、`var`、原生 property（`get`／`set`／`field`）、`for` 的雙冒號標頭、try-with-resources 以換行分隔 |

完整的語法與語意寫在 **[docs/language.md](docs/language.md)**。

---

## 標準程式庫

標準程式庫以 **Teyru 本身**撰寫（`lib/*.teyru`），每次編譯都與使用者程式
一起被編譯與檢查：

`Object`、`String`、`StringBuilder`、`Math`、`System`、`PrintStream`、
`Iterable`／`Iterator`、`Comparable`、`AutoCloseable`、`Cloneable`、`Enum`、`Record`、
八種原生包裝類別（`Byte`／`Short`／`Integer`／`Long`／`Float`／`Double`／`Character`／`Boolean`）、
集合（`List`／`ArrayList`／`Map`／`HashMap`），以及 `Throwable` 家族（`Exception`、
`RuntimeException`、`NullPointerException`、`ArrayIndexOutOfBoundsException`、
`ArithmeticException`、`ClassCastException`、`IllegalArgumentException`、
`IllegalStateException`、`IndexOutOfBoundsException`、`NoSuchElementException`、
`NegativeArraySizeException`、`AssertionError`、`UnsupportedOperationException`）。

`ArrayList` 實作 `Iterable`，所以 `for (String s : names)` 與 Java 寫法一致。
沒有 `printf`、沒有檔案 I/O——這些仍是刻意的範圍限制。

需要自己的原生程式庫時，宣告 `native` 方法並用 C 實作：

```teyru
class Native {
  public static native int add(int a, int b)
}
```
```sh
teyru build --native-header native.h program.teyru   # 產生要實作的宣告
teyru build --native impl.c program.teyru            # 一起編譯
```

完整說明見 [`docs/native.md`](docs/native.md)。

---

## 編輯器與工具

- **VS Code**：`editors/vscode/` 提供 `.teyru` 的 TextMate 語法highlight、語言設定與片段。
  用 `npx @vscode/vsce package` 打包，再以 `code --install-extension teyru-0.1.0.vsix` 安裝。
- **tree-sitter**：`editors/tree-sitter-teyru/` 是完整文法，附 highlight query、縮排 query
  與 corpus 測試，Neovim、Helix、Zed 等可直接使用。
- GitHub 目前仍把 `.teyru` 顯示成 Java：linguist 還沒有 Teyru 的定義，
  `.gitattributes` 先對應到最接近的語法。

---

## 專案結構

| 路徑 | 說明 |
|---|---|
| `cmd/teyru` | CLI 進入點（`build`／`run`／`emit`／`emit-llvm`／`version`） |
| `internal/driver` | 編譯流程：串起前後端、呼叫 C 編譯器、處理 native 來源與輸出選項 |
| `internal/source` | 檔案、位置換算、診斷容器 |
| `internal/lexer` | 詞法分析；換行不產生 token，只在 token 上標記「前面有換行」 |
| `internal/parser` | 遞迴下降剖析器，以顯著性與前綴完整性判斷敘述是否結束 |
| `internal/ast` | 語法樹、符號（類別／方法／欄位／變數）、型別 |
| `internal/sema` | 名稱解析、型別檢查、泛型抹除與推斷、多載解析、vtable／selector 配置、property 降階 |
| `internal/codegen` | 產生 C：類別→struct、虛擬呼叫→vtable、介面呼叫→itable、switch 降階、GC 根資訊 |
| `internal/util` | 前後端共用的工具：名稱修飾、型別描述、C 版面配置 |
| `internal/runtime/src` | C 執行期：GC、字串、陣列、例外、boxing、Math／System／StringBuilder |
| `lib` | 以 Teyru 撰寫的標準程式庫 |
| `tests/programs` | 端到端測試程式與期望輸出（`go test` 會逐一編譯並比對） |
| `tests/native` | native 方法互通測試：Teyru 宣告 + C 實作 + 期望輸出（`TestNative`） |
| `examples` | 範例程式與 JVM 對照的 benchmark（`bench_*.teyru` 與 `.java`） |
| `scripts` | 開發腳本：`bench.sh` 效能量測、`pre-commit` 掛勾 |
| `docs` | 語言參考、診斷碼、架構 |
| `editors` | 編輯器支援：VS Code 擴充與 tree-sitter 文法 |

---

## 執行期模型

- **物件**：C struct，第一欄是 `tyobj { tyclass* cls }`。每個類別一張 `tyclass`，
  記錄父類、介面、vtable、介面表、GC 需要追蹤的參考欄位位移。
- **虛擬呼叫**：`obj->cls->vtable[slot]`；**介面呼叫**：`ty_itab(obj, selector)`。
  每個介面方法有全域唯一的 selector，每個類別的介面表由編譯期填好。
- **泛型**：編譯期抹除，執行期沒有泛型資訊（與 Java 相同）。
- **例外**：以 `setjmp`／`longjmp` 實作的 handler 鏈；`finally` 以巢狀 handler
  保證在任何路徑（含 catch 內再拋出）都執行。
- **GC**：保守式標記清除。根包含原生堆疊（保守掃描）、靜態欄位註冊表與暫存器
  （`setjmp` 溢出）。物件不搬移，所以 C 端的暫存指標永遠有效。
- **字串**：UTF-8 `tystr { tyobj obj; int64 len; char* data }`；字面值是靜態物件，
  不經 GC。
- **陣列**：`tyarr { tyobj; len; data; esize; refs }`，元素內嵌在物件後方。

---

## 與 Java 的差異

Teyru 不是 Java 的子集，而是「Java 開發者一看就懂」的獨立語言。主要差異：

1. **沒有分號。** 分號會被編譯器拒絕（`TY-SYN-0001`）。
2. **`for` 標頭用冒號**：`for (int i = 0 : i < n : i++)`。
3. **try-with-resources 用換行分隔**，不用分號。
4. **enum 常數區與成員區用一個冒號**分隔（沒有成員時可省略）。
5. **原生 property**：欄位後面接 accessor 區塊即成 property；`field` 代表底層儲存。
   沒有 accessor 區塊的欄位就是普通 Java 欄位。
6. **`val`**：推斷型別的不可重綁區域變數（不是深度不可變）。
7. **沒有 checked exception 檢查**；`throws` 會被剖析但不強制。
8. **沒有 `System.out.printf`、沒有執行期反射、沒有 annotation processor**。
9. **不是 bytecode 平台**：沒有 `.class`、沒有 `java.lang`、沒有 JNI，
   目前也**無法**與既有 Java 程式庫互通——這是刻意的取捨。

完整清單見 [docs/language.md §12](docs/language.md)。

---

## 命令列介面

```
teyru build [flags] <files...>                 編譯成原生執行檔
teyru run   [flags] <files...> [-- args...]    編譯後直接執行
teyru emit  [flags] <files...>                 印出產生的 C
teyru emit-llvm [flags] <files...>             印出交給 LLVM 的 IR
teyru version                                  版本
teyru help                                     說明
```

| 旗標 | 說明 |
|---|---|
| `-o <path>` | 輸出檔名（預設 `a.out`） |
| `-c <path>` | 保留產生的 C 檔在指定路徑 |
| `--cc <name>` | 使用的 C 編譯器（預設依序找 `clang`、`gcc`、`cc`） |
| `-O0`…`-O3` | 最佳化等級（預設 `-O2`） |
| `--llvm-ir <path>` | 額外輸出 LLVM IR 模組 |
| `--native <file.c>` | 加入 C 檔一起編譯，實作 native 方法（可重複） |
| `--native-header <path>` | 產生 native 方法的宣告（見 [docs/native.md](docs/native.md)） |
| `--link <arg>` | 傳給連結步驟的參數，例如 `--link -lm` |
| `--no-lto` | 關閉 LTO（工具鏈不支援時會自動退回） |
| `-v` | 顯示實際執行的編譯命令 |

---

## 開發

```sh
go build ./...          # 建置
go test ./...           # 端到端測試（會編譯 tests/programs 下每個程式並比對輸出）
go vet ./...
sh scripts/bench.sh       # 與 JVM 對照的效能測試（需要 java 才會跑 JVM 那一半）
```

新增測試只要在 `tests/programs/` 放 `xxx.teyru` 與 `xxx.expected`；
若程式需要命令列參數，再放 `xxx.args`（每行一個參數）。`go test` 會自動處理。

貢獻前請讀 [AGENTS.md](AGENTS.md)。

---

## 授權

見 [LICENSE](LICENSE) 與 [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md)。
