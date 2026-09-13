# Teyru for VS Code

Syntax highlighting, brackets and snippets for the [Teyru](../..) language —
the Java-like language whose compiler lives in this repository.

Teyru source files use the `.teyru` extension.

## What this extension does

- **Syntax highlighting** for the whole language as *this* compiler accepts it,
  not as Java accepts it:
  - no semicolons anywhere (a `;` is shown as an error, because the lexer
    rejects it with `TY-SYN-0001`)
  - the colon forms: `for (init : cond : update)`, `for ( : : )`, `for (x : xs)`,
    and the lone `:` that separates enum constants from enum members
  - native properties, including the `get` / `set` accessors and the `field` /
    `value` names that are only meaningful inside them
  - `val` / `var`, `native` members, `package teyru`
  - the Java 25 surface: `record`, `sealed` / `permits` / `non-sealed`, `yield`,
    `when` guards, `_` unnamed variables, text blocks, pattern matching
  - comments, text blocks, string and character escapes, and every numeric
    literal form the lexer has (`0x`, `0b`, leading-`0` octal, `_` separators,
    `L` / `f` / `F` / `d` / `D` suffixes, exponents)
- **Bracket matching, auto-closing, comments and folding** —
  `language-configuration.json`.
- **Snippets** for classes, interfaces, records, sealed interfaces, enums (both
  forms), `main`, all four `for` shapes, switch expressions with `yield`,
  pattern switches with `when`, `try` / `catch` / `finally`, `try` with
  resources, properties, native methods, `package` and `import`.

## What this extension does not do

- **No language server.** There is no completion, go-to-definition, rename,
  hover, formatting or refactoring.
- **No in-editor diagnostics.** The Teyru compiler is a command-line program;
  this extension does not run it. Errors such as `TY-SYN-0001` are shown by
  the compiler, not by the editor.
- **No semantic highlighting.** Everything above is the TextMate grammar, so it
  is lexical only: the editor cannot tell a type name from a variable name, and
  a word that is only contextually a keyword (`record`, `when`, `field`, …) is
  highlighted by shape.

## Build the `.vsix`

From this directory:

```sh
npx --yes @vscode/vsce package
```

That writes `teyru-0.1.0.vsix` here. To keep the repository clean, write it
somewhere else instead:

```sh
npx --yes @vscode/vsce package --out /tmp/teyru-0.1.0.vsix
```

## Install

```sh
code --install-extension teyru-0.1.0.vsix
```

Then open any `.teyru` file. If a file is already open, run
**Developer: Reload Window**.

## Check the grammar

`tools/check-grammar.mjs` reads the compiler in this repository and verifies
that the grammar has not drifted from it. It needs a checkout of the repository
and nothing else — plain Node, no dependencies:

```sh
node tools/check-grammar.mjs
```

It checks three things and exits non-zero if the first two fail:

1. **Keywords.** Every keyword in the `var keywords` table in
   `internal/lexer/lexer.go` appears in a `keyword-lexer-*` pattern, and no
   `keyword-lexer-*` pattern contains a word the lexer does not have. Words in
   `keyword-contextual-*` patterns are listed separately, and any of them that
   is *also* a lexer keyword is an error — contextual words like `record` and
   `when` are plain identifiers to the lexer.
2. **Patterns.** Every `match`, `begin` and `end` compiles as a `RegExp`, every
   `include` resolves to a repository entry, and every pattern that matches
   something names a scope. The regexes are also checked for Oniguruma-only
   syntax that JavaScript cannot compile, so they stay portable.
3. **Coverage.** The comment, string, character and number rules are run over
   every `tests/programs/*.teyru` and `lib/*.teyru`, and the number of lines
   touched is printed per file. The percentage is reported, never a failure: it
   counts the lines carrying a literal or a comment, and a Teyru line made of
   identifiers and operators is not supposed to be counted. What *is* a failure
   is a comment, string or character rule still open at end of file — that
   would swallow the rest of the file in an editor too.

## Status and licence

Not published on the Marketplace. The grammar is written by hand for this
extension; it is not derived from VS Code's bundled Java grammar or from any
other third-party grammar.

Licensed under GPL-2.0-only, the same terms as the compiler.
