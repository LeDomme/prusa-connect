// The building blocks `library.js`, `application.js` and `module.js` compose
// into their own flat configs. Not published on its own: a consumer imports
// one of those three, never this file.

import { existsSync, readFileSync } from 'node:fs';
import { basename, dirname, extname, join, relative } from 'node:path/posix';

import js from '@eslint/js';
import prettier from 'eslint-config-prettier';
import svelte from 'eslint-plugin-svelte';
import globals from 'globals';
import tseslint from 'typescript-eslint';

import { noLiteralText } from './rules/no-literal-text.js';

/** Every consumer ignores its own build output and its own lockfile. */
export const baseIgnores = ['dist/', 'node_modules/', 'pnpm-lock.yaml'];

/** The rule sets every consumer type turns on, in the order they must run. */
export const baseRuleSets = [
  js.configs.recommended,
  ...tseslint.configs.strictTypeChecked,
  ...tseslint.configs.stylisticTypeChecked,
  ...svelte.configs.recommended,
  prettier,
  ...svelte.configs.prettier,
];

/**
 * Type-aware linting reads a real tsconfig through `projectService`, and
 * `tsconfigRootDir` names where that tsconfig is. It is the consumer's own
 * project root, a path on the consumer's disk that a published package
 * cannot know, so it is read from the caller rather than guessed here.
 *
 * @param {string} tsconfigRootDir
 * @returns {import('eslint').Linter.Config}
 */
export const languageOptions = (tsconfigRootDir) => ({
  languageOptions: {
    globals: { ...globals.browser, ...globals.node },
    parserOptions: {
      // typescript.md: without projectService the no-unsafe rules pass
      // everything.
      projectService: true,
      extraFileExtensions: ['.svelte'],
      tsconfigRootDir,
    },
  },
});

/**
 * @param {string} transportMessage
 */
const restrictedGlobals = (transportMessage) => [
  'error',
  { name: 'fetch', message: transportMessage },
  { name: 'EventSource', message: transportMessage },
  { name: 'XMLHttpRequest', message: transportMessage },
  { name: 'WebSocket', message: transportMessage },
];

// frontend-css.md: no arbitrary values. `p-[13px]` and `text-[#333]` are
// refused here rather than in review. The pattern is a utility name, a hyphen
// and an opening bracket.
const ARBITRARY_VALUE = String.raw`/(^|\s)[!@]?[a-z][a-z0-9:/-]*-\[/`;

// ui-integration.md: a spinner and a live failure each have a primitive. The
// lint sees the class string and the attribute; the look the primitive draws
// is not something a selector can recognise.
const ANIMATE_SPIN = String.raw`/(^|\s)[a-z0-9:-]*animate-spin(\s|$)/`;
const SPIN_MESSAGE = 'A hand-drawn spinner. Use Spinner (ui-integration.md).';
const ROLE_ALERT_MESSAGE =
  'role="alert" announces on mount. Use Alert or FaultAlert for a live failure, Card for a standing notice (ui-integration.md).';

/**
 * @param {string} arbitraryMessage
 */
const restrictedSyntax = (arbitraryMessage) => [
  'error',
  { selector: `Literal[value=${ARBITRARY_VALUE}]`, message: arbitraryMessage },
  {
    selector: `TemplateElement[value.raw=${ARBITRARY_VALUE}]`,
    message: arbitraryMessage,
  },
  {
    selector: `SvelteLiteral[value=${ARBITRARY_VALUE}]`,
    message: arbitraryMessage,
  },
  { selector: `Literal[value=${ANIMATE_SPIN}]`, message: SPIN_MESSAGE },
  {
    selector: `TemplateElement[value.raw=${ANIMATE_SPIN}]`,
    message: SPIN_MESSAGE,
  },
  { selector: `SvelteLiteral[value=${ANIMATE_SPIN}]`, message: SPIN_MESSAGE },
  {
    selector: `SvelteAttribute[key.name='role'] > SvelteLiteral[value='alert']`,
    message: ROLE_ALERT_MESSAGE,
  },
];

/**
 * The rules every consumer type turns on the same way. `transportMessage` and
 * `arbitraryMessage` differ by consumer type: the core is reached
 * differently by each, and the theme lives in a different place relative to
 * each.
 *
 * @param {{ transportMessage: string, arbitraryMessage: string }} messages
 * @returns {import('eslint').Linter.Config}
 */
export const commonRules = ({ transportMessage, arbitraryMessage }) => ({
  // ESLint reports an unused disable as a warning by default, and no script
  // here fails on a warning: an exception that outlived its raw element would
  // stay in the file unseen. An HTML comment directive in markup is read by
  // eslint-plugin-svelte's comment-directive rule, not by ESLint, so the rule
  // takes the same option.
  linterOptions: { reportUnusedDisableDirectives: 'error' },
  rules: {
    'svelte/comment-directive': [
      'error',
      { reportUnusedDisableDirectives: true },
    ],
    '@typescript-eslint/consistent-type-imports': 'error',
    '@typescript-eslint/no-explicit-any': 'error',
    '@typescript-eslint/no-non-null-assertion': 'error',
    '@typescript-eslint/no-restricted-types': [
      'error',
      {
        types: {
          object: 'Say which properties. `object` is near-useless.',
          '{}': 'Say which properties. `{}` is near-useless.',
          Function: 'Write the signature. `Function` is near-useless.',
        },
      },
    ],
    eqeqeq: ['error', 'always', { null: 'ignore' }],
    // frontend-logging.md: the console is read in screenshots and bug
    // reports. A refused build shows a failure on the screen instead.
    'no-console': 'error',
    'no-nested-ternary': 'error',
    'no-restricted-globals': restrictedGlobals(transportMessage),
    'no-restricted-syntax': restrictedSyntax(arbitraryMessage),
  },
});

/**
 * A `.svelte` file is markup with a TypeScript island inside it, so the
 * Svelte parser owns the file and hands the `<script>` block to the
 * TypeScript one. `svelteConfig` is the consumer's own `svelte.config.js`
 * default export: it carries a project-specific workaround at least once in
 * this tree (a Storybook dependency's own component compiling outside runes
 * mode), so it stays a file the consumer owns and passes in.
 *
 * @param {unknown} svelteConfig
 * @returns {import('eslint').Linter.Config}
 */
export const svelteFileParser = (svelteConfig) => ({
  files: ['**/*.svelte', '**/*.svelte.ts'],
  languageOptions: {
    parserOptions: { parser: tseslint.parser, svelteConfig },
  },
});

/**
 * `{@render snippet()}` is a call in a value position that returns void, which
 * is what this rule forbids. It is Svelte's only syntax for drawing a snippet,
 * so the rule reports every component that draws one and can report nothing
 * else in markup. `ignoreVoidReturningFunctions` does not reach it: the
 * parser hands the rule the call, not the snippet's type.
 *
 * `.svelte.ts` keeps the rule. It has no markup, so no render tag either.
 *
 * @returns {import('eslint').Linter.Config}
 */
export const svelteRenderTagRule = () => ({
  files: ['**/*.svelte'],
  rules: { '@typescript-eslint/no-confusing-void-expression': 'off' },
});

/**
 * frontend-i18n.md rule 1: every text is `t('key')`. `src/test/**` holds
 * test probes a test renders and a `.stories.svelte` file holds the sample
 * data a story shows, so their words are fixtures, never a screen, in any of
 * the three consumer types.
 *
 * @returns {import('eslint').Linter.Config}
 */
export const noLiteralTextRule = () => ({
  files: ['**/*.svelte'],
  ignores: ['src/test/**', '**/*.stories.svelte'],
  plugins: {
    'frontend-standards': { rules: { 'no-literal-text': noLiteralText } },
  },
  rules: { 'frontend-standards/no-literal-text': 'error' },
});

/**
 * ui-integration.md: a screen composes the primitives in the component
 * library and never draws a control of its own. Each message names the
 * primitive that replaces the element. Tests and stories are ignored for the
 * reason `noLiteralTextRule` gives; the library lifts the rule for its own
 * primitives, which are where these elements live.
 *
 * @returns {import('eslint').Linter.Config}
 */
export const rawControlRule = () => ({
  files: ['**/*.svelte'],
  ignores: ['src/test/**', '**/*.stories.svelte'],
  rules: {
    'svelte/no-restricted-html-elements': [
      'error',
      {
        elements: ['button'],
        message: 'Use Button, Toggle, Tile or Chip.',
      },
      {
        elements: ['input'],
        message: 'Use Input, Checkbox, Switch or Slider.',
      },
      { elements: ['select'], message: 'Use Select or Combobox.' },
      { elements: ['textarea'], message: 'Use Textarea.' },
      { elements: ['dialog'], message: 'Use Dialog, Sheet or AlertDialog.' },
      { elements: ['details', 'summary'], message: 'Use Collapsible.' },
      { elements: ['hr'], message: 'Use Separator.' },
      { elements: ['table'], message: 'Use Table.' },
      { elements: ['label'], message: 'Use Label or Field.' },
    ],
  },
});

/**
 * typescript.md: a test may use `!` after an explicit shape assertion.
 * `restrictedGlobalsOff` also lifts the transport ban in test files, which a
 * library or an application test needs to build the fake response its fake
 * SDK client is handed. A module never constructs its own transport, in a
 * test or otherwise (ADR-20260911-4p6 rules 18 and 20), so its tests keep the ban.
 *
 * @param {{ restrictedGlobalsOff?: boolean }} [options]
 * @returns {import('eslint').Linter.Config}
 */
export const testFileOverride = ({ restrictedGlobalsOff = false } = {}) => ({
  files: ['**/*.test.ts', 'src/test/**/*.ts'],
  rules: {
    '@typescript-eslint/no-non-null-assertion': 'off',
    ...(restrictedGlobalsOff ? { 'no-restricted-globals': 'off' } : {}),
  },
});

const escapeRegex = (text) => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/**
 * The `no-restricted-imports` patterns, which its typescript-eslint twin
 * takes too, for the components a designer marked deprecated in
 * `<rootDir>/design/deprecations.json`, keyed by file relative to `rootDir`.
 * No manifest, no patterns; an entry without a `use` string gives none.
 *
 * The rule compares the specifier as written, so each component gets two:
 * - its file, by its directory and name at the end of the specifier
 *   (`../button/button.svelte`), from any depth;
 * - the name it is re-exported under as `default as <Name>` in the
 *   `index.ts` beside it, imported from any barrel on the way up to its
 *   package (`../../primitives`, `@prusa/connect-ui/primitives`) or from the
 *   package itself.
 *
 * ponytail: matched by name, not resolved, so another `button/button.svelte`
 * or another `Button` behind a `button` or `primitives` barrel warns too; an
 * import resolver plugin would tell them apart.
 *
 * @param {string} rootDir
 * @returns {{ regex: string, importNames?: string[], message: string }[]}
 */
export const deprecatedImports = (rootDir) => {
  const manifest = join(rootDir, 'design/deprecations.json');
  if (!existsSync(manifest)) return [];
  /** @type {unknown} */
  const deprecations = JSON.parse(readFileSync(manifest, 'utf8'));
  if (typeof deprecations !== 'object' || deprecations === null) return [];
  return Object.entries(deprecations).flatMap(([file, entry]) => {
    // Every lint run loads this config, so an entry without a replacement
    // is skipped rather than failing them all.
    const use = entry?.use;
    if (typeof use !== 'string') return [];
    const message = `Deprecated in design: use ${use}`;
    const dir = dirname(file);
    const ext = extname(file);
    const name = basename(file, ext);
    const patterns = [
      {
        regex: `(^|/)${escapeRegex(`${basename(dir)}/${name}`)}(${escapeRegex(ext)})?$`,
        message,
      },
    ];
    const index = join(rootDir, dir, 'index.ts');
    const exported = existsSync(index)
      ? new RegExp(
          `default\\s+as\\s+(\\w+)\\s*\\}\\s*from\\s*['"]\\./${escapeRegex(name)}(${escapeRegex(ext)})?['"]`,
        ).exec(readFileSync(index, 'utf8'))?.[1]
      : undefined;
    if (exported === undefined) return patterns;

    let pkg = dir;
    while (pkg !== '.' && !existsSync(join(rootDir, pkg, 'package.json'))) {
      pkg = dirname(pkg);
    }
    const packageJson = join(rootDir, pkg, 'package.json');
    const barrels = [
      ...relative(pkg, dir)
        .split('/')
        .filter((segment) => segment !== '' && segment !== 'src')
        .map((segment) => `(^|/)${escapeRegex(segment)}$`),
      ...(existsSync(packageJson)
        ? [
            `^${escapeRegex(JSON.parse(readFileSync(packageJson, 'utf8')).name)}$`,
          ]
        : []),
    ];
    return [
      ...patterns,
      { regex: barrels.join('|'), importNames: [exported], message },
    ];
  });
};
