import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { test, type TestContext } from 'node:test'
import { loadTokens, resolveToken } from '../src/status/tokens.ts'

const root = path.resolve(import.meta.dirname, '../../..')

const fixture = (t: TestContext, css: string) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'tokens-'))
  t.after(() => fs.rmSync(dir, { recursive: true }))
  fs.writeFileSync(path.join(dir, 'tokens.css'), css)
  return loadTokens(path.join(dir, 'tokens.css'))
}

test('a token resolves to the literal at the end of its chain', () => {
  const tokens = loadTokens(path.join(root, 'packages/svelte/src/styles/tokens.css'))
  const end = tokens.light.get('--color-gray-light-6')
  assert.match(end ?? '', /^oklch\(/)
  assert.equal(resolveToken('--color-panel-outline', 'light', tokens), end)
})

test('a circular chain is unresolved', (t) => {
  const tokens = fixture(t, ':root { --a: var(--b); --b: var(--a); }')
  assert.deepEqual(resolveToken('--a', 'light', tokens), { unresolved: true, chain: ['--a', '--b', '--a'] })
})

test('a token that reads itself inside calc() is unresolved', (t) => {
  const tokens = fixture(t, ':root { --c: calc(1px + var(--c)); }')
  assert.deepEqual(resolveToken('--c', 'light', tokens), { unresolved: true, chain: ['--c', '--c'] })
})

test('a token only the dark theme declares is unresolved in light', (t) => {
  const tokens = fixture(t, ':root { --a: red; }\n.dark { --night: var(--a); }')
  assert.deepEqual(resolveToken('--night', 'light', tokens), { unresolved: true, chain: ['--night'] })
  assert.equal(resolveToken('--night', 'dark', tokens), 'red')
})
