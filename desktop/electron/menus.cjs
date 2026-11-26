function menuTemplate(language, platform, newChat, releases) {
  const zh = language === 'zh-CN'
  const label = (en, cn) => zh ? cn : en
  const role = (name, en, cn) => ({ role: name, label: label(en, cn) })
  const mac = platform === 'darwin'
  return [
    ...(mac ? [{ label: 'Go Bot', submenu: [
      role('about', 'About Go Bot', '关于 Go Bot'), { type: 'separator' },
      { ...role('services', 'Services', '服务'), submenu: [] }, { type: 'separator' },
      role('hide', 'Hide Go Bot', '隐藏 Go Bot'), role('hideOthers', 'Hide Others', '隐藏其他'), role('unhide', 'Show All', '显示全部'),
      { type: 'separator' }, role('quit', 'Quit Go Bot', '退出 Go Bot'),
    ] }] : []),
    { label: label('File', '文件'), submenu: [
      { label: label('New Chat', '新建会话'), accelerator: 'CmdOrCtrl+N', click: newChat },
      { type: 'separator' }, role('close', 'Close Window', '关闭窗口'),
      ...(!mac ? [role('quit', 'Quit', '退出')] : []),
    ] },
    { label: label('Edit', '编辑'), submenu: [role('undo', 'Undo', '撤销'), role('redo', 'Redo', '重做'), { type: 'separator' },
      role('cut', 'Cut', '剪切'), role('copy', 'Copy', '复制'), role('paste', 'Paste', '粘贴'),
      role('pasteAndMatchStyle', 'Paste and Match Style', '粘贴并匹配样式'), role('selectAll', 'Select All', '全选'),
    ] },
    { label: label('View', '视图'), submenu: [role('reload', 'Reload', '重新加载'), role('forceReload', 'Force Reload', '强制重新加载'),
      role('toggleDevTools', 'Developer Tools', '开发者工具'), { type: 'separator' }, role('resetZoom', 'Actual Size', '实际大小'),
      role('zoomIn', 'Zoom In', '放大'), role('zoomOut', 'Zoom Out', '缩小'), { type: 'separator' }, role('togglefullscreen', 'Toggle Full Screen', '切换全屏'),
    ] },
    { label: label('Window', '窗口'), submenu: [role('minimize', 'Minimize', '最小化'), role('zoom', 'Zoom', '缩放'), ...(mac ? [role('front', 'Bring All to Front', '全部置于前面')] : [])] },
    { label: label('Help', '帮助'), submenu: [{ label: label('Download Updates', '下载更新'), click: releases }] },
  ]
}
function trayTemplate(language, show) {
  return [{ label: language === 'zh-CN' ? '打开 Go Bot' : 'Open Go Bot', click: show }, { type: 'separator' }, { role: 'quit', label: language === 'zh-CN' ? '退出' : 'Quit' }]
}
module.exports = { menuTemplate, trayTemplate }
