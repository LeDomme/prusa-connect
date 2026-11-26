import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { test } from 'node:test'
import { importClosure } from '../src/status/closure.ts'
import { screenModuleFor, storyClosure } from '../src/status/screens.ts'

const root = path.resolve(import.meta.dirname, '../../..')
const at = (p: string) => path.join(root, p)
const screen = (name: string) => at(`frontend/src/lib/screens/${name}.svelte`)

test('a library story stays inside the library', () => {
  const files = storyClosure(at('packages/svelte/src/primitives/card/card.stories.svelte'), root)
  assert.ok(files.includes(at('packages/svelte/src/primitives/card/card.svelte')))
  assert.deepEqual(files.filter((f) => f.startsWith(at('frontend/src/'))), [])
})

test('a screen story takes its own screen and the shell, not the others', () => {
  const files = storyClosure(at('frontend/src/lib/screens/home.stories.svelte'), root)
  assert.ok(files.includes(screen('home')))
  assert.ok(files.includes(at('frontend/src/app.svelte')))
  // Home draws the dashboard itself, and steps aside for the printers list or
  // the one printer's page, so those are in; no unrelated screen is.
  for (const opened of ['dashboard', 'printers', 'printer-detail']) {
    assert.ok(files.includes(screen(opened)), `${opened}.svelte is missing`)
  }
  for (const other of ['jobs', 'storage', 'people', 'account']) {
    assert.ok(!files.includes(screen(other)), `${other}.svelte is in the closure`)
  }
})

test('a path the story computes in a helper opens the screen beside the story', () => {
  const files = storyClosure(at('frontend/src/lib/screens/storage.stories.svelte'), root)
  assert.ok(files.includes(screen('storage')))
  assert.ok(!files.includes(screen('printers')))
})

test('a computed path with no screen beside the story keeps every screen', () => {
  const files = storyClosure(at('frontend/src/tour/one-printer.stories.svelte'), root)
  assert.ok(files.includes(screen('tags')))
})

test('an address no route answers opens the not-found screen', () => {
  const files = storyClosure(at('frontend/src/lib/screens/not-found.stories.svelte'), root)
  assert.ok(files.includes(screen('not-found')))
  assert.ok(!files.includes(screen('jobs')))
})

test('a story takes only its own fixtures and the ones it imports', (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'closure-'))
  t.after(() => fs.rmSync(dir, { recursive: true }))
  fs.writeFileSync(path.join(dir, 'x.stories.svelte'), "<script>\n  import data from './shared.fixture.json'\n</script>\n")
  fs.writeFileSync(path.join(dir, 'y.stories.svelte'), '<script></script>')
  for (const f of ['x.fixture.json', 'x.empty.fixture.json', 'xy.fixture.json', 'shared.fixture.json', 'other.json']) {
    fs.writeFileSync(path.join(dir, f), '{}')
  }
  assert.deepEqual(storyClosure(path.join(dir, 'x.stories.svelte'), root), [
    path.join(dir, 'shared.fixture.json'),
    path.join(dir, 'x.empty.fixture.json'),
    path.join(dir, 'x.fixture.json'),
    path.join(dir, 'x.stories.svelte'),
  ])
  assert.deepEqual(storyClosure(path.join(dir, 'y.stories.svelte'), root), [path.join(dir, 'y.stories.svelte')])
})

test('screenModuleFor', () => {
  assert.equal(screenModuleFor('/', root), screen('home'))
  assert.equal(screenModuleFor('/dashboards/1?kiosk=1', root), screen('dashboard'))
  assert.equal(screenModuleFor('/storage/file', root), screen('old-file-address'))
  assert.equal(screenModuleFor('/storage/local', root), screen('storage'))
  assert.equal(screenModuleFor('/printers/x/files', root), screen('printer-detail'))
  assert.equal(screenModuleFor('/no-such-page', root), undefined)
})

test('importClosure reads every import form this repo writes', (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'closure-'))
  t.after(() => fs.rmSync(dir, { recursive: true }))
  const files: Record<string, string> = {
    'entry.svelte': `<script lang="ts">
  import {
    a, // the (x) thing; = 'q'
    b, /* "c" */
  } from './multi.ts'
  import type { T } from './typed.ts'
  import { s } from './store.svelte'
  import data from './data.json'
  const home = 'https://example.com/'; import { after } from './after.ts'
  const lazy = () => import('./lazy.ts')
</script>`,
    'multi.ts': "export * from './barrel.ts'\nexport const a = 1, b = 2\n",
    'barrel.ts': 'export const c = 3\n',
    'typed.ts': 'export type T = string\n',
    'store.svelte.ts': 'export const s = 1\n',
    'data.json': '{}',
    'after.ts': 'export const after = 1\n',
    'lazy.ts': 'export default 1\n',
  }
  for (const [name, text] of Object.entries(files)) fs.writeFileSync(path.join(dir, name), text)
  const want = ['after.ts', 'barrel.ts', 'data.json', 'entry.svelte', 'lazy.ts', 'multi.ts', 'store.svelte.ts']
  assert.deepEqual(importClosure(path.join(dir, 'entry.svelte'), [], new Set()), want.map((f) => path.join(dir, f)))
})
