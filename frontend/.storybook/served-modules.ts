// The module bundles a Storybook serves beside its preview document, at
// `/modules/<at>/`, where a story's resources listing points each module's
// `entry_url`. A Storybook that spreads this app's config passes its own
// modules here, so every module its preview registers is served.
//
// A build serves each module's built bundle, `<ui>/dist/ui`, which the
// module's own build writes first. The dev server serves a module marked
// `fromSource` from `<ui>/src` instead, so an edit to it shows without a
// rebuild: `index.js` redirects to `src/index.ts`, which the app's plugins
// compile, and its stylesheet is scoped to module markup as `moduleBuild()`
// scopes it. Any other module is its built bundle in the dev server too.
import path from 'node:path';

import type { Plugin } from 'vite';

import { scopedToTheModule } from '../../packages/svelte/build/scope.ts';

export interface ServedModule {
  /** The path under `/modules/` its `index.js` and `locales/` are served at. */
  readonly at: string;
  /** Its `ui/` directory, holding `src/` and the built `dist/ui/`. */
  readonly ui: string;
  /** Whether the dev server compiles it from `src/` instead. */
  readonly fromSource: boolean;
}

interface StaticDir {
  readonly from: string;
  readonly to: string;
}

export const servedModules = (
  modules: readonly ServedModule[],
  building: boolean,
): { staticDirs: StaticDir[]; plugins: Plugin[] } => {
  const live = new Map<string, string>(
    building
      ? []
      : modules
          .filter(({ fromSource }) => fromSource)
          .map(({ at, ui }): [string, string] => [at, ui]),
  );

  // Storybook serves `staticDirs` ahead of Vite, so a module from source
  // names only its catalogues here and leaves `index.js` to the redirect.
  const staticDirs = modules.map(({ at, ui }) =>
    live.has(at)
      ? { from: path.join(ui, 'src/locales'), to: `/modules/${at}/locales` }
      : { from: path.join(ui, 'dist/ui'), to: `/modules/${at}` },
  );

  const fromSource: Plugin = {
    name: 'prusa-connect:storybook-modules-from-source',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        const at = /^\/modules\/(.+)\/index\.js$/.exec(
          request.url?.split('?')[0] ?? '',
        )?.[1];
        const ui = at === undefined ? undefined : live.get(at);
        if (ui === undefined) {
          next();
          return;
        }
        response.statusCode = 302;
        response.setHeader('Location', `/@fs${path.join(ui, 'src/index.ts')}`);
        response.end();
      });
    },
  };

  // Tailwind's transform is `pre` too, and `pre` plugins run in list order,
  // so this one, placed after the app's plugins, sees Tailwind's output, and
  // `pre` still runs it before Vite turns the CSS into a module. The host's
  // own stylesheets are left alone.
  const scopeStyles: Plugin = {
    name: 'prusa-connect:storybook-scope-module-styles',
    enforce: 'pre',
    apply: 'serve',
    transform: {
      filter: { id: /\.css(?:$|\?)/ },
      handler: (css, id) =>
        [...live.values()].some((ui) =>
          id.startsWith(path.join(ui, 'src') + path.sep),
        )
          ? scopedToTheModule(css)
          : null,
    },
  };

  return { staticDirs, plugins: [fromSource, scopeStyles] };
};
