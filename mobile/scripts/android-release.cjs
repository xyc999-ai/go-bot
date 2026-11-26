const fs = require('node:fs/promises')
const path = require('node:path')
const os = require('node:os')
const { execFileSync } = require('node:child_process')
const { createHash } = require('node:crypto')

const checks = ['apk-signature', 'apk-alignment', 'apk-metadata', 'apk-certificate']

function releaseConfig(root = path.resolve(__dirname, '..')) {
  const { expo } = require(path.join(root, 'app.json'))
  const pkg = require(path.join(root, 'package.json'))
  if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(expo.version) || pkg.version !== expo.version ||
      !Number.isSafeInteger(expo.android.versionCode) || expo.android.versionCode < 1 ||
      expo.android.package !== 'com.gobot.mobile') throw new Error('Invalid Android release configuration')
  return { version: expo.version, versionCode: expo.android.versionCode, packageName: expo.android.package }
}

function certificateDigest(value) {
  const digest = (value || '').replace(/:/g, '').toLowerCase()
  if (!/^[a-f0-9]{64}$/.test(digest)) throw new Error('ANDROID_SIGNING_CERT_SHA256 must be the existing release certificate SHA256 fingerprint')
  return digest
}

function validateBadging(text, config) {
  const pkg = /^package: name='([^']+)' versionCode='(\d+)' versionName='([^']+)'/m.exec(text)
  const abis = /^native-code: (.+)$/m.exec(text)
  if (!pkg || pkg[1] !== config.packageName || Number(pkg[2]) !== config.versionCode || pkg[3] !== config.version ||
      !abis || abis[1].trim() !== "'arm64-v8a'" || /^application-debuggable/m.test(text)) {
    throw new Error('APK package, version, architecture or release mode mismatch')
  }
}

function validateSigner(text, expectedDigest) {
  const digests = [...text.matchAll(/^(?:Signer #\d+|V\d+(?:\.\d+)* Signer:) certificate SHA-256 digest: ([a-fA-F0-9]+)\r?$/gm)]
  const count = /^Number of signers: (\d+)\r?$/m.exec(text)
  if ((count && count[1] !== '1') || digests.length !== 1 || certificateDigest(digests[0][1]) !== certificateDigest(expectedDigest) ||
      /^(?:Signer #\d+|V\d+(?:\.\d+)* Signer:) certificate DN:.*(?:CN=Android Debug|CN=AndroidDebug)/mi.test(text)) {
    throw new Error('APK release signing certificate mismatch (or debug key)')
  }
}

async function signAndCollect(input, output, env = process.env) {
  const config = releaseConfig()
  const digest = certificateDigest(env.ANDROID_SIGNING_CERT_SHA256)
  for (const key of ['ANDROID_KEYSTORE_PATH', 'ANDROID_KEYSTORE_PASSWORD', 'ANDROID_KEY_ALIAS', 'ANDROID_KEY_PASSWORD', 'ANDROID_BUILD_TOOLS']) {
    if (!env[key]) throw new Error(`Missing release setting: ${key}`)
  }
  const run = (name, args) => execFileSync(path.join(env.ANDROID_BUILD_TOOLS, name), args, { encoding: 'utf8', env, stdio: ['ignore', 'pipe', 'pipe'] })
  const temporary = await fs.mkdtemp(path.join(os.tmpdir(), 'go-bot-apk-'))
  try {
    const aligned = path.join(temporary, 'aligned.apk')
    const signed = path.join(temporary, 'signed.apk')
    run('zipalign', ['-P', '16', '-f', '4', path.resolve(input), aligned])
    run('apksigner', ['sign', '--ks', env.ANDROID_KEYSTORE_PATH, '--ks-key-alias', env.ANDROID_KEY_ALIAS,
      '--ks-pass', 'env:ANDROID_KEYSTORE_PASSWORD', '--key-pass', 'env:ANDROID_KEY_PASSWORD',
      '--v4-signing-enabled', 'false', '--out', signed, aligned])
    validateSigner(run('apksigner', ['verify', '--verbose', '--print-certs', signed]), digest)
    run('zipalign', ['-c', '-P', '16', '4', signed])
    validateBadging(run('aapt', ['dump', 'badging', signed]), config)
    const name = `go-bot-${config.version}-android-arm64.apk`
    const sha256 = createHash('sha256').update(await fs.readFile(signed)).digest('hex')
    const report = { ...config, platform: 'android', arch: 'arm64', certificateSha256: digest, checks, files: [{ name, sha256 }] }
    await fs.mkdir(output, { recursive: true })
    await fs.copyFile(signed, path.join(output, name))
    await fs.writeFile(path.join(output, name.replace(/\.apk$/, '.verification.json')), JSON.stringify(report, null, 2) + '\n')
    console.log(`Verified Android ${config.version} (${config.versionCode}) ARM64 APK`)
    return report
  } finally {
    await fs.rm(temporary, { recursive: true, force: true })
  }
}

module.exports = { releaseConfig, certificateDigest, validateBadging, validateSigner, signAndCollect, checks }

if (require.main === module) {
  if (process.argv[2] === '--metadata') {
    const config = releaseConfig()
    certificateDigest(process.env.ANDROID_SIGNING_CERT_SHA256)
    console.log(`android-version=${config.version}\nandroid-version-code=${config.versionCode}`)
  } else {
    signAndCollect(process.argv[2] || 'android/app/build/outputs/apk/release/app-release-unsigned.apk',
      process.argv[3] || 'out/release/android-arm64').catch(() => {
      // Tool diagnostics may contain keystore details. Keep secret-bearing errors off CI logs.
      console.error('Android release signing/verification failed; check SDK tools, APK metadata and signing settings')
      process.exitCode = 1
    })
  }
}
