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
| State | Upstash Redis (external, unchanged) | Postgres 18 add-on, `uploads` volume |
| Needs from the proxy | WebSocket, SPA rewrites, cache headers | WebSocket + SSE through compression, 300 MB bodies, 60 min requests |
| Change in the app repo | add `server/main.ts` (serve `dist/`, mount the handlers and `ws`) and a `start` script | add `kayly.toml`; drop `docker-compose.coolify.yml` after cut-over |

**v0.1 non-goals:** multiple servers, teams and roles, autoscaling, scale to zero, a service catalogue, static hosting without a container, preview deploys, GitHub App, Telegram service type (v0.2), UDP ports (LiveKit stays where it is).

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
| Docker Engine + buildx plugin | runtime and builds (BuildKit) | Swarm/K8s cost RAM and complexity a single server does not need |
| Caddy 2, as the `kayly-proxy` container | TLS, routing, WebSocket/SSE, compression | Zero-downtime config loads with automatic rollback on a bad config, on-demand TLS for custom domains, HTTP/3. Not embedded in the daemon: invariant 4 |
| Railpack CLI | zero-config builds when there is no Dockerfile | The 2026 standard (Railway, Coolify). Used as `railpack prepare` + the BuildKit frontend, the documented production path |

## 4. Data model

SQLite (`modernc.org/sqlite`, pure Go, WAL), schema migrations embedded in the binary and applied on start.

```
project      id, name, source (git url + branch | upload), created_at
release      id, project_id, seq, image, commit, config_json, env_version, status, created_at
               status: building → releasing → starting → live | failed | superseded
service      derived from release.config_json; not a table
env_var      project_id, key, value (encrypted), services (scope), version
addon        id, project_id, kind (postgres), version, credentials (encrypted), status
volume       project_id, name                          (docker volume kayly_<project>_<name>)
domain       project_id, service, hostname, status
deploy_log   release_id, path on disk                  (/var/lib/kayly/logs/<release>.log)
token        id, hash, created_at, last_used_at        (API/CLI tokens)
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
8 prune     keep the last 3 images per project for instant rollback
```

- Steps 1–5 never touch the live release (invariant 1). Step 6 is the only point of change. Step 7 runs after the switch, so a failure there leaves garbage, not an outage; the reconciler collects it.
- **Overlap.** By default old and new containers overlap from step 4 to step 7. A service with `overlap = false` (Telegram polling bots, singletons) is stopped in step 4 *before* its successor starts; this is the only case with a gap, and it is opt-in.
- **Rollback** = steps 4–7 with an existing release's image and env snapshot. No build, no release step (expand/contract migrations keep the old code working, as leryo's runbook already requires).
- **Logs.** Each step streams to the release's log file and to subscribers over SSE. The build command is printed verbatim so the user can rerun it by hand.

### Crash recovery

On start, and every 30 s, the reconciler:

1. Marks any release stuck in `building | releasing | starting` with no running pipeline as `failed` and removes its containers.
2. Ensures every service of each active release has a running container; starts missing ones from the release's image.
3. Removes containers labelled with a release that is neither active nor in a running pipeline.
4. Pushes the proxy config generated from the DB if it differs from what Caddy reports.

Because live containers are only replaced after the switch and the proxy config is regenerated from the DB, a crash at any step converges to "the last committed active release is serving".

## 7. Networking and the proxy

- `kayly-edge`: one Docker network shared by the proxy and every port service. Upstreams are container names (`leryo-app-r12:3000`), resolved by Docker DNS, so a switch is a config load, not a port shuffle.
- `kayly-<project>`: one network per project for its services, release step and add-ons. Add-ons are *not* on the edge network.
- The proxy config is generated as Caddy JSON from the DB and loaded through the admin API on a unix socket shared with the daemon only. Caddy's `/load` is atomic and reverts a failing config without downtime.
- Domains: every service with a port gets `<service>-<project>.<ip>.sslip.io` at once, so HTTPS works before DNS. Custom domains are added in the UI; Kayly checks the A record, then lets Caddy issue the certificate.
- Proxy defaults: no request body limit, no read timeout, streaming responses flushed immediately (SSE), gzip + zstd. Per-domain headers and rewrites are not needed by either tenant (both serve their SPA from Node) and are not built.

`ponytail:` every port service shares `kayly-edge`, so services of different projects can reach each other by name. Fine while one owner runs all projects; isolate per project before Kayly hosts strangers' apps.

## 8. Add-ons: Postgres and Redis

One click (UI) or `kayly addons add <kind>` attaches an add-on to a project, like a Vercel integration: Kayly starts it, generates its password and injects its URL. A project has at most one add-on of each kind.

| Kind | Image (pinned by digest) | Reached at | Injects |
|---|---|---|---|
| `postgres` | `postgres:18.6-alpine`, initdb with `--locale-provider=builtin --builtin-locale=C.UTF-8` | `postgres:5432`, database = project name | `DATABASE_URL` (superuser) |
| `redis` | `valkey/valkey:9.1.2-alpine` (BSD, Redis-compatible), append-only file on | `redis:6379` | `REDIS_URL` |

- Add-ons live on the project network only, data in volume `kayly_<project>_<kind>`. The image is recorded per add-on, so upgrading Kayly never upgrades a database.
- They are not part of releases: deploys wait until add-ons are healthy, the reconciler restarts a vanished one and never removes one. Removing an add-on (typed confirmation) deletes its data; backups stay.
- Services receive a new add-on on their next deploy, or at once with `kayly redeploy` (a release of the live image with current env, no build).
- **Backups:** `pg_dump -Fc` before every release step (a bad migration can always be undone), daily, and on demand; 14 kept per project in `/var/lib/kayly/backups/<project>`, downloadable from the UI and CLI.
- `ponytail:` backups stay on the server's disk: they cover mistakes and bad migrations, not a lost server. Off-site copies (S3) and restore drills come next. Volumes (leryo's uploads) are not backed up yet.

## 9. API, UI, CLI, MCP

- **API:** JSON over HTTP under `/api/v1`, SSE for logs and status. Handlers are thin; logic lives in the domain packages. Errors have one shape: `{ "error": { "code", "message", "hint" } }`. `hint` tells the user what to do next.
- **Auth:** v0.1 has one owner and API tokens only, stored hashed. `install.sh` prints one. The browser signs in with a token and gets it back as an HttpOnly, Secure, SameSite=Strict cookie; cookie-authenticated writes also need an `X-Kayly-Request` header.
- **UI:** React 19 + Vite + TanStack Router/Query + Tailwind v4, built into `web/dist` and embedded with `go:embed`. Three screens: projects, new project, project (releases · logs · env · domains · settings).
- **CLI:** `kayly login <url>`, `kayly init`, `kayly deploy` (tarball of the working tree minus `.gitignore`), `kayly logs [service]`, `kayly rollback [release]`, `kayly env set|ls|rm`, `kayly status`.
- **MCP:** the same operations as the CLI, served by the daemon at `/mcp`, authorized with an API token.

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
| v0.2 | GitHub webhook auto-deploy, Telegram bot service type (webhook/polling), Mini App template | a bot and Mini App go from repo to live in under 2 minutes |
| v0.3 | preview deploys, custom-domain on-demand TLS, MCP polish, cron services | |
| later | second server, UDP ports, teams | |
