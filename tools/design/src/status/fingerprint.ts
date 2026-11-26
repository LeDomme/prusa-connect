import { spawnSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import { sourceAliases } from '../../../../frontend/vite/import-map.ts'
import { ASSET, importClosure } from './closure.ts'
import { storyClosure } from './screens.ts'
import { fileStyle, GLOBAL_CSS, loadTokens, resolveToken, sha256, stamp } from './tokens.ts'

/** The library theme, relative to the repository root. */
export const TOKENS = 'packages/svelte/src/styles/tokens.css'
// The product Storybook's preview, whose decorators wrap every story, the library's included.
const PREVIEW = 'frontend/.storybook/preview.ts'
const CATALOGUES = [
  'packages/svelte/locales/en*.json',
  'frontend/src/locales/en.json',
  '{extensions,examples}/*/ui/src/locales/en.json',
]
// The head of a string or template literal: a whole key, or the part of a
// key written before a `${…}`. Keys are often built or looked up from a table
// rather than passed to `t()` as they are.
const HEAD = /['"`]([A-Za-z_][\w.-]*)/g

type Facts = { digest: string; heads: string[] }
const facts = new Map<string, { stamp: string; facts: Facts }>()

/** A file's content digest and literal heads, kept until the file changes. */
function factsOf(file: string): Facts {
  const now = stamp(file)
  const hit = facts.get(file)
  if (hit?.stamp === now) return hit.facts
  // The bytes, so an image or font hashes as it is; a text file hashes as its UTF-8 text would.
  const bytes = fs.readFileSync(file)
  const found = { digest: sha256(bytes), heads: /\.(ts|svelte)$/.test(file) ? [...bytes.toString('utf8').matchAll(HEAD)].map((m) => m[1]!) : [] }
  facts.set(file, { stamp: now, facts: found })
  return found
}

const previews = new Map<string, { files: string[]; stamps: string[] }>()

const unchanged = (file: string, was: string) => {
  try {
    return stamp(file) === was
  } catch {
    return false
  }
}

/** The import closure of a preview file, kept until one of its files changes. */
function previewClosure(file: string, repoRoot: string): string[] {
  const hit = previews.get(file)
  if (hit?.files.every((f, i) => unchanged(f, hit.stamps[i]!))) return hit.files
  const files = importClosure(file, sourceAliases(path.join(repoRoot, 'packages')), new Set())
  previews.set(file, { files, stamps: files.map(stamp) })
  return files
}

/**
 * The files outside a story's own closure that its fingerprint reads,
 * relative to `repoRoot`: the preview's closure, the global stylesheets, the
 * theme file and the English catalogues. A change to one of them can move
 * every story's fingerprint.
 */
export function sharedInputs(repoRoot: string): string[] {
  const own = [...previewClosure(path.join(repoRoot, PREVIEW), repoRoot), ...GLOBAL_CSS.map((f) => path.join(repoRoot, f)), path.join(repoRoot, TOKENS)]
  return [
    ...own.map((f) => path.relative(repoRoot, f).split(path.sep).join('/')),
    ...fs.globSync(CATALOGUES, { cwd: repoRoot }).map((f) => f.split(path.sep).join('/')),
  ]
}

const catalogues = new Map<string, { stamp: string; entries: [string, string][] }>()

/** Every English string as (catalogue, key, value), each catalogue read again when it changes. */
function englishStrings(repoRoot: string): [string, string, string][] {
  return fs.globSync(CATALOGUES, { cwd: repoRoot }).sort().flatMap((rel) => {
    const file = path.join(repoRoot, rel)
    const now = stamp(file)
    let hit = catalogues.get(file)
    if (hit?.stamp !== now) {
      hit = { stamp: now, entries: Object.entries(JSON.parse(fs.readFileSync(file, 'utf8')) as Record<string, string>) }
      catalogues.set(file, hit)
    }
    return hit.entries.map(([key, value]): [string, string, string] => [rel.split(path.sep).join('/'), key, value])
  })
}

/** A key some head names: the key itself, or a head that ends at one of its dots or just after one. */
function named(key: string, heads: Set<string>): boolean {
  for (let i = key.indexOf('.'); i !== -1; i = key.indexOf('.', i + 1)) {
    if (heads.has(key.slice(0, i)) || heads.has(key.slice(0, i + 1))) return true
  }
  return heads.has(key)
}

/** The English strings under the keys the literal heads of `files` name. */
export function matchedStrings(files: string[], repoRoot: string): [string, string, string][] {
  const heads = new Set(files.flatMap((f) => factsOf(f).heads))
  return englishStrings(repoRoot).filter(([, key]) => named(key, heads))
}

const ignored = new Map<string, boolean>()

/**
 * Whether git ignores each file. A generated file, such as the library's
 * `src/i18n/catalogues.ts`, exists only in a built checkout, while approvals
 * are fingerprinted in a fresh one; its sources are inputs of their own.
 */
function gitIgnored(files: string[], repoRoot: string): boolean[] {
  const fresh = files.filter((f) => !ignored.has(f))
  if (fresh.length > 0) {
    const out = spawnSync('git', ['check-ignore', '--stdin', '-z'], { cwd: repoRoot, input: fresh.join('\0'), encoding: 'utf8' })
    if (out.status !== 0 && out.status !== 1) throw new Error(`git check-ignore failed: ${out.stderr.trim()}`)
    const hit = new Set(out.stdout.split('\0'))
    for (const f of fresh) ignored.set(f, hit.has(f))
  }
  return files.map((f) => ignored.get(f)!)
}

/**
 * The files a story file's look is taken over, absolute and sorted: each file
 * git does not ignore of the story's closure, of the Storybook preview and of
 * the global stylesheets Storybook wraps every story in. The theme file is
 * left out; its tokens are read one by one. `own` is the story's closure alone.
 */
export function inputsOf(storyFile: string, repoRoot: string, tokensCss = path.join(repoRoot, TOKENS)): { files: string[]; own: string[] } {
  const globals = GLOBAL_CSS.map((f) => path.join(repoRoot, f))
  // A data file only the preview reaches feeds the preview's own frames, not the story.
  const preview = previewClosure(path.join(repoRoot, PREVIEW), repoRoot).filter((f) => !f.endsWith('.json'))
  const own = storyClosure(path.resolve(repoRoot, storyFile), repoRoot)
  const candidates = [...new Set([...own, ...globals, ...preview])].filter((f) => f !== path.join(repoRoot, TOKENS) && f !== tokensCss)
  const skip = gitIgnored(candidates, repoRoot)
  return { files: candidates.filter((_, i) => !skip[i]).sort(), own: own.filter((f) => !ignored.get(f)) }
}

/** The English catalogues, relative to the repository root. */
export const catalogueFiles = (repoRoot: string): string[] =>
  fs.globSync(CATALOGUES, { cwd: repoRoot }).map((f) => f.split(path.sep).join('/')).sort()

type Print = { hash: string; files: string[]; tokens: Record<string, { light: string; dark: string }> }
const prints = new Map<string, { inputs: string[]; stamps: string[]; print: Print }>()

/**
 * The fingerprint of a story file's look, and the inputs it was taken over.
 * It is kept until one of its inputs, the theme file or a catalogue changes.
 *
 * The hash covers, with paths relative to `repoRoot` so it is the same on any
 * machine:
 * - each file of `inputsOf`, as a content digest, with the digest of the CSS
 *   of the utilities the file names;
 * - the light and dark value of each theme token those files read, rather
 *   than the whole theme file, so a token edit touches only its readers;
 * - the theme file's residue, which holds its variants, layers and utilities,
 *   and the values of the tokens the residue reads;
 * - the English strings under the keys the story's own closure can name. The
 *   preview names keys for every story, which would tie each to every
 *   printer code and fault message.
 */
export async function fingerprint(storyFile: string, repoRoot: string, opts: { tokensCss?: string } = {}): Promise<Print> {
  const tokensCss = opts.tokensCss ?? path.join(repoRoot, TOKENS)
  const { files, own } = inputsOf(storyFile, repoRoot, tokensCss)
  const inputs = [...files, tokensCss, ...catalogueFiles(repoRoot).map((f) => path.join(repoRoot, f))]
  const key = `${storyFile}\0${tokensCss}`
  const hit = prints.get(key)
  if (hit && hit.inputs.length === inputs.length && hit.inputs.every((f, i) => f === inputs[i] && unchanged(f, hit.stamps[i]!))) return hit.print
  const stamps = inputs.map(stamp)

  const rel = (file: string) => path.relative(repoRoot, file).split(path.sep).join('/')
  const styles = await Promise.all(files.map((f) => (ASSET.test(f) ? { properties: [], css: '' } : fileStyle(f, tokensCss))))
  const theme = loadTokens(tokensCss)
  const value = (name: string, t: 'light' | 'dark') => {
    const v = resolveToken(name, t, theme)
    return typeof v === 'string' ? v : 'unresolved'
  }
  const tokens: Record<string, { light: string; dark: string }> = {}
  // The residue's own rules, such as the scrollbars in `@layer base`, draw on every page.
  const global = [...theme.residue.matchAll(/\bvar\(\s*(--[\w-]+)/g)].map((m) => m[1]!)
  for (const name of [...new Set([...global, ...styles.flatMap((s) => s.properties)])].sort()) {
    if (theme.light.has(name) || theme.dark.has(name)) tokens[name] = { light: value(name, 'light'), dark: value(name, 'dark') }
  }

  const read = files.map(factsOf)
  const strings = matchedStrings(own, repoRoot)

  const hash = sha256(
    JSON.stringify({
      files: files.map((f, i) => [rel(f), read[i]!.digest, styles[i]!.css]),
      tokens,
      residue: theme.residue,
      strings,
    })
  )
  const print = { hash, files: files.map(rel), tokens }
  prints.set(key, { inputs, stamps, print })
  return print
}
