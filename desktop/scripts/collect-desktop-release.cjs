const fs = require('node:fs/promises')
const path = require('node:path')
const { createReadStream } = require('node:fs')
const { createHash } = require('node:crypto')
const { clientReleaseConfig } = require('../../scripts/client-release-config.cjs')
const { releaseConfig, certificateDigest, checks: androidChecks } = require('../../mobile/scripts/android-release.cjs')

async function hashFile(file) {
  const hash = createHash('sha256')
  for await (const chunk of createReadStream(file)) hash.update(chunk)
  return hash.digest('hex')
}

const macChecks = ['app-signature', 'hardened-runtime', 'app-notarization', 'app-gatekeeper',
  'native-smoke', 'video-playback', 'dmg-signature', 'dmg-notarization', 'dmg-gatekeeper',
  'mounted-payload', 'copied-installation-smoke']

async function collectArtifacts(directory, version, teamId, android) {
  if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version || '') || !/^[A-Z0-9]{10}$/.test(teamId || '')) {
    throw new Error('Release collection requires a valid version and APPLE_TEAM_ID')
  }
  if (!android || !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(android.version || '') ||
      !Number.isSafeInteger(android.versionCode) || android.versionCode < 1 || android.packageName !== 'com.gobot.mobile') {
    throw new Error('Release collection requires valid Android metadata')
  }
  if (android.version !== version) throw new Error('All client release versions must match')
  const androidDigest = certificateDigest(android.certificateSha256)
  const androidBase = `go-bot-${android.version}-android-arm64`
  const expected = ['arm64', 'x64'].flatMap(arch => {
    const base = `go-bot-${version}-macos-${arch}`
    return [`${base}.dmg`, `${base}.zip`, `${base}.sha256`, `${base}.verification.json`]
  }).concat(`go-bot-${version}-windows-x64-setup.exe`, `${androidBase}.apk`, `${androidBase}.verification.json`).sort()
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
  const androidReport = JSON.parse(await fs.readFile(path.join(directory, `${androidBase}.verification.json`), 'utf8'))
  if (androidReport.version !== android.version || androidReport.versionCode !== android.versionCode ||
      androidReport.packageName !== android.packageName || androidReport.platform !== 'android' || androidReport.arch !== 'arm64' ||
      androidReport.certificateSha256 !== androidDigest || !Array.isArray(androidReport.checks) ||
      !androidChecks.every(check => androidReport.checks.includes(check)) ||
      androidReport.files?.length !== 1 || androidReport.files[0].name !== `${androidBase}.apk` ||
      androidReport.files[0].sha256 !== hashes.get(`${androidBase}.apk`)) {
    throw new Error('Invalid Android verification report or APK hash mismatch')
  }
  await fs.writeFile(path.join(directory, 'SHA256SUMS'), expected.map(name => `${hashes.get(name)}  ${name}\n`).join(''))
  return `Go Bot v${version}\n\n` +
    `- Android ARM64: signed APK for direct installation. Allow installation from your browser/file manager when prompted. Existing installations require the same signing key and a compatible versionCode. Connect to a reachable Controller address in the app.\n` +
    '- macOS: Developer ID signed and Apple notarized. Choose arm64 for Apple Silicon or x64 for Intel. Open the DMG and drag Go Bot into Applications.\n' +
    '- Windows x64: unsigned installer. Windows may show an unknown publisher or SmartScreen warning; managed devices may block installation.\n' +
    '- Start the local Go Bot Controller at http://127.0.0.1:8080 before opening the desktop client. The installer does not include the Controller.\n' +
    '- Closing the window keeps Go Bot running. Use Quit / Command+Q to exit.\n' +
    '- SHA256SUMS contains checksums for all release assets. macOS and Android verification reports are included.\n\n' +
    'macOS 安装包已签名并通过 Apple 公证，arm64 适用于 Apple Silicon，x64 适用于 Intel。Windows 安装包未签名，系统可能显示未知发布者或拦截提示。\n\n' +
    `Android ${android.version}（versionCode ${android.versionCode}）为 ARM64 签名 APK，允许下载来源安装后即可安装；覆盖升级需使用相同签名密钥及兼容的 versionCode。在手机 App 中填写可访问的 Controller 地址。\n\n` +
    '桌面端需要预先启动本机 8080 端口的 Go Bot Controller。关闭窗口后继续后台运行，通过应用菜单主动退出。\n'
}

module.exports = { collectArtifacts, macChecks, hashFile }

if (require.main === module) {
  const { version } = clientReleaseConfig()
  if (process.env.RELEASE_VERSION !== version) throw new Error('Release version must match all client packages')
  collectArtifacts(path.resolve(process.argv[2] || 'release-artifacts'), version, process.env.APPLE_TEAM_ID,
    { ...releaseConfig(), certificateSha256: process.env.ANDROID_SIGNING_CERT_SHA256 })
    .then(notes => fs.writeFile('RELEASE_NOTES.md', notes))
    .catch(error => { console.error(error.message); process.exitCode = 1 })
}
