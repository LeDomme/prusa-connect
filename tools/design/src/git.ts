import { execFileSync } from 'node:child_process'

// Output of a command run with an argument array, never through a shell.
export function capture(cmd: string, args: string[], cwd?: string, input?: string): string {
  return execFileSync(cmd, args, {
    cwd,
    input,
    encoding: 'utf8',
    stdio: [input === undefined ? 'ignore' : 'pipe', 'pipe', 'pipe']
  }).trimEnd()
}

export function git(args: string[], cwd?: string): string {
  return capture('git', args, cwd)
}

export function repoRoot(cwd?: string): string {
  return git(['rev-parse', '--show-toplevel'], cwd)
}
