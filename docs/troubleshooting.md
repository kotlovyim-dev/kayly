# Troubleshooting

Start with the release: `kayly releases` shows each release's status and the first line of its error, and `kayly releases log r13` the deploy's whole output, with every command printed so you can run it by hand. The UI shows the same under the release.

## A deploy failed

| The error says | Do |
|---|---|
| `X has no value` | `kayly env set X=…`, then deploy again. Values are checked before the build, so this costs a second. |
| `kayly.toml:12:3: …` / `unknown key` | Fix the line it names. Unknown keys are errors on purpose. See the [reference](reference/kayly-toml.md). |
| the build failed | Rerun the printed `docker buildx build` or `railpack` command on the server to iterate. For Railpack, check the language version your project declares. |
| `release step exited with N` | Your `[release] command` failed; its output is in the log. The live release kept serving. Postgres was backed up just before, as `<time>-before-r<n>.dump`. |
| `health check failed: GET /x returned 503` | The new release did not become healthy in 2 minutes. Read `kayly releases log` for the container's output; check that the service listens on `0.0.0.0`, not `127.0.0.1`, and on the declared port. |
| `bot: the token in BOT_TOKEN does not work` | The bot token is wrong or revoked. Set a new one with `kayly env set`. |

In every case, nothing live changed.

## The app is down

1. `kayly status`: is the service `running`, `restarting` or `exited`, and how many restarts? A restart loop sends an alert after 3 restarts in 10 minutes.
2. `kayly logs <service> -n 200`: what did it print before it died? `memory` too low shows as an exit with code 137.
3. Roll back while you look: `kayly rollback r<previous>` (seconds, no build).
4. If the service runs but the URL does not answer, check `kayly domains ls` and the DNS A record; a certificate is issued only once DNS points at the server.

## Kayly itself

- `journalctl -u kayly -f` is the daemon's log (JSON, one line per event, with `project` and `release`).
- The daemon can be restarted at any time: `sudo systemctl restart kayly`. Apps and the proxy keep serving; an interrupted deploy is marked failed and cleaned up.
- `docker ps --filter name=kayly-proxy` should show the proxy running. If it is missing, the daemon recreates it within 30 seconds.
- Disk full? `kayly status` shows it, and an alert fires above 85 %. Kayly keeps three images per project and trims the build cache to 10 GB daily; old backups beyond 14 per kind are deleted.

## Signing in

- `401 missing, invalid or expired token`: create a token on the server with `sudo kayly token create <name>`; check `kayly token ls` for its expiry.
- `429 too many invalid tokens from this address`: wait the number of seconds given (one attempt a minute after 10 failures) and use a valid token.
- `403 … needs deploy` or `… limited to project x`: the token's scope or project binding refuses this. Create a token with the scope you need.
