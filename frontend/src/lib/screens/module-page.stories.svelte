<!--
  A page an installed module adds, at `/m/<namespace>/<path>`. States it
  branches on: the page of fleet-stats, which the modules toolbar installs; no
  such page on this server, with every module off; and a module the operator's
  key keeps off; and "opening" while the loader waits for its own read of the
  module listing.
  Omitted Failed: a failed module listing leaves the loader nothing to load, so the page says it has no such page, as the No such page story shows.
  Omitted Refused: the module listing never refuses a signed-in person, it lists only what they may see.
-->
<script module lang="ts">
  import { defineMeta } from '@storybook/addon-svelte-csf';

  import AppAt from '../../../test/story/app-at.svelte';
  import { FLEET_STATS_NAMESPACE as NAMESPACE } from '../../../test/story/fleet-stats';
  import { type Overrides, pending } from '../../../test/story/api';
  import { coreResources } from '../../../test/story/modules';
  import { json } from '../../test/stub';

  const { Story } = defineMeta({ title: 'Screens/Module page' });

  const PATH = `/m/${NAMESPACE}/fleet-stats`;

  const RESOURCES = 'GET /api/v1/resources';

  // The frame reads the listing before it draws a screen, and the loader
  // reads it again when the page opens. Only the second read is held, since
  // holding the first leaves the whole app blank.
  const firstOnly = (): Overrides[string] => {
    let reads = 0;

    return () => (reads++ === 0 ? coreResources() : pending());
  };
</script>

<Story name="Fleet stats">
  <AppAt path={PATH} />
</Story>

<Story name="Loading">
  <AppAt path={PATH} overrides={{ [RESOURCES]: firstOnly() }} />
</Story>

<Story name="No such page" globals={{ modules: '' }}>
  <AppAt path={PATH} />
</Story>

<Story name="Modules off">
  <AppAt
    path={PATH}
    overrides={{
      'GET /api/v1/resources': () =>
        json(200, {
          items: [
            {
              kind: 'asset',
              name: 'fleet-stats',
              namespace: NAMESPACE,
              uid: 'uid-fleet-stats',
              enabled: true,
              source: 'overlay',
              status: {
                state: 'disabled',
                conditions: [
                  {
                    type: 'Started',
                    status: 'False',
                    reason: 'ModulesDisabled',
                    last_transition_time: '2026-10-19T00:00:00Z',
                    observed_generation: 1,
                  },
                ],
              },
            },
          ],
        }),
    }}
  />
</Story>
