# Teyru

[繁體中文](README.md) · [简体中文](README.zh-CN.md) · [English](README.en.md) · **日本語**

**Teyru は独立実装のプログラミング言語です。コンパイラは完全に Go で書かれており、ネイティブ実行ファイルを直接生成します——JVM も javac も bytecode も使いません。**

文法は Java 開発者にとって見慣れたものです（クラス、インターフェース、ジェネリクス、
ラムダ、例外、record、enum、annotation）。一方でセミコロンを廃止し、ネイティブ
プロパティを追加し、**ネイティブ機械語**として動作します。コンパイラはプログラム全体を
C に落とし、clang/LLVM（または gcc）が実行ファイルにします。ランタイムは約 1500 行の C で、
保守的マークアンドスイープ GC、文字列、配列、例外を自前で実装しており、仮想マシンは
一切ありません。

```
Teyru ソース (.teyru)
      │  Go 製コンパイラ：lexer → parser → 意味解析 → C 生成器
      ▼
  生成された C  ──clang（Clang フロントエンド + LLVM 中/後段）──▶  LLVM IR  ──▶  ネイティブ実行ファイル
                                                                      （JVM なし、bytecode なし）
```

バックエンドは **LLVM** です。`./teyru emit-llvm` で IR モジュールを出力できるので、
そのまま `opt` や `llc`、独自パスに渡せます。C を見たいときは `./teyru emit` です。

**ドキュメント：**[言語リファレンス](docs/language.md) · [ネイティブ連携](docs/native.md) · [Lombok 互換レイヤー](docs/lombok.md) · [診断コード一覧](docs/diagnostics.md) · [コンパイラ構成](docs/architecture.md) · [開発ルール](AGENTS.md)

---

## 目次

- [なぜ JVM より速いのか](#なぜ-jvm-より速いのか)
- [クイックスタート](#クイックスタート)
- [言語ツアー](#言語ツアー)
- [対応している言語機能](#対応している言語機能)
- [標準ライブラリ](#標準ライブラリ)
- [エディタとツール](#エディタとツール)
- [プロジェクト構成](#プロジェクト構成)
- [ランタイムモデル](#ランタイムモデル)
- [Java との違い](#java-との違い)
- [コマンドライン](#コマンドライン)
- [開発](#開発)
- [ライセンス](#ライセンス)

---

## なぜ JVM より速いのか

同一マシンでの実測（Linux x86-64、clang 22、OpenJDK 21 Temurin、5 回の最良値）：

| 指標 | Teyru（ネイティブ） | Java（HotSpot） | 差 |
|---|---|---|---|
| 起動 100 回の合計 | **0.068 s**（1 回 0.68 ms） | 1.97 s（1 回 19.8 ms） | **約 29 倍速い** |
| 実行ファイルの大きさ | **35 KB** | JDK ランタイム約 200 MB | 約 5900 倍小さい |
| ピークメモリ（hello） | **2.1 MB** | 50.2 MB | **約 24 倍少ない** |
| `bench_fib` 再帰 | **0.0060 s** | 0.0264 s | **4.4 倍速い** |
| `bench_loop` ループと整数演算 | **0.0209 s** | 0.0429 s | **2.1 倍速い** |
| `bench_oop` オブジェクトと仮想呼び出し | **0.0044 s** | 0.0253 s | **5.8 倍速い** |
| `bench_string` 文字列処理 | **0.0093 s** | 0.0554 s | **6.0 倍速い** |
| `bench_alloc` 短命オブジェクトの確保 | **0.0233 s** | 0.0308 s | **1.3 倍速い** |

**速さの理由：**

1. **JVM の起動コストがない。** クラスロードも JIT のウォームアップも GC スレッドもない。
2. **コンパイル時にできることは実行時に残さない。** ジェネリクスの消去、呼び出しの静的
   解決、文字列定数の静的配置、`static final` 定数の畳み込み、vtable とインターフェース
   テーブルの確定をすべてコンパイラが行う。
3. **バイトコード解釈の段階がない。** clang/LLVM がプログラム全体を最初から最適化する
   （LTO による横断インライン、定数伝播、ループのベクトル化）。
4. **確保が要らないオブジェクトは確保しない。** エスケープ解析が「生成したメソッドから
   出ない」オブジェクトを C のスタックに置き、LLVM がそのフィールドをレジスタへ昇格して
   オブジェクトごと消します。JVM の scalar replacement と同じ結果で、`bench_alloc` が
   HotSpot を上回る理由です。
5. **確保と境界チェックはインラインの高速経路を通る。** `ty_alloc` はヘッダー内で
   ポインタを進めるだけ、配列アクセスは必要なときだけ低速経路を呼び、GC は完全に
   空になった chunk を解放する。
6. **予測可能な性能。** 脱最適化もウォームアップ曲線も GC チューニングもない。

**正直な限界。** エスケープ解析が扱うのは「生成したメソッドから出ない」オブジェクト
だけです。フィールド、配列、戻り値、他のオブジェクトへ渡したものはヒープと
マークアンドスイープに残り、オブジェクトが長生きして繰り返し回収される負荷では
HotSpot の世代別の仮定が勝ります。上の数値はすべてプロセス起動を含むため絶対値は
小さい。再現方法は `sh scripts/bench.sh` で、5 本のプログラム、起動 100 回、実行ファイルの
大きさ、ピークメモリはこのスクリプトが計測します（大きさの行の JDK ランタイムは計測
マシンにインストールされているランタイムで、スクリプトは計測しません）。

---

## クイックスタート

**Go 1.26+** と **clang**（または gcc）が必要です。

```sh
# コンパイラをビルド
go build -o teyru ./cmd/teyru

# コンパイルして実行
./teyru run hello.teyru

# 実行ファイルを生成
./teyru build -O2 -o hello hello.teyru
./hello

# 生成された C を見る
./teyru emit hello.teyru

# LLVM に渡される IR を見る（opt/llc にそのまま渡せる）
./teyru emit-llvm hello.teyru

# バージョン
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

注意：**Teyru にセミコロンはありません。** 文は改行で終わり、`for` のヘッダは
三つの部分をコロン二つで区切ります。

---

## 言語ツアー

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
  public double radius {      // ネイティブプロパティ
    get {
      return field            // field = 実体の格納領域
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

    // for ( 初期化 : 条件 : 更新 )
    for (int i = 0 : i < 3 : i++) {
      System.out.println(i)
    }

    int[] xs = {1, 2, 3}
    for (int x : xs) {
      System.out.print(x)
    }
    System.out.println()

    // ラムダとメソッド参照
    Fn<Integer> f = (v) -> v + 1
    Fn<Integer> g = Main::twice
    System.out.println(f.apply(41))
    System.out.println(g.apply(21))

    // switch 式と型パターン
    Color c = Color.GREEN
    String name = switch (c) {
      case RED -> "red"
      case GREEN -> "green"
      default -> "other"
    }
    System.out.println(name)
    System.out.println(describe(c))

    // 例外
    try {
      System.out.println(10 / 0)
    } catch (ArithmeticException e) {
      System.out.println("ゼロ除算")
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

### Lombok 互換レイヤー

コンパイラに Lombok を内蔵しています。注釈は意味解析の段階で通常の Teyru メンバーに
展開され、手書きのコードと同じ型検査・コード生成の経路を通ります。
annotation processor は不要です。

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

完全な一覧と差異は **[docs/lombok.md](docs/lombok.md)** にあります
（`@Getter`／`@Setter`／`@ToString`／`@EqualsAndHashCode`／`@Data`／`@Value`／
`@Builder`／`@NonNull`／`@Cleanup`／`@SneakyThrows`／`@Synchronized`／`@With`／
`@Accessors`／`@FieldDefaults`／`@UtilityClass`／`@StandardException`／`@Log` 系／
`@ExtensionMethod`／`@FieldNameConstants`／`@Delegate`／`@Helper`／`@Tolerate`／
`@Locked`／`@NonFinal`／`@PackagePrivate` に対応。`@Singular`（1 件ずつ追加、
まとめて追加、クリア、`build()` がコピーを受け取る）、`@SuperBuilder`（継承チェーン
全体のフィールド）、`@Builder.ObtainVia` も含みます）。

### Java 25 構文への対応

Teyru は Java SE 25 の確定した構文（プレビューを除く）を基準にしています：
JEP 512 コンパクトソースファイルとインスタンス `main`（暗黙の `println`／`print`／
`readln` を含む）、JEP 511 モジュールインポート、JEP 513 柔軟なコンストラクタ本体、
JEP 440 レコードパターン、JEP 441 switch のパターンと `when` ガード、
JEP 507 プリミティブ型パターン（`case int i`、`o instanceof int i`、正確な変換）、
JEP 456 未使用変数 `_`、JEP 395 record、JEP 394 `instanceof` パターン、
JEP 409 sealed クラス（`sealed`／`permits`／`non-sealed`）、
JEP 378 テキストブロック、JEP 361 switch 式、JEP 286 `var`。

### 対応している言語機能

| 区分 | 内容 |
|---|---|
| 型 | プリミティブ、クラス、インターフェース、enum、record、annotation 型、ジェネリクス（境界・ワイルドカード・ダイヤモンド・ジェネリックメソッド）、多次元配列 |
| メンバ | フィールド、メソッド、コンストラクタ、可変長引数、静的／インスタンス初期化ブロック、ネスト／内部／ローカル／無名クラス、`sealed`／`permits` |
| 文 | `if`、`while`、`do-while`、基本 `for`（コロンヘッダ）、拡張 `for`、`switch`（文と式、アローとコロン、複数ラベル、enum、文字列、型パターン + `when` ガード）、`try`／`catch`／`finally`、try-with-resources、複数型 catch、`throw`、`yield`、`assert`、`synchronized`、ラベル付き `break`／`continue` |
| 式 | 演算子と優先順位の全体、条件演算子、キャスト、`instanceof`（パターン含む）、ラムダ、メソッド参照（静的・束縛・非束縛・コンストラクタ）、無名クラス、配列初期化子、文字列連結、自動 boxing／unboxing |
| 独自拡張 | セミコロンなしの文法、`val`（型推論される再代入不可のローカル変数）、`var`、ネイティブプロパティ（`get`／`set`／`field`）、`for` のコロンヘッダ、try-with-resources の改行区切り |

文法と意味の全体は **[docs/language.md](docs/language.md)** にあります。

---

## 標準ライブラリ

標準ライブラリは **Teyru 自身**で書かれています（`lib/*.teyru`）。
コンパイルのたびにユーザープログラムと一緒に型検査されます。

`Object`、`String`、`StringBuilder`、`Math`、`System`、`PrintStream`、
`Iterable`／`Iterator`、`Comparable`、`AutoCloseable`、`Cloneable`、`Enum`、`Record`、
八つのプリミティブラッパー（`Byte`／`Short`／`Integer`／`Long`／`Float`／`Double`／
`Character`／`Boolean`）、コレクション（`List`／`ArrayList`／`Map`／`HashMap`）、そして
`Throwable` ファミリ（`Exception`、`RuntimeException`、`NullPointerException`、
`ArrayIndexOutOfBoundsException`、`ArithmeticException`、`ClassCastException`、
`IllegalArgumentException`、`IllegalStateException`、`IndexOutOfBoundsException`、
`NoSuchElementException`、`NegativeArraySizeException`、`AssertionError`、
`UnsupportedOperationException`）。

`ArrayList` は `Iterable` を実装しているので、`for (String s : names)` は Java と
同じ書き方になります。`printf` とファイル I/O は意図的な範囲外のままです。

自前のネイティブライブラリは `native` メソッドを宣言して C で実装します。詳細は
[`docs/native.md`](docs/native.md)：

```sh
teyru build --native-header native.h program.teyru   # 実装すべき宣言を出力
teyru build --native impl.c program.teyru            # 一緒にコンパイル
```

---

## エディタとツール

- **VS Code**: `editors/vscode/` が `.teyru` の TextMate 構文ハイライト、言語設定、
  スニペットを提供します。`npx @vscode/vsce package` でパッケージし、
  `code --install-extension teyru-0.1.0.vsix` でインストールします。
- **tree-sitter**: `editors/tree-sitter-teyru/` はハイライトクエリ、インデントクエリ、
  corpus テストを備えた完全な文法で、Neovim、Helix、Zed などから使えます。
- GitHub は現在も `.teyru` を Java として表示します。linguist に Teyru の定義が
  まだ無いためで、`.gitattributes` が最も近い文法に対応づけています。

---

## プロジェクト構成

| パス | 役割 |
|---|---|
| `cmd/teyru` | CLI エントリ（`build`／`run`／`emit`／`emit-llvm`／`version`） |
| `internal/driver` | コンパイル手順：前後段をつなぎ、C コンパイラを呼び、native ソースと出力オプションを扱う |
| `internal/source` | ファイル、位置変換、診断 |
| `internal/lexer` | 字句解析。改行はトークンにせず「直前に改行があるか」を各トークンに記録 |
| `internal/parser` | 再帰下降。改行の有意性と前置の完結性で文の終わりを決める |
| `internal/ast` | 構文木、シンボル（クラス／メソッド／フィールド／変数）、型 |
| `internal/sema` | 名前解決、型検査、ジェネリクスの消去と推論、オーバーロード解決、vtable／セレクタ配置、プロパティ降下 |
| `internal/codegen` | C 生成：クラス→struct、仮想呼び出し→vtable、インターフェース呼び出し→itable、switch 降下、GC ルート情報 |
| `internal/util` | 前後段で共有する補助：名前修飾、型記述子、C レイアウト |
| `internal/runtime/src` | C ランタイム：GC、文字列、配列、例外、boxing、Math／System／StringBuilder |
| `lib` | Teyru で書かれた標準ライブラリ |
| `tests/programs` | エンドツーエンドのテストプログラムと期待出力（`go test` が逐一比較） |
| `tests/native` | native メソッドの連携テスト：Teyru の宣言、C の実装、期待出力（`TestNative`） |
| `examples` | サンプルと JVM 比較用 benchmark（`bench_*.teyru` と `.java`） |
| `scripts` | 開発スクリプト：`bench.sh`、`pre-commit` フック |
| `docs` | 言語リファレンス、診断コード、アーキテクチャ |
| `editors` | エディタ支援：VS Code 拡張と tree-sitter 文法 |

---

## ランタイムモデル

- **オブジェクト**は C の struct で、先頭が `tyobj { tyclass* cls }`。各クラスは
  `tyclass` を持ち、親クラス、インターフェース、vtable、インターフェース表、
  GC が辿る参照フィールドのオフセットを記録します。
- **仮想呼び出し**は `obj->cls->vtable[slot]`、**インターフェース呼び出し**は
  `ty_itab(obj, selector)`。各インターフェースメソッドはグローバルに一意なセレクタを
  持ち、各クラスのインターフェース表はコンパイル時に埋められます。
- **ジェネリクス**はコンパイル時に消去され、実行時には型引数の情報がありません
  （Java と同じ）。
- **例外**は `setjmp`／`longjmp` によるハンドラチェーン。`finally` は入れ子の
  ハンドラで実装され、catch の中で再送出した場合も含め必ず実行されます。
- **GC** は保守的マークアンドスイープ。ルートはネイティブスタック（保守的に走査）、
  静的フィールドのアドレス登録表、`setjmp` で退避したレジスタです。オブジェクトは
  移動しないため、C 側の一時ポインタは常に有効です。
- **文字列**は UTF-8 の `tystr { tyobj obj; int64 len; char* data }`。リテラルは
  静的オブジェクトで、ヒープに入りません。
- **配列**は `tyarr { tyobj; len; data; esize; refs }` で、要素はオブジェクトの
  直後に埋め込まれます。

---

## Java との違い

Teyru は Java のサブセットではなく、Java 開発者にとってすぐ理解できる独立した言語です。
主な違い：

1. **セミコロンがない。** セミコロンはコンパイラに拒否されます（`TY-SYN-0001`）。
2. **`for` ヘッダはコロン**：`for (int i = 0 : i < n : i++)`。
3. **try-with-resources は改行区切り**で、セミコロンは使いません。
4. **enum の定数領域とメンバ領域はコロン一つ**で区切ります（メンバがなければ省略）。
5. **ネイティブプロパティ**：フィールドの後に accessor ブロックを書くとプロパティに
   なります。`field` は実体の格納領域を指します。accessor ブロックのないフィールドは
   普通の Java フィールドです。
6. **`val`** は型推論される再代入不可のローカル変数です（深い不変性ではありません）。
7. **checked exception の検査はありません**。`throws` は解析されますが強制されません。
8. **`System.out.printf`、実行時リフレクション、annotation processor はありません。**
9. **bytecode プラットフォームではありません**：`.class` も `java.lang` も JNI もなく、
   既存の Java ライブラリとの相互運用もできません。これは意図的な割り切りです。

全体の一覧は [docs/language.md](docs/language.md) にあります。

---

## コマンドライン

```
teyru build [flags] <files...>                 ネイティブ実行ファイルにコンパイル
teyru run   [flags] <files...> [-- args...]    コンパイルして実行
teyru emit  [flags] <files...>                 生成された C を出力
teyru emit-llvm [flags] <files...>             LLVM IR を出力
teyru version                                  バージョン
teyru help                                     使い方
```

| フラグ | 意味 |
|---|---|
| `-o <path>` | 出力パス（既定 `a.out`） |
| `-c <path>` | 生成された C を指定パスに残す |
| `--cc <name>` | 使用する C コンパイラ（既定は `clang`、`gcc`、`cc` の順に探索） |
| `-O0`…`-O3` | 最適化レベル（既定 `-O2`） |
| `--llvm-ir <path>` | LLVM IR モジュールも出力 |
| `--native <file.c>` | C ファイルを一緒にコンパイルして native メソッドを実装（複数可） |
| `--native-header <path>` | native メソッドの宣言を出力（[docs/native.md](docs/native.md)） |
| `--link <arg>` | リンク手順へ渡す引数（例：`--link -lm`） |
| `--no-lto` | LTO を無効化（未対応のツールチェーンでは自動的にフォールバック） |
| `-v` | 実行されるコンパイルコマンドを表示 |

---

## 開発

```sh
go build ./...          # ビルド
go test ./...           # エンドツーエンド（tests/programs の各プログラムをコンパイルして比較）
go vet ./...
sh scripts/bench.sh       # JVM との比較（java がある場合のみ JVM 側も実行）
```

テストを追加するには `tests/programs/` に `xxx.teyru` と `xxx.expected` を置きます。
コマンドライン引数が必要なら `xxx.args`（1 行に 1 引数）を、プログラムが**失敗する
べき**なら `xxx.exit`（終了ステータス）と `xxx.experr`（stderr に出す内容）も追加してください。
`go test` が残りを処理します。

コントリビュートの前に [AGENTS.md](AGENTS.md) を読んでください。

---

## ライセンス

[LICENSE](LICENSE) と [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md) を参照してください。
