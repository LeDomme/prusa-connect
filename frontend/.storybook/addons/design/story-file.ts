// The story file an index entry was read from, relative to the repository
// root, which is how the approvals and `design status` name it. Storybook
// names it relative to the directory it runs in: `./src/…` and
// `../packages/…` from `frontend/`, and `../prusa-connect/…` from a
// Storybook that holds this repository as a submodule and runs outside it.

/** Where the Storybook runs, against this repository's root. */
export interface StoryPaths {
  /** The Storybook's working directory, relative to the repository root. */
  cwdFromRoot: string;
  /** The repository root, relative to the Storybook's working directory. */
  rootFromCwd: string;
}

/** The paths of the Storybook in this repository's own `frontend/`. */
export const FRONTEND: StoryPaths = {
  cwdFromRoot: 'frontend',
  rootFromCwd: '..',
};

/**
 * The paths `main.ts` writes into the manager's and the preview's page, or
 * those of `frontend/` on a page without them.
 */
export const storyPaths = (): StoryPaths =>
  (globalThis as { DESIGN_STORY_PATHS?: StoryPaths }).DESIGN_STORY_PATHS ??
  FRONTEND;

const normalize = (file: string): string => {
  const parts: string[] = [];
  for (const part of file.split('/')) {
    if (part === '..' && parts.length > 0 && parts.at(-1) !== '..') parts.pop();
    else if (part !== '.' && part !== '') parts.push(part);
  }
  return parts.join('/');
};

export const storyFileOf = (
  importPath: string,
  { cwdFromRoot, rootFromCwd }: StoryPaths,
): string => {
  const file = normalize(importPath);
  if (!cwdFromRoot.startsWith('..')) return normalize(`${cwdFromRoot}/${file}`);
  const prefix = `${normalize(rootFromCwd)}/`;
  return file.startsWith(prefix) ? file.slice(prefix.length) : file;
};

/** What the preview sends the Design panel after a hot update in the dev server. */
export const DESIGN_CHANGED = 'prusa-connect/design/changed';
