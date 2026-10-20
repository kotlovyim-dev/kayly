# Deploy

## Describe the app

Add `kayly.toml` at the root of the app. A web service with a health check and a worker:

```toml
[release]
command = "node migrate.js"          # optional; a failure stops the deploy

[services.web]
port = 3000
health = "/health"

[services.worker]
command = "node worker.js"

[env]
SESSION_SECRET = { generate = "base64:32" }
API_KEY        = {}
PUBLIC_URL     = { template = "${web.url}" }
```

Kayly builds with your `Dockerfile` if `[build] dockerfile` names one, and with Railpack otherwise. Every key is in the [kayly.toml reference](reference/kayly-toml.md).

## Deploy

```sh
kayly deploy
```

From the app's directory, this uploads the working tree (without what `.gitignore` excludes), creates the project the first time (named after the directory, or `-p name`) and streams every step:

1. **env**: every required value must be set. A missing one fails here, in a second, with the exact `kayly env set` to run.
2. **build**: the image, tagged `kayly/<project>:r<n>`.
3. **release step**: `[release] command` in a one-off container, after a Postgres backup.
4. **start**: the new containers start next to the live ones.
5. **health**: the new services must pass their checks within 2 minutes.
6. **switch**: the proxy routes to the new release in one atomic config load.
7. **drain**: the old containers stop with a 30 s grace period.

Steps 1 to 5 never touch the live release: a failure anywhere there leaves production exactly as it was, and the deploy output says what failed. Nothing is lost if Kayly itself crashes mid-deploy; on restart it marks the interrupted release failed and removes its containers.

## Releases and rollback

Every deploy is an immutable release: image, config and env as they were. `kayly releases` lists them with their commit; `kayly releases log r12` shows a deploy's full output.

```sh
kayly rollback r11      # r11's image and env, live again in seconds; no build
kayly redeploy          # the live image again, with the current env and add-ons
```

The last three images are kept for rollback.

## Env values

```sh
kayly env set API_KEY=sk_live_…
kayly env ls
```

Values are encrypted on the server and never shown again. They reach services on the next deploy or `kayly redeploy`. A key reaches every service unless `to` in `kayly.toml` narrows it, and only keys declared in `kayly.toml` are ever delivered.

## Deploy on push

```sh
kayly source set git@github.com:you/app.git -branch main
```

This prints a deploy key to add to the repository (read-only) and a webhook to create. From then on, a push to `main` deploys that commit. `kayly source deploy v1.2.0` deploys a tag, branch or commit by hand. Only SSH URLs are supported.

## Watch it run

```sh
kayly logs web -f
kayly status            # disk and memory; per service: state, restarts, CPU, memory
kayly restart worker    # stop and start one service (a short gap)
```

The web UI shows the same: the live release, its track of past releases, services with CPU and memory, logs, and settings for env values, add-ons, domains and backups.

## Limits

Cap a service so one runaway process cannot starve the rest:

```toml
[services.worker]
memory = "512m"
cpus = 1.5
```
