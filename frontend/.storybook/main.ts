// The Storybook for the whole product: the app's stories under `src/` and the
// component library's under `packages/svelte/src/`, on one page.
//
// `vite.config.ts` is read for its plugins, with `importMap()` swapped for one
// that writes the map into Storybook's preview document, `iframe.html`. The
// map is what a module's built bundle, served from `modules/<name>/`, loads
// Svelte and the library through, so it draws with the page's own runtime.
//
// The dev server serves each module in `MODULES` from source instead, so an
// edit to it shows without a rebuild (`served-modules.ts`).
//
// The library is read from source in both the dev server and the build,
// through the same aliases `importMap()` sets in the app's dev server.
// Library stories import their components by relative path, so app code that
// imports `@prusa/connect-ui` gets the same module, not a copy from
// `dist/`.
//
// Three extensions' stories are drawn here too, so one Storybook shows the
// whole product. Their sources import their own dependencies from their own
// `ui/node_modules`, which `make frontend-storybook` installs. Their
// `@prusa/connect-ui` imports go through the same aliases as the app's, and
// the Storybook addon they import is this app's.
//
// The library and the app each install their own `svelte`. The page still loads
// one runtime, because `svelte()` from vite-plugin-svelte puts `svelte` in
// `resolve.dedupe`. Two runtimes would fail with "Function called outside
// component initialization".
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import type { StorybookConfig } from '@storybook/svelte-vite';
import { logger } from 'storybook/internal/node-logger';
import {
  mergeConfig,
  type Plugin,
  type PluginOption,
  searchForWorkspaceRoot,
} from 'vite';

import { SERVED_MODULES } from './module-list.ts';
import { servedModules } from './served-modules.ts';
import { storageThumbnails } from '../test/story/storage-thumbnails.ts';

import {
  extensionClientAliases,
  importMap,
  sourceAliases,
} from '../vite/import-map.ts';

const here = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(here, '../..');
const posix = (file: string): string => file.split(path.sep).join('/');

// Where this Storybook runs against the repository root, for the Design panel
// and the pill to name a story file as the design tools do. A Storybook that
// spreads this config and runs elsewhere gets its own.
const storyPathsScript = `<script>globalThis.DESIGN_STORY_PATHS = ${JSON.stringify(
  {
    cwdFromRoot: posix(path.relative(repoRoot, process.cwd())),
    rootFromCwd: posix(path.relative(process.cwd(), repoRoot)),
  },
)};</script>`;

// The library's own Storybook measured a build at 5 minutes 37 with the props
// tables against 10 seconds without, so a build leaves them out and the dev
// server reads them one story at a time.
const building = process.argv.includes('build');

// The modules an app story can install (`parameters.modules` in the preview).
const MODULES = servedModules(
  SERVED_MODULES.map((m) => ({ ...m, ui: path.resolve(here, '../..', m.ui) })),
  building,
);

// Written by `design status --write` before a build. Each entry of the build
// gains the tag `status:<status>` from it, which the sidebar's tag filter
// offers.
const DESIGN_STATUS = path.join(here, 'generated/design-status.json');

const designStatuses = (): Map<string, string> => {
  if (!existsSync(DESIGN_STATUS)) {
    logger.warn(
      `no design status tags: ${path.relative(process.cwd(), DESIGN_STATUS)} is missing, run design status --write`,
    );
    return new Map();
  }
  // Written by the design CLI in this shape.
  const written = JSON.parse(readFileSync(DESIGN_STATUS, 'utf8')) as Record<
    string,
    { status: string }
  >;
  return new Map(
    Object.entries(written).map(([file, { status }]) => [file, status]),
  );
};

const isImportMap = (plugin: PluginOption): boolean =>
  typeof plugin === 'object' &&
  plugin !== null &&
  'name' in plugin &&
  plugin.name === 'prusa-connect:import-map';

const filePreviews: Plugin = {
  name: 'prusa-connect:storybook-file-thumbnails',
  configureServer(server) {
    server.middlewares.use(
      '/api/v1/storages/local/thumbnail',
      (request, response, next) => {
        const url = new URL(request.url ?? '', 'http://localhost');
        const preview = storageThumbnails.get(
          url.searchParams.get('path') ?? '',
        );
        if (request.method !== 'GET' || preview === undefined) {
          next();
          return;
        }
        response.setHeader('Content-Type', 'image/png');
        response.end(
          readFileSync(path.join(here, '../test/story/assets', preview.file)),
        );
      },
    );
  },
};

// An extension's story files import the Storybook addon from this app, so
// the page holds one copy of it, not one per extension's own install.
const extensionStorybookImports: Plugin = {
  name: 'prusa-connect:extension-storybook-imports',
  async resolveId(source, importer, options) {
    if (!source.startsWith('@storybook/') || importer === undefined) {
      return null;
    }
    if (
      !importer.startsWith(path.resolve(here, '../../extensions') + path.sep)
    ) {
      return null;
    }
    return this.resolve(source, path.join(here, 'preview.ts'), {
      ...options,
      skipSelf: true,
    });
  },
};

const config: StorybookConfig & {
  managerEntries: (entries?: string[]) => string[];
  managerHead: (head?: string) => string;
  previewHead: (head?: string) => string;
} = {
  // A self-hosted open-source tool must not phone home on every build.
  core: { disableTelemetry: true },
  stories: [
    '../src/**/*.mdx',
    '../src/**/*.stories.svelte',
    '../../packages/svelte/src/**/*.stories.svelte',
    '../../extensions/{threemf,server-slicing,simple-queue}/ui/src/**/*.stories.svelte',
  ],
  addons: ['@storybook/addon-svelte-csf', '@storybook/addon-docs'],
  // The dev server leaves the tags out: read once at its start, they would go
  // stale, while its Design panel and pill follow every edit.
  experimental_indexers: (indexers = []) => {
    if (!building) return indexers;
    const status = designStatuses();
    return indexers.map((indexer) => ({
      ...indexer,
      createIndex: async (fileName, options) => {
        const tag = status.get(posix(path.relative(repoRoot, fileName)));
        const entries = await indexer.createIndex(fileName, options);
        return tag === undefined
          ? entries
          : entries.map((entry) => ({
              ...entry,
              tags: [...(entry.tags ?? []), `status:${tag}`],
            }));
      },
    }));
  },
  // An addon directory's `manager` file is resolved as JavaScript only, so the
  // TypeScript one is named here, as a preset would name it.
  managerHead: (head?: string) => `${head ?? ''}${storyPathsScript}`,
  previewHead: (head?: string) => `${head ?? ''}${storyPathsScript}`,
  managerEntries: (entries: string[] = []) => [
    ...entries,
    path.join(here, 'addons/modules/manager.tsx'),
    path.join(here, 'addons/design/manager.tsx'),
  ],
  framework: {
    name: '@storybook/svelte-vite',
    options: { docgen: !building },
  },
  // A build serves each module's bundle, which `pnpm run build` in its `ui/`
  // writes and `make frontend-storybook` runs first.
  staticDirs: [
    { from: '../public/img', to: '/img' },
    { from: '../public/fonts', to: '/fonts' },
    ...MODULES.staticDirs,
    // The hosted Design panel reads each story file's status from here.
    ...(building && existsSync(DESIGN_STATUS)
      ? [{ from: './generated', to: '/design' }]
      : []),
  ],
  viteFinal: async (vite) =>
    mergeConfig(
      {
        ...vite,
        plugins: [
          filePreviews,
          extensionStorybookImports,
          ...(vite.plugins ?? [])
            .flat()
            .map((p) =>
              isImportMap(p) ? importMap({ page: 'iframe.html' }) : p,
            ),
          ...MODULES.plugins,
          // Imported only for the dev server, so a build needs no install
          // of tools/design. Without approvals given, a story is raw,
          // concept or deprecated.
          ...(building
            ? []
            : [(await import('./design-server.ts')).designServer()]),
        ],
      },
      {
        resolve: {
          alias: [
            ...sourceAliases(),
            ...extensionClientAliases(path.resolve(here, '../../extensions')),
            {
              find: '@prusa/connect-ui/app.css',
              replacement: path.resolve(
                here,
                '../../packages/svelte/src/styles/app.css',
              ),
            },
            {
              find: '@prusa/connect-ui/tokens.css',
              replacement: path.resolve(
                here,
                '../../packages/svelte/src/styles/tokens.css',
              ),
            },
          ],
          dedupe: ['svelte'],
        },
        // A list here replaces Vite's default of the workspace root, which
        // holds Storybook's own runtime, so the root is named again.
        server: {
          fs: {
            allow: [
              searchForWorkspaceRoot(process.cwd()),
              path.resolve(here, '../../packages'),
              path.resolve(here, '../../extensions'),
            ],
          },
        },
      },
    ),
};

export default config;
