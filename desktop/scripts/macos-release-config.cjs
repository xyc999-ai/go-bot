const fs = require('node:fs')
const path = require('node:path')

// Binary resources (.pak, fonts, images, app.asar) are sealed by their enclosing
// bundle. Only Mach-O files and bundles need their own code signatures.
function skipNonCodeResource(file) {
  if (!fs.statSync(file).isFile()) return false
  const handle = fs.openSync(file, 'r')
  try {
    const bytes = Buffer.alloc(4)
    if (fs.readSync(handle, bytes, 0, 4, 0) !== 4) return true
    return !['feedface', 'cefaedfe', 'feedfacf', 'cffaedfe',
      'cafebabe', 'bebafeca', 'cafebabf', 'bfbafeca'].includes(bytes.toString('hex'))
  } finally { fs.closeSync(handle) }
}

function macReleaseConfig(env = process.env, platform = process.platform) {
  if (env.GOBOT_DESKTOP_RELEASE !== '1') return {}
  if (platform !== 'darwin') throw new Error('macOS releases must be built on macOS')
  for (const name of ['APPLE_SIGNING_IDENTITY', 'APPLE_TEAM_ID', 'APPLE_API_KEY', 'APPLE_API_KEY_ID']) {
    if (!env[name]?.trim()) throw new Error(`macOS release requires ${name}`)
  }
  if (!/^[A-Z0-9]{10}$/.test(env.APPLE_TEAM_ID)) throw new Error('Invalid APPLE_TEAM_ID')
  if (!env.APPLE_SIGNING_IDENTITY.startsWith('Developer ID Application: ') ||
      !env.APPLE_SIGNING_IDENTITY.endsWith(`(${env.APPLE_TEAM_ID})`)) {
    throw new Error('APPLE_SIGNING_IDENTITY must be a Developer ID Application identity for APPLE_TEAM_ID')
  }
  const appleApiKey = path.resolve(env.APPLE_API_KEY)
  if (!fs.statSync(appleApiKey, { throwIfNoEntry: false })?.isFile()) throw new Error('APPLE_API_KEY must point to a private key file')
  return {
    osxSign: {
      identity: env.APPLE_SIGNING_IDENTITY,
      type: 'distribution',
      hardenedRuntime: true,
      continueOnError: false,
      batchCodesignCalls: true,
      ignore: [skipNonCodeResource],
      ...(env.APPLE_SIGNING_KEYCHAIN ? { keychain: env.APPLE_SIGNING_KEYCHAIN } : {}),
    },
    osxNotarize: {
      appleApiKey,
      appleApiKeyId: env.APPLE_API_KEY_ID,
      // Individual API keys must omit the issuer; team keys require it.
      ...(env.APPLE_API_ISSUER ? { appleApiIssuer: env.APPLE_API_ISSUER } : {}),
    },
  }
}

module.exports = { macReleaseConfig, skipNonCodeResource }
