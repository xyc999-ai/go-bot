const { test } = require('node:test')
const assert = require('node:assert/strict')
const http = require('node:http')
const { once } = require('node:events')
const { createLoopbackReceiver } = require('../electron/oauth-loopback.cjs')
async function port() { const s = http.createServer(); s.listen(0, '127.0.0.1'); await once(s, 'listening'); const p = s.address().port; await new Promise(r => s.close(r)); return p }
function config(port, host = '127.0.0.1') { const redirectUri = `http://${host}:${port}/callback`; return { redirectUri, state: 'state-value', authorizationUrl: 'https://example.test/authorize?' + new URLSearchParams({ redirect_uri: redirectUri, state: 'state-value' }) } }
function request(url, headers = {}) { return new Promise((resolve, reject) => { http.get(url, { headers }, response => { let body = ''; response.on('data', value => body += value); response.on('end', () => resolve({ status: response.statusCode, headers: response.headers, body })) }).on('error', reject) }) }
test('binds before browser opens, rejects unrelated requests, then returns exactly one callback', async () => {
 const p = await port(), value = config(p)
 const opened = Promise.withResolvers()
 const receiver = createLoopbackReceiver({ openExternal: url => opened.resolve(url) })
 const result = receiver.open('attempt:prompt', value)
 assert.equal(await opened.promise, value.authorizationUrl)
 for (const suffix of ['?code=a&state=wrong', '?code=a&code=b&state=state-value', '?code=a&state=state-value&state=wrong', '?code=a&error=no&state=state-value']) {
  assert.equal((await request(value.redirectUri + suffix)).status, 400)
 }
 assert.equal((await request(value.redirectUri + '?code=a&state=state-value', { Origin: 'https://evil.test' })).status, 400)
 assert.equal((await request(value.redirectUri.replace('/callback', '/other') + '?code=a&state=state-value')).status, 400)
 const url = value.redirectUri + '?code=private-code&state=state-value'
 const response = await request(url)
 assert.equal(response.status, 200); assert.equal(response.headers['cache-control'], 'no-store'); assert.ok(!response.body.includes('private-code'))
 assert.equal(await result, url)
 await assert.rejects(request(url), /ECONNREFUSED/)
})
test('port conflict never opens browser; cancellation closes all sockets and permits fresh attempt', async () => {
 const server = http.createServer(); server.listen(0, '127.0.0.1'); await once(server, 'listening')
 const value = config(server.address().port); let opened = 0
 const receiver = createLoopbackReceiver({ openExternal: () => opened++ })
 await assert.rejects(receiver.open('one', value), /PORT_IN_USE/); assert.equal(opened, 0)
 await new Promise(resolve => server.close(resolve))
 const ready = Promise.withResolvers()
 const retry = createLoopbackReceiver({ openExternal: () => ready.resolve() })
 const pending = retry.open('two', value); await ready.promise
 const rejected = assert.rejects(pending, /CANCELLED/)
 await retry.cancel('stale'); assert.equal((await request(value.redirectUri)).status, 400)
 await retry.cancel('two'); await rejected
 const pendingAgain = retry.open('three', value); await new Promise(resolve => setTimeout(resolve, 15))
 await retry.cancel('three'); await assert.rejects(pendingAgain, /CANCELLED/)
})
test('timeout, browser failure and OAuth denial release the receiver', async () => {
 const value = config(await port())
 const timeout = createLoopbackReceiver({ openExternal: () => {}, timeoutMs: 15 })
 await assert.rejects(timeout.open('timeout', value), /TIMED_OUT/)
 const failed = createLoopbackReceiver({ openExternal: () => { throw new Error('browser') } })
 await assert.rejects(failed.open('browser', value), /BROWSER_FAILED/)
 const ready = Promise.withResolvers()
 const denial = createLoopbackReceiver({ openExternal: () => ready.resolve() })
 const result = denial.open('denial', value); await ready.promise
 const url = value.redirectUri + '?error=access_denied&state=state-value'
 assert.equal((await request(url)).status, 200); assert.equal(await result, url)
})
test('rejects non-loopback destinations or inconsistent metadata', async () => {
 const receiver = createLoopbackReceiver({ openExternal: () => assert.fail('must not open') })
 const value = config(await port())
 for (const changed of [{ redirectUri: 'http://example.com:8082/callback' }, { state: 'wrong' }, { authorizationUrl: value.authorizationUrl.replace('https:', 'http:') }]) {
  await assert.rejects(receiver.open('invalid', { ...value, ...changed }), /INVALID_CALLBACK/)
 }
})
test('localhost accepts browser IPv4 and IPv6 resolutions and rejects another active receiver', async () => {
 const value = config(await port(), 'localhost'), ready = Promise.withResolvers()
 const receiver = createLoopbackReceiver({ openExternal: () => ready.resolve() })
 const pending = receiver.open('a', value); await ready.promise
 await assert.rejects(receiver.open('b', value), /RECEIVER_BUSY/)
 const suffix = '?code=ok&state=state-value'
 const res = await request(value.redirectUri + suffix)
 assert.equal(res.status, 200); assert.equal(await pending, value.redirectUri + suffix)
})
test('gws callback with no explicit slash accepts the browser root path', async () => {
 const redirectUri = 'http://localhost:' + await port(), value = { redirectUri, state: 'gws-state', authorizationUrl: 'https://example.test/login?' + new URLSearchParams({redirect_uri:redirectUri,state:'gws-state'}) }, ready=Promise.withResolvers()
 const receiver = createLoopbackReceiver({openExternal:()=>ready.resolve()})
 const pending = receiver.open('gws',value);await ready.promise
 const url=redirectUri+'/?code=gws-code&state=gws-state'
 assert.equal((await request(url)).status,200);assert.equal(await pending,url)
})
