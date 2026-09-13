# webapp — 模組、容器、路由與 JSON 一起用

一個最小的 todo 服務，示範這個專案的四塊東西怎麼接起來：模組（`teyru.mod`）、
容器（`@Service` 與 `@Autowired`）、路由（`@RestController` 與 `@GetMapping`）、
以及物件與 JSON 的雙向綁定（`record Todo` 直接當回應主體，`record NewTodo`
直接從請求主體解析）。

## 跑起來

```sh
teyru run                # 建置所在模組，監聽 8080
teyru build ./...        # 或編成執行檔
```

## 打它

```sh
curl -s localhost:8080/todos                          # count=0 q=
curl -s -X POST -d '{"text":"寫文件"}' localhost:8080/todos  # {"id":1,"text":"寫文件","done":false}
curl -s -X POST -d '{"text":"測試"}'   localhost:8080/todos  # {"id":2,"text":"測試","done":false}
curl -s localhost:8080/todos/1                    # {"id":1,"text":"寫文件","done":false}
curl -s -X POST localhost:8080/todos/2/done       # {"id":2,"text":"測試","done":true}
curl -s localhost:8080/todos/count                # count=2
curl -s localhost:8080/todos/9                    # 空主體（記錄不存在）
```

請求主體與回應主體都是編譯器產生的綁定：`@RequestBody NewTodo` 由編譯器改寫成它
為 `NewTodo` 產生的讀取器，回傳的 `Todo` 由它產生的寫入器序列化。兩邊都沒有手寫的
解析或轉型。

`count` 兩次之間會累加，因為 `TodoStore` 是單例：容器只建它一次，兩個請求拿到
同一個物件。那不是快取，是 bean 的定義。

## 這裡沒有什麼

沒有執行緒，所以一次只服務一個連線；沒有資料庫，狀態在行程裡；沒有
`Content-Type` 協商，回傳型別決定格式。細節與理由見
[docs/framework.md](../../docs/framework.md)。
