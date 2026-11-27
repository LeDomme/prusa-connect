import fs from 'node:fs'
import path from 'node:path'
import { SERVED_MODULES } from '../../../../frontend/.storybook/module-list.ts'
import { extensionClientAliases, sourceAliases } from '../../../../frontend/vite/import-map.ts'
import { type Alias, importClosure, scriptOf } from './closure.ts'

const SHELL = 'frontend/src/app.svelte'
const SCREENS = 'frontend/src/lib/screens.ts'
const ROUTES = 'frontend/src/lib/routes.ts'

const read = (repoRoot: string, file: string) => fs.readFileSync(path.join(repoRoot, file), 'utf8')

/** The source entry of each module the Storybook serves that this checkout has. */
const moduleEntries = (repoRoot: string) =>
  SERVED_MODULES.map((m) => path.join(repoRoot, m.ui, 'src/index.ts')).filter((f) => fs.existsSync(f))

/** `PATHS` from routes.ts, in declaration order: the order the router tries them. */
function paths(repoRoot: string): Map<string, string> {
  const body = read(repoRoot, ROUTES).match(/export const PATHS = \{([\s\S]*?)\} as const/)?.[1] ?? ''
  return new Map([...body.matchAll(/^\s*(\w+):\s*'([^']*)'/gm)].map((m) => [m[1]!, m[2]!]))
}

// The router's rule: a `:name` segment takes any one non-empty segment, every
// other segment matches itself, and nothing matches across a slash.
function matches(pattern: string, routePath: string): boolean {
  const want = pattern.split('/')
  const got = routePath.split('/')
  return want.length === got.length && want.every((s, i) => (s.startsWith(':') ? got[i] !== '' : s === got[i]))
}

/** The file a component named in screens.ts is imported from. */
function componentFile(source: string, name: string, repoRoot: string): string | undefined {
  const file = source.match(new RegExp(`^import ${name} from '([^']+)'`, 'm'))?.[1]
  return file ? path.resolve(repoRoot, path.dirname(SCREENS), file) : undefined
}

/** The screen component file that answers `routePath`, or undefined when no route does. */
export function screenModuleFor(routePath: string, repoRoot: string): string | undefined {
  const bare = routePath.replace(/[?#].*$/, '')
  const key = [...paths(repoRoot)].find(([, pattern]) => matches(pattern, bare))?.[0]
  if (!key) return undefined
  const source = read(repoRoot, SCREENS)
  const component = source.match(new RegExp(`pattern:\\s*PATHS\\.${key}\\s*,\\s*component:\\s*(\\w+)`))?.[1]
  return component && componentFile(source, component, repoRoot)
}

/** Every routed screen module, and the one `screenFor` draws when no route answers. */
function screenModules(repoRoot: string): { all: string[]; notFound: string | undefined } {
  const source = read(repoRoot, SCREENS)
  const names = [...source.matchAll(/component:\s*(\w+)/g)].map((m) => m[1]!)
  const fallback = source.match(/\.component \?\? (\w+)/)?.[1]
  const file = (name: string) => componentFile(source, name, repoRoot)
  const all = [...new Set([...names, ...(fallback ? [fallback] : [])].map(file))]
  return { all: all.filter((f) => f !== undefined), notFound: fallback && file(fallback) }
}

/**
 * The screen a story file sits beside: `x.svelte` for `x.stories.svelte`, or
 * else the only screen module in its directory.
 */
function screenBeside(storyFile: string, modules: string[]): string | undefined {
  const named = storyFile.replace(/\.stories\.svelte$/, '.svelte')
  if (modules.includes(named)) return named
  const here = modules.filter((m) => path.dirname(m) === path.dirname(storyFile))
  return here.length === 1 ? here[0] : undefined
}

// A `${PATHS.x}` part is that path, and any other `{…}` or `${…}` part one segment.
const fill = (s: string, known: Map<string, string>) =>
  s.replace(/\$\{\s*PATHS\.(\w+)\s*\}/g, (_, k: string) => known.get(k) ?? 'x').replace(/\$?\{[^}]*\}/g, 'x')

/**
 * The address an `<AppAt path=…>` names, when it is written out: a quoted
 * attribute, a string or template literal, `PATHS.x`, or a `const` bound to
 * one of those in the same file. Anything computed, such as a helper call, is
 * undefined.
 */
function literalPath(attr: string, source: string, known: Map<string, string>): string | undefined {
  const value = (expr: string, depth = 0): string | undefined => {
    const e = expr.trim()
    const quoted = e.match(/^(['"`])([^'"`]*)\1$/)
    if (quoted) return fill(quoted[2]!, known)
    const named = e.match(/^PATHS\.(\w+)$/)
    if (named) return known.get(named[1]!)
    if (depth > 0 || !/^\w+$/.test(e)) return undefined
    const bound = source.match(new RegExp(`\\bconst ${e}\\s*=\\s*([^;\\n]+)`))?.[1]
    return bound === undefined ? undefined : value(bound, depth + 1)
  }
  return attr.startsWith('{') ? value(attr.slice(1, -1)) : fill(attr.slice(1, -1), known)
}

/** Every address some code can name: each `PATHS.x`, and each literal that starts with a slash. */
function addressesIn(code: string, known: Map<string, string>): string[] {
  const named = [...code.matchAll(/\bPATHS\.(\w+)/g)].map((m) => known.get(m[1]!))
  const literal = [...code.matchAll(/(['"`])((?:\/|\$\{\s*PATHS\.)[^'"`]*)\1/g)].map((m) => fill(m[2]!, known))
  return [...named, ...literal].filter((a) => a?.startsWith('/')) as string[]
}

/**
 * The screens a redirecting screen can send the reader on to: one that calls
 * `ctx.go(…, { replace: true })`, itself or in a `.svelte` file it imports
 * from its own directory, draws another screen in its place. The targets are
 * every address those scripts name, directly or through an `…Href(…)` helper,
 * so a link that is not the redirect is in too. A redirect made deeper down,
 * or through a variable holding the options, is not seen.
 */
function redirectTargets(screen: string, repoRoot: string, aliases: Alias[], stop: Set<string>): string[] {
  const own = scriptOf(screen)
  const children = [...own.matchAll(/\bfrom\s*['"]\.\/([^'"/]+\.svelte)['"]/g)].map((m) => path.join(path.dirname(screen), m[1]!))
  const code = [own, ...children.filter((f) => fs.existsSync(f)).map(scriptOf)].join('\n')
  if (!/\bctx\.go\(/.test(code) || !/\breplace(?:\s*:\s*true)?\s*[,}]/.test(code)) return []
  const helpers = [...new Set([...code.matchAll(/\b(\w+Href)\(/g)].map((m) => m[1]!))]
  const bodies = !helpers.length
    ? []
    : importClosure(screen, aliases, stop)
        .filter((f) => f.endsWith('.ts') || f.endsWith('.svelte'))
        .flatMap((f) => {
          const text = fs.readFileSync(f, 'utf8')
          return helpers.map((h) => text.match(new RegExp(`export const ${h}\\b[\\s\\S]*?(?=\\nexport |$)`))?.[0] ?? '')
        })
  const known = paths(repoRoot)
  return [...new Set([code, ...bodies].flatMap((c) => addressesIn(c, known)))]
    .map((a) => screenModuleFor(a, repoRoot))
    .filter((s): s is string => s !== undefined && s !== screen)
}

/**
 * The files a story file's look depends on.
 *
 * App screen stories mount the whole app through `AppAt`, whose shell imports
 * every screen through screens.ts, so that file is not entered. The screens
 * the story's addresses open are added instead, with the shell and every
 * screen a redirect among them leads on to. An address that matches no route
 * opens the not-found screen; one this cannot read opens the screen beside
 * the story. With no such screen, the story keeps every screen. The sources
 * of every module the Storybook serves are in too: the app draws them all.
 *
 * Its concept data is the fixtures named after it beside it and every
 * `*.fixture.json` its closure imports, through a helper module or not.
 * `X.stories.svelte` is named by `X.fixture.json` and `X.*.fixture.json`; a
 * fixture of another story in the same directory is not its own.
 */
export function storyClosure(storyFile: string, repoRoot: string): string[] {
  const aliases = [...sourceAliases(path.join(repoRoot, 'packages')), ...extensionClientAliases(path.join(repoRoot, 'extensions'))]
  const stop = new Set([path.join(repoRoot, SHELL), path.join(repoRoot, SCREENS)])
  const source = fs.readFileSync(storyFile, 'utf8')
  const dir = path.dirname(storyFile)
  const prefix = `${path.basename(storyFile, '.stories.svelte')}.`
  const fixtures = fs.readdirSync(dir).filter((f) => f.startsWith(prefix) && f.endsWith('.fixture.json')).map((f) => path.join(dir, f))

  const attrs = [...source.matchAll(/<AppAt\b[^>]*?\bpath=("[^"]*"|\{(?:[^{}]|\{[^{}]*\})*\})/g)].map((m) => m[1]!)
  let screens: (string | undefined)[] = []
  if (attrs.length || /<AppAt\b/.test(source)) {
    const known = paths(repoRoot)
    const modules = screenModules(repoRoot)
    const beside = screenBeside(storyFile, modules.all)
    screens = attrs.map((attr) => {
      const routePath = literalPath(attr, source, known)
      return routePath === undefined ? beside : (screenModuleFor(routePath, repoRoot) ?? modules.notFound)
    })
    // A tag the pattern above could not read counts as an unreadable address.
    if ((source.match(/<AppAt\b/g)?.length ?? 0) > attrs.length) screens.push(beside)
  }
  const modules = /<AppAt\b/.test(source) ? moduleEntries(repoRoot).flatMap((entry) => importClosure(entry, aliases, stop)) : []
  if (screens.includes(undefined)) return [...new Set([...importClosure(storyFile, aliases, new Set()), ...modules, ...fixtures])].sort()

  const opened = new Set<string>()
  const open = (screen: string) => {
    if (opened.has(screen)) return
    opened.add(screen)
    for (const next of redirectTargets(screen, repoRoot, aliases, stop)) open(next)
  }
  for (const screen of screens as string[]) open(screen)

  const entries = [storyFile, ...(opened.size ? [path.join(repoRoot, SHELL)] : []), ...opened]
  return [...new Set([...entries.flatMap((entry) => importClosure(entry, aliases, stop)), ...modules, ...fixtures])].sort()
}
