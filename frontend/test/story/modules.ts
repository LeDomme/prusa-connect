// The modules an app story can install, and which of them the toolbar's
// `modules` global turns on. The global is `all`, or the names of the modules
// that are on, apart with commas or spaces; a value that names none of them,
// such as the empty string, is the core alone. Storybook drops a comma and an
// empty value from a global it reads from the URL, so the toolbar writes
// spaces, and `none` for the core alone: `globals=modules:fleet-stats+b`.
import { createContext } from 'svelte';

import fixtures from '../ux/fixtures/api.json';
import { type Answer, json } from '../../src/test/stub';
import type { Overrides } from './api';

export interface StoryModule {
  readonly name: string;
  /** One item of `GET /api/v1/resources`, as `module add` leaves it. */
  readonly resource: unknown;
}

export interface ModuleSelection {
  readonly selection: string;
  readonly modules: readonly StoryModule[];
}

export const [selectedModules, provideModules] =
  createContext<ModuleSelection>();

const RESOURCES = 'GET /api/v1/resources';

/** The listing of the story core, before any module is installed. */
export const coreResources = (): Response =>
  json(200, JSON.parse(fixtures[RESOURCES].body));

const nameOf = (item: unknown): unknown =>
  typeof item === 'object' && item !== null && 'name' in item
    ? item.name
    : undefined;

/**
 * The resources listing with the selected modules installed and the other
 * registered ones taken out. An item no module registers stays as it is, and a
 * module the listing already holds keeps the listing's own item.
 */
export const resourcesFor = async (
  selection: string,
  modules: readonly StoryModule[],
  listing: Answer,
): Promise<Answer> => {
  if (listing instanceof Error || listing.status !== 200) return listing;
  const named = new Set(selection.split(/[\s,]+/));
  const on = (name: string): boolean => selection === 'all' || named.has(name);
  const off = new Set<unknown>(
    modules.filter(({ name }) => !on(name)).map(({ name }) => name),
  );
  // The listing is the story core's own fixture, so only its shape is named.
  const body = (await listing.clone().json()) as { items?: unknown[] };
  const kept = (body.items ?? []).filter((item) => !off.has(nameOf(item)));
  const listed = new Set(kept.map(nameOf));

  return json(200, {
    ...body,
    items: [
      ...kept,
      ...modules
        .filter(({ name }) => on(name) && !listed.has(name))
        .map(({ resource }) => resource),
    ],
  });
};

/** A story's overrides, with its resources listing put through the selection. */
export const installModules = (
  overrides: Overrides,
  { selection, modules }: ModuleSelection,
): Overrides => {
  const own = overrides[RESOURCES] ?? coreResources;

  return {
    ...overrides,
    [RESOURCES]: async (query) =>
      resourcesFor(
        selection,
        modules,
        typeof own === 'function' ? await own(query) : own,
      ),
  };
};
