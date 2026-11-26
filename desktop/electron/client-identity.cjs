const os = require('node:os')
const { execFile } = require('node:child_process')
const { promisify } = require('node:util')
const run = promisify(execFile)

async function computerName(platform = process.platform, { execute = run, hostname = os.hostname } = {}) {
  if (platform === 'darwin') {
    try {
      const { stdout } = await execute('/usr/sbin/scutil', ['--get', 'ComputerName'], { timeout: 2000, maxBuffer: 8192 })
      if (stdout.trim()) return stdout.trim()
    } catch {}
  }
  try { return hostname().trim() } catch { return '' }
}

function desktopIdentity(name, platform = process.platform) {
  const fallback = { darwin: 'Mac', win32: 'Windows PC', linux: 'Linux PC' }[platform] || 'Desktop'
  const model = Array.from(name?.replace(/[\u0000-\u001f\u007f-\u009f]/g, '').trim() || fallback).slice(0, 120).join('')
  return { type: 'desktop', platform, model }
}
module.exports = { computerName, desktopIdentity }
