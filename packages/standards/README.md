# @prusa/frontend-standards

The engineering standards every Prusa frontend lints, formats, type-checks and
reads against: three ESLint flat configs, one Prettier configuration, one base
`tsconfig.json`, and the prose rules that go with them. A second application,
a component library and a module plugin all read the same rules from here
instead of each keeping its own copy.

## What is in it

| subpath                | holds                                                                  |
| ---------------------- | ---------------------------------------------------------------------- |
| `./eslint/library`     | the flat config for a component library                                |
| `./eslint/application` | the flat config for a Svelte application                               |
| `./eslint/module`      | the flat config for a module plugin's own tree                         |
| `./prettier`           | the one Prettier configuration                                         |
| `./tsconfig.json`      | the base compiler options every consumer extends                       |
| `rules/`               | the frontend `.agents/rules/` prose, published as files, not a subpath |

Every one of the three ESLint configs carries `no-literal-text`, the rule
behind `frontend-i18n.md` rule 1: no English string may be written into a
component.

They also refuse a raw control such as `<button>`, `<input>` or `<label>`
through `svelte/no-restricted-html-elements`. They refuse a hand-drawn
`animate-spin` and a `role="alert"` attribute too. Each message names the
primitive to use (`ui-integration.md`). An exception is an
`<!-- eslint-disable-next-line svelte/no-restricted-html-elements -- reason -->`
above the element, and a disable that excuses nothing is an error. A component
library lifts the element rule for its own primitives folder, where those
elements live.

## Using the ESLint config

Each config is a function. It needs two things from the consumer, because
both name paths on the consumer's own disk that a published package cannot
know: where its `tsconfig.json` lives, and its own `svelte.config.js`.

```js
// eslint.config.js
import { libraryConfig } from '@prusa/frontend-standards/eslint/library';

import svelteConfig from './svelte.config.js';

export default libraryConfig({
  tsconfigRootDir: import.meta.dirname,
  svelteConfig,
});
```

An application writes the same shape against `eslint/application`, and a
module plugin's own tree against `eslint/module`. The three differ in two
places only: how each reaches the core (`@prusa/connect` directly, or
`host.client` for a module, per ADR-20260911-4p6 rules 18 and 20), and where the
Tailwind theme lives relative to each. Everything else, the banned-type rules,
`no-console`, the arbitrary-value ban, the i18n gate, is one rule set.

The returned array is a real flat config, so a consumer adds more to it the
way flat config already supports, by appending further config objects:

```js
export default [
  ...libraryConfig({ tsconfigRootDir: import.meta.dirname, svelteConfig }),
  { ignores: ['storybook-static/'] },
];
```

`eslint/library` and `eslint/application` also export
`deprecatedImports(rootDir)`. It reads `<rootDir>/design/deprecations.json`,
the components a designer marked deprecated, and returns the
`no-restricted-imports` patterns that warn on a new import of one:

```js
{
  rules: {
    'no-restricted-imports': [
      'warn',
      { patterns: deprecatedImports(path.resolve(import.meta.dirname, '..')) },
    ],
  },
},
```

A later config object that sets `no-restricted-imports` for some files
replaces these patterns there, as flat config replaces any rule's options.

## Using the Prettier config

```json
{
  "prettier": "@prusa/frontend-standards/prettier"
}
```

No `.prettierrc` of the consumer's own. A consumer whose components use
Tailwind's custom `@utility` names and wants those sorted too adds one small
file, because a bare `.prettierrc*` file replaces the `"prettier"` field
entirely rather than merging with it:

```js
// .prettierrc.js
import config from '@prusa/frontend-standards/prettier';

export default { ...config, tailwindStylesheet: './src/styles/tokens.css' };
```

## Using the tsconfig

```json
{
  "extends": "@prusa/frontend-standards/tsconfig.json",
  "compilerOptions": {
    "types": ["svelte", "vite/client", "node"],
    "paths": {
      "@prusa/connect": ["../packages/typescript/src/index.ts"]
    }
  },
  "include": ["src", "*.config.ts", "*.config.js"]
}
```

`paths` and `include` stay in the consumer's own `tsconfig.json`: they name
paths on the consumer's disk, one per project, so a shared base cannot hold
them.

## Depending on it from a checkout

Nothing publishes this package yet. `frontend/`, `packages/svelte`, each `examples/*/ui`, and a module that `new-module
-checkout` scaffolds all name it by version in `package.json`. Each overrides
that in its own `pnpm-workspace.yaml`:

```yaml
overrides:
  '@prusa/connect': 'link:../packages/typescript'
  '@prusa/connect-ui': 'link:../packages/svelte'
  '@prusa/frontend-standards': 'file:../packages/standards'
```

The path is relative to that consumer. The quoting is Prettier's own:
`pnpm-workspace.yaml` is itself formatted by the `format` script, and
Prettier's YAML printer single-quotes a plain string. A key or value written
double-quoted turns into a `format:check` failure.

The SDK and the component library are `link:`ed: a symlink, so an edit shows
up the moment the consumer next reads the file. Standards is `file:`d
instead, a copy, because linking it here would make its own ESLint config
resolve `eslint` from `packages/standards/node_modules` rather than from the
consumer's. The trade-off is that `pnpm install`, even `pnpm install
--force`, does not refresh a `file:` copy once it is in place. An edit to
this package reaches a checkout consumer only after removing that consumer's
`node_modules` (or at least `node_modules/@prusa/frontend-standards` and its
entry under `node_modules/.pnpm`) and reinstalling.

A consumer that adds its own `.prettierrc.js` (see
[Using the Prettier config](#using-the-prettier-config)) needs it in its
`tsconfig.json`'s `include`. The file is typed against this package's
`prettier.d.ts`, and TypeScript only checks a file `include` names.

## Using the rules

`rules/` carries the nine `.agents/rules/` files that describe frontend work
specifically: CSS and Tailwind, i18n, logging, file structure, Svelte 5,
Svelte data flow, Svelte testing and performance, TypeScript strictness, and
UI integration. A consuming repository reads them from
`node_modules/@prusa/frontend-standards/rules/` directly, or copies them into
its own `.agents/rules/` where an agent already looks.

The rules that stayed behind are specific to this repository rather than to a
frontend: process rules like `adversarial-review.md` and
`engineering-standards.md`, this repository's own `git-discipline.md` and
`changelog-discipline.md`, `go-standards.md` and `docker-services.md`, which
name no frontend concern at all, and `writing-style.md`, which governs every
comment and document in this repository, Go included, not only its frontend.
`security-supply-chain.md` also stayed: its pnpm rules apply to this very
package, but they describe how a dependency enters this repository's tree,
not a rule a consuming repository's frontend code follows.
