# Teyru

[繁體中文](README.md) · [简体中文](README.zh-CN.md) · **English** · [日本語](README.ja.md)

**Teyru is an independently implemented programming language whose compiler is written entirely in Go and emits native executables directly — no JVM, no javac, no bytecode.**

The syntax will feel familiar to Java developers (classes, interfaces, generics, lambdas,
exceptions, records, enums, annotations), but Teyru drops semicolons, adds native
properties, and runs as **native machine code**: the compiler lowers the whole program to
C and hands it to clang/LLVM (or gcc). The runtime is about 1500 lines of C — a
conservative mark-and-sweep collector, strings, arrays and exceptions — with no virtual
machine of any kind.

```
Teyru source (.teyru)
      │  Go compiler: lexer → parser → semantic analysis → C generator
      ▼
  generated C  ──clang (Clang front end + LLVM middle/back end)──▶  LLVM IR  ──▶  native executable
                                                                        (no JVM, no bytecode)
```

The back end **is LLVM**: `./teyru emit-llvm` prints the IR module so it can go straight
into `opt`, `llc` or a custom pass; `./teyru emit` prints the generated C.

**Docs:** [Language reference](docs/language.md) · [Native interop](docs/native.md) · [Lombok layer](docs/lombok.md) · [Diagnostics](docs/diagnostics.md) · [Compiler architecture](docs/architecture.md) · [Contributor rules](AGENTS.md)

---

## Contents

- [Why it is faster than the JVM](#why-it-is-faster-than-the-jvm)
- [Quick start](#quick-start)
- [Language tour](#language-tour)
- [Language features](#language-features)
- [Standard library](#standard-library)
- [Editors and tooling](#editors-and-tooling)
- [Project layout](#project-layout)
- [Runtime model](#runtime-model)
- [Differences from Java](#differences-from-java)
- [Command line interface](#command-line-interface)
- [Development](#development)
- [License](#license)

---

## Why it is faster than the JVM

Measured on one machine (Linux x86-64, clang 22, OpenJDK 21 Temurin, best of 5 runs):

| Metric | Teyru (native) | Java (HotSpot) | Difference |
|---|---|---|---|
| 100 startups | **0.068 s** (0.68 ms each) | 1.97 s (19.8 ms each) | **~29x faster** |
| Executable size | **35 KB** | ~200 MB JDK runtime | ~5900x smaller |
| Peak RSS (hello) | **2.1 MB** | 50.2 MB | **~24x less** |
| `bench_fib` recursion | **0.0060 s** | 0.0264 s | **4.4x faster** |
| `bench_loop` loops and integer math | **0.0209 s** | 0.0429 s | **2.1x faster** |
| `bench_oop` objects and virtual calls | **0.0044 s** | 0.0253 s | **5.8x faster** |
| `bench_string` string handling | **0.0093 s** | 0.0554 s | **6.0x faster** |
| `bench_alloc` short-lived allocation | **0.0233 s** | 0.0308 s | **1.3x faster** |

**Where the speed comes from:**

1. **No JVM startup.** No class loading, no JIT warmup, no GC threads to spawn.
2. **Compile-time work stays at compile time.** Generics are erased, calls are
   addressed statically, string constants are allocated statically, `static final`
   constants are folded, and the compiler fills in the vtables and interface tables.
3. **No bytecode interpreter.** clang/LLVM optimises the whole program up front
   (LTO inlining across the module, constant propagation, loop vectorisation)
   instead of waiting for a JIT to find hot spots.
4. **Objects that do not need an allocation do not get one.** Escape analysis puts
   an object that stays inside its method on the C stack, and LLVM then promotes its
   fields to registers and deletes the object, the same result a JVM gets from scalar
   replacement. That is what makes `bench_alloc` faster than HotSpot.
5. **Allocation and bounds checks take an inlined fast path.** `ty_alloc` bumps a
   pointer inline in the header, array access only calls the slow path when it must,
   and the collector releases chunks that are completely empty.
6. **Predictable performance.** No deoptimisation, no warmup curve, no GC tuning.

**The honest boundary.** Escape analysis only covers objects that stay inside the
method that creates them. An object stored into a field, an array, a return value or
another object still goes to the heap and the mark-and-sweep collector, and HotSpot's
generational assumption wins on workloads where objects live long and are collected
repeatedly. Every number above includes process startup, so the absolute values are
small. Every number is reproducible with `sh scripts/bench.sh`, which measures the five
programs, the 100 startups, the executable size and the peak RSS; the JDK-runtime figure
in the size row is the runtime installed on the measuring machine, which the script does
not measure.

---

## Quick start

You need **Go 1.26+** and **clang** (or gcc).

```sh
# Build the compiler
go build -o teyru ./cmd/teyru

# Compile and run
./teyru run hello.teyru

# Produce an executable
./teyru build -O2 -o hello hello.teyru
./hello

# Inspect the generated C
./teyru emit hello.teyru

# Inspect the LLVM IR the back end feeds to LLVM (works with opt/llc)
./teyru emit-llvm hello.teyru

# Version
./teyru version
```

`hello.teyru`:

```teyru
class Hello {
  public static void main(String[] args) {
    System.out.println("Hello, Teyru!")
  }
}
```

Note: **Teyru has no semicolons.** Statements end at a newline, and a `for` header uses
two colons to separate its three parts.

---

## Language tour

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
  public double radius {      // native property
    get {
      return field            // field = backing storage
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

    // lambdas and method references
    Fn<Integer> f = (v) -> v + 1
    Fn<Integer> g = Main::twice
    System.out.println(f.apply(41))
    System.out.println(g.apply(21))

    // switch expressions and type patterns
    Color c = Color.GREEN
    String name = switch (c) {
      case RED -> "red"
      case GREEN -> "green"
      default -> "other"
    }
    System.out.println(name)
    System.out.println(describe(c))

    // exceptions
    try {
      System.out.println(10 / 0)
    } catch (ArithmeticException e) {
      System.out.println("division by zero")
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

### Java 25 syntax coverage

Teyru tracks the final (non-preview) Java SE 25 syntax and keeps Java semantics,
dropping only semicolons and adding native properties:

| JEP | Feature | Status |
|---|---|---|
| 512 | Compact source files, instance `main`, implicit `java.io.IO` (`println`/`print`/`readln`) | ✅ |
| 511 | `import module java.base` (parsed and ignored; no module system at run time) | ✅ parsed |
| 513 | Flexible constructor bodies (statements before `super()`) | ✅ |
| 440 | Record patterns (including nested and in `instanceof`) | ✅ |
| 441 | Pattern matching for switch with `when` guards | ✅ |
| 507 | Primitive type patterns (`case int i`, `o instanceof int i`, exact conversions) | ✅ |
| 456 | Unnamed variables and patterns `_` | ✅ |
| 395 | Records (including compact constructors) | ✅ |
| 394 | `instanceof` patterns | ✅ |
| 409 | Sealed classes (`sealed`/`permits`/`non-sealed`) | ✅ parsed |
| 378 | Text blocks | ✅ |
| 361 | Switch expressions | ✅ |
| 286 | `var` local type inference | ✅ |

### Lombok compatibility

The compiler has Lombok built in. Annotations are expanded during semantic analysis
into ordinary Teyru members, which then take the same type-checking and code
generation path as hand-written code — no annotation processor is involved.

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

The full list and the differences are in **[docs/lombok.md](docs/lombok.md)**:
`@Getter`/`@Setter`/`@ToString`/`@EqualsAndHashCode`/`@Data`/`@Value`/`@Builder`/
`@NonNull`/`@Cleanup`/`@SneakyThrows`/`@Synchronized`/`@With`/`@Accessors`/
`@FieldDefaults`/`@UtilityClass`/`@StandardException`/the `@Log` family/
`@ExtensionMethod`/`@FieldNameConstants`/`@Delegate`/`@Helper`/`@Tolerate`/`@Locked`/
`@NonFinal`/`@PackagePrivate` are all supported, including `@Singular`
(accumulate one at a time, add a whole collection, clear, and build() takes a
copy), `@SuperBuilder` over a whole hierarchy, and `@Builder.ObtainVia`.

### Language features

| Area | What is supported |
|---|---|
| Types | Primitives, classes, interfaces, enums, records, annotation types, generics (bounds, wildcards, diamond, generic methods), multi-dimensional arrays |
| Members | Fields, methods, constructors, varargs, static and instance initialiser blocks, nested/inner/local/anonymous classes, `sealed`/`permits` |
| Statements | `if`, `while`, `do-while`, basic `for` (colon header), enhanced `for`, `switch` (statement and expression, arrow and colon form, multi-label, enum, string, type patterns with `when` guards), `try`/`catch`/`finally`, try-with-resources, multi-catch, `throw`, `yield`, `assert`, `synchronized`, labels with `break`/`continue` |
| Expressions | Full operator set and precedence, conditional, casts, `instanceof` (including patterns), lambdas, method references (static, bound, unbound, constructor), anonymous classes, array initialisers, string concatenation, automatic boxing/unboxing |
| Native extensions | Semicolon-free syntax, `val` (inferred, non-reassignable local), `var`, native properties (`get`/`set`/`field`), colon-separated `for` header, newline-separated try-with-resources |

The complete syntax and semantics live in **[docs/language.md](docs/language.md)**.

---

## Standard library

The standard library is written **in Teyru itself** (`lib/*.teyru`) and is
compiled and checked together with every user program:

`Object`, `String`, `StringBuilder`, `Math`, `System`, `PrintStream`,
`Iterable`/`Iterator`, `Comparable`, `AutoCloseable`, `Cloneable`, `Enum`, `Record`,
the eight primitive wrappers (`Byte`, `Short`, `Integer`, `Long`, `Float`, `Double`,
`Character`, `Boolean`), the collections (`List`, `ArrayList`, `Map`, `HashMap`), and the
`Throwable` family (`Exception`, `RuntimeException`, `NullPointerException`,
`ArrayIndexOutOfBoundsException`, `ArithmeticException`, `ClassCastException`,
`IllegalArgumentException`, `IllegalStateException`, `IndexOutOfBoundsException`,
`NoSuchElementException`, `NegativeArraySizeException`, `AssertionError`,
`UnsupportedOperationException`).

`ArrayList` implements `Iterable`, so `for (String s : names)` reads the same as in
Java. There is no `printf` and no file I/O — those remain deliberate scope limits.

To bring your own native library, declare a `native` method and implement it in C.
See [`docs/native.md`](docs/native.md):

```sh
teyru build --native-header native.h program.teyru   # the declarations to implement
teyru build --native impl.c program.teyru            # compile them together
```

---

## Editors and tooling

- **VS Code**: `editors/vscode/` adds TextMate syntax highlighting, language
  configuration and snippets for `.teyru` files. Package it with
  `npx @vscode/vsce package` and install the result with
  `code --install-extension teyru-0.1.0.vsix`.
- **tree-sitter**: `editors/tree-sitter-teyru/` is a complete grammar with
  highlight queries, indentation queries and corpus tests, usable from Neovim,
  Helix, Zed and anything else that loads tree-sitter parsers.
- GitHub still labels `.teyru` files as Java. Linguist has no Teyru definition
  yet; `.gitattributes` maps the extension to the closest grammar until it does.

---

## Project layout

| Path | Purpose |
|---|---|
| `cmd/teyru` | CLI entry point (`build`/`run`/`emit`/`emit-llvm`/`version`) |
| `internal/driver` | Compile pipeline: wires the front end to the C back end, runs the C compiler, handles native sources and output options |
| `internal/source` | Files, position mapping, diagnostics |
| `internal/lexer` | Tokeniser; newlines are not tokens, each token carries a "newline before" flag |
| `internal/parser` | Recursive descent; statement termination uses newline significance plus prefix completeness |
| `internal/ast` | Syntax tree, symbols (class/method/field/variable), types |
| `internal/sema` | Name resolution, type checking, erasure and inference, overload resolution, vtable/selector layout, property lowering |
| `internal/codegen` | C generation: classes to structs, virtual calls to vtables, interface calls to itables, switch lowering, GC root info |
| `internal/util` | Shared helpers: name mangling, type descriptors, C layout |
| `internal/runtime/src` | C runtime: GC, strings, arrays, exceptions, boxing, Math/System/StringBuilder |
| `lib` | Standard library, written in Teyru |
| `tests/programs` | End-to-end programs plus expected output (`go test` compiles and diffs each one) |
| `tests/native` | Native-method interop test: Teyru declarations, a C implementation and the expected output (`TestNative`) |
| `examples` | Examples and the JVM comparison benchmarks (`bench_*.teyru` and `.java`) |
| `scripts` | Development scripts: `bench.sh`, the `pre-commit` hook |
| `docs` | Language reference, diagnostics, architecture |
| `editors` | Editor support: the VS Code extension and the tree-sitter grammar |

---

## Runtime model

- **Objects** are C structs whose first member is `tyobj { tyclass* cls }`. Each class
  has a `tyclass` record with its superclass, interfaces, vtable, interface table and the
  offsets of the reference fields the collector must trace.
- **Virtual calls** go through `obj->cls->vtable[slot]`; **interface calls** through
  `ty_itab(obj, selector)`. Every interface method has a globally unique selector and each
  class's interface table is filled in at compile time.
- **Generics** are erased at compile time; no generic information exists at run time
  (exactly like Java).
- **Exceptions** use a handler chain built on `setjmp`/`longjmp`; `finally` is implemented
  with a nested handler so it runs on every path, including a throw from inside a catch.
- **GC** is conservative mark-and-sweep. Roots are the native stack (scanned
  conservatively), a registry of static field addresses, and registers spilled by
  `setjmp`. Objects never move, so C-level temporaries stay valid across a collection.
- **Strings** are UTF-8 `tystr { tyobj obj; int64 len; char* data }`; literals are static
  objects that never enter the heap.
- **Arrays** are `tyarr { tyobj; len; data; esize; refs }` with the elements stored inline.

---

## Differences from Java

Teyru is not a subset of Java; it is a separate language designed to feel immediately
familiar to Java developers. The main differences:

1. **No semicolons.** A semicolon is rejected by the compiler (`TY-SYN-0001`).
2. **`for` headers use colons**: `for (int i = 0 : i < n : i++)`.
3. **try-with-resources separates resources with newlines**, not semicolons.
4. **Enum constants are separated from members by a single colon** (omitted when there are
   no members).
5. **Native properties**: a field followed by an accessor block becomes a property;
   `field` refers to the backing storage. A field with no accessor block is an ordinary
   Java field.
6. **`val`** declares an inferred, non-reassignable local (not deep immutability).
7. **No checked exception checking**; `throws` is parsed but not enforced.
8. **No `System.out.printf`, no runtime reflection, no annotation processors.**
9. **Not a bytecode platform**: no `.class` files, no `java.lang`, no JNI, and no
   interoperability with existing Java libraries — a deliberate trade-off.

The full list is in [docs/language.md](docs/language.md).

---

## Command line interface

```
teyru build [flags] <files...>                 compile to a native executable
teyru run   [flags] <files...> [-- args...]    compile and run
teyru emit  [flags] <files...>                 print the generated C
teyru emit-llvm [flags] <files...>             print the LLVM IR
teyru version                                  print the version
teyru help                                     print usage
```

| Flag | Meaning |
|---|---|
| `-o <path>` | Output path (default `a.out`) |
| `-c <path>` | Keep the generated C at this path |
| `--cc <name>` | C compiler to use (defaults to `clang`, then `gcc`, then `cc`) |
| `-O0`…`-O3` | Optimisation level (default `-O2`) |
| `--llvm-ir <path>` | Also write the LLVM IR module here |
| `--native <file.c>` | Compile a C file into the program, implementing native methods (repeatable) |
| `--native-header <path>` | Write the declarations of the native methods (see [docs/native.md](docs/native.md)) |
| `--link <arg>` | Extra argument for the link step, such as `--link -lm` |
| `--no-lto` | Disable LTO (the build retries without it when the toolchain lacks support) |
| `-v` | Print the compiler command being run |

---

## Development

```sh
go build ./...          # build
go test ./...           # end-to-end tests (compiles every program under tests/programs)
go vet ./...
sh scripts/bench.sh       # JVM comparison (the JVM half runs only if java is installed)
```

To add a test, drop `xxx.teyru` and `xxx.expected` into `tests/programs/`; if the program
takes command line arguments, add `xxx.args` (one argument per line). `go test` handles
the rest.

Please read [AGENTS.md](AGENTS.md) before contributing.

---

## License

See [LICENSE](LICENSE) and [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).
