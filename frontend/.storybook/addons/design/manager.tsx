// The Design panel: the selected story file's design status, its review note
// and, in the dev server, the theme tokens it reads (`design-server.ts`). A
// hosted build has no server behind it, so there the panel reads the status
// the build was made with. The manager's bundler maps `react` to the copy
// Storybook runs, and compiles JSX to `React.createElement`, which is why
// `React` is imported.
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { AddonPanel } from 'storybook/internal/components';
import { DOCS_RENDERED, STORY_RENDERED } from 'storybook/internal/core-events';
import {
  addons,
  types,
  useChannel,
  useStorybookState,
} from 'storybook/manager-api';

import type { Detail, StatusAnswer } from '../../design-server.ts';
import { DESIGN_CHANGED, storyFileOf, storyPaths } from './story-file.ts';

const ADDON = 'prusa-connect/design';

// Storybook's own fact about this page: `storybook dev` serves it as
// DEVELOPMENT, a build writes PRODUCTION.
const DEV =
  (globalThis as { CONFIG_TYPE?: string }).CONFIG_TYPE === 'DEVELOPMENT';

// The status the hosted build was made with, in the shape the server answers.
const hostedStatus = async (): Promise<StatusAnswer> => {
  // eslint-disable-next-line no-restricted-globals -- a file of this build, not the core
  const response = await fetch('./design/design-status.json');
  if (!response.ok) {
    return { unavailable: 'this build carries no design status' };
  }
  // Written by `design status --write`, in the shape the server answers.
  const files = (await response.json()) as Extract<
    StatusAnswer,
    { files: unknown }
  >['files'];
  return { files };
};

const liveStatus = async (): Promise<StatusAnswer> => {
  // eslint-disable-next-line no-restricted-globals -- the Storybook's own design server, not the core
  const response = await fetch('/__design/status');
  // The design server answers in this shape.
  return (await response.json()) as StatusAnswer;
};

const liveDetail = async (file: string): Promise<Detail | null> => {
  // eslint-disable-next-line no-restricted-globals -- the Storybook's own design server, not the core
  const response = await fetch(
    `/__design/status?file=${encodeURIComponent(file)}`,
  );
  // The design server answers in one of these two shapes.
  const answer = (await response.json()) as Detail | { error: string };
  return 'error' in answer ? null : answer;
};

const Design = (): React.JSX.Element => {
  const { storyId, index } = useStorybookState();
  const [answer, setAnswer] = useState<StatusAnswer | null>(null);
  const [detail, setDetail] = useState<Detail | null>(null);

  const entry = index?.[storyId];
  const file =
    entry?.type === 'story'
      ? storyFileOf(entry.importPath, storyPaths())
      : undefined;
  const shown = useRef(file);
  shown.current = file;

  const load = useCallback(async () => {
    try {
      setAnswer(DEV ? await liveStatus() : await hostedStatus());
      const now = shown.current;
      if (DEV && now !== undefined) setDetail(await liveDetail(now));
    } catch (e) {
      setAnswer({ unavailable: `status unavailable: ${String(e)}` });
    }
  }, []);

  // A story drawn again, after a selection or a hot update, and any other
  // hot update the preview reports, may have changed what the status reads.
  useChannel({
    [STORY_RENDERED]: () => void load(),
    [DOCS_RENDERED]: () => void load(),
    [DESIGN_CHANGED]: () => void load(),
  });

  useEffect(() => {
    setDetail(null);
    void load();
  }, [file, load]);

  if (answer === null) {
    return <p>Reading the design status, the first scan takes a while…</p>;
  }
  if ('unavailable' in answer) return <p>{answer.unavailable}</p>;
  if (file === undefined) return <p>Select a story.</p>;
  const state = answer.files[file];
  if (state === undefined) return <p>{file} has no design status.</p>;

  return (
    <div style={{ display: 'grid', gap: 8, padding: 12 }}>
      <div>
        <code>{file}</code>
        <div>
          {state.status}
          {state.reason ? ` (${state.reason})` : ''}
        </div>
        {state.note === undefined ? null : <div>Note: {state.note}</div>}
      </div>
      {detail === null ? null : (
        <details>
          <summary>Tokens ({Object.keys(detail.tokens).length})</summary>
          <table>
            <tbody>
              {Object.entries(detail.tokens).map(([name, { light, dark }]) => (
                <tr key={name}>
                  <td>
                    <code>{name}</code>
                  </td>
                  <td>{light}</td>
                  <td>{dark}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </details>
      )}
    </div>
  );
};

addons.register(ADDON, () => {
  addons.add(`${ADDON}/panel`, {
    type: types.PANEL,
    title: 'Design',
    render: ({ active }) => (
      <AddonPanel active={active ?? false}>
        <Design />
      </AddonPanel>
    ),
  });
});
