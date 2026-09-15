# AGENTS.md — Teyru 專案工作規範

> 適用對象：本儲存庫內所有人類貢獻者與 Coding Agents。
> 版本 3.1（2026-09-14）。v1.0 是 Java/JVM 時期的規範，已於 v2.0 作廢；本版補齊
> util 分離、禁止佔位、文件與測試的完整規範，並記下拆分之後的倉庫佈局。

---

## 倉庫佈局

| 倉庫 | 內容 |
|---|---|
| `teyru-lang/Teyru`（本倉庫） | 編譯器（Go）、執行期（C）、標準程式庫（`lib/*.teyru`）、範例（`examples/`） |
| `teyru-lang/tests` | 端到端測試資料，以 **submodule** 掛在 `tests/`：第一次 clone 要 `git submodule update --init` |
| `teyru-lang/docs` | 使用者文件（fumadocs），發佈在 <https://docs.teyru.dev> |
| `teyru-lang/editors` | VS Code／Zed／JetBrains 擴充、tree-sitter 文法 |
| `teyru-lang/website` | 官網原始碼，發佈在 <https://teyru.dev> |

**產品的文件只有一份**，在 `teyru-lang/docs`：改了行為就改那裡，不要在程式庫裡再放
一份。本倉庫只留 `README.md`（倉庫門面與安裝方式）、`AGENTS.md`（本檔）與範例自己的
說明。

---

## 0. 產品契約（不可協商）

1. **語言名稱固定 Teyru**：副檔名 `.teyru`、語言 ID `teyru`、CLI `teyru`。
   不要重新命名的是這些；程式庫的模組路徑跟著倉庫走，目前是
   `github.com/teyru-lang/Teyru`（語言的家在 `teyru-lang` 組織）。
2. **編譯器用 Go 撰寫，只用標準函式庫。** `go.mod` 目前沒有任何 `require`，
   必須維持。要引入第三方模組前，先證明標準函式庫做不到。
3. **不依賴 JVM、javac、bytecode。** 產物是原生執行檔。任何「先產生 Java 再交給
   javac」、「產生 bytecode」、或執行期需要 JVM 的設計，都違反產品契約。
4. **編譯路徑固定**：`.teyru → Go 前後端 → C → clang/LLVM（或 gcc）→ 原生執行檔`。
   執行期在 `internal/runtime/src`，以 C 撰寫。
5. **標準程式庫以 Teyru 本身撰寫**（`lib/` 下的 `*.teyru`）。只有在不能用
   Teyru 表達的原生操作才能標記 `native`，並在執行期提供實作。
6. **效能是產品目標，但宣稱必須可重現。** 任何「比 X 快」的說法都要附上
   `examples/bench_*.teyru` 與 `scripts/bench.sh` 可重現的量測環境，並誠實標出劣勢情境。

---

## 1. 事實與證據

1. 不得偽造測試、效能、部署或相容性結果。跑不動就說跑不動。
2. 文件中的數字必須來自實際執行，並註明量測方式與環境。
3. 「支援某語言特性」的定義是：`tests/programs/` 有對應程式，且
   `go test ./...` 通過。只會剖析不算支援。
4. 不確定的事情寫「未驗證」，不要寫成已完成。

---

## 2. 套件邊界與 util 分離

依賴方向固定，**不得反向**：

```
source → lexer → parser → ast → sema → codegen
             util ←───────────┴────────┘
       runtime（C）與 prelude（Teyru）獨立於 Go 端
```

- `internal/util` 放**前後端共用**的工具，目前有：
  - `name.go`：`Mangle`（C 識別字修飾）、`Capitalize`（JavaBeans 命名）、
    `Descriptor`（型別單字元描述）、`Signature`（參數列表查詢鍵）、
    `FloatLiteral`（Java 的浮點字面值印刷）。
  - `layout.go`：`SizeOf`、`AlignOf`、`Align`、`FieldLayout`、`IsRef`、`IsPrim`。
- **禁止在兩個套件各寫一份相同的工具。** 只要一段邏輯同時被 `sema` 與 `codegen`
  需要（型別描述、名稱修飾、C 版面配置…），就必須放進 `internal/util`，
  由兩邊呼叫。`codegen` 只保留極薄的轉呼（例如 `func mangle(s string) string`）。
- 只在單一套件內使用的輔助函式留在原套件，不要為了「集中」而搬到 util。
- 新增 util 函式時要附單元測試（`internal/util/*_test.go`）。

---

## 3. Go 程式碼風格

- 一律 `gofmt`；提交前 `go vet ./...` 必須乾淨。
- 匯入分三組（標準函式庫 → 第三方 → 本專案），不使用全限定名稱、不使用
  wildcard import、不使用未使用的匯入。
- 錯誤用診斷回報（`diags.Errorf` / `ctx.errf`），**不要在編譯器路徑 panic**。
  panic 只允許出現在「不可能發生」的內部不變式，且必須附註解說明為何不可能。
- 註解解釋**為什麼**，不要逐行翻譯程式碼。匯出符號要有 doc comment。
- 不用魔法數字：抽成具名常數（例如 `util.SizeRef`、`TY_CHUNK`）。
- 偏好既有模式：新功能先找同類節點怎麼做，再照著做。

---

## 4. C 程式碼（執行期與產生的程式碼）

- 產生的 C 以 `-std=gnu11` 編譯，允許 GNU 敘述運算式（statement expression）；
  這是刻意的選擇，讓運算式型別轉換與臨時變數保持單一運算式。
- 執行期必須在 `gcc -c -O2 -Wall -Wextra` 下沒有警告。
- 需要被 GC 追蹤的物件一律走 `ty_alloc`／`ty_alloc_arr`，**不得直接 `malloc`**。
- 新增或調整類別欄位時，`tyclass.refoffs` 必須同步更新。產生器會依 C 的對齊規則
  用 `util.FieldLayout` 計算；**修改欄位配置一定要重跑測試**，
  漏掉位移會讓 GC 回收存活物件，多一個位移會讓它解引用垃圾。
- 執行期函式只要參數或回傳是物件，就用 `void*`；呼叫端負責轉型。

---

## 5. 禁止佔位與多行未實作

1. **禁止留下多行的 TODO、`/* unimplemented */`、或回傳未定義值的空殼。**
   若某條路徑真的無法實作，必須：
   - 讓它呼叫明確的失敗路徑（例如 `ty_unimplemented("Class.method")`，以狀態 70 結束），
   - 並在文件站（`teyru-lang/docs`）的「尚未實作」列出。
2. 禁止「編譯器接受了但執行期默默回傳垃圾」的組合。寧可拒絕編譯，也不要產生
   行為未定義的程式。
3. 禁止用註解掉的程式碼當作待辦事項；要嘛刪掉，要嘛在文件站開一節說明。
4. 文件與程式碼必須一致：改了行為就同步改 `teyru-lang/docs` 的對應頁面
   （語言參考、診斷碼、該主題那一頁），並在 §9 的表格裡找到它。

---

## 6. 測試規範

- **端到端**：在 `tests/programs/` 放 `xxx.teyru` 與 `xxx.expected`。
  需要命令列參數時另外放 `xxx.args`（每行一個）。`go test` 會自動編譯並比對輸出。
  `tests/` 是 `teyru-lang/tests` 的 submodule：改測試要在**那個**倉庫提交，這裡只會
  動到 gitlink。submodule 沒 checkout 時 `make test` 與 `go test` 都會直接說清楚，
  不會安靜地零測試通過。
- **診斷**：在 `driver_test.go` 的 `TestDiagnostics` 加入「應該被拒絕」的案例與期望
  錯誤碼。
- **單元**：`internal/util` 等純函式要有 table-driven 測試。
- 修 bug 的順序固定：先寫一個會失敗的測試 → 修到通過 → 再提交。
- 提交前至少跑：

  ```sh
  go build ./... && go vet ./... && go test ./... -count=1
  ```

- 效能相關的改動要附上 `sh scripts/bench.sh` 的前後數字。

---

## 7. 診斷碼規範

- 格式 `TY-<階段>-<四位數字>`，階段為 `SYN`（詞法與語法）、`TYP`（語意）、
  `PROP`（property）、`INT`（編譯器內部）、`IO`（檔案）。
- 代碼一旦發布就不再改變意義；新增要往後編號，不要重複使用已刪除的號碼。
- 每個代碼都要在文件站的〈診斷碼〉那一頁有一列說明（訊息、原因、修法）。
- 訊息格式：小寫開頭、不超過一行、用 `%s` 帶入符號名稱，不要有大寫縮寫。

---

## 8. 提交與 PR 規範

- 訊息用範圍前綴：`feat(compiler):`、`fix(runtime):`、`feat(codegen):`、`test:`、
  `docs:`、`chore:`。
- 一個提交做一件事；不要把改名、重構與新功能混在一起。
- PR 描述要寫：動機、做法、如何驗證、已知限制。

---

## 9. 文件規範

| 檔案 | 內容 | 什麼時候要改 |
|---|---|---|
| `README.md`（本倉庫） | 倉庫門面：這是什麼、怎麼安裝、其他倉庫在哪 | 專案定位或安裝方式改變時 |
| `examples/*/README.md`（本倉庫） | 範例自己的說明 | 範例改變時 |
| `AGENTS.md`（本倉庫） | 本檔 | 流程改變時 |
| `teyru-lang/docs` 的〈語言參考〉 | 完整語言參考 | 語法或語意改變時 |
| `teyru-lang/docs` 的〈診斷碼〉 | 每個診斷碼的說明 | 新增／修改診斷碼時 |
| `teyru-lang/docs` 的〈Lombok〉 | 支援狀態、產生的成員、與 Lombok 的差異 | 新增或調整標註支援時 |
| `teyru-lang/docs` 的〈原生互通〉 | 以 C 實作 native 方法、符號命名與型別對應 | native 介面或 CLI 旗標改變時 |
| `teyru-lang/docs` 的〈架構〉 | 編譯流程與執行期模型 | 架構改變時 |
| `teyru-lang/docs` 的〈JSON〉／〈Web 框架〉／〈模組〉 | 各主題的完整說明 | 該主題行為改變時 |
| `teyru-lang/tests` | 端到端測試資料 | 新增或修改測試時 |
| `teyru-lang/editors` | 編輯器擴充與文法 | 語法或關鍵字改變時 |

文件站在 `teyru-lang/docs`，`README` 的英／日／簡中版本也在那裡（對應語言的頁面），
四種語言的內容要一致：改了其中一份就要改其餘的結構。

---

## 10. 已知限制（不要當成已完成）

- checked exception 沒有編譯期檢查。
- `sealed` 的 `permits` 子句沒有被驗證：沒有 `permits` 的 sealed 型別在 switch
  窮盡性上被視為不可判定而要求 `default`。
- 反射在 `lib/26_reflect.teyru`：`Class`、`Field`、`Method`、`Constructor`、
  `Modifier`、`Array` 與 `java.lang.reflect` 的六個例外，讀的是編譯器為每個類別
  產生的靜態表（欄位、方法、修飾子、列舉常數），所以查一次資料是走一次陣列，
  執行期不建表。與 Java 的差異（都已實測，不是未驗證）：
  - 類別名是 Teyru 的：`String.class.getName()` 是 `teyru.String`，
    `Class.forName` 兩種寫法都收（`java.lang.String` 會找到同一類別）。
  - 註解反射有，但元素是**按名字讀**：`Class`／`Field`／`Method`／`Constructor`
    上的 `getAnnotations()`、`getAnnotation(Class)`、`isAnnotationPresent(Class)` 是
    Java 的，`Annotation` 則沒有「每個註解型別一個實作類別」——所以要
    `ann.stringValue("value")`／`intValue`／`booleanValue`／`doubleValue`／
    `classValue`／`enumValue`，而不是 Java 的 `ann.value()`。沒寫的元素讀得到介面
    宣告的預設值（parser 保留 `default`）；`@Retention` 收得下但沒有作用；陣列型別
    的元素值不帶（讀它會說不支援）。
  - 所有陣列共用一個類別，因此沒有 `getComponentType`、沒有每個元素型別的陣列
    類別，`forName("[I")` 也沒有東西可回答。
  - 沒有泛型型別參數的反射（`getGenericType` 等不存在）。
  - 原生型別的取值器只收完全相符的裝箱型別，Java 的拓寬（例如對 `byte` 欄位
    呼叫 `getInt`）在這裡是 `IllegalArgumentException`。
  - 存取控制不檢查：私有成員可以直接讀寫。final 一律攔，`setAccessible(true)`
    之後可以寫 instance final，但 static final 一律拒絕（javac 也是這樣：JDK 不再
    讓它通過，即使 setAccessible 過）。
  - 內部類別與區域類別不能被反射建構（沒有外圍實例可用）。
  - 成員表（欄位、方法與 invoker）只在使用者程式真的可能用到反射時才寫進執行檔
    （`emit.go` 的 `reflectUsed`）：它們是唯一會指名別的類別的中繼資料，擺在檔案
    層級會把整個標準程式庫釘進每一支程式（實測 hello world 從 445.9 KB 漲到
    2.9 MB）。用到反射的程式仍要付出整份約 3 MB，這是這個設計尚未解決的成本。
  - 啟動時的尖峰記憶體（hello world）從 2.2 MB 變成 4.1 MB：加入反射翻譯單元之後，
    LTO 不再丟掉那塊 8 MB 的 `ty_roots` 保留區與相關符號。差異已量到，根因未定——
    同一支程式額外帶一塊 8 MB 的未觸碰 `.bss` 時 RSS 只多 64 kB，所以不是那塊保留區
    本身；把 `forName` 的表拿掉也只降回約 4.1 MB。
- JSON 綁定（`lib/27_json_binding.teyru`）讀的是類別本身：基本型別、字串、`char`、
  列舉、`Object`、巢狀類別、record，以及**容器**（`List`／`Set`／`Map`／`Deque` 等
  介面與其實作、陣列）都已往返。尚未完成的是：容器裡的**物件元素**（元素型別由
  抹除後的宣告型別推導，遇到物件時目前是明確拒絕而不是產生壞資料）、
  `GsonBuilder` 的其餘旋鈕（`serializeNulls`／`setPrettyPrinting`／
  `setLenient`／`setFieldNamingPolicy`／`excludeFields*`）、`@Expose`／
  `@Since`／`@Until` 的過濾、`JsonSerializer`／`JsonDeserializer` 轉接器，以及
  Gson 的串流 `JsonReader`／`JsonWriter`。
- 執行緒只有一部分：`Thread`（`Runnable`、`start`／`join`／`sleep`／`yield`／
  `currentThread`／`getId`／`getName`／`isAlive`）、真的 `synchronized`（含
  `synchronized` 方法修飾子，方法會持有監視器整段）與 `Object.wait`／`notify`／
  `notifyAll`。執行期在 `internal/runtime/src/tyrt_thread.c`，端到端測試是
  `tests/programs/t159_threads.teyru`。**沒有的**：`interrupt`、daemon、優先權、
  `ThreadGroup`、`ThreadLocal`、堆疊大小與逾時的 `join(long)`，以及未處理例外的
  handler（執行期印出 Java 預設處理常式那一行，然後結束那個執行緒、行程繼續）。
  GC 是**合作式**停止世界：安全點在每個迴圈回邊（產生器會放）、配置慢路徑、等
  heap 鎖，以及每個會阻塞的呼叫。因此一個既不迴圈、不配置也不阻塞的執行緒
  （例如卡在原生 `read()` 裡）會讓收集等它，直到它回來；這是已知限制，不是未
  驗證的不確定性。每個執行緒有自己的配置區（slab），單執行緒程式的配置速度不變。
- **收集器的掃描範圍檢查（#57 期間暴露，已修）**：`trace_object` 會把「沒有參照欄位
  的類別」的類別描述子當成候選標記，而 `valid_obj` 原本只用一個 16 位元組對齊測試就打
  發掉它。#57 讓執行期自己的靜態資料位移改變，描述子剛好落在 16 位元組邊界上，於是同
  一個值落進 slab 搜尋，而 slab 快取永遠不會命中 `.data` 位址：每次標記 489 ×
  1,048,627 = 5.129 億次探測。修法是先用 heap 的 slab 範圍篩掉候選（範圍外的字組不屬
  於任何 slab，逐一探測本來也會回 0，所以只會少做事、不會改答案）。同一個 400 萬物件
  的程式：兩次收集由 282.0 + 281.3 ms 變成 37.3 + 37.4 ms，而 #57 之前的基準是
  38.1 + 36.5 ms。單執行緒配置快速路徑不受影響（2000 萬次 `ty_alloc(16)` 11.5 ns）；
  `bench_loop` 仍比 #57 之前慢約 10%，那是迴圈回邊安全點的代價。

---

## 11. 送出前檢查清單

- [ ] `go build ./...`、`go vet ./...`、`go test ./... -count=1` 全綠（`tests/` submodule 已 checkout）
- [ ] 新功能有端到端測試；新診斷碼有拒絕測試
- [ ] 沒有多行 TODO 或空殼實作；未實作路徑會明確失敗
- [ ] 共用邏輯在 `internal/util`，沒有兩份實作
- [ ] 執行期 C 在 `-Wall -Wextra` 下無警告
- [ ] 行為改變時，`teyru-lang/docs` 的對應頁面已同步（文件只有那一份）
- [ ] commit 訊息符合 §8，且沒有 AI 署名
