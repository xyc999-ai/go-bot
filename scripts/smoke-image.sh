#!/usr/bin/env bash
set -euo pipefail
image="${1:-ghcr.io/xyc999-ai/go-bot-host:latest}"
docker run --rm --entrypoint /bin/sh "$image" -ec '
  python --version
  python -m pip --version
  node --version
  npm --version
  npx --version
  node -e "require(\"pdf-lib\")"
  chromium --version
  soffice --version
  python -c "import pypdf,pdfplumber,reportlab,pymupdf,pypdfium2,pptx"
  # Official CLIs are installed by plugin lifecycle scripts, not preinstalled.
  ! command -v officecli
  ! command -v agent-browser
  ! command -v xurl
  ! command -v gws
  ! command -v gemini-api
  test ! -e /usr/share/go-bot/skill-seed
  test ! -e /usr/share/go-bot/plugins
  test -s /usr/share/go-bot/cordis/package.json
  test -s /usr/share/go-bot/sdk/index.mjs
  chromium --headless --no-sandbox --disable-dev-shm-usage --no-zygote --screenshot=/tmp/browser.png "data:text/html,<h1>Go Bot</h1>" >/dev/null 2>&1
  test -s /tmp/browser.png
  echo "Base environment and packaged plugin smoke: OK"
'
