// Proves the patterns catch a deprecated component however it is imported:
// by its file from any depth, and by its name through a barrel or the
// package, while its neighbours behind the same barrel stay clean.

import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { describe, it } from 'node:test';

import { Linter } from 'eslint';

import { deprecatedImports } from '../eslint/shared.js';

const root = mkdtempSync(join(tmpdir(), 'deprecated-imports-'));
const write = (file, text) => {
  mkdirSync(join(root, file, '..'), { recursive: true });
  writeFileSync(join(root, file), text);
};
write('packages/svelte/package.json', '{"name": "@prusa/connect-ui"}');
write(
  'packages/svelte/src/primitives/button/index.ts',
  "export { default as Button } from './button.svelte';\n",
);
write(
  'design/deprecations.json',
  JSON.stringify({
    'packages/svelte/src/primitives/button/button.svelte': {
      use: 'packages/svelte/src/primitives/link/link.svelte',
      at: '2026-11-24',
    },
  }),
);

const warnings = (code) =>
  new Linter().verify(code, [
    {
      rules: {
        'no-restricted-imports': [
          'warn',
          { patterns: deprecatedImports(root) },
        ],
      },
    },
  ]);

describe('deprecatedImports', () => {
  it('warns on the file, the barrels and the package', () => {
    for (const code of [
      "import Button from '../button/button.svelte';",
      "import Button from '../../primitives/button/button.svelte';",
      "import { Button } from '../../primitives';",
      "import { Button } from '../button';",
      "import { Button } from '@prusa/connect-ui/primitives';",
      "import { Button } from '@prusa/connect-ui';",
    ]) {
      const [message] = warnings(code);
      assert.match(
        message?.message ?? '',
        /Deprecated in design: use packages\/svelte\/src\/primitives\/link\/link\.svelte$/,
        code,
      );
    }
  });

  it('leaves the neighbours behind the same barrel alone', () => {
    for (const code of [
      "import { Card } from '@prusa/connect-ui/primitives';",
      "import { Button } from 'bits-ui';",
      "import Card from '../card/card.svelte';",
    ]) {
      assert.deepEqual(warnings(code), [], code);
    }
  });

  it('is empty without a manifest', () => {
    assert.deepEqual(deprecatedImports(tmpdir()), []);
  });

  it('skips a malformed manifest or entry instead of throwing', () => {
    const broken = mkdtempSync(join(tmpdir(), 'deprecated-imports-'));
    mkdirSync(join(broken, 'design'));
    const manifest = join(broken, 'design/deprecations.json');
    for (const text of ['null', '[]', '"x"', '{"a/b.svelte": null}']) {
      writeFileSync(manifest, text);
      assert.deepEqual(deprecatedImports(broken), [], text);
    }
    writeFileSync(
      manifest,
      JSON.stringify({
        'a/b.svelte': { at: '2026-11-26' },
        'c/d.svelte': { use: 'e/f.svelte' },
      }),
    );
    assert.deepEqual(
      deprecatedImports(broken).map((p) => p.message),
      ['Deprecated in design: use e/f.svelte'],
    );
  });
});
