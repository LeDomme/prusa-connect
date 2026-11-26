import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { test } from 'node:test'
import { buildReport, renderReport, type Report } from '../src/report.ts'
import { fingerprint } from '../src/status/fingerprint.ts'

const report: Report = {
  primitives: ['packages/svelte/src/primitives/card/card.stories.svelte'],
  screens: ['frontend/src/lib/screens/cameras.stories.svelte'],
  missing: [
    { fixture: 'frontend/src/lib/screens/cameras.fixture.json', endpoint: 'GET /api/v1/cameras/x/snapshots', fields: [] },
    { fixture: 'frontend/src/lib/screens/cameras.fixture.json', endpoint: 'GET /api/v1/cameras', fields: ['fps', 'items.lens'] },
  ],
  statusChanges: [
    {
      file: 'packages/svelte/src/primitives/card/card.stories.svelte',
      from: 'approved',
      to: 'needs re-check',
      reason: 'changed: packages/svelte/src/primitives/card/card.svelte',
    },
  ],
  contrast: [{ pair: ['--muted-foreground', '--muted'], theme: 'dark', ratio: 3.1234 }],
  deadClasses: [{ file: 'packages/svelte/src/primitives/card/card.svelte', classes: ['zz-nothing'] }],
  external: [{ file: 'packages/svelte/src/primitives/card/card.svelte', urls: ['https://example.com/x.png'] }],
}

test('renderReport prints every section', () => {
  assert.equal(
    renderReport(report),
    `## Design report

<details><summary>Primitives (1)</summary>

- \`packages/svelte/src/primitives/card/card.stories.svelte\`

</details>

<details><summary>Screens (1)</summary>

- \`frontend/src/lib/screens/cameras.stories.svelte\`

</details>

### Missing backend
- \`GET /api/v1/cameras/x/snapshots\`: the endpoint does not exist (\`frontend/src/lib/screens/cameras.fixture.json\`)
- \`GET /api/v1/cameras\`: missing fields \`fps\`, \`items.lens\` (\`frontend/src/lib/screens/cameras.fixture.json\`)

### Status changes on merge
- \`packages/svelte/src/primitives/card/card.stories.svelte\`: approved → needs re-check (changed: packages/svelte/src/primitives/card/card.svelte)

### Contrast under 4.5:1
- dark: \`--muted-foreground\` on \`--muted\`, 3.12:1

### Classes that compile to nothing
- \`packages/svelte/src/primitives/card/card.svelte\`: \`zz-nothing\`

### External resources
- \`packages/svelte/src/primitives/card/card.svelte\`: \`https://example.com/x.png\`
`
  )
})

test('renderReport says so when nothing changed', () => {
  const empty: Report = { primitives: [], screens: [], missing: [], statusChanges: [], contrast: [], deadClasses: [], external: [] }
  assert.equal(renderReport(empty), '## Design report\n\nNo story, fixture or status changes.\n')
})

const ROOT = path.resolve(import.meta.dirname, '../../..')

const run = (cwd: string, ...args: string[]) => execFileSync('git', args, { cwd, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim()
// Hooks and signing off, so the user's own git config does not reach the fixture commits.
const commit = (cwd: string, message: string) => {
  run(cwd, 'add', '-A')
  run(cwd, '-c', 'user.name=Designer', '-c', 'user.email=designer@example.com', '-c', 'commit.gpgsign=false', '-c', 'core.hooksPath=/dev/null', 'commit', '-q', '-m', message)
}

test('buildReport reads a PR against its merge base', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'design-report-test-'))
  try {
    run(dir, 'clone', '-q', '--shared', ROOT, 'repo')
    const repo = path.join(dir, 'repo')
    const card = 'packages/svelte/src/primitives/card/card.stories.svelte'
    const button = 'packages/svelte/src/primitives/button/button.stories.svelte'
    const badgeFixture = 'packages/svelte/src/primitives/badge/badge.fixture.json'
    const approval = async (file: string) => ({ fingerprint: (await fingerprint(path.join(repo, file), repo)).hash, by: 'designer', at: '2026-11-24' })

    // Card approved as it is at the base.
    const approvals = { [card]: await approval(card) }

    // The PR: card changes, badge gets a concept fixture.
    fs.appendFileSync(path.join(repo, 'packages/svelte/src/primitives/card/card.svelte'), '\n<i class="zz-nothing"></i>\n<img src="https://example.com/x.png" alt="" />\n')
    fs.writeFileSync(path.join(repo, badgeFixture), JSON.stringify({ 'GET /api/v1/cameras/x/snapshots': { status: 200, type: 'json', body: '{}' } }))
    commit(repo, 'pr')

    const r = await buildReport('HEAD~1', 'HEAD', repo, approvals)
    assert.ok(r.primitives.includes(card))
    assert.ok(!r.primitives.includes(button))
    assert.deepEqual(r.screens.filter((s) => !s.startsWith('frontend/')), [])
    assert.deepEqual(r.missing, [{ fixture: badgeFixture, endpoint: 'GET /api/v1/cameras/x/snapshots', fields: [] }])
    const changes = Object.fromEntries(r.statusChanges.map((c) => [c.file, c]))
    assert.deepEqual(changes[card], { file: card, from: 'approved', to: 'needs re-check', reason: 'changed: packages/svelte/src/primitives/card/card.svelte' })
    assert.equal(changes['packages/svelte/src/primitives/badge/badge.stories.svelte']?.to, 'concept')
    assert.equal(r.statusChanges.length, 2)
    assert.deepEqual(r.deadClasses, [{ file: 'packages/svelte/src/primitives/card/card.svelte', classes: ['zz-nothing'] }])
    assert.deepEqual(r.external, [{ file: 'packages/svelte/src/primitives/card/card.svelte', urls: ['https://example.com/x.png'] }])
    assert.deepEqual(r.contrast, [])
    // The base was read from a temporary worktree, which is gone again.
    assert.equal(run(repo, 'worktree', 'list').split('\n').length, 1)

    // Without the approvals, the card was never approved, so its status does not move.
    assert.equal((await buildReport('HEAD~1', 'HEAD', repo)).statusChanges.some((c) => c.file === card), false)
  } finally {
    fs.rmSync(dir, { recursive: true, force: true })
  }
})
