# 框架：Spring 形狀的容器與 web 層

`lib/17_container.teyru`、`lib/18_web.teyru` 加上 `internal/sema/framework*.go` 構成
一個應用程式框架，介面照 Spring 抄，實作方式不同。

## 為什麼不是反射

Spring 在啟動時掃描 classpath、讀註解、用反射建立與注入 bean、把請求綁到方法。
Teyru 沒有反射——但它在**編譯期知道整個程式**：每個類別、每個欄位、每個建構子、
每條繼承邊。所以這些工作在編譯期做完，而不是啟動時做完。

兩邊的差別是具體的：

| | Spring | Teyru |
|---|---|---|
| bean 從哪來 | 執行期掃描 classpath | 編譯器列舉所有 `@Component` 等標註的類別 |
| 注入誰 | 執行期依型別查詢 | 編譯期解析成 bean 名稱（`ctx.getBean("userRepo")`） |
| 缺少 bean | `NoSuchBeanDefinitionException`，啟動時 | `TY-TYP-0103`，編譯期，附原始碼位置 |
| 循環相依 | `BeanCurrentlyInCreationException`，啟動時 | `TY-TYP-0107`，編譯期，印出環 |
| 執行期新增 bean | 可以 | 不行 |
| 啟動成本 | 掃描與反射 | 沒有——註冊表是靜態初始化填好的 |

代價是明確的：不能執行期擴充。好處也是：整類錯誤從「部署之後才看到」變成「編譯
不過」。

## 容器

```teyru
import teyru.Service
import teyru.Repository
import teyru.Autowired
import teyru.Value
import teyru.PostConstruct

@Repository
class UserRepo {
  String find(String id) { return "user " + id }
}

@Service
class UserService {
  @Autowired UserRepo repo
  @Value("${app.name:unnamed}") String appName
  private boolean ready = false

  @PostConstruct
  void start() { ready = true }

  String describe() { return appName + ": " + repo.find("7") }
}

class Main {
  public static void main(String[] args) {
    ApplicationContext ctx = new ApplicationContext()
    ctx.setProperty("app.name", "demo")
    ctx.refresh()
    UserService s = (UserService) ctx.getBean(UserService.class)
    System.out.println(s.describe())
  }
}
```

### 支援的註解

| 註解 | 意思 |
|---|---|
| `@Component`／`@Service`／`@Repository`／`@Controller`／`@RestController` | 這個類別是 bean；名稱預設是首字母小寫的類別名，`@Component("x")` 可改 |
| `@Configuration` + `@Bean` | 這個類別本身也是 bean，它的 `@Bean` 方法各產生一個 bean |
| `@Autowired` | 欄位、建構子參數或 `@Bean` 方法參數由容器提供 |
| `@Qualifier("name")` | 同型別有多個時指名 |
| `@Primary` | 同型別有多個時的預設選擇 |
| `@Value("${key}")`／`@Value("${key:default}")` | 注入屬性 |
| `@PostConstruct` | 建好並注入後呼叫（無參、回 void） |
| `@Scope("prototype")` | 每次查詢都建新的；預設 singleton |

### 建構子選擇

Spring 4.3 起的規則：只有一個建構子就用它，否則找標了 `@Autowired` 的，否則用無參
的那個。兩個都標或都沒有而有多個，是 `TY-TYP-0105`。

### 生命週期

`refresh()` 會把每個 singleton 都建起來，所以缺相依會在啟動時（而不是第一次請求
時）報出來。bean 的建立是遞迴的：要 A 就先建它需要的 B。`creating` 旗標擋住環。

`@PostConstruct` 由產生的注入器在注入完成後呼叫。`@PreDestroy` 已宣告但**沒有**
執行——Teyru 沒有行程關閉鉤子，容器也沒有 `close()`。

## Web 層

```teyru
import teyru.RestController
import teyru.RequestMapping
import teyru.GetMapping
import teyru.PathVariable
import teyru.RequestParam
import teyru.RequestBody

@RestController
@RequestMapping("/pets")
class PetController {
  @Autowired PetStore store

  @GetMapping("/{id}")
  Pet one(@PathVariable("id") int id) { return store.find(id) }

  @GetMapping("")
  String list(@RequestParam(value = "q", defaultValue = "all") String q) {
    return "q=" + q
  }

  @PostMapping("")
  Pet create(@RequestBody String body) { return new Pet(body, 1) }
}
```

| 註解 | 意思 |
|---|---|
| `@RequestMapping`（類別上） | 路徑前綴 |
| `@RequestMapping`（方法上） | 路徑與 `method`（不寫是任何動詞） |
| `@GetMapping`／`@PostMapping`／`@PutMapping`／`@DeleteMapping`／`@PatchMapping` | 路徑與動詞 |
| `@PathVariable` | 路徑裡 `{name}` 的值，會轉成參數的型別 |
| `@RequestParam` | 查詢參數，`defaultValue` 可給預設 |
| `@RequestHeader` | 請求標頭（名稱不分大小寫），`defaultValue` 可給預設 |
| `@RequestBody` | 請求主體 |
| `@ResponseBody` | 已宣告；`@RestController` 本來就隱含，所以有沒有都一樣 |

**參數轉型**：路徑與查詢參數都是字串，所以 `int`、`long`、`double`、`float`、
`short`、`byte`、`boolean` 的參數會經由對應的 `parseX` 轉換（`int` 用
`Integer.parseInt`）。

**回應**：回傳 `HttpResponse` 就完全自己決定；回傳 `String` 是 `text/plain`；回傳
`void` 是空主體；其他型別以 Gson 綁定序列化成 `application/json`（見
`docs/json.md`）。

**路由**：`Router.match` 取最特定的符合——字面片段勝過變數片段，所以
`/pets/mine` 不會被 `/pets/{id}` 吃掉，與註冊順序無關。路徑存在但動詞不對是 405，
路徑不存在是 404。

### 伺服器

```teyru
ApplicationContext ctx = Application.boot(args)
Router router = Application.routerFrom(ctx)
HttpServer server = new HttpServer(port, router, ctx)
```

`HttpServer.handle(HttpRequest)` 是請求進來後唯一的入口——它與 socket 迴圈分開，
所以不需要連線就能測（`tests/programs/t102_web.teyru` 就是這樣測的）。

## 已知限制

1. **一次處理一個連線。** 語言沒有執行緒（`docs/language.md` §13），所以第二個連線
   要等第一個處理完。這對「會回應請求的程式」夠用，對「服務一群人」不夠；形狀已經
   是 thread-per-connection 需要的形狀。
2. **沒有內容協商。** 只看方法的宣告型別，不看 `Accept`。
3. **回傳陣列或 `List` 還沒有 JSON 映射**（`TY-TYP-0111`），因為綁定尚未支援它們。
4. **只有 `Application.boot` 讀 `--key=value` 形式的命令列參數**寫進屬性；沒有
   `application.properties` 檔案的讀取。
5. **`@PreDestroy` 不執行**；沒有 `@Conditional`、`@Profile`、`@Import`、
   `@Lazy`、AOP、交易、`@ExceptionHandler`。
6. **沒有 `@ComponentScan` 的範圍控制**：整個程式都是掃描範圍，因為編譯器看得見
   全部——要排除什麼，就不要標註它。
