import assert from 'node:assert/strict'
import fs from 'node:fs'
import { test } from 'node:test'
import { missingBackend } from '../src/backend.ts'

const openapi = JSON.parse(
  fs.readFileSync(new URL('../../../internal/transport/http/httpapi/openapi.json', import.meta.url), 'utf8')
)
const entry = (body: unknown) => ({ status: 200, type: 'application/json', body: JSON.stringify(body) })

test('a query string does not make an endpoint unknown', () => {
  const r = missingBackend({ 'GET /api/v1/jobs?phase=ended&limit=200': entry({ items: [] }) }, openapi)
  assert.deepEqual(r, [])
})

test('a concrete path matches its template', () => {
  const r = missingBackend({ 'GET /api/v1/devices/abc': entry({}) }, openapi)
  assert.deepEqual(r, [])
})

test('an endpoint the spec lacks is reported with no fields', () => {
  const r = missingBackend({ 'GET /api/v1/cameras/x/snapshots': entry({ a: 1 }) }, openapi)
  assert.deepEqual(r, [{ endpoint: 'GET /api/v1/cameras/x/snapshots', fields: [] }])
})

test('an extra body field is listed, nested through $ref and arrays', () => {
  const jobs = missingBackend({ 'GET /api/v1/jobs': entry({ items: [{ fps: 30 }], cursor: 'x' }) }, openapi)
  assert.deepEqual(jobs, [{ endpoint: 'GET /api/v1/jobs', fields: ['items.fps'] }])
  const top = missingBackend({ 'GET /api/v1/devices/abc': entry({ fps: 30 }) }, openapi)
  assert.deepEqual(top, [{ endpoint: 'GET /api/v1/devices/abc', fields: ['fps'] }])
})

const mini = {
  paths: {
    '/devices/{id}': { get: { responses: { '200': { content: { 'application/json': { schema: { $ref: '#/components/schemas/Wrapped' } } } } } } },
    '/devices/stats': { get: { responses: { '200': { content: { 'application/json': { schema: { type: 'object', properties: { n: {} }, additionalProperties: false } } } } } } }
  },
  components: {
    schemas: {
      A: { type: 'object', properties: { id: { type: 'string' } }, additionalProperties: false },
      Wrapped: { allOf: [{ $ref: '#/components/schemas/A' }], description: 'wrapper' }
    }
  }
}

test('an allOf wrapper does not make the object open', () => {
  const r = missingBackend({ 'GET /devices/abc': entry({ id: 'x', fps: 1 }) }, mini)
  assert.deepEqual(r, [{ endpoint: 'GET /devices/abc', fields: ['fps'] }])
})

test('a literal path beats a template', () => {
  const r = missingBackend({ 'GET /devices/stats': entry({ n: 1, extra: 2 }) }, mini)
  assert.deepEqual(r, [{ endpoint: 'GET /devices/stats', fields: ['extra'] }])
})

test('a missing method on a known path is reported with no fields', () => {
  const r = missingBackend({ 'POST /devices/abc': entry({}) }, mini)
  assert.deepEqual(r, [{ endpoint: 'POST /devices/abc', fields: [] }])
})

test('a non-JSON body is skipped', () => {
  const r = missingBackend({ 'GET /devices/abc': { status: 200, body: 'plain text' } }, mini)
  assert.deepEqual(r, [])
})
