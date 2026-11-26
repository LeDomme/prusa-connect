import { describe, expect, it } from 'vitest';

import { json } from '../../src/test/stub';
import { resourcesFor, type StoryModule } from './modules';

const moduleNamed = (name: string): StoryModule => ({
  name,
  resource: { kind: 'asset', name },
});

const registry = [
  moduleNamed('a'),
  moduleNamed('b'),
  moduleNamed('fleet-stats'),
];

const core = { kind: 'storage', name: 'local' };

const namesIn = async (answer: Response | Error): Promise<string[]> => {
  if (answer instanceof Error) throw answer;
  const { items } = (await answer.json()) as { items: { name: string }[] };

  return items.map(({ name }) => name);
};

describe('resourcesFor', () => {
  const listing = (): Response =>
    json(200, { items: [core, moduleNamed('a').resource] });

  it('keeps no module for core only', async () => {
    const answer = await resourcesFor('', registry, listing());
    if (answer instanceof Error) throw answer;
    const { items } = (await answer.json()) as { items: { kind: string }[] };

    expect(items).toEqual([core]);
    expect(items.some(({ kind }) => kind === 'asset')).toBe(false);
  });

  it('installs every registered module for all', async () => {
    expect(
      await namesIn(await resourcesFor('all', registry, listing())),
    ).toEqual(['local', 'a', 'b', 'fleet-stats']);
  });

  it('keeps the named modules and drops the rest', async () => {
    expect(
      await namesIn(await resourcesFor('fleet-stats,b', registry, listing())),
    ).toEqual(['local', 'b', 'fleet-stats']);
  });

  it('reads names separated by spaces, as a URL carries them', async () => {
    expect(
      await namesIn(await resourcesFor('fleet-stats b', registry, listing())),
    ).toEqual(['local', 'b', 'fleet-stats']);
  });

  it('ignores names no module registers', async () => {
    expect(
      await namesIn(await resourcesFor('nope,b', registry, listing())),
    ).toEqual(['local', 'b']);
  });

  it('keeps the listing’s own item for a module it already lists', async () => {
    const disabled = { kind: 'asset', name: 'fleet-stats', enabled: false };
    const answer = await resourcesFor(
      'all',
      registry,
      json(200, { items: [disabled] }),
    );
    if (answer instanceof Error) throw answer;

    expect(((await answer.json()) as { items: unknown[] }).items).toEqual([
      disabled,
      moduleNamed('a').resource,
      moduleNamed('b').resource,
    ]);
  });

  it('passes a failed listing through', async () => {
    const failed = json(500, { code: 'internal' });

    expect(await resourcesFor('all', registry, failed)).toBe(failed);
  });
});
