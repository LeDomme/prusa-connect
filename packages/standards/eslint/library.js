// The flat config for a component library: the package a Svelte application
// and a module plugin both build their screens on.
//
//   import { libraryConfig } from '@prusa/frontend-standards/eslint/library';
//   import svelteConfig from './svelte.config.js';
//
//   export default libraryConfig({
//     tsconfigRootDir: import.meta.dirname,
//     svelteConfig,
//   });

import { defineConfig } from 'eslint/config';

import {
  baseIgnores,
  baseRuleSets,
  commonRules,
  languageOptions,
  noLiteralTextRule,
  rawControlRule,
  svelteFileParser,
  svelteRenderTagRule,
  testFileOverride,
} from './shared.js';

export { deprecatedImports } from './shared.js';

const TRANSPORT_MESSAGE =
  'The core is reached through @prusa/connect (frontend-structure.md, ADR-20260909-c7x rules 2 and 8).';

const ARBITRARY_MESSAGE =
  'A Tailwind arbitrary value. Add the value to the theme in src/styles/tokens.css and use its name (frontend-css.md).';

/**
 * @param {{ tsconfigRootDir: string, svelteConfig: unknown }} options
 * @returns {import('eslint').Linter.Config[]}
 */
export const libraryConfig = ({ tsconfigRootDir, svelteConfig }) =>
  defineConfig(
    { ignores: baseIgnores },
    ...baseRuleSets,
    languageOptions(tsconfigRootDir),
    commonRules({
      transportMessage: TRANSPORT_MESSAGE,
      arbitraryMessage: ARBITRARY_MESSAGE,
    }),
    svelteFileParser(svelteConfig),
    svelteRenderTagRule(),
    noLiteralTextRule(),
    rawControlRule(),
    testFileOverride({ restrictedGlobalsOff: true }),
  );

export default libraryConfig;
