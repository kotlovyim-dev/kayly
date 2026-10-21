# kayly

The command-line client for [Kayly](https://github.com/kotlovyim-dev/kayly): deploy web apps, APIs, workers and Telegram bots to a server you own, with the deploy model of a managed platform. A release goes live only when it is healthy, a failed deploy changes nothing, and a bad release rolls back in one command.

This npm package is the CLI for your laptop or CI. The daemon itself runs on a Linux server, installed with Kayly's install script (see [Install](https://github.com/kotlovyim-dev/kayly/blob/main/docs/install.md)).

## Install

```sh
npm install -g kayly
```

Or run any command without installing:

```sh
npx kayly --help
```

Requires Node.js 20 or newer.

The package holds no binary. The first time you run `kayly`, it downloads the binary of its own version for your system from the [GitHub release](https://github.com/kotlovyim-dev/kayly/releases), checks the release's ed25519 signature and the binary's SHA-256 checksum, and keeps it in your cache. Later runs use the cached copy.

| System | Architectures |
|---|---|
| Linux | amd64, arm64 |
| macOS | amd64, arm64 |
| Windows | amd64, arm64 |

Without Node.js, download the binary for your system from the [releases page](https://github.com/kotlovyim-dev/kayly/releases) (for example `kayly-windows-amd64.exe` or `kayly-darwin-arm64`) and put it on your `PATH`.

### Where the binary is cached

| System | Directory |
|---|---|
| Windows | `%LOCALAPPDATA%\kayly` |
| macOS | `~/Library/Caches/kayly` |
| Linux | `$XDG_CACHE_HOME/kayly`, or `~/.cache/kayly` |

## Quick start

```sh
# 1. Sign in with the server URL and a token the server created for you
kayly login https://kayly.203-0-113-7.sslip.io kayly_…

# 2. From your app's directory, deploy
kayly deploy

# 3. Watch it
kayly logs web -f
kayly releases
```

`kayly deploy` uploads the working tree (minus what `.gitignore` excludes), creates the project on first use, and streams the deploy. It exits non-zero when the release fails.

## Commands

Commands that act on a project take `-p <project>`. Without it, the project is the name of the current directory. Flags may come before or after arguments. When Kayly knows the next step after an error, it prints that too.

### Projects and deploys

| Command | What it does |
|---|---|
| `kayly login <url> <token>` | Checks the token and saves the server for this user. |
| `kayly deploy [dir]` | Uploads `dir` (default `.`) and deploys it. Records the current git commit and message. |
| `kayly projects` | Lists projects with their live release. |
| `kayly status [-p project]` | Server disk and memory; per service: state, restarts, CPU, memory. |
| `kayly releases` | Lists releases with status, commit and error. |
| `kayly releases log <release>` | Full output of one deploy. |
| `kayly rollback <release>` | Makes an earlier release live again with its own image and env. No rebuild. |
| `kayly redeploy` | Releases the live image again with the current env and add-ons. No rebuild. |
| `kayly logs <service> [-f] [-n 100]` | Output of a service of the live release. |
| `kayly restart <service>` | Stops and starts one service of the live release (a short gap). |
| `kayly delete` | Removes the project with its containers, add-ons, volumes and images. Backups stay. Asks for the project name. |
| `kayly source set <git-url> [-branch main]` | Follows a git repository over SSH. Prints the deploy key to add (read-only) and the webhook to create. |
| `kayly source show` / `rm` | Shows or removes the git source. |
| `kayly source deploy [ref]` | Deploys a branch, tag or commit now. |

### Configuration

| Command | What it does |
|---|---|
| `kayly env ls` | Lists env keys. Values are never printed. |
| `kayly env set KEY=value …` | Sets values. They reach services on the next deploy or redeploy. |
| `kayly env rm KEY …` | Removes values. |
| `kayly domains ls` | Lists custom domains. |
| `kayly domains add <hostname> <service>` | Adds a custom domain. Point an A record at the server first. |
| `kayly domains rm <hostname>` | Removes a custom domain. |
| `kayly addons ls` | Lists add-ons. |
| `kayly addons add postgres\|redis` | Adds an add-on. Backed up daily and before every release step. |
| `kayly addons rm <kind>` | Removes an add-on and its data. Asks for the project name. |
| `kayly notify ls` | Shows where alerts go. |
| `kayly notify telegram <chat-id>` | Sends alerts to a Telegram chat. Reads the bot token from stdin. |
| `kayly notify webhook <url>` | Sends alerts to a webhook. |
| `kayly notify off telegram\|webhook` | Stops alerts to one destination. |
| `kayly notify test` | Sends a test alert. |

### Backups

| Command | What it does |
|---|---|
| `kayly backups ls [-remote]` | Lists the project's backups, local or off-site. |
| `kayly backups create` | Takes a backup now. |
| `kayly backups get <name>` | Downloads a backup. |
| `kayly backups restore <name\|latest> [-remote] [-from project]` | Replaces live data with a backup. Takes a fresh backup of what it replaces first. Asks for the project name. |
| `kayly backups setup <endpoint> <bucket> <access-key> [region]` | Configures off-site S3. Reads the secret key from stdin. Prints the recovery key once. |
| `kayly backups status` | Off-site backup state. |
| `kayly backups sync` | Uploads now. |
| `kayly backups drill` | Restore-tests the newest dumps now. |

### Server administration

These run on the server, where the CLI talks to the daemon's root-only admin socket and needs no login.

| Command | What it does |
|---|---|
| `kayly token create <name> [-scope read\|deploy\|admin] [-p project] [-expires-days n]` | Creates an API token and prints it once. Default scope `admin`. |
| `kayly token ls` / `kayly token rm <id>` | Lists or revokes tokens. |
| `kayly audit [-n lines]` | Who changed what, newest first. |
| `sudo kayly upgrade [version]` | Installs a release after checking its signature and checksum. Takes a Kayly backup first. Apps and the proxy keep serving. |
| `kayly restore …` | On a new server, brings back Kayly's state and the newest backups from the bucket. |

Run `kayly --help` for the full usage.

## CI and scripts

Set these instead of running `kayly login`:

| Variable | Value |
|---|---|
| `KAYLY_URL` | Your server, for example `https://kayly.203-0-113-7.sslip.io` |
| `KAYLY_TOKEN` | A token with the `deploy` scope, scoped to one project if you can |

For scripts, rely on exit codes and the [HTTP API](https://github.com/kotlovyim-dev/kayly/blob/main/docs/reference/api.md) for data. Output meant for people (tables and messages) may change between releases.

## Troubleshooting

- **`Kayly publishes no binary for <platform>/<arch>`**: your system isn't in the table above. Build from source: <https://github.com/kotlovyim-dev/kayly>.
- **`GET … 404`** on first run: the release for this package's version is missing its binary. Check the [releases page](https://github.com/kotlovyim-dev/kayly/releases), or reinstall to get the matching version.
- **`is not signed by the release key`** or **`does not match its checksum`**: the download was altered or is incomplete. Retry on a stable network. If it persists, do not run the binary; report it.
- **Corporate proxy or no internet**: the first run needs access to `github.com`. Download the binary by hand (see [Install](#install)) and put it on your `PATH`.

## Links

- Project and docs: <https://github.com/kotlovyim-dev/kayly>
- Deploy guide: <https://github.com/kotlovyim-dev/kayly/blob/main/docs/deploy.md>
- `kayly.toml` reference: <https://github.com/kotlovyim-dev/kayly/blob/main/docs/reference/kayly-toml.md>
- Changelog: <https://github.com/kotlovyim-dev/kayly/blob/main/CHANGELOG.md>

## License

AGPL-3.0-only. See [LICENSE](https://github.com/kotlovyim-dev/kayly/blob/main/LICENSE).
