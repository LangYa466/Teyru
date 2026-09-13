# Teyru

[繁體中文](README.md) · **简体中文** · [English](README.en.md) · [日本語](README.ja.md)

**Teyru 是一门独立实现的编程语言：编译器完全用 Go 编写，直接生成原生可执行文件——不依赖 JVM、不依赖 javac、不产生任何 bytecode。**

Teyru 的语法对 Java 开发者非常熟悉（类、接口、泛型、lambda、异常、record、enum、annotation），
但去掉了分号、加入了原生 property，并且以**原生机器码**运行：编译器把整个程序降级为 C，
再交给 clang/LLVM（或 gcc）编译成可执行文件。运行时只有约 1500 行 C，其中包含自己的垃圾回收器
（conservative mark-and-sweep）、字符串、数组与异常实现，没有任何虚拟机。

```
Teyru 源码 (.teyru)
      │  用 Go 写的编译器：lexer → parser → 语义分析 → C 生成器
      ▼
  generated C  ──clang（Clang 前端 + LLVM 中后端）──▶  LLVM IR  ──▶  原生可执行文件
                                                          （无 JVM、无 bytecode）
```

后端是 **LLVM**：`./teyru emit-llvm` 可以直接打印 IR 模块，接 `opt`／`llc`／自定义 pass
都没有问题；只想看 C 也可以用 `./teyru emit`。

**文档**：[语言参考](docs/language.md) · [原生互通](docs/native.md) · [Lombok 兼容层](docs/lombok.md) · [诊断码一览](docs/diagnostics.md) · [编译器架构](docs/architecture.md) · [工作规范](AGENTS.md)

---

## 目录

- [为什么比 JVM 快](#为什么比-jvm-快)
- [快速开始](#快速开始)
- [语言速览](#语言速览)
- [支持的语言特性](#支持的语言特性)
- [标准库](#标准库)
- [编辑器与工具](#编辑器与工具)
- [项目结构](#项目结构)
- [运行时模型](#运行时模型)
- [与 Java 的差异](#与-java-的差异)
- [命令行接口](#命令行接口)
- [开发](#开发)
- [许可](#许可)

---

## 为什么比 JVM 快

同一台机器实测（Linux x86-64、clang 22、OpenJDK 21 Temurin、每个数字取 5 次最佳）：

| 指标 | Teyru（原生） | Java（HotSpot） | 差距 |
|---|---|---|---|
| 启动 100 次总时间 | **0.068 s**（0.68 ms/次） | 1.97 s（19.8 ms/次） | **约 29 倍快** |
| 可执行文件大小 | **35 KB** | JDK 运行时约 200 MB | 约 5900 倍小 |
| 峰值内存（hello） | **2.1 MB** | 50.2 MB | **约 24 倍省** |
| `bench_fib` 递归 | **0.0060 s** | 0.0264 s | **4.4 倍快** |
| `bench_loop` 循环与整数运算 | **0.0209 s** | 0.0429 s | **2.1 倍快** |
| `bench_oop` 对象与虚调用 | **0.0044 s** | 0.0253 s | **5.8 倍快** |
| `bench_string` 字符串处理 | **0.0093 s** | 0.0554 s | **6.0 倍快** |
| `bench_alloc` 短命对象分配 | **0.0233 s** | 0.0308 s | **1.3 倍快** |

**为什么快：**

1. **没有 JVM 启动成本。** 没有 class loading、没有 JIT 预热、没有 GC 线程启动。
2. **编译期能做完的事不留到运行期。** 泛型擦除、调用定址、字符串常量静态分配、
   `static final` 常量折叠、vtable 与接口表都由编译器填好。
3. **没有字节码解释阶段。** clang/LLVM 直接优化整个程序（LTO 跨模块内联、
   常量传播、循环向量化），不需要等 JIT 观察热点。
4. **不需要分配的对象就不分配。** 逃逸分析把“不离开所在方法”的对象放在 C 栈上，
   LLVM 随后把它的字段提升为寄存器、把整个对象消除——与 JVM 的 scalar replacement
   效果相同，`bench_alloc` 正是靠这一点赢过 HotSpot。
5. **分配与边界检查都走行内快速路径。** `ty_alloc` 的指针碰撞分配在头文件内联，
   数组访问只在必要时调用慢路径；GC 会回收完全空掉的 chunk。
6. **可预测的性能。** 没有 deopt、没有预热曲线、没有 GC 调参。

**诚实的边界。** 逃逸分析只覆盖“不离开所在方法”的对象。会存进字段、数组、返回或
交给其他对象的对象仍然走堆与标记清除回收，而 HotSpot 有分代假设，所以在“对象长期
存活、反复回收”的负载上 JVM 仍可能胜出。上面的数字都包含 process 启动，绝对值都很
小；重现方式见 `sh scripts/bench.sh`，五支 benchmark 程序、启动 100 次、可执行文件大小
与峰值内存都由这支脚本测量（大小那一列对照的 JDK 运行时是测量机器上安装的运行时，
不由脚本测量）。

---

## 快速开始

需要 **Go 1.26+** 与 **clang**（或 gcc）。

```sh
# 构建编译器
go build -o teyru ./cmd/teyru

# 编译并运行
./teyru run hello.teyru

# 生成可执行文件
./teyru build -O2 -o hello hello.teyru
./hello

# 查看编译器生成的 C 代码
./teyru emit hello.teyru

# 查看交给 LLVM 的 IR（后端是 clang/LLVM，可交给 opt/llc）
./teyru emit-llvm hello.teyru

# 版本
./teyru version
```

`hello.teyru`：

```teyru
class Hello {
  public static void main(String[] args) {
    System.out.println("Hello, Teyru!")
  }
}
```

注意：**Teyru 不用分号**。每条语句以换行结束；`for` 头部用两个冒号分隔三段。

---

## 语言速览

```teyru
interface Shape {
  double area()
  default String describe() {
    return "area=" + area()
  }
}

class Rect implements Shape {
  public double w
  public double h
  public Rect(double w, double h) {
    this.w = w
    this.h = h
  }
  public double area() {
    return w * h
  }
}

class Circle implements Shape {
  private double r
  public double radius {      // 原生 property
    get {
      return field            // field = 底层存储
    }
    set {
      field = value < 0 ? 0 : value
    }
  }
  public Circle(double r) {
    radius = r
  }
  public double area() {
    return Math.PI * r * r
  }
}

record Point(int x, int y) {
}

enum Color {
  RED, GREEN, BLUE
}

interface Fn<R> {
  R apply(int v)
}

class Main {
  static int twice(int v) {
    return v * 2
  }

  public static void main(String[] args) {
    Shape s = new Rect(3, 4)
    System.out.println(s.describe())

    // for ( init : condition : update )
    for (int i = 0 : i < 3 : i++) {
      System.out.println(i)
    }

    int[] xs = {1, 2, 3}
    for (int x : xs) {
      System.out.print(x)
    }
    System.out.println()

    // lambda 与方法引用
    Fn<Integer> f = (v) -> v + 1
    Fn<Integer> g = Main::twice
    System.out.println(f.apply(41))
    System.out.println(g.apply(21))

    // switch 表达式与类型 pattern
    Color c = Color.GREEN
    String name = switch (c) {
      case RED -> "red"
      case GREEN -> "green"
      default -> "other"
    }
    System.out.println(name)
    System.out.println(describe(c))

    // 异常
    try {
      System.out.println(10 / 0)
    } catch (ArithmeticException e) {
      System.out.println("除以零")
    } finally {
      System.out.println("cleanup")
    }
  }

  static String describe(Object o) {
    return switch (o) {
      case String s -> "string of length " + s.length()
      case Integer i when i.intValue() > 10 -> "big int"
      case Integer i -> "small int"
      default -> "other"
    }
  }
}
```

### Lombok 兼容层

编译器内置 Lombok：注解在语义分析阶段展开成普通的 Teyru 成员，与手写代码走同一条
类型检查与代码生成路径，不需要 annotation processor。

```teyru
import lombok.Data
import lombok.AllArgsConstructor
import lombok.Builder

@Data
@AllArgsConstructor
@Builder
class Person {
  private String name
  private int age
}

class Main {
  public static void main(String[] args) {
    Person p = Person.builder().name("ada").age(36).build()
    System.out.println(p.getName() + " " + p.getAge())
    System.out.println(p)
  }
}
```

完整清单与差异见 **[docs/lombok.md](docs/lombok.md)**：`@Getter`／`@Setter`／`@ToString`／
`@EqualsAndHashCode`／`@Data`／`@Value`／`@Builder`／`@NonNull`／`@Cleanup`／
`@SneakyThrows`／`@Synchronized`／`@With`／`@Accessors`／`@FieldDefaults`／
`@UtilityClass`／`@StandardException`／`@Log` 系列／`@ExtensionMethod`／
`@FieldNameConstants`／`@Delegate`／`@Helper`／`@Tolerate`／`@Locked`／
`@NonFinal`／`@PackagePrivate` 全部支持，包含 `@Singular`（逐项累积、整批加入、
清除、`build()` 取得副本）、`@SuperBuilder`（覆盖整条继承链的字段）与
`@Builder.ObtainVia`。

### Java 25 语法对照

Teyru 以 Java SE 25 最终定案的语法为基准（不含预览功能），保留 Java 语义，
只去掉分号并加入原生 property：JEP 512 紧凑源文件与实例 `main`（含隐式
`println`／`print`／`readln`）、JEP 511 模块导入、JEP 513 弹性构造器本体、
JEP 440 record 模式、JEP 441 switch 模式与 `when` 守卫、JEP 456 未命名变量 `_`、
JEP 507 原生类型 pattern（`case int i`、`o instanceof int i`，精确转换）、
JEP 395 record、JEP 394 `instanceof` 模式、JEP 409 sealed 类（`sealed`／`permits`／
`non-sealed`）、JEP 378 文本块、JEP 361 switch 表达式、JEP 286 `var`。

### 支持的语言特性

| 类别 | 内容 |
|---|---|
| 类型 | 原生类型、类、接口、enum、record、annotation type、泛型（bound／wildcard／diamond／泛型方法）、多维数组 |
| 成员 | 字段、方法、构造器、可变参数、静态与实例初始化块、嵌套／内部／局部／匿名类、`sealed`／`permits` |
| 语句 | `if`、`while`、`do-while`、基本 `for`（冒号头部）、增强 `for`、`switch`（语句／表达式、箭头／冒号、多标签、enum、字符串、类型 pattern + `when` 守卫）、`try`／`catch`／`finally`、try-with-resources、多类型 catch、`throw`、`yield`、`assert`、`synchronized`、标签与 `break`／`continue` |
| 表达式 | 完整运算符与优先级、三元、cast、`instanceof`（含 pattern）、lambda、方法引用（静态／绑定／未绑定／构造器）、匿名类、对象初始化列表、字符串拼接、自动 boxing／unboxing |
| 原生扩展 | 无分号语法、`val`（推断类型的不可重绑定局部变量）、`var`、原生 property（`get`／`set`／`field`）、`for` 的双冒号头部、try-with-resources 以换行分隔 |

完整的语法与语义写在 **[docs/language.md](docs/language.md)**。

---

## 标准库

标准库以 **Teyru 本身**编写（`lib/*.teyru`），每次编译都和使用者程序一起被编译与
检查。包名沿用 Java 的写法，所以 `import java.util.List` 原样可用：

| 包 | 内容 |
|---|---|
| `java.lang` | `Object`、`Class`、`String`（`format`／`join`／`valueOf` 等）、`StringBuilder`、`Math`、`System`、`PrintStream`、八个包装类和 `Number`、`Throwable` 家族、`Enum`、`Record` |
| `java.util` | `List`／`ArrayList`／`LinkedList`、`Set`／`HashSet`／`LinkedHashSet`／`TreeSet`、`Map`／`HashMap`／`LinkedHashMap`／`TreeMap`、`Deque`／`ArrayDeque`、`Arrays`、`Collections`、`Objects`、`Optional`、`StringJoiner` |
| `java.time` | `LocalDate`／`LocalTime`／`LocalDateTime`／`Instant`／`Duration`／`Period` |
| `java.io` | `File`、`Path`／`Paths`、`Files` |
| `java.util.regex` | `Pattern`／`Matcher` |
| `java.net` | `ServerSocket`、`Socket` 及其输入输出流 |
| `java.util.stream` | `Stream`／`IntStream`／`LongStream`／`DoubleStream`、`Collectors`、`Collector`、`Spliterator`；惰性求值，入口是 `Collection.stream()` |
| `java.math` | `BigInteger`、`BigDecimal`、`MathContext`、`RoundingMode` |
| `java.text` | `NumberFormat`／`DecimalFormat`（完整 pattern 语言）、`DateFormat`／`SimpleDateFormat`、`DateTimeFormatter`、`MessageFormat`；只做 ROOT／en-US，`format` 用 `Instant` |
| `com.google.gson` | Gson 的树状 API，以及由编译器生成的对象绑定（[docs/json.md](docs/json.md)） |
| 框架 | Spring 形状的容器与 web 层（[docs/framework.md](docs/framework.md)） |

集合以 Teyru 编写，所以 `for` 循环直接支持：

```teyru
List<String> names = new ArrayList<String>()
names.add("ada")
for (String n : names) {
  System.out.println(n)
}
```

依赖用 `teyru.mod` 声明，获取与校验照 Go 的做法（[docs/modules.md](docs/modules.md)）：

```sh
teyru mod init example.com/app
teyru get example.com/greeting@v0.1.0
teyru build ./...
```

没有反射、没有线程、没有 `Stream`、没有 `BigDecimal`、没有时区数据库——这些缺席都是刻意的，理由记在
[docs/language.md](docs/language.md) §11 与 §13。

需要自己的原生库时，声明 `native` 方法并用 C 实现：

```teyru
class Native {
  public static native int add(int a, int b)
}
```
```sh
teyru build --native-header native.h program.teyru   # 生成要实现的原型
teyru build --native impl.c program.teyru            # 一起编译
```

完整说明见 [`docs/native.md`](docs/native.md)。

---

## 编辑器与工具

- **VS Code**：`editors/vscode/` 提供 `.teyru` 的 TextMate 语法高亮、语言配置与片段。
  用 `npx @vscode/vsce package` 打包，再用 `code --install-extension teyru-0.1.0.vsix` 安装。
- **tree-sitter**：`editors/tree-sitter-teyru/` 是完整文法，附高亮 query、缩进 query
  与 corpus 测试，Neovim、Helix、Zed 等可直接使用。
- GitHub 目前仍把 `.teyru` 显示为 Java：linguist 还没有 Teyru 的定义，
  `.gitattributes` 先映射到最接近的语法。

---

## 项目结构

| 路径 | 说明 |
|---|---|
| `cmd/teyru` | CLI 入口（`build`／`run`／`emit`／`emit-llvm`／`version`） |
| `internal/driver` | 编译流程：串起前端与 C 后端、调用 C 编译器、处理 native 源文件与输出选项 |
| `internal/source` | 文件、位置换算、诊断容器 |
| `internal/lexer` | 词法分析；换行不产生 token，只在 token 上标记“前面有换行” |
| `internal/parser` | 递归下降解析器，用显著性与前缀完整性判断语句是否结束 |
| `internal/ast` | 语法树、符号（类／方法／字段／变量）、类型 |
| `internal/sema` | 名称解析、类型检查、泛型擦除与推断、重载解析、vtable／selector 分配、property 降级 |
| `internal/codegen` | 生成 C：类→struct、虚调用→vtable、接口调用→itable、switch 降级、GC 根信息 |
| `internal/util` | 前后端共用的工具：名称修饰、类型描述、C 内存布局 |
| `internal/runtime/src` | C 运行时：GC、字符串、数组、异常、boxing、Math／System／StringBuilder |
| `lib` | 用 Teyru 编写的标准库 |
| `tests/programs` | 端到端测试程序与期望输出（`go test` 会逐一编译并比对） |
| `tests/native` | native 方法互通测试：Teyru 声明、C 实现与期望输出（`TestNative`） |
| `examples` | 示例程序与 JVM 对照的 benchmark（`bench_*.teyru` 与 `.java`） |
| `scripts` | 开发脚本：`bench.sh` 性能测量、`pre-commit` 钩子 |
| `docs` | 语言参考、诊断码、架构 |
| `editors` | 编辑器支持：VS Code 扩展与 tree-sitter 文法 |

---

## 运行时模型

- **对象**：C struct，第一栏是 `tyobj { tyclass* cls }`。每个类一张 `tyclass`，
  记录父类、接口、vtable、接口表、GC 需要追踪的引用字段偏移。
- **虚调用**：`obj->cls->vtable[slot]`；**接口调用**：`ty_itab(obj, selector)`。
  每个接口方法有全局唯一的 selector，每个类的接口表由编译期填好。
- **泛型**：编译期擦除，运行期没有泛型信息（与 Java 相同）。
- **异常**：以 `setjmp`／`longjmp` 实现的 handler 链；`finally` 以嵌套 handler
  保证在任何路径（含 catch 内再抛出）都执行。
- **GC**：保守式标记清除。根包含原生栈（保守扫描）、静态字段注册表与寄存器
  （`setjmp` 溢出）。对象不移动，所以 C 端的临时指针永远有效。
- **字符串**：UTF-8 `tystr { tyobj obj; int64 len; char* data }`；字面量是静态对象，
  不经过 GC。
- **数组**：`tyarr { tyobj; len; data; esize; refs }`，元素内嵌在对象后方。

---

## 与 Java 的差异

Teyru 不是 Java 的子集，而是“Java 开发者一看就懂”的独立语言。主要差异：

1. **没有分号。** 分号会被编译器拒绝（`TY-SYN-0001`）。
2. **`for` 头部用冒号**：`for (int i = 0 : i < n : i++)`。
3. **try-with-resources 用换行分隔**，不用分号。
4. **enum 常量区与成员区用一个冒号**分隔（没有成员时可省略）。
5. **原生 property**：字段后面接 accessor 块即成 property；`field` 代表底层存储。
   没有 accessor 块的字段就是普通 Java 字段。
6. **`val`**：推断类型的不可重绑定局部变量（不是深度不可变）。
7. **没有 checked exception 检查**；`throws` 会被解析但不强制。
8. **没有 `System.out.printf`、没有运行期反射、没有 annotation processor**。
9. **不是 bytecode 平台**：没有 `.class`、没有 `java.lang`、没有 JNI，
   目前也**无法**与既有 Java 库互通——这是刻意的取舍。

完整清单见 [docs/language.md](docs/language.md)。

---

## 命令行接口

```
teyru build [flags] <files...>                 编译成原生可执行文件
teyru run   [flags] <files...> [-- args...]    编译后直接运行
teyru emit  [flags] <files...>                 打印生成的 C
teyru emit-llvm [flags] <files...>             打印交给 LLVM 的 IR
teyru version                                  版本
teyru help                                     帮助
```

| 旗标 | 说明 |
|---|---|
| `-o <path>` | 输出文件名（默认 `a.out`） |
| `-c <path>` | 保留生成的 C 文件到指定路径 |
| `--cc <name>` | 使用的 C 编译器（默认依次查找 `clang`、`gcc`、`cc`） |
| `-O0`…`-O3` | 优化等级（默认 `-O2`） |
| `--llvm-ir <path>` | 额外输出 LLVM IR 模块 |
| `--native <file.c>` | 加入 C 文件一起编译，实现 native 方法（可重复） |
| `--native-header <path>` | 生成 native 方法的声明（见 [docs/native.md](docs/native.md)） |
| `--link <arg>` | 传给链接步骤的参数，例如 `--link -lm` |
| `--no-lto` | 关闭 LTO（工具链不支持时自动退回） |
| `-v` | 显示实际执行的编译命令 |

---

## 开发

```sh
go build ./...          # 构建
go test ./...           # 端到端测试（会编译 tests/programs 下每个程序并比对输出）
go vet ./...
sh scripts/bench.sh       # 与 JVM 对照的性能测试（需要 java 才会跑 JVM 那一半）
```

新增测试只需在 `tests/programs/` 放 `xxx.teyru` 与 `xxx.expected`；
若程序需要命令行参数，再放 `xxx.args`（每行一个参数）；程序如果**应该**失败，
用 `xxx.exit` 写它必须结束时的状态码、`xxx.experr` 写它应该输出到 stderr 的内容。
`go test` 会自动处理。

贡献前请读 [AGENTS.md](AGENTS.md)。

---

## 许可

见 [LICENSE](LICENSE) 与 [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md)。
