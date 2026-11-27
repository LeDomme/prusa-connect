import assert from 'node:assert/strict'
import { test } from 'node:test'
import fs from 'node:fs'
import path from 'node:path'
import { inputsOf } from '../src/status/fingerprint.ts'
import { concepts, scan, statusOf, verdict, type Approvals, type Deprecations } from '../src/status/manifest.ts'
import { designRepo, STORY_A, STORY_B } from './design-repo.ts'

const story = 'packages/svelte/src/primitives/button/button.stories.svelte'
const approved: Approvals = { [story]: { fingerprint: 'h1', by: 'designer', at: '2026-11-24' } }
const deprecated: Deprecations = { 'packages/svelte/src/primitives/button/button.svelte': { use: 'packages/svelte/src/primitives/link/link.svelte', at: '2026-11-24' } }
const concept = new Set([story])
const none = new Set<string>()

test('a deprecated component outranks every other status', () => {
  assert.equal(statusOf(story, 'h1', approved, deprecated, concept), 'deprecated')
  assert.deepEqual(verdict(story, 'h1', approved, deprecated, none), {
    status: 'deprecated',
    reason: 'use packages/svelte/src/primitives/link/link.svelte'
  })
})

test('a concept outranks an approval', () => {
  assert.equal(statusOf(story, 'h1', approved, {}, concept), 'concept')
})

test('a matching fingerprint is approved', () => {
  assert.deepEqual(verdict(story, 'h1', approved, {}, none), { status: 'approved' })
})

test('a fingerprint that moved needs a re-check because files changed', () => {
  assert.deepEqual(verdict(story, 'h2', approved, {}, none), { status: 'needs re-check', reason: 'files changed' })
})

test('a story with no approval is raw', () => {
  assert.equal(statusOf(story, 'h1', {}, {}, none), 'raw')
})

test('an entry without a fingerprint, such as a review note, is raw', () => {
  const note: Approvals = { [story]: { note: 'spacing', by: 'designer', at: '2026-11-24' } }
  assert.equal(statusOf(story, 'h1', note, {}, none), 'raw')
})

test('a story file listed itself is deprecated', () => {
  assert.equal(statusOf(story, 'h1', {}, { [story]: { use: 'x.svelte', at: '2026-11-24' } }, none), 'deprecated')
})

test('a rescan sees a closure file that changed and keeps the other hashes', async () => {
  const work = designRepo()
  const before = await scan(work, [STORY_A, STORY_B])
  fs.writeFileSync(path.join(work, 'packages/svelte/src/a/a.svelte'), '<p>a, a little longer</p>\n')
  const after = await scan(work, [STORY_A, STORY_B])
  assert.notEqual(after.get(STORY_A)?.hash, before.get(STORY_A)?.hash)
  assert.equal(after.get(STORY_B)?.hash, before.get(STORY_B)?.hash)
})

test('a fixture makes a concept of its own story only', () => {
  const work = designRepo()
  fs.mkdirSync(path.join(work, 'internal/transport/http/httpapi'), { recursive: true })
  fs.writeFileSync(path.join(work, 'internal/transport/http/httpapi/openapi.json'), '{ "paths": {} }\n')
  const dir = path.join(work, 'packages/svelte/src/a')
  const unknown = JSON.stringify({ 'GET /api/v1/cameras': { status: 200, type: 'application/json', body: '{}' } })
  fs.writeFileSync(path.join(dir, 'a.fixture.json'), unknown)
  fs.writeFileSync(path.join(work, 'packages/svelte/src/shared.fixture.json'), unknown)
  const plain = 'packages/svelte/src/a/plain.stories.svelte'
  const importer = 'packages/svelte/src/a/importer.stories.svelte'
  fs.writeFileSync(path.join(work, plain), '<script></script>\n')
  fs.writeFileSync(path.join(work, importer), "<script>\n  import data from '../shared.fixture.json'\n</script>\n")
  const helped = 'packages/svelte/src/a/helped.stories.svelte'
  fs.writeFileSync(path.join(work, 'packages/svelte/src/a/data.ts'), "export { default } from '../shared.fixture.json'\n")
  fs.writeFileSync(path.join(work, helped), "<script>\n  import data from './data.ts'\n</script>\n")
  assert.deepEqual([...concepts(work, [STORY_A, STORY_B, plain, importer, helped])].sort(), [STORY_A, helped, importer].sort())
  const fixture = path.join(dir, 'a.fixture.json')
  assert.ok(inputsOf(STORY_A, work).files.includes(fixture))
  assert.ok(!inputsOf(plain, work).files.includes(fixture))
})

test('a fixture that cannot be read makes a concept and stops nothing', () => {
  const work = designRepo()
  fs.mkdirSync(path.join(work, 'internal/transport/http/httpapi'), { recursive: true })
  const spec = { paths: { '/api/v1/x': { get: { responses: { '200': { content: { 'application/json': { schema: { type: 'object' } } } } } } } } }
  fs.writeFileSync(path.join(work, 'internal/transport/http/httpapi/openapi.json'), JSON.stringify(spec))
  fs.writeFileSync(path.join(work, 'packages/svelte/src/a/a.fixture.json'), '{ "GET /api/v1/x": ')
  fs.writeFileSync(path.join(work, 'packages/svelte/src/b/b.fixture.json'), '{ "GET /api/v1/x": null }')
  assert.deepEqual([...concepts(work, [STORY_A, STORY_B])], [STORY_A])
})
