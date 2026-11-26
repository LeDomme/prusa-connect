// The flat config for the Svelte application: the composition root that
// fills the route table and the slots.
//
//   import { applicationConfig } from '@prusa/frontend-standards/eslint/application';
//   import svelteConfig from './svelte.config.js';
//
//   export default applicationConfig({
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
  'A Tailwind arbitrary value. Add the value to the theme in the component library and use its name (frontend-css.md).';

/**
 * @param {{ tsconfigRootDir: string, svelteConfig: unknown }} options
 * @returns {import('eslint').Linter.Config[]}
 */
export const applicationConfig = ({ tsconfigRootDir, svelteConfig }) =>
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

export default applicationConfig;
