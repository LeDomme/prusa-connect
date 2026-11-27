// The toolbar switch for the `modules` global (see `test/story/modules.ts`):
// one checkbox per module the story's `parameters.modules` registers, which
// the preview sends to the manager with the story. The manager's bundler maps
// `react` to the copy Storybook runs, and compiles JSX to
// `React.createElement`, which is why `React` is imported.
import React from 'react';
import { Button, PopoverProvider } from 'storybook/internal/components';
import { addons, types, useGlobals, useParameter } from 'storybook/manager-api';

const ADDON = 'prusa-connect/modules';

// Storybook drops a global with a comma from the URL, and an empty one, so
// the names are written apart with spaces and the core alone is `none`, which
// names no module.
const CORE_ONLY = 'none';

const Modules = (): React.JSX.Element | null => {
  const registered = useParameter<readonly { name: string }[]>('modules', []);
  const [globals, updateGlobals] = useGlobals();
  if (registered.length === 0) return null;

  const names = registered.map(({ name }) => name);
  const selection = String(globals.modules ?? 'all');
  const on = new Set(selection === 'all' ? names : selection.split(/[\s,]+/));
  const shown = names.filter((name) => on.has(name));
  let label = shown.length === 0 ? 'core only' : shown.join(', ');
  if (selection === 'all') label = 'all';
  const select = (modules: string): void => {
    updateGlobals({ modules });
  };
  const toggle = (name: string): void => {
    const next = names.filter((each) => (each === name) !== on.has(each));
    if (next.length === names.length) select('all');
    else select(next.length === 0 ? CORE_ONLY : next.join(' '));
  };

  return (
    <PopoverProvider
      ariaLabel="Modules in the app stories"
      placement="bottom"
      padding={10}
      popover={
        <div style={{ display: 'grid', gap: 6, minWidth: 160 }}>
          <div style={{ display: 'flex', gap: 6 }}>
            <button
              type="button"
              onClick={() => {
                select('all');
              }}
            >
              All
            </button>
            <button
              type="button"
              onClick={() => {
                select(CORE_ONLY);
              }}
            >
              Core only
            </button>
          </div>
          {names.map((name) => (
            <label key={name} style={{ display: 'flex', gap: 6 }}>
              <input
                type="checkbox"
                checked={on.has(name)}
                onChange={() => {
                  toggle(name);
                }}
              />
              {name}
            </label>
          ))}
        </div>
      }
    >
      <Button variant="ghost" padding="small" ariaLabel={false}>
        Modules: {label}
      </Button>
    </PopoverProvider>
  );
};

addons.register(ADDON, () => {
  addons.add(ADDON, {
    type: types.TOOL,
    title: 'Modules',
    match: ({ viewMode }) => viewMode === 'story',
    render: Modules,
  });
});
