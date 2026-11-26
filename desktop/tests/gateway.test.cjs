const { test } = require('node:test')
const assert = require('node:assert/strict')
const { mkdtemp, writeFile, rm } = require('node:fs/promises')
const { tmpdir } = require('node:os')
const { join } = require('node:path')
const { controllerURL, externalURL, gateway } = require('../electron/gateway.cjs')

test('Controller configuration accepts an origin and rejects credentials, remote cleartext and path ambiguity', () => {
  assert.equal(controllerURL(' http://127.0.0.1:8080/ '), 'http://127.0.0.1:8080')
  assert.equal(controllerURL('https://bot.example.com/'), 'https://bot.example.com')
  for (const value of ['http://remote.example.com', 'https://alice:secret@example.com', 'https://example.com/v1', 'https://example.com/?key=secret', 'https://example.com/#a', 'file:///tmp/a', 'javascript:alert(1)']) assert.throws(() => controllerURL(value))
})

test('external navigation permits only browser links, never file or application protocols', () => {
  assert.equal(externalURL('https://example.com/login'), 'https://example.com/login')
  for (const url of ['file:///etc/passwd', 'gobot://desktop/', 'javascript:alert(1)', 'https://user:password@example.com']) assert.equal(externalURL(url), undefined)
})

test('gateway forwards multipart data, scopes headers and uses the Controller origin for OAuth', async () => {
  const form = new FormData(); form.set('content', 'hello'); form.set('files', new Blob(['attachment']), 'note.txt')
  const input = new Request('gobot://desktop/v1/sessions/id/messages', { method: 'POST', body: form, headers: { 'X-User-ID': 'alice', Cookie: 'private=1', Authorization: 'untrusted', 'X-Go-Bot-Controller-Origin': 'gobot://desktop' } })
  let called = false
  const run = gateway({ root: '/unused', getController: () => 'https://controller.example', fetchImpl: async (url, options) => {
    called = true
    assert.equal(url, 'https://controller.example/v1/sessions/id/messages')
    assert.equal(options.headers.get('origin'), 'https://controller.example')
    assert.equal(options.headers.get('x-go-bot-controller-origin'), 'https://controller.example')
    assert.equal(options.headers.get('x-user-id'), 'alice')
    assert.equal(options.headers.has('cookie'), false)
    assert.equal(options.headers.has('authorization'), false)
    const decoded = await new Response(options.body, { headers: options.headers }).formData()
    assert.equal(decoded.get('content'), 'hello'); assert.equal(await decoded.get('files').text(), 'attachment')
    return Response.json({ messageId: 'ok' }, { status: 202, headers: { 'Content-Length': '999' } })
  } })
  const result = await run(input)
  assert.equal(result.headers.has('content-length'), false, 'upstream compressed length must not describe a decoded stream')
  assert.equal(called, true); assert.equal(result.status, 202); assert.deepEqual(await result.json(), { messageId: 'ok' })
})

test('SSE reaches the renderer before upstream closes, preserving cancellation', async () => {
  let upstream, receivedSignal
  const abort = new AbortController()
  const body = new ReadableStream({ start(controller) { upstream = controller; controller.enqueue(new TextEncoder().encode('event: host\ndata: {}\n\n')) } })
  const run = gateway({ root: '/unused', getController: () => 'http://localhost:8080', fetchImpl: async (_url, options) => { receivedSignal = options.signal; return new Response(body, { headers: { 'Content-Type': 'text/event-stream' } }) } })
  const input = new Request('gobot://desktop/v1/host/stream?cursors=%7B%7D', { signal: abort.signal })
  const result = await run(input)
  assert.match(result.headers.get('content-type'), /text\/event-stream/)
  const reader = result.body.getReader()
  assert.match(new TextDecoder().decode((await reader.read()).value), /event: host/)
  abort.abort(); assert.equal(receivedSignal.aborted, true)
  upstream.close(); await reader.cancel()
})

test('redirects cannot send account headers to a second origin', async () => {
  const run = gateway({ root: '/unused', getController: () => 'https://controller.example', fetchImpl: async (_url, options) => {
    assert.equal(options.redirect, 'manual')
    return new Response(null, { status: 302, headers: { Location: 'https://another.example/' } })
  } })
  assert.equal((await run(new Request('gobot://desktop/v1/accounts'))).status, 502)
})

test('local protocol serves nested app routes and enforces asset boundaries and CSP', async () => {
  const root = await mkdtemp(join(tmpdir(), 'gobot-gateway-'))
  try {
    await writeFile(join(root, 'index.html'), '<main>Go Bot</main>')
    const run = gateway({ root, getController: () => undefined })
    for (const route of ['/', '/tasks', '/plugins', '/settings/models', '/chat/00000000-0000-0000-0000-000000000000']) {
      const response = await run(new Request('gobot://desktop' + route))
      assert.equal(response.status, 200)
      assert.match(response.headers.get('content-security-policy'), /script-src 'self'/)
      assert.match(await response.text(), /Go Bot/)
    }
    assert.equal((await run(new Request('gobot://wrong/index.html'))).status, 403)
    assert.equal((await run(new Request('gobot://desktop/assets/missing.js'))).status, 404)
    assert.equal((await run(new Request('gobot://desktop/%2e%2e%2fpackage.json'))).status, 403)
    assert.equal((await run(new Request('gobot://desktop/v1/accounts'))).status, 503)
  } finally { await rm(root, { recursive: true, force: true }) }
})

test('plugin assets stream from the Controller with HEAD and byte ranges', async () => {
  const run = gateway({ root: '/unused', getController: () => 'https://controller.example', fetchImpl: async (url, options) => {
    assert.equal(url, 'https://controller.example/assets/plugins/office/1.0.0/tutorial.mp4')
    assert.equal(options.headers.get('range'), 'bytes=0-3')
    assert.equal(options.method, 'GET')
    return new Response('data', { status: 206, headers: { 'Content-Type': 'video/mp4', 'Content-Range': 'bytes 0-3/10', 'Accept-Ranges': 'bytes' } })
  } })
  const response = await run(new Request('gobot://desktop/assets/plugins/office/1.0.0/tutorial.mp4', { headers: { Range: 'bytes=0-3' } }))
  assert.equal(response.status, 206)
  assert.equal(response.headers.get('content-range'), 'bytes 0-3/10')
  assert.equal(await response.text(), 'data')
  assert.equal((await run(new Request('gobot://desktop/assets/plugins/office/icon.svg', { method: 'POST' }))).status, 405)
  const head = gateway({ root: '/unused', getController: () => 'https://controller.example', fetchImpl: async (_url, options) => {
    assert.equal(options.method, 'HEAD'); return new Response(null, { headers: { 'Content-Type': 'image/svg+xml' } })
  } })
  assert.equal((await head(new Request('gobot://desktop/assets/plugins/office/icon.svg', { method: 'HEAD' }))).headers.get('content-type'), 'image/svg+xml')
})

test('installation identity reaches the Controller stream unchanged', async () => {
  const value = encodeURIComponent(JSON.stringify({ id: 'client-persistent', type: 'desktop', platform: 'darwin' }))
  const run = gateway({ root: '/unused', getController: () => 'http://localhost:8080', fetchImpl: async (_url, options) => {
    assert.equal(options.headers.get('x-go-bot-client'), value); return new Response(': ready\n\n')
  } })
  assert.equal((await run(new Request('gobot://desktop/v1/host/stream', { headers: { 'X-Go-Bot-Client': value } }))).status, 200)
})
