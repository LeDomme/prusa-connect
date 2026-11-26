import { describe, expect, it } from 'vitest';

import { FRONTEND, storyFileOf } from './story-file';

describe('the story file of an index entry', () => {
  it('names an app story under frontend/', () => {
    expect(storyFileOf('./src/lib/x/x.stories.svelte', FRONTEND)).toBe(
      'frontend/src/lib/x/x.stories.svelte',
    );
  });

  it('names a library or extension story from the repository root', () => {
    expect(
      storyFileOf(
        '../packages/svelte/src/primitives/card/card.stories.svelte',
        FRONTEND,
      ),
    ).toBe('packages/svelte/src/primitives/card/card.stories.svelte');
    expect(
      storyFileOf('../extensions/threemf/ui/src/view.stories.svelte', FRONTEND),
    ).toBe('extensions/threemf/ui/src/view.stories.svelte');
  });

  it('names a story from a Storybook that runs outside the repository', () => {
    const outside = { cwdFromRoot: '../storybook', rootFromCwd: '../core' };
    expect(
      storyFileOf('../core/frontend/src/lib/x/x.stories.svelte', outside),
    ).toBe('frontend/src/lib/x/x.stories.svelte');
    expect(
      storyFileOf(
        '../core/packages/svelte/src/primitives/card/card.stories.svelte',
        outside,
      ),
    ).toBe('packages/svelte/src/primitives/card/card.stories.svelte');
  });

  it('names a story from a Storybook that runs at the repository root', () => {
    expect(
      storyFileOf('./frontend/src/lib/x/x.stories.svelte', {
        cwdFromRoot: '',
        rootFromCwd: '',
      }),
    ).toBe('frontend/src/lib/x/x.stories.svelte');
  });
});
