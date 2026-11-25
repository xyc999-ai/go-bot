# Reviewed remote plugin market

The Controller reads the public [plugin repository](https://github.com/xyc999-ai/got-bot-plugins)'s `catalog.json`. Business packages, original Skills, icons and tutorial videos are released there, independently from Host/Controller images. Clients choose a reviewed plugin ID; they cannot submit arbitrary package addresses.

Each package owns its `plugin.json` display metadata. The release workflow prepares original upstream Skills, runs `npm pack`, publishes complete versioned GitHub Release assets, and only then appends the generated metadata to the catalog. Keep historical entries: an installed selection resolves its exact package and version, while the market shows the newest reviewed version as an explicit update.

The Controller caches public metadata for five minutes with ETag revalidation. The market refresh button forces a recheck. A failed refresh retains the last valid snapshot; a first failed fetch is reported as an error. No public catalog is stored in Redis or a local state file. The deployment-only `controller --plugin-catalog URL` flag supports development catalogs.

Icons and tutorials are served through `/assets/plugins/{id}/{file}?v={version}`, with HEAD, Range and cache headers. Assets must be declared in the selected release. They are available before plugin installation.

Reviewed exact npm versions, pinned Git sources and HTTPS tarballs remain supported. The public repository uses versioned tarballs, so users do not need GitHub npm registry credentials. Local HTTP tarballs are accepted only on loopback or `host.docker.internal` for development tests. The catalog's private `builds` policy approves native pnpm lifecycle scripts; the browser never grants these permissions.

业务包在独立仓库维护和发布，主镜像只保留 Go 核心、Cordis 和 SDK。新增插件或版本后刷新市场即可发现，不需要重构主镜像。用户仍通过现有市场安装、启停、更新和授权；默认安装的 agent-browser、OfficeCLI 也遵循同一网络安装流程。
