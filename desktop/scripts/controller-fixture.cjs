// Deterministic local API fixture. It never starts a Host or calls a model.
const http = require('node:http')
const fs = require('node:fs/promises')
const path = require('node:path')
const { randomUUID } = require('node:crypto')

async function startFixture({ webRoot, videos = false } = {}) {
  const id = '10000000-0000-4000-8000-000000000001'
  const events = [
    { seq: 1, type: 'user/message', data: { id: 'user-1', blocks: [{ id: 'u1', type: 'text', text: '帮我整理桌面客户端的实现计划。' }] } },
    { seq: 2, type: 'turn/start', data: { turn: 1 } },
    { seq: 3, type: 'assistant/message', data: { turn: 1, step: 1, message: { id: 'assistant-1', blocks: [
      { id: 'a1', type: 'text', text: '可以，沿用 Web 的视觉和交互，建立独立桌面项目。\n\n### 实现计划\n\n| 阶段 | 交付内容 |\n| --- | --- |\n| 原型 | 服务连接与流式聊天 |\n| 完善 | 附件、插件、任务与设置 |\n\n```sh\nnpm install\nnpm run dev\n```\n\n桌面端支持 **macOS、Windows、Linux**。' },
      { id: 'f1', type: 'file', name: 'desktop-plan.txt', description: '桌面端实现计划', file: { source: 'presented', path: '/root/harness/desktop-plan.txt' } },
    ] } } },
    { seq: 4, type: 'turn/end', data: { turn: 1 } },
  ]
  if (videos) events[2].data.message.blocks = [
    { id: 'intro', type: 'text', text: 'Video playback preview' },
    ...['playback.webm', 'second.webm', 'unsupported.mp4'].map((name, index) => ({ id: 'video-' + index, type: 'file', name, description: 'Inline video delivery', file: { source: 'presented', path: '/root/harness/' + name } })),
  ]
  const ordinaryId = '20000000-0000-4000-8000-000000000002'
  const sessions = [{ sessionId: id, title: 'Desktop design', isMain: true, createdAt: '2026-11-25T12:00:00Z' }, { sessionId: ordinaryId, title: 'Project notes', createdAt: '2026-11-25T11:00:00Z' }]
  const streams = new Set(), records = [], uploads = [], answers = []
  let streamCount = 0, permission = { mode: 'danger-full-access' }, sequence = 4
  const model = { defaultEndpointId: 'fixture', endpoints: { fixture: { name: 'Local test', protocol: 'responses', baseUrl: 'http://localhost:1/v1', hasApiKey: true, models: ['desktop-fixture'] } } }
  const plugins = { catalog: [{ pluginId: 'office', package: '@fixture/office', name: 'Office tools', version: '1.0.0', icon: '/assets/plugins/office/1.0.0/icon.svg', description: { en: 'Create and edit documents, spreadsheets and presentations.', 'zh-CN': '创建和编辑文档、电子表格与演示文稿。' }, maintainer: 'Fixture', license: 'MIT' }], settings: { packages: {} }, status: { runtime: 'ready', packages: [], instances: [] } }
  const emit = (type, data, sessionId = id) => { for (const stream of streams) stream.write('event: host\ndata: ' + JSON.stringify({ sessionId, type, data }) + '\n\n') }
  const durable = (type, data) => { const event = { seq: ++sequence, type, data }; events.push(event); emit('session', event) }
  const server = http.createServer(async (req, res) => {
    const url = new URL(req.url, 'http://localhost')
    if (url.pathname.startsWith('/assets/plugins/')) {
      records.push({ path: url.pathname, search: url.search, method: req.method, headers: req.headers })
      res.writeHead(200, { 'Content-Type': 'image/svg+xml' })
      res.end('<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32"><rect width="32" height="32" rx="6" fill="#7863be"/></svg>'); return
    }
    if (!url.pathname.startsWith('/v1/')) {
      if (!webRoot) { res.writeHead(404); res.end(); return }
      let filename = url.pathname
      if (filename === '/' || /^\/(chat|settings|tasks|plugins)(\/|$)/.test(filename)) filename = '/index.html'
      const file = path.resolve(webRoot, '.' + decodeURIComponent(filename))
      if (!file.startsWith(path.resolve(webRoot) + path.sep)) { res.writeHead(403); res.end(); return }
      try {
        const data = await fs.readFile(file)
        const type = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.png': 'image/png', '.woff2': 'font/woff2' }[path.extname(file)] || 'application/octet-stream'
        res.writeHead(200, { 'Content-Type': type }); res.end(data)
      } catch { res.writeHead(404); res.end() }
      return
    }
    records.push({ path: url.pathname, search: url.search, method: req.method, headers: req.headers })
    const send = (body, status = 200) => { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(body)) }
    if (url.pathname === '/v1/host/stream') {
      streamCount++; streams.add(res)
      res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' }); res.write(': connected\n\n')
      const heartbeat = setInterval(() => res.write(': heartbeat\n\n'), 1000)
      res.on('close', () => { streams.delete(res); clearInterval(heartbeat) }); return
    }
    const chunks = []; for await (const chunk of req) chunks.push(chunk)
    const body = Buffer.concat(chunks)
    if (url.pathname === '/v1/capabilities') return send({ protocols: ['responses', 'anthropic_messages'] })
    if (url.pathname === '/v1/accounts') return send({ userId: 'desktop-fixture' }, 201)
    if (url.pathname === '/v1/host/start') return send({ mainSessionId: id })
    if (url.pathname === '/v1/host/stop') return send({})
    if (url.pathname === '/v1/settings/models') return send(model)
    if (/\/title$/.test(url.pathname)) {
      const title = JSON.parse(body).title.trim()
      if (url.pathname.includes(id)) return send({ error: 'main_session_title_fixed' }, 409)
      if (!title) return send({ error: 'invalid_session_title' }, 400)
      const row = sessions.find(row => url.pathname.includes(row.sessionId)); row.title = title
      emit('session', { seq: ++sequence, type: 'session/title', data: { title } }, row.sessionId)
      return send({ title })
    }
    if (/\/model$/.test(url.pathname)) return send({ selection: { endpointId: 'fixture', modelId: 'desktop-fixture' }, status: 'ready' })
    if (url.pathname === '/v1/sessions') {
      if (req.method === 'POST') { const sessionId = randomUUID(); sessions.push({ sessionId, title: '', createdAt: '2026-11-25T13:00:00Z' }); return send({ sessionId }) }
      return send({ sessions })
    }
    if (/\/transcript$/.test(url.pathname)) return send({ events: url.pathname.includes(id) ? events : [], before: null, throughSeq: sequence, queue: [], questions: [], permission })
    if (/\/questions\/[^/]+\/answer$/.test(url.pathname)) { answers.push(JSON.parse(body)); emit('questions.state', { questions: [] }); return send({}) }
    if (/\/permissions$/.test(url.pathname)) {
      if (req.method === 'POST') { permission = { mode: JSON.parse(body).mode }; emit('permissions.state', permission) }
      return send(permission)
    }
    if (/\/messages$/.test(url.pathname)) {
      let content = ''
      if (req.headers['content-type']?.includes('multipart/form-data')) {
        const form = await new Response(body, { headers: { 'Content-Type': req.headers['content-type'] } }).formData()
        content = form.get('content'); for (const [key, value] of form) if (typeof value !== 'string') uploads.push({ key, name: value.name, content: await value.text() })
      } else content = JSON.parse(body).content
      const messageId = randomUUID(), turn = sequence, attemptId = randomUUID(); send({ messageId }, 202)
      durable('user/message', { id: messageId, blocks: [{ id: messageId + '/0', type: 'text', text: content }] })
      durable('turn/start', { turn })
      emit('assistant-stream', { frame: { type: 'start', attemptId, revision: 1, turn, step: 1, startedAfterSeq: sequence } })
      setTimeout(() => emit('assistant-stream', { frame: { type: 'chunk', attemptId, revision: 1, index: 0, chunk: { type: 'text-delta', text: 'Streaming ' } } }), 80)
      setTimeout(() => {
        durable('assistant/message', { turn, step: 1, message: { id: 'answer-' + messageId, blocks: [{ id: 'a2', type: 'text', text: 'Streaming reply received.' }] } })
        durable('turn/end', { turn })
      }, 450)
      return
    }
    if (/\/files\/content$/.test(url.pathname) && /\.(webm|mp4)$/.test(url.searchParams.get('path') || '')) {
      const user = req.headers['x-user-id'] || (url.searchParams.get('inline') === '1' && url.searchParams.get('userId'))
      if (user !== 'desktop-fixture') return send({ error: 'account required' }, 400)
      const data = url.searchParams.get('path').endsWith('unsupported.mp4') ? Buffer.from('unsupported media') : await fs.readFile(path.join(__dirname, 'fixtures/playback.webm'))
      const range = req.headers.range?.match(/^bytes=(\d+)-(\d*)$/)
      const start = range ? Number(range[1]) : 0, end = range && range[2] ? Math.min(Number(range[2]), data.length - 1) : data.length - 1
      const headers = { 'Content-Type': 'video/webm', 'Accept-Ranges': 'bytes', 'Content-Length': String(end - start + 1), 'Content-Disposition': url.searchParams.has('inline') ? 'inline' : 'attachment' }
      if (range) headers['Content-Range'] = `bytes ${start}-${end}/${data.length}`
      res.writeHead(range ? 206 : 200, headers); res.end(req.method === 'HEAD' ? undefined : data.subarray(start, end + 1)); return
    }
    if (/\/files\/content$/.test(url.pathname)) { res.writeHead(200, { 'Content-Type': 'text/plain' }); res.end('Desktop plan\n'); return }
    if (url.pathname === '/v1/host/files') { res.setHeader('X-Harness-Storage', 'docker-volume'); return send({ rootPath: '/root/harness', path: '/root/harness', files: [{ name: 'desktop-plan.txt', path: '/root/harness/desktop-plan.txt', isDir: false, size: 13 }, { name: 'workspaces', path: '/root/harness/workspaces', isDir: true, size: 0 }] }) }
    if (url.pathname === '/v1/host/schedules') return send({ tasks: [{ record: { id: 'task-fixture', title: 'Review desktop implementation', prompt: 'Check the desktop plan', kind: 'daily', time: '09:00', timeZone: 'Asia/Shanghai', scheduledAt: '2026-11-26T01:00:00Z' }, status: 'active', target: { kind: 'session', sessionId: id } }] })
    if (url.pathname === '/v1/settings/plugins' || url.pathname === '/v1/host/plugins/prepare') return send(plugins)
    if (url.pathname === '/v1/host/extensions/authorization') return send([])
    if (url.pathname === '/v1/host/mcp') return send({ status: { servers: [] } })
    if (url.pathname === '/v1/settings/proactive') return send({ enabled: false, phase: 'disabled' })
    if (url.pathname === '/v1/settings/web-search') return send({ provider: 'brave', defaultProvider: 'brave', providers: [{ id: 'brave', name: 'Brave Search' }], entries: { brave: { hasApiKey: false } } })
    send({ error: 'Unknown fixture endpoint: ' + url.pathname }, 404)
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  return { origin: 'http://127.0.0.1:' + server.address().port, id, ordinaryId, sessions, records, uploads, answers, emit, get streamCount() { return streamCount }, disconnect() { for (const stream of streams) stream.end() }, async close() { for (const stream of streams) stream.end(); server.closeAllConnections(); await new Promise(resolve => server.close(resolve)) } }
}
module.exports = { startFixture }
