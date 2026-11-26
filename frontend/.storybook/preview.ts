// What every story shares: the library's theme switch, the modules switch, the
// English locale with the app's own strings registered, and the app's
// stylesheet.
import type { Decorator, Preview } from '@storybook/svelte';
import { DOCS_RENDERED } from 'storybook/internal/core-events';
import { addons } from 'storybook/preview-api';

import {
  DESIGN_CHANGED,
  storyFileOf,
  storyPaths,
} from './addons/design/story-file';

import library from '../../packages/svelte/.storybook/preview';
// After the library's preview, which brings its own Tailwind build: the app's
// sheet comes later in the cascade, so its utilities win where both define a
// class, as `.hidden` against a container-query `block`.
import '../src/styles/global.css';
import './extensions.css';
import { startLocale } from '../src/lib/i18n';
import { registerCatalogue } from '@prusa/connect-ui/i18n';

import english from '../../extensions/threemf/ui/src/locales/en.json';
import { NAMESPACE as THREEMF } from '../../extensions/threemf/ui/src/i18n';
import FleetFrame from './fleet-frame.svelte';
import ModulesFrame from './modules-frame.svelte';
import { fleetStats } from '../test/story/fleet-stats';
import type { StoryModule } from '../test/story/modules';

startLocale(['en']);
// A module's texts are merged under its namespace by the host. The threemf
// stories read them from there, so its catalogue is registered the same way.
registerCatalogue(THREEMF, 'en', english);

const withFilaments: Decorator = () => ({ Component: FleetFrame });

// The modules come from `parameters.modules`, so a Storybook that imports this
// preview can register its own. The toolbar (`addons/modules`) reads the same
// parameter.
const withModules: Decorator = (_story, context) => {
  const globals: Record<string, string | undefined> = context.globals;
  const parameters: { modules?: readonly StoryModule[] } = context.parameters;

  return {
    Component: ModulesFrame,
    props: {
      selection: globals.modules ?? 'all',
      modules: parameters.modules ?? [],
    },
  };
};

// The story file's design status as a pill in the corner. The dev server
// answers it live, as the Design panel reads it; a hosted build has the
// `status:` tags of its index. It is drawn again on every render and every hot
// update, so an edit shows without a reload. The pill is fixed and ignores the
// pointer, so the story's layout is left alone.
let shown: string | undefined;
let hosted: Promise<Map<string, string>> | undefined;

const statusOf = async (file: string): Promise<string | undefined> => {
  if (import.meta.env.DEV) {
    // eslint-disable-next-line no-restricted-globals -- the Storybook's own design server, not the core
    const response = await fetch('/__design/status');
    // The design server answers in this shape.
    const answer = (await response.json()) as {
      files?: Record<string, { status: string }>;
    };
    return answer.files?.[file]?.status;
  }
  // eslint-disable-next-line no-restricted-globals -- Storybook's own index, not the core
  hosted ??= fetch('./index.json')
    // Storybook writes its index in this shape.
    .then(
      (response) =>
        response.json() as Promise<{
          entries: Record<string, { importPath: string; tags?: string[] }>;
        }>,
    )
    .then(
      ({ entries }) =>
        new Map(
          Object.values(entries).flatMap(({ importPath, tags = [] }) => {
            const tag = tags.find((t) => t.startsWith('status:'));
            return tag
              ? [[storyFileOf(importPath, storyPaths()), tag.slice(7)] as const]
              : [];
          }),
        ),
    );
  return (await hosted).get(file);
};

const drawStatus = async (): Promise<void> => {
  const file = shown;
  const status =
    file === undefined
      ? undefined
      : await statusOf(file).catch(() => undefined);
  // A later story's answer may have come first; it owns the pill.
  if (file !== shown) return;
  let pill = document.getElementById('design-status');
  if (status === undefined) {
    pill?.remove();
    return;
  }
  if (pill === null) {
    pill = document.createElement('div');
    pill.id = 'design-status';
    pill.style.cssText =
      'position:fixed;inset-block-start:4px;inset-inline-end:4px;z-index:2147483647;pointer-events:none;padding:1px 8px;border-radius:999px;font:11px/1.5 system-ui,sans-serif;background:CanvasText;color:Canvas;opacity:.7';
    document.body.append(pill);
  }
  pill.textContent = status;
};

const withDesignStatus: Decorator = (story, context) => {
  const parameters: { fileName?: string } = context.parameters;
  shown =
    parameters.fileName === undefined
      ? undefined
      : storyFileOf(parameters.fileName, storyPaths());
  void drawStatus();
  return story();
};

// An MDX page runs no decorator, so the last story's pill is taken down.
addons.getChannel().on(DOCS_RENDERED, () => {
  shown = undefined;
  void drawStatus();
});

// A component a story imports is swapped in place on an edit, without a new
// render, so a hot update redraws the pill and tells the Design panel.
import.meta.hot?.on('vite:afterUpdate', () => {
  void drawStatus();
  addons.getChannel().emit(DESIGN_CHANGED);
});

const preview: Preview = {
  ...library,
  decorators: [
    ...(library.decorators as Decorator[]),
    withFilaments,
    withModules,
    withDesignStatus,
  ],
  initialGlobals: { ...library.initialGlobals, modules: 'all' },
  // The toolbar entry is the `addons/modules` manager addon, so no `toolbar`
  // is declared here.
  globalTypes: {
    ...library.globalTypes,
    modules: { description: 'Modules installed in the app stories' },
  },
  parameters: {
    modules: [fleetStats],
    // The narrowest phone the app is checked at.
    viewport: {
      options: {
        phone: {
          name: 'Phone, 390 wide',
          styles: { width: '390px', height: '844px' },
          type: 'mobile',
        },
      },
    },
    options: {
      storySort: {
        order: ['Tour', 'Screens', 'Parts', 'Shell', 'Primitives'],
      },
    },
  },
};

export default preview;
