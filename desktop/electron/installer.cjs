const path = require('node:path')

// Squirrel invokes the app during install/update/uninstall. Maintain shortcuts
// without opening the client, connecting a Controller or starting a Host.
function installerStartup({ platform = process.platform, argv = process.argv, executable = process.execPath, spawn = require('node:child_process').spawn } = {}) {
  if (platform !== 'win32') return false
  const event = argv.find(arg => /^--squirrel-(install|updated|uninstall|obsolete)$/.test(arg))
  if (!event) return false
  if (event === '--squirrel-obsolete') return true
  const update = path.win32.resolve(path.win32.dirname(executable), '..', 'Update.exe')
  const command = event === '--squirrel-uninstall' ? '--removeShortcut' : '--createShortcut'
  const child = spawn(update, [command, path.win32.basename(executable)], { detached: true, stdio: 'ignore' })
  child.on('error', () => {})
  child.unref()
  return true
}
module.exports = { installerStartup }
