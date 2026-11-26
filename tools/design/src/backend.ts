type Json = unknown
type Schema = Record<string, any>
type Spec = { paths?: Record<string, Record<string, any>>; components?: Record<string, any> }

export type Missing = { endpoint: string; fields: string[] }

function resolve(spec: Spec, schema: Schema | undefined, seen: Set<string>): Schema[] {
  if (!schema || typeof schema !== 'object') return []
  if (typeof schema.$ref === 'string') {
    if (seen.has(schema.$ref)) return []
    const target = schema.$ref.replace(/^#\//, '').split('/').reduce<any>((n, k) => n?.[k], spec)
    return resolve(spec, target, new Set(seen).add(schema.$ref))
  }
  const branches = [schema.allOf, schema.oneOf, schema.anyOf].flatMap((l) => (Array.isArray(l) ? l : []))
  return [schema, ...branches.flatMap((b) => resolve(spec, b, seen))]
}

// A branch that composes others (allOf/oneOf/anyOf) says nothing about keys
// itself; its members are separate branches. A non-object branch, such as a
// nullable union's `{type: "null"}`, takes no keys either.
function isOpenObject(b: Schema): boolean {
  if (b.allOf || b.oneOf || b.anyOf) return false
  const t = b.type
  if (t !== undefined && t !== 'object' && !(Array.isArray(t) && t.includes('object'))) return false
  return !b.properties || b.additionalProperties === true
}

// Dotted paths of fields in `value` that no branch of `schema` declares. A
// branch without `properties`, or with a schema-valued `additionalProperties`,
// is open: its keys are free and only their values are walked further.
function unknownFields(spec: Spec, schema: Schema | undefined, value: Json, at: string, out: string[]) {
  const branches = resolve(spec, schema, new Set())
  if (Array.isArray(value)) {
    const items = branches.map((b) => b.items).filter(Boolean)
    for (const v of value) for (const i of items) unknownFields(spec, i, v, at, out)
    return
  }
  if (value === null || typeof value !== 'object') return
  const open = branches.length === 0 || branches.some(isOpenObject)
  for (const [key, v] of Object.entries(value)) {
    const declared = branches.map((b) => b.properties?.[key]).filter(Boolean)
    const extra = branches.map((b) => b.additionalProperties).filter((a) => a && typeof a === 'object')
    const path = at ? `${at}.${key}` : key
    if (declared.length) for (const d of declared) unknownFields(spec, d, v, path, out)
    else if (extra.length) for (const e of extra) unknownFields(spec, e, v, path, out)
    else if (!open) out.push(path)
  }
}

function findPath(spec: Spec, concrete: string): string | undefined {
  const parts = concrete.split('/')
  let best: string | undefined
  let bestParams = Infinity
  for (const tpl of Object.keys(spec.paths ?? {})) {
    const t = tpl.split('/')
    if (t.length !== parts.length) continue
    const params = t.filter((s) => s.startsWith('{')).length
    const ok = t.every((s, i) => (s.startsWith('{') ? parts[i] !== '' : s === parts[i]))
    if (ok && params < bestParams) [best, bestParams] = [tpl, params]
  }
  return best
}

// Fixtures may hold text or other non-JSON bodies; those have no fields to
// compare, so they are skipped rather than reported.
function parse(body: unknown): Json | undefined {
  if (typeof body !== 'string') return body
  try {
    return JSON.parse(body)
  } catch {
    return undefined
  }
}

// One entry per fixture key the spec does not fully cover, in key order. An
// endpoint the spec lacks (unknown path, or no such method on it) has
// `fields: []`; an endpoint it has lists dotted paths of the undeclared body
// fields (array elements add no segment). Keys fully covered are not listed.
export function missingBackend(fixture: Record<string, { status?: number; body?: unknown } | null>, openapi: object): Missing[] {
  const spec = openapi as Spec
  const result: Missing[] = []
  for (const [key, entry] of Object.entries(fixture)) {
    const [method = '', target = ''] = key.split(/\s+/, 2)
    const bare = target.replace(/\?.*$/, '')
    const endpoint = `${method.toUpperCase()} ${bare}`
    const tpl = findPath(spec, bare)
    const op = tpl && spec.paths?.[tpl]?.[method.toLowerCase()]
    if (!op) {
      result.push({ endpoint, fields: [] })
      continue
    }
    const status = entry?.status ?? 200
    const response = op.responses?.[String(status)] ?? op.responses?.default
    const schema = response?.content?.['application/json']?.schema
    const body = parse(entry?.body)
    if (!schema || body === undefined) continue
    const fields: string[] = []
    unknownFields(spec, schema, body, '', fields)
    if (fields.length) result.push({ endpoint, fields: [...new Set(fields)] })
  }
  return result
}
