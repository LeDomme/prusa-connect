import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'
import { test } from 'node:test'
import * as api from '../src/api.ts'
import { designRepo, STORY_A, STORY_B } from './design-repo.ts'

test('the API names what another checkout builds on', () => {
  assert.deepEqual(Object.keys(api).sort(), [
    'DEPRECATIONS',
    'TOKENS',
    'catalogueFiles',
    'componentOf',
    'deadClasses',
    'externalResources',
    'fingerprint',
    'inputsOf',
    'readManifest',
    'scan',
    'statuses',
    'storyFiles',
    'writeManifest'
  ])
})

test('without approvals every story is raw, and an approval given is read', async () => {
  const work = designRepo()
  const found = await api.scan(work, api.storyFiles(work))
  assert.deepEqual([...found.keys()], [STORY_A, STORY_B])
  assert.deepEqual(api.statuses(work, found), { [STORY_A]: { status: 'raw' }, [STORY_B]: { status: 'raw' } })
  const approvals: api.Approvals = {
    [STORY_A]: { fingerprint: found.get(STORY_A)!.hash, by: 'designer', at: '2026-11-26' },
    [STORY_B]: { note: 'spacing', by: 'designer', at: '2026-11-26' }
  }
  assert.deepEqual(api.statuses(work, found, approvals), {
    [STORY_A]: { status: 'approved' },
    [STORY_B]: { status: 'raw', note: 'spacing' }
  })
})

test('status reads the approvals file it is given', async () => {
  const work = designRepo()
  const found = await api.scan(work, [STORY_A])
  const file = path.join(work, '..', 'reviewed.json')
  api.writeManifest(path.dirname(file), path.basename(file), { [STORY_A]: { fingerprint: found.get(STORY_A)!.hash, by: 'designer', at: '2026-11-26' } })
  const cli = path.resolve(import.meta.dirname, '../src/cli.ts')
  const status = (...args: string[]) => execFileSync(process.execPath, [cli, 'status', ...args], { cwd: work, encoding: 'utf8' })
  assert.match(status(STORY_A), /^raw +packages\/svelte\/src\/a\/a\.stories\.svelte$/m)
  assert.match(status(STORY_A, '--approvals', file), /^approved +packages\/svelte\/src\/a\/a\.stories\.svelte$/m)
  assert.ok(fs.existsSync(file))
})
