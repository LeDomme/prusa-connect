import { createHash } from 'node:crypto'
import fs from 'node:fs'
import path from 'node:path'

/** The root of the checkout this tool runs from. */
export const ROOT = path.resolve(import.meta.dirname, '../../../..')

/** The library theme of this checkout. */
export const TOKENS = path.join(ROOT, 'packages/svelte/src/styles/tokens.css')

/**
 * The stylesheets the product Storybook wraps every story in, relative to the
 * repository root. Its preview imports most of them; `app.css` arrives
 * through a package specifier that no import is followed through.
 */
export const GLOBAL_CSS = [
  'frontend/src/styles/global.css',
  'frontend/.storybook/extensions.css',
  'packages/svelte/src/styles/app.css',
  'packages/svelte/src/styles/index.css',
  'packages/svelte/.storybook/preview.css',
]

export type Theme = 'light' | 'dark'
export type Unresolved = { unresolved: true; chain: string[] }

/**
 * The custom properties each theme declares, and the residue: the file with
 * its comments and those declarations taken out, which is everything else
 * the file does to a page.
 */
export type Tokens = { light: Map<string, string>; dark: Map<string, string>; residue: string }

const THEMES = new Set([':root', '.dark', '@theme', '@theme inline'])
const COMMENT = /\/\*[\s\S]*?\*\//g
// A wildcard reset such as `--color-*: initial` does not match, so it stays in the residue.
const DECLARATION = /^\s*(--[\w-]+)\s*:([\s\S]*?);?\s*$/
const VAR = /\bvar\(\s*(--[\w-]+)/g

const collapse = (text: string) => text.replace(/\s+/g, ' ').trim()
const all = (re: RegExp, text: string) => [...text.matchAll(re)].map((m) => m[1]!)

/** The top-level blocks of a stylesheet without comments: the text before `{`, and where the body starts and ends. */
function blocks(css: string): { prelude: string; start: number; end: number }[] {
  const found: { prelude: string; start: number; end: number }[] = []
  let depth = 0
  let from = 0
  let open = 0
  for (let i = 0; i < css.length; i++) {
    if (css[i] === '{' && depth++ === 0) open = i
    else if (css[i] === '}' && --depth === 0) {
      found.push({ prelude: collapse(css.slice(from, open)), start: open + 1, end: i })
      from = i + 1
    } else if (css[i] === ';' && depth === 0) from = i + 1
  }
  return found
}

/** A block body cut into its declarations and nested blocks. */
function statements(body: string): string[] {
  const found: string[] = []
  let depth = 0
  let from = 0
  for (let i = 0; i < body.length; i++) {
    if (body[i] === '{') depth++
    else if ((body[i] === '}' && --depth === 0) || (body[i] === ';' && depth === 0)) {
      found.push(body.slice(from, i + 1))
      from = i + 1
    }
  }
  return [...found, body.slice(from)]
}

let pass: Map<string, string> | undefined

/** A file's size and modification time: it is read again when either changes. */
export const stamp = (file: string) => {
  const hit = pass?.get(file)
  if (hit !== undefined) return hit
  const s = fs.statSync(file)
  const now = `${s.mtimeMs}:${s.size}`
  pass?.set(file, now)
  return now
}

/**
 * Runs `fn` with each file's stamp read once, so the closures of hundreds of
 * stories, which share most files, do not stat them again and again. A file
 * that changes meanwhile is seen by the next pass.
 */
export async function onePass<T>(fn: () => Promise<T>): Promise<T> {
  if (pass) return fn()
  pass = new Map()
  try {
    return await fn()
  } finally {
    pass = undefined
  }
}

const loaded = new Map<string, { stamp: string; tokens: Tokens }>()

/**
 * The tokens of every top-level `:root`, `.dark`, `@theme` and `@theme inline`
 * block. Light is the theme blocks overlaid by `:root`, and dark is light
 * overlaid by `.dark`, so a token only `.dark` declares has no light value.
 * A `:root[…]` block, or one nested in `@media` or `@layer`, is not read.
 */
export function loadTokens(cssPath: string): Tokens {
  const now = stamp(cssPath)
  const hit = loaded.get(cssPath)
  if (hit?.stamp === now) return hit.tokens
  const tokens = parse(fs.readFileSync(cssPath, 'utf8').replace(COMMENT, ' '))
  loaded.set(cssPath, { stamp: now, tokens })
  return tokens
}

function parse(css: string): Tokens {
  const declared = { theme: new Map<string, string>(), root: new Map<string, string>(), dark: new Map<string, string>() }
  let residue = ''
  let at = 0
  for (const block of blocks(css)) {
    if (!THEMES.has(block.prelude)) continue
    const into = block.prelude === '.dark' ? declared.dark : block.prelude === ':root' ? declared.root : declared.theme
    const kept = statements(css.slice(block.start, block.end)).filter((s) => {
      const d = s.includes('{') ? null : s.match(DECLARATION)
      if (d) into.set(d[1]!, collapse(d[2]!))
      return !d
    })
    residue += css.slice(at, block.start) + kept.join('')
    at = block.end
  }
  const light = new Map([...declared.theme, ...declared.root])
  return { light, dark: new Map([...light, ...declared.dark]), residue: collapse(residue + css.slice(at)) }
}

/** The top-level `@theme` blocks of a stylesheet, as written. */
export function themeBlocks(cssPath: string): string {
  const css = fs.readFileSync(cssPath, 'utf8').replace(COMMENT, ' ')
  return blocks(css)
    .filter((b) => b.prelude.startsWith('@theme'))
    .map((b) => `${b.prelude} {${css.slice(b.start, b.end)}}`)
    .join('\n')
}

/** The index of the `)` that closes the `(` at `open`, or -1. */
function closing(text: string, open: number): number {
  let depth = 0
  for (let i = open; i < text.length; i++) {
    if (text[i] === '(') depth++
    else if (text[i] === ')' && --depth === 0) return i
  }
  return -1
}

/** The index of the first comma outside any brackets, or -1. */
function topComma(text: string): number {
  let depth = 0
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '(') depth++
    else if (text[i] === ')') depth--
    else if (text[i] === ',' && depth === 0) return i
  }
  return -1
}

function substitute(value: string, map: Map<string, string>, seen: string[]): string | Unresolved {
  let out = ''
  let at = 0
  for (const m of value.matchAll(/\bvar\(/g)) {
    if (m.index < at) continue
    const close = closing(value, m.index + 3)
    if (close === -1) break
    const inner = value.slice(m.index + 4, close)
    const comma = topComma(inner)
    const name = (comma === -1 ? inner : inner.slice(0, comma)).trim()
    const own = map.get(name)
    if (seen.includes(name) || (own === undefined && comma === -1)) return { unresolved: true, chain: [...seen, name] }
    const next = own !== undefined ? substitute(own, map, [...seen, name]) : substitute(inner.slice(comma + 1), map, seen)
    if (typeof next !== 'string') return next
    out += value.slice(at, m.index) + next
    at = close + 1
  }
  return collapse(out + value.slice(at))
}

/**
 * A token's value in one theme with every `var()` inside it replaced, at any
 * depth. A name the theme lacks takes its fallback; with none, or on a cycle,
 * the token is unresolved and `chain` is the names walked to that point.
 */
export function resolveToken(name: string, theme: Theme, tokens: Pick<Tokens, Theme>): string | Unresolved {
  return substitute(`var(${name})`, tokens[theme], [])
}

export const sha256 = (text: string | Uint8Array) => createHash('sha256').update(text).digest('hex')

/** Tailwind's CSS for each candidate, or null for one that is no utility. */
export type DesignSystem = (candidates: string[]) => (string | null)[]

const systems = new Map<string, { stamp: string; text: string; system: Promise<DesignSystem> }>()

async function load(tokensCss: string, extra: string): Promise<DesignSystem> {
  // `source(none)` stops the file scan, so only the candidates asked for are emitted.
  const entry = `@import 'tailwindcss' source(none);\n@import ${JSON.stringify(tokensCss)};\n${extra}`
  // Loaded on first use, so the commands that never compile a class start without Tailwind installed.
  const { __unstable__loadDesignSystem } = await import('@tailwindcss/node')
  const ds = await __unstable__loadDesignSystem(entry, { base: import.meta.dirname })
  const known = new Map<string, string | null>()
  return (candidates) => {
    const fresh = [...new Set(candidates.filter((c) => !known.has(c)))]
    ds.candidatesToCss(fresh).forEach((css, i) => known.set(fresh[i]!, css ?? null))
    return candidates.map((c) => known.get(c) ?? null)
  }
}

/**
 * The design system of a theme file, with `extra` CSS after it, kept while
 * the file's text stays the same.
 */
export function designSystem(tokensCss = TOKENS, extra = ''): Promise<DesignSystem> {
  const key = `${tokensCss}\0${extra}`
  const now = stamp(tokensCss)
  const hit = systems.get(key)
  if (hit?.stamp === now) return hit.system
  const text = fs.readFileSync(tokensCss, 'utf8')
  const system = hit?.text === text ? hit.system : load(tokensCss, extra)
  systems.set(key, { stamp: now, text, system })
  return system
}

// Every run of characters between whitespace, quotes and semicolons. Most are
// no utility and compile to nothing, which costs a lookup.
const WORD = /[^\s'"`;]+/g

export type FileStyle = { properties: string[]; css: string }

const styles = new Map<string, { stamp: string; system: DesignSystem; style: FileStyle }>()

/**
 * The custom properties a file reads, directly in a `var()` or through a
 * utility it names, and the digest of those utilities' CSS. That CSS carries
 * the `@theme inline` values Tailwind writes into a utility in place of the
 * token's name.
 */
export async function fileStyle(file: string, tokensCss = TOKENS): Promise<FileStyle> {
  const system = await designSystem(tokensCss)
  const now = stamp(file)
  const hit = styles.get(file)
  if (hit?.stamp === now && hit.system === system) return hit.style
  const text = fs.readFileSync(file, 'utf8')
  const words = [...new Set(text.match(WORD))].map((w) => w.replace(/^class:([^=]+)(=.*)?$/, '$1')).sort()
  const css = system(words).filter((c) => c !== null).join('')
  const style = { properties: [...new Set([...all(VAR, text), ...all(VAR, css)])], css: sha256(css) }
  styles.set(file, { stamp: now, system, style })
  return style
}

/** Every custom property the files read: see `fileStyle`. */
export async function usedProperties(files: string[], tokensCss = TOKENS): Promise<Set<string>> {
  const found = new Set<string>()
  for (const file of files) for (const p of (await fileStyle(file, tokensCss)).properties) found.add(p)
  return found
}
