import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'

const source = await readFile(new URL('../src/api.ts', import.meta.url), 'utf8')
const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ES2022, target: ts.ScriptTarget.ES2022 } }).outputText
const { createApiClient, ApiError } = await import(`data:text/javascript;base64,${Buffer.from(output).toString('base64')}`)

test('preserves typed errors, dependency and request ID', async () => {
  const client = createApiClient({ fetch: async () => new Response(JSON.stringify({ code: 'record_referenced', message: '配置被引用', dependency: { id: 'route-id', kind: 'sync-rules', name: '官方群同步' } }), { status: 409, headers: { 'X-Request-ID': 'diagnostic-id' } }) })
  await assert.rejects(client('/api/v1/records'), error => error instanceof ApiError && error.code === 'record_referenced' && error.requestId === 'diagnostic-id' && error.dependency.id === 'route-id' && error.message.includes('官方群同步'))
})
test('uses same-origin cookies, no cache, JSON headers and caller headers', async () => {
  let called
  const client = createApiClient({ fetch: async (url, init) => { called = init; return new Response(null, { status: 204 }) } })
  assert.equal(await client('/api/v1/records', { method: 'POST', body: '{}', headers: new Headers({ 'X-Test': 'preserved' }) }), undefined)
  assert.equal(called.credentials, 'same-origin'); assert.equal(called.cache, 'no-store')
  assert.equal(called.headers.get('Content-Type'), 'application/json'); assert.equal(called.headers.get('X-Test'), 'preserved')
})
test('expires a request that never responds', async () => {
  const client = createApiClient({ timeoutMs: 10, fetch: async (_url, init) => new Promise((_resolve, reject) => init.signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))) })
  await assert.rejects(client('/api/v1/records'), error => error.code === 'request_timeout')
})
test('caller can cancel an in-flight request', async () => {
  const controller = new AbortController()
  const client = createApiClient({ fetch: async (_url, init) => new Promise((_resolve, reject) => init.signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))) })
  const pending = client('/api/v1/records', { signal: controller.signal }); controller.abort()
  await assert.rejects(pending, error => error.code === 'request_cancelled')
})
test('already cancelled requests never reach fetch', async () => {
  let fetched = false
  const controller = new AbortController(); controller.abort()
  const client = createApiClient({ fetch: async () => { fetched = true; return new Response('{}') } })
  await assert.rejects(client('/api/v1/records', { signal: controller.signal }), error => error.code === 'request_cancelled')
  assert.equal(fetched, false)
})
test('only management 401 invalidates the console session', async () => {
  let unauthorized = 0
  const client = createApiClient({ fetch: async () => new Response('{}', { status: 401 }), onUnauthorized: () => unauthorized++ })
  await assert.rejects(client('/api/v1/auth/login')); assert.equal(unauthorized, 0)
  await assert.rejects(client('/api/v1/records')); assert.equal(unauthorized, 1)
  const upstream = createApiClient({ fetch: async () => new Response('{}', { status: 502 }), onUnauthorized: () => unauthorized++ })
  await assert.rejects(upstream('/api/v1/adapter/test')); assert.equal(unauthorized, 1)
})
test('HTML error responses do not leak raw internal error output', async () => {
  const client = createApiClient({ fetch: async () => new Response('<html>internal details</html>', { status: 500 }) })
  await assert.rejects(client('/api/v1/logs'), error => error instanceof ApiError && error.status === 500 && !error.message.includes('internal details'))
})
