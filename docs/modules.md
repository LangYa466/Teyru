# Teyru 的模組系統

模組是「一組一起版控、一起抓取的套件」，身分是它的**模組路徑**（例如
`example.com/app`）。匯入路徑是模組路徑加上套件在模組裡的位置：

```
example.com/app               模組（github.com/x/y 那種倉庫位址）
example.com/app/util          模組裡的 util 套件
example.com/greeting/text     被依賴模組裡的 text 套件
```

模組系統由 `internal/mod` 實作，建置端接在 `internal/driver`，命令列在
`cmd/teyru`。

---

## teyru.mod

放在模組根目錄，是「這個目錄是一個模組」的標誌。建置會從被編譯的路徑往
上找，找到的第一個 `teyru.mod` 就是這個建置所屬的模組。

```
module example.com/app

teyru 1

require (
	example.com/greeting v0.1.0
)
```

| 指令 | 意義 |
|---|---|
| `module <路徑>` | 模組路徑，必寫、只能一次。這個模組所有套件的匯入路徑都以它開頭。 |
| `teyru <n>` | 語言版本。模組說自己寫給哪一代語言，將來的編譯器才有依據拒絕它得重新詮釋的模組。 |
| `require <路徑> <版本>` | 依賴。可單行，也可寫成 `require ( ... )` 區塊；`teyru mod tidy` 一律整理成區塊。 |

`//` 之後是註解。**不認識的指令是錯誤**：`exclude`、`replace`、`go` 這些 Go
的指令都在拒絕之列。默默忽略它會讓 `teyru mod tidy` 把檔案改寫成意思不一樣
的檔案。

### 版本

`vMAJOR.MINOR.PATCH`，可加 prerelease（`v2.0.0-rc1`）。版本同時是 git tag 與
快取裡的目錄名，所以**只認一種寫法**：`1.2.3`、`v1.2`、`v01.2.3`、
`v1.2.3+build` 全部拒絕——它們都能指向同一個 tag，接受就等於同一個版本有兩個
名字。

從 v2 起，主版本號屬於匯入路徑（`example.com/dep/v2`）。路徑與版本必須互相
吻合：`example.com/dep/v2@v1.0.0` 是把 v1 的原始碼放進要求 v2 的程式裡，兩個
方向都拒絕。

### 標準格式

`teyru mod tidy` 寫出來的一定是標準格式，而且再跑一次不會產生差異：

```
module example.com/app

teyru 1

require (
	example.com/greeting v0.1.0
	example.com/other    v1.2.3
)
```

`module` 在最前面，空行，`teyru`，空行，require 區塊：路徑排序、版本對齊成
一欄。單行的 `require x v1.0.0` 也會展開成區塊，因為下一次 `teyru get` 就會
再加一行，而那就是檔案接下來的樣子。註解留在作者放的位置。

---

## 匯入

兩種拼法都可以，指的是同一個套件：

```
import example.com/greeting/text   // 模組的拼法
import example.com.greeting.text   // 點的拼法
```

剖析器沿用的是 Java 的匯入語法，只是路徑段接受 `/` 與 `-`，所以
`example.com/greeting/my-util` 是一個合法路徑，會正規化成套件名
`example.com.greeting.my-util`。診斷算出來的行列位置因此指著磁碟上真正的
字元，不必先改寫原始碼再解析。

`import static example.com/dep/pkg.Widget.icon` 匯入型別的單一靜態成員（`Widget.*`
匯入它全部；static 匯入只寫到型別本身是 `TY-TYP-0086`，因為它要的是成員名）。
`import example.com/dep/pkg.Widget` 匯入那個型別，沒有尾綴的
`import example.com/dep/pkg` 匯入整個套件。

### 套件的身分是匯入路徑

建置會把每個檔案的 `ast.File.Package` 改寫成它所屬套件的**匯入路徑**。宣告的
套件名只是名字，兩個模組都可以宣告 `package util` 而不相撞——把它們分開的是
匯入路徑，而 C 符號再由 `util.Mangle` 把 `/`、`.` 換成 `_`。

同一個目錄只能宣告一個套件，否則報 `TY-IO-0104`：靜默合併兩個套件會編出一個
匯入從未要求的程式。

### 依賴的載入

- 解析採用**最長前綴**：模組路徑 `example.com/a` 是 `example.com/a` 與
  `example.com/a/pkg` 的前綴，而匯入指的是比較明確的那一個。
- 套件是「在模組裡最長的一段、而且是磁碟上真的有 `.teyru` 的目錄」；後面剩下
  的是型別或成員的名字。所以 `example.com.dep` 與 `example.com.dep.Widget` 都
  停在模組根目錄。
- 一個目錄就是一個套件，**不遞迴**：相依模組的 `cmd/` 底下還有第二個進入點，
  順手拉進來就會編出匯入沒有要求的程式。
- 載入是**延遲**的：`require` 了但快取沒有的模組，在真的有原始碼匯入它之前
  都不算錯。模組檔列的是作者可能需要的東西，建置不該為一行沒人用的宣告失敗。

---

## 模組快取

```
$TEYRUPATH/pkg/mod/example.com/greeting@v0.1.0/
```

`TEYRUPATH` 預設 `~/.teyru`。抓取是對模組路徑做一次淺層 clone：

```
git clone --depth 1 --branch <版本> https://<模組路徑> <暫存目錄>
```

clone 完會刪掉 `.git`（模組的雜湊算的是原始碼，不是歷史），再把整個目錄**改名**
進快取：快取條目要嘛完整出現、要嘛不存在，讀快取的建置不會看到半個模組。

`TEYRU_GIT_URL` 換掉 `https://` 這段前綴，給鏡像、`file://` 路徑或本機伺服器
用；Teyru 不做 vanity URL 查詢（Go 的 `?go-get=1`），所以自訂網域的模組就是
用它指定來源。

已經在快取裡的版本**不會再抓**：模組的某個版本是不變的，第二次抓只可能抓出
一個不一樣的副本，而每次建置重驗的 checksum 就是證明第一次是對的。

`mod.LocalFetcher` 是同一件事的無網路版本：從 `<root>/<模組>@<版本>/` 複製。
測試用它，`tests/modules/fixtures` 就是這種鏡像。

---

## teyru.sum

每一行記一個已經抓下來的模組版本：

```
example.com/greeting v0.1.0 h1:Q5zOZ...
example.com/greeting v0.1.0/teyru.mod h1:ujwgc...
```

雜湊跟 Go 一樣是 `dirhash.Hash1`：把每個檔案算成 `sha256(內容)  <相對路徑>\n`
這一行，依路徑排序串起來再取一次 sha256，base64 之後加 `h1:`。點開頭的目錄
（fetch 留下的 `.git`）與符號連結都不算——前者不是模組的一部分，後者會讓一個
模組把磁碟上任何東西包進來。

第二種行記的是相依模組**自己的 `teyru.mod`**：那個檔案的內容決定建置清單裡還
有哪些模組，改它一行就能改掉整個建置，卻不動任何原始碼。它不強制要有——樹狀
雜湊本來就涵蓋了那個檔案的位元組。

驗證的時機是**用到套件的時候**，不是列出依賴的時候。所以：

| 情況 | 結果 |
|---|---|
| 快取有、`teyru.sum` 有、兩者一致 | 建置 |
| 快取沒有 | `TY-IO-0102`，訊息裡有 `teyru get <模組>@<版本>` |
| 快取有、`teyru.sum` 沒有這一行 | `TY-IO-0102`，要求先 `teyru get` |
| 快取有、雜湊不一樣 | `TY-IO-0103`，**停止建置** |

最後一種是整個模組系統裡唯一在講安全性問題的錯誤：同一個版本、不同的內容。
建置不會把「不是模組檔當初寫的那一份」的東西編進去。

---

## 指令

```sh
teyru mod init example.com/myapp   # 在目前目錄寫一個 teyru.mod
teyru get example.com/dep@v1.0.0   # 抓進快取、記 checksum、寫進 require
teyru mod tidy                     # 讓 teyru.mod 與 teyru.sum 跟原始碼一致
teyru build [paths...]             # 編成原生執行檔（預設 a.out）
teyru run   [paths...] [-- args]   # 編好並執行
```

`teyru mod tidy` 做三件事，而且**永遠不連網**：

- 沒有任何原始碼匯入的 require 拿掉；
- 有原始碼匯入、而快取拿得出來的模組，用快取裡最高的版本加進去；
- `teyru.sum` 只留建置清單要的東西，留下的每個模組都重驗一次雜湊。

它**不會**主動提高已經記著的版本：那是個有後果的決定，原始碼看不出來，所以只
回報（`kept the requirement ...`）。快取拿不出來的模組也只回報，並保留它的
checksum 行——重算不了，刪掉只會把缺的那次抓取藏起來。

路徑的寫法：

| 寫法 | 意思 |
|---|---|
| `<目錄>` | 以該目錄為根的整棵套件樹（遞迴） |
| `./...` | 同上；`...` 單獨寫不算路徑 |
| 不寫 | 目前目錄所屬的模組；不在模組裡時就是目前目錄 |

目錄裡有 `teyru.mod` 就是另一個模組，走訪到它會停下來：它的套件在自己當主角時
才編。不寫路徑的 `teyru build` 在模組裡就是編這個模組。

---

## 診斷碼

| 代碼 | 時機 |
|---|---|
| `TY-IO-0101` | `teyru.mod` 讀不到或不是模組檔（解析錯誤、版本不合法、不認識的指令⋯）。 |
| `TY-IO-0102` | 匯入找不到可以提供它的模組、快取裡沒有那個版本、`teyru.sum` 沒有那筆，或套件目錄讀不到。 |
| `TY-IO-0103` | checksum 不符。快取的內容不是 `teyru.mod` 當初寫的那一份。 |
| `TY-IO-0104` | 同一個目錄裡宣告了兩個不同的套件名。 |

這四個代碼在 `docs/diagnostics.md` 也各有一列，寫著訊息與修法。

---

## 與 Go 不同的地方

刻意的取捨，不是還沒做：

- **沒有 proxy、沒有 vanity URL 查詢**。模組路徑就是倉庫網址，抓到的就是作者
  推上去的東西。要鏡像就用 `TEYRU_GIT_URL`。
- **`replace`、`exclude`、`retract`、`vendor` 都沒有**，出現就是解析錯誤。
- **版本選擇是「要求最高的贏」**，不是完整的 MVS：建置清單只由看得到的
  `teyru.mod` 決定。已經被編過套件的模組被別的依賴要求更高的版本時，
  報 `versionConflict` 而不是默默換掉——一個建置不能同時有兩個版本的模組。
- **建置不連網**。快取沒有的東西要自己 `teyru get`。
- **主模組自己的套件也有匯入路徑身分**，跟依賴一視同仁。

## 已知限制

- 沒有 `teyru mod why`、`teyru list`、`teyru get` 的升級語法。
