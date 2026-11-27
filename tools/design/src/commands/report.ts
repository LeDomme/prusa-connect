import fs from 'node:fs'
import { capture, repoRoot } from '../git.ts'
import { approvalsFrom, option } from '../options.ts'
import { buildReport, renderReport } from '../report.ts'

const USAGE = 'usage: design report --pr <number> [--approvals <file>] [--json <out.json>]'

// design report --pr <n> [--approvals <file>] [--json <out.json>]: prints the
// PR's design report, and writes the report's data for another tool to read.
export async function run(args: string[]): Promise<number> {
  const pr = option(args, 'pr')
  const approvals = pr && option(pr.rest, 'approvals')
  const out = approvals && option(approvals.rest, 'json')
  const n = pr?.value
  if (!out || out.rest.length > 0 || !n || !/^\d+$/.test(n)) {
    console.error(USAGE)
    return 2
  }
  try {
    const root = repoRoot()
    const head = JSON.parse(capture('gh', ['pr', 'view', n, '--json', 'baseRefName,headRefOid'], root)) as {
      baseRefName: string
      headRefOid: string
    }
    const report = await buildReport(`origin/${head.baseRefName}`, head.headRefOid, root, approvalsFrom(approvals.value))
    if (out.value) fs.writeFileSync(out.value, `${JSON.stringify(report, null, 2)}\n`)
    process.stdout.write(renderReport(report))
    return 0
  } catch (e) {
    console.error(`report unavailable: ${(e as Error).message.trim().split('\n')[0]}`)
    return 1
  }
}
