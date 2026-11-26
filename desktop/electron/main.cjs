const { app, BrowserWindow, Menu, Tray, nativeImage, Notification, ipcMain, protocol, net, shell, session, powerMonitor, screen } = require('electron')
const fs = require('node:fs')
const path = require('node:path')
const { APP_ORIGIN, controllerURL, externalURL, gateway } = require('./gateway.cjs')

// Installer lifecycle events must not create a window or launch an Agent.
const installerStartup = require('./installer.cjs').installerStartup()
if (process.env.GOBOT_DESKTOP_USER_DATA) app.setPath('userData', path.resolve(process.env.GOBOT_DESKTOP_USER_DATA))
protocol.registerSchemesAsPrivileged([{ scheme: 'gobot', privileges: { standard: true, secure: true, supportFetchAPI: true, corsEnabled: true, stream: true } }])

let win, tray, config = {}, quitting = false
let identity
let language = 'en'
const { menuTemplate, trayTemplate } = require('./menus.cjs')
let controller = 'http://127.0.0.1:8080'
const configFile = () => path.join(app.getPath('userData'), 'desktop.json')
async function loadConfig() {
  try { config = JSON.parse(fs.readFileSync(configFile(), 'utf8')) } catch { config = {} }
  // The installed client always targets the local Controller; the environment
  // override is used by isolated native integration tests. Old preferences expire.
  controller = controllerURL(process.env.GOBOT_DESKTOP_CONTROLLER || 'http://127.0.0.1:8080')
  delete config.controller
  delete config.keepInTray
  const { computerName, desktopIdentity } = require('./client-identity.cjs')
  identity = desktopIdentity(await computerName())
  delete config.clientId
  language = config.language === 'zh-CN' || config.language === 'en' ? config.language : /^zh/i.test(app.getLocale()) ? 'zh-CN' : 'en'
  persist()
}
function persist() {
  fs.mkdirSync(path.dirname(configFile()), { recursive: true })
  const temporary = configFile() + '.tmp'
  fs.writeFileSync(temporary, JSON.stringify(config, null, 2), { mode: 0o600 })
  fs.renameSync(temporary, configFile())
}
function show() { if (win) { if (win.isMinimized()) win.restore(); win.show(); win.focus() } }
function command(value) { show(); win?.webContents.send('desktop:command', value) }
function validSender(event) {
  if (event.sender !== win?.webContents || event.senderFrame !== win.webContents.mainFrame || !event.senderFrame.url.startsWith(APP_ORIGIN + '/')) throw new Error('Untrusted sender')
}
function handle(name, fn) { ipcMain.handle(name, async (event, ...args) => { validSender(event); return fn(...args) }) }
function openExternal(value) { const url = externalURL(value); if (url) void shell.openExternal(url) }

function syncTray() {
  if (process.platform === 'darwin') return // macOS restores the window from its Dock icon.
  if (!tray) {
    tray = new Tray(nativeImage.createFromPath(path.join(__dirname, '../dist/tray.png')).resize({ width: 18, height: 18 }))
    tray.setToolTip('Go Bot')
    tray.on('click', show)

  }
  tray.setContextMenu(Menu.buildFromTemplate(trayTemplate(language, show)))
}

function updateMenus() {
  Menu.setApplicationMenu(Menu.buildFromTemplate(menuTemplate(language, process.platform, () => command({ type: 'new-chat' }), () => openExternal('https://github.com/xyc999-ai/go-bot/releases'))))
  syncTray()
}

function setupIPC() {
  handle('desktop:language', value => {
    if (value !== 'en' && value !== 'zh-CN') throw new Error('Invalid language')
    if (value !== language) { language = value; config.language = value; persist(); updateMenus() }
  })
  handle('desktop:configuration', () => ({ controller, version: app.getVersion(), identity }))
  const notifications = new Map()
  handle('desktop:notify', value => {
    if (!Notification.isSupported() || win.isFocused()) return false
    if (!value || !/^[\da-f-]{36}$/i.test(value.sessionId) || !['approval', 'question', 'completed'].includes(value.kind)) return false
    const key = value.sessionId + ':' + value.kind
    if (Date.now() - (notifications.get(key) || 0) < 30000) return false
    if (notifications.size > 200) notifications.clear()
    notifications.set(key, Date.now())
    const texts = { approval: 'A chat needs your approval / 会话需要你的审批', question: 'A chat has a question / 会话有待回答的问题', completed: 'A reply is ready / 回复已完成' }
    const notice = new Notification({ title: 'Go Bot', body: texts[value.kind] })
    notice.on('click', () => command({ type: 'navigate', path: '/chat/' + value.sessionId }))
    notice.show(); return true
  })
}

function createWindow() {
  const bounds = config.bounds
  const usableBounds = bounds && Number.isInteger(bounds.width) && Number.isInteger(bounds.height) && bounds.width >= 960 && bounds.height >= 680 &&
    Number.isInteger(bounds.x) && Number.isInteger(bounds.y) && screen.getAllDisplays().some(({ workArea: a }) => bounds.x + bounds.width > a.x + 80 && bounds.x < a.x + a.width - 80 && bounds.y + bounds.height > a.y + 80 && bounds.y < a.y + a.height - 80)
  win = new BrowserWindow({
    width: 1440, height: 960, minWidth: 960, minHeight: 680,
    ...(usableBounds ? bounds : {}),
    title: 'Go Bot', icon: path.join(__dirname, '../assets/icon.png'), backgroundColor: '#ffffff', show: false,
    ...(process.platform === 'darwin' ? { titleBarStyle: 'hiddenInset', trafficLightPosition: { x: 16, y: 12 } } : {}),
    webPreferences: { preload: path.join(__dirname, 'preload.cjs'), contextIsolation: true, nodeIntegration: false, sandbox: true, webSecurity: true, backgroundThrottling: false },
  })
  win.webContents.setWindowOpenHandler(({ url }) => { openExternal(url); return { action: 'deny' } })
  win.webContents.on('will-navigate', (event, url) => {
    if (!url.startsWith(APP_ORIGIN + '/')) { event.preventDefault(); openExternal(url) }
  })
  win.webContents.on('will-attach-webview', event => event.preventDefault())
  win.on('close', event => {
    if (!quitting) { event.preventDefault(); win.hide() }
    if (!win.isMaximized() && !win.isFullScreen()) { config.bounds = win.getBounds(); persist() }
  })
  win.once('ready-to-show', show)
  void win.loadURL(APP_ORIGIN + '/')
}

if (installerStartup || !app.requestSingleInstanceLock()) app.quit()
else {
  app.on('second-instance', show)
  app.on('before-quit', () => { quitting = true })
  app.on('activate', show)
  // Closing a window never quits the app; only an explicit Quit does.
  app.on('window-all-closed', () => {})
  app.whenReady().then(async () => {
    await loadConfig()
    const ses = session.defaultSession
    ses.setPermissionRequestHandler((_contents, permission, callback) => callback(permission === 'clipboard-sanitized-write'))
    ses.setPermissionCheckHandler((_contents, permission) => permission === 'clipboard-sanitized-write')
    // All remote data passes through the fixed Controller gateway. Remote HTML never executes.
    ses.webRequest.onBeforeRequest((details, callback) => callback({ cancel: details.resourceType !== 'mainFrame' && !['gobot:', 'blob:', 'data:'].some(scheme => details.url.startsWith(scheme)) && details.webContentsId === win?.webContents.id }))
    protocol.handle('gobot', gateway({ root: path.join(__dirname, '../dist'), getController: () => controller, fetchImpl: (...args) => net.fetch(...args) }))
    setupIPC(); createWindow(); syncTray()
    updateMenus()
    powerMonitor.on('resume', () => win?.webContents.send('desktop:command', { type: 'resume' }))
  }).catch(error => { console.error(error); app.exit(1) })
}
