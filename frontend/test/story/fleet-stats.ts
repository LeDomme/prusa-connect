// The fleet-stats extension, as a module an app story can install (see
// `modules.ts`). Storybook serves it at `modules/fleet-stats/` beside the
// preview document: from source in the dev server, and as the built bundle in
// a build. `iframe.html` carries the import map the bundle loads Svelte and
// the library through (see `.storybook/main.ts`). The history it reads is
// answered for every story, since no core screen reads it.
import { fleetOf, type FleetSize } from '../../.storybook/fleet';
import { json } from '../../src/test/stub';
import type { StoryModule } from './modules';

export const FLEET_STATS_NAMESPACE = 'com.github.aux.prusa3d.fleet-stats';

// Read once, as this file loads, before any story moves the address to a route
// of the app. A relative entry would resolve against the loader's own chunk.
// The tests that read this file in Node have no address, and fetch nothing.
const SITE = new URL(
  '.',
  'location' in globalThis ? location.href : 'http://story.test/',
).href;

/** Where every module's files are, so `fetch` reads its catalogues from there. */
export const MODULE_FILES = `${SITE}modules/`;

const FLEET_STATS_FILES = `${MODULE_FILES}fleet-stats/`;

const JOB_SETTLED = 'com.prusa.connect.device.job.settled';
const OUTCOMES = ['finished', 'finished', 'finished', 'failed', 'cancelled'];
const HOUR = 3_600_000;
const DAYS = 60;

/**
 * Sixty days of settled jobs for each printer of the fleet, ending now, so
 * every range the page offers has a period before it to compare against.
 * The numbers come from a fixed seed, so a story draws the same each time.
 */
const settledJobs = (size: FleetSize) => {
  let seed = 7;
  const next = (): number => {
    seed = (seed * 48_271) % 2_147_483_647;
    return seed / 2_147_483_647;
  };
  const now = Date.now();
  const facts = [];

  for (const device of fleetOf(size)) {
    let at = now - DAYS * 24 * HOUR;
    while (at < now) {
      const started = at + next() * 6 * HOUR;
      const ended = started + (1 + next() * 7) * HOUR;
      if (ended >= now) break;
      const outcome = OUTCOMES[Math.floor(next() * OUTCOMES.length)];
      facts.push({
        at: new Date(ended).toISOString(),
        epoch: 'story',
        origin: `device:${device.id}`,
        sequence: facts.length + 1,
        type: JOB_SETTLED,
        payload: {
          job_id: `job-${String(facts.length + 1)}`,
          display_name: `part-${String(facts.length % 17)}.bgcode`,
          started: new Date(started).toISOString(),
          ended: new Date(ended).toISOString(),
          outcome,
          machine_state: outcome === 'finished' ? 'FINISHED' : 'STOPPED',
        },
      });
      at = ended;
    }
  }

  return facts;
};

/** `GET /api/v1/history/facts`: one page, which the page filters by range. */
export const historyAnswer = (size: FleetSize): Response =>
  json(200, { items: settledJobs(size), cursor: '', incomplete: false });

export const statsJobsAnswer = (size: FleetSize): Response =>
  json(200, {
    items: settledJobs(size).map(({ origin, payload }) => ({
      device_id: origin.slice('device:'.length),
      id: payload.job_id,
      display_name: payload.display_name,
      started: payload.started,
      ended: payload.ended,
      outcome: payload.outcome,
      state: payload.machine_state,
    })),
    cursor: '',
  });

const period = (byDefault: string) => ({
  key: 'period',
  kind: 'choice',
  label: 'Period',
  options: ['7', '30', '90'].map((days) => ({
    value: days,
    label: `${days} days`,
  })),
  default: byDefault,
});
const PRINTERS = { key: 'printers', kind: 'printers', label: 'Printers' };
const TAG = { key: 'tag', kind: 'tag', label: 'Tag' };

/** fleet-stats as `module add` lists it, its `contributes` as in `module.yaml`. */
export const fleetStats: StoryModule = {
  name: 'fleet-stats',
  resource: {
    kind: 'asset',
    name: 'fleet-stats',
    namespace: FLEET_STATS_NAMESPACE,
    uid: 'uid-fleet-stats',
    enabled: true,
    source: 'overlay',
    version: '0.3.4',
    title: 'Fleet stats',
    summary:
      'Job KPIs, a daily trend, the most printed files, a utilization heatmap and failures for the fleet, with dashboard cards.',
    codeowner: 'Prusa Research',
    status: { state: 'ready', serving: true, conditions: [] },
    ui: {
      entry_url: `${FLEET_STATS_FILES}index.js`,
      library: '>=0.0.1 <1.0.0',
      // The loader drops the whole module, silently, at the first registration
      // this block leaves out, so it lists every one `ui/src/index.ts` makes.
      contributes: {
        pages: [
          { path: 'fleet-stats', title: 'Statistics', icon: 'chart-line' },
        ],
        device_card: [{ id: 'fleet-stats', title: 'Success rate' }],
        device_section: [{ id: 'fleet-stats', title: 'Print statistics' }],
        cards: [
          {
            id: 'kpis',
            title: 'Fleet figures',
            description:
              'Jobs, success, print hours and utilization, against the period before.',
            icon: 'chart-line',
            size: { w: 6, h: 3 },
            least: { w: 3, h: 3 },
            settings: [period('7'), PRINTERS, TAG],
          },
          {
            id: 'trend',
            title: 'Daily trend',
            description: 'Jobs, success or print hours day by day.',
            icon: 'chart-line',
            size: { w: 6, h: 5 },
            least: { w: 4, h: 4 },
            settings: [
              period('30'),
              PRINTERS,
              TAG,
              {
                key: 'metric',
                kind: 'choice',
                label: 'Metric',
                options: [
                  { value: 'jobs', label: 'Jobs' },
                  { value: 'success', label: 'Success' },
                  { value: 'hours', label: 'Print hours' },
                ],
                default: 'jobs',
              },
            ],
          },
          {
            id: 'utilization',
            title: 'Utilization heatmap',
            description: 'When each printer prints, hour by weekday.',
            icon: 'calendar-clock',
            size: { w: 6, h: 4 },
            least: { w: 4, h: 4 },
            settings: [period('7'), PRINTERS, TAG],
          },
          {
            id: 'failures',
            title: 'Recent failures',
            description:
              'The newest failed and cancelled jobs, with their printers.',
            icon: 'list-checks',
            size: { w: 4, h: 5 },
            least: { w: 3, h: 3 },
            settings: [
              period('7'),
              PRINTERS,
              TAG,
              {
                key: 'limit',
                kind: 'number',
                label: 'How many',
                min: 1,
                max: 20,
                step: 1,
                default: 5,
              },
            ],
          },
        ],
      },
    },
  },
};
