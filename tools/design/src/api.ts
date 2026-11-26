// What another checkout may import from the design tools, such as a
// workspace that holds this repository as a submodule and keeps the approvals.
// Everything else under src/ may change without notice; this module changes
// only with its tests in test/api.test.ts.
export { catalogueFiles, fingerprint, inputsOf, TOKENS } from './status/fingerprint.ts'
export {
  componentOf,
  DEPRECATIONS,
  readManifest,
  scan,
  statuses,
  storyFiles,
  writeManifest,
  type Approvals,
  type Deprecations,
  type Noted,
  type Scan,
  type Status
} from './status/manifest.ts'
export { deadClasses } from './status/dead-classes.ts'
export { externalResources } from './status/external.ts'
