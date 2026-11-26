import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

export const git = (cwd: string, ...args: string[]) =>
  execFileSync('git', args, { cwd, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim()

export const STORY_A = 'packages/svelte/src/a/a.stories.svelte'
export const STORY_B = 'packages/svelte/src/b/b.stories.svelte'

// The least a checkout needs for `storyFiles` and `fingerprint`: the
// Storybook's story globs and preview, the global stylesheets, the theme, the
// two linked packages, an English catalogue and two stories.
const FILES: Record<string, string> = {
  'frontend/.storybook/main.ts': "const config = {\n  stories: ['../../packages/svelte/src/**/*.stories.svelte'],\n}\n",
  'frontend/.storybook/preview.ts': 'export default {}\n',
  'frontend/.storybook/extensions.css': '',
  'frontend/src/styles/global.css': '',
  'packages/svelte/src/styles/app.css': '',
  'packages/svelte/src/styles/index.css': '',
  'packages/svelte/.storybook/preview.css': '',
  'packages/svelte/src/styles/tokens.css': ':root {\n  --a: red;\n}\n',
  'packages/svelte/package.json': '{ "name": "@fixture/svelte", "exports": {} }\n',
  'packages/typescript/package.json': '{ "name": "@fixture/typescript", "exports": {} }\n',
  'packages/svelte/locales/en.json': '{ "a.title": "A" }\n',
  [STORY_A]: "<script>\n  import A from './a.svelte'\n</script>\n",
  'packages/svelte/src/a/a.svelte': '<p>a</p>\n',
  [STORY_B]: "<script>\n  import B from './b.svelte'\n</script>\n",
  'packages/svelte/src/b/b.svelte': '<p>b</p>\n',
  'design/deprecations.json': '{}\n'
}

// A clone of a bare origin whose main holds the files above. Hooks and
// signing are switched off so the user's global git config does not leak in.
export function designRepo(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'design-repo-'))
  const origin = path.join(dir, 'origin.git')
  const work = path.join(dir, 'work')
  git(dir, 'init', '-q', '--bare', '-b', 'main', origin)
  git(dir, 'clone', '-q', origin, work)
  fs.mkdirSync(path.join(dir, 'hooks'))
  for (const [k, v] of [
    ['user.name', 'Designer'],
    ['user.email', 'designer@example.com'],
    ['commit.gpgsign', 'false'],
    ['core.hooksPath', path.join(dir, 'hooks')]
  ]) {
    git(work, 'config', k!, v!)
  }
  git(work, 'switch', '-q', '-c', 'main')
  for (const [file, body] of Object.entries(FILES)) {
    fs.mkdirSync(path.dirname(path.join(work, file)), { recursive: true })
    fs.writeFileSync(path.join(work, file), body)
  }
  git(work, 'add', '-A')
  git(work, 'commit', '-q', '-m', 'chore: seed')
  git(work, 'push', '-q', '-u', 'origin', 'main')
  return work
}
