# Teyru 診斷碼一覽

每個診斷都有穩定代碼，格式為 `TY-<階段>-<四位數字>`。階段前綴：

| 前綴 | 階段 | 代表 |
|---|---|---|
| `TY-SYN` | 詞法與語法分析 | 分號、括號、換行、字面值 |
| `TY-TYP` | 語意分析（名稱、型別、成員） | 找不到符號、型別不符、多載 |
| `TY-PROP` | 原生 property 規則 | accessor 衝突、儲存需求 |
| `TY-INT` | 編譯器內部／前導程式庫 | 前導程式庫損壞、未支援的節點 |
| `TY-IO` | 檔案存取 | 讀不到來源檔 |

輸出格式為 `檔案:行:欄: error[碼]: 訊息`，例如：

```
hello.teyru:4:11: error[TY-TYP-0051]: incompatible types: String cannot be converted to int
```

`TY-SYN-0001`、`0002`、`0004`–`0010` 由詞法分析器產生（`TY-SYN-0003` 例外：它是剖析器
在敘述結尾與 `throw` 換行時發出的），`TY-SYN-0100` 之後也由剖析器產生。

---

## TY-SYN：詞法與語法

| 代碼 | 訊息 | 說明與修法 |
|---|---|---|
| TY-SYN-0001 | `';' is not Teyru syntax; end statements with a newline` | Teyru 沒有分號。刪掉分號，讓敘述以換行結束；`for` 標頭改用冒號分隔。 |
| TY-SYN-0002 | `unexpected character %q` | 出現不屬於任何 token 的字元（多半是全形標點或貼上的控制字元）。 |
| TY-SYN-0003 | `expected end of line, found %s`／`throw expression must start on the same line` | 敘述後面還有殘餘 token；或 `throw`／需要值的 `yield` 的運算式被換行切斷。把運算式寫在同一行，或用 `(` 開頭讓它跨行。 |
| TY-SYN-0004 | `unterminated block comment` | `/*` 沒有對應的 `*/`。 |
| TY-SYN-0005 | `invalid unicode escape` | `\uXXXX` 不是四位十六進位。 |
| TY-SYN-0006 | `unterminated string literal`／`unterminated text block` | 字串在換行前沒有收尾，或 text block 少了結尾的 `"""`。 |
| TY-SYN-0007 | `text block must start with a line break after """` | `"""` 之後必須立刻換行。 |
| TY-SYN-0008 | `unterminated character literal`／`character literal does not fit in a char` | 字元常值沒有收尾，或超過 U+FFFF。 |
| TY-SYN-0009 | `malformed integer literal`／`malformed floating-point literal` | 數字格式錯誤（例如 `0x` 後面沒有數字、`1e` 沒有指數）。 |
| TY-SYN-0010 | `integer literal out of range` | 整數字面值超出 `int`／`long` 範圍，請加 `L` 後綴。 |
| TY-SYN-0100 | `expected '%s', found %s` | 少了預期的 token（`)`、`]`、`{`、`}`、`:` 等）。 |
| TY-SYN-0101 | `expected identifier, found %s` | 需要識別字的位置放了別的東西；常見於把關鍵字當名稱使用。 |
| TY-SYN-0102 | `unexpected %s at top level` | 檔案最上層只允許 package／import／型別宣告，或直接寫成員（隱式類別形式）。 |
| TY-SYN-0103 | `unexpected %s in class body` | 類別成員宣告不完整。 |
| TY-SYN-0104 | `expected ',' ':' or '}' after enum constants` | enum 常數區與成員區之間要用一個 `:` 分隔。 |
| TY-SYN-0105 | `expected 'get' or 'set' accessor, found %s` | accessor 區塊裡只能有 `get` 與 `set`。 |
| TY-SYN-0106 | `unexpected %s` | 區塊內出現無法剖析的敘述。 |
| TY-SYN-0107 | `try resources must be separated by line breaks` | try-with-resources 的每個資源用換行分隔，不能用分號。 |
| TY-SYN-0108 | `try requires catch or finally` | `try` 至少要有一個 `catch` 或 `finally`。 |
| TY-SYN-0109 | `expected 'case' or 'default', found %s` | switch 區塊裡只能有 `case`／`default`。 |
| TY-SYN-0110 | `cannot mix '->' and ':' case labels` | 同一個 switch 只能用一種標籤形式。 |
| TY-SYN-0111 | `expected expression, found %s` | 需要運算式的位置放了別的 token。 |
| TY-SYN-0112 | `array dimension expression after empty dimension` | `new int[3][]` 之後不能再寫 `[5]`。 |
| TY-SYN-0113 | `array creation with both dimensions and initializer` | `new int[3]{1,2,3}` 不合法；要嘛給大小，要嘛給初始值。 |

## TY-TYP：型別與符號

### 宣告與成員（0001–0025）

| 代碼 | 訊息 | 說明與修法 |
|---|---|---|
| TY-TYP-0001 | `duplicate type %s`／`duplicate nested type %s` | 同名型別重複宣告。 |
| TY-TYP-0002 | `type variable %s cannot have type arguments` | 型別變數不能再帶型別引數。 |
| TY-TYP-0003 | `cannot find type %s` | 型別名稱找不到；檢查拼字、import 或前導程式庫。 |
| TY-TYP-0004 | `type %s expects %d type arguments, found %d` | 泛型引數數量不符。 |
| TY-TYP-0005 | `primitive type %s cannot be a type argument` | 泛型不能用原生型別，請用包裝類別。 |
| TY-TYP-0006 | `class cannot extend interface %s` | 類別要用 `implements` 介面。 |
| TY-TYP-0007 | `cannot extend final class %s` | 被 `final` 的類別不能被繼承。 |
| TY-TYP-0008 | `cyclic inheritance involving %s` | 繼承關係成環。 |
| TY-TYP-0009 | `%s is not an interface` | `implements` 後面只能是介面。 |
| TY-TYP-0010 | `duplicate field %s in %s` | 同一個類別重複宣告欄位。 |
| TY-TYP-0011 | `duplicate method %s in %s`／`duplicate constructor %s` | 參數抹除後簽章相同的方法或建構子重複。 |
| TY-TYP-0012 | `varargs parameter must be last` | `...` 只能放在最後一個參數。 |
| TY-TYP-0013 | `interface method with a body must be default, static or private` | 介面方法有 body 時要標 `default`／`static`／`private`。 |
| TY-TYP-0014 | `method %s needs a body` | 非抽象方法要有 body。 |
| TY-TYP-0015 | `abstract or native method %s cannot have a body` | `abstract`／`native` 方法不能有 body。 |
| TY-TYP-0016 | `abstract method %s in non-abstract class %s` | 有抽象方法的類別必須標 `abstract`。 |
| TY-TYP-0017 | （已移除） | `native` 方法現在可以宣告在任一類別，並以 `--native` 提供的 C 實作。 |
| TY-TYP-0018 | `'%s' is only allowed for local variables` | `var`／`val` 不能用在欄位、參數或回傳型別。 |
| TY-TYP-0019 | `%s must implement %s from %s` | 具體類別沒有實作介面或父類別的抽象方法。 |
| TY-TYP-0020 | `missing return statement` | 有回傳值的方法在某些路徑沒有 `return`。 |
| TY-TYP-0021 | `duplicate local variable %s` | 同一個作用域重複宣告區域變數。 |
| TY-TYP-0022 | `break outside of loop or switch` | `break` 只能出現在迴圈或 switch 內（有標籤者除外）。 |
| TY-TYP-0023 | `continue outside of loop` | `continue` 只能出現在迴圈內。 |
| TY-TYP-0024 | `thrown value must be a Throwable` | `throw` 的物件必須繼承 `Throwable`。 |
| TY-TYP-0025 | `cannot synchronize on void` | `synchronized` 的鎖不能是 void 運算式。 |

### 陳述式（0026–0044）

| 代碼 | 訊息 | 說明與修法 |
|---|---|---|
| TY-TYP-0026 | `local variables cannot be declared final; use 'val'` | 區域變數的不可重綁請用 `val`。 |
| TY-TYP-0027 | `'%s' requires an initializer` | `var`／`val` 一定要有初始值。 |
| TY-TYP-0028 | `'%s' cannot infer a type from null` | `null` 無法推斷型別，請寫出明確型別。 |
| TY-TYP-0029 | `'%s' cannot infer a functional interface type` | lambda 需要目標型別，請明確宣告介面型別。 |
| TY-TYP-0030 | `for-each requires an array or Iterable` | 增強 `for` 只能用在陣列或 `Iterable`。 |
| TY-TYP-0031 | `incompatible types: %s is not assignable to %s` | 迴圈變數型別與元素型別不符。 |
| TY-TYP-0032 | `return value required for %s` | 有回傳值的方法不能空手 `return`。 |
| TY-TYP-0033 | `cannot return a value from a void method` | void 方法不能回傳值。 |
| TY-TYP-0034 | `catch type must be a Throwable` | `catch` 的型別必須是 `Throwable` 家族。 |
| TY-TYP-0035 | `switch selector must be an integral, String or enum type` | switch 的選擇子型別不合法。 |
| TY-TYP-0036 | `duplicate default label` | 同一個 switch 只能有一個 `default`。 |
| TY-TYP-0037 | `incompatible pattern type %s for switch on %s` | `case 型別 名` 與選擇子型別無關。 |
| TY-TYP-0038 | `case label must be a constant expression` | case 標籤必須是編譯期常數。 |
| TY-TYP-0039 | `duplicate case label` | 同一個 switch 內標籤重複。 |
| TY-TYP-0040 | `array required, found %s` | 對非陣列使用 `[]`。 |
| TY-TYP-0041 | `inconvertible types: %s cannot be cast to %s` | 這個 cast 永遠不可能成立。 |
| TY-TYP-0042 | `incompatible pattern type %s for %s` | `instanceof` pattern 的型別與左邊無關。 |
| TY-TYP-0043 | `not an enclosing class: %s` | `Outer.this` 的外層類別不存在。 |
| TY-TYP-0044 | `no superclass` | 沒有父類別卻使用 `super`。 |

### 名稱與存取（0045–0048）

| 代碼 | 訊息 | 說明與修法 |
|---|---|---|
| TY-TYP-0045 | `cannot access instance field %s from a static context` | 靜態方法內不能直接讀實例欄位。 |
| TY-TYP-0046 | `%s has private access in %s` | 私有成員只能在自己的類別內存取。 |
| TY-TYP-0048 | `cannot find symbol %s` | 名稱找不到：檢查拼字、作用域、import，或是否忘了宣告。 |

### 型別轉換與運算子（0049–0067）

| 代碼 | 訊息 | 說明與修法 |
|---|---|---|
| TY-TYP-0049 | `null is not assignable to %s` | `null` 不能給原生型別。 |
| TY-TYP-0050 | `possible lossy conversion from %s to %s` | 需要窄化轉換，請加 cast。 |
| TY-TYP-0051 | `incompatible types: %s cannot be converted to %s` | 最常見的型別錯誤：指派、傳參、回傳的型別不相容。 |
| TY-TYP-0052 | `operator '!' cannot be applied to %s` | `!` 只能用在 `boolean`。 |
| TY-TYP-0053 | `operator '~' requires an integral operand` | `~` 只能用在整數。 |
| TY-TYP-0054 | `operator '%s' requires a numeric operand` | 一元 `+`／`-` 只能用在數值。 |
| TY-TYP-0055 | `operator '%s' requires a numeric operand` | `++`／`--` 只能用在數值。 |
| TY-TYP-0056 | `cannot apply '%s' to a non-assignable expression` | `++`／`--` 的目標必須可以被指派。 |
| TY-TYP-0057 | `cannot assign a value to final variable %s` | `val` 或 `final` 變數不能再次指派。 |
| TY-TYP-0058 | `cannot assign a value to final field %s` | 不能指派其他類別的 final 欄位。 |
| TY-TYP-0059 | `incompatible operand types %s and %s` | `==`／`!=` 兩邊型別無法比較。 |
| TY-TYP-0060 | `incomparable types: %s and %s` | 兩個參考型別之間不可能相等。 |
| TY-TYP-0061 | `operator '%s' cannot be applied to %s and %s` | 大小比較只能用在數值。 |
| TY-TYP-0062 | `operator '%s' requires integral or boolean operands` | `&`／`\|`／`^` 的運算元型別不合法。 |
| TY-TYP-0063 | `operator '%s' requires integral operands` | 位移運算子只能用在整數。 |
| TY-TYP-0064 | `operator '%s' cannot be applied to %s and %s` | 算術運算子的運算元不是數值（常見：物件忘了 unbox）。 |
| TY-TYP-0065 | `left-hand side of an assignment must be a variable` | 指派的左邊不能是任意運算式。 |
| TY-TYP-0066 | `operator '%s' cannot be applied to boolean` | `boolean` 不能做加減乘除。 |
| TY-TYP-0067 | `array dimension must be non-negative` | 陣列大小是負的常數。 |

### 建構與實例化（0068–0075）

| 代碼 | 訊息 | 說明與修法 |
|---|---|---|
| TY-TYP-0068 | `cannot instantiate %s` | 這個型別不能用 `new`。 |
| TY-TYP-0069 | `%s is abstract; cannot be instantiated` | 抽象類別不能直接 `new`。 |
| TY-TYP-0070 | `cannot extend final class %s`／`cannot subclass enum %s` | 匿名類別不能繼承 final 類別或 enum。 |
| TY-TYP-0071 | `an enclosing instance of %s is required` | 內部類別需要在有外層實例的地方建立。 |
| TY-TYP-0072 | `no suitable constructor found for %s(%s)` | 沒有相符的建構子；檢查參數數量與型別。 |
| TY-TYP-0073 | `array clone takes no arguments` | `clone()` 不接受參數。 |
| TY-TYP-0074 | `constructor call must be the first statement of a constructor` | `this(...)`／`super(...)` 必須是第一句。 |
| TY-TYP-0075 | `recursive constructor invocation` | 建構子遞迴呼叫自己。 |

### 方法解析、lambda 與 pattern（0076–0094）

| 代碼 | 訊息 | 說明與修法 |
|---|---|---|
| TY-TYP-0076 | `cannot find method %s(%s)`／`cannot find method %s for this functional interface` | 找不到方法：檢查名稱、參數型別、可見性，或受體型別。 |
| TY-TYP-0077 | `non-static method %s cannot be referenced from a type name` | 用類別名稱只能呼叫靜態方法。 |
| TY-TYP-0078 | `cannot invoke %s on %s` | 對這個型別呼叫方法不合法。 |
| TY-TYP-0079 | `%s has %s access in %s` | 方法可見性不足。 |
| TY-TYP-0080 | `cannot find symbol %s in %s`／`on array` | 成員不存在於該型別。 |
| TY-TYP-0081 | `cannot infer the functional interface for this lambda` | lambda／方法參照沒有目標型別，請明確指定。 |
| TY-TYP-0082 | `lambda target type must be a functional interface` | 目標型別不是介面。 |
| TY-TYP-0083 | `%s is not a functional interface` | 介面有多個抽象方法，不能當 lambda 目標。 |
| TY-TYP-0084 | `lambda has %d parameters but %s requires %d` | lambda 參數數量不符。 |
| TY-TYP-0085 | `cannot construct %s` | 建構子參照的目標不能建構。 |
| TY-TYP-0086 | `cannot resolve static import %s` | 靜態 import 找不到對應成員。 |
| TY-TYP-0087 | `record pattern requires a record type, found %s` | 解構 pattern 的左邊不是 record 型別（`case Point(int x, int y)` 的 `Point` 必須是 record）。 |
| TY-TYP-0088 | `record pattern for %s needs %d components, found %d` | 解構的綁定數量與 record 成員數不符；巢狀解構也要逐一對上。 |
| TY-TYP-0089 | `'case null' requires a reference selector` | `case null` 只能用在參考型別的 switch 選擇子上，原生型別請改用 `default`。 |
| TY-TYP-0090 | `%s does not name a super interface` | `Interface.super.method()` 的 `Interface` 不存在或不是介面。 |
| TY-TYP-0091 | `%s is not a super interface of %s` | 限定的 `super` 只能指向自己（直接或間接）實作的介面。 |
| TY-TYP-0092 | `a primitive pattern needs a name to bind the value to` | 原生型別 pattern 一定要綁定變數：`o instanceof int i`，不能只寫 `o instanceof int`。 |
| TY-TYP-0093 | `boolean cannot be converted to %s` | `boolean` 只能和 `boolean` pattern 配對。 |
| TY-TYP-0094 | `primitive pattern %s needs a boxed value, found %s` | 選擇子既不是參考型別也不是原生數值。 |

## TY-PROP：原生 property

| 代碼 | 訊息 | 說明與修法 |
|---|---|---|
| TY-PROP-0001 | `a property declaration must declare exactly one name` | 一個 property 只能宣告一個名稱。 |
| TY-PROP-0002 | `duplicate get accessor`／`duplicate set accessor` | `get`／`set` 各只能出現一次。 |
| TY-PROP-0003 | `computed property %s cannot use storage modifiers` | 沒有儲存的 property 不能用 `final`／`volatile`／`transient`。 |
| TY-PROP-0004 | `final property %s cannot declare a setter` | `final` property 不能有 setter。 |
| TY-PROP-0005 | `property %s has no getter` | 只有 setter 的 property 不能被讀取；在 accessor 內請用 `field`。 |
| TY-PROP-0007 | `property %s has no setter` | 只有 getter 的 property 不能被指派。 |
| TY-PROP-0008 | `property %s needs a getter for compound assignment` | `p.x += 1` 需要 getter 與 setter。 |

## TY-INT 與 TY-IO

| 代碼 | 訊息 | 說明 |
|---|---|---|
| TY-INT-0001 | `prelude is missing class %s` | 前導程式庫損壞或類別被覆蓋，屬於編譯器內部錯誤。 |
| TY-INT-0002 | `unsupported expression %T` | 語意分析遇到未處理的節點，屬於編譯器內部錯誤（請回報）。 |
| TY-INT-0004 | `@Singular goes on a builder field, not on the class` | 寫在類別上沒有意義。 |
| TY-INT-0005 | `@Singular needs a List or Map field, found %s` | `@Singular` 只能用在集合欄位。 |
| TY-INT-0006 | `@CustomLog needs lombok.config…` | Teyru 不讀設定檔，請改用 `@Log` 或自己宣告欄位。 |
| TY-IO-0001 | `cannot read %s: %v` | 來源檔讀不到，檢查路徑與權限。 |

## 執行期錯誤

執行期的失敗不是診斷碼，而是 `Throwable` 家族：

| 例外 | 觸發時機 |
|---|---|
| `NullPointerException` | 對 `null` 取值、呼叫方法、取陣列長度 |
| `ArrayIndexOutOfBoundsException` | 索引超出 `[0, length)` |
| `IndexOutOfBoundsException` | `ArrayList.get`／`set`／`removeAt` 的索引超出 `[0, size)` |
| `NoSuchElementException` | 已經沒有元素卻再呼叫 `Iterator.next()` |
| `ArithmeticException` | 整數除以零或取餘數為零 |
| `ClassCastException` | `cast` 或 `instanceof` 失敗的強制轉型 |
| `NegativeArraySizeException` | 陣列長度為負 |
| `AssertionError` | `assert` 失敗 |
| `IllegalArgumentException` | `enum.valueOf` 找不到常數等 |

沒有被 catch 的例外會印出 `Exception in thread "main" …` 並以狀態 1 結束。
若執行到沒有實作的抽象方法，會印出 `teyru: no implementation for …` 並以狀態 70 結束。
