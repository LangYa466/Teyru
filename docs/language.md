# Teyru 語言參考

本文件描述 Teyru 0.2 的語法與語意。文件以實作為準：這裡寫的每一項語言特性都在
`tests/programs/` 有對應的測試，`go test ./...` 會逐項驗證；標準程式庫的 API 則只
涵蓋一部分（例如 `Map.putAll`、`Map.keys` 還沒有測試用到），測試涵蓋範圍仍不完整。

- [1. 原始檔與詞法](#1-原始檔與詞法)
- [2. 換行與敘述終止](#2-換行與敘述終止)
- [3. 型別](#3-型別)
- [4. 宣告](#4-宣告)
- [5. 原生 property](#5-原生-property)
- [6. 陳述式](#6-陳述式)
- [7. 運算式](#7-運算式)
- [8. 泛型](#8-泛型)
- [9. lambda 與方法參照](#9-lambda-與方法參照)
- [10. 例外](#10-例外)
- [11. 標準程式庫](#11-標準程式庫)
- [12. 與 Java 的差異](#12-與-java-的差異)
- [13. 尚未實作](#13-尚未實作)

---

## 1. 原始檔與詞法

- 原始檔是 UTF-8，副檔名 `.teyru`。開頭的 BOM 會被忽略。
- 註解：`//` 行註解、`/* ... */` 區塊註解（可跨行）。
- 識別字：字母、`_`、`$` 或非 ASCII 字母開頭，其後可接數字。
- 關鍵字與 Java 相同的一組（`class`、`interface`、`enum`、`record`、`new`、`switch`…），
  另有 `var` 與 `val`（見 §3.4）。
- 字面值：整數（十進位、`0x`、`0b`、`0` 開頭八進位、`_` 分隔、`L` 後綴）、
  浮點（`f`／`d` 後綴、指數）、`char`、`String`、text block `"""…"""`、
  `true`／`false`／`null`。
- 跳脫序列：`\n \t \r \b \f \s \0-7 \uXXXX`；`\<換行>` 續行在一般字串與 text block
  都適用，未知的跳脫字元會去掉反斜線後原樣輸出（`\q` 就是 `q`，不是錯誤）。

**沒有分號。** 分號不是合法 token，會直接產生 `TY-SYN-0001`；
字串、字元、註解與 text block 內的分號是資料，不受影響。

## 2. 換行與敘述終止

詞法分析器不產生 NEWLINE token：每個 token 只記錄「前面是否有換行」。
剖析器在兩個條件同時成立時把換行當成敘述結束：

1. **換行在此處顯著**：在小括號、中括號與引數列表內不顯著
   （`f(a,\n b)` 合法）；在區塊層級、類別成員層級顯著。
2. **前綴已完整**：下列情況即使有換行也不算結束——
   - 運算式以運算子、逗號、`.`、`::`、`->`、`?`、`:` 結尾；
   - 下一行以 `.` 或 `::` 開頭（方法鏈）；
   - 分隔符尚未關閉。

實作上由 `parser.continues()` 決定；下一行以 `+ - ! ~ ( [ { @ <` 開頭時**不**延續，
避免「下一行是新敘述」與「上一行還沒寫完」混淆。

特殊情況：

- `return` 後直接換行＝無值回傳；要回傳值，運算式必須從同一行開始，
  或用 `return (` 讓它跨行。
- `throw` 與需要值的 `yield` 同理。
- `++`／`--` 不跨行附著：後綴運算子必須與運算元同行。
- 不能在行末用 `;` 塞多個敘述，請分行。
- `do { … } while (c)` 之後不加分號。

## 3. 型別

### 3.1 原生型別

`boolean byte short char int long float double void`，與 Java 相同的大小與範圍。

### 3.2 參考型別

類別、介面、enum、record、陣列、型別變數。`Object` 是所有類別的根，
`null` 可指派給任何參考型別。

### 3.3 陣列

`T[]`、`T[][]`、`new int[10]`、`new int[2][3]`（會建立內層陣列）、
`new String[]{"a","b"}`、`{1,2,3}` 初始化列表。陣列有 `length` 欄位與
`clone()` 方法；元素存取會做邊界檢查（讀取與寫入都是，null 陣列先丟
`NullPointerException` 再檢查邊界，順序與 Java 相同）。

陣列是共變的（`Object[] o = new String[2]` 合法），但建立時就記下元素型別，
所以透過較寬的視角寫入不符合的值會丟 `ArrayStoreException`：

```teyru
Object[] o = new String[2]
o[0] = "hello"
o[0] = Integer.valueOf(5)   // ArrayStoreException
```

### 3.4 `var` 與 `val`

```teyru
var n = 10        // int，可重新指派
val name = "ada"  // String，不可重新指派
```

- 兩者都只能用在區域變數（含增強 `for` 的變數與 try-with-resources），
  不可用於欄位、參數或回傳型別。
- 一定需要初始值；`null` 推斷不出型別；lambda 需要目標型別。
- `val` 是「不可重新綁定」，不是深度不可變。
- 欄位與參數一律要寫出型別。

## 4. 宣告

### 4.1 類別、介面、enum、record

```teyru
class Base {
  protected int value
  public Base(int v) {
    value = v
  }
  public int get() {
    return value
  }
}

class Derived extends Base implements Comparable<Derived> {
  public Derived(int v) {
    super(v)
  }
  @Override
  public int compareTo(Derived o) {
    return get() - o.get()
  }
}

interface Greeter {
  String greet(String who)
  default String hello() {
    return greet("world")
  }
}

enum Color {
  RED, GREEN, BLUE
}

enum Planet {
  EARTH(1), MARS(2)

  :  // 常數區與成員區的分隔冒號
  private final int rank
  Planet(int rank) {
    this.rank = rank
  }
  public int rank() {
    return rank
  }
}

record Point(int x, int y) {
}
```

- 修飾符：`public protected private static final abstract native synchronized
  transient volatile strictfp sealed non-sealed default`。
- 巢狀類別、內部類別（有外層實例）、區域類別、匿名類別都支援。
- `enum` 的常數區與成員區之間用**一個冒號**分隔；沒有成員時省略冒號；
  沒有常數但有成員時以冒號開頭。
- `record` 自動產生私有 final 欄位、accessor、`toString`、`hashCode`、`equals`
  與標準建構子；也可寫精簡建構子（compact constructor）補驗證。
- `annotation` 型別可以宣告並使用，但沒有執行期反射。

### 4.2 欄位與方法

```teyru
class Counter {
  private int count          // 一般 Java 欄位
  public static final int MAX = 100
  public int step = 1        // 有初始值的欄位

  public void inc() {
    count += step
  }
  public static Counter create() {
    return new Counter()
  }
  public Counter() {
  }
}
```

- 靜態與實例初始化區塊：`static { … }` 與 `{ … }`。
- 建構子可以多載；`this(...)`／`super(...)` 必須是建構子第一句。
- 可變參數：`void log(String fmt, Object... args)`。
- 抽象方法只能在抽象類別或介面中；介面方法有 body 時必須是
  `default`、`static` 或 `private`。

## 5. 原生 property

欄位宣告後面接 accessor 區塊就成為 property：

```teyru
class Person {
  public String name        // 普通欄位
  private int age
  public int years {        // property
    get {
      return field          // field = 底層儲存
    }
    set {
      field = value < 0 ? 0 : value
    }
  }
  public String label {     // 只有 getter 的計算 property
    get {
      return name + " (" + age + ")"
    }
  }
  public Person(String name, int age) {
    this.name = name
    this.age = age
  }
}
```

規則：

| 主題 | 行為 |
|---|---|
| 儲存 | 有初始值、有預設 accessor、有 setter，或 accessor 內用到 `field` 時才需要儲存；否則為計算 property，且不能用 `final`／`volatile`／`transient`。 |
| `field` | 只在该 property 的 accessor 內代表底層儲存；其他地方的 `field` 還是一般識別字。 |
| 可見性 | property 的修飾符是 accessor 的預設可見性；底層儲存一律 `private`。 |
| 存取 | `p.years` 讀取呼叫 getter，`p.years = v` 呼叫 setter，`p.years += 1` 先 getter 再 setter。物件初始化與建構子內的指派直接寫入儲存，不呼叫 setter。 |
| 命名 | JavaBeans：getter `getX`（`boolean` 可用 `isX`），setter `setX`。也可以直接呼叫 `p.getYears()`。 |
| 繼承 | accessor 參與覆寫、可視性與泛型代換，與一般方法相同。 |
| `final` | `final` property 不能有 setter。 |
| 靜態 | `static` property 的 accessor 也是靜態。 |

## 6. 陳述式

### 6.1 基本 for 使用冒號

```teyru
for (int i = 0 : i < 10 : i++) {
  System.out.println(i)
}
for ( : : ) {          // 無窮迴圈
  break
}
```

三段用**兩個頂層冒號**分隔。括號、中括號、大括號內以及三元運算子的 `:`
不會被當成分隔符，所以 `for (int i = 0 : i < n ? a : b : i++)` 合法。

### 6.2 增強 for

```teyru
for (String s : names) { … }
for (var s : names) { … }
for (int v : new int[]{1,2,3}) { … }
```

支援陣列與 `Iterable`。

### 6.3 try-with-resources

資源以**換行**分隔，不用分號：

```teyru
try (
  Reader r = open("a.txt")
  Writer w = create("b.txt")
) {
  copy(r, w)
}
```

關閉順序是宣告的反序，與 Java 相同。

### 6.4 switch

支援陳述式與運算式、`->` 與 `:` 兩種形式、多標籤、enum、字串、
型別 pattern 與 `when` 守衛：

```teyru
switch (cmd) {
  case "up", "north":
    move(0, 1)
    break
  case "down":
    move(0, -1)
    break
  default:
    break
}

String label = switch (n) {
  case 1, 2 -> "low"
  case 3 -> "high"
  default -> "none"
}

String kind = switch (obj) {
  case String s -> "string:" + s.length()
  case Integer i when i.intValue() > 10 -> "big"
  case Integer i -> "small"
  default -> "other"
}
```

- `:` 形式保有 Java 的 fall-through；`->` 形式不會。
- 同一個 switch 不能混用兩種形式。
- switch 運算式若沒有任何 case 命中且沒有 `default`，執行期會拋出
  `IllegalStateException`。

### 6.5 其他

`if`／`else`、`while`、`do…while`（結尾不加分號）、`return`、`break`／`continue`
（可加標籤）、`throw`、`yield`、`assert`、`synchronized (lock) { … }`、
標籤陳述式。

## 7. 運算式

- 完整運算子優先序與 Java 相同：`||` `&&` `|` `^` `&` `==` `!=`
  `< > <= >= instanceof` `<< >> >>>` `+ -` `* / %`，一元、後綴、三元、指派。
- 整數除法與取餘數會檢查除零（丟 `ArithmeticException`）。
- `==` 在參考型別上是**參照相等**，與 Java 相同；`String` 的 `==` 也是參照相等，
  要比內容請用 `equals`。
- 字串串接：`+` 的任一側是 `String` 時就做串接，其他運算元會自動轉成字串
  （`null` 變成 `"null"`）。
- `instanceof` 支援型別 pattern：`if (o instanceof String s) { … }`，以及 record 解構
  pattern：`if (o instanceof Point(int x, int y)) { … }`。
- **原生型別 pattern**（JEP 507）：`if (o instanceof int i)`、`case byte b ->`。
  配對條件是**轉換精確**：`Integer(42)` 可以匹配 `int`、`long`、`double`，也可以
  匹配 `byte`，但 `Integer(300)` 不匹配 `byte`；`16777217` 不匹配 `float`
  （會失真），`16777216` 則匹配。`boolean` 只和 `Boolean` 配對，`null` 一律不匹配。
  原生型別 pattern 一定要有變數名稱。

  ```teyru
  String kind = switch (o) {
    case int i when i > 100 -> "large int"
    case int i -> "int " + i
    case double d -> "double " + d
    default -> "other"
  }
  ```
- `Interface.super.method()` 會靜態綁定到該介面的 default 實作：
  `A.super.hello()`；介面必須是當前類別的 super interface。
- cast：數值間做轉換，參考型別間做執行期檢查（失敗丟 `ClassCastException`）。
- boxing／unboxing 自動發生，`null` 拆箱會丟 `NullPointerException`。
- 物件初始化列表：`new int[]{…}`、`int[] xs = {1,2,3}`、巢狀 `{{1,2},{3}}`。

## 8. 泛型

```teyru
class Box<T> {
  private T value
  public Box(T v) {
    value = v
  }
  public T get() {
    return value
  }
}

interface Mapper<A, B> {
  B map(A a)
}

class Util {
  static <T> T first(T[] xs) {
    return xs[0]
  }
}
```

- 支援型別參數、bound（`<T extends Number>`）、多重 bound（`&`）、
  萬用字元（`?`、`? extends`、`? super`）、泛型方法、diamond `new Box<>("x")`。
- 泛型方法可以由引數推斷型別參數（原生引數會自動 boxing），也可以顯式指定：
  `Main.<String>identity("x")`、`box.<Integer>map(v -> v.length())`。
- **泛型在編譯期抹除**：執行期只知道類別，不會有 `ClassCastException` 之外的
  泛型檢查；`List<String>` 與 `List<Integer>` 在執行期是同一個型別。
- 原生型別不能當型別引數（`Box<int>` 不合法），請用包裝類別。

## 9. lambda 與方法參照

```teyru
interface Fn<R> {
  R apply(int v)
}

Fn<Integer> f = (v) -> v + 1
Fn<Integer> g = v -> v * 2          // 單一無型別參數可省略括號
Fn<Integer> h = (int v) -> {
  return v - 1
}
Fn<Integer> m = Main::twice         // 靜態方法
Fn<String>  c = String::valueOf     // 多載以目標型別決定

interface Maker<T> {
  T make()
}
Maker<Rect> s = Rect::new           // 建構子參照（目標介面需自行宣告）
```

- 目標型別必須是**函式介面**（只有一個抽象方法的介面）。
- lambda 捕獲外部區域變數時，會複製到合成類別的欄位；被捕獲的變數可以
  在 lambda 之後繼續使用，但**寫入的變數不會回寫**（與 Java 相同，
  差別是 Teyru 不要求變數是 effectively final 才能捕獲）。
- 方法參照支援：`Type::staticMethod`、`obj::instanceMethod`、
  `Type::instanceMethod`（未綁定型，第一個參數當受體）、`Type::new`。

## 10. 例外

```teyru
try {
  risky()
} catch (IllegalArgumentException | IllegalStateException e) {
  recover()
} catch (Exception e) {
  log(e.getMessage())
} finally {
  cleanup()
}
```

- `Throwable` 家族：`Exception`、`RuntimeException`、`NullPointerException`、
  `ArithmeticException`、`ArrayIndexOutOfBoundsException`、`ClassCastException`、
  `IllegalArgumentException`、`IllegalStateException`、`NoSuchElementException`、
  `NegativeArraySizeException`、`ArrayStoreException`、`AssertionError`、
  `UnsupportedOperationException`。
- 讀取 null 參考的欄位、呼叫 null 參考的方法、對 null 參考賦值都會丟
  `NullPointerException`。
- `catch` 多型別用 `|`；`finally` 一定會執行（含 catch 內再拋出的情況）。
- **沒有 checked exception 檢查**：`throws` 會被剖析但不強制。
- 未捕捉的例外會印出訊息並以狀態 1 結束。

## 11. 標準程式庫

標準程式庫以 Teyru 撰寫（`lib/` 下的 `*.teyru`），內容如下：

| 類別 | 內容 |
|---|---|
| `Object` | `toString`、`hashCode`、`equals`、`getClass` |
| 陣列 | `length`、元素存取、`clone`；`toString` 印成 `[array]`（Java 是 `[I@<hash>`；Teyru 的陣列不帶元素型別，印不出 `[I` 這種拼法） |
| `String` | `length`、`charAt`、`isEmpty`、`equals`、`hashCode`、`indexOf`、`substring`、`toUpperCase`、`toLowerCase`、`trim`、`contains`、`startsWith`、`endsWith`、`replace`、`compareTo`、`concat`、`valueOf`（多載） |
| `StringBuilder` | `append`（String／Object／int／long／char／double／boolean）、`toString`、`length` |
| `Math` | `PI`、`abs`、`max`、`min`、`sqrt`、`pow`、`floor`、`ceil`、`round`、`random` |
| `System` | `out`、`err`、`currentTimeMillis`、`nanoTime`、`exit`、`arraycopy` |
| `PrintStream` | `print`／`println`（String／Object／int／long／double／boolean／char／無參數） |
| `Number` | `Byte`、`Short`、`Integer`、`Long`、`Float`、`Double` 的共同父類別，六個轉換 `intValue`／`longValue`／`doubleValue`／`floatValue`／`byteValue`／`shortValue`（窄化依 Java 規則） |
| 包裝類別 | `Byte`、`Short`、`Integer`、`Long`、`Float`、`Double`、`Character`、`Boolean`：`valueOf`、`parseXxx`、`xxxValue`、`compareTo`、`equals`、`hashCode`、`toString` |
| 介面 | `Cloneable`、`Comparable<T>`、`AutoCloseable`、`Iterable<T>`、`Iterator<T>` |
| `Enum<E>` | `ordinal`、`name`、`compareTo`、`toString`、`hashCode`、`equals` |
| `Record` | 所有 record 的根 |
| `List<T>` | `size`、`get`、`add`、`addAll`、`isEmpty`、`contains`、`indexOf`；繼承 `Iterable<T>` |
| `ArrayList<T>` | `List<T>` 的實作；可加倍成長，另有 `set`、`removeAt`、`clear`、`addAll`、`toString` |
| `Map<K,V>` | `get`、`put`、`putAll`、`containsKey`、`remove`、`size`、`isEmpty`、`keys` |
| `HashMap<K,V>` | `Map<K,V>` 的實作；另有 `toString` |
| `Logger` | `trace`／`debug`／`info`／`warn`／`error` |

集合以 Teyru 撰寫，因此 `for` 迴圈直接支援：

```teyru
List<String> names = new ArrayList<String>()
names.add("ada")
names.add("grace")
for (String n : names) {
  System.out.println(n)
}
```

`for (int v : listOfInteger)` 會自動拆箱。沒有 `printf`、沒有正規表達式、
沒有檔案 I/O。

需要自己的原生程式庫時，`native` 方法可以實作在 C 裡，見
[docs/native.md](native.md)。

## 12. 與 Java 的差異

1. **沒有分號**（`TY-SYN-0001`）。
2. `for` 標頭用冒號：`for (init : condition : update)`。
3. try-with-resources 以換行分隔資源。
4. enum 常數區與成員區用一個冒號分隔。
5. **原生 property**：欄位加 accessor 區塊；`field` 代表底層儲存。
6. **`val`**：推斷型別的不可重綁區域變數。
7. 捕獲的區域變數不要求 effectively final。
8. 沒有 annotation processor、沒有執行期反射、沒有 JNI。
9. 泛型與 checked exception 的規則同 Java，但沒有 checked 檢查。
10. 同名區域類別：Java 把區域類別限縮在它的區塊（JLS 6.3），所以同一個類別的
    兩個方法可以各宣告一個 `class Local`；Teyru 以簡單名稱透過外圍型別解析，
    這種寫法會回報 `TY-TYP-0001`。
11. lambda 的型別引數推論不會從主體回推，`f.compose(v -> v * 10)` 這種沒有目標
    型別的寫法需要寫出型別見證（javac 也拒絕該例，只是訊息不同）。

## 13. 尚未實作

- checked exception 的編譯期檢查（`throws` 只被解析）
- `sealed` 的 `permits` 子句沒有被驗證：沒有 `permits` 的 sealed 型別在
  switch 窮盡性上被視為不可判定而要求 `default`；switch **陳述式**的窮盡性
  仍從寬
- 反射、執行緒、檔案與網路 I/O
- 與 Java 生態互通（JAR、JDK 類別庫、JNI）
- 識別字中的 Unicode 逸出（`\u0041` 不能拼出識別字）
- 泛型建構子的顯式型別引數 `new <T>Foo(...)`
- 文字區塊的縮排細則（目前實作最小縮排去除）
- 註解的執行期保留與讀取（`java.lang.annotation` 不存在；Lombok 的 `@onX`
  只把註解複製到產生的成員上，不會有任何執行期效果）
- 模組系統的語意（`import module X` 會被剖析後忽略，執行期沒有模組系統；`module-info` 不支援）
- 陣列的執行期元素型別一律是 `teyru.Array`，所以 `String[].class` 與
  `int[].class` 是同一個物件（Java 是兩個）
- 標準程式庫缺口：`String.lines()`（需要 `Stream`）、`List.of(...)`
