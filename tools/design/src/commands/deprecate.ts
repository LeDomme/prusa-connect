import fs from 'node:fs'
import path from 'node:path'
import { repoRoot } from '../git.ts'
import { DEPRECATIONS, readManifest, repoPath, writeManifest, type Deprecations } from '../status/manifest.ts'

// design deprecate <file> --use <file>: names the component to use instead of
// `file` in design/deprecations.json of the working tree. The change is
// committed and reviewed like any other.
export async function run(args: string[]): Promise<number> {
  const at = args.indexOf('--use')
  const rest = args.filter((_, i) => i !== at && i !== at + 1)
  if (at < 0 || !args[at + 1] || rest.length !== 1) {
    console.error('usage: design deprecate <file> --use <file>')
    return 2
  }
  const root = repoRoot()
  const file = repoPath(root, rest[0]!)
  const use = repoPath(root, args[at + 1]!)
  if (file === use) {
    console.error('refused: a component cannot replace itself')
    return 2
  }
  const missing = [file, use].filter((f) => !fs.existsSync(path.join(root, f)))
  if (missing.length > 0) {
    console.error(`refused: no such file: ${missing.join(', ')}`)
    return 2
  }
  const deprecations = readManifest<Deprecations>(root, DEPRECATIONS)
  deprecations[file] = { use, at: new Date().toISOString().slice(0, 10) }
  writeManifest(root, DEPRECATIONS, deprecations)
  console.log(`${DEPRECATIONS}: ${file} is deprecated, use ${use}`)
  return 0
}
