# Prusa Connect

Prusa Connect is a server you run on your own machine. It talks to 3D printers and gives every printer the same API, meaning one address and one set of rules for every request. A program reaches that API over HTTP, over SSE and over MQTT. SSE is a one-way stream of events from the server to a browser. MQTT is a small message protocol that many devices speak. None of it needs a cloud service. It ships as one file that runs on a Raspberry Pi or on a large server, with one printer or four hundred.

The project is a small core that stays the same everywhere, with everything that varies behind a contract. A contract is a Go interface plus a test suite that proves an implementation fits it. Anyone in the community can write such an implementation.

## Read these first

| document | what it is |
|---|---|
| [docs/src/content/docs/onboarding/](docs/src/content/docs/onboarding/index.md) | pages that take you from nothing to knowing where to look. Start with these if the tree is new to you |
| [docs/src/content/docs/internals/](docs/src/content/docs/internals/the-application-core.md) | the core file by file, with the diagrams: how a printer message reaches a browser, and how a command reaches a printer |
| [docs/map.md](docs/map.md) | where everything is and who reads which document |
| [docs/decisions/](docs/decisions/) | ADRs, one file per decision. Each states rules, never history |
| [GitHub issues](https://github.com/prusa3d/prusa-connect/issues) | follow-up work and known debt |

The sources of truth are the code, the ADRs and the GitHub issues. There is no research folder and no backlog. A developer's word outranks an ADR. Build what a record says. If the code shows a decision was wrong, say so with evidence and write a new ADR that supersedes or amends it.

## Rules

Every file in this table is binding.

| rule | covers |
|---|---|
| [engineering-standards](.agents/rules/engineering-standards.md) | how to look things up, where a decision is written down, and the difference between predicting and checking |
| [go-standards](.agents/rules/go-standards.md) | errors, context, goroutines, the code that runs for every printer message, the `sdk/` module, SQL for two engines |
| [adversarial-review](.agents/rules/adversarial-review.md) | who reviews a change, the six questions a reviewer asks, and the defects this project meets again and again |
| [git-discipline](.agents/rules/git-discipline.md) | the shape of a commit, when to commit, what to check before pushing |
| [changelog-discipline](.agents/rules/changelog-discipline.md) | which commits need a changelog entry, the `Changelog:` trailer, and how an entry is written |
| [writing-style](.agents/rules/writing-style.md) | how to write prose and headings, and what never to write |
| [docker-services](.agents/rules/docker-services.md) | where compose files go, the keys every service needs, volume permissions, base-image upgrades |
| [security-supply-chain](.agents/rules/security-supply-chain.md) | how a dependency enters the tree: lockfile first, exact versions, the three-day wait, pinned images and actions |
| [svelte-5](.agents/rules/svelte-5.md) | runes only, stores as classes, snippets, callback props, bits-ui, where a shared component lives |
| [svelte-data](.agents/rules/svelte-data.md) | data from the server against data the page owns, service to store to component, one request per piece of data |
| [svelte-testing](.agents/rules/svelte-testing.md) | tests for stores and components, counting recomputations, leak tests, performance budgets |
| [frontend-css](.agents/rules/frontend-css.md) | Tailwind everywhere, oklch colour tokens, logical properties, container queries, layout arithmetic stays in CSS |
| [frontend-structure](.agents/rules/frontend-structure.md) | component folders, how styles ship, server-side rendering, the core reached only through the SDK |
| [storybook](.agents/rules/storybook.md) | every component and every screen state has a story, how to write one, and the gates |
| [frontend-i18n](.agents/rules/frontend-i18n.md) | every text is a key, a fault is shown by its code, a module ships its own translations |
| [frontend-logging](.agents/rules/frontend-logging.md) | the browser logger, what never reaches the console, the log level at run time |
| [typescript](.agents/rules/typescript.md) | compiler options, banned types, the lint checks |
| [ui-integration](.agents/rules/ui-integration.md) | a UI change must look like it was always there; check it in a real browser |

## Before you write prose

These records say how anything here is written, and a script checks both. Read
them before you write a document, a comment or a commit body.

| record | covers |
|---|---|
| [ADR-20260826-gtb](docs/decisions/20260826-gtb-writing-that-reads-as-written.md) | nine rules on phrasing, each with a check in `scripts/check-writing-style.sh` |
| [ADR-20260826-shb](docs/decisions/20260826-shb-a-document-is-read-at-speed.md) | the reader is at B2 and is not a native speaker of English |

B2 is the upper-intermediate level of the common European scale: a person who
reads everyday English well but not idiom or rare words. Most people here read
English as a second language, and customers read it below B2. So use the common
word, cut idiom and metaphor, and never rename a thing you have already named.
Keep the exact technical term and explain it in one plain sentence the first
time it appears.

`make style-check` runs fifteen checks, numbered 1 to 9 and 12 to 17, against a
baseline in `scripts/writing-style.baseline`. The baseline lists the failures
the checks allow, one line per file and check. **Remove a line when you fix a file. Never add one.**
`internal/arch/style_checks_test.go` checks that this count matches the script.

## Writing a record

A significant decision goes in an ADR. Follow-up work and known debt go in a GitHub issue,
and only when they are worth tracking. A measurement goes in the ADR it
supports or in the pull request description.

A new decision record gets its id from `make adr-new TITLE="..."`. The id is the date and three random characters, such as
`ADR-20261025-k7q`, so two pull requests never take the same one. Never take a
number, and never edit an index or a ledger: no file lists the records. The
documentation site builds the indexes from the files.
A record that replaces an older one lists it under `supersedes:` in its own
front matter, and one that changes some rules lists it under `amends:`. The
older record is not edited.
[git-discipline](.agents/rules/git-discipline.md#a-new-record-takes-a-dated-id)
names the gates.

## Who decided what

A question only the maintainers can settle goes in a GitHub issue, not in a
record that settles it.

## Gates you must run

A gate is a check that refuses a change. Run these before you push:
`make style-check`, `make docs-check`, `make api-check`, `make error-codes-check`,
`make extensions-check`, `make binary-check`, `make sdk-check`,
`make makefile-check`, `make hooks-check`, and `go test ./internal/arch/`.

That list is not CI. CI is the set of checks GitHub runs on every push, and it
runs more jobs than this list covers. The others include the lint job, the
generated files, the Docker image, the frontend type sync, and every failure
that appears only on Linux or only under `-race`. Read [what runs here and
what only runs in
CI](docs/src/content/docs/internals/build-gates-and-testing.md#what-runs-here-and-what-only-runs-in-ci)
before pushing. It names the local command for each.

These surprise people, so they are explained here:

- **`make test-unit`, never a bare `go test ./...`.** A test file can sit behind
  a build tag, which is a label that says "compile me only when asked". An
  untagged run skips such a file, and a package whose test files are all
  skipped still prints `ok`. `internal/authz/engine_postgres_test.go` is behind
  `//go:build postgres`, so `go test ./internal/authz/` prints `ok` without it.
  `test-unit` names each tagged package again with its tag, and
  `internal/arch/taggedtests_test.go` refuses one the recipe does not name.
- **A dry run of a target that calls `$(MAKE)` fails here, and the Makefile is
  not the reason.** GNU make runs a recipe line containing `$(MAKE)` even under
  `-n`. GnuWin32 make then starts it without quoting the space in
  `C:/Program Files (x86)`, so `make -n pre-commit` dies with
  `CreateProcess(NULL, "", ...) failed` and `e=87`. Bare `make --dry-run` exits
  0. To diagnose a make failure, run the target; never dry-run it.
- **`make install-hooks` on a fresh clone.** A hook is a script git runs before
  a commit or a push. Until this target runs, git uses its own empty hooks
  directory and nothing checks a commit. A frontend install points
  `core.hooksPath` at husky's directory instead. The scripts there run the
  same checks, so the order of the two installs changes nothing.
  `scripts/check-hooks.sh` refuses a push from a clone whose hooks do nothing.
- **`make test-race`**, not `go test -race`. The race detector needs cgo, which
  needs a C compiler. On a machine with none, the target runs the tests in a
  `golang:1.26` container and says which way it took.

## The server that came before

An earlier server stood in a `legacy/` folder, and the folder is deleted. What
that server knew about printers is kept as test data under
`internal/arch/testdata/`. No code of it remains.

## The three things that never become pluggable

Pluggable means an operator can swap a part for another implementation. The event envelope, the registry and the authorization graph are never pluggable. The event envelope is the fixed wrapper every event is sent in. The registry is the list of every resource and its name. The authorization graph is the record of who may do what to which resource. Together they are the vocabulary every resource speaks. The product exists to give one address for everything. Any printer, any storage and any event is reached the same way from the API, from the frontend and from another server this one is joined to. Making any of them configurable breaks that.

The set of states of a device is not fixed. An adapter is the code that speaks
to one kind of device. The adapter declares the states, and the host places
them.

What is fixed is the *addressing*, meaning how a thing is named and found. The
content is not fixed. The envelope's shape, the registry's naming rule, the
relation vocabulary and the closed set of permission classes belong to the core
and cannot be configured. The verbs a device accepts are not fixed. A driver
declares its own verbs, and each verb has a class from the closed core set
(`sdk/connectorapi/permissions.go`). Where data is stored is not fixed either.
The history provider and the record backend are registered adapters under
`adapters/db`.

## What breaks quietly here

These come from defects already found in this tree. A reviewer checks every one, whether or not the change looks related.

- a permission checked on one transport and not the others. The list query, the event stream and the path to a joined server each need it
- a list endpoint that fetches rows and then filters them. Even when the rows are right, the page count and the totals give away what was filtered
- anything in the printer message path that allocates memory once per subscriber
- a call into third-party code that the host does not supervise
- an unchecked `Close()` on a write. On FAT and USB, a full disk is reported there and nowhere else
- a `.gitignore` rule with no leading slash that excludes a directory which exists locally

## Commands

| command | does |
|---|---|
| `make` | print the target list and build nothing |
| `make help` | the same list, by name. It is `.DEFAULT_GOAL`, and it ends by printing how to see every target without a description |
| `make run` | build the core and run it in the foreground on `:8080` |
| `make build` | build with the feature tags the config asks for |
| `make test-unit` | unit tests, both modules |
| `make lint` | golangci-lint, both modules |
| `make test-engines` | run the authorization tests against SQLite and a real Postgres, with both required |
| `make docker-check` | build the image and prove it is hardened |
| `docker compose -f docker-compose.local.yml up -d` | run the stack locally |
| `node tools/design/src/cli.ts <command>` | the design status of the stories. [tools/design/README.md](tools/design/README.md) lists the commands |
| `make design-check` | the design command line's tests and its type-check |

## Layout

`cmd/` holds only commands. `sdk/` is a separate module that third parties compile against, and it imports only the standard library. `extensions/` holds the first-party extensions shipped through the marketplace. Their gates run from `make -C extensions check` and `.github/workflows/extensions.yml`, never from the root Makefile or the core CI. See [extensions/README.md](extensions/README.md). `internal/` is grouped by role, and a group may import only the groups below it. [docs/map.md](docs/map.md) has the table of groups, and `internal/arch` has the test that checks the imports.
