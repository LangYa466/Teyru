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
| `@Getter` | ✅ 完整 | 含 `lazy = true`、`AccessLevel`、`@Accessors` 影響命名 |
| `@Setter` | ✅ 完整 | 含 `AccessLevel`、`@Accessors(chain)`, `@NonNull` 檢查 |
| `@ToString` | ✅ 完整 | `of`／`exclude`／`callSuper`／`includeFieldNames`／`onlyExplicitlyIncluded` |
| `@EqualsAndHashCode` | ✅ 完整 | `of`／`exclude`／`callSuper`／`onlyExplicitlyIncluded` |
| `@NoArgsConstructor` | ✅ 完整 | `access`；`staticName` 會產生靜態工廠 |
| `@RequiredArgsConstructor` | ✅ 完整 | final（無初始值）與 `@NonNull` 欄位 |
| `@AllArgsConstructor` | ✅ 完整 | 略過已有初始值的 final 欄位 |
| `@Data` | ✅ 完整 | getter + setter + `@RequiredArgsConstructor` + `@ToString` + `@EqualsAndHashCode` |
| `@Value` | ✅ 完整 | final 類別、private final 欄位、getter、全參數建構子、`staticConstructor` |
| `@Builder` | ✅ 完整 | 類別／建構子／靜態方法；`builderMethodName`／`buildMethodName`／`builderClassName`／`toBuilder`／`setterPrefix`／`@Builder.Default`／`@Builder.ObtainVia(field=…)`／`ObtainVia(method=…)` |
| `@NonNull` | ✅ 完整 | 參數開頭檢查、setter 檢查、建構子檢查，訊息格式同 Lombok |
| `@With` | ✅ 完整 | 產生 `withX(T)`，以全參數建構子複製 |
| `@Accessors` | ✅ 完整 | `chain`／`fluent`／`prefix` |
| `@FieldDefaults` | ✅ 完整 | `level`／`makeFinal` |
| `@UtilityClass` | ✅ 完整 | 類別 final、建構子 private、成員 static |
| `@StandardException` | ✅ 完整 | 產生 4 個標準例外界建構子 |
| `@Cleanup` | ✅ 完整 | 展開為 try-with-resources，任何離開路徑都會 close |
| `@SneakyThrows` | ✅ 完整 | 展開為 try/catch(Throwable) 後重拋 |
| `@Synchronized` | ✅ 完整 | 方法本體包進 synchronized；靜態方法用產生的 `$lock` 欄位 |
| `@Log` 家族 | ✅ 完整 | `@Log`／`@Slf4j`／`@Log4j`／`@Log4j2`／`@CommonsLog`／`@JBossLog`／`@Flogger`／`@XSlf4j` 都產生 `private static final Logger log`（見 §5） |
| `@ExtensionMethod` | ✅ 完整 | 找不到方法時改寫為 `Ext.method(receiver, ...)` |
| `@FieldNameConstants` | ✅ 完整 | 產生巢狀 `Fields` 類別（含 `prefix`） |
| `@Delegate` | ✅ 完整 | 為欄位型別的公開方法產生委派方法 |
| `@Helper` | ✅ 完整 | 巢狀類別加上 static |
| `@Tolerate` | ✅ 完整 | 允許與產生出來的成員同名 |
| `@Locked` | ✅ 完整 | 以具名鎖欄位包住方法本體 |
| `@NonFinal` | ✅ 完整 | 移除 final |
| `@PackagePrivate` | ✅ 完整 | 移除存取修飾符 |
| `@Var` | ✅ 完整 | 已棄用的 Lombok 別名，無需產生任何東西 |
| `@SuperBuilder` | ✅ 完整 | 建構子鏈上的所有欄位都在同一個 builder；見 §4 |
| `@Singular` | ✅ 完整 | 逐項加入、整批加入、清除、`build()` 取得副本；`@Singular("name")` 可改名；見 §4 |
| `@Jacksonized` | ❌ 不適用 | 沒有 Jackson，註解被接受但不產生任何東西 |
| `@Builder.ObtainVia` | ✅ 完整 | `field`／`method` 兩種形式 |
| `@onMethod_`／`@onParam_`／`@onConstructor_` | ✅ 完整 | 註解被複製到產生的 getter／setter 參數／建構子上（見 §3.5） |
| `@CustomLog` | ✅ 完整 | 讀 `lombok.config` 的 `lombok.log.custom.declaration`（見 §3.5） |

「完整」的定義：`tests/programs/t16`–`t19`、`t54` 有對應的測試，`go test ./...` 會驗證輸出；
`t55_lombok_every.teyru` 在一支程式裡把上表每一個 ✅ 的註解各用一次，輸出逐行比對；
`t91_lombok_log.teyru` 涵蓋 `@Log`、`@CustomLog`（含 `lombok.config`）與 `@onX` 家族。
上表已無 ⚠️ 一列。
（寫 `t55` 時才發現 `@Builder.Default`、`@StandardException`、回傳值的 `@Synchronized`
三個「文件說完成、實際沒測過」的 bug，已修。）

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
- `@Data` 產生的建構子是 `@RequiredArgsConstructor`（final 且無初始值的欄位＋
  `@NonNull` 欄位）。若類別沒有這類欄位，就是無參數建構子；要全參數建構子請同時加
  `@AllArgsConstructor`。
- `@Builder` **不會**產生 getter，與 Lombok 相同。
- `@Getter(lazy = true)` 會產生一個 `private volatile` 的持有欄位，首次讀取時計算。
  與 Lombok 的差別：Lombok 用 `AtomicReference` 做執行緒安全初始化，
  Teyru 用 `volatile` 欄位加一次性檢查（沒有 `AtomicReference` 可用）。

---

## 3. 與 Lombok 的差異（重要）

1. **沒有 annotation processor。** 展開發生在編譯器內部，`javac` 完全不參與。
2. **`@NonNull` 只檢查參數、setter 與建構子。** Teyru 沒有欄位寫入攔截，
   Lombok 對「直接指派欄位」的檢查在此不適用。
3. **`@Singular` 傳的是可變副本**，不是 `Collections.unmodifiableList` 包裝（見 §4）。
4. **`@SuperBuilder` 產生一個攤平的 builder**，不是 builder 繼承鏈（見 §4）。
5. **`@onX` 註解只會被複製，不會被執行。** 註解字面上會掛到產生出來的成員上，
   但 Teyru 沒有 `java.lang.annotation` 的執行期，所以 `@Deprecated` 之類的標記
   不會有任何效果；需要反射讀取註解的框架在此不適用。
6. **只讀 `lombok.config` 的一個鍵。** `lombok.log.custom.declaration`
   （`@CustomLog` 用）會被讀取；其餘鍵與 `config.stopBubbling` 都不讀，搜尋一律
   走到檔案系統根目錄。
7. **`@Value` 的欄位一定是 private final**；若欄位已經有初始值，建構子不會再收它。

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
class Made { String a; int b }
```

`onMethod_` 掛到 getter 上，`onParam_` 掛到 setter 的參數上，`onConstructor_`
掛到產生的建構子上。兩種寫法都讀：Lombok 的參數形式（含 javac7 時代的 `@__(...)`
包裝）與緊鄰在旁邊的 `@onMethod_Deprecated` 裸寫法。

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
| `items(E value)` | 第一次呼叫時建立 `ArrayList`，之後逐項加入 |
| `addItemsAll(List<E> values)` | 整批加入 |
| `clearItems()` | 清空（下次加入會重新建立） |
| `build()` | 傳入**副本**，且永遠不是 `null` |

Map 欄位的加入方法用欄位名本身：`counts(K key, V value)`、`countsAll(Map<K,V>)`、
`clearCounts()`。`@Singular("tag")` 會把加入方法改名為 `tag(E)`。

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

若同一個簽章已經存在（手寫或產生），產生的成員會被跳過；`@Tolerate`
則反過來——標記手寫成員不要被產生版本覆蓋。
