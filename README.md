# Kayly

Deploy web apps, APIs, workers and Telegram bots to a server you own, with one command and the deploy model of a managed platform:

- **Nothing goes down when a deploy fails.** Build, migrations and health checks all run before traffic moves. A failed step leaves the live release serving.
- **Zero-downtime switches.** The new release starts next to the old one and takes traffic only once it is healthy.
- **Instant rollback** to any of the last three releases, without rebuilding.
- **Secrets per service.** A variable reaches only the services you list; generated secrets are created for you.
- **Small.** One Go binary and Caddy: about 40 MB of RAM for the whole control plane.

- **You hear about trouble, and a lost server loses no data.** Failed deploys, failed backups, restart loops and a filling disk reach your Telegram or webhook. Postgres, Redis and volumes are backed up, encrypted, to your own S3 bucket and restored from it, and a weekly drill proves the restore works.

> **Status: on the way to 1.0.** Released up to v0.3.1: deploys, rollbacks, crash recovery, env, domains, one-click Postgres and Redis, backups (local and off-site, with restore and a weekly drill), alerts, git push deploys, scoped tokens with an audit log, and an MCP server; Kayly runs its first two production apps. On `main`, unreleased: Telegram bots and Mini Apps, signed releases with `kayly upgrade`, a rate limit on token guessing, end-to-end CI with crashes and an upgrade under load, the full [documentation](docs/README.md), and a rebuilt web UI. 1.0 follows once its exit criteria hold on real servers ([roadmap](ARCHITECTURE.md#12-milestones)).

## Install

On a fresh Ubuntu 22.04+ or Debian 12+ server with ports 80 and 443 open:

```sh
curl -fsSL https://raw.githubusercontent.com/kotlovyim-dev/kayly/main/install.sh | sudo sh
```

It installs Docker (if missing), Railpack and Kayly, starts the `kayly` systemd service and prints a login command for your laptop.

## Deploy

Describe the app in `kayly.toml` at its root:

```toml
[release]                       # optional: runs before each release; failure stops the deploy
command = "node migrate.js"

[services.web]
port = 3000
health = "/health"

[services.worker]
command = "node worker.js"

[env]
SESSION_SECRET = { generate = "base64:32" }   # created on the first deploy
API_KEY        = {}                           # set with `kayly env set`
ADMIN_TOKEN    = { to = ["release"] }         # only the release step sees it
PUBLIC_URL     = { template = "${web.url}" }
```

Then, from that directory:

```sh
kayly deploy                         # build with your Dockerfile, or with Railpack if there is none
kayly env set API_KEY=...            # Kayly says which values are missing
kayly domains add app.example.com web
kayly logs web -f
kayly releases                       # with the git commit of each; `kayly releases log r12` has its output
kayly rollback r12
kayly status                         # server disk and memory; state, restarts, CPU and memory per service
```

Limits per service, so one runaway process cannot starve the rest: `memory = "512m"` and `cpus = 1.5` under `[services.<name>]`.

## Telegram bots and Mini Apps

```toml
[services.bot.telegram]
token = "BOT_TOKEN"          # the [env] key that holds the token
mode = "webhook"             # or "polling"
path = "/telegram"
menu_button = { text = "Open", url = "${web.url}" }   # optional: opens a Mini App
```

Kayly checks the token before a release starts, sets the webhook (with a secret it generates) or clears it for polling, and keeps the menu button pointed at your app. Examples: [a bot](examples/telegram-bot) and [a Mini App](examples/telegram-mini-app).

## Alerts and backups

```sh
echo "$BOT_TOKEN" | kayly notify telegram <chat-id>    # or: kayly notify webhook <url>
kayly addons add postgres                              # backed up daily and before every release step
echo "$SECRET_KEY" | kayly backups setup <endpoint> <bucket> <access-key>   # prints the recovery key once
kayly backups restore latest -remote                   # also: -from <project>, to rehearse in another project
```

On a new server after a loss: `kayly restore -endpoint … -recovery-key …` brings back Kayly's database and key and the newest backups; see [ARCHITECTURE.md §8](ARCHITECTURE.md#8-add-ons-and-backups).

Every service with a port is served at `https://<service>.<project>.<ip>.sslip.io` before you set up any DNS.

Upgrade later with `sudo kayly upgrade`: it checks the release's signature, backs Kayly up and restarts the daemon while your apps keep serving.

## Documentation

[docs/](docs/README.md) has the guides (install, deploy, domains, backups, bots, troubleshooting, security model) and the references for `kayly.toml`, the CLI, the HTTP API and MCP.

## How it works

[ARCHITECTURE.md](ARCHITECTURE.md) describes the invariants, the deploy pipeline, crash recovery and the code layout. [RESEARCH.md](RESEARCH.md) explains why Kayly exists next to Vercel, Railway, Coolify and others.

## Develop

```sh
go test -race ./...                                   # unit tests, including the pipeline against a fake runtime
KAYLY_DOCKER_TESTS=1 go test ./internal/docker ./internal/build   # against a real Docker daemon
sudo bash test/e2e/e2e.sh ./kayly                     # a whole server, on a disposable host (CI runs it)
cd web && npm test && npm run dev:mock                # the UI's tests, and the UI on sample data
```

## License

[AGPL-3.0](LICENSE).
