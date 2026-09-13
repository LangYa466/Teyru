# Teyru 編譯器架構

## 流程

```
來源檔 (UTF-8)
   │  internal/source      — 檔案、行號對應、診斷容器
   ▼
Token 串
   │  internal/lexer       — 關鍵字、運算子、字面值、文字區塊
   │                        換行不是 token，只在 token 上標記 NL 旗標
   ▼
AST
   │  internal/parser      — 遞迴下降；游標 + 前瞻 + 選擇點回退
   ▼
已檢查的程式模型
   │  internal/sema        — 符號表、型別、泛型抹除、多載、佈局
   ▼
C 原始碼
   │  internal/codegen    — 類別→struct、vtable／itable、GC 根資訊
   ▼
原生執行檔
      clang/LLVM 或 gcc + internal/runtime/src（GC、字串、陣列、例外）
```

## 換行當敘述終止符

Teyru 沒有分號。詞法分析器不產生 NEWLINE token，而是在每個 token 上記錄
「前面是否有換行」。剖析器用兩件事判斷敘述是否結束：

1. **目前剖析位置的換行是否顯著**（`nl` 堆疊；括號內、引數列表內不顯著）。
2. **前綴是否已完整**。例如 `return` 後面直接換行就是無值 return，
   但運算子、逗號、`.`、`::`、`->` 之後的換行不終止敘述，
   行首是 `.`／`::` 時也視為延續。

`internal/parser/parser.go` 的 `continues()` 是唯一的判斷點。

## 型別與符號

- `ast.Type` 有六種：原生、類別（含型別引數）、陣列、型別變數、萬用字元、null／error。
- 泛型在 `sema.erasure` 抹除；執行期只知道類別，不知道型別引數。
- 多載解析（`pickOverload`）：先算每個候選的轉換成本（完全相同 0、拓寬／上轉 1、
  boxing 2、unboxing 3、varargs 另計），取總成本最低者。
- 方法的 vtable 槽位在 `layout()` 決定：由父類複製，覆寫者沿用同一槽位；
  介面方法另有全域唯一的 selector（`Selector`），供 itable 使用。

## 產生 C 的關鍵對應

| Teyru | C |
|---|---|
| 類別 `Foo` | `struct C_Foo { tyobj obj; ... }`（欄位依 `InstFields` 平鋪，含繼承） |
| 實例方法 | `M_<class>_<name>_<idx>(C_Foo* this, ...)` |
| 虛擬呼叫 | `this->obj.cls->vtable[slot](...)` |
| 介面呼叫 | `ty_itab(obj, selector)(...)` |
| `new Foo(...)` | GNU 敘述運算式：配置 → 設 `cls` → 呼叫建構子 |
| 陣列 | `tyarr { tyobj; len; data; esize; refs }`，元素內嵌 |
| 字串常數 | 靜態 `tystr`（不經 GC） |
| `try`/`catch` | `tycatch` + `setjmp`/`longjmp` |
| property 讀寫 | 降階成 getter／setter 呼叫（`sema.Props` 記錄） |
| `for (a : b : c)` | `for (init; b; c)` |
| 記錄 `Point(int x,int y)` | struct + 建構子 + `x()`/`y()` + `toString`/`hashCode`/`equals` |
| enum 常數 | 靜態欄位，於 `<clinit>` 建立並填入 ordinal／name |

## 效能設計

產生的 C 由 clang/LLVM 以 `-O2` 加 LTO 編譯（`--no-lto` 可關閉；不支援 LTO 的
工具鏈會自動退回），跨函式 inline、常數傳播與迴圈向量化都由 LLVM 負責。在此之上，
編譯器與執行期刻意讓熱路徑保持單一指令層級：

| 機制 | 位置 | 說明 |
|---|---|---|
| 行內配置 | `tyrt.h` 的 `static inline ty_alloc` | 指標碰撞（bump pointer）路徑完全內聯，只有區塊用盡或超過 GC 門檻才呼叫 `ty_alloc_slow` |
| 行內邊界檢查 | `codegen.boundCheck` | 檢查以敘述運算式內聯在取用點，每個索引都產生一次比較（常數索引也一樣，`sema` 不先摺疊，化簡留給 LLVM） |
| 常數折疊 | `codegen.foldBinary`、`ident` | 字面值運算、`static final` 常數、字串相加在編譯期算完 |
| 死 chunk 回收 | `tyrt.c` 的 sweep | 一個 chunk 內若沒有任何存活物件就整塊 `free` 還給系統，之後的回收不再走它；仍在使用的 chunk 則每個區塊都要走過（計數存活一次、標記或釋放一次），所以單次回收的成本與保留的記憶體量成正比，而不是與存活量成正比 |
| 字串常數 | `codegen.strLit` | 字串字面值是靜態 `tystr`，不經配置、不進 GC |
| 類別初始化 | `codegen.clinitStmt` | 惰性初始化，但旗標由產生的程式碼自己測；繼承鏈上沒有靜態初始化區塊的類別完全不產生程式碼 |
| 逃逸分析 | `codegen.escape.go` | 不離開所在方法的物件放在 C 堆疊上，LLVM 得以提升欄位並刪除物件 |
| 原生互通 | `codegen.native.go` | `native` 方法的 C 符號與宣告由編譯器產生（`--native-header`） |

逃逸分析（`escape.go`）只提升同時滿足兩個條件的區域物件：宣告型別與 `new` 的類別
完全相同（`sameCreatedClass`），且該類別有編譯器配置好的 struct 與可直接呼叫的
建構子（`promotable`）。判定方式是走訪方法內的每一處使用（`escWalk`）：把物件當接收者
讀原生欄位、讀「不可能裝得下這個物件」的參考欄位、寫入物件自己的欄位（含
`c.next = c`）、`instanceof`、以及呼叫「接收者不會外流」的方法算安全。只要參考本身
被當成值用掉（當引數、`==`／`!=`、轉型、賦值給別的變數）、被存進別的物件或陣列、
被回傳／拋出／`yield`、被 lambda 或方法參考或匿名類別捕獲，或走到分析未涵蓋的節點，
就留在堆積。方法是否會讓接收者外流是對呼叫圖做的傳遞分析（`leaksThis`）：native
方法假設不會，沒有主體的方法假設會，分析中遇到環路也視為會。被提升的物件本身不在
chunk 裡（`valid_obj` 會拒絕它的位址），但它的參考欄位就在 C 堆疊上，保守的原生堆疊
掃描因此仍看得到它指向的堆積物件。

已知的效能邊界：逃逸分析只涵蓋留在方法內的物件；真正上堆積的物件仍走保守式
標記清除（無分代假設），在「物件長期存活、反覆回收」的負載上 HotSpot 仍可能
勝出。目前的五項 benchmark 都快於 JVM；重現方式見 `sh scripts/bench.sh`。

## 垃圾回收

- **保守式標記清除**。物件不搬移，所以 C 端的暫存指標永遠有效。
- 根：shadow stack（`ty_roots`／`ty_sp`）、以 `ty_gc_register_static` 註冊的靜態欄位
  位址表、以及**原生堆疊的保守掃描**（起點為當前堆疊指標，終點為執行緒堆疊頂端，
  由 `pthread_getattr_np` 取得）。堆疊上的字不保證是物件，所以每個候選位址都要通過
  `valid_obj`：必須 16 位元組對齊、必須是某個 chunk 裡某個區塊的開頭（`starts`
  位元圖每次回收前重建，落在區塊內部的位址一律不算），而且不能是已釋放的區塊。
- 標記：`tyclass.refoffs` 列出每個類別需要追蹤的參考欄位位移；陣列用 `refs` 旗標。
- 清除：未標記的區塊進入大小分級的 free list（`TY_NCLASS` 級，過大的走 `bigfree`），
  下一次配置優先重用；區塊大小字（header 第一個字）的最高位 `TY_FREE_BIT` 表示已釋放，
  free list 的鏈結就放在第二個字。完全空掉的 chunk 直接 `free` 還給系統，所以長時間
  執行的程式不會一直佔住尖峰記憶體，但**頭 chunk 例外**：它承載 bump 指標，永遠保留。
- 觸發：配置量超過 `ty_gc_threshold`，或頭 chunk 裝不下下一個區塊而 free list 也沒有
  可用區塊時。門檻初始 4 MB，每次回收後設為存活量的兩倍，最低不低於 4 MB。
- 已知代價：每次回收都要掃描整個使用中的堆疊，且沒有分代假設；清除階段還要走過每個
  保留 chunk 的每個區塊。

## 例外

`ty_cur_catch` 是一條 handler 鏈。`throw` 呼叫 `ty_throw`，後者 `longjmp` 到最近的
handler；沒有 handler 時印出訊息並以狀態 1 結束。

`finally` 有兩條路徑，缺一不可：

1. **例外路徑**：`try` 外層包一個 handler，`setjmp` 回來後先跑 `finally`，
   再把例外重拋。catch 區塊內再拋出時也走同一條路。
2. **正常離開路徑**：`return`、`break`、`continue` 不會經過 `longjmp`，
   所以程式碼產生器維護一個 finally 堆疊（`Emitter.finallys`），在每個
   跳躍敘述前先跑完被離開的 `finally`（由內而外），最後才跳。
   `try`-with-resources 的 `close()` 是同一個機制的隱含 `finally`，
   因此資源在 return 與例外兩條路徑上都會關閉。

區域與匿名類別捕獲的區域變數會變成合成類別的欄位（`Class.CapFields`），
由建構子或 closure 建立運算式填入；這讓「方法參考的接收者」也只在建立時求值一次。
