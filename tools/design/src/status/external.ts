import fs from 'node:fs'

const ADDRESS = `(https?:\\/\\/[^'"()\\s;>]+)`
const FETCHED = [
  new RegExp(`\\burl\\(\\s*['"]?${ADDRESS}`, 'g'),
  new RegExp(`@import\\s+['"]${ADDRESS}`, 'g'),
  new RegExp(`<(?:img|script)\\b[^>]*?\\ssrc=['"]?${ADDRESS}`, 'g'),
  new RegExp(`<link\\b[^>]*?\\shref=['"]?${ADDRESS}`, 'g'),
]

/**
 * Every address outside the app a file makes the browser fetch: a CSS
 * `url()` or `@import`, and the source of an image, script or stylesheet
 * link. A link a reader follows, such as `<a href>`, is not one.
 */
export function externalResources(file: string): string[] {
  const text = fs.readFileSync(file, 'utf8')
  return [...new Set(FETCHED.flatMap((re) => [...text.matchAll(re)].map((m) => m[1]!)))]
}
