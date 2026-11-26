// The design status of every story file for the Storybook's Design panel and
// pill, in the dev server only.
//
// The status is computed in this process, so the fingerprints and closures the
// design tools keep stay warm between requests: the first scan starts with the
// server, and a request during it waits for that scan.
import type { ServerResponse } from 'node:http';
import path from 'node:path';

import type { Plugin } from 'vite';

import {
  readManifest,
  scan,
  statuses,
  storyFiles,
  type Approvals,
  type Noted,
  type Scan,
} from '../../tools/design/src/status/manifest.ts';
import { onePass } from '../../tools/design/src/status/tokens.ts';

const root = path.resolve(import.meta.dirname, '../..');

export type StatusAnswer =
  { files: Record<string, Noted> } | { unavailable: string };

/** The theme tokens one story file reads, light and dark. */
export interface Detail {
  tokens: Record<string, { light: string; dark: string }>;
}

export interface DesignServerOptions {
  /**
   * A JSON file of approvals, keyed by story file relative to this
   * repository's root. Without it every story is `raw`, `concept` or
   * `deprecated`. The server watches it and reads it again on a change.
   */
  approvals?: string;
}

const send = (response: ServerResponse, body: unknown): void => {
  response.statusCode = 200;
  response.setHeader('Content-Type', 'application/json');
  response.end(JSON.stringify(body));
};

export const designServer = (options: DesignServerOptions = {}): Plugin => {
  const approvals = (): Approvals =>
    options.approvals === undefined
      ? {}
      : readManifest<Approvals>(root, options.approvals);

  // The fingerprints of the last scan, for one story file's detail.
  let latest: Scan | undefined;

  const compute = (): Promise<{ answer: StatusAnswer; found?: Scan }> =>
    onePass(async () => {
      const found = await scan(root, storyFiles(root));
      latest = found;
      return { found, answer: { files: statuses(root, found, approvals()) } };
    });

  // One computation at a time. A request shares the one running unless a
  // watched file changed since it began; then a fresh one follows it. A
  // failure, such as a broken theme file, is the answer until the next
  // computation.
  let generation = 0;
  let running:
    | {
        generation: number;
        result: Promise<{ answer: StatusAnswer; found?: Scan }>;
      }
    | undefined;

  const refresh = (): Promise<{ answer: StatusAnswer; found?: Scan }> => {
    if (running?.generation !== generation) {
      const mine = {
        generation,
        result: (running?.result ?? Promise.resolve())
          .then(compute)
          .catch((e: unknown) => {
            const message = e instanceof Error ? e.message : String(e);
            return {
              answer: {
                unavailable: `status unavailable: ${message.split('\n')[0] ?? ''}`,
              },
            };
          }),
      };
      running = mine;
      void mine.result.finally(() => {
        if (running === mine) running = undefined;
      });
    }
    return running.result;
  };

  const detail = async (file: string): Promise<Detail | { error: string }> => {
    try {
      const print = (latest ?? (await refresh()).found)?.get(file);
      return { tokens: print?.tokens ?? {} };
    } catch (e) {
      return { error: e instanceof Error ? e.message : String(e) };
    }
  };

  return {
    name: 'prusa-connect:design-server',
    apply: 'serve',
    configureServer(server) {
      void refresh();
      if (options.approvals !== undefined) {
        server.watcher.add(path.resolve(root, options.approvals));
      }
      server.watcher.on('all', () => {
        generation++;
      });
      server.middlewares.use('/__design/status', (request, response, next) => {
        if (request.method !== 'GET') {
          next();
          return;
        }
        const file = new URL(
          request.url ?? '/',
          'http://localhost',
        ).searchParams.get('file');
        void (
          file === null ? refresh().then(({ answer }) => answer) : detail(file)
        ).then((body) => {
          send(response, body);
        });
      });
    },
  };
};
