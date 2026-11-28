// Deterministic controller fixture; no third-party login, tokens or model calls.
const http = require('node:http')
const { once } = require('node:events')
const { randomUUID } = require('node:crypto')
const { startFixture } = require('./controller-fixture.cjs')
async function startOAuthFixture(options = {}) {
 const base = await startFixture(options), attempts = new Map(), answers = [], events = []
 let current, failSubmission = false
 const key = 'oauth-fixture/account', prefix = '/v1/host/extensions/authorization'
 const plugin = { pluginId: 'oauth-fixture', package: '@fixture/oauth', name: 'OAuth receiver test', version: '1.0.0', description: { en: 'Test the native callback receiver.', 'zh-CN': '测试原生回调接收。' }, icon: '/assets/plugins/oauth/icon.svg', maintainer: 'Fixture', license: 'MIT' }
 const entry = () => ({ key, label: 'OAuth receiver test', methods: [{ id: 'oauth', label: 'Connect account' }], inFlight: current?.status === 'running', configured: current?.status === 'authorized', attemptId: current?.id, attemptStatus: current?.status })
 const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost')
  const send = (value, status = 200) => { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(value)) }
  if (options.enableControls && url.pathname === '/__test/reset') { options.redirectUri='http://localhost:8082/callback';current=undefined;answers.length=0;attempts.clear();return send({}) }
  if (options.enableControls && url.pathname === '/__test/root-path') { options.redirectUri='http://localhost:8083';return send({}) }
  if (options.enableControls && url.pathname === '/__test/state') return send({ current, answers, events })
  if (options.enableControls && url.pathname === '/__test/callback') {
   const config = current?.prompts[0]?.loopback
   if (!config) return send({error:'No callback prompt'},409)
   const callback = config.redirectUri + '?' + new URLSearchParams({code:'synthetic-code',state:config.state})
   http.get(callback, reply => { reply.resume(); reply.on('end',()=>send({status:reply.statusCode})) }).on('error',()=>send({error:'Receiver not ready'},503));return
  }
  if (options.enableControls && url.pathname === '/__test/fail-submission') { failSubmission=true;return send({}) }
  if (url.pathname === '/v1/settings/plugins' || url.pathname === '/v1/host/plugins/prepare') return send({ catalog: [plugin], settings: { packages: { '@fixture/oauth': { catalogId: 'oauth-fixture', version: '1.0.0', enabled: true } } }, status: { runtime: 'running', packages: [{ name: '@fixture/oauth', present: true, version: '1.0.0', source: { type: 'directory', spec: '/fixture' } }], instances: [{ id: 'oauth-fixture', package: '@fixture/oauth', state: 'ready' }] } })
  if (url.pathname.startsWith(prefix)) {
   let raw = ''; for await (const part of req) raw += part
   const body = raw ? JSON.parse(raw) : {}
   events.push({ path: url.pathname, method: req.method })
   if (url.pathname === prefix && req.method === 'GET') return send([entry()])
   if (url.pathname === prefix && req.method === 'DELETE') { if (current?.status === 'running') { current.status = 'cancelled'; current.prompts = [] }; return send({}) }
   if (url.pathname === prefix && req.method === 'POST') {
    const state = randomUUID(), redirectUri = options.redirectUri || 'http://localhost:8082/callback'
    const authorizationUrl = 'https://example.test/authorize?' + new URLSearchParams({ state, redirect_uri: redirectUri })
    current = { id: randomUUID(), key, label: 'OAuth receiver test', method: 'oauth', status: 'running', notices: [], prompts: [{ id: randomUUID(), kind: 'text', message: 'Paste the complete redirect URL.', loopback: { authorizationUrl, redirectUri, state } }] }
    attempts.set(current.id, current); return send(current)
   }
   const id = url.pathname.slice(prefix.length + 1).split('/')[0], attempt = attempts.get(id)
   if (!attempt) return send({ error: 'not found' }, 404)
   if (req.method === 'DELETE') { attempt.status = 'cancelled'; attempt.prompts = []; return send(attempt) }
   if (url.pathname.endsWith('/answer')) {
    if (failSubmission) { failSubmission = false; return send({ error: 'simulated unavailable Host' }, 503) }
    const prompt = attempt.prompts.find(p => p.id === body.promptId)
    if (!prompt) return send({ error: 'stale prompt' }, 409)
    const callback = new URL(body.answer)
    if (callback.searchParams.get('state') !== prompt.loopback.state || !callback.searchParams.get('code')) return send({ error: 'invalid callback' }, 400)
    answers.push({ attemptId: id, promptId: body.promptId }); attempt.status = 'authorized'; attempt.prompts = []; return send(attempt)
   }
   return send(attempt)
  }
  const upstream = http.request(base.origin + req.url, { method: req.method, headers: req.headers }, reply => { res.writeHead(reply.statusCode, reply.headers); reply.pipe(res) })
  upstream.on('error', () => { res.writeHead(502); res.end() }); req.pipe(upstream)
 })
 server.listen(options.port || 0, '127.0.0.1'); await once(server, 'listening')
 return { origin: 'http://127.0.0.1:' + server.address().port, get current() { return current }, answers, events, failNextSubmission() { failSubmission = true }, async close() { server.closeAllConnections(); await new Promise(r => server.close(r)); await base.close() } }
}
module.exports = { startOAuthFixture }

if (require.main === module) {
 startOAuthFixture({port:Number(process.env.OAUTH_FIXTURE_PORT||8094),enableControls:true}).then(f => {console.log(f.origin);process.on('SIGTERM',()=>f.close().then(()=>process.exit(0)))})
}
