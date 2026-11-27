# design, the design status of the stories

`design` gives every story file of the Storybook a design status, says which
story files a change reaches, and reports what a pull request changes in the
design. A reviewer approves the look of a story file, and the status follows
that approval until a file the look depends on changes.

The commands run with Node 24, which strips the TypeScript types itself, so
there is no build step:

```sh
pnpm --dir tools/design install --frozen-lockfile
node tools/design/src/cli.ts status
```

## Commands

The table lists `cli.ts` `COMMANDS` in its order. Each command name below is
written as `design <command>`, which is `node tools/design/src/cli.ts
<command>`.

| command | does |
|---|---|
| `design status [file…]` | prints the status of the named story files, or a count of every status |
| `design status --write <out.json>` | also writes every status to a JSON file |
| `design impact <file>` | lists the story files whose fingerprint reads the file; every story file reads the theme file and the catalogues |
| `design impact <--token>` | lists the story files that read a theme token, such as `--primary` |
| `design report --pr <n>` | prints the design report of pull request `n` |
| `design report --pr <n> --json <out.json>` | also writes the report's data to a JSON file |
| `design deprecate <file> --use <file>` | names the component to use instead of `file` in `design/deprecations.json` |

`status`, `impact` and `report` take `--approvals <file>`, a JSON file of
approvals. Without it no story file is `approved`.

What some commands do beyond the table:

- `design status --write` writes the file a Storybook build reads,
  `frontend/.storybook/generated/design-status.json`
- `design impact` with a token also lists the token's colour pairs under the
  WCAG AA contrast ratio
- `design report` reads the pull request's base and head with `gh`. It lists
  the story files the pull request touches and the status changes it makes.
  It also lists fixture endpoints and fields the core lacks, contrast pairs
  under 4.5:1, classes that compile to nothing and outside resources
- `design deprecate` changes the working tree only. The change is committed
  and reviewed like any other
- a command that fails unexpectedly prints one line and exits 1

## Approvals

This repository holds no approvals. Whoever reviews the design keeps them in a
JSON file and passes it in: `--approvals <file>` on the command line, and
`designServer({ approvals: '<file>' })` from `frontend/.storybook/design-server.ts`
in a Storybook dev server. The file maps a story file, relative to the
repository root, to one entry:

| entry | means |
|---|---|
| `{ "fingerprint": "<hash>", "by": "<who>", "at": "<date>" }` | the look of the story file was approved when its fingerprint was this hash |
| `{ "note": "<text>", "by": "<who>", "at": "<date>" }` | a review note. The story file stays `raw` |

Another checkout builds on `src/api.ts` only: the story files, the scan and
fingerprint, the statuses, the manifests and the quality checks. Everything
else under `src/` may change without notice.

## Key files

| path | purpose |
|---|---|
| `src/cli.ts` | the entry point and the list of commands |
| `src/commands/` | one file per command |
| `src/api.ts` | what another checkout may import |
| `src/status/fingerprint.ts` | the fingerprint of a story file |
| `src/status/manifest.ts` | the status of a story file, from its fingerprint, the approvals and the deprecations |
| `src/report.ts` | the design report of a pull request |
| `../../design/deprecations.json` | the deprecated components, each with its replacement |

## How a story file gets its status

A story file is approved as a whole: every story in it shares one set of
imported files. The status is one of these, checked in this order:

| status | when |
|---|---|
| `deprecated` | `design/deprecations.json` names the component of the story file, or the story file itself |
| `concept` | a fixture file of the story names an endpoint or a field the core's OpenAPI spec lacks, or cannot be read |
| `approved` | the approvals hold a fingerprint for the story file, and it matches the current one |
| `needs re-check` | the approvals hold a fingerprint, and the current one differs |
| `raw` | none of the above, including a story file with only a review note |

The fingerprint is a hash of what decides how the stories look:

- every file the story file imports, followed through all imports, with the CSS of the utility classes each file names. An imported image or font counts by its bytes
- the Storybook preview, which wraps every story
- for a story that mounts the app through `AppAt`, the sources of every module the Storybook serves, as `frontend/.storybook/module-list.ts` lists them
- the light and dark value of each theme token those files read
- the English strings under the keys the story file's own imports name

A fixture file holds server data for a design the core cannot answer yet. The
rules for it are in [.agents/rules/storybook.md](../../.agents/rules/storybook.md).

## Gates

| command | what it refuses |
|---|---|
| `make design-check` | a failing test under `test/`, or a type error in `src/` or `test/` |

`make design-check` runs `node --test 'tools/design/test/*.test.ts'` and
`tsc` with `tsconfig.json`. The type-check takes `tsc` and the Vite types from
`frontend/node_modules`, because the CLI imports `frontend/vite/import-map.ts`.
CI runs it in the job `Web / Application`.
