# JSON 與 Gson 相容層

Teyru 的 JSON 分成兩層：`lib/10_json.teyru` 是 Gson 的**樹狀 API**（純 Teyru，不需要
編譯器配合），`lib/19_gson.teyru` 加上編譯器的**物件綁定**。

## 樹狀 API（lib/10）

`JsonElement`、`JsonObject`、`JsonArray`、`JsonPrimitive`、`JsonNull`、
`JsonParser.parseString`、`Gson`、`GsonBuilder`、`JsonSyntaxException`，
行為對齊 Gson 2.10.0，包含幾個容易做錯的細節：

- `JsonNull` 繼承 `JsonElement` 而不是 `JsonPrimitive`（Gson 2.10 的改動）。
- 解析後的數字保留原始字面量，所以 `1e5` 印回來還是 `1e5`，不會變成 `100000.0`。
- 逸出規則照 Gson 的替換表：`"` 與 `\` 會逸出、五個短形式 `\b \t \n \f \r`、
  其餘小於 0x20 的用 `\u00xx`（小寫十六進位）、`/` **不**逸出、非 ASCII 以 UTF-8
  輸出而不是 `\u` 逸出，只有 U+2028／U+2029 例外；`<`、`>`、`&`、`=`、`'` 只在
  htmlSafe 的寫法（預設的 `new Gson()`）下逸出，`JsonElement.toString()` 不逸出。
- 空物件與空陣列在 pretty print 時保持一行（`{}`、`[]`）。

與 Gson 的差異（刻意的）：

- `JsonParser.parseString` 是嚴格的（RFC 8259）。Gson 寬鬆模式接受的 `{a:1}`、
  `'單引號'`、`01`、`+1`、`.5`、`1.`、`NaN`、`[1,]` 一律拒絕並丟
  `JsonSyntaxException`；空文件也拒絕，尾端多餘資料一律拒絕。
- `entrySet()` 回傳 `List<JsonMember>`、`keySet()` 回傳 `List<String>`（Gson 給的是
  `Set`）；`JsonMember` 的 `getKey`／`getValue` 對應 `Map.Entry`。
- 數字只到 `long`，沒有 `getAsBigDecimal`／`getAsBigInteger`。
- 數值存取器丟 `IllegalArgumentException` 而不是 `NumberFormatException`
  （標準程式庫沒有後者；前者是它的父類別，所以 catch 父類別的程式碼不受影響）。

## 物件綁定（lib/19 加上編譯器）

Gson 用反射把物件綁到 JSON。Teyru 沒有反射，所以**編譯器代勞**：

```teyru
import teyru.SerializedName

class Address {
  String city
  String zip
}

class Person {
  String name
  int age
  @SerializedName("home_address") Address address
}

Gson gson = new Gson()
Person p = gson.fromJson(json, Person.class)   // 不需要 cast
String out = gson.toJson(p)
```

編譯器看到 `fromJson(s, Person.class)` 就在 `Person` 上產生
`__teyruJsonRead(JsonElement)` 與 `__teyruJsonWrite()`，並把呼叫改寫成它們。因此：

- **不需要 cast**：Gson 的 `<T> T fromJson(String, Class<T>)` 在 Teyru 無法表達
  （`Class` 不是泛型），但改寫後的回傳型別就是目標類別本身。
- **欄位型別沒有映射是編譯錯誤**（`TY-TYP-0110`；`List`／`Map` 這類 prelude 類別是
  `TY-TYP-0108`），而不是執行期從反射轉接器深處拋出的 `IllegalArgumentException`。
- 綁定是**依呼叫點產生**的：沒有用到就不產生，而且欄位清單是產生當下的（Lombok
  產生的成員也在內）。

支援的欄位型別：`String`、`boolean`/`byte`/`short`/`char`/`int`/`long`/`float`/
`double` 與其裝箱類別、enum（寫成常數名稱，讀回來比對名稱，與 Gson 相同）、其他
可綁定的類別（遞迴）、`Object`（保留原始樹：讀進來的 `JsonObject` 原樣寫回去）。
`char` 和 Gson 一樣寫成單字元字串、也從字串讀回來。`@SerializedName` 可以改名。
**不支援**：`List`／`Map`／陣列欄位、`@Expose`／`@Since`／`@Until`／`@JsonAdapter`
（已宣告但未實作，用了不會有作用）。

### 執行期回退表

靜態型別是 `Object` 的參考（`gson.toJson(someObject)`）編譯器看不穿，這時走
`JsonBinding` 這張表：每個產生過綁定的類別在自己的靜態初始化裡註冊reader／
writer，查表用物件自己的類別——這正是 Gson 反射給的答案。

`Object` 可以裝著程式裡的任何類別，所以**這種呼叫點一出現，編譯器就替程式宣告的
每個類別都產生一份綁定**（介面、抽象類別、沒有無參數建構子又無法從 JSON 讀的
類別除外；那些產生不出來的就不登記，執行期問到會說沒有綁定）。沒有這個呼叫點的
程式仍然只為真正用到的類別產生綁定。

## 相關測試

- `tests/programs/t93_json.teyru` — 樹狀 API：逸出、數字、pretty print、往返。
- `tests/programs/t101_gson.teyru` — 物件綁定：巢狀類別、`@SerializedName`、
  缺欄位、執行期回退表。
- `tests/programs/t140_json_binding_edges.teyru` — 邊界：`char` 往返、`Object`
  欄位裡的樹、只透過 `Object` 傳遞的類別、enum 與未知的常數名稱。
