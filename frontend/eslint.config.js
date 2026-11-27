import path from 'node:path';

import {
  applicationConfig,
  deprecatedImports,
} from '@prusa/frontend-standards/eslint/application';

import svelteConfig from './svelte.config.js';

export default [
  ...applicationConfig({
    tsconfigRootDir: import.meta.dirname,
    svelteConfig,
  }),
  { ignores: ['storybook-static/'] },
  {
    rules: {
      '@typescript-eslint/no-restricted-imports': [
        'warn',
        {
          patterns: deprecatedImports(path.resolve(import.meta.dirname, '..')),
        },
      ],
    },
  },
  // The logger is the one place that writes to the console
  // (frontend-logging.md).
  { files: ['src/lib/log/**'], rules: { 'no-console': 'off' } },
];
