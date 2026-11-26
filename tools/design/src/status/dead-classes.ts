import fs from 'node:fs'
import path from 'node:path'
import { designSystem, GLOBAL_CSS, ROOT, themeBlocks, TOKENS } from './tokens.ts'

// A `{…}` inside the value may hold quotes of its own.
const CLASS_ATTR = /\bclass=(?:"((?:[^"{]|\{[^}]*\})*)"|'((?:[^'{]|\{[^}]*\})*)')/g
const CLASS_EXPR = /\bclass=\{/g
const DIRECTIVE = /\bclass:([^\s=/>]+)/g
const HELPER = /\b(?:cn|tv|clsx)\(/g
const CALL = /([\w$.]+)\s*\(/g
const VARIANT_SETTINGS = /\b(?:defaultVariants|compoundVariants)\s*:\s*[{[]/g
const LITERAL = /'(?:[^'\\\n]|\\.)*'|"(?:[^"\\\n]|\\.)*"|`(?:[^`\\]|\\.)*`/g
// Sticky and of its own: `matchAll` would start a shared regex where `exec` left it.
const LITERAL_AT = new RegExp(LITERAL.source, 'y')
const COMPARED_BEFORE = /(?:[!=]==?|[<>]=?)\s*$/
const COMPARED_AFTER = /^\s*(?:[!=]==?|[<>]=?)/
const STYLE = /<style\b[^>]*>([\s\S]*?)<\/style>/g
// Tailwind's markers for a parent or sibling a variant reads, which emit no CSS of their own.
const MARKER = /^(group|peer)(\/[\w-]+)?$/
const CLOSE: Record<string, string> = { '{': '}', '(': ')', '[': ']' }

const words = (text: string) => text.split(/\s+/).filter(Boolean)

/** The index of the bracket closing the one at `open`, stepping over string literals; -1 when none does. */
function end(text: string, open: number): number {
  const [opener, closer] = [text[open], CLOSE[text[open]!]]
  let depth = 0
  for (let i = open; i < text.length; i++) {
    if (/['"`]/.test(text[i]!)) {
      LITERAL_AT.lastIndex = i
      const literal = LITERAL_AT.exec(text)
      if (literal) i += literal[0].length - 1
    } else if (text[i] === opener) depth++
    else if (text[i] === closer && --depth === 0) return i
  }
  return -1
}

/**
 * `text` with the inside of the bracket each match ends on turned to spaces,
 * unless `keep` says otherwise. What a string literal holds is not matched.
 */
function blank(text: string, re: RegExp, keep: (m: RegExpExecArray) => boolean = () => false): string {
  let out = text
  const code = text.replace(LITERAL, (l) => l[0] + 'x'.repeat(l.length - 2) + l[0])
  for (const m of code.matchAll(re)) {
    if (out[m.index] === ' ' || keep(m)) continue
    const open = m.index + m[0].length - 1
    const close = end(text, open)
    if (close !== -1) out = out.slice(0, open + 1) + ' '.repeat(close - open - 1) + out.slice(close)
  }
  return out
}

/**
 * The class words in the string literals of a JavaScript expression. A
 * literal is left out when it holds a `${…}`, is compared with something, is
 * an argument of a call other than `cn`, `tv` or `clsx`, or sits in a `tv`
 * `defaultVariants` or `compoundVariants` setting, whose class values are lost
 * with it.
 */
function expressionClasses(expr: string): string[] {
  const code = blank(blank(expr, CALL, (m) => /(^|\.)(cn|tv|clsx)$/.test(m[1]!)), VARIANT_SETTINGS)
  return [...code.matchAll(LITERAL)].flatMap((m) => {
    const compared = COMPARED_BEFORE.test(code.slice(0, m.index)) || COMPARED_AFTER.test(code.slice(m.index + m[0].length))
    return compared || m[0].includes('${') ? [] : words(m[0].slice(1, -1))
  })
}

/** The text inside the bracket each match ends on. */
const spans = (text: string, re: RegExp) =>
  [...text.matchAll(re)].map((m) => {
    const open = m.index + m[0].length - 1
    return text.slice(open + 1, Math.max(open + 1, end(text, open)))
  })

// Their own `@theme` blocks add utilities, such as the story frame's widths,
// that tokens.css does not declare.
const GLOBALS = GLOBAL_CSS.map((f) => path.join(ROOT, f))

/**
 * The classes a file names that compile to nothing. They are read from a
 * `class="…"` value (a word holding a `{…}` is left out), a `class:`
 * directive, the strings of a `class={…}` expression, and the strings passed
 * to `cn(`, `tv(` or `clsx(` anywhere in the file; `expressionClasses` says
 * which strings are not classes. A class that a selector in the file's own
 * `<style>`, the theme or a global stylesheet names is not dead.
 */
export async function deadClasses(file: string, tokensCss = TOKENS): Promise<string[]> {
  const text = fs.readFileSync(file, 'utf8')
  const attrs = [...text.matchAll(CLASS_ATTR)].flatMap((m) => words((m[1] ?? m[2]!).replace(/\{[^}]*\}/g, '\0')))
  const candidates = [
    ...new Set([
      ...attrs.filter((w) => !w.includes('\0')),
      ...[...text.matchAll(DIRECTIVE)].map((m) => m[1]!),
      ...[...spans(text, CLASS_EXPR), ...spans(text, HELPER)].flatMap(expressionClasses),
    ]),
  ]
  const css = (await designSystem(tokensCss, GLOBALS.map(themeBlocks).join('\n')))(candidates)
  const own = [...text.matchAll(STYLE)].map((m) => m[1]!)
  const styles = [...own, ...[tokensCss, ...GLOBALS].map((f) => fs.readFileSync(f, 'utf8'))].join('\n')
  return candidates.filter(
    (c, i) => css[i] === null && !MARKER.test(c) && !new RegExp(`\\.${RegExp.escape(c)}(?![\\w-])`).test(styles)
  )
}
