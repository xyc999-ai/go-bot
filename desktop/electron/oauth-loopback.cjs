const http = require('node:http')

function configuration(value) {
  const login = new URL(value.authorizationUrl), redirect = new URL(value.redirectUri)
  if (login.protocol !== 'https:' || login.username || login.password || redirect.protocol !== 'http:' ||
      !['localhost', '127.0.0.1', '[::1]'].includes(redirect.hostname) || !redirect.port || Number(redirect.port) < 1 || redirect.username || redirect.password || redirect.search || redirect.hash ||
      !value.state || login.searchParams.getAll('state').length !== 1 || login.searchParams.get('state') !== value.state ||
      login.searchParams.getAll('redirect_uri').length !== 1 || login.searchParams.get('redirect_uri') !== value.redirectUri) throw new Error('INVALID_CALLBACK')
  return { login, redirect, state: value.state }
}
function callbackURL(raw, host, origin, config) {
 try {
  const actual = new URL(raw, config.redirect)
  if (!raw.startsWith('/') || raw.startsWith('//') || origin || host !== config.redirect.host || actual.origin !== config.redirect.origin || actual.pathname !== config.redirect.pathname || actual.hash ||
      actual.searchParams.getAll('state').length !== 1 || actual.searchParams.get('state') !== config.state) return undefined
  const codes = actual.searchParams.getAll('code'), errors = actual.searchParams.getAll('error')
  if (!((codes.length === 1 && codes[0] && !errors.length) || (errors.length === 1 && errors[0] && !codes.length))) return undefined
  return actual.href
 } catch { return undefined }
}

// One receiver per app; no URL/code is written to disk or emitted to logs.
function createLoopbackReceiver({ openExternal, timeoutMs = 300000 }) {
  let active
  async function cancel(id) { if (active && (!id || active.id === id)) active.finish(new Error('CANCELLED')) }
  async function open(id, value) {
    if (typeof id !== 'string' || !id || active) throw new Error('RECEIVER_BUSY')
    const config = configuration(value), servers = [], sockets = new Set()
    let timer, consumed = false
    return new Promise((resolve, reject) => {
      const record = { id, finish }
      active = record
      function finish(error, url) {
        if (active !== record) return
        active = undefined; clearTimeout(timer)
        for (const server of servers) server.close()
        for (const socket of sockets) socket.destroy()
        error ? reject(error) : resolve(url)
      }
      const handle = (req, res) => {
        const url = !consumed && req.method === 'GET' && callbackURL(req.url, req.headers.host, req.headers.origin, config)
        if (url) consumed = true
        const body = url ? 'Authorization received. You can return to Go Bot.' : 'This request does not match the active authorization.'
        res.writeHead(url ? 200 : 400, { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store', 'Referrer-Policy': 'no-referrer', 'Content-Security-Policy': "default-src 'none'", 'Connection': 'close' })
        res.end('<!doctype html><meta name="viewport" content="width=device-width"><title>Go Bot</title><p>' + body + '</p>', () => { if (url) finish(undefined, url) })
      }
      const hosts = config.redirect.hostname === 'localhost' ? ['127.0.0.1', '::1'] : [config.redirect.hostname.replace(/^\[|\]$/g, '')]
      Promise.all(hosts.map(host => new Promise((ready, fail) => {
        const server = http.createServer({ maxHeaderSize: 16384 }, handle); servers.push(server)
        server.headersTimeout = 10000; server.requestTimeout = 10000
        server.on('connection', socket => { sockets.add(socket); socket.on('close', () => sockets.delete(socket)); socket.setTimeout(10000, () => socket.destroy()) })
        server.once('error', error => { if (host === '::1' && ['EAFNOSUPPORT', 'EADDRNOTAVAIL'].includes(error.code)) ready(); else fail(new Error(error.code === 'EADDRINUSE' ? 'PORT_IN_USE' : 'LISTENER_FAILED')) })
        server.listen({ host, port: Number(config.redirect.port), ipv6Only: true, exclusive: true }, ready)
      }))).then(async () => {
        if (active !== record) { for (const server of servers) server.close(); return }
        timer = setTimeout(() => finish(new Error('TIMED_OUT')), timeoutMs)
        try { await openExternal(config.login.href) } catch { finish(new Error('BROWSER_FAILED')) }
      }, error => finish(error))
    })
  }
  return { open, cancel }
}
module.exports = { createLoopbackReceiver, configuration, callbackURL }
