import fs from 'node:fs'
import path from 'node:path'
import { missingBackend } from '../backend.ts'
import { fingerprint } from './fingerprint.ts'
import { storyClosure } from './screens.ts'
import { onePass } from './tokens.ts'

export type Status = 'raw' | 'approved' | 'needs re-check' | 'deprecated' | 'concept'
export type Verdict = { status: Status; reason?: string }
/**
 * Keyed by story file, relative to the repository root. An entry is an
 * approval of the file's fingerprint, or a review note that leaves it raw.
 * This repository holds none: whoever reviews the design keeps them and
 * passes them in.
 */
export type Approvals = Record<string, { by: string; at: string } & ({ fingerprint: string; note?: never } | { note: string; fingerprint?: never })>
/** Keyed by component file, relative to the repository root. */
export type Deprecations = Record<string, { use: string; at: string }>

export const DEPRECATIONS = 'design/deprecations.json'
const MAIN = 'frontend/.storybook/main.ts'
const OPENAPI = 'internal/transport/http/httpapi/openapi.json'

/** The component a story file shows: `x.stories.svelte` shows `x.svelte` beside it. */
export const componentOf = (storyFile: string) => storyFile.replace(/\.stories\.svelte$/, '.svelte')

/** Precedence: deprecated, concept, approved or needs re-check, raw. */
export function verdict(
  storyFile: string,
  currentHash: string,
  approvals: Approvals,
  deprecations: Deprecations,
  concepts: Set<string>
): Verdict {
  const deprecated = deprecations[componentOf(storyFile)] ?? deprecations[storyFile]
  if (deprecated) return { status: 'deprecated', reason: `use ${deprecated.use}` }
  if (concepts.has(storyFile)) return { status: 'concept' }
  // An entry without a fingerprint is a review note, not an approval.
  const approval = approvals[storyFile]
  if (!approval?.fingerprint) return { status: 'raw' }
  return approval.fingerprint === currentHash ? { status: 'approved' } : { status: 'needs re-check', reason: 'files changed' }
}

export function statusOf(
  storyFile: string,
  currentHash: string,
  approvals: Approvals,
  deprecations: Deprecations,
  concepts: Set<string>
): Status {
  return verdict(storyFile, currentHash, approvals, deprecations, concepts).status
}

/** The manifest at `file`, relative to `root` or absolute, or an empty one when there is no such file. */
export function readManifest<T>(root: string, file: string): T {
  try {
    return JSON.parse(fs.readFileSync(path.resolve(root, file), 'utf8')) as T
  } catch (e) {
    if ((e as NodeJS.ErrnoException).code === 'ENOENT') return {} as T
    throw e
  }
}

/** Keys sorted, so two approvals touch two lines and merge without a conflict. */
export function writeManifest(root: string, file: string, data: Record<string, unknown>): void {
  const sorted = Object.fromEntries(Object.entries(data).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)))
  fs.mkdirSync(path.dirname(path.join(root, file)), { recursive: true })
  fs.writeFileSync(path.join(root, file), `${JSON.stringify(sorted, null, 2)}\n`)
}

/**
 * Every story file the product Storybook shows, relative to the repository
 * root. The globs are read from the `stories` list of its `main.ts`, so the
 * two cannot drift apart.
 */
export function storyFiles(root: string): string[] {
  const list = /^\s*stories:\s*\[([^\]]*)\]/m.exec(fs.readFileSync(path.join(root, MAIN), 'utf8'))?.[1] ?? ''
  const globs = [...list.matchAll(/'([^']+\.stories\.svelte)'/g)].map((m) =>
    path.relative(root, path.resolve(root, path.dirname(MAIN), m[1]!)).split(path.sep).join('/')
  )
  if (globs.length === 0) throw new Error(`no story globs found in the stories list of ${MAIN}`)
  return fs.globSync(globs, { cwd: root, exclude: (f) => f.includes('node_modules') }).map((f) => f.split(path.sep).join('/')).sort()
}

/**
 * The story files whose closure (`storyClosure`) holds a `*.fixture.json` that
 * names an endpoint or field the core OpenAPI spec lacks.
 *
 * ponytail: every fixture is checked against the core spec, so an extension's
 * own endpoints count as missing; read the extension's `openapi.json` too once
 * an extension story carries a fixture.
 */
export function concepts(root: string, files: string[]): Set<string> {
  let spec: object | undefined
  const found = new Set<string>()
  for (const file of files) {
    for (const fixture of storyClosure(path.join(root, file), root).filter((f) => f.endsWith('.fixture.json'))) {
      spec ??= JSON.parse(fs.readFileSync(path.join(root, OPENAPI), 'utf8')) as object
      const data = readFixture(fixture, root)
      // Data that cannot be read cannot show the backend has it.
      if (data === null || missingBackend(data, spec).length > 0) found.add(file)
    }
  }
  return found
}

/** A fixture file's entries, or null after one line naming the file when it is not a JSON object. */
export function readFixture(file: string, root: string): Parameters<typeof missingBackend>[0] | null {
  try {
    const data: unknown = JSON.parse(fs.readFileSync(file, 'utf8'))
    if (data !== null && typeof data === 'object' && !Array.isArray(data)) return data as Parameters<typeof missingBackend>[0]
    throw new Error('not a JSON object')
  } catch (e) {
    console.error(`unreadable fixture ${path.relative(root, file)}: ${(e as Error).message.split('\n')[0]}`)
    return null
  }
}

export type Scan = Map<string, Awaited<ReturnType<typeof fingerprint>>>

/**
 * The fingerprint of each story file, one after another in this process, so
 * the caches carry over. It yields between files, so a server running it
 * keeps answering.
 */
export function scan(root: string, files: string[]): Promise<Scan> {
  return onePass(async () => {
    const result: Scan = new Map()
    for (const file of files) {
      result.set(file, await fingerprint(path.join(root, file), root))
      await new Promise(setImmediate)
    }
    return result
  })
}

export type Noted = Verdict & { note?: string }

/** The status of each scanned story file against the approvals given, none by default, and the deprecations of `root`, with its review note. */
export function statuses(
  root: string,
  fingerprints: Scan,
  approvals: Approvals = {},
  deprecations = readManifest<Deprecations>(root, DEPRECATIONS)
): Record<string, Noted> {
  const files = [...fingerprints.keys()]
  const ideas = concepts(root, files)
  return Object.fromEntries(
    files.map((f) => {
      const note = approvals[f]?.note
      return [f, { ...verdict(f, fingerprints.get(f)!.hash, approvals, deprecations, ideas), ...(note ? { note } : {}) }]
    })
  )
}

/** A path as given on the command line, relative to the repository root. */
export const repoPath = (root: string, file: string) => path.relative(root, path.resolve(file)).split(path.sep).join('/')
