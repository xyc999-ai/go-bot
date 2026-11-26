const fs = require('node:fs/promises')
const path = require('node:path')
const os = require('node:os')
const { spawn } = require('node:child_process')
const { macReleaseConfig } = require('./macos-release-config.cjs')
const { run, verifyAppSignature, verifyApp, verifyDmg } = require('./verify-macos-release.cjs')
const { notarizeDmg, sha256 } = require('./macos-notarization.cjs')

const desktop = path.resolve(__dirname, '..')

function child(command, args, env) {
  return new Promise((resolve, reject) => {
    const proc = spawn(command, args, { cwd: desktop, env, stdio: 'inherit' })
    proc.on('error', reject)
    proc.on('exit', (code, signal) => code === 0 ? resolve() : reject(new Error(`${command} failed (${signal || code})`)))
  })
}

async function main() {
  const arch = process.argv[2] || process.arch
  if (!['arm64', 'x64'].includes(arch)) throw new Error('Supported macOS release architectures: arm64, x64')
  const fromApp = process.argv[3] === '--from-app' && process.argv[4] ? path.resolve(process.argv[4]) : null
  if (process.argv.length > 3 && (!fromApp || process.argv.length !== 5)) {
    throw new Error('Usage: npm run release:mac -- [arm64|x64] [--from-app /path/to/Go Bot.app]')
  }
  const env = { ...process.env, GOBOT_DESKTOP_RELEASE: '1' }
  const signing = macReleaseConfig(env)
  const xcode = run('xcodebuild', ['-version'])
  if (!env.APPLE_API_ISSUER && Number(/Xcode (\d+)/.exec(xcode)?.[1] || 0) < 26) {
    throw new Error('Individual API keys require Xcode 26+; select a compatible Xcode before releasing')
  }
  run('openssl', ['pkey', '-in', signing.osxNotarize.appleApiKey, '-check', '-noout'])
  // Validate authentication before spending time building. No credentials are logged or stored.
  run('xcrun', ['notarytool', 'history', '--key', signing.osxNotarize.appleApiKey,
    '--key-id', env.APPLE_API_KEY_ID, ...(env.APPLE_API_ISSUER ? ['--issuer', env.APPLE_API_ISSUER] : []),
    '--output-format', 'json'], { timeout: 60000 })
  const pkg = require('../package.json')
  const appPath = fromApp || path.join(desktop, 'out', `${pkg.productName}-darwin-${arch}`, `${pkg.productName}.app`)
  if (fromApp) {
    if (!appPath.endsWith('.app')) throw new Error('--from-app must identify an existing signed app bundle')
    const info = JSON.parse(run('plutil', ['-convert', 'json', '-o', '-', path.join(appPath, 'Contents', 'Info.plist')]))
    if (info.CFBundleIdentifier !== 'io.go-bot.desktop' || info.CFBundleShortVersionString !== pkg.version) {
      throw new Error('Existing app bundle ID/version does not match this desktop release')
    }
    const architectures = run('lipo', ['-archs', path.join(appPath, 'Contents', 'MacOS', 'go-bot')]).trim().split(/\s+/)
    if (architectures.length !== 1 || architectures[0] !== arch) throw new Error('Existing app architecture does not match release target')
    console.log(`Using an existing signed macOS ${arch} app; only the final DMG will be submitted`)
  } else {
    console.log(`Building and signing macOS ${arch} app`)
    await child('npm', ['run', 'package', '--', '--platform=darwin', `--arch=${arch}`], env)
  }
  verifyAppSignature(appPath, env.APPLE_TEAM_ID)
  const executable = path.join(appPath, 'Contents', 'MacOS', 'go-bot')
  const qaEnv = { ...env, DESKTOP_EXECUTABLE: executable, DESKTOP_QA_OUTPUT: path.join(desktop, 'test-results', `release-${arch}`) }
  await child(process.execPath, ['scripts/smoke.cjs'], qaEnv)
  await child(process.execPath, ['scripts/video-smoke.cjs'], qaEnv)

  const stage = await fs.mkdtemp(path.join(os.tmpdir(), 'go-bot-release-'))
  try {
    const base = `go-bot-${pkg.version}-macos-${arch}`
    const source = path.join(stage, 'contents')
    await fs.mkdir(source)
    run('ditto', [appPath, path.join(source, `${pkg.productName}.app`)])
    await fs.symlink('/Applications', path.join(source, 'Applications'))
    await fs.writeFile(path.join(source, '安装说明 - Read Me.txt'),
      '将 Go Bot.app 拖到 Applications，然后从“应用程序”打开。\n' +
      '请先启动本机 Go Bot Controller（http://127.0.0.1:8080）。\n' +
      '关闭窗口后应用继续运行；使用菜单“退出 Go Bot”或 Command+Q 完全退出。\n\n' +
      'Drag Go Bot.app into Applications, then launch it from Applications.\n' +
      'Start the local Go Bot Controller at http://127.0.0.1:8080 first.\n' +
      'Closing the window keeps the app running. Use Quit Go Bot or Command+Q to exit.\n')
    const pendingRoot = path.join(desktop, 'out', 'notarization', `macos-${arch}`)
    await fs.mkdir(pendingRoot, { recursive: true })
    const pending = await fs.mkdtemp(path.join(pendingRoot, 'attempt-'))
    const dmg = path.join(pending, `${base}.dmg`)
    console.log('Creating compressed DMG with App and Applications shortcut')
    run('hdiutil', ['create', '-volname', pkg.productName, '-srcfolder', source, '-format', 'UDZO', '-ov', dmg])
    run('codesign', ['--force', '--sign', env.APPLE_SIGNING_IDENTITY, '--timestamp', '-i', `io.go-bot.desktop.dmg.${arch}`,
      ...(env.APPLE_SIGNING_KEYCHAIN ? ['--keychain', env.APPLE_SIGNING_KEYCHAIN] : []), dmg])
    const submission = await notarizeDmg(dmg, signing.osxNotarize, path.join(pending, 'submission.json'))
    run('xcrun', ['stapler', 'staple', dmg])
    verifyDmg(dmg, env.APPLE_TEAM_ID)
    // The one DMG submission issues tickets for its nested App as well. Staple
    // the writable App before creating the optional ZIP; no second submission.
    run('xcrun', ['stapler', 'staple', appPath])
    verifyApp(appPath, env.APPLE_TEAM_ID)

    // Verify the actual payload on a read-only volume, then always detach it.
    const mount = path.join(stage, 'mounted')
    await fs.mkdir(mount)
    run('hdiutil', ['attach', '-readonly', '-nobrowse', '-mountpoint', mount, dmg])
    try {
      if (await fs.readlink(path.join(mount, 'Applications')) !== '/Applications') throw new Error('DMG Applications shortcut is invalid')
      const payload = path.join(mount, `${pkg.productName}.app`)
      verifyApp(payload, env.APPLE_TEAM_ID, { requireStapledTicket: false })
      const installed = path.join(stage, 'installed', `${pkg.productName}.app`)
      run('ditto', [payload, installed])
      verifyApp(installed, env.APPLE_TEAM_ID, { requireStapledTicket: false })
      await child(process.execPath, ['scripts/smoke.cjs'], {
        ...qaEnv, DESKTOP_EXECUTABLE: path.join(installed, 'Contents', 'MacOS', 'go-bot'),
        DESKTOP_QA_OUTPUT: path.join(desktop, 'test-results', `release-installed-${arch}`),
      })
    } finally { run('hdiutil', ['detach', mount]) }

    const zip = path.join(stage, `${base}.zip`)
    run('ditto', ['-c', '-k', '--sequesterRsrc', '--keepParent', appPath, zip])
    const output = path.join(desktop, 'out', 'release', `macos-${arch}`)
    await fs.mkdir(output, { recursive: true })
    const files = []
    for (const artifact of [dmg, zip]) {
      const name = path.basename(artifact)
      const hash = await sha256(artifact)
      await fs.copyFile(artifact, path.join(output, name))
      files.push({ name, sha256: hash })
    }
    await fs.writeFile(path.join(output, `${base}.sha256`), files.map(file => `${file.sha256}  ${file.name}\n`).join(''))
    await fs.writeFile(path.join(output, `${base}.verification.json`), JSON.stringify({
      version: pkg.version, platform: 'darwin', arch, teamId: env.APPLE_TEAM_ID,
      notarization: { container: 'dmg', id: submission.id, status: submission.status },
      verifiedAt: new Date().toISOString(), xcode: xcode.trim(),
      checks: ['app-signature', 'hardened-runtime', 'app-notarization', 'app-gatekeeper',
        'native-smoke', 'video-playback', 'dmg-signature', 'dmg-notarization', 'dmg-gatekeeper',
        'mounted-payload', 'copied-installation-smoke'], files,
    }, null, 2) + '\n')
    console.log(`PASS: verified public release artifacts: ${output}`)
  } finally { await fs.rm(stage, { recursive: true, force: true }) }
}

if (require.main === module) main().catch(error => { console.error(error.message); process.exitCode = 1 })
