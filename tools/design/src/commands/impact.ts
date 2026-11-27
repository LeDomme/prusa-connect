import fs from 'node:fs'
import path from 'node:path'
import { repoRoot } from '../git.ts'
import { approvalsFrom, option } from '../options.ts'
import { contrastIssues } from '../status/contrast.ts'
import { catalogueFiles, TOKENS } from '../status/fingerprint.ts'
import { statuses, storyFiles } from '../status/manifest.ts'
import { loadTokens, type Tokens } from '../status/tokens.ts'
import { scanOrSay } from './status.ts'

/** `name` and every token whose value reaches it through `var()`, in either theme. */
export function readers(name: string, tokens: Tokens): Set<string> {
  const found = new Set([name])
  for (let grew = true; grew; ) {
    grew = false
    for (const map of [tokens.light, tokens.dark]) {
      for (const [token, raw] of map) {
        if (found.has(token)) continue
        if ([...raw.matchAll(/\bvar\(\s*(--[\w-]+)/g)].some((m) => found.has(m[1]!))) {
          found.add(token)
          grew = true
        }
      }
    }
  }
  return found
}

// design impact <--token | file> [--approvals <file>]: the story files that
// depend on it, with their status, and for a token the contrast pairs it
// takes part in that fall under WCAG AA.
export async function run(args: string[]): Promise<number> {
  const approvals = option(args, 'approvals')
  const target = approvals?.rest[0]
  if (!approvals || !target || approvals.rest.length > 1) {
    console.error('usage: design impact <--token | file> [--approvals <file>]')
    return 2
  }
  const root = repoRoot()
  let theme: Tokens | undefined
  try {
    if (target.startsWith('--')) theme = loadTokens(path.join(root, TOKENS))
  } catch (e) {
    console.error(`status unavailable: ${(e as Error).message.split('\n')[0]}`)
    return 1
  }
  if (theme && !theme.light.has(target) && !theme.dark.has(target)) {
    console.error(`no token ${target} in ${TOKENS}`)
    return 2
  }
  let file = ''
  if (!theme) {
    let real: string
    try {
      real = fs.realpathSync(target)
    } catch {
      console.error(`no file ${target}`)
      return 2
    }
    file = path.relative(root, real).split(path.sep).join('/')
    if (file.startsWith('../') || path.isAbsolute(file)) {
      console.error(`${target} is outside the design repository`)
      return 2
    }
    // A fingerprint reads the theme and the catalogues key by key, so none lists them as a file.
    if (file === TOKENS || catalogueFiles(root).includes(file)) {
      console.log(`every story file reads ${file}${file === TOKENS ? '; design impact <--token> lists the readers of one token' : ''}`)
      return 0
    }
  }
  const fingerprints = await scanOrSay(root, storyFiles(root))
  if (!fingerprints) return 1
  const names = theme ? readers(target, theme) : undefined
  const dependents = [...fingerprints].filter(([, f]) =>
    names ? Object.keys(f.tokens).some((t) => names.has(t)) : f.files.includes(file)
  )
  const result = statuses(root, new Map(dependents), approvalsFrom(approvals.value))
  for (const [story, { status }] of Object.entries(result)) console.log(`${status.padEnd(15)} ${story}`)
  console.log(`${dependents.length} story files depend on ${theme ? target : file}`)
  if (theme && names) {
    for (const { pair, theme: mode, ratio } of contrastIssues(theme)) {
      if (pair.some((t) => names.has(t))) console.log(`contrast ${ratio.toFixed(2)}:1 in ${mode}: ${pair[0]} on ${pair[1]}`)
    }
  }
  return 0
}
