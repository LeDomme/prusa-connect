---
title: "ADR-20261123-sat: a printer driver may run in a module process and the host calls it over the plugin wire"
description: "A module of kind driver serves one printer driver from its own process. The host calls it over the plugin wire, and an endpoint names it as its connector, as it names a builtin one."
date: 2026-11-23
status: proposed
supersedes: []
amends: []
tags:
  - adr
  - connector
  - module
---
# ADR-20261123-sat: a printer driver may run in a module process and the host calls it over the plugin wire

<!-- nav:related -->
> [!NOTE]
> Index: [decisions](README.md) | Answers the runtime question [ADR-20260827-wmv](20260827-wmv-a-module-is-mounted-and-an-adapter-is-constructed.md) rule 4 left open | Builds the seam [ADR-20260831-nbj](20260831-nbj-a-module-declares-a-device-kind-and-the-host-places-it.md) rules 1, 24 and 25 ask for | Follows [ADR-20260827-7jn](20260827-7jn-the-plugin-wire-is-a-specification.md)

Every printer driver is in the binary today, so every install carries every
driver, and a driver ships only with a release of the core. The connector
should let any printer reach Prusa Connect through a driver that ships and
updates on its own. The xBuddy driver should move out of the core in time.
A driver fills `connectorapi`, which is in `sdk/`, so a driver that imports
nothing else can move as it is.

The core cannot run a driver outside the binary yet. `ref.KindDriver` already offers
`runtime: service`, and a module of kind `driver` already installs and
starts. But the plugin wire carries no driver call, nothing on the host turns
a child process into a `connectorapi.Driver`, and an endpoint can only name a
builtin connector. ADR-20260831-nbj lists all three as open items. This record
closes them, for any printer driver.

## rules

### the module

1. A module of kind `driver` serves one printer driver from its own process.
   No code of the module runs inside the core.
2. The driver's type is `<namespace>/<name>`, with the namespace the host
   derives under ADR-20260827-wmv rule 7. A module driver can then never take
   the name of a builtin one or of another module's.
3. The manifest carries a `driver` block with the driver's dialect and its
   baseline. The dialect is what an endpoint needs, the models, the admission
   and the state set. Both are the JSON `connectorapi` already defines. The
   install records the block, and the host reads it from there and never from
   the running process, as ADR-20260831-nbj rule 6 says of a declared kind.
4. A module driver is outbound only. The host dials it, and a printer never
   dials the module. A module that needs a printer to reach it uses its
   declared ports under ADR-20261019-3ns and dials from there.

### the wire

5. The driver contract crosses the wire as `sdk/driverwire`, in the shape
   `sdk/linkapi` already has. The package holds the method names, the limits,
   the module's `Handlers` and the host's `Client`.
6. The host calls the module with one method for each call of the contract:
   - `driver.connect`, under a session id the host picks, which returns the
     identity, the capabilities and the optional parts the session has
   - `driver.do`
   - `driver.storages`, `driver.list`, `driver.create_folder`,
     `driver.delete_file` and `driver.delete_folder`
   - `driver.send`, for an upload
   - `driver.close`
7. The module calls the host with `driver.state` for each state patch,
   `driver.capabilities` when its set or epoch changes, and `driver.ended`
   when a session ends by itself. A module names these in `require` with the
   `core-` prefix, as it names any host method.
8. An upload's bytes cross on a wire stream with credit, which `wirerpc.Stream`
   already gives. The module opens the content with `driver.content` at
   the offset it asks for, so a resumed upload reads from where it stopped.
   Progress travels back on the same session as `driver.progress`.
9. Every call names its session, and a session lives in one process. When the
   process exits, the host ends each of its sessions as disconnected. The dial
   supervisor dials again once the module serves, with its usual backoff.
10. The host checks what a module sends as it checks a builtin driver. The
    capability set is held to the baseline, and each patch to the state
    paths. Each result is held to the fault codes, and each value to the wire
    limits. A module is third-party code, and the host trusts none of it.

### the endpoint

11. An endpoint's connector may name a driver module's instance. The dial
    supervisor then reaches the driver through the `driverwire` client,
    exactly as it reaches a builtin, and the rest of the host cannot tell them
    apart.
12. The endpoint API and the overlay accept a driver module's instance as a
    connector. An endpoint whose module is switched off or removed stays
    declared, and its start names the module it lacks.
13. A module driver ships no profile registry, so its whole baseline applies.
    A registry across the wire waits for a driver that needs one.

### a print in a module

14. A planned stop of a driver module waits while one of its printers prints
    or holds a paused print. That is an upgrade, a switch-off, an uninstall
    or a settings change. The host logs the printers it waits for, and
    makes the change when they finish. An operator who needs it sooner
    stops the prints.
15. An unplanned stop is never held: a crash, the ping watchdog of
    ADR-20261030-b74, the memory kill of ADR-20261027-5m4 or safe mode. A host
    streaming print is lost when its process dies, and this record does not
    hide that cost.

### moving a driver

16. `driverwire.Run` runs a `connectorapi.Driver` as a module, so a driver
    author writes no wire code. A driver that imports only `sdk/` moves into a
    module with no change to its session code.

## enforcement

Each gate is watched failing on a deliberate mutation before it is trusted
green.

| rule | gate | state |
|---|---|---|
| 1, 16 | `TestADriverThroughTheWireKeepsTheContract`: `RunDriverContract` through `driverwire` over a pipe | exists |
| 6 | `TestARefusalKeepsItsFault`: a fault crosses the wire with its code | exists |
| 7 | `TestPatchesCrossAndAnEndedSessionCloses`: patches cross in order, and `driver.ended` closes the host's session | exists |
| 11 | `TestAnEndpointNamingADriverModuleIsDialledThroughIt`: an endpoint is dialled through the module it names | exists |
| 3 | `sdk/manifest` tests for the `driver` block and its kind, and `TestADriverBlockReadsTheSameInBothFiles` for the install record | exists |
| 3 | a case that the dial lookup reads the dialect from the install record and never from the process | owed |
| 8 | `TestAnUploadReadsFromItsOffset`: a resumed upload reads from its offset, and progress reaches the host | exists |
| 9 | `TestASessionEndsWithItsModule`: a session ends when its module goes away | exists |
| 9 | a case that the endpoint dials again after the module restarts | owed |
| 14 | `TestADriverModuleChangeWaitsForItsPrints`: a stop or restart waits on a print, an in-place change does not | exists |
| 14 | a case that the held step is carried out once the print ends | owed |
| 2 | a case that a module driver named like a builtin installs as `<namespace>/<name>` | owed |
| 6, 7 | `make wire-check`, with the driver methods in the protocol document | owed |
| 10 | `TestAModuleCannotOfferMoreThanItsBaseline`: a set over the baseline is held to it, on connect and on a change | exists |
| 10 | `TestAModuleCannotPatchOffTheStatePaths`: a bad path or too many patches refuse the whole call | exists |
| 10 | `TestAnOversizedTelemetryValueIsClippedInTheStateTree`: the device plane clips a value from any driver | exists |
| 10 | a result held to the registered fault codes | owed |
| 12 | a case that an endpoint of a switched-off module names the module it lacks | owed |

## rejected

| option | why not |
|---|---|
| keep every driver in the binary | every install carries drivers few of them use, and a driver ships only with the core |
| a module that translates its printer to the Connect protocol and dials `/p/ws` | it translates twice and limits each printer to what that protocol carries |
| the core keeps the protocol, and the module only passes bytes | the printer's code stays in the binary |
| a generic printer the core runs, fed by the module, as camera types are under ADR-20261106-km2 | a camera has a generic shape and a printer does not. The driver is the part that differs |
| several driver types in one module | an endpoint names an instance, so one type per instance keeps that mapping one to one. Nothing needs more yet |
| gRPC, or a plugin library | ADR-20260827-7jn already specifies the wire, and a second one would add a dependency for no gain |
| hold every stop while a print runs | a hung or runaway process would hold the server hostage. The print is lost in those cases anyway |

## open items

- Moving the xBuddy driver out of the core into a driver module, under rule
  16. The move is a record of its own.
- An inbound driver, one a printer dials, has no module form. It needs the
  host to pass a socket or a route to a process.
- A profile registry across the wire, for a module driver whose verbs depend
  on the firmware.
- ADR-20260827-wmv rule 15's `CoreContract` does not exist yet. Until it does,
  `contract_major` and `require` are the only checks that a module and a core
  agree.
- A module artefact for armv6, so a 32-bit Raspberry Pi can install a driver module.
