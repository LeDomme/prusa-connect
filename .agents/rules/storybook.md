---
paths:
  - "**/*.svelte"
  - "**/*.stories.svelte"
---

# Storybook

The designer works from the Storybook. A component or a screen state with no
story does not exist for design. So every component and every state a screen
draws has a story. [ADR-20261023-r3x](../../docs/decisions/20261023-r3x-every-component-and-every-screen-state-has-a-story.md)
states the rules, and two tests hold them.

## When you write a story

- you add a component that a barrel `index.ts` exports: add
  `<name>.stories.svelte` beside it, in the same commit
- you change how a component looks or add a prop that changes how it looks:
  add or update the story for that look
- you add a screen to `SCREENS`: add `<screen>.stories.svelte` beside it
- you add a branch a screen draws on data state, such as an `{#if}` on a
  request that is loading, failed, refused or empty: add the story for it
- a state you cannot show is named in the story file's top comment, one line
  each: `Omitted Refused: every signed-in person may read this page.`

## How to write one

- a library component: draw it over fakes from `packages/svelte/src/test/`.
  Import the `.svelte` file by its relative path, the way
  `primitives/card/card.stories.svelte` does
- an app screen: mount the real app with `AppAt` from
  `frontend/test/story/app-at.svelte`, and change the server's answers with
  `overrides`. `frontend/test/story/api.ts` has `pending`, `failed`, `refused`
  and `emptyList`. The Storybook section of `frontend/README.md` shows one
- a screen's story names are exact: `Loading`, `Failed`, `Refused`, and
  `Empty` for a screen that is a list. Other states take any plain name
- server data for a design the core cannot answer yet goes in a fixture file,
  keyed like `frontend/test/ux/fixtures/api.json`. The story file
  `x.stories.svelte` owns `x.fixture.json` and `x.<name>.fixture.json` beside
  it, and any `*.fixture.json` it imports, directly or through a helper
  module. A fixture that names an endpoint or field the OpenAPI spec lacks
  marks only those stories `concept`. Import it and pass
  `overrides={fromFixture(fixture)}` to `AppAt`

## Design status

Every story file has a design status, which the Storybook shows in the
Design panel and a pill, and a build also as the tag `status:<status>`:

| status | means |
|---|---|
| `raw` | nobody approved the look yet |
| `approved` | a designer approved the look, and no file it depends on changed since |
| `needs re-check` | a designer approved the look, and a file it depends on changed since |
| `deprecated` | `design/deprecations.json` names the component, with the one to use instead |
| `concept` | a fixture file of the story names an endpoint or a field the core lacks |

So a change to a component or a token can move a story from `approved` to
`needs re-check`. That is expected: the reviewer looks again and approves.
The approvals are not in this repository; whoever reviews the design passes
them in, and without them no story is `approved`.
[ADR-20261124-r7t](../../docs/decisions/20261124-r7t-design-status-follows-a-story-fingerprint.md)
states the rules.

| command | does |
|---|---|
| `node tools/design/src/cli.ts status [file…]` | prints the status of story files |
| `node tools/design/src/cli.ts impact <file \| --token>` | lists the story files a file or a theme token reaches |
| `node tools/design/src/cli.ts report --pr <n>` | prints what a pull request changes in the design |

[tools/design/README.md](../../tools/design/README.md) lists every command.

## Gates to run

| command | what it refuses |
|---|---|
| `pnpm --dir packages/svelte exec vitest run --project node src/stories-cover-components.test.ts` | a library component with no story, and a baseline line whose component now has one |
| `pnpm --dir frontend exec vitest run --project node src/lib/stories-cover-components.test.ts` | an app component with no story |
| `pnpm --dir frontend exec vitest run --project node src/lib/screens-storied.test.ts` | a screen with no story file, or a screen story file missing a state |
| `make frontend-storybook` | a story that does not build |
| `make design-check` | a failing test or a type error in `tools/design` |

`packages/svelte/stories.baseline` lists the library components with no story
yet. Delete a line when you write the story. Never add one.
