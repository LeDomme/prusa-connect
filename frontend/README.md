# The web app

The Svelte application that the core serves at `/`. It talks to the core only
through `@prusa/connect`, and it builds its screens from
`@prusa/connect-ui` in `packages/svelte`.

## Storybook

The Storybook in this directory shows the whole product. It holds the screens
of this app in all their states and every story of the component library in
`packages/svelte/src/`. It also holds the stories of the `threemf`,
`server-slicing` and `simple-queue` extensions under
`extensions/<name>/ui/src/`. It needs no running core. The stories answer the
app's requests from recorded data.

```sh
make frontend-storybook              # builds storybook-static/, as CI does
pnpm --dir frontend run storybook    # dev server at http://localhost:6006
```

Build once before the first dev server. `make frontend-storybook` builds the
SDK, the library and the Fractal client the app stories import. It installs
the extensions' dependencies, which their stories and modules import from, and
those of `tools/design`, which the dev server's Design panel runs.
`build-storybook` itself does not load `tools/design`.

### The groups in the sidebar

| group        | holds                                                                        |
| ------------ | ---------------------------------------------------------------------------- |
| `Tour`       | the product in the order a person meets it, for one, two and many printers   |
| `Screens`    | every screen the app routes to, in each of its states                        |
| `Parts`      | the larger parts a screen is built from, from the app and the library        |
| `Shell`      | the frame around a screen: shell, rail, phone bar, footer, menus and dialogs |
| `Primitives` | the small components from the library                                        |

The `Tour` pages are in `src/tour/`. A screen's stories are in
`src/lib/screens/<screen>.stories.svelte`, beside the screen. Every other story
file is beside the component it shows.

### Design status

A story file's design status is one of `raw`, `approved`, `needs re-check`,
`deprecated` and `concept`. A pill in the preview's corner shows it.

In a build, every story carries the tag `status:<status>` of its story file,
which the sidebar's tag filter offers. The tags come from
`.storybook/generated/design-status.json`, which
`node tools/design/src/cli.ts status --write …` writes before the build:
`make frontend-storybook` runs it. Without the file the stories carry no
status tag.

The dev server carries no status tags: read once at its start, they would go
stale. Its pill and its Design panel read the status live and follow every
edit.

The **Design** panel shows the selected story file's status, its review note,
and in the dev server the theme tokens it reads. It only reads.

This repository holds no approvals, so here every story file is `raw`,
`concept` or `deprecated`. Whoever reviews the design keeps the approvals and
passes them in: `designServer({ approvals: '<file>' })` from
`.storybook/design-server.ts` for a dev server, and `--approvals <file>` for
`design status`. A Storybook that spreads this one's config replaces the
plugin named `prusa-connect:design-server` with its own.

[tools/design/README.md](../tools/design/README.md) lists every `design`
command and how a status is computed.

### How a screen story runs the real app

`AppAt` in `test/story/app-at.svelte` mounts the real `App` at one address.
It replaces `fetch` with the story core in `test/story/api.ts`, which answers
from the UX fixture table, `test/ux/fixtures/api.json`. When the story closes,
`AppAt` restores the real `fetch` and the Storybook address.

```svelte
<AppAt path="/jobs" overrides={{ 'GET /api/v1/jobs': emptyList }} />
```

- `path` is the address the app opens.
- `size` sets the fleet size: `one`, `two` or `many`. Without it, `many`.
- `overrides` replaces rows of the table. A key is `"<METHOD> <path>"`, with the
  query added when the row answers one query only. A value is a `Response` or a
  function that returns one.

A story's fixture file holds rows in the shape of `api.json`:
`{ "status": 200, "type": "application/json", "body": … }`. The body is the
text the core sends, or plain JSON. A row with no status answers 200.
`fromFixture` from `api.ts` turns the rows into `overrides`:

```svelte
<script lang="ts">
  import { fromFixture } from '../../../test/story/api';
  import cameras from './cameras.fixture.json';
</script>

<AppAt path="/cameras" overrides={fromFixture(cameras)} />
```

`api.ts` exports helpers for the usual states:

| helper        | answers                                 |
| ------------- | --------------------------------------- |
| `pending()`   | never, so a loading state stays loading |
| `refused()`   | 403, for a person without the grant     |
| `failed()`    | 500                                     |
| `emptyList()` | a list with no items                    |

The event stream, `GET /api/v1/events`, stays open unless a story overrides it.
A stream that closes would show the connection as lost on every screen.

### Modules in the app stories

A paid add-on such as fleet-stats is a module the story core can install.
The **Modules** toolbar button turns each one on or off in every app story. So
a screen shows as the core alone, or the core with some modules and not
others. The choice is in the address: `globals=modules:fleet-stats+other` for
two modules, `globals=modules:none` for the core alone, nothing for all.

- The modules are `parameters.modules` in `.storybook/preview.ts`, a list of
  `StoryModule` (`test/story/modules.ts`): a name and the item that
  `GET /api/v1/resources` lists for it. `test/story/fleet-stats.ts` is one.
- `AppAt` puts the story's own resources listing through the selection. A story
  that must show one selection whatever the toolbar says sets it with
  `globals={{ modules: '' }}` on the `Story`.
- The dev server serves a module from its source, `extensions/<name>/ui/src/`,
  so an edit to it shows at once. A build serves the module's built bundle.
  Both are at `modules/<name>/`, which `MODULES` in `.storybook/main.ts` lists.
- A Storybook that spreads `.storybook/main.ts` and registers more modules
  serves them through `servedModules()` (`.storybook/served-modules.ts`),
  each at the path its `entry_url` names, from source or as its built bundle.
  `make frontend-storybook-deps` builds what the spread config reads.

### A new screen or component needs a story

[The Storybook rule](../.agents/rules/storybook.md) is binding, and two tests
hold it:

- `src/lib/screens-storied.test.ts` fails when a screen in `SCREENS` has no
  `<screen>.stories.svelte` beside it. It also fails when the file has no
  story named `Loading`, `Failed` or `Refused`, or `Empty` for a screen that
  is a list. A state the screen cannot show is named in the top comment, on
  a line of its own: `Omitted Refused: <reason>`
- `src/lib/stories-cover-components.test.ts` fails when a component that a
  barrel `index.ts` exports is imported by no story file

Write a screen's file with `AppAt` at the screen's path, one story for each
state the screen has.

## ESLint fails after a change to packages/standards

The lint preset `@prusa/frontend-standards` lives in `packages/standards`.
pnpm copies it into `node_modules` here and in `packages/svelte`, and it does
not refresh that copy on a later install, even with `--force`. So after a pull
that adds an export to the preset, ESLint stops with an error such as
`does not provide an export named 'deprecatedImports'`. Remove both copies and
install again:

```sh
rm -rf frontend/node_modules packages/svelte/node_modules && make frontend-storybook-deps
```

[packages/standards/README.md](../packages/standards/README.md) says why the
preset is copied and not linked.
