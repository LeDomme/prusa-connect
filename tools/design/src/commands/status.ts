import fs from 'node:fs'
import path from 'node:path'
import { repoRoot } from '../git.ts'
import { repoPath, scan, statuses, storyFiles, type Noted, type Scan } from '../status/manifest.ts'
import { approvalsFrom, option } from '../options.ts'

const USAGE = 'usage: design status [file…] [--approvals <file>] [--write <out.json>]'

/** The fingerprints of `files`, or null after one line naming why they cannot be taken, such as a broken theme file. */
export async function scanOrSay(root: string, files: string[]): Promise<Scan | null> {
  try {
    return await scan(root, files)
  } catch (e) {
    console.error(`status unavailable: ${(e as Error).message.split('\n')[0]}`)
    return null
  }
}

// design status [file…] [--approvals <file>] [--write <out.json>]
export async function run(args: string[]): Promise<number> {
  const root = repoRoot()
  const approvals = option(args, 'approvals')
  const write = approvals && option(approvals.rest, 'write')
  if (!approvals || !write) {
    console.error(USAGE)
    return 2
  }
  const named = write.rest.map((f) => repoPath(root, f))
  const known = storyFiles(root)
  const unknown = named.filter((f) => !known.includes(f))
  if (unknown.length > 0) {
    console.error(`not a story file the Storybook shows: ${unknown.join(', ')}`)
    return 2
  }

  const fingerprints = await scanOrSay(root, named.length > 0 ? named : known)
  if (!fingerprints) return 1
  const result = statuses(root, fingerprints, approvalsFrom(approvals.value))
  // Over the whole Storybook, the raw files are counted rather than listed: they are most of it.
  const counts = new Map<string, number>()
  for (const [file, { status, reason }] of Object.entries(result)) {
    counts.set(status, (counts.get(status) ?? 0) + 1)
    if (named.length > 0 || status !== 'raw') console.log(`${status.padEnd(15)} ${file}${reason ? `  (${reason})` : ''}`)
  }
  console.log([...counts].map(([status, n]) => `${n} ${status}`).join(' · '))
  if (write.value) save(path.resolve(write.value), result)
  return 0
}

function save(file: string, result: Record<string, Noted>): void {
  fs.mkdirSync(path.dirname(file), { recursive: true })
  fs.writeFileSync(file, `${JSON.stringify(result, null, 2)}\n`)
}
