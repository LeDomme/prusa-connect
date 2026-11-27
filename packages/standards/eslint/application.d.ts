import type { Linter } from 'eslint';

/** The `no-restricted-imports` patterns for the components `<rootDir>/design/deprecations.json` lists. */
export declare const deprecatedImports: (
  rootDir: string,
) => { regex: string; importNames?: string[]; message: string }[];

export declare const applicationConfig: (options: {
  tsconfigRootDir: string;
  svelteConfig: unknown;
}) => Linter.Config[];

export default applicationConfig;
