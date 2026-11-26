const fs = require('node:fs/promises')
const path = require('node:path')
const { createReadStream } = require('node:fs')
const { createHash } = require('node:crypto')

async function hashFile(file) {
  const hash = createHash('sha256')
  for await (const chunk of createReadStream(file)) hash.update(chunk)
  return hash.digest('hex')
}

const macChecks = ['app-signature', 'hardened-runtime', 'app-notarization', 'app-gatekeeper',
  'native-smoke', 'video-playback', 'dmg-signature', 'dmg-notarization', 'dmg-gatekeeper',
  'mounted-payload', 'copied-installation-smoke']

async function collectArtifacts(directory, version, teamId) {
  if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version || '') || !/^[A-Z0-9]{10}$/.test(teamId || '')) {
    throw new Error('Release collection requires a valid version and APPLE_TEAM_ID')
  }
  const expected = ['arm64', 'x64'].flatMap(arch => {
    const base = `go-bot-${version}-macos-${arch}`
    return [`${base}.dmg`, `${base}.zip`, `${base}.sha256`, `${base}.verification.json`]
  }).concat(`go-bot-${version}-windows-x64-setup.exe`).sort()
  const actual = (await fs.readdir(directory)).filter(name => name !== 'SHA256SUMS').sort()
  if (JSON.stringify(actual) !== JSON.stringify(expected)) throw new Error('Missing or unexpected release artifacts')
  const hashes = new Map()
  for (const name of expected) {
    const file = path.join(directory, name)
    if (!(await fs.lstat(file)).isFile()) throw new Error('Release artifacts must be regular files')
    hashes.set(name, await hashFile(file))
  }
  for (const arch of ['arm64', 'x64']) {
    const base = `go-bot-${version}-macos-${arch}`
    const report = JSON.parse(await fs.readFile(path.join(directory, `${base}.verification.json`), 'utf8'))
    if (report.version !== version || report.platform !== 'darwin' || report.arch !== arch || report.teamId !== teamId ||
        report.notarization?.container !== 'dmg' || report.notarization?.status !== 'Accepted' ||
        !/^[0-9a-f-]{36}$/i.test(report.notarization?.id || '') ||
        !Array.isArray(report.checks) || !macChecks.every(check => report.checks.includes(check)) ||
        !Array.isArray(report.files) || report.files.length !== 2) {
      throw new Error(`Invalid macOS verification report: ${arch}`)
    }
    let checksumText = ''
    for (const extension of ['dmg', 'zip']) {
      const name = `${base}.${extension}`
      if (report.files.find(file => file.name === name)?.sha256 !== hashes.get(name)) throw new Error(`Release artifact hash mismatch: ${name}`)
      checksumText += `${hashes.get(name)}  ${name}\n`
    }
    if (await fs.readFile(path.join(directory, `${base}.sha256`), 'utf8') !== checksumText) throw new Error(`Invalid macOS checksum file: ${arch}`)
  }
  await fs.writeFile(path.join(directory, 'SHA256SUMS'), expected.map(name => `${hashes.get(name)}  ${name}\n`).join(''))
  return `Go Bot Desktop ${version}\n\n` +
    '- macOS: Developer ID signed and Apple notarized. Choose arm64 for Apple Silicon or x64 for Intel. Open the DMG and drag Go Bot into Applications.\n' +
    '- Windows x64: unsigned installer. Windows may show an unknown publisher or SmartScreen warning; managed devices may block installation.\n' +
    '- Start the local Go Bot Controller at http://127.0.0.1:8080 before opening the desktop client. The installer does not include the Controller.\n' +
    '- Closing the window keeps Go Bot running. Use Quit / Command+Q to exit.\n' +
    '- SHA256SUMS contains checksums for all release assets. macOS verification reports are included.\n\n' +
    'macOS 安装包已签名并通过 Apple 公证，arm64 适用于 Apple Silicon，x64 适用于 Intel。Windows 安装包未签名，系统可能显示未知发布者或拦截提示。\n\n' +
    '桌面端需要预先启动本机 8080 端口的 Go Bot Controller。关闭窗口后继续后台运行，通过应用菜单主动退出。\n'
}

module.exports = { collectArtifacts, macChecks, hashFile }

if (require.main === module) {
  collectArtifacts(path.resolve(process.argv[2] || 'release-artifacts'), process.env.RELEASE_VERSION, process.env.APPLE_TEAM_ID)
    .then(notes => fs.writeFile('RELEASE_NOTES.md', notes))
    .catch(error => { console.error(error.message); process.exitCode = 1 })
}
