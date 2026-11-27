import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { test } from 'node:test'
import { contrastIssues } from '../src/status/contrast.ts'
import { loadTokens } from '../src/status/tokens.ts'

test('#777 text on #fff falls short of 4.5:1', (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'contrast-'))
  t.after(() => fs.rmSync(dir, { recursive: true }))
  fs.writeFileSync(path.join(dir, 'tokens.css'), ':root { --note: #fff; --note-foreground: #777; }')
  const issues = contrastIssues(loadTokens(path.join(dir, 'tokens.css')))
  assert.deepEqual(issues.map((i) => [i.pair, i.theme]), [
    [['--note-foreground', '--note'], 'light'],
    [['--note-foreground', '--note'], 'dark'],
  ])
  assert.ok(Math.abs(issues[0]!.ratio - 4.48) < 0.01, String(issues[0]!.ratio))
})
