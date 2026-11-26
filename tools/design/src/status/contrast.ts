import { resolveToken, type Theme, type Tokens } from './tokens.ts'

type Srgb = readonly [number, number, number]

// OKLCH to sRGB (CSS Color 4), then WCAG 2.1 relative luminance and contrast,
// the arithmetic of packages/svelte/src/styles/contrast.test.ts.
const encode = (v: number) => {
  const x = Math.min(1, Math.max(0, v))
  return x <= 0.0031308 ? 12.92 * x : 1.055 * x ** (1 / 2.4) - 0.055
}

const oklchToSrgb = (L: number, C: number, hDeg: number): Srgb => {
  const h = (hDeg * Math.PI) / 180
  const a = C * Math.cos(h)
  const b = C * Math.sin(h)
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3
  return [
    encode(4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s),
    encode(-1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s),
    encode(-0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s),
  ]
}

const decode = (c: number) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4)

const luminance = ([r, g, b]: Srgb) => 0.2126 * decode(r) + 0.7152 * decode(g) + 0.0722 * decode(b)

/** An opaque colour written as `oklch(L% C H)`, `rgb()`/`rgba()` or hex; null for anything else. */
function srgb(value: string): Srgb | null {
  const oklch = /^oklch\(([\d.]+)%\s+([\d.]+)\s+([\d.]+)\)$/.exec(value)
  if (oklch) return oklchToSrgb(Number(oklch[1]) / 100, Number(oklch[2]), Number(oklch[3]))
  const rgba = /^rgba?\(\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)\s*(?:,\s*([\d.]+)\s*)?\)$/.exec(value)
  if (rgba) return Number(rgba[4] ?? '1') === 1 ? [Number(rgba[1]) / 255, Number(rgba[2]) / 255, Number(rgba[3]) / 255] : null
  const hex = /^#([0-9a-f]{3,8})$/i.exec(value)?.[1]
  if (!hex) return null
  const digits = hex.length <= 4 ? hex.replace(/./g, '$&$&') : hex
  const [r, g, b, a = 1] = (digits.match(/../g) ?? []).map((pair) => parseInt(pair, 16) / 255)
  return (digits.length === 6 || digits.length === 8) && a === 1 ? [r!, g!, b!] : null
}

const colour = (name: string, theme: Theme, tokens: Tokens) => {
  const value = resolveToken(name, theme, tokens)
  return typeof value === 'string' ? srgb(value) : null
}

/**
 * Every `--X-foreground` measured against `--X` in both themes, and
 * `--foreground` against `--background`, where the ratio is under WCAG AA's
 * 4.5:1. A colour that is translucent, unresolved or written in a form this
 * cannot read, such as relative colour syntax, is skipped.
 */
export function contrastIssues(tokens: Tokens): { pair: [string, string]; theme: Theme; ratio: number }[] {
  const issues: { pair: [string, string]; theme: Theme; ratio: number }[] = []
  for (const theme of ['light', 'dark'] as const) {
    for (const [name, raw] of tokens[theme]) {
      if (!name.endsWith('-foreground')) continue
      // `@theme inline` repeats each pair under a `--color-` name.
      if (name.startsWith('--color-') && raw === `var(--${name.slice('--color-'.length)})`) continue
      const background = name === '--foreground' ? '--background' : name.slice(0, -'-foreground'.length)
      const [fg, bg] = [colour(name, theme, tokens), colour(background, theme, tokens)]
      if (!fg || !bg) continue
      const [hi, lo] = [luminance(fg), luminance(bg)].sort((x, y) => y - x)
      const ratio = (hi! + 0.05) / (lo! + 0.05)
      if (ratio < 4.5) issues.push({ pair: [name, background], theme, ratio })
    }
  }
  return issues
}
