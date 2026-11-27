import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { test } from 'node:test'
import { externalResources } from '../src/status/external.ts'

test('a font fetched from elsewhere is found and a link is not', (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'external-'))
  t.after(() => fs.rmSync(dir, { recursive: true }))
  const file = path.join(dir, 'x.svelte')
  fs.writeFileSync(file, '<a href="https://docs">Docs</a>\n<style>@font-face { src: url(https://x/y.woff2); }</style>')
  assert.deepEqual(externalResources(file), ['https://x/y.woff2'])
})
