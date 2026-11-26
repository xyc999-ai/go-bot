const macRelease = require('./scripts/macos-release-config.cjs').macReleaseConfig()

module.exports = {
  packagerConfig: {
    asar: true,
    ...(process.env.ELECTRON_ZIP_DIR ? { electronZipDir: process.env.ELECTRON_ZIP_DIR } : {}),
    executableName: 'go-bot',
    appBundleId: 'io.go-bot.desktop',
    icon: require('node:path').join(__dirname, 'assets/icon'),
    appCategoryType: 'public.app-category.productivity',
    // Sign the App here. release:mac notarizes the final DMG once, including its
    // nested code, instead of submitting the App and DMG separately.
    ...(macRelease.osxSign ? { osxSign: macRelease.osxSign } : {}),
    ignore: [/^\/renderer(?:\/|$)/, /^\/tests(?:\/|$)/, /^\/design(?:\/|$)/, /^\/scripts(?:\/|$)/, /^\/out(?:\/|$)/, /^\/\.local(?:\/|$)/, /\.(?:p8|p12|pfx)$/i, /(?:^|\/)\.env(?:\.|$)/],
  },
  makers: [
    { name: '@electron-forge/maker-zip', platforms: ['darwin'] },
    { name: '@electron-forge/maker-squirrel', config: { name: 'go_bot' } },
    { name: '@electron-forge/maker-deb', config: { options: { maintainer: 'Go Bot contributors', homepage: 'https://github.com/xyc999-ai/go-bot' } } },
    { name: '@electron-forge/maker-rpm', config: {} },
  ],
}
