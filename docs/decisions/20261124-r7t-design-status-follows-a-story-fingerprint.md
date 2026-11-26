---
title: "ADR-20261124-r7t: design status follows a story fingerprint"
description: "Every story file has a design status, and an approval records a hash of what decides the story's look, so the status moves to needs re-check when one of those inputs changes."
date: 2026-11-24
status: proposed
supersedes: []
amends: []
tags:
  - adr
  - frontend
---
# ADR-20261124-r7t: design status follows a story fingerprint

<!-- nav:related -->
> [!NOTE]
> Index: [decisions](README.md) | Builds on [ADR-20261023-r3x](20261023-r3x-every-component-and-every-screen-state-has-a-story.md) | Rule file: [storybook](../../.agents/rules/storybook.md)

A designer reviews the product in the Storybook and approves how each story
looks. A developer then changes a component or a theme token, and the approved
look may change with it. The designer needs to see which stories to look at
again, without screenshots and without reading the diff.

So an approval records a fingerprint: a hash of every file and token value
that decides how the stories of one story file look. The `design` command line
in `tools/design` computes the fingerprint again on every read. When it no
longer matches, the story file is `needs re-check`.

## rules

1. A story file has one of five design statuses: `raw`, `approved`, `needs re-check`, `deprecated`, `concept`.
2. Compute the status on every read. The manifests hold fingerprints, notes and replacements, never a status.
3. The precedence is `deprecated`, then `concept`, then `approved` or `needs re-check`, then `raw`.
4. Key an approval by the story file's path relative to the repository root. Every story in the file shares one fingerprint.
5. The fingerprint covers each local file the story file imports, followed through every import with the aliases Vite uses, and the CSS of the utility classes each file names.
6. The fingerprint covers the light and the dark value of every theme token those files read, followed through each `var()` to its end.
7. The fingerprint covers the Storybook preview, the theme file's own rules, and the English strings under the keys the story file's own imports name.
8. Take a class name from every string literal of a file. An extra re-check is acceptable; a missed change is not.
9. Leave type-only imports and files git ignores out of the fingerprint. A generated file counts through its sources.
10. The approvals are an input, never a file of this repository: `--approvals <file>` on the command line, the `approvals` option of the Storybook's design server. Without them no story file is `approved`.
11. An approvals entry without a fingerprint is a review note. The story file stays `raw`.
12. A story file is `deprecated` when `design/deprecations.json` names its component or the story file itself, with the component to use instead.
13. Put server data for a design the core cannot answer yet in a `*.fixture.json` file beside the story file, keyed like `frontend/test/ux/fixtures/api.json`.
14. A story file owns `<name>.fixture.json`, `<name>.<state>.fixture.json` and every fixture it imports. Only those fixture files are checked against the OpenAPI spec; data written inside a story is not.
15. A story file is `concept` when one of its fixtures names an endpoint or a field the OpenAPI spec lacks.
16. `design/deprecations.json` changes through a pull request like any other file. `design deprecate` edits it in the working tree.
17. Another checkout imports only `tools/design/src/api.ts` and the Storybook config and addons.

## enforcement

| rule | gate |
|---|---|
| 1, 3 | `a deprecated component outranks every other status` in `tools/design/test/manifest.test.ts` |
| 1, 3 | `a concept outranks an approval` in `tools/design/test/manifest.test.ts` |
| 2, 4 | `a matching fingerprint is approved` in `tools/design/test/manifest.test.ts` |
| 2, 4 | `a fingerprint that moved needs a re-check because files changed` in `tools/design/test/manifest.test.ts` |
| 5 | `tools/design/test/closure.test.ts` |
| 6 | `a token edit changes the stories that use it and no other` in `tools/design/test/fingerprint.test.ts` |
| 7 | `a key only the preview names is no string of a library story` in `tools/design/test/fingerprint.test.ts` |
| 8 | none. Review holds it |
| 9 | none. Review holds it |
| 10 | `without approvals every story is raw, and an approval given is read` in `tools/design/test/api.test.ts` |
| 10 | `status reads the approvals file it is given` in `tools/design/test/api.test.ts` |
| 11 | `an entry without a fingerprint, such as a review note, is raw` in `tools/design/test/manifest.test.ts` |
| 12 | `a story file listed itself is deprecated` in `tools/design/test/manifest.test.ts` |
| 13, 14 | `a story takes only its own fixtures and the ones it imports` in `tools/design/test/closure.test.ts` |
| 15 | `a fixture makes a concept of its own story only` in `tools/design/test/manifest.test.ts` |
| 15 | `tools/design/test/backend.test.ts` |
| 16 | none. Review holds it |
| 17 | `the API names what another checkout builds on` in `tools/design/test/api.test.ts` |

`make design-check` runs these tests and type-checks `tools/design`. CI runs
it in the job `Web / Application`.

## rejected

| option | why not |
|---|---|
| a hash of the component file alone | it misses a change to a theme token the component reads |
| screenshot comparison | the images would live in the repository, and every pipeline would get slower |
| one approval per story | every story in a file imports the same files, so they always share one fingerprint |
| the status stored in the manifest | it is wrong the moment a developer changes an input |
| concept data inline in the story | nothing can check it against the OpenAPI spec |
| the approvals in this repository | every release would publish the review log and its notes, and each approval would need a pull request |

## open items

- The fingerprint misses the static files the Storybook serves from `frontend/public`, such as printer images and fonts: an edit there leaves an approved story `approved`. Adding them as shared inputs would move every story file on each image change.
- The fingerprint misses dependency and lockfile bumps (rule 5 covers local files only). A lockfile as a shared input would move every story file on each weekly dependency update.
- The closure follows a barrel file whole, so an edit to one component behind `packages/svelte/src/index.ts` moves most story files. Following only the names an importer takes, `export *` included, would narrow it.
