# Changelog

Every release of Kayly, newest first. The format follows [Keep a Changelog](https://keepachangelog.com); versions follow [semantic versioning](docs/compatibility.md) from 1.0. When a version is tagged, its section here becomes the release notes, so rename `Unreleased` to `[x.y.z] - date` in the commit that is tagged.

## [Unreleased]

### Fixed
- The web UI no longer crashes on a project with no env values (`env_keys` was `null` instead of a list).

## [0.5.0] - 2026-10-20

### Added
- `kayly upgrade [version]`: installs a signed release after a Kayly backup, keeps the previous binary as `kayly.previous`, restarts the daemon after running deploys finish, and refuses a downgrade. Apps and the proxy keep serving.
- Signed releases: the release workflow signs `checksums.txt` with ed25519; `install.sh` (with openssl) and `kayly upgrade` install only a binary whose signature and checksum hold.
- Rate limit on unknown tokens: an address gets 10 in a row (sign-in, API bearer or MCP), then one a minute, answered with `429` and `Retry-After`.
- `install.sh` takes `KAYLY_BASE_DOMAIN` (e.g. `localhost` for a test machine).
- End-to-end CI on a real Docker host: install, deploy, a daemon crash at every pipeline step, rollback, and an upgrade from the previous release under load with zero failed requests (`test/e2e/e2e.sh`).
- Documentation: install, deploy, domains, backups, bots, troubleshooting and security model guides; references for `kayly.toml`, the CLI, the HTTP API and MCP; the 1.x compatibility promise.
- Telegram bots and Mini Apps: `[services.<name>.telegram]` with a token check before release, webhook (with a generated secret) or polling, and a menu button that opens a Mini App; bot and Mini App examples.

### Fixed
- `kayly restore` works right after `install.sh`: it stops the daemon, sets the fresh install's empty state aside and starts the daemon again once restored. It still refuses a state that holds any project.

### Changed
- The web UI is rebuilt with shadcn/ui on Radix in a framed-sheet layout: a sidebar of projects and the server's disk, a ⌘K palette, a live-release card with the release track, releases in a side sheet with the failure reason and deploy log, two-column settings, and light and dark themes. `npm run dev:mock` serves it against sample data; the screens are tested with Vitest.

## [0.3.1] - 2026-10-18

### Fixed
- The Redis add-on keeps starting over data an earlier container wrote.

## [0.3.0] - 2026-10-18

### Added
- Projects follow a git repository over SSH with a deploy key; a GitHub push deploys.
- Token scopes (`read`, `deploy`, `admin`), project binding, expiry, and an audit log of every change.
- MCP server at `/mcp`, its tools limited by the token's scope.
- `kayly delete` and `kayly restart <service>`.

## [0.2.0] - 2026-10-18

### Added
- Backups of Redis, volumes and Kayly itself; off-site upload to S3 encrypted under a recovery key, with retention and a weekly restore drill; `kayly backups restore` and `kayly restore` for a lost server.
- Alerts to Telegram or a webhook: failed deploys and backups, restart loops, failing health checks, a filling disk.
- Each release keeps its deploy log and git commit; service state, restarts, CPU, memory and disk in the API, CLI and UI.
- `memory` and `cpus` limits per service; build cache trimming.

## [0.1.0] - 2026-10-18

### Added
- The deploy pipeline with immutable releases, a release step, health checks, a zero-downtime switch, instant rollback and crash recovery.
- `kayly.toml`, env values scoped per service with generated and templated values, custom domains with automatic HTTPS, one-click Postgres and Redis add-ons with backups, host ports.
- The `kayly` CLI and daemon, the HTTP API with tokens, the web UI, and `install.sh`.
