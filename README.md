# Kayly

Deploy web apps, APIs, workers and Telegram bots to a server you own, with one command and the deploy model of a managed platform:

- **Nothing goes down when a deploy fails.** Build, migrations and health checks all run before traffic moves. A failed step leaves the live release serving.
- **Zero-downtime switches.** The new release starts next to the old one and takes traffic only once it is healthy.
- **Instant rollback** to any of the last three releases, without rebuilding.
- **Secrets per service.** A variable reaches only the services you list; generated secrets are created for you.
- **Small.** One Go binary and Caddy: about 40 MB of RAM for the whole control plane.

> **Status: v0.1 in progress.** Deploys, rollbacks, crash recovery, env, domains and the CLI work end to end. Postgres add-on with backups and a web UI are next. Not yet run on a production server.

## Install

On a fresh Ubuntu or Debian server with ports 80 and 443 open:

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
kayly releases
kayly rollback r12
```

Every service with a port is served at `https://<service>.<project>.<ip>.sslip.io` before you set up any DNS.

## How it works

[ARCHITECTURE.md](ARCHITECTURE.md) describes the invariants, the deploy pipeline, crash recovery and the code layout. [RESEARCH.md](RESEARCH.md) explains why Kayly exists next to Vercel, Railway, Coolify and others.

## Develop

```sh
go test -race ./...                                   # unit tests, including the pipeline against a fake runtime
KAYLY_DOCKER_TESTS=1 go test ./internal/docker ./internal/build   # against a real Docker daemon
```

## License

[AGPL-3.0](LICENSE).
