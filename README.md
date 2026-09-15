# Teyru

**以 Java 的形狀寫，編成原生執行檔。**

沒有 JVM、沒有 bytecode、沒有 JAR、沒有 JDK，標準程式庫用 Teyru 自己寫。編譯器用 Go
寫，只用標準函式庫。

```teyru
class Main {
  public static void main(String[] args) {
    System.out.println("hello, Teyru")
  }
}
```

```sh
go install github.com/teyru-lang/Teyru/cmd/teyru@latest
teyru run hello.teyru
```

## 兩個後端

| | |
|---|---|
| `teyru build`（預設） | 產生 C，交給 clang 或 gcc。整個標準程式庫都在這條路上驗證 |
| `teyru build --backend=llvm` | **自己產生 LLVM IR**，程式不經過 C；clang 只負責組譯與連結。目前涵蓋語言的一大部分，其餘用 `TY-INT-` 診斷**明確拒絕**，不會退回 C 後端 |

## 平台

| 目標 | 狀態 |
|---|---|
| linux/amd64 | 原生，整套測試都跑 |
| windows/amd64 | 已建置並以 Wine 執行；當時的 195 支測試程式有 179 支輸出逐位元組相同，差異的 16 支已逐一歸因（14 支在改動前的編譯器上用 gcc 也會失敗，2 支是 NTFS 檔名與 POSIX 路徑的事實） |
| linux/arm64、darwin/amd64、darwin/arm64 | 已實作，由 CI 建置與執行 |

執行期把平台相依集中在 `internal/runtime/src/tyrt_plat.h` 後面（POSIX 與 Windows 各一份），
`--target <os>/<arch>` 選擇編譯器、旗標與輸出檔名。拿不到的 runner 不請求——見
`.github/workflows/ci.yml` 開頭的說明。

## 標準程式庫

* **集合與工具**：`List`／`Set`／`Map`／`Deque` 與其實作、`Arrays`、`Collections`、
  `Comparator`、`Optional`、`UUID`、`StringBuilder`、`Stream`、`java.util.function`。
* **文字與時間**：`String` 的完整介面、`String.format`、`java.util.regex`、
  `LocalDate` 家族、`Scanner`、`java.io` 的緩衝與二進位資料流（含 Java 的 modified
  UTF-8）、`HexFormat`。
* **JSON**：`JsonElement` 樹、解析與輸出，以及 Gson 形狀的綁定——`Gson`、`GsonBuilder`
  （`serializeNulls`／`setPrettyPrinting`／`setLenient`／欄位命名策略／排除修飾子）、
  `@Expose`／`@Since`／`@Until`／`@SerializedName`／`@JsonAdapter`、
  `JsonSerializer`／`JsonDeserializer`、串流 `JsonReader`／`JsonWriter`。容器會依**宣告的
  元素型別**綁定物件，包含陣列。
* **Web**：HTTP/1.1 伺服器（keep-alive、chunked、cookie、表單、multipart、gzip）、
  WebSocket（RFC 6455）、HTTP 客戶端，以及 Spring Boot 形狀的容器——`@RestController`、
  `@RequestMapping` 家族、`@RequestBody`／`@PathVariable`／`@RequestParam`、
  `@ConfigurationProperties`、`@Profile`、`@PreDestroy`、`@ControllerAdvice` ＋
  `@ExceptionHandler`、`HandlerInterceptor`、靜態檔案、CORS、`ResponseEntity`、
  `MockServer`、會話與逾時、驗證註解。
* **並行**：真正的作業系統執行緒（`Thread`、`Runnable`、真正的 `synchronized`、
  `Object.wait`／`notify`），以及 `java.util.concurrent` 的形狀——`Executors`、
  `ExecutorService`、`Future`、`Callable`、`CountDownLatch`、`AtomicInteger`／
  `AtomicLong`、`ConcurrentHashMap`。
* **其他**：`MessageDigest`（MD5、SHA-1／224／256／384／512）、`CRC32`、`Adler32`、
  deflate／inflate 與 gzip、`BigInteger`／`BigDecimal`、反射（`java.lang.reflect` 的形狀，
  含註解的中繼資料）、`java.util.logging` 形狀的日誌。

## 量測

完整表格在 <https://docs.teyru.dev>，方法是 `RUNS=5 sh scripts/bench.sh`（五次取最佳、整支
程式的 wall time、含行程啟動、`-O2`），同一台機器上與 HotSpot 對照：啟動快約 26 倍、
hello 執行檔 54.6 KB（同一支程式在 `-O1` 是 75,232 位元組）、尖峰記憶體約 12 倍少，
`bench_fib`／`bench_oop`／`bench_string` 快 3.5～5 倍。**也有輸的一項**：`bench_invoke`
慢約 2.3 倍，原因是 Teyru 的**直接呼叫**迴圈本身就比 Java 慢 2.6 倍，反射再疊上機器成本；
推測與已知落後情境都寫在 `AGENTS.md` §10。

## 安裝與建置

```sh
go install github.com/teyru-lang/Teyru/cmd/teyru@latest     # 或從原始碼：
git clone --recurse-submodules https://github.com/teyru-lang/Teyru
cd Teyru
make build     # 產生 ./teyru
make test      # 單元 + 端到端（需要 clang 或 gcc）
```

`tests/` 是 submodule，而**它現在自己就能跑**：`TEYRU=<編譯器> sh tests/run.sh` 不需要 Go、
不需要編譯器原始碼樹。忘記 `--recurse-submodules` 時 `make test` 會直接說，不會安靜地跑零個
測試。

## 文件

完整文件在 **<https://docs.teyru.dev>**：語言參考、診斷碼、Lombok 相容層、JSON 綁定、
Web 框架、模組系統、原生互通、架構。原始檔在
[`teyru-lang/docs`](https://github.com/teyru-lang/docs)（fumadocs，英／日／簡中版本
也在那裡）。

## 這個組織的其他倉庫

| 倉庫 | 內容 |
|---|---|
| [`teyru-lang/tests`](https://github.com/teyru-lang/tests) | 端到端測試資料，本倉庫以 submodule 掛在 `tests/`；`run.sh` 讓它自己就能執行 |
| [`teyru-lang/docs`](https://github.com/teyru-lang/docs) | 文件站（<https://docs.teyru.dev>） |
| [`teyru-lang/editors`](https://github.com/teyru-lang/editors) | VS Code／Zed／JetBrains IDEA 擴充、tree-sitter 文法 |
| [`teyru-lang/website`](https://github.com/teyru-lang/website) | 官網原始檔（<https://teyru.dev>） |

貢獻前請讀 [`AGENTS.md`](AGENTS.md)：程式風格、測試規範、文件規範與送出前檢查清單
都在那裡。`AGENTS.md` §10 是**已知限制**，包括尚未實作與仍在退步的東西——要看誠實的
狀態就看那裡。

授權見 [LICENSE](LICENSE) 與 [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md)。
