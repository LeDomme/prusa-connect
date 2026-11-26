import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { test, type TestContext } from 'node:test'
import { deadClasses } from '../src/status/dead-classes.ts'

const dead = (t: TestContext, svelte: string) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dead-'))
  t.after(() => fs.rmSync(dir, { recursive: true }))
  fs.writeFileSync(path.join(dir, 'x.svelte'), svelte)
  return deadClasses(path.join(dir, 'x.svelte'))
}

test('a class that compiles to nothing is dead', async (t) => {
  assert.deepEqual(await dead(t, '<div class="bg-card bg-doesnotexist"></div>'), ['bg-doesnotexist'])
})

test('the strings of a class expression are classes, and what they are compared with is not', async (t) => {
  const svelte = `<div class={['bg-card', open && 'bg-doesnotexist', variant === 'ghost' ? 'p-2' : 'p-4']}></div>`
  assert.deepEqual(await dead(t, svelte), ['bg-doesnotexist'])
})

test('a template literal with a placeholder is skipped', async (t) => {
  const svelte = '<script>const c = cn(`bg-${tone}-x`)</script>\n<div class={`text-${size}-y p-2`}></div>'
  assert.deepEqual(await dead(t, svelte), [])
})
