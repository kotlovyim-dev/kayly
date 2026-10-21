# CLI reference

One binary, `kayly`, is both the daemon and its CLI. The daemon runs on Linux; the CLI also runs on macOS and Windows, installed with `npm install -g kayly` (or run as `npx kayly …`) or from the releases page. On the server the CLI talks to the daemon's root-only admin socket (`/run/kayly/admin.sock`, or `$KAYLY_RUN/admin.sock`) and needs no login. Anywhere else it uses the server and token saved by `kayly login`, or `KAYLY_URL` and `KAYLY_TOKEN` from the environment (for CI).

Commands that act on a project take `-p <project>`; without it, the project is the current directory's name. Flags may come before or after the arguments. Errors print the message and, when Kayly knows one, the next step.

## On the server

| Command | What it does |
|---|---|
| `kayly serve --base-domain <ip>.sslip.io [--domain host] [--email addr] [--data dir] [--run dir] [--railpack path] [--build-cache 10g]` | Runs the daemon. `install.sh` sets it up as the `kayly` systemd unit. |
| `kayly token create <name> [-scope read\|deploy\|admin] [-p project] [-expires-days n]` | Creates an API token and prints it once. Default scope `admin`, no project, no expiry. |
| `kayly token ls` / `kayly token rm <id>` | Lists or revokes tokens. |
| `kayly audit [-n lines]` | Who changed what, newest first. |
| `kayly upgrade [version] [-binary path] [-wait 10m]` | Installs a release (default: the latest) after checking its signature and checksum, takes a Kayly backup, swaps the binary (the old one is kept as `kayly.previous`) and restarts the daemon. Running deploys finish first; apps and the proxy keep serving. Refuses an older version. `-binary` installs a local file without a signature check. |
| `kayly restore -endpoint E -bucket B -access-key K -recovery-key KEY [-region R]` | On a new server, right after `install.sh`: stops the daemon, sets the fresh install's empty state aside, brings back Kayly's database, master key and the newest backups from the bucket, and starts the daemon again. Refuses a state that holds any project. The S3 secret key comes on stdin. |

## Projects and deploys

| Command | What it does |
|---|---|
| `kayly login <url> <token>` | Checks the token and saves the server for this user. |
| `kayly deploy [dir]` | Uploads `dir` (default `.`, without what `.gitignore` excludes), creates the project on first use, and streams the deploy. Exits non-zero when the release fails. The current git commit and message are recorded. |
| `kayly projects` | Projects with their live release. |
| `kayly status` | The server's disk and memory; per service: state, restarts, CPU, memory. All projects, or `-p` one. |
| `kayly releases` / `kayly releases log <release>` | Releases with status, commit and error; the full output of one deploy. |
| `kayly rollback <release>` | Makes an earlier release live again with its own image and env; no build. |
| `kayly redeploy` | Releases the live image again with the current env and add-ons; no build. |
| `kayly logs <service> [-f] [-n 100]` | Output of a service of the live release. |
| `kayly restart <service>` | Stops and starts one service of the live release (a short gap). |
| `kayly delete` | Removes the project with its containers, add-ons, volumes and images; backups stay. Asks for the project name. |
| `kayly source set <git-url> [-branch main]` / `show` / `rm` / `deploy [ref]` | Follows a repository over SSH: prints the deploy key to add read-only and the webhook to create. A push to the branch deploys; `deploy [ref]` deploys a branch, tag or commit now. |

## Configuration

| Command | What it does |
|---|---|
| `kayly env ls` / `set KEY=value…` / `rm KEY…` | Env values (never printed back). They reach services on the next deploy or redeploy. |
| `kayly domains ls` / `add <hostname> <service>` / `rm <hostname>` | Custom domains. Point an A record at the server first. |
| `kayly addons ls` / `add postgres\|redis` / `rm <kind>` | Add-ons. `rm` deletes the data and asks for the project name. |
| `kayly notify ls` / `telegram <chat-id>` / `webhook <url>` / `off telegram\|webhook` / `test` | Where alerts go. `telegram` reads the bot token from stdin. |

## Backups

| Command | What it does |
|---|---|
| `kayly backups ls [-remote]` / `create` / `get <name>` | Lists, takes or downloads the project's backups. |
| `kayly backups restore <name\|latest> [-remote] [-from project]` | Replaces live data with a backup after a fresh backup of what is replaced. Asks for the project name. `latest` restores the newest backup of each kind. |
| `kayly backups setup <endpoint> <bucket> <access-key> [region]` | Off-site S3; the secret key comes on stdin. Prints the recovery key once. |
| `kayly backups status` / `sync` / `drill` / `kayly` | Off-site state; upload now; restore-test the newest dumps now; snapshot Kayly's own database and key. |

## Compatibility

From v1.0 the commands, flags and exit codes above follow semantic versioning: what works in 1.x keeps working in later 1.x. Output meant for people (tables, messages) may change; scripts should rely on exit codes and on the [HTTP API](api.md) for data.
