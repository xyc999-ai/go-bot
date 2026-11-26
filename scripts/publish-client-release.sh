#!/usr/bin/env bash
set -euo pipefail

# The tag is an operator-selected release label, independent of package versions.
# GitHub creates missing tags at the exact commit used by all build jobs.
if gh release view --json isDraft -- "$RELEASE_TAG" > "$RUNNER_TEMP/existing-release.json" 2>/dev/null; then
  node -e "if (!require(process.argv[1]).isDraft) throw new Error('Refusing to modify a published release')" "$RUNNER_TEMP/existing-release.json"
  gh release edit --draft --target "$RELEASE_COMMIT" --title "Go Bot v$RELEASE_VERSION" --notes-file RELEASE_NOTES.md -- "$RELEASE_TAG"
else
  args=(--target "$RELEASE_COMMIT" --draft --title "Go Bot v$RELEASE_VERSION" --notes-file RELEASE_NOTES.md)
  if [[ "$RELEASE_VERSION" == *-* ]]; then args+=(--prerelease); fi
  gh release create "${args[@]}" -- "$RELEASE_TAG"
fi
gh release upload --clobber -- "$RELEASE_TAG" release-artifacts/*
