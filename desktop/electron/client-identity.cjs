const { randomUUID } = require('node:crypto')
const validClientId = id => typeof id === 'string' && /^[A-Za-z0-9_-]{8,80}$/.test(id)

function desktopIdentity(savedId, platform = process.platform) {
  return {
    id: validClientId(savedId) ? savedId : 'client-' + randomUUID(),
    type: 'desktop', platform,
    model: { darwin: 'Go Bot · macOS', win32: 'Go Bot · Windows', linux: 'Go Bot · Linux' }[platform] || 'Go Bot Desktop',
  }
}
module.exports = { desktopIdentity }
