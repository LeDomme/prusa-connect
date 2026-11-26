import fs from 'node:fs'
import path from 'node:path'
import { stamp } from './tokens.ts'

export type Alias = { find: string | RegExp; replacement: string }

// Images and fonts a file imports. They are leaves: their bytes are hashed and
// nothing is read from them.
export const ASSET = /\.(svg|png|jpe?g|gif|webp|avif|woff2?)$/
const followed = (file: string) => /\.(svelte|ts|css|json)$/.test(file) || ASSET.test(file)
// Locale strings are a fixed input of every fingerprint, not part of any one story.
const LOCALES = /\/locales\/[^/]+\.json$/
const SCRIPT = /<script\b[^>]*>([\s\S]*?)<\/script>/g
const STYLE = /<style\b[^>]*>([\s\S]*?)<\/style>/g
// Type-only statements are left out: they change no pixel.
const FROM = /\b(import|export)(\s+type)?\s[^'"`;=()]*?\bfrom\s*['"]([^'"]+)['"]/g
const BARE = /\bimport\s*['"]([^'"]+)['"]/g
const DYNAMIC = /\bimport\(\s*['"]([^'"]+)['"]\s*\)/g
const CSS = /@(?:import|reference)\s+(?:url\()?['"]([^'"]+)['"]/g

// Strings are matched first so a `//` inside one, such as a URL, is kept.
const COMMENT = /('(?:\\.|[^'\\\n])*'|"(?:\\.|[^"\\\n])*"|`(?:\\.|[^`\\])*`)|\/\/[^\n]*|\/\*[\s\S]*?\*\//g

const all = (re: RegExp, text: string) => [...text.matchAll(re)].map((m) => m[1]!)

/** The script of a `.ts` or `.svelte` file, with its comments removed. */
export function scriptOf(file: string): string {
  const text = fs.readFileSync(file, 'utf8')
  const code = file.endsWith('.svelte') ? all(SCRIPT, text).join('\n') : text
  return code.replace(COMMENT, (_, string: string | undefined) => string ?? ' ')
}

function scriptSpecifiers(code: string): string[] {
  const from = [...code.matchAll(FROM)].filter((m) => !m[2]).map((m) => m[3]!)
  return [...from, ...all(BARE, code), ...all(DYNAMIC, code)]
}

function specifiers(file: string): string[] {
  if (file.endsWith('.json') || ASSET.test(file)) return []
  if (file.endsWith('.css')) return all(CSS, fs.readFileSync(file, 'utf8'))
  const styles = file.endsWith('.svelte') ? all(STYLE, fs.readFileSync(file, 'utf8')).join('\n') : ''
  return [...scriptSpecifiers(scriptOf(file)), ...all(CSS, styles)]
}

const isFile = (p: string) => fs.statSync(p, { throwIfNoEntry: false })?.isFile() ?? false

// Vite's resolution for the specifiers this repo writes: an exact file, then
// `.ts` (which is how `./x.svelte` finds a runes module `x.svelte.ts`), then
// a directory's `index.ts`.
function resolve(spec: string, from: string, aliases: Alias[]): string | undefined {
  const bare = spec.replace(/[?#].*$/, '')
  let target: string | undefined
  if (bare.startsWith('.')) target = path.resolve(path.dirname(from), bare)
  else if (path.isAbsolute(bare)) target = bare
  else {
    const alias = aliases.find((a) => (typeof a.find === 'string' ? a.find === bare : a.find.test(bare)))
    if (!alias) return undefined
    target = typeof alias.find === 'string' ? alias.replacement : bare.replace(alias.find, alias.replacement)
  }
  const found = [target, `${target}.ts`, path.join(target, 'index.ts')].find(isFile)
  if (!found || !followed(found) || LOCALES.test(found) || found.split(path.sep).includes('node_modules')) return undefined
  return found
}

const edges = new Map<string, { stamp: string; next: string[] }>()

/**
 * The files `file` imports, kept until it changes.
 *
 * ponytail: a file that appears later and would win the resolution, such as
 * `x.ts` beside a directory `x/`, goes unseen until the importer changes; key
 * the entry on the stamps of the directories resolved into if that bites.
 */
function importsOf(file: string, aliases: Alias[]): string[] {
  const now = stamp(file)
  const hit = edges.get(file)
  if (hit?.stamp === now) return hit.next
  const next = specifiers(file).flatMap((spec) => resolve(spec, file, aliases) ?? [])
  edges.set(file, { stamp: now, next })
  return next
}

/**
 * Every local `.svelte`, `.ts`, `.css` and `.json` file, image and font `entry`
 * reaches through its imports. A file in `stop` is listed but not entered; the entry is always
 * entered.
 */
export function importClosure(entry: string, aliases: Alias[], stop: Set<string>): string[] {
  const start = path.resolve(entry)
  const seen = new Set([start])
  const queue = [...seen]
  for (let file = queue.pop(); file !== undefined; file = queue.pop()) {
    if (file !== start && stop.has(file)) continue
    for (const next of importsOf(file, aliases)) {
      if (!seen.has(next)) {
        seen.add(next)
        queue.push(next)
      }
    }
  }
  return [...seen].sort()
}
