# Kayly architecture

Kayly deploys web apps, APIs, workers, Telegram bots and Mini Apps to a server you own, with the deploy model of a managed platform: immutable releases, a release step that cannot take production down, blue-green switches and one-click rollback.

This document is the source of truth for how Kayly is built. A change that contradicts it updates it first. Product research and positioning: [RESEARCH.md](RESEARCH.md).

## 1. Scope

**First tenants.** Two real apps define "done" for v0.1. Every feature below is justified by one of them; anything neither needs waits.

| | ten00 | leryo |
|---|---|---|
| Shape | Vite SPA + Web-standard API handlers + WebSocket | Fastify API serving the SPA + pg-boss worker |
| Build | Railpack (Node, `npm run build`) | `Dockerfile`, target `app` |
| Services | `web` (one Node process: static, `/api/*`, `/api/ws`) | `app` (:3000, health `/api/ready`), `worker` (no port) |
| Release step | none | `node src/db/migrate.ts`, needs the admin DB URL |
| State | Redis add-on (moved from Upstash) | Postgres 18 add-on, `uploads` volume |
| Needs from the proxy | WebSocket, SPA rewrites, cache headers | WebSocket + SSE through compression, 300 MB bodies, 60 min requests |
| Change in the app repo | add `server/main.ts` (serve `dist/`, mount the handlers and `ws`) and a `start` script | add `kayly.toml`; drop `docker-compose.coolify.yml` after cut-over |

**v1.0** is Kayly that someone other than its author installs from the README and leaves running: they hear when something breaks, losing the server loses no data, upgrading Kayly stops no traffic, and `kayly.toml`, `/api/v1`, the CLI and the MCP tools follow semver. Telegram bots and Mini Apps are first-class (§6), because the positioning promises them. Milestones and exit criteria: §12.

**1.0 non-goals:** multiple servers, teams and roles, autoscaling, scale to zero, a service catalogue, static hosting without a container, preview deploys, GitHub App, cron services, TCP passthrough on 443 (TURN over TLS).

## 2. Invariants

These hold across every code path, including crashes mid-deploy. Tests target them first.

1. **The live release keeps serving until its successor is healthy and routed.** No step before the proxy switch touches live containers. A failed build, release step or health check leaves production exactly as it was.
2. **A release is immutable:** image digest + resolved config + env snapshot. Rollback re-activates an old release as-is; it never rebuilds.
3. **The database (SQLite) holds desired state; Docker holds actual state.** Every container Kayly creates carries labels that map it back to project, release and service. Reconciliation compares the two; it never trusts memory.
4. **Kayly can stop, crash or upgrade without stopping traffic.** Apps and the proxy are separate long-lived containers; the daemon only steers them.
5. **A secret reaches only the services it is scoped to.** No shared `.env` file, no "every variable to every service".
6. **Add-ons outlive releases.** Postgres and volumes are project-scoped and never recreated by a deploy.
7. **One writer per project.** Deploys, rollbacks and restarts of a project are serialized.

## 3. Components

```
                    ┌────────────────────── host ──────────────────────┐
  browser / CLI ──► │  kayly-proxy (Caddy container, :80 :443)         │
  / agents (MCP)    │     │  routes from the daemon via admin API      │
                    │     ├──► kayly daemon (:7000, UI + API + MCP)    │
                    │     ├──► leryo-app-r12 ─┐                         │
                    │     └──► ten00-web-r4   │ project network         │
                    │          leryo-worker-r12 ┤                        │
                    │          leryo-postgres ──┘ (add-on)               │
                    │                                                   │
                    │  kayly daemon (systemd) ──► docker.sock           │
                    │     /var/lib/kayly: kayly.db, keys, logs, builds  │
                    └───────────────────────────────────────────────────┘
```

One Go binary, `kayly`, plays every role:

- `kayly serve`: the daemon (systemd unit). HTTP API, embedded UI, MCP endpoint, deploy pipeline, reconciler, scheduler for backups.
- `kayly <command>`: the CLI. Talks to the daemon's API over HTTPS with a token. Same binary on the server and on a laptop.

Runtime dependencies on the host, all installed and pinned by `install.sh`:

| Dependency | Role | Why not something else |
|---|---|---|
| Docker Engine + buildx plugin, containerd image store | runtime and builds (BuildKit; Railpack needs the containerd store) | Swarm/K8s cost RAM and complexity a single server does not need |
| Caddy 2, as the `kayly-proxy` container | TLS, routing, WebSocket/SSE, compression | Zero-downtime config loads with automatic rollback on a bad config, on-demand TLS for custom domains, HTTP/3. Not embedded in the daemon: invariant 4 |
| Railpack CLI | zero-config builds when there is no Dockerfile | The 2026 standard (Railway, Coolify). Used as `railpack prepare` + the BuildKit frontend, the documented production path |

## 4. Data model

SQLite (`modernc.org/sqlite`, pure Go, WAL), schema migrations embedded in the binary and applied on start.

```
project      id, name, source (git url + branch | upload), deploy_key (encrypted), webhook_secret (encrypted), created_at
release      id, project_id, seq, image, commit, config_json, env_version, status, created_at
               status: building → releasing → starting → live | failed | superseded
service      derived from release.config_json; not a table
env_var      project_id, key, value (encrypted), services (scope), version
addon        id, project_id, kind (postgres), version, credentials (encrypted), status
volume       project_id, name                          (docker volume kayly_<project>_<name>)
domain       project_id, service, hostname, status
deploy_log   release_id, path on disk                  (/var/lib/kayly/logs/<release>.log)
token        id, name, hash, scope (read | deploy | admin), project_id (null = every project),
               expires_at, created_at, last_used_at    (API/CLI/MCP tokens)
audit        id, token (name, kept after revocation), action (the route), project (name), created_at   (every write, whichever door it came through)
setting      key, value (encrypted)                    (server-wide: S3 target, notification targets)
user         id, email, password_hash                  (v0.1: one owner)
```

- `project.active_release_id` points at the release that serves traffic. It changes only in the proxy-switch step, in the same transaction that marks the release `live` and its predecessor `superseded`.
- Env values and add-on credentials are encrypted with XChaCha20-Poly1305; the key lives in `/var/lib/kayly/master.key` (0600), outside the database, so a copied `kayly.db` alone leaks nothing.
- `env_version` increments on every env change. A release records the version it was built with; rolling back restores that version's values for the release's containers but does not rewrite current values.

## 5. Configuration: `kayly.toml`

The file in the app repo is the single source of truth for shape (services, commands, ports, volumes, add-ons, env *keys*). Values of secrets live only in Kayly. `kayly init` and the "New project" screen detect the app and *write* this file; they never keep a second hidden config.

```toml
# leryo
[build]
dockerfile = "Dockerfile"
target = "app"

[release]
command = "node src/db/migrate.ts"

[services.app]
command = "node src/server.ts"
port = 3000
health = "/api/ready"
volumes = ["uploads:/data/uploads"]

[services.worker]
command = "node src/worker.ts"
volumes = ["uploads:/data/uploads"]

[env]
BETTER_AUTH_SECRET = { generate = "base64:32" }
DB_APP_PASSWORD    = { generate = "hex:32", to = [] }   # only feeds the template below
DATABASE_URL       = { template = "postgres://leryo_app:${DB_APP_PASSWORD}@${postgres.host}:5432/leryo" }
DATABASE_ADMIN_URL = { template = "${postgres.url}", to = ["release"] }   # superuser: release step only
WEB_ORIGIN         = { template = "${app.url}" }
OPENROUTER_API_KEY = { required = false }
SMTP_URL           = {}
```

Rules:

- An env key without `to` reaches every service and the release step. `to` narrows it (invariant 5); `to = []` makes it template-only.
- `generate` values are created once, on first deploy, and shown in the UI; they never change unless the user regenerates them.
- `template` resolves `${KEY}`, `${postgres.<host|port|user|password|db|url>}`, `${redis.<host|port|password|url>}` and `${<service>.url}` at release time. `${postgres.url}` is the superuser URL; scope it.
- Add-ons are not declared here: they are attached in Kayly (§8) and inject their variable (`DATABASE_URL`, `REDIS_URL`) into every target. Declaring that key in `[env]` takes it over, so leryo keeps its DML-only `DATABASE_URL`.
- Only declared keys are delivered. A stored value whose key is no longer in `[env]` is ignored and reported, so deleting a line from the file can never leak a secret to every service.
- A `command` replaces the image's entrypoint and command, and runs without a shell (words are split like a shell would split them). Use `sh -c '…'` for pipes, `&&` or `$VAR` expansion. Without `command` the image runs as built.
- Unknown keys are an error, not a warning. The parser reports file, line and the nearest valid key.
- `ports = ["7882/udp", …]` publishes ports on the host at the same numbers, for traffic the HTTP proxy cannot carry (media, TURN). Such a service never overlaps: its old container stops before the new one starts, a short gap on deploy. 80 and 443 belong to the proxy and are refused.
- `memory = "512m"` and `cpus = 1.5` cap a service, so one runaway process cannot starve the others on the server. Without them a service is unlimited.
- `[services.<name>.telegram]` makes a service a Telegram bot; see §6.
- Defaults for every service: restart `unless-stopped`, `no-new-privileges`, all capabilities dropped, log rotation 5 × 10 MB, stop grace 30 s. `read_only = true` adds a read-only root filesystem with a tmpfs on `/tmp`. Opt-outs are added when an app needs one.

## 6. Deploy pipeline

```
1 source    git fetch <commit> | receive tarball from `kayly deploy`
2 build     Dockerfile → docker buildx build --load
            otherwise  → railpack prepare → docker buildx build (railpack frontend)
            tag kayly/<project>:r<seq>; record digest
3 release   one-off container from the new image with the release env
            exit 0 → continue; non-zero or timeout → release failed, stop here
4 start     start every service of the new release next to the old one
5 health    port services: HTTP GET health path → 2xx, or TCP connect if no path
            portless services: running and not restarted for 10 s
            timeout (default 120 s) → remove new containers, release failed
6 switch    load proxy config pointing at the new containers; commit DB transaction
7 drain     stop old containers (SIGTERM, grace period), remove them
8 prune     keep the last 3 images per project for instant rollback; trim the build cache to 10 GB
```

- Steps 1–5 never touch the live release (invariant 1). Step 6 is the only point of change. Step 7 runs after the switch, so a failure there leaves garbage, not an outage; the reconciler collects it.
- **Overlap.** By default old and new containers overlap from step 4 to step 7. A service with `overlap = false` (Telegram polling bots, singletons) is stopped in step 4 *before* its successor starts; this is the only case with a gap, and it is opt-in.
- **Rollback** = steps 4–7 with an existing release's image and env snapshot. No build, no release step (expand/contract migrations keep the old code working, as leryo's runbook already requires).
- **Logs.** Each step streams to the release's log file and to subscribers over SSE. The build command is printed verbatim so the user can rerun it by hand.

### Telegram bots and Mini Apps

```toml
[services.bot]
command = "node bot.js"
port = 8080

[services.bot.telegram]
token = "BOT_TOKEN"              # the [env] key that holds the token; it must reach this service
mode = "webhook"                 # or "polling"
path = "/telegram"               # webhook only
menu_button = { text = "Open", url = "${web.url}" }   # optional: open a Mini App from the chat
```

- **Health (step 5)** also calls `getMe` with the new release's token. A wrong or revoked token fails the release before anything live changes.
- **Webhook.** Needs a `port`. Kayly generates a secret once (like `generate`) and delivers it to the service as `TELEGRAM_WEBHOOK_SECRET`; the bot checks it against the `X-Telegram-Bot-Api-Secret-Token` header. After step 6, Kayly compares `getWebhookInfo` with the wanted URL (`https://<first custom domain, else the sslip.io one><path>`) and calls `setWebhook` only if they differ. The URL does not change between releases, so an ordinary deploy never touches Telegram, and updates sent during the switch reach whichever container the proxy routes to.
- **Polling.** Implies `overlap = false`: two `getUpdates` clients get `409 Conflict`. Before the new container starts, Kayly calls `deleteWebhook` (keeping pending updates), so a bot moved from webhook to polling works at once.
- **Menu button.** After step 6, `setChatMenuButton` with a `web_app` button, under the same compare-then-set rule. `url` takes the templates of §5.
- A Telegram call after the switch that fails leaves the release live (as step 7 does), sends a notification (§9) and is retried by the reconciler every 30 s. Rollback applies the old release's `telegram` table under the same rules.
- `initData` validation is the app's job (every Telegram library ships it). The docs carry a bot and a Mini App example repo; Kayly ships no templates.

### Crash recovery

On start, and every 30 s, the reconciler:

1. Marks any release stuck in `building | releasing | starting` with no running pipeline as `failed` and removes its containers.
2. Ensures every service of each active release has a running container; starts missing ones from the release's image.
3. Removes containers labelled with a release that is neither active nor in a running pipeline.
4. Pushes the proxy config generated from the DB if it differs from what Caddy reports.
5. Re-applies the Telegram webhook and menu button of every active release whose `getWebhookInfo` differs.

Because live containers are only replaced after the switch and the proxy config is regenerated from the DB, a crash at any step converges to "the last committed active release is serving".

## 7. Networking and the proxy

- `kayly-edge`: one Docker network shared by the proxy and every port service. Upstreams are container names (`leryo-app-r12:3000`), resolved by Docker DNS, so a switch is a config load, not a port shuffle.
- `kayly-<project>`: one network per project for its services, release step and add-ons. Add-ons are *not* on the edge network.
- The proxy config is generated as Caddy JSON from the DB and loaded through the admin API on a unix socket shared with the daemon only. Caddy's `/load` is atomic and reverts a failing config without downtime.
- Domains: every service with a port gets `<service>-<project>.<ip>.sslip.io` at once, so HTTPS works before DNS. Custom domains are added in the UI; Kayly checks the A record, then lets Caddy issue the certificate.
- Proxy defaults: no request body limit, no read timeout, streaming responses flushed immediately (SSE), gzip + zstd. Per-domain headers and rewrites are not needed by either tenant (both serve their SPA from Node) and are not built.

`ponytail:` every port service shares `kayly-edge`, so services of different projects can reach each other by name. Fine while one owner runs all projects; isolate per project before Kayly hosts strangers' apps.

## 8. Add-ons and backups

One click (UI) or `kayly addons add <kind>` attaches an add-on to a project, like a Vercel integration: Kayly starts it, generates its password and injects its URL. A project has at most one add-on of each kind.

| Kind | Image (pinned by digest) | Reached at | Injects |
|---|---|---|---|
| `postgres` | `postgres:18.6-alpine`, initdb with `--locale-provider=builtin --builtin-locale=C.UTF-8` | `postgres:5432`, database = project name | `DATABASE_URL` (superuser) |
| `redis` | `valkey/valkey:9.1.2-alpine` (BSD, Redis-compatible), append-only file on | `redis:6379` | `REDIS_URL` |

- Add-ons live on the project network only, data in volume `kayly_<project>_<kind>`. The image is recorded per add-on, so upgrading Kayly never upgrades a database.
- They are not part of releases: deploys wait until add-ons are healthy, the reconciler restarts a vanished one and never removes one. Removing an add-on (typed confirmation) deletes its data; backups stay.
- Services receive a new add-on on their next deploy, or at once with `kayly redeploy` (a release of the live image with current env, no build).
### Backups

| What | How | When |
|---|---|---|
| Postgres add-on | `pg_dump -Fc` | before every release step (a bad migration can always be undone), daily, on demand |
| Redis add-on | `valkey-cli --rdb` (a point-in-time snapshot) | daily, on demand |
| Volumes | `tar` from a throwaway container that mounts the volume read-only | daily, on demand |
| Kayly itself | `VACUUM INTO` snapshot of `kayly.db`, plus `master.key` | daily, and before `kayly upgrade` |

- **Local:** 14 per project and kind in `/var/lib/kayly/backups/<project>`, downloadable from the UI and CLI. They cover mistakes and bad migrations.
- **Off-site, for a lost server:** any S3-compatible bucket (Hetzner, R2, B2, AWS, MinIO), set once with `kayly backups setup` (endpoint, bucket, access key; the secret is read from stdin). Every backup is uploaded once it is written. Objects are encrypted on the server with XChaCha20-Poly1305 under a *recovery key* that setup generates and prints once: the owner keeps it off the server, and the bucket alone leaks nothing. Kayly applies retention itself (everything from the last 7 days, then one a week for 4 weeks), because not every provider has lifecycle rules.
- **Restore:** `kayly backups restore <name|latest> [-remote] [-from <project>]` puts a dump, snapshot or volume archive into the live add-on or volume. Typed confirmation, under the project lock (invariant 7). A remote backup is downloaded and decrypted completely first; then a fresh backup of what is about to be replaced is taken, so a failed restore never leaves the data worse than it was. Postgres is restored in one transaction; Redis is written as the base file of its append-only log (Valkey ignores `dump.rdb` while the log is on); a volume is emptied and unpacked with the services that mount it stopped, and the reconciler starts them again. `-from` restores one project's backup into another, which is how a restore is rehearsed in a throwaway project.
- **Drill:** weekly, Kayly restores the newest off-site Postgres dump of every project into a throwaway container of the add-on's image (own volume, no network). A failed drill counts as a failed backup. `kayly backups drill` runs it now.
- **Lost server:** `install.sh` on a new one, then `kayly restore -endpoint … -bucket … -access-key … -recovery-key …` (secret key on stdin) before the daemon starts: it brings back `kayly.db` and `master.key` from the newest Kayly backup and downloads the newest backup of every project and kind. Then `systemctl start kayly` recreates the add-ons (empty), `kayly deploy` rebuilds each app (images are not backed up), and `kayly backups restore latest -p <project>` puts the data back. The Kayly backup is daily and on demand (`kayly backups kayly`, e.g. before an upgrade), so a lost server loses at most the changes to projects, env and add-ons since the last one.
- Every failed backup, upload or drill sends a notification (§9). A backup nobody hears about failing is not a backup.
- `tar` of a live volume can catch a file mid-write. That is fine for write-once uploads; an app that rewrites files in place keeps them in Postgres instead.

## 9. API, UI, CLI, MCP

- **API:** JSON over HTTP under `/api/v1`, SSE for logs and status. Handlers are thin; logic lives in the domain packages. Errors have one shape: `{ "error": { "code", "message", "hint" } }`. `hint` tells the user what to do next.
- **Auth:** v0.1 has one owner and API tokens only, stored hashed. `install.sh` prints one. The browser signs in with a token and gets it back as an HttpOnly, Secure, SameSite=Strict cookie; cookie-authenticated writes also need an `X-Kayly-Request` header.
- **UI:** React 19 + Vite + TanStack Router/Query + Tailwind v4, built into `web/dist` and embedded with `go:embed`. Three screens: projects, new project, project (releases · logs · env · domains · settings).
- **CLI:** `kayly login <url>`, `kayly init`, `kayly deploy` (tarball of the working tree minus `.gitignore`), `kayly logs [service]`, `kayly rollback [release]`, `kayly restart <service>` (stop and start one service of the live release, a short gap), `kayly delete` (typed confirmation; removes containers, add-ons, volumes and images, keeps backups), `kayly env set|ls|rm`, `kayly status` (server disk and memory; per service: state, restarts, CPU, memory), `kayly releases [log <release>]` (each release keeps its deploy output and git commit), `kayly backups …` and `kayly restore` (§8), `kayly notify …`.
- **Tokens** carry a scope (`read` < `deploy` < `admin`), optionally one project and an expiry. A token used by an agent should be `deploy` and project-bound. `read` lists and reads; `deploy` deploys, rolls back, sets env and domains, takes backups; `admin` adds the rest (add-ons, restores, projects, notifications, off-site, audit). A project-bound token sees and touches only its project. Every write lands in `audit` with the token that made it. Sign-in is rate-limited per IP.
- **Git source:** a project can follow a repo and branch over SSH (`kayly source set git@github.com:owner/repo.git`; HTTPS and submodules are not supported). Kayly generates an ed25519 deploy key (the public half is shown any time, to add to the repo read-only) and a webhook secret (shown once). A GitHub `push` webhook to `/api/v1/hooks/github/<project>` with a valid `X-Hub-Signature-256` fetches the pushed commit (shallow) and deploys it; pushes to other branches, deletions and pings are acknowledged and ignored, and the deploy runs on after the 202. A fetch that fails before a release exists sends a notification; later failures are alerted by the pipeline. The commit and its message are recorded on the release; `kayly source deploy [ref]` deploys a branch, tag or commit by hand. Host keys are trusted on first use (`known_hosts` in the data directory). `kayly deploy` from a working tree keeps working for any project.
- **Notifications:** to a Telegram chat (a bot token and chat id) and/or a webhook URL (JSON POST). Events: deploy or rollback failed, a service restarting in a loop or failing its health check while live, a backup, upload or drill failed, disk above 85 %, a certificate that could not be issued, a Telegram call that failed. One message per event and state change, not per check: a restart loop is 3 restarts in 10 minutes, a failing health check is 3 failed probes in a row, and each ends with a short "all clear" message. Projects busy with a deploy, rollback or restore are not judged. (The certificate event waits for domain and certificate state, which §7 describes and Kayly does not track yet.)
- **Upgrade:** `kayly upgrade [version]` downloads the release, checks its signature, takes a Kayly backup (§8), swaps the binary and restarts the unit. Apps and the proxy keep serving (invariant 4); schema migrations only move forward, so downgrading means restoring that backup.

### MCP

The daemon serves MCP at `/mcp` with the official Go SDK (`github.com/modelcontextprotocol/go-sdk`), over stateless Streamable HTTP. Auth is an API token as a Bearer header, with the token's scope and project limiting the tools:

```sh
claude mcp add --transport http kayly https://kayly.<host>/mcp --header "Authorization: Bearer <token>"
```

| Scope | Tools |
|---|---|
| `read` | `list_projects`, `get_project` (live release, service states and restarts, URLs, missing env keys, add-ons), `list_releases`, `get_release` (status, error, log tail), `get_logs` (service, tail, grep), `list_env` (keys, whether set, which services get them), `list_domains` (hostname and service; DNS and certificate state wait until Kayly tracks them), `list_backups`, `server_status` (disk, memory, versions), `validate_config` (parses a `kayly.toml`, returns errors with line numbers) |
| `deploy` | `deploy` (a git ref of the project's source), `redeploy`, `rollback`, `restart_service`, `set_env`, `delete_env`, `add_domain`, `remove_domain`, `create_backup` |
| `admin` | `add_addon`, `remove_addon`, `restore_backup`, `delete_project` |

- Tools call the same `deploy` functions as the HTTP handlers; MCP adds no logic of its own.
- No tool returns an env value or a credential. `set_env` writes; nothing reads back. Add-on connection URLs carry the superuser password, so they are left out of every MCP answer and of the HTTP API below `admin`.
- A token sees only the tools its scope allows, and each call is checked again for the project it names. Calls are audited as `mcp <tool>` under the token's name.
- Destructive tools carry `destructiveHint` and require `confirm` equal to the project name, the rule the UI uses.
- `deploy`, `redeploy` and `rollback` return the release number at once; `wait` (up to 60 s) blocks for the outcome, otherwise the agent polls `get_release`.
- Output is capped (logs at 32 KB) so a tool call cannot flood the agent's context.
- Deploying an uncommitted working tree stays the CLI's job (`kayly deploy`), which an agent runs like any command.
- OAuth 2.1, which hosted chat clients need for remote connectors, waits until after 1.0; Bearer tokens cover Claude Code, Cursor and Codex.

## 10. Code layout

```
cmd/kayly/            main: CLI commands and `serve`
internal/config/      kayly.toml: parse, validate, defaults, env resolution
internal/store/       SQLite: migrations, queries (database/sql, hand-written SQL)
internal/docker/      deploy.Runtime on the Docker Engine API (moby client)
internal/build/       buildx + railpack invocation, log streaming
internal/deploy/      pipeline, rollback, redeploy, reconciler, add-ons, backups
internal/proxy/       Caddy JSON generation (pure) + admin API client
internal/secret/      encryption, generators
internal/notify/      Telegram and webhook delivery; targets live in the `setting` table
internal/offsite/     encrypted copies of backups in an S3 bucket: crypto, retention, sync
internal/api/         HTTP handlers, auth, SSE, MCP
web/                  UI (pnpm, Vite); `dist/` embedded
install.sh            host setup: docker, buildx, railpack, caddy container, systemd unit
```

Dependency direction: `api → deploy → {proxy, store, config, secret}`; `docker` and `build` implement interfaces defined in `deploy`. Nothing imports `api`. `deploy` talks to Docker through a small `Runtime` interface so the pipeline and the reconciler are tested against an in-memory fake; `internal/docker` and `internal/build` are tested against a real Docker daemon (`KAYLY_DOCKER_TESTS=1`, as in CI).

## 11. Quality bar

- Go: `gofmt`, `go vet`, `staticcheck`, `golangci-lint` (default set + `errorlint`, `bodyclose`, `contextcheck`), `go test -race`. Errors are wrapped with context (`fmt.Errorf("start %s: %w", name, err)`) and returned, never logged and returned. Every blocking call takes a `context.Context`.
- Tests that must exist before v0.1: config parsing (every rule in section 5), proxy config generation (golden files), the pipeline against the fake runtime (each failing step leaves the active release untouched; crash at each step converges), env scoping (a key never reaches an unscoped service), and one end-to-end deploy of a fixture app in CI with real Docker.
- Web: same tooling as leryo (Biome, `tsc`, Vitest).
- Logs: `log/slog`, JSON in production, every line in a deploy carries `project` and `release`.
- Conventional commits, a CHANGELOG, signed release binaries for linux/amd64 and linux/arm64 built in CI.

## 12. Milestones

| | Delivers | Done when |
|---|---|---|
| **v0.1** | daemon, CLI, pipeline, rollback, reconciler, proxy, env, Postgres add-on, backups, minimal UI, `install.sh` | ten00 and leryo serve production from Kayly; leryo's deploy runbook fits on one page |
| **v0.2: data and alerts** (done) | backups of Redis, volumes and Kayly itself; off-site S3 with recovery key, retention and weekly drill; `backups restore`; notifications (Telegram, webhook); deploy logs and commit kept per release; service state, restarts, CPU/RAM and disk in UI and API; `memory`/`cpus` limits; build cache trimming | a failed backup reaches the owner's Telegram; leryo's DB and uploads restore from S3 into a throwaway project |
| v0.3: git and agents | git source with deploy key and GitHub webhook; project delete, service restart; token scopes, project binding, expiry; audit log; MCP (§9) | a push to leryo's `main` goes live with no command; an agent with a `deploy` token deploys, reads logs and rolls back over MCP, and is refused `remove_addon` |
| v0.4: Telegram | `[services.<name>.telegram]`: webhook, polling, `getMe` health, menu button; bot and Mini App example repos | a bot and a Mini App go from repo to live in under 2 minutes, in both modes |
| v0.5: ready for strangers | `kayly upgrade`; sign-in rate limit; signed releases checked by `install.sh` and `upgrade`; CI end-to-end deploy with real Docker, crash of the real daemon at each step, upgrade from the previous release under load; reference for `kayly.toml`, CLI, API and MCP; user docs (install, deploy, domains, backups, bots, troubleshooting, security model); CHANGELOG | every 1.0 exit criterion below is met once, on a fresh VPS |
| **v1.0** | the contracts above frozen under semver | ten00 and leryo run 30 days with no manual fix on the server; leryo is restored from S3 onto a clean VPS; v0.5 upgrades to v1.0 with zero failed requests; a fresh VPS reaches a live URL in under 5 minutes with only the README |
| after 1.0 | cron services, preview deploys, OAuth for MCP, TCP passthrough on 443, second server, teams | |
