const fs = require('node:fs')
const path = require('node:path')

function clientReleaseConfig(root = path.resolve(__dirname, '..')) {
  const read = name => JSON.parse(fs.readFileSync(path.join(root, name), 'utf8'))
  const desktop = read('desktop/package.json')
  const mobile = read('mobile/package.json')
  const web = read('frontend/package.json')
  const { expo } = read('mobile/app.json')
  const version = desktop.version
  if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version || '') ||
      [mobile.version, web.version, expo.version].some(value => value !== version)) {
    throw new Error('Desktop, Web, mobile package.json and Expo versions must match')
  }
  if (desktop.name !== 'go-bot-desktop' || mobile.name !== 'go-bot-mobile' || web.name !== 'go-bot-web') {
    throw new Error('Unexpected client package name')
  }
  return { version }
}

module.exports = { clientReleaseConfig }

if (require.main === module) {
  console.log(`version=${clientReleaseConfig().version}`)
}
