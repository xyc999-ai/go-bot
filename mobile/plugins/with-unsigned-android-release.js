const { withAppBuildGradle } = require('expo/config-plugins')

// Release APKs are signed outside Gradle using the existing distribution key.
// Expo's template otherwise reuses the debug key for release builds.
function unsignedRelease(contents) {
  const pattern = /(release\s*\{[\s\S]*?signingConfig\s+)(?:signingConfigs\.debug|null)/g
  const matches = [...contents.matchAll(pattern)]
  if (matches.length !== 1) throw new Error('Expected one Android release signing configuration')
  return contents.replace(pattern, '$1null')
}

module.exports = config => withAppBuildGradle(config, next => {
  if (next.modResults.language !== 'groovy') throw new Error('Expected Groovy app/build.gradle')
  next.modResults.contents = unsignedRelease(next.modResults.contents)
  return next
})
module.exports.unsignedRelease = unsignedRelease
