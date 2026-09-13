# Lombok 相容層

Teyru 的編譯器內建 Lombok 相容層：標註（annotation）會被解析成帶參數的語法樹，
再於語意分析階段展開成**一般的 Teyru 成員**，之後與手寫程式碼走完全相同的
型別檢查與程式碼產生路徑。不需要 annotation processor，不需要 javac，也不會有
AST 注入。

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
System.out.println(p.getName())        // ada
System.out.println(p)                  // Person(name=ada, age=36)
```

使用方式：`import lombok.X`（或直接寫 `@lombok.X`）。匯入只是為了可讀性，
編譯器以註解的**簡單名稱**比對，所以 `@Data`、`@lombok.Data`、
`@lombok.experimental.UtilityClass` 都認得。

---

## 1. 支援狀態總表

| 註解 | 狀態 | 說明 |
|---|---|---|
| `@Getter` | ⚠️ 部分 | 含 `AccessLevel`（只讀位置形式）、`@Accessors` 影響命名；`lazy = true` 的行為與 Lombok 不同，且原生型別不支援（見 §2） |
| `@Setter` | ✅ 完整 | 含 `AccessLevel`（只讀位置形式）、`@Accessors(chain)`；**不會**像 Lombok 那樣為 `@NonNull` 欄位補上檢查（見 §3） |
| `@ToString` | ⚠️ 部分 | `of`／`exclude`／`callSuper`／`includeFieldNames`；`onlyExplicitlyIncluded` 無效，`callSuper` 的格式也與 Lombok 不同（見 §2） |
| `@EqualsAndHashCode` | ⚠️ 部分 | `of`／`exclude`／`callSuper`；`onlyExplicitlyIncluded` 無效，`hashCode` 的常數與 `canEqual` 也與 Lombok 不同（見 §2） |
| `@NoArgsConstructor` | ⚠️ 部分 | `staticName` 會產生靜態工廠；`access` 只讀位置形式，而且類別沒有手寫建構子時產生的那一個會被隱含的無參數建構子擋掉，等於沒作用（見 §3） |
| `@RequiredArgsConstructor` | ✅ 完整 | final（無初始值）與 `@NonNull` 欄位 |
| `@AllArgsConstructor` | ✅ 完整 | 略過已有初始值的 final 欄位 |
| `@Data` | ⚠️ 部分 | getter + setter + `@RequiredArgsConstructor` + `@ToString` + `@EqualsAndHashCode`；它的隱含建構子不收 `@NonNull` 欄位（Lombok 會，見 §2） |
| `@Value` | ⚠️ 部分 | private final 欄位、getter、全參數建構子、`staticConstructor`；**類別攔不住繼承**（見 §3） |
| `@Builder` | ⚠️ 部分 | 類別與建構子；`builderMethodName`／`buildMethodName`／`builderClassName`／`toBuilder`／`@Builder.Default`／`@Builder.ObtainVia(field=…)`。`setterPrefix` 不會把首字母大寫，方法上的 `@Builder` 與 `ObtainVia(method=…)` 編譯不過（見 §3） |
| `@NonNull` | ⚠️ 部分 | 對欄位有效：標了 `@NonNull` 又被 `@RequiredArgsConstructor`／`@AllArgsConstructor` 收進建構子時會插檢查。手寫的參數要**方法本身**也標 `@NonNull` 才檢查（只標在參數上不算），手寫的建構子與 `@Setter` 產生的 setter 都不檢查 |
| `@With` | ⚠️ 部分 | 欄位上的 `@With` 產生 `withX(T)`，以全參數建構子複製；寫在類別上不會替所有欄位產生（Lombok 會） |
| `@Accessors` | ⚠️ 部分 | `chain`／`fluent`／`prefix`；`fluent = true` 不會像 Lombok 那樣連帶把 setter 變成可鏈式（要另外寫 `chain = true`，見 §3） |
| `@FieldDefaults` | ✅ 完整 | `level`／`makeFinal` |
| `@UtilityClass` | ⚠️ 部分 | 建構子 private、成員 static；`extends` 這個類別仍然編得過（見 §3 第 7 點） |
| `@StandardException` | ⚠️ 部分 | 產生 4 個標準例外界建構子；`E(Throwable)` 沒有像 Lombok 那樣把 `cause.getMessage()` 當成自己的訊息（見 §2） |
| `@Cleanup` | ✅ 完整 | 展開為 try-with-resources，任何離開路徑都會 close |
| `@SneakyThrows` | ✅ 完整 | 展開為 try/catch(Throwable) 後重拋 |
| `@Synchronized` | ✅ 完整 | 方法本體包進 synchronized；靜態方法用產生的 `__lock$<類別名>` 欄位 |
| `@Log` 家族 | ✅ 完整 | `@Log`／`@Slf4j`／`@Log4j`／`@Log4j2`／`@CommonsLog`／`@JBossLog`／`@Flogger`／`@XSlf4j` 都產生 `private static final Logger log`（見 §5） |
| `@ExtensionMethod` | ✅ 完整 | 找不到方法時改寫為 `Ext.method(receiver, ...)` |
| `@FieldNameConstants` | ⚠️ 部分 | 產生巢狀 `Fields` 類別；`prefix` 是加在常數的**值**上，不是加在名稱上，與 Lombok（1.18.4 以前）相反 |
| `@Delegate` | ✅ 完整 | 為欄位型別的公開方法產生委派方法 |
| `@Helper` | ⚠️ 部分 | 只是把類別標成 static，加與不加的行為完全相同；Lombok 真正要解的「方法內的區域類別」Teyru 剖析不了（見 §3） |
| `@Tolerate` | ⚠️ 部分 | 註解收得下，但沒有任何作用（見 §3） |
| `@Locked` | ✅ 完整 | 以具名鎖欄位包住方法本體 |
| `@NonFinal` | ✅ 完整 | 移除 final |
| `@PackagePrivate` | ✅ 完整 | 移除存取修飾符 |
| `@Var` | ✅ 完整 | 已棄用的 Lombok 別名，無需產生任何東西 |
| `@SuperBuilder` | ✅ 完整 | 建構子鏈上的所有欄位都在同一個 builder；見 §4 |
| `@Singular` | ✅ 完整 | 逐項加入、整批加入、清除、`build()` 取得副本；`@Singular("name")` 可改名；見 §4 |
| `@Jacksonized` | ❌ 不適用 | 沒有 Jackson，註解被接受但不產生任何東西 |
| `@Builder.ObtainVia` | ⚠️ 部分 | `field` 形式可用；`method` 形式編譯不過（見 §3） |
| `@onMethod_`／`@onParam_`／`@onConstructor_` | ✅ 完整 | 註解被複製到產生的 getter／setter 參數／建構子上（見 §3.5） |
| `@CustomLog` | ✅ 完整 | 讀 `lombok.config` 的 `lombok.log.custom.declaration`（見 §3.5） |

「完整」的定義：`tests/programs/t16`–`t19`、`t54` 有對應的測試，`go test ./...` 會驗證輸出；
`t55_lombok_every.teyru` 在一支程式裡把上表每一個 ✅ 的註解各用一次，輸出逐行比對；
`t91_lombok_log.teyru` 涵蓋 `@Log`、`@CustomLog`（含 `lombok.config`）與 `@onX` 家族。
標 ⚠️ 的是「收得下註解、但行為與 Lombok 有落差」的列。`@Helper` 與 `@Tolerate` 在 `t55`
裡各寫了一次卻都沒有作用——`t55` 的用法剛好不觸發差異，所以那兩列的 ✅ 是假的。
（寫 `t55` 時才發現 `@Builder.Default`、`@StandardException`、回傳值的 `@Synchronized`
三個「文件說完成、實際沒測過」的 bug，已修。）

同樣的理由，⚠️ 這幾列是逐條寫程式、和真的 Lombok 對跑之後才標上的：`t55` 的
`@Data` 範例剛好沒有 `@NonNull` 欄位，`@NoArgsConstructor` 也剛好是公開的形式，
所以舊表把它們列成 ✅。

---

## 2. 產生的成員長什麼樣子

以 `@Data class Person { private String name; private int age }` 為例，
展開後等同於：

```teyru
class Person {
  private String name
  private int age

  public Person() {
  }

  public String getName() {
    return this.name
  }
  public int getAge() {
    return this.age
  }
  public void setName(String value) {
    this.name = value
  }
  public void setAge(int value) {
    this.age = value
  }
  public String toString() {
    return "Person(name=" + this.name + ", age=" + this.age + ")"
  }
  public boolean equals(Object o) {
    if (this == o) {
      return true
    }
    if (o == null || !(o instanceof Person)) {
      return false
    }
    Person other = (Person) o
    if (this.name == null) {
      if (other.name != null) {
        return false
      }
    } else if (!this.name.equals(other.name)) {
      return false
    }
    if (this.age != other.age) {
      return false
    }
    return true
  }
  public int hashCode() {
    int result = 1
    result = 31 * result + (this.name == null ? 0 : this.name.hashCode())
    result = 31 * result + this.age
    return result
  }
}
```

差異說明：

- `@Getter`／`@Setter` 必須與 `@Data`、`@Value` 或類別層級註解搭配才會涵蓋所有欄位；
  寫在單一欄位上只影響該欄位。
- `@Data` 產生的建構子是 `@RequiredArgsConstructor`（final 且無初始值的欄位）。若類別
  沒有這類欄位，就是無參數建構子；要全參數建構子請同時加 `@AllArgsConstructor`。
  `@Data` **不會**把 `@NonNull` 欄位收進這個建構子（Lombok 會，並在裡面插檢查）：
  `@Data class C { @NonNull String s }` 只剩無參數建構子，`new C("x")` 是
  `TY-TYP-0072`。要那條檢查就自己加 `@RequiredArgsConstructor` 或
  `@AllArgsConstructor`——這兩個標註收 `@NonNull` 欄位是正常的。
- `@Builder` **不會**產生 getter，與 Lombok 相同。
- `@Getter(lazy = true)` 的初始值會被算**兩次**：建構子裡先算一次，第一次讀取時再算
  一次，之後才快取。Lombok 只在第一次讀取時算一次。持有欄位的型別必須是可為 null 的
  參考型別，`int` 之類的原生型別編譯不過。
- `@EqualsAndHashCode` 的 `hashCode` 用 31 與 0（Lombok 用 59 與 43），欄位順序照宣告
  順序（Lombok 會排序），而且不產生 `canEqual`——所以父類別與子類別只要欄位相同就相等，
  Lombok 會說不相等。
- `@ToString(callSuper = true)` 產生的字串是 `Child(c=2; super=Base(b=1))`，
  Lombok 是 `Child(super=Base(b=1), c=2)`：自己的欄位先寫，super 那一段在最後。
- `@StandardException` 的 `E(Throwable)` 直接 `super(cause)`，訊息留成 `null`；
  Lombok 是 `super(cause == null ? null : cause.getMessage(), cause)`，所以
  `new E(new RuntimeException("c")).getMessage()` 在 Lombok 是 `c`，在這裡是 `null`。

---

## 3. 與 Lombok 的差異（重要）

1. **沒有 annotation processor。** 展開發生在編譯器內部，`javac` 完全不參與。
2. **`@NonNull` 主要作用在欄位上。** 欄位標了 `@NonNull`、又被明寫的
   `@RequiredArgsConstructor`／`@AllArgsConstructor` 收進建構子時才插入檢查
   （`@Data` 隱含的那一個不算，見 §2）。手寫的參數是另一條路：`@NonNull` 要標在
   **方法**上才會去看參數，只標在參數上（Lombok 的正規寫法）不會有任何檢查。
   `@Setter` 產生的 setter 與手寫的建構子也都不檢查。
   Teyru 沒有欄位寫入攔截，Lombok 對「直接指派欄位」與 setter 的檢查在此都不適用。
3. **`@Singular` 傳的是可變副本**，不是 `Collections.unmodifiableList` 包裝（見 §4）。
4. **`@SuperBuilder` 產生一個攤平的 builder**，不是 builder 繼承鏈（見 §4）。
5. **`@onX` 註解只會被複製，不會被執行。** 註解字面上會掛到產生出來的成員上，
   但 Teyru 沒有 `java.lang.annotation` 的執行期，所以 `@Deprecated` 之類的標記
   不會有任何效果；需要反射讀取註解的框架在此不適用。
6. **只讀 `lombok.config` 的一個鍵。** `lombok.log.custom.declaration`
   （`@CustomLog` 用）會被讀取；其餘鍵與 `config.stopBubbling` 都不讀，搜尋一律
   走到檔案系統根目錄。
7. **`@Value` 的欄位一定是 private final**；若欄位已經有初始值，建構子不會再收它。
   但 `class Ext extends V` 編得過（Lombok 會說 `cannot inherit from final V`）：
   `final` 是在檢查繼承之後才標上去的，所以攔不住。`@UtilityClass` 的 `final` 同理。
8. **`@Builder(setterPrefix = "with")` 產生的是 `withname`**，不會把首字母大寫
   （Lombok 是 `withName`）；掛在方法上的 `@Builder` 與 `@Builder.ObtainVia(method = …)`
   都編譯不過。
9. **`@Tolerate` 沒有作用。** 撞名時的行為與它無關：建構子會直接跳過，方法則是
   `TY-TYP-0011` 重複定義的錯誤。`@Helper` 也只是把類別標成 static，行為不變，
   而 Lombok 真正要解的「方法內的區域類別」Teyru 剖析不了。
10. **`@Accessors(fluent = true)` 不會順便開啟鏈式。** Lombok 的 `fluent` 會連帶把
    setter 的回傳值改成自身，所以 `new F().n(5).n()` 在 Lombok 成立；這裡的 setter
    仍是 `void`，要鏈式得自己加 `chain = true`。
11. **建構子的 `access` 只讀位置形式。** `@AllArgsConstructor(AccessLevel.PRIVATE)`
    有效，`@AllArgsConstructor(access = AccessLevel.PRIVATE)`（Lombok 的慣用寫法）
    會被忽略而產生 `public` 建構子。`@NoArgsConstructor` 更進一步：類別沒有手寫建構子
    時，隱含的公開無參數建構子已經佔位，產生的那一個照 §6 的規則被跳過，所以
    `access` 完全沒有作用——要它生效得先自己寫一個別的建構子。

---

## 3.5 `@onX` 家族與 `@CustomLog`

### `@onMethod_`／`@onParam_`／`@onConstructor_`

這些選項把一個註解複製到另一個註解產生出來的成員上：

```teyru
class Annotated {
  @Getter(onMethod_ = @Deprecated) String name
  @Getter @Setter(onParam_ = @Deprecated) int age
}

@AllArgsConstructor(onConstructor_ = @Deprecated)
class Made {
  String a
  int b
}
```

`onMethod_` 掛到 getter 上，`onParam_` 掛到 setter 的參數上，`onConstructor_`
掛到產生的建構子上。兩種寫法都讀：Lombok 的參數形式（含 javac7 時代的 `@__(...)`
包裝）與緊鄰在旁邊的 `@onMethod_Deprecated` 裸寫法。

只有**單一個**註解讀得回來：陣列形式 `onMethod_ = {@A, @B}` 會被靜默忽略，因為剖析器
不會保留陣列引數裡的註解。

註解只是**被複製**，不會被執行——Teyru 沒有 `java.lang.annotation` 的執行期，
所以標記本身沒有作用，是給後續的編譯器階段讀的。

### `@CustomLog`

Lombok 只用 `lombok.config` 設定這一個註解。Teyru 讀
`lombok.log.custom.declaration`，格式與 Lombok 相同：

```
lombok.log.custom.declaration = MyLog MyLog.of(NAME)
```

第一個字是 logger 型別，後面是建立它的樣式；`NAME` 會被代換成掛註解的類別名稱，
`TYPE` 在 Lombok 是類別物件。**Teyru 不支援 `TYPE`**：樣式裡的引數會傳給一個
靜態工廠，而 Teyru 的 `X.class`（見 `docs/language.md`）能表達的只有名稱，
硬傳會產生一個對不上工廠參數的東西，因此回報 `TY-INT-0006` 並要求改用 `NAME`。

搜尋規則與 Lombok 相同：從來源檔所在目錄往上找最近的 `lombok.config`，每個鍵
最近的一份為準。差別是 `config.stopBubbling` 不被讀取，搜尋一律走到根目錄。

若宣告的型別找不到，回報 `TY-INT-0006`；找不到 `log` 符號時則是一般的
`TY-TYP-0048`。

---

## 4. `@Singular` 與 `@SuperBuilder`

### `@Singular`

```teyru
@Builder
class Order {
  @Singular private List<String> items
  @Singular private Map<String, Integer> counts
  @Singular("tag") private List<String> tags
}
```

產生（以 `items` 為例）：

| 成員 | 行為 |
|---|---|
| `addItems(E value)` | 第一次呼叫時建立 `ArrayList`，之後逐項加入 |
| `addItemsAll(List<E> values)` | 整批加入 |
| `clearItems()` | 清空（下次加入會重新建立） |
| `build()` | 傳入**副本**，且永遠不是 `null` |

Map 欄位的加入方法用欄位名本身：`counts(K key, V value)`、`countsAll(Map<K,V>)`、
`clearCounts()`。`@Singular("tag")` 會把加入方法改名為 `tag(E)`。

命名與 Lombok 不同：Lombok 對 `List` 欄位 `items` 產生的是單數化的 `item(E)` 與
`items(Collection)`，對 `Map` 欄位 `counts` 產生的是 `count(K,V)` 與 `counts(Map)`。
Teyru 一律是 `add<欄位名>`／`add<欄位名>All`，Map 的兩參數版本直接叫欄位名。

與 Lombok 的差別：Lombok 產生 `java.util.Collections.unmodifiableList` 包裝，
Teyru 沒有那個 API，所以傳的是一份可變副本——**物件與 builder 不共用同一個集合**，
但拿到的人仍可修改它。

### `@SuperBuilder`

```teyru
@SuperBuilder
class Animal {
  private String name
  private int legs
}

@SuperBuilder
class Dog extends Animal {
  private String breed
}

Dog d = Dog.builder().name("rex").legs(4).breed("lab").build()
```

Lombok 用「builder 繼承 builder，並以自我指涉的型別參數 `B extends Builder<B>` 回傳
自身」來讓鏈式呼叫跨層遺傳。Teyru 改成**單一攤平的 builder**：子類別的 builder 涵蓋
整條繼承鏈的欄位，`build()` 一次傳給子類別建構子，建構子再把父類別那一份往上傳。
鍊式寫法完全一樣，而且不需要泛型。

代價：`Animal.builder()` 與 `Dog.builder()` 是兩個獨立的類別，`Dog` 的 builder
不是 `Animal` 的 builder 的子類別。把 builder 當引數在繼承鏈之間傳遞的程式碼
在 Lombok 可以編譯，在這裡不行——這種寫法很少見。

## 5. 日誌註解

Teyru 沒有 SLF4J、Log4j 這些外部套件，標準程式庫提供一個簡單的 `Logger`：

```teyru
class Logger {
  public Logger(String name)
  public void trace(String msg)
  public void debug(String msg)
  public void info(String msg)
  public void warn(String msg)
  public void error(String msg)
}
```

`@Slf4j` 等註解產生的欄位是 `private static final Logger log = new Logger("類別名")`，
輸出格式為 `LEVEL 類別名 - 訊息`，寫到標準輸出。要接真正的日誌系統，請自行把
`log` 欄位換成對應的實作。

---

## 6. 展開順序

1. 類別層級的結構性註解（`@Value`、`@FieldDefaults`、`@UtilityClass`、`@Data`）先調整修飾符。
2. 成員層級註解（`@Getter`、`@Setter`、`@NonNull`、`@With`、`@Delegate` …）逐欄位處理。
3. 類別層級的產生器（`@ToString`、`@EqualsAndHashCode`、建構子、`@Builder`）最後執行。
4. 產生的成員在 vtable 配置**之前**加入，因此它們和手寫成員一樣參與覆寫與多型。

若同一個簽章已經存在（手寫或產生），**建構子**會被跳過，**方法**則是
`TY-TYP-0011` 重複定義的編譯錯誤；`@Tolerate` 不會改變這件事，它只是被接受而已。
