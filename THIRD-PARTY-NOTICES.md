# Third-party notices / 第三方元件聲明

本文件列出 Teyru 專案實際使用與散布的第三方元件。專案本身的授權見 `LICENSE`。

> 編譯器只用 Go 標準函式庫，`go.mod` 沒有任何外部模組依賴；產生的執行檔只連到
> 系統 C 函式庫與本專案自帶的執行期。因此本清單很短，而且是完整的。

---

## 1. 建置編譯器所需

| 元件 | 用途 | 授權 | 散布方式 |
|---|---|---|---|
| Go 標準函式庫（`go1.26` 以上） | 建置 `cmd/teyru` 與 `internal/*` | BSD-3-Clause（Copyright The Go Authors） | 不散布，使用者自行安裝 |

Go 標準函式庫以 BSD 3-Clause 授權釋出，條文見
<https://go.dev/LICENSE>。本專案未修改標準函式庫，也未將其嵌入產物。

---

## 2. 產生執行檔所需（後端）

| 元件 | 用途 | 授權 | 散布方式 |
|---|---|---|---|
| clang / LLVM（預設後端） | 將產生的 C 編譯成原生執行檔 | Apache-2.0 with LLVM Exceptions | 不散布，使用者自行安裝；亦可用 gcc 取代 |
| GCC（替代後端） | 同上 | GPL-3.0-or-later（執行期例外） | 不散布，使用者自行安裝 |
| libc / libm / libpthread | 執行期使用的系統函式庫 | 依系統而異（多為 LGPL-2.1+ 或 MIT） | 動態連結，不散布 |

產生的執行檔**連結**這些函式庫，但不包含其原始碼。Teyru 執行期
（`internal/runtime/src`）本身是本專案的原創程式碼，與上述元件無授權關聯。

---

## 3. 執行期與標準程式庫

| 元件 | 來源 | 授權 |
|---|---|---|
| Teyru 執行期（`tyrt.h`、`tyrt.c`、`tyrt2.c`） | 本專案原創 | 見 `LICENSE` |
| Teyru 標準程式庫（`lib/*.teyru`） | 本專案原創，以 Teyru 撰寫 | 見 `LICENSE` |

沒有使用任何第三方 C 函式庫（沒有 Boehm GC、沒有 libgc、沒有 mimalloc 之類的
配置器）：垃圾回收器、字串、陣列、例外與 box 類別都是本專案自行實作。

---

## 4. 測試與工具

| 元件 | 用途 | 授權 |
|---|---|---|
| Go 測試框架（`testing`） | `go test ./...` | BSD-3-Clause（Go 標準函式庫的一部分） |
| OpenJDK / HotSpot | **只用於** `scripts/bench.sh` 的對照量測 | GPL-2.0 with Classpath Exception |

`scripts/bench.sh` 需要 `java` / `javac` 才會執行 JVM 那一半；若系統沒有安裝，
腳本只會跳過該部分，不影響 Teyru 的建置與測試。JVM 不是 Teyru 的執行環境，
也不是任何產物的依賴。

---

## 5. 授權檔案

| 檔案 | 說明 |
|---|---|
| `LICENSE` | 本專案的主要授權 |
| `LICENSE-CLASSPATH-EXCEPTION-2.0` | Classpath Exception 全文（條文來自 OpenJDK，隨主要授權一併保留） |

若發行時修改了上述任何一項（例如把 clang 靜態連結進產物，或把 Go 標準函式庫
嵌入發行包），必須重新產生本文件並附上對應的授權全文。
