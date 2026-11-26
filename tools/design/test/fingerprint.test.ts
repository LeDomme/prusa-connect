import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { test } from 'node:test'
import { sourceAliases } from '../../../frontend/vite/import-map.ts'
import { importClosure } from '../src/status/closure.ts'
import { fingerprint, matchedStrings } from '../src/status/fingerprint.ts'
import { storyClosure } from '../src/status/screens.ts'
import { scan } from '../src/status/manifest.ts'
import { loadTokens } from '../src/status/tokens.ts'
import { designRepo, git, STORY_A } from './design-repo.ts'

const root = path.resolve(import.meta.dirname, '../../..')
const tokensCss = path.join(root, 'packages/svelte/src/styles/tokens.css')
const card = path.join(root, 'packages/svelte/src/primitives/card/card.stories.svelte')
const logo = path.join(root, 'packages/svelte/src/primitives/connect-logo/connect-logo.stories.svelte')
const TOKEN = '--card-foreground'

test('a token edit changes the stories that use it and no other', async (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'fingerprint-'))
  t.after(() => fs.rmSync(dir, { recursive: true }))
  const edited = path.join(dir, 'tokens.css')
  fs.writeFileSync(edited, fs.readFileSync(tokensCss, 'utf8').replace(new RegExp(`^(\\s*${TOKEN}:)[^;]+;`, 'm'), '$1 #123456;'))
  assert.notEqual(loadTokens(edited).light.get(TOKEN), loadTokens(tokensCss).light.get(TOKEN))

  const [a, b] = [await fingerprint(card, root), await fingerprint(logo, root)]
  assert.ok(TOKEN in a.tokens, `${TOKEN} is not a token of the card story`)
  assert.ok(!(TOKEN in b.tokens), `${TOKEN} is a token of the logo story`)
  assert.equal((await fingerprint(card, root)).hash, a.hash)
  assert.ok(a.files.every((f) => !path.isAbsolute(f)), 'a path in files is absolute')

  assert.notEqual((await fingerprint(card, root, { tokensCss: edited })).hash, a.hash)
  assert.equal((await fingerprint(logo, root, { tokensCss: edited })).hash, b.hash)
})

test('a key only the preview names is no string of a library story', () => {
  const preview = importClosure(path.join(root, 'frontend/.storybook/preview.ts'), sourceAliases(), new Set())
  const shared = (key: string) => key.startsWith('printerCode.xbuddy.') || key.startsWith('error.')
  assert.ok(matchedStrings(preview, root).some(([, key]) => shared(key)), 'the preview names no printer code or fault key')
  const button = path.join(root, 'packages/svelte/src/primitives/button/button.stories.svelte')
  assert.deepEqual(matchedStrings(storyClosure(button, root), root).filter(([, key]) => shared(key)), [])
})

test('an image a story imports is part of its fingerprint, by its bytes', async () => {
  const passwordInput = path.join(root, 'packages/svelte/src/primitives/password-input/password-input.stories.svelte')
  assert.ok((await fingerprint(passwordInput, root)).files.includes('packages/svelte/src/primitives/password-input/assets/password-visible.svg'))

  const work = designRepo()
  const icon = path.join(work, 'packages/svelte/src/a/icon.svg')
  fs.writeFileSync(icon, '<svg viewBox="0 0 1 1"></svg>\n')
  fs.writeFileSync(path.join(work, 'packages/svelte/src/a/a.svelte'), "<script>\n  import icon from './icon.svg'\n</script>\n<img src={icon} alt='' />\n")
  const before = (await scan(work, [STORY_A])).get(STORY_A)!
  assert.ok(before.files.includes('packages/svelte/src/a/icon.svg'))
  fs.writeFileSync(icon, '<svg viewBox="0 0 2 2"><path d="M0 0h2v2" /></svg>\n')
  assert.notEqual((await scan(work, [STORY_A])).get(STORY_A)!.hash, before.hash)
})

test("a served module's source is part of every app story's fingerprint", async (t) => {
  const home = 'frontend/src/lib/screens/home.stories.svelte'
  const page = 'extensions/fleet-stats/ui/src/page.svelte'
  // A checkout of its own, so the edit never touches this one.
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'fingerprint-module-'))
  git(root, 'worktree', 'add', '-q', '--detach', dir, 'HEAD')
  t.after(() => {
    git(root, 'worktree', 'remove', '--force', dir)
    fs.rmSync(dir, { recursive: true, force: true })
  })
  const before = await fingerprint(path.join(dir, home), dir)
  assert.ok(before.files.includes(page))
  fs.appendFileSync(path.join(dir, page), '<p>one more line</p>\n')
  assert.notEqual((await fingerprint(path.join(dir, home), dir)).hash, before.hash)
})

test("an extension client's source is part of the stories that import it by name", async () => {
  const story = 'extensions/simple-queue/ui/src/views/printer-queue-tab/printer-queue-tab.stories.svelte'
  const { files } = await fingerprint(path.join(root, story), root)
  assert.ok(files.includes('extensions/simple-queue/client/src/index.ts'), 'simple-queue-client is not followed')
})
