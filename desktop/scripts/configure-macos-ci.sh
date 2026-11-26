#!/usr/bin/env bash
set +x
set -euo pipefail

if [[ $# -lt 6 || $# -gt 7 ]]; then
  echo "Usage: bash desktop/scripts/configure-macos-ci.sh OWNER/REPO certificate.p12 key.p8 KEY_ID TEAM_ID 'Developer ID Application: Company (TEAM_ID)' [TEAM_KEY_ISSUER]" >&2
  exit 1
fi
release_repo="$1"
certificate_file="$2"
api_key_file="$3"
api_key_id="$4"
release_team_id="$5"
signing_identity="$6"
api_issuer="${7:-}"

[[ "$release_repo" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || { echo 'Invalid repository' >&2; exit 1; }
[[ "$release_team_id" =~ ^[A-Z0-9]{10}$ ]] || { echo 'Invalid team ID' >&2; exit 1; }
[[ "$signing_identity" == "Developer ID Application: "*"($release_team_id)" ]] || { echo 'Signing identity must match the team ID' >&2; exit 1; }
[[ -r "$certificate_file" && -r "$api_key_file" ]] || { echo 'Certificate or API key file is not readable' >&2; exit 1; }
command -v gh >/dev/null || { echo 'Install GitHub CLI first (brew install gh), then run gh auth login.' >&2; exit 1; }
gh auth status >/dev/null 2>&1 || { echo 'Run gh auth login first.' >&2; exit 1; }
openssl pkey -in "$api_key_file" -check -noout >/dev/null 2>&1 || { echo 'Invalid API private key' >&2; exit 1; }

printf 'Configuring macOS release secrets for %s\n' "$release_repo"
IFS= read -r -s -p 'Enter the exported .p12 protection password (not the Mac login password): ' certificate_password </dev/tty
printf '\n' >/dev/tty
[[ -n "$certificate_password" ]] || { echo 'A password-protected .p12 is required' >&2; exit 1; }
if ! openssl pkcs12 -in "$certificate_file" -noout -passin fd:3 3<<<"$certificate_password" >/dev/null 2>&1; then
  openssl pkcs12 -legacy -in "$certificate_file" -noout -passin fd:3 3<<<"$certificate_password" >/dev/null 2>&1 || {
    echo 'Could not validate the .p12 password or certificate format' >&2; exit 1;
  }
fi

# Only pipes to gh receive secret contents. Neither password nor key is printed,
# placed in command arguments, written to a temporary file, or committed.
base64 < "$certificate_file" | tr -d '\r\n' | gh secret set MACOS_CERTIFICATE_P12_BASE64 --repo "$release_repo"
printf '%s' "$certificate_password" | gh secret set MACOS_CERTIFICATE_PASSWORD --repo "$release_repo"
unset certificate_password
gh secret set APPLE_API_PRIVATE_KEY --repo "$release_repo" < "$api_key_file"
printf '%s' "$api_key_id" | gh secret set APPLE_API_KEY_ID --repo "$release_repo"
gh variable set APPLE_TEAM_ID --repo "$release_repo" --body "$release_team_id"
gh variable set APPLE_SIGNING_IDENTITY --repo "$release_repo" --body "$signing_identity"
if [[ -n "$api_issuer" ]]; then
  gh variable set MACOS_NOTARY_API_ISSUER --repo "$release_repo" --body "$api_issuer"
elif gh variable get MACOS_NOTARY_API_ISSUER --repo "$release_repo" >/dev/null 2>&1; then
  gh variable delete MACOS_NOTARY_API_ISSUER --repo "$release_repo"
fi
echo 'Configured four macOS release secrets and signing variables. No Windows signing settings were added.'
