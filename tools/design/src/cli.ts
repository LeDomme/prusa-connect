#!/usr/bin/env node
const COMMANDS = ['status', 'impact', 'report', 'deprecate']

const [command, ...args] = process.argv.slice(2)
if (!command || !COMMANDS.includes(command)) {
  console.error(`usage: design <command> [args]\ncommands: ${COMMANDS.join(', ')}`)
  process.exit(1)
}
try {
  const mod: { run(args: string[]): Promise<number> } = await import(`./commands/${command}.ts`)
  process.exit(await mod.run(args))
} catch (e) {
  // A failed git or gh call carries the tool's own complaint in stderr.
  const { message, stderr } = e as Error & { stderr?: unknown }
  const detail = String(stderr ?? '').trim().split('\n')[0]
  console.error(`design ${command} failed: ${message.split('\n')[0]}${detail ? `: ${detail}` : ''}`)
  process.exit(1)
}
