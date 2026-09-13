# 原生互通範例

Teyru 宣告、C 實作，兩者一起編譯成同一個執行檔。

```sh
# 1. 讓編譯器寫出要實作的宣告（沒有給 --native，所以在這裡停下來，不嘗試連結）
go run ./cmd/teyru build --native-header native.h examples/native/main.teyru

# 2. 照著它寫 impl.c，然後把兩者一起編譯
go run ./cmd/teyru run --native examples/native/impl.c examples/native/main.teyru
```

第 1 步只寫標頭就結束：宣告了 native 方法卻還沒有人實作時，連結一定會失敗，所以
`--native-header` 在沒有同時給 `--native` 的情況下不進後端。給了實作就會一起建置
（`Build`／`Compile` 的呼叫端如果要兩者，傳 `Native` 就好）。

輸出：

```
5
hello, world
15
```

`impl.c` 裡的每個符號都是 `--native-header` 產生的名字。完整說明見
[docs/native.md](../../docs/native.md)。
