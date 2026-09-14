# AGENTS.md — Teyru 專案工作規範

> 適用對象：本儲存庫內所有人類貢獻者與 Coding Agents。
> 版本 3.0（2026-09-13）。v1.0 是 Java/JVM 時期的規範，已於 v2.0 作廢；本版補齊
> util 分離、禁止佔位、文件與測試的完整規範。

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
   - 並在 `docs/language.md` 的「尚未實作」列出。
2. 禁止「編譯器接受了但執行期默默回傳垃圾」的組合。寧可拒絕編譯，也不要產生
   行為未定義的程式。
3. 禁止用註解掉的程式碼當作待辦事項；要嘛刪掉，要嘛在 `docs/` 開一節說明。
4. 文件與程式碼必須一致：改了行為就同步改 `README*`、`docs/language.md`、
   `docs/diagnostics.md`。

---

## 6. 測試規範

- **端到端**：在 `tests/programs/` 放 `xxx.teyru` 與 `xxx.expected`。
  需要命令列參數時另外放 `xxx.args`（每行一個）。`go test` 會自動編譯並比對輸出。
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
- 每個代碼都要在 `docs/diagnostics.md` 有一列說明（訊息、原因、修法）。
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
| `README.md` | 繁中主文件（含語言切換列） | 使用者可見行為改變時 |
| `README.zh-CN.md` / `README.en.md` / `README.ja.md` | 對應語言版本 | 與 `README.md` 同步 |
| `docs/language.md` | 完整語言參考 | 語法或語意改變時 |
| `docs/diagnostics.md` | 每個診斷碼的說明 | 新增／修改診斷碼時 |
| `docs/lombok.md` | Lombok 相容層：支援狀態、產生的成員、與 Lombok 的差異 | 新增或調整標註支援時 |
| `docs/native.md` | 原生互通：以 C 實作 native 方法、符號命名與型別對應 | native 介面或 CLI 旗標改變時 |
| `docs/architecture.md` | 編譯流程與執行期模型 | 架構改變時 |
| `AGENTS.md` | 本檔 | 流程改變時 |

翻譯版本必須與 `README.md` 結構一致（章節、表格、範例），不能只寫摘要。

---

## 10. 已知限制（不要當成已完成）

- checked exception 沒有編譯期檢查。
- `sealed` 的 `permits` 子句沒有被驗證：沒有 `permits` 的 sealed 型別在 switch
  窮盡性上被視為不可判定而要求 `default`。
- 沒有反射、沒有執行緒（`java.util` 集合、`java.io`、`java.net` 都有）。
- 與 Java 生態不相容（沒有 JAR、沒有 JDK 類別庫、沒有 JNI）。
- GC 為保守式標記清除，非分代；大量短命物件的情境仍落後 HotSpot 的逃逸分析。
- 型別推論比 javac 弱一層，界線見 `docs/language.md` §12 第 11 條。

---

## 11. 送出前檢查清單

- [ ] `go build ./...`、`go vet ./...`、`go test ./... -count=1` 全綠
- [ ] 新功能有端到端測試；新診斷碼有拒絕測試
- [ ] 沒有多行 TODO 或空殼實作；未實作路徑會明確失敗
- [ ] 共用邏輯在 `internal/util`，沒有兩份實作
- [ ] 執行期 C 在 `-Wall -Wextra` 下無警告
- [ ] `README*` 與 `docs/` 已同步
- [ ] commit 訊息符合 §8，且沒有 AI 署名
