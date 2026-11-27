const { nativeText } = require('./locale.cjs')
function menuTemplate(language, platform, newChat, releases) {
  const label = key => nativeText(language, key)
  const role = (name, key) => ({ role: name, label: label(key) })
  const mac = platform === 'darwin'
  return [
    ...(mac ? [{ label: 'Go Bot', submenu: [
      role('about', 'About Go Bot'), { type: 'separator' },
      { ...role('services', 'Services'), submenu: [] }, { type: 'separator' },
      role('hide', 'Hide Go Bot'), role('hideOthers', 'Hide Others'), role('unhide', 'Show All'),
      { type: 'separator' }, role('quit', 'Quit Go Bot'),
    ] }] : []),
    { label: label('File'), submenu: [
      { label: label('New Chat'), accelerator: 'CmdOrCtrl+N', click: newChat },
      { type: 'separator' }, role('close', 'Close Window'),
      ...(!mac ? [role('quit', 'Quit')] : []),
    ] },
    { label: label('Edit'), submenu: [role('undo', 'Undo'), role('redo', 'Redo'), { type: 'separator' },
      role('cut', 'Cut'), role('copy', 'Copy'), role('paste', 'Paste'),
      role('pasteAndMatchStyle', 'Paste and Match Style'), role('selectAll', 'Select All'),
    ] },
    { label: label('View'), submenu: [role('reload', 'Reload'), role('forceReload', 'Force Reload'),
      role('toggleDevTools', 'Developer Tools'), { type: 'separator' }, role('resetZoom', 'Actual Size'),
      role('zoomIn', 'Zoom In'), role('zoomOut', 'Zoom Out'), { type: 'separator' }, role('togglefullscreen', 'Toggle Full Screen'),
    ] },
    { label: label('Window'), submenu: [role('minimize', 'Minimize'), role('zoom', 'Zoom'), ...(mac ? [role('front', 'Bring All to Front')] : [])] },
    { label: label('Help'), submenu: [{ label: label('Download Updates'), click: releases }] },
  ]
}
function trayTemplate(language, show) {
  return [{ label: nativeText(language, 'Open Go Bot'), click: show }, { type: 'separator' }, { role: 'quit', label: nativeText(language, 'Quit') }]
}
module.exports = { menuTemplate, trayTemplate }
