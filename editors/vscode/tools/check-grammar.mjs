#!/usr/bin/env node
// check-grammar.mjs — sanity checks for the hand-written Teyru TextMate grammar.
//
// Run from editors/vscode/:
//
//     node tools/check-grammar.mjs
//
// It reads the compiler in this repository, so it only works from a checkout.
// Dependency-free: plain Node, nothing installed.
//
// What it checks:
//
//   1. KEYWORDS. The keyword table in internal/lexer/lexer.go is the authority.
//      Every keyword in it must appear in a `keyword-lexer-*` pattern in the
//      grammar. Any word in a `keyword-lexer-*` pattern that the lexer does
//      not have is reported (that is how you catch a stale grammar). Words in
//      `keyword-contextual-*` patterns are reported too, and any of them that
//      is also a lexer keyword is a failure — contextual words are `Ident`
//      tokens to the lexer, not keywords.
//
//   2. PATTERNS. Every `match` / `begin` / `end` string in the grammar must
//      compile as a RegExp (the same strings are handed to Oniguruma by
//      VS Code). Every `include` must name a repository entry, and every
//      pattern that carries a scope must actually name one.
//
//   3. COVERAGE. The comment, string, number and character rules are run over
//      every tests/programs/*.teyru and lib/*.teyru with a small single-pass
//      scanner that implements `match`, `begin`/`end` and a rule stack, and
//      the number of lines touched by at least one rule is printed per file.
//
// Exit status is 0 when checks 1 and 2 pass, 1 otherwise. Coverage is
// reported, never a failure: it measures the grammar, not the compiler.

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const extRoot = path.resolve(here, '..')
const repoRoot = path.resolve(extRoot, '..', '..')

const GRAMMAR_PATH = path.join(extRoot, 'syntaxes', 'teyru.tmLanguage.json')
const LEXER_PATH = path.join(repoRoot, 'internal', 'lexer', 'lexer.go')
const PROGRAM_DIRS = [
  path.join(repoRoot, 'tests', 'programs'),
  path.join(repoRoot, 'lib'),
]

const failures = []
const notes = []

function fail(msg) {
  failures.push(msg)
}

function readJSON(file) {
  const text = fs.readFileSync(file, 'utf8')
  return JSON.parse(text)
}

// ---------------------------------------------------------------------------
// 1. keywords
// ---------------------------------------------------------------------------

// `var keywords = map[string]bool{ "abstract": true, ... }`
function lexerKeywords() {
  const src = fs.readFileSync(LEXER_PATH, 'utf8')
  const block = /var keywords = map\[string\]bool\{([\s\S]*?)\n\}/.exec(src)
  if (!block) {
    fail(`could not find the keyword table in ${LEXER_PATH}`)
    return []
  }
  const words = [...block[1].matchAll(/"([A-Za-z_][A-Za-z0-9_]*)":/g)].map(m => m[1])
  if (words.length === 0) fail(`the keyword table in ${LEXER_PATH} parsed to nothing`)
  return words
}

// The word list of a rule: the leading `(?:a|b|c)` alternation, or the first
// identifier in the pattern when there is no alternation. `\b` is stripped
// first so `\bword\b` and `\b(?:a|b)\b` both work.
function wordsOf(pattern) {
  const stripped = pattern.replace(/\\b/g, '')
  let body
  const alt = /^\(\?:([^)]*)\)/.exec(stripped)
  if (alt) {
    body = alt[1]
  } else {
    const one = /^([A-Za-z_][A-Za-z0-9_-]*)/.exec(stripped)
    body = one ? one[1] : ''
  }
  return body
    .split('|')
    .map(s => s.trim())
    .filter(s => /^[A-Za-z_][A-Za-z0-9_-]*$/.test(s))
}

function keywordCheck(grammar, lexer) {
  const lexerSet = new Set(lexer)
  const grammarWords = new Map() // word -> rule key
  const contextual = new Map()

  for (const [key, rule] of Object.entries(grammar.repository)) {
    if (!/^keyword-(lexer|contextual)-/.test(key)) continue
    const pattern = rule.match
    if (typeof pattern !== 'string') {
      fail(`repository key "${key}" is a keyword rule but has no "match"`)
      continue
    }
    const target = key.startsWith('keyword-lexer-') ? grammarWords : contextual
    for (const word of wordsOf(pattern)) {
      if (target.has(word)) fail(`word "${word}" is declared twice (${target.get(word)}, ${key})`)
      target.set(word, key)
    }
  }

  const missing = [...lexerSet].filter(w => !grammarWords.has(w)).sort()
  const extra = [...grammarWords.keys()].filter(w => !lexerSet.has(w)).sort()
  const overlapping = [...contextual.keys()].filter(w => lexerSet.has(w)).sort()

  if (missing.length) fail(`keywords the lexer has but the grammar does not: ${missing.join(' ')}`)
  if (overlapping.length) {
    fail(`contextual rules use words that ARE lexer keywords: ${overlapping.join(' ')}`)
  }

  const width = Math.max(...[...lexerSet, ...grammarWords.keys()].map(w => w.length))
  return { lexerSet, grammarWords, contextual, missing, extra, overlapping, width }
}

// ---------------------------------------------------------------------------
// 2. patterns
// ---------------------------------------------------------------------------

function walkPatterns(grammar) {
  let patterns = 0
  let regexes = 0
  let includes = 0
  const seen = new Set()

  const checkScope = (where, obj, kind) => {
    const captures = obj[kind]
    if (captures === undefined) return
    if (typeof captures !== 'object' || captures === null) {
      fail(`${where}: "${kind}" must be an object`)
      return
    }
    for (const [id, cap] of Object.entries(captures)) {
      if (!cap || typeof cap.name !== 'string' || !cap.name) {
        fail(`${where}: ${kind}["${id}"] has no "name"`)
      }
    }
  }

  const visit = (node, where) => {
    if (Array.isArray(node)) {
      node.forEach((child, i) => visit(child, `${where}[${i}]`))
      return
    }
    if (!node || typeof node !== 'object') return

    if (typeof node.include === 'string') {
      includes++
      const key = node.include.startsWith('#') ? node.include.slice(1) : null
      if (key === null) {
        fail(`${where}: external include "${node.include}" (only #repository includes are used here)`)
      } else if (!Object.hasOwn(grammar.repository, key)) {
        fail(`${where}: include "${node.include}" does not resolve to a repository entry`)
      }
    }

    const isPattern = typeof node.match === 'string' || typeof node.begin === 'string'
    if (isPattern) {
      patterns++
      for (const kind of ['match', 'begin', 'end']) {
        const src = node[kind]
        if (src === undefined) continue
        if (typeof src !== 'string') {
          fail(`${where}: "${kind}" must be a string`)
          continue
        }
        regexes++
        try {
          new RegExp(src)
        } catch (e) {
          fail(`${where}: ${kind} is not a valid RegExp: ${src} (${e.message})`)
        }
        // `\b`, `(?:` and lookaround are all both JS and Oniguruma; `(?x)`
        // and possessive quantifiers are Oniguruma-only and must not appear,
        // because this checker has to be able to compile every pattern.
        if (/\(\?[a-zA-Z]/.test(src) && !/\(\?(?:[:=!]|<[=!])/.test(src)) {
          fail(`${where}: ${kind} uses a non-JavaScript regex feature: ${src}`)
        }
      }
      if (typeof node.begin === 'string' && typeof node.end !== 'string') {
        fail(`${where}: has "begin" but no "end"`)
      }
      const named =
        typeof node.name === 'string' ||
        typeof node.contentName === 'string' ||
        node.captures !== undefined ||
        node.beginCaptures !== undefined ||
        node.endCaptures !== undefined
      if (!named) fail(`${where}: pattern carries no scope (no "name", "captures" or "*Captures")`)
    }

    for (const kind of ['captures', 'beginCaptures', 'endCaptures']) checkScope(where, node, kind)

    if (Array.isArray(node.patterns)) {
      if (seen.has(node)) {
        fail(`${where}: pattern list is self-referencing in a way the grammar does not intend`)
      } else {
        seen.add(node)
        visit(node.patterns, `${where}.patterns`)
        seen.delete(node)
      }
    }
  }

  visit(grammar.patterns, 'patterns')
  for (const [key, rule] of Object.entries(grammar.repository)) {
    visit(rule, `repository.#${key}`)
  }
  return { patterns, regexes, includes }
}

// ---------------------------------------------------------------------------
// 3. coverage
// ---------------------------------------------------------------------------

const COVERAGE_KEYS = [
  'comment-line',
  'comment-block',
  'comment-block-documentation',
  'text-block',
  'string-double',
  'string-single',
  'number-hex',
  'number-binary',
  'number-octal',
  'number-float',
  'number-exponent',
  'number-integer',
]

function compile(src) {
  // `y` so a match must start exactly where we are; `m` so ^ and $ keep their
  // per-line meaning, the way they do for VS Code.
  return new RegExp(src, 'my')
}

// Flatten a rule's inner patterns into match rules. `{include: "#x"}` is
// resolved so `#string-escape` really consumes `\"` and `\'`, instead of the
// scanner closing a string on an escaped quote and then opening a character
// literal that never closes. Nested begin/end rules are skipped: they cannot
// contain a comment or a literal's terminator that the outer rule would miss.
function collectMatches(grammar, patterns, seen, out) {
  for (const p of patterns || []) {
    if (typeof p.match === 'string') {
      out.push({ key: p.name || '?', match: compile(p.match) })
    } else if (typeof p.include === 'string' && p.include.startsWith('#')) {
      const key = p.include.slice(1)
      if (seen.has(key)) continue
      seen.add(key)
      const target = grammar.repository[key]
      if (target) collectMatches(grammar, target.patterns, seen, out)
    }
  }
}

function toRule(grammar, key) {
  const raw = grammar.repository[key]
  if (!raw) throw new Error(`no repository entry for #${key}`)
  const rule = { key }
  if (typeof raw.match === 'string') {
    rule.match = compile(raw.match)
  } else if (typeof raw.begin === 'string') {
    rule.match = compile(raw.begin)
    rule.end = compile(raw.end)
    rule.inner = []
    collectMatches(grammar, raw.patterns, new Set(), rule.inner)
    for (const inner of rule.inner) inner.key = `${key} ! ${inner.key}`
  }
  return rule
}

// A deliberately small TextMate scanner: `match` consumes, `begin` pushes the
// rule so its `end` (checked before anything else) closes it, and the rule's
// own patterns run in between. Enough to run the string and comment rules the
// way an editor does.
function scan(text, rootRules) {
  const lineStarts = [0]
  for (let i = 0; i < text.length; i++) {
    if (text.charCodeAt(i) === 10) lineStarts.push(i + 1)
  }
  const covered = new Uint8Array(lineStarts.length)

  const lineAtOrBefore = off => {
    let lo = 0
    let hi = lineStarts.length - 1
    let best = 0
    while (lo <= hi) {
      const mid = (lo + hi) >> 1
      if (lineStarts[mid] <= off) {
        best = mid
        lo = mid + 1
      } else {
        hi = mid - 1
      }
    }
    return best
  }

  const mark = (from, len) => {
    const end = from + Math.max(len, 1)
    const first = lineAtOrBefore(from)
    const last = lineAtOrBefore(end - 1)
    for (let i = first; i <= last; i++) covered[i] = 1
  }

  const stack = []
  let pos = 0
  let steps = 0
  const maxSteps = text.length * 8 + 10000

  while (pos < text.length && steps++ < maxSteps) {
    if (stack.length) {
      const top = stack[stack.length - 1]
      top.end.lastIndex = pos
      const m = top.end.exec(text)
      if (m) {
        mark(pos, m[0].length)
        pos += m[0].length || 1
        stack.pop()
        continue
      }
    }
    const set = stack.length ? stack[stack.length - 1].inner || [] : rootRules
    let found = null
    for (const rule of set) {
      rule.match.lastIndex = pos
      const m = rule.match.exec(text)
      if (m) {
        found = { rule, m }
        break
      }
    }
    if (!found) {
      pos++
      continue
    }
    mark(pos, found.m[0].length)
    pos += found.m[0].length || 1
    if (found.rule.end) stack.push(found.rule)
  }

  let coveredLines = 0
  for (let i = 0; i < covered.length; i++) {
    if (covered[i]) coveredLines++
  }
  // Anything still on the stack at EOF was opened and never closed. For a
  // comment, a string, a character or a text block that is a real bug: the
  // rule would swallow the rest of the file in an editor too.
  return { coveredLines, totalLines: lineStarts.length, openAtEOF: stack.map(r => r.key) }
}

function programFiles() {
  const files = []
  for (const dir of PROGRAM_DIRS) {
    if (!fs.existsSync(dir)) continue
    for (const name of fs.readdirSync(dir).sort()) {
      if (name.endsWith('.teyru')) files.push(path.join(dir, name))
    }
  }
  return files
}

// ---------------------------------------------------------------------------

function main() {
  const grammar = readJSON(GRAMMAR_PATH)

  if (grammar.scopeName !== 'source.teyru') {
    fail(`scopeName is "${grammar.scopeName}", expected "source.teyru"`)
  }

  const lexer = lexerKeywords()
  const kw = keywordCheck(grammar, lexer)
  const pat = walkPatterns(grammar)

  console.log('== 1. keywords ==')
  console.log(`lexer table            : ${kw.lexerSet.size} keywords (${path.relative(repoRoot, LEXER_PATH)})`)
  console.log(`grammar keyword-lexer-*: ${kw.grammarWords.size} words in ${new Set(kw.grammarWords.values()).size} patterns`)
  console.log(`grammar contextual     : ${kw.contextual.size} words in ${new Set(kw.contextual.values()).size} patterns`)
  console.log(`missing from grammar   : ${kw.missing.length ? kw.missing.join(' ') : '(none)'}`)
  console.log(`extra in grammar       : ${kw.extra.length ? kw.extra.join(' ') : '(none)'}`)
  console.log(`contextual that are lexer keywords: ${kw.overlapping.length ? kw.overlapping.join(' ') : '(none)'}`)

  console.log('')
  console.log('== 2. patterns ==')
  console.log(`patterns with match/begin : ${pat.patterns}`)
  console.log(`regexes compiled          : ${pat.regexes}`)
  console.log(`includes resolved         : ${pat.includes}`)

  console.log('')
  console.log('== 3. coverage over tests/programs/*.teyru and lib/*.teyru ==')

  console.log('the counted rules are the comment, string, character and number rules:')
  console.log(`  ${COVERAGE_KEYS.join(', ')}`)
  console.log('the remaining lines of a Teyru program are made of identifiers, keywords and')
  console.log('operators, which those rules deliberately do not match — a low percentage here')
  console.log('means "few literals and comments in the file", not "unhighlighted code".')
  console.log('')

  const rules = COVERAGE_KEYS.map(key => toRule(grammar, key))
  const files = programFiles()
  if (files.length === 0) fail('no .teyru programs found to check coverage against')

  const width = Math.max(...files.map(f => path.relative(repoRoot, f).length))
  let sumCovered = 0
  let sumLines = 0
  const worst = []

  for (const file of files) {
    const text = fs.readFileSync(file, 'utf8')
    const { coveredLines, totalLines, openAtEOF } = scan(text, rules)
    sumCovered += coveredLines
    sumLines += totalLines
    const rel = path.relative(repoRoot, file)
    if (openAtEOF.length) {
      fail(`${rel}: these rules were still open at end of file: ${openAtEOF.join(', ')}`)
    }
    const label = rel.padEnd(width)
    const pct = totalLines ? ((100 * coveredLines) / totalLines).toFixed(1) : '0.0'
    console.log(`${label}  ${String(coveredLines).padStart(5)}/${String(totalLines).padEnd(5)} lines covered (${pct}%)`)
    worst.push({ file: label.trim(), pct: Number(pct), uncovered: totalLines - coveredLines })
  }

  console.log('')
  const totalPct = sumLines ? ((100 * sumCovered) / sumLines).toFixed(1) : '0.0'
  console.log(
    `${'TOTAL'.padEnd(width)}  ${String(sumCovered).padStart(5)}/${String(sumLines).padEnd(5)} lines covered (${totalPct}%)`
  )
  console.log(`files checked: ${files.length}`)
  worst.sort((a, b) => b.uncovered - a.uncovered)
  console.log('least covered files:')
  for (const w of worst.slice(0, 3)) console.log(`  ${w.file} (${w.pct}%)`)

  console.log('')
  if (failures.length) {
    console.log(`FAIL — ${failures.length} problem(s):`)
    for (const f of failures) console.log(`  - ${f}`)
    process.exit(1)
  }
  for (const n of notes) console.log(`note: ${n}`)
  console.log('PASS — keywords match the lexer, every pattern compiles and is scoped.')
}

main()
