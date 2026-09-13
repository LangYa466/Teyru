# 模組測試的素材

`internal/mod` 的測試用這裡的檔案，全部不需要網路：`TEYRUPATH` 指向一個臨時
目錄，抓取由 `mod.LocalFetcher` 從 `fixtures/` 複製，而不是 `git clone`。
`fixtures/` 本身就是一份快取該有的樣子，所以它也可以直接當成 `TEYRUPATH` 底下
的 `pkg/mod` 手動複製進去看。

| 路徑 | 是什麼 |
|---|---|
| `fixtures/example.com/greeting@v0.1.0/` | 被依賴的模組，目錄名就是快取裡的名字（`<module>@<version>`） |
| `app/` | 一個模組：`teyru.mod` 依賴上述模組，`teyru.sum` 記著它的雜湊 |
| `app/expected` | `app` 編譯並執行後的預期輸出 |
| `badsum/` | 同一套設定但 `teyru.sum` 的雜湊是錯的，必須以 `TY-IO-0103` 拒絕 |

修改 `fixtures/` 底下任何檔案都會讓 `app/teyru.sum` 失效（這正是它存在的
理由）。重新產生：

```sh
go test ./internal/mod -run TestTeyruSumMatchesFixture -v   # 會印出正確的雜湊
```
