import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { missingBackend, type Missing } from './backend.ts'
import { git } from './git.ts'
import { contrastIssues } from './status/contrast.ts'
import { deadClasses } from './status/dead-classes.ts'
import { externalResources } from './status/external.ts'
import { fingerprint, sharedInputs, TOKENS } from './status/fingerprint.ts'
import { concepts, DEPRECATIONS, readFixture, readManifest, storyFiles, verdict, type Approvals, type Deprecations, type Status } from './status/manifest.ts'
import { ASSET } from './status/closure.ts'
import { storyClosure } from './status/screens.ts'
import { loadTokens, type Theme } from './status/tokens.ts'

const OPENAPI = 'internal/transport/http/httpapi/openapi.json'
const MISSING_HEADING = '### Missing backend'

export type Report = {
  /** Story files whose import closure the PR changes and that show a component. */
  primitives: string[]
  /** Story files whose import closure the PR changes and that mount the app through `AppAt`. */
  screens: string[]
  missing: (Missing & { fixture: string })[]
  statusChanges: { file: string; from: Status; to: Status; reason: string }[]
  /** Pairs under 4.5:1 at the head that were not under it at the base. */
  contrast: { pair: [string, string]; theme: Theme; ratio: number }[]
  deadClasses: { file: string; classes: string[] }[]
  external: { file: string; urls: string[] }[]
}

/**
 * A checkout of `ref` for the duration of `use`: `repoRoot` itself when its
 * HEAD is that commit, otherwise a temporary detached worktree. A fresh
 * worktree lacks the generated, git-ignored files a built checkout has, which
 * the fingerprint leaves out, so the hashes of both agree.
 */
async function checkout<T>(repoRoot: string, ref: string, use: (root: string) => Promise<T>): Promise<T> {
  const commit = git(['rev-parse', `${ref}^{commit}`], repoRoot)
  if (git(['rev-parse', 'HEAD'], repoRoot) === commit) return use(repoRoot)
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'design-report-'))
  git(['worktree', 'add', '-q', '--detach', dir, commit], repoRoot)
  try {
    return await use(dir)
  } finally {
    git(['worktree', 'remove', '--force', dir], repoRoot)
    fs.rmSync(dir, { recursive: true, force: true })
  }
}

const once = <T>(fn: () => Promise<T>) => {
  let p: Promise<T> | undefined
  return () => (p ??= fn())
}

const existing = (root: string, files: string[]) => files.filter((f) => fs.existsSync(path.join(root, f)))

/**
 * What a PR changes in the design, from the merge base of `base` and `head`
 * to `head`, judged against one set of approvals on both sides.
 *
 * - Primitives and screens: the story files at the head whose closure holds a
 *   changed file.
 * - Status changes: each story file at the head whose status differs between
 *   the merge base and the head. The base is checked out in a temporary
 *   worktree. A fingerprint is taken only where a status depends on it: an
 *   approval on that side, and a changed input or approval. Otherwise both
 *   sides share one hash, so their approval reads the same.
 * - Missing backend, dead classes and external resources: the fixture, Svelte
 *   and CSS files the PR changes. Contrast: the theme pairs the PR pushes under
 *   4.5:1, when it changes the theme.
 */
export async function buildReport(base: string, head: string, repoRoot: string, approvals: Approvals = {}): Promise<Report> {
  const mergeBase = git(['merge-base', base, head], repoRoot)
  const changed = new Set(git(['diff', '--name-only', '-z', mergeBase, head], repoRoot).split('\0').filter(Boolean))
  return checkout(repoRoot, head, (headRoot) => checkout(repoRoot, mergeBase, (baseRoot) => report(changed, headRoot, baseRoot, approvals)))
}

async function report(changed: Set<string>, headRoot: string, baseRoot: string, approvals: Approvals): Promise<Report> {
  const r: Report = { primitives: [], screens: [], missing: [], statusChanges: [], contrast: [], deadClasses: [], external: [] }
  const stories = storyFiles(headRoot)
  // A PR that touches no source a closure can hold, such as one that only edits
  // the deprecations, needs no closure at all.
  const sources = [...changed].some((f) => (/\.(svelte|ts|js|css|json)$/.test(f) || ASSET.test(f)) && !f.startsWith('design/'))
  const touched = new Map<string, string[]>()
  for (const f of sources ? stories : []) {
    const hit = storyClosure(path.join(headRoot, f), headRoot)
      .map((abs) => path.relative(headRoot, abs).split(path.sep).join('/'))
      .filter((rel) => changed.has(rel))
    if (hit.length === 0) continue
    touched.set(f, hit)
    const screen = /<AppAt\b/.test(fs.readFileSync(path.join(headRoot, f), 'utf8'))
    ;(screen ? r.screens : r.primitives).push(f)
  }

  // ponytail: a changed shared input, a catalogue edit included, fingerprints
  // every approved story on both sides; narrow it to the stories that read
  // the changed keys or tokens if that gets slow.
  const shared = [...new Set([...sharedInputs(headRoot), ...sharedInputs(baseRoot)])].filter((f) => changed.has(f))
  const side = (root: string, files: string[]) => ({
    approvals,
    deprecations: readManifest<Deprecations>(root, DEPRECATIONS),
    concepts: concepts(root, files),
  })
  const atBase = new Set(existing(baseRoot, stories))
  const [b, h] = [side(baseRoot, [...atBase]), side(headRoot, stories)]
  for (const f of stories) {
    const inputs = [...(touched.get(f) ?? []), ...shared]
    const [fb, fh] = [b.approvals[f]?.fingerprint ?? '', h.approvals[f]?.fingerprint ?? '']
    const headPrint = once(() => fingerprint(path.join(headRoot, f), headRoot))
    const baseHash = inputs.length > 0 ? once(async () => (atBase.has(f) ? (await fingerprint(path.join(baseRoot, f), baseRoot)).hash : '')) : async () => (await headPrint()).hash
    // Assuming each approval still holds; that differs from the truth on both
    // sides alike when no input and no approval changed.
    let from = verdict(f, fb, b.approvals, b.deprecations, b.concepts)
    let to = verdict(f, fh, h.approvals, h.deprecations, h.concepts)
    if (inputs.length === 0 && fb === fh && from.status === to.status) continue
    if (fb) from = verdict(f, await baseHash(), b.approvals, b.deprecations, b.concepts)
    if (fh) to = verdict(f, (await headPrint()).hash, h.approvals, h.deprecations, h.concepts)
    if (from.status === to.status) continue
    let reason = to.reason ?? ''
    if (to.status === 'needs re-check') {
      const own = new Set((await headPrint()).files)
      const moved = inputs.filter((i) => own.has(i) || i === TOKENS || /locales\/en[^/]*\.json$/.test(i))
      if (moved.length > 0) reason = `changed: ${[...new Set(moved)].join(', ')}`
    }
    r.statusChanges.push({ file: f, from: from.status, to: to.status, reason })
  }

  const now = existing(headRoot, [...changed].sort())
  const fixtures = now.filter((f) => f.endsWith('.fixture.json'))
  if (fixtures.length > 0) {
    const spec = JSON.parse(fs.readFileSync(path.join(headRoot, OPENAPI), 'utf8')) as object
    for (const fixture of fixtures) {
      for (const m of missingBackend(readFixture(path.join(headRoot, fixture), headRoot) ?? {}, spec)) r.missing.push({ fixture, ...m })
    }
  }

  const theme = path.join(headRoot, TOKENS)
  if (changed.has(TOKENS)) {
    const key = (i: Report['contrast'][number]) => `${i.theme} ${i.pair.join(' ')}`
    const before = fs.existsSync(path.join(baseRoot, TOKENS)) ? new Set(contrastIssues(loadTokens(path.join(baseRoot, TOKENS))).map(key)) : new Set<string>()
    r.contrast = contrastIssues(loadTokens(theme)).filter((i) => !before.has(key(i)))
  }

  for (const f of now.filter((f) => f.endsWith('.svelte'))) {
    const classes = await deadClasses(path.join(headRoot, f), theme)
    if (classes.length > 0) r.deadClasses.push({ file: f, classes })
  }
  for (const f of now.filter((f) => /\.(svelte|css)$/.test(f))) {
    const urls = externalResources(path.join(headRoot, f))
    if (urls.length > 0) r.external.push({ file: f, urls })
  }
  return r
}

const code = (s: string) => `\`${s}\``

// One changed primitive reaches most of the Storybook, so the story lists fold.
const folded = (title: string, files: string[]) =>
  files.length === 0 ? [] : [`<details><summary>${title} (${files.length})</summary>`, '', ...files.map((f) => `- ${code(f)}`), '', '</details>', '']

const section = (heading: string, lines: string[]) => (lines.length === 0 ? [] : [heading, ...lines, ''])

/** The report as Markdown, for a terminal or a pull request comment. */
export function renderReport(r: Report): string {
  const lines = [
    '## Design report',
    '',
    ...folded('Primitives', r.primitives),
    ...folded('Screens', r.screens),
    ...section(
      MISSING_HEADING,
      r.missing.map((m) =>
        m.fields.length === 0
          ? `- ${code(m.endpoint)}: the endpoint does not exist (${code(m.fixture)})`
          : `- ${code(m.endpoint)}: missing fields ${m.fields.map(code).join(', ')} (${code(m.fixture)})`
      )
    ),
    ...section(
      '### Status changes on merge',
      r.statusChanges.map((c) => `- ${code(c.file)}: ${c.from} → ${c.to}${c.reason ? ` (${c.reason})` : ''}`)
    ),
    ...section(
      '### Contrast under 4.5:1',
      r.contrast.map((c) => `- ${c.theme}: ${code(c.pair[0])} on ${code(c.pair[1])}, ${c.ratio.toFixed(2)}:1`)
    ),
    ...section(
      '### Classes that compile to nothing',
      r.deadClasses.map((d) => `- ${code(d.file)}: ${d.classes.map(code).join(', ')}`)
    ),
    ...section(
      '### External resources',
      r.external.map((e) => `- ${code(e.file)}: ${e.urls.map(code).join(', ')}`)
    ),
  ]
  if (lines.length === 2) lines.push('No story, fixture or status changes.', '')
  return `${lines.join('\n').trimEnd()}\n`
}
