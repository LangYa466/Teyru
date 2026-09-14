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
- 跳脫序列：`\n \t \r \b \f \s \0-7 \uXXXX` 與 `\\ \' \"`；`\<換行>` 續行在一般字串
  與 text block 都適用。這份清單以外的跳脫序列是錯誤，會得到 `TY-SYN-0011`
  （`\q` 不是 `q`）。

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
- `throw` 同理：運算式沒接在同一行就是 `TY-SYN-0003`。`yield` 也拿不到下一行的值，
  但它是被當成識別字，訊息因此是 `cannot find symbol yield`。
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
- annotation 型別（`@interface`）可以宣告並使用，但沒有執行期反射；`annotation`
  不是關鍵字。

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
- 建構子可以多載；`this(...)`／`super(...)` 只能寫在建構子裡，但**不必是**第一句——
  `super()` 之前的敘述會先執行（JEP 513，Java 25 的彈性建構子主體），實際輸出與
  javac 25 相同。
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
| `field` | 只在該 property 的 accessor 內代表底層儲存；其他地方的 `field` 還是一般識別字。 |
| 可見性 | property 的修飾符是 accessor 的預設可見性；底層儲存一律 `private`。 |
| 存取 | `p.years` 讀取呼叫 getter，`p.years = v` 呼叫 setter，`p.years += 1` 先 getter 再 setter。只有屬性宣告上的初始值直接寫入儲存；建構子與初始化區塊裡的指派跟其他地方一樣會呼叫 setter。 |
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
不會被當成分隔符，所以 `for (int i = a > b ? 0 : 1 : i < 3 : i++)` 合法。

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
- switch **運算式**必須窮盡：沒有 `default` 又沒有涵蓋所有值時是 `TY-TYP-0096`
  編譯錯誤（`int`／`String` 選擇子一律要 `default`；列舉選擇子要列完每一個常數），
  不會產生一個默默算成零值的結果。switch **陳述式**沒有這個要求。

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
  配對條件是**轉換精確**，規則與 Java 相同，分選擇子的兩種型別：
  - 選擇子是**參考型別**時，只有它的包裝型別剛好等於該原生型別才匹配：
    `Integer(42)` 匹配 `int`，但不匹配 `long`、`double`、`byte`。
  - 選擇子是**原生數值**時，值能精確轉成該型別才匹配：`42` 匹配 `byte`，
    `16777217` 不匹配 `float`（會失真），`16777216` 則匹配。
  `boolean` 只和 `Boolean` 配對，`null` 一律不匹配。
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
- 對 `null` 讀**寫**欄位、呼叫方法、讀寫陣列元素（含取 `length`）都會丟
  `NullPointerException`。
- `catch` 多型別用 `|`；`finally` 一定會執行（含 catch 內再拋出的情況）。
- **沒有 checked exception 檢查**：`throws` 會被剖析但不強制。
- 未捕捉的例外會印出訊息並以狀態 1 結束。

## 11. 標準程式庫

標準程式庫以 **Teyru 本身**撰寫（`lib/*.teyru`），每次編譯都與使用者程式一起被
編譯與檢查——它沒有什麼特別的地位，`lib/` 底下的檔案就是用 Teyru 寫的普通程式。
標準程式庫是**一個** Teyru 套件：`teyru`。裡面的類別取 Java 的名字，所以 Teyru
程式碼寫 `import teyru.List`（或一行 `import teyru.*`），而 Java 風格的
`import java.util.List` 也照樣收——那是「Java 原始碼不改就能編」的那條路，見
下面的〈名稱怎麼找〉。

### java.lang（`lib/01`–`lib/07`）

`Object`、`Class`、`String`（`format`／`join`／`valueOf`／`compareTo`／
`startsWith`／`replace`／`split`／`strip`／`repeat`…）、`StringBuilder` 與
`StringBuffer`、`Math`（含 `floorDiv`／`floorMod`／`round`／三角函式）、
`System`（`out`／`err`／`currentTimeMillis`／`nanoTime`／`arraycopy`／`getenv`／
`exit`）、`PrintStream`、`InputStream`、`IO`（`println`／`readln`）、
`Number` 與八個包裝類別（`Integer.parseInt`、`Long.toHexString`、`Character.isDigit`
等完整靜態 API）、`Throwable` 家族、`Enum`、`Record`、`Comparable`／`Iterable`／
`Iterator`／`Cloneable`／`AutoCloseable`、`Logger`。

### java.util（`lib/08`、`lib/14_*`）

`Collection`、`List`／`ArrayList`／`LinkedList`、`Set`／`HashSet`／
`LinkedHashSet`／`TreeSet`、`Map`／`HashMap`／`LinkedHashMap`／`TreeMap`（紅黑樹）、
`SortedSet`／`NavigableSet`／`SortedMap`／`NavigableMap`、`Queue`／`Deque`／
`ArrayDeque`、`Iterator`／`ListIterator`、`Arrays`、`Collections`、`Objects`、
`Optional`、`StringJoiner`。

契約照 JDK：`LinkedHashMap` 是插入序、`TreeMap` 是鍵序、`TreeSet` 的
`headSet`／`tailSet`／`subSet` 是**活的視圖**（在範圍內 `add` 會寫進原集合，
範圍外是 `IllegalArgumentException: key out of range`；`TreeMap.keySet()` 的視圖
則照 JDK 一樣拒絕新增）、`computeIfAbsent`／`merge`／`forEach` 都在。
`Stream.of(array)` 會把陣列攤成元素（與 javac 相同的多載：`of(T...)` 比 `of(T)`
更特定）。
`java.util.function`（`lib/09`）提供 `Function`／`BiFunction`／`Consumer`／
`Supplier`／`Predicate`／`Runnable`／`Comparator`。

```teyru
List<String> names = new ArrayList<String>()
names.add("ada")
for (String n : names) {
  System.out.println(n)
}
```

### 其他套件

| 套件 | 檔案 | 內容 |
|---|---|---|
| `java.time` | `lib/20` | `LocalDate`／`LocalTime`／`LocalDateTime`／`Instant`／`Duration`／`Period`／`DayOfWeek`／`Month`；曆法算在 epoch day 上，輸出與 JDK 逐位元組相同（沒有時區，`now()` 讀 UTC） |
| `java.io` | `lib/16` | `File`、`Path`／`Paths`、`Files`（`readString`／`writeString`／`readAllLines`／`exists`／`createDirectories`／`listFiles`） |
| `java.util.regex` | `lib/21` | `Pattern`／`Matcher`：回溯式比對，支援字面值、`.`、`*`／`+`／`?`／`{n,m}` 與其懶惰形式、字元類別、`\d`／`\w`／`\s`、`^`／`$`、`|`、捕獲與非捕獲群組、`replaceAll`／`replaceFirst`／`split`（含 `limit` 的三種正負號）；不支援的語法（佔有量詞、前後視、反向參考、`\p{...}`）在 `compile` 就被拒絕。`String.matches`／`replaceAll`／`replaceFirst`／`split` 就是這五個方法，不是另一套實作 |
| `java.net` | `lib/15` | `ServerSocket`、`Socket`、`SocketInputStream`／`SocketOutputStream`；同步阻塞的 POSIX socket，逾時以 `SocketTimeoutException` 回報 |
| `java.util.stream` | `lib/22` | `Stream`／`IntStream`／`LongStream`／`DoubleStream`、`Collectors`（26 個工廠）、`Collector`、`Spliterator`／`Spliterators`、`StreamSupport`、統計與 `OptionalInt` 家族；中間操作建管線、終端操作才拉，`Collection.stream()` 是入口 |
| `java.math` | `lib/23` | `BigInteger`（base-2^30 limb、符號與大小）、`BigDecimal`（unscaled value 與 scale）、`MathContext`、`RoundingMode`；演算法照 JDK 翻譯，因為小數位數、除法留下的 scale、進位方式都是可觀察的 |
| `java.text` | `lib/24` | `NumberFormat`／`DecimalFormat`／`DecimalFormatSymbols`（完整 pattern 語言）、`DateFormat`／`SimpleDateFormat`（四種 style 與 parse）、`DateTimeFormatter`、`MessageFormat`、`ChoiceFormat`、`ParseException`／`ParsePosition`。**沒有 `Locale`**（只做 ROOT／en-US），**沒有 `java.util.Date`**（`format`／`parse` 走 `Instant`），`format` 沒有 `FieldPosition` 多載 |
| `java.util` 其餘 | `lib/25` | `Properties`、`Random`（逐位元組照 java.util.Random）、`UUID`、`BitSet`、`StringTokenizer`、`Enumeration`、`ArrayOps`（陣列的範圍形式） |
| `com.google.gson` | `lib/10`、`lib/19` | Gson 的樹狀 API，以及由編譯器產生的物件綁定（見 [docs/json.md](json.md)） |
| 框架 | `lib/17`、`lib/18` | Spring 形狀的容器與 web 層（見 [docs/framework.md](framework.md)） |

### 名稱怎麼找

簡單名稱照 JLS 6.5.5：先看單一型別匯入（它蓋過同名的套件成員），再看檔案自己的
套件，再看 on-demand 匯入，最後才看程式整體的名字（預設套件與前綴）。兩個
`import p.*` 都提供同一個名字時是 `TY-TYP-0099`，不會照宣告順序挑一個。

**匯入本身會被檢查**（`TY-TYP-0115`）：一條 `import` 必須指向標準程式庫回答的套件
（`teyru`，以及相容用的 `java.util`、`com.google.gson`、`lombok`…，見上一節的
套件表）、這次建置裡某個檔案宣告的套件，或是完整名稱就是那條路徑的型別。名字在
Teyru 是照**簡單名稱**找的，前面寫什麼套件都一樣，所以 `import java.utli.List`
曾經是安靜地被忽略、然後照樣拿到 `List`；現在它是錯誤。模組匯入
（`example.com/dep/pkg`）由建置解析，不在此檢查範圍。

**單一型別匯入與 on-demand 匯入（`import p.*`）都可以。** 匯入寫的是套件**宣告的
名字**：在模組建置裡，一個套件的 identity 是它目錄的 import path
（`package todo` 在 `example.com/app` 裡是 `example.com/app/todo`），而 import 寫的
是 `todo`——兩種寫法都查得到。`import p.*` 提供該套件的所有公開名稱；兩個 on-demand
匯入都提供同一個名字時是 `TY-TYP-0099`。檔案**自己宣告**的型別優先於 on-demand
匯入（JLS 6.5.5.1），所以 `import teyru.*` 旁邊寫一個 `class Node` 不會被標準庫的
`Node` 蓋掉。

前綴的名字是全域的——這正是 `List`、`String` 不加 import 就能用的原因——但
**具名套件看不到預設套件**（JLS 7.4.2）。所以使用者在預設套件宣告 `class Node`
不會弄壞標準庫自己講的 `Node`；反過來，在 `package teyru` 裡宣告一個前綴已經有
的名字是 `TY-TYP-0001` 重複宣告，因為兩者的完整名稱相同。

### 沒有的東西

反射、執行緒、`java.util.concurrent`、時區資料庫、`Scanner`。這些缺席都是刻意的：它們要嘛需要執行期反射，要嘛需要一份比整個語言還大
的資料表（時區），要嘛需要語言本身沒有的東西（執行緒），要嘛——`Scanner` 就是——
做半套會比不做更糟。

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
10. 型別引數推論比 javac 弱一層，靠目標型別而不是完整的約束求解（沒有 JLS 18）：
    - lambda 的型別引數會**從主體回推**：目標是 `Fn<String, ? extends R>` 而主體是
      `s -> s.length()` 時 `R` 定為 `Integer`。反過來不行——主體本身是一個需要目標
      型別的泛型呼叫時，兩邊互相依賴，單向代入停在那裡：
      `words.stream().flatMap(w -> Stream.of(w.split(" ")))` 要先把
      `Function<String, Stream<String>>` 寫出來。
    - 引數如果只有唯一一個候選方法，會拿該參數的型別當目標——所以巢狀的泛型呼叫
      可以推出來。
    - **有自由型別變數的泛型呼叫，當它是鏈式呼叫的接收者時，拿不到目標型別**：
      `xs.sort(naturalOrder())` 要寫出型別見證（`Comparator.<String>naturalOrder()`）；
      `comparing(...).thenComparing(...)` 則是連見證都不生效，只能先放進一個有宣告
      型別的變數（`Comparator<String> c = comparing(...)` 之後 `c.thenComparing(...)`）。
      javac 對這兩種寫法都可以。
    - 顯式見證屬於它自己的呼叫：`pair(f, Builder.<Integer>make())` 的外層見證不會被
      內層覆蓋。
11. **沒有捕獲轉換**：`List<? extends Number>` 在這裡就是 `List<Number>`。Java 靠捕獲
    擋下的寫入（對 `? extends` 的容器 `add`）這裡擋不住；反過來說，Java 靠捕獲才
    能編過的讀取（`list.get(0).doubleValue()`）這裡直接可行。

## 13. 尚未實作

- checked exception 的編譯期檢查（`throws` 只被解析）
- `sealed` 的 `permits` 子句沒有被驗證：沒有 `permits` 的 sealed 型別在
  switch 窮盡性上被視為不可判定而要求 `default`；switch **陳述式**的窮盡性
  仍從寬
- 反射、執行緒（檔案與網路 I/O 有，見 `java.io`／`java.net`）
- 與 Java 生態互通（JAR、JDK 類別庫、JNI）
- 識別字中的 Unicode 逸出（`\u0041` 不能拼出識別字）
- 泛型建構子的顯式型別引數 `new <T>Foo(...)`
- 文字區塊的縮排細則（目前實作最小縮排去除）
- 註解的執行期保留與讀取（`java.lang.annotation` 不存在；Lombok 的 `@onX`
  只把註解複製到產生的成員上，不會有任何執行期效果）
- 模組系統的語意（`import module X` 會被剖析後忽略，執行期沒有模組系統；`module-info` 不支援）
- 陣列的執行期元素型別一律是 `teyru.Array`，所以 `String[].class` 與
  `int[].class` 是同一個物件（Java 是兩個）
- 標準程式庫缺口：`Scanner`（見 §11）；`String.format` 的
  `%t`／`%T`（日期時間轉換）也未實作，遇到會以 `ty_unimplemented` 停止而不是
  印出看起來合理的東西
- 無法解析的完整限定名稱（例如 `com.example.Baz.qux(x)`）會回報
  `cannot find symbol com`——訊息指向鏈的第一段而不是整條路徑
