# Kayly: research

> Deploy anything (site, backend, worker, Telegram bot, Mini App) in one action, with a minimalist interface.
> Date: 2026-10-17. Prices and versions come from public sources as of that date. Re-check them before you make decisions.

---

## 0. Summary

1. **The niche exists.** Vercel does frontends well but does not hold long-running processes, WebSockets or workers. Railway and Render do, but cost 2–5x more than your own server. Coolify and Dokploy are cheap, but complex, and a Compose stack goes down during a deploy. None of them treats Telegram bots as their own app type.
2. **Positioning:** "Vercel UX on your own server, plus Telegram bots and Mini Apps out of the box". leryo is the first customer, ten.00 the second.
3. **Model:** self-hosted first (one binary on your VPS, BYO server), a managed cloud later. A multi-tenant cloud needs isolation (gVisor/Firecracker), billing and abuse protection: a separate product and a year of work.
4. **Stack:** a Go daemon in one binary that drives Caddy (proxy and TLS, a separate container), Railpack/BuildKit (builds), Docker Engine (runtime) and SQLite (state). A React/Vite UI (as in leryo) embedded in the binary. CLI and MCP server from day one, since you work through agents.
5. **The main decision is yours:** self-hosted first or a cloud right away (section 8).

---

## 1. What hurts today (leryo as the example)

leryo deploys to Coolify through `docker-compose.coolify.yml`. The runbook `leryo/docs/operations/deploy.md` is 139 lines long, and almost every item works around a platform limitation:

| Pain in leryo | Cause | How Kayly should do it |
|---|---|---|
| A deploy takes the site down for 5–30 s, so deploys must avoid lesson times | Coolify does no rolling update for Compose ([docs](https://coolify.io/docs/applications/deployments/rolling-updates)) | Blue-green by default: the new version starts, passes its health check, the proxy switches, the old one drains |
| If `migrate` fails, the site stays down until manual recovery | Compose has already stopped the old containers | A **release step** as a first-class concept: it runs before the switch; on failure the old version keeps running |
| Every service sees `DB_ADMIN_PASSWORD` | Coolify adds `env_file: .env` to every service ([#5612](https://github.com/coollabsio/coolify/issues/5612)) | Secrets scoped per service, each grant explicit |
| `entrypoint: sh -c export …` tricks so Coolify leaves variables alone | Coolify keeps UI variables whose name is a compose key | The platform never rewrites the config |
| Rollback = type a SHA into Git settings and press Deploy | No model of releases | A list of releases, a "Roll back" button, instant switch to the old image |
| 30+ env vars by hand, passwords via `openssl rand` | No generation | Kayly reads `.env.example`, generates secrets itself, and wires `DATABASE_URL` from the attached database |
| `:3000` in the domain to pick the port | UI convention | The port comes from `EXPOSE` or from what the process listens on |
| Database backups still in the backlog (MAK-11) | Separate task | A Postgres resource with S3 backups and restore checks by default |
| LiveKit as a separate resource, UDP ports | Special case | Escape hatch: a "raw" service with host ports (not in the MVP) |

Conclusion: what leryo needs most is not a "simpler UI" but the **right deploy model** (release, release step, blue-green, per-service secrets). A simple UI grows out of it.

---

## 2. Competitors

### 2.1 Managed platforms

| | Strengths | Weaknesses | Price (2026) |
|---|---|---|---|
| **Vercel** | The UX benchmark: import repo → auto-detect → Deploy. A preview per PR. Express, Fastify and FastAPI now deploy with zero config as Functions on Fluid compute ([docs](https://vercel.com/docs/frameworks/backend/fastify)) | Backends only as functions: no long-running processes, WebSocket servers, queue workers or a Postgres next to them. leryo (Socket.IO, SSE, pg-boss) does not fit | Hobby $0, Pro $20/seat + usage, billed by active CPU |
| **Railway** | Closest in spirit: a service canvas, Railpack with zero config, ~2 min to first deploy, an Agent Skill for coding agents, opt-in sleep | Usage billing with no hard cap. HA Postgres still experimental. 5 major incidents Nov 2025 – May 2026 ([bex](https://bex.co/blog/2026/10/14/railway-vs-render-vs-flyio-july-2026-benchmarks)) | Hobby $5, Pro $20/workspace + usage, egress $0.05/GB |
| **Render** | Predictable per-service pricing, a preDeploy command (release step), blueprints | Free services sleep after 15 min, cold start 30–60 s. In 2026 added fees for bandwidth ($0.15/GB) and for domains beyond 15 | Pro $25 + compute from $7/service |
| **Fly.io** | 18 regions, cold start 0.3–2 s, cheap egress | Hardest UX (5–10 min to first deploy), Managed Postgres v2 still in beta, GPUs shut down 2026-07-31 | Pay-as-you-go from ~$2/machine |
| **Heroku** | Invented `git push`, Procfile, release phase | In sustaining engineering since 2026-02-06: no new features ([bex](https://bex.co/blog/2026/09/08/heroku-sustaining-mode-migration-timing)) | Std-1X $25 |
| **Sliplane** | Fixed price per server, any Docker image | No autoscaling, minimal features | from €9 |
| **Northflank / Koyeb** | K8s, BYOC, GPU / serverless, scale to zero | Complex / expensive for small teams. Koyeb was acquired by Mistral (2026-02) | from $10 / $29 + usage |

### 2.2 Self-hosted

| | Architecture | Strengths | Weaknesses |
|---|---|---|---|
| **Coolify** v4.4.6, Apache-2.0, ~63k★ | PHP/Laravel, Traefik, Docker. Needs ~2 GB RAM | 280+ one-click services, DB backups, previews, Railpack since v4.1, MCP server | Overloaded UI, Compose without zero downtime, env problems, fast releases ("test updates on a staging instance first") |
| **Dokploy** v0.30, ~38k★ | Next.js, Traefik, Docker Swarm | Cleaner UI | Pre-1.0. Part of the code is under DSAL (production use needs a commercial license) |
| **CapRover** | Swarm | Stable since 2017 | Dated UX, partial Compose support |
| **Dokku** MIT | CLI + plugins | Lightest, `git push` | No UI |
| **Kamal 2** MIT (37signals) | SSH from a laptop, kamal-proxy (Go) | Zero downtime, config in the repo, no overhead on the server | No UI, databases, logs or previews. A tool, not a platform |

### 2.3 Telegram-specific

- **Telegram Serverless (tgcloud)** is a new and serious competitor for simple bots. Telegram itself hosts the bot and the Mini App: a V8 isolate, built-in SQLite, `npx tgcloud push`. Mini App hosting arrived on **2026-10-06** ([docs](https://core.telegram.org/bots/serverless)).
  Limits: JS only, **no npm packages**, no filesystem, network only through `sdk.fetch`, no foreign keys, no Python. Pricing not published.
  → Kayly does not compete with it for "hello-world bots"; it takes everything else: aiogram/grammY/telegraf from npm, Postgres, AI calls, queues, files, a real backend for the Mini App.
- Niche bot hosts (SquareCloud, JustRunMy.App, Pandastack) offer "upload a zip, get a process". Weak UX, webhooks and Mini Apps not automated.

### 2.4 What nobody does (your differentiators)

1. **A bot as an app type.** Kayly knows the token, calls `setWebhook` with `secret_token` on deploy, and uses a test bot for previews. For polling bots it stops the old version first, because two concurrent `getUpdates` calls return `409 Conflict`.
2. **A Mini App in one click:** static frontend + backend + `setChatMenuButton` + ready-made `initData` validation.
3. **A release step that cannot take production down** when a migration fails.
4. **Per-service secrets** and password generation from `.env.example`.
5. **Agent-first:** CLI + MCP, so Claude can deploy, read logs and roll back on its own. Railway and Coolify are moving this way, but as an add-on.

---

## 3. Economics (why self-hosted)

The same workload, "3 apps of 512 MB + a worker + Postgres" ([pethost](https://pethost.dev/blog/railway-alternatives/), indicative):

| Railway | Render | Fly.io | Hetzner server |
|---|---|---|---|
| ~$23 + CPU | ~$35 | $17–53 | ~€6–20 |

At 1 TB of traffic the gap widens: Render ~$150, Railway ~$51, Fly ~$20, Hetzner $0 (20 TB included).
Caveats: Hetzner raised prices twice in 2026 (CX/CAX +33–37%, x86 up to +176% for new orders), and CX/CAX are periodically unavailable. Kayly must not be tied to one provider: "any Ubuntu server with SSH".

---

## 4. Minimal UX

### 4.1 Principles

1. **One action to the first URL.** Paste a repo or run `kayly` in a folder → get `https://…`. Everything else has sensible defaults and can change later.
2. **Show a plan, not a form.** Instead of 15 fields, show the detected result, editable:
   ```
   leryo
   ├─ app      Node 24 · node src/server.ts · :3000   → leryo.example.com
   ├─ worker   node src/worker.ts                     (no port)
   ├─ release  node src/db/migrate.ts                 (before every release)
   ├─ postgres 18                                     DATABASE_URL ✓
   └─ volume   /data/uploads
   Env: 6 generated · 4 to fill in
                                        [ Deploy ]
   ```
3. **Ask only what cannot be guessed.** Kayly generates secrets that can be generated (`*_SECRET`, `*_PASSWORD`, `*_KEY` without an external service). External keys (`OPENROUTER_API_KEY`) appear as a list with one field each; optional ones are collapsed.
4. **State as a word and a coloured dot, no dashboards:** `● Live · 2 min ago · a1b2c3 feat: billing`.
5. **Error = cause + action.** Not "Build failed", but "`pnpm install` failed: the lockfile is out of date. [Show log] [Roll back to the previous release]".
6. **No decorative text.** The same rule as in leryo: no grey subtitles or taglines. Every screen shows live data or does something.
7. **Keyboard:** `⌘K` for everything (deploy, rollback, logs, env).

### 4.2 Screen map (MVP = 3 screens)

```
/                 Projects: a list of rows (name · URL · state · last deploy). A "New" button.
/new              One field: a GitHub repo or a template (Static · Node API · Bot · Mini App). Then plan → Deploy.
/p/:name          Project. URL and state on top. Tabs: Releases · Logs · Env · Domains · Settings.
```

No separate "Servers", "Teams", "Sources", "Destinations", "Keys" pages (as in Coolify). There is one server, and servers appear only once there are two.

### 4.3 User journeys

| Scenario | Steps |
|---|---|
| Static site / Vite SPA | repo → Deploy (1 click) |
| leryo | repo → plan (detected from Dockerfile/compose) → fill in 4 keys → Deploy |
| Telegram bot | "Bot" template or repo → paste the token from @BotFather → Deploy. The webhook is set automatically |
| Mini App | repo → token → Deploy. Kayly points the menu button at the app URL |
| From a terminal / an agent | `kayly` in the project folder (like `vercel`): an archive goes to the server, is built there, and a URL comes back |

### 4.4 Design

- Monochrome, one accent colour only for state and the primary button. Inter or Geist, monospace for logs and hashes.
- Dense rows instead of cards. A projects table like Linear: 40 px rows, hover actions on the right.
- Full-width streaming logs, filter by service, search.
- Dark theme following the system.
- References: Vercel (project dashboard), Linear (density, ⌘K), Railway (service plan, but as a list rather than a canvas: a canvas pays off from 6+ services).

---

## 5. Processes: the deploy model

```
push / kayly / click
  → build      Railpack (auto-detect) | Dockerfile
  → image      tagged by commit
  → release    one-off container from the new image (migrations). Failure = stop, production untouched
  → start      new containers next to the old ones
  → health     HTTP check of /health or the TCP port; for bots: process alive for N seconds
  → switch     Caddy switches the upstream (atomically)
  → drain      old containers get SIGTERM after a grace period
  → keep       the last 3 images for instant rollback
```

Rules that remove manual work:

- **Service = image + command.** app, worker and release run from one image with different commands (exactly how leryo already works).
- **The Postgres/Redis add-on** wires `DATABASE_URL`/`REDIS_URL` only into the services you mark.
- **Backups:** nightly `pg_dump` to any S3-compatible storage, retention 7 daily / 4 weekly, a weekly test restore into a throwaway container.
- **Preview deploys** per PR: a separate subdomain and a separate database with an empty schema. Bots can take a second (test) token.
- **Domains:** instantly `<app>.<ip>.sslip.io` with HTTPS and no DNS (check Let's Encrypt limits for sslip.io), then your own domain: show one A record and wait for DNS to propagate.
- **Updating Kayly** is one command, `kayly upgrade`. Only the control plane updates; apps keep running.

### Installation

```bash
curl -fsSL https://get.kayly.dev | sh
```
The script installs Docker (if missing), downloads one binary, creates a systemd unit and prints a one-time link `https://<ip>.sslip.io/claim?token=…`. Target: under 60 seconds on a fresh Ubuntu and under 150 MB RAM for the control plane, so a 1 GB server works. Coolify and Dokploy ask for ~2 GB.

---

## 6. Stack

### Recommendation

| Layer | Choice | Why |
|---|---|---|
| Daemon / control plane | **Go**, one static binary | Every key component is already written in Go: Caddy, BuildKit, Railpack, the Docker SDK, kamal-proxy as a reference. No Node or PHP on the server |
| Proxy + TLS | **Caddy, as a separate container driven by the daemon** | Automatic HTTPS, on-demand TLS with an `ask` endpoint for customer domains, atomic upstream switches through the admin API. Not embedded in the daemon, so a Kayly upgrade or crash never stops traffic (see ARCHITECTURE.md) |
| Builds | **Railpack** (BuildKit frontend) + a Dockerfile when present | The 2026 standard (Railway, Coolify). Images 38–77% smaller than with Nixpacks |
| Runtime | **Docker Engine**, no Swarm/K8s | One server covers 95% of cases. Multiple servers later, through an agent per server |
| State | **SQLite** (pure-Go driver, WAL) | One file, backed up with the config. Postgres for a single-server control plane is overkill |
| UI | **React 19 + Vite + TanStack Router/Query + Tailwind v4** | Your stack from leryo: agents already know it, design tokens can carry over. Embedded in the binary with `go:embed` |
| Live logs / state | SSE | Simpler than WebSocket, passes through any proxy |
| Auth | One owner, password + API tokens (v0.1); GitHub sign-in with the GitHub App later | No accounts system to build first |
| Git | GitHub webhook / GitHub App (push/PR events, per-repo tokens) | As in Vercel/Coolify |
| CLI | The same Go binary (`kayly deploy`, `kayly logs`, `kayly rollback`) | One artifact for the server and the laptop |
| MCP | Built into the daemon, the same operations as the CLI | Agents deploy and debug without the UI |

### Alternatives considered and rejected

- **TypeScript/Node for the daemon.** Familiar to you, but then the server needs Node, and Caddy and BuildKit have to be driven as external processes anyway. Keep TS for the UI.
- **Kubernetes / k3s.** On one VPS that is 500+ MB of overhead and complexity the user never sees but you do at 3 a.m.
- **Docker Swarm** (Dokploy, CapRover). Dated, and Compose files do not run as is.
- **Nixpacks.** In maintenance mode; Railway has moved fully to Railpack.
- **Firecracker / gVisor.** Needed only for a multi-tenant cloud (phase 4).

---

## 7. Config: `kayly.toml`

The file is described in [ARCHITECTURE.md §5](ARCHITECTURE.md), with the full leryo example. A bot would look like this (the decided syntax is in ARCHITECTURE.md §6):

```toml
[services.bot]
command = "python -m bot"
telegram = { token = "BOT_TOKEN", mode = "webhook", path = "/tg" }   # or mode = "polling"
```

Kayly also imports `docker-compose.yml`: it converts it into its own services instead of running `compose up`. `compose up` is exactly what causes the downtime and the env confusion in Coolify.

---

## 8. Decisions for the owner

1. **Self-hosted first or a cloud?** Recommended: self-hosted. Smaller scope, immediately useful for leryo/ten.00, open source as a distribution channel (that is how Coolify and Dokploy grew). A cloud can come later as "we run your server" (Hetzner via API, from ~$9/mo), without multi-tenant isolation. **Decided: self-hosted, open source from day one.**
2. **License.** AGPL for the core (stops anyone reselling it as a closed cloud) or Apache-2.0 (maximum trust). A split like Dokploy's (DSAL) is received badly by the community.
3. **Python bots from day one?** Railpack handles them, so the cost is low. Recommended: yes; aiogram is very popular.

---

## 9. Plan

The milestone plan lives in [ARCHITECTURE.md §12](ARCHITECTURE.md).

Not in the MVP: teams and roles, multiple servers, a 280-service one-click catalogue, autoscaling, a canvas, metrics beyond CPU/RAM/restarts.

---

## Sources

- Coolify: [rolling updates](https://coolify.io/docs/applications/deployments/rolling-updates) · [issue #5612](https://github.com/coollabsio/coolify/issues/5612) · [discussion #3767](https://github.com/coollabsio/coolify/discussions/3767) · [Railpack in Coolify](https://coolify.io/docs/applications/build-packs/railpack) · [v5 review](https://www.bitdoze.com/coolify-v5-self-hosted-paas-review/)
- Self-hosted comparisons: [WZ-IT 2026](https://wz-it.com/en/blog/self-hosted-paas-comparison-coolify-dokploy-caprover/) · [Coolify vs Dokploy vs Kamal 2](https://www.bitdoze.com/coolify-vs-dokploy-vs-kamal-2/) · [getdeploying](https://getdeploying.com/guides/coolify-vs-dokploy)
- Prices and benchmarks: [bex: re-benchmark 2026-10](https://bex.co/blog/2026/10/14/railway-vs-render-vs-flyio-july-2026-benchmarks) · [bex: Heroku sustaining](https://bex.co/blog/2026/09/08/heroku-sustaining-mode-migration-timing) · [pethost](https://pethost.dev/blog/railway-alternatives/) · [Qovery](https://www.qovery.com/blog/git-push-deploy-platforms-cheaper-than-heroku) · [Northflank: Railway alternatives](https://northflank.com/blog/railway-alternatives) · [Railway pricing tracker](https://pricingsaas.com/companies/railway) · [Railway serverless](https://docs.railway.com/guides/cut-idle-costs-serverless.md) · [Render pricing](https://livemy.app/blog/render-pricing) · [Sliplane vs Koyeb](https://sliplane.io/blog/sliplane-vs-koyeb)
- Hetzner: [WZ-IT: June 2026](https://wz-it.com/en/blog/hetzner-price-increase-june-2026-cpx-ccx-alternatives/) · [findstack](https://findstack.com/resources/hetzner-price-increase-2026)
- Railpack: [Railway blog](https://blog.railway.com/p/introducing-railpack) · [running in production](https://railpack.com/platforms/running-railpack-in-production) · [nixpacks vs railpack](https://www.bitdoze.com/nixpacks-vs-railpack)
- Caddy: [admin API](https://caddyserver.com/docs/api) · [on-demand TLS](https://caddy.community/t/use-case-for-on-demand-tls/19944)
- Vercel: [Fastify](https://vercel.com/docs/frameworks/backend/fastify) · [Express](https://vercel.com/docs/frameworks/backend/express) · [Import](https://vercel.com/docs/getting-started-with-vercel/import)
- Telegram: [Serverless](https://core.telegram.org/bots/serverless) · [Mini Apps](https://core.telegram.org/bots/webapps) · [setWebhook](https://core.telegram.org/bots/api#setwebhook) · [bot hosting 2026](https://pandastack.io/blog/best-telegram-bot-hosting-2026)
