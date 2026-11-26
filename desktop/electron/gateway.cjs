const { readFile } = require('node:fs/promises')
const path = require('node:path')

const APP_ORIGIN = 'gobot://desktop'
const CSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' blob: data:; font-src 'self'; connect-src 'self'; object-src 'none'; frame-src 'none'; base-uri 'none'; form-action 'self'"

function controllerURL(value) {
  if (typeof value !== 'string' || value.length > 2048) throw new Error('Enter a Controller URL.')
  let url
  try { url = new URL(value.trim()) } catch { throw new Error('Enter a valid Controller URL.') }
  const loopback = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)
  if (url.protocol !== 'https:' && !(url.protocol === 'http:' && loopback)) throw new Error('Use HTTPS, or HTTP on localhost.')
  if (url.username || url.password || url.search || url.hash || url.pathname !== '/') throw new Error('Use the Controller origin without a path, credentials, query or fragment.')
  return url.origin
}

function externalURL(value) {
  try {
    const url = new URL(value)
    return ['https:', 'http:', 'mailto:'].includes(url.protocol) && !url.username && !url.password ? url.href : undefined
  } catch { return undefined }
}

const types = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8', '.json': 'application/json', '.png': 'image/png', '.svg': 'image/svg+xml', '.woff2': 'font/woff2', '.md': 'text/markdown; charset=utf-8', '.txt': 'text/plain; charset=utf-8' }
const requestHeaders = ['accept', 'content-type', 'x-user-id', 'x-client-time-zone', 'x-go-bot-client', 'last-event-id', 'range']
// net.fetch decodes HTTP compression; the upstream byte length may no longer
// describe the returned stream. Let the local protocol determine its length.
const responseHeaders = ['content-type', 'content-disposition', 'content-range', 'accept-ranges', 'x-harness-storage', 'x-harness-storage-source']

function gateway({ root, getController, fetchImpl = fetch }) {
  return async request => {
    try {
      const url = new URL(request.url)
      if (url.protocol !== 'gobot:' || url.host !== 'desktop') return new Response('Forbidden', { status: 403 })
      const pluginAsset = url.pathname.startsWith('/assets/plugins/')
      if (url.pathname.startsWith('/v1/') || pluginAsset) {
        if (pluginAsset && !['GET', 'HEAD'].includes(request.method)) return new Response('Method not allowed', { status: 405 })
        const target = getController()
        if (!target) return Response.json({ error: 'Configure a Controller first.' }, { status: 503 })
        const origin = controllerURL(target)
        const headers = new Headers()
        for (const key of requestHeaders) if (request.headers.has(key)) headers.set(key, request.headers.get(key))
        // The selected Controller, not the internal app origin, owns OAuth callbacks.
        headers.set('Origin', origin)
        headers.set('X-Go-Bot-Controller-Origin', origin)
        const method = request.method
        const response = await fetchImpl(origin + url.pathname + url.search, {
          method, headers, signal: request.signal, redirect: 'manual',
          ...(!['GET', 'HEAD'].includes(method) ? { body: await request.arrayBuffer() } : {}),
        })
        // Never let an API redirect navigate the renderer or leak account headers.
        if (response.status >= 300 && response.status < 400 && response.status !== 304) {
          await response.body?.cancel()
          return Response.json({ error: 'Controller redirected the API request. Check its address or gateway authentication.' }, { status: 502 })
        }
        const returned = new Headers({ 'Cache-Control': 'no-store', 'Content-Security-Policy': CSP, 'X-Content-Type-Options': 'nosniff' })
        for (const key of responseHeaders) if (response.headers.has(key)) returned.set(key, response.headers.get(key))
        // Preserve the body as a stream: buffering it would break SSE playback.
        return new Response(response.body, { status: response.status, headers: returned })
      }
      if (!['GET', 'HEAD'].includes(request.method)) return new Response('Method not allowed', { status: 405 })
      let name = decodeURIComponent(url.pathname)
      if (name.includes('\0') || name.includes('\\')) return new Response('Forbidden', { status: 403 })
      if (name === '/' || /^\/(?:chat\/[\da-f-]{36}|settings\/(?:models|web-search|image-generation|proactive)|tasks|plugins)$/.test(name)) name = '/index.html'
      const file = path.resolve(root, '.' + name)
      if (!file.startsWith(path.resolve(root) + path.sep)) return new Response('Forbidden', { status: 403 })
      const data = await readFile(file)
      return new Response(request.method === 'HEAD' ? null : data, { headers: {
        'Content-Type': types[path.extname(file)] || 'application/octet-stream',
        'Content-Security-Policy': CSP, 'X-Content-Type-Options': 'nosniff', 'Cache-Control': 'no-cache',
      } })
    } catch (error) {
      if (error.name === 'AbortError') throw error
      return Response.json({ error: error.code === 'ENOENT' ? 'Not found' : 'Controller unavailable. Check its address and connection.' }, { status: error.code === 'ENOENT' ? 404 : 502 })
    }
  }
}

module.exports = { APP_ORIGIN, CSP, controllerURL, externalURL, gateway }
