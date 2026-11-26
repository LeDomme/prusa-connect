import { readManifest, type Approvals } from './status/manifest.ts'

/**
 * `args` without `--<name> <value>`, with that value, or null when the flag
 * is given without a value.
 */
export function option(args: string[], name: string): { rest: string[]; value?: string } | null {
  const at = args.indexOf(`--${name}`)
  if (at < 0) return { rest: args }
  const value = args[at + 1]
  if (!value || value.startsWith('--')) return null
  return { rest: args.filter((_, i) => i !== at && i !== at + 1), value }
}

/** The approvals of `--approvals <file>`, none without it. A missing file holds none either. */
export const approvalsFrom = (file: string | undefined): Approvals => (file === undefined ? {} : readManifest<Approvals>(process.cwd(), file))
