# Install

## What you need

- A server you can reach as root: Ubuntu 22.04+ or Debian 12+, amd64 or arm64. 1 GB of RAM runs Kayly and a small app; give builds 2 GB if you can.
- Ports 80 and 443 open to the internet (UDP 443 too, for HTTP/3).
- Nothing else: the installer brings Docker (if missing), Railpack and Kayly.

## Run the installer

```sh
curl -fsSL https://raw.githubusercontent.com/kotlovyim-dev/kayly/main/install.sh | sudo sh
```

It:

1. installs Docker with the containerd image store, git and the OpenSSH client if they are missing;
2. installs Railpack, pinned and checked by its SHA-256;
3. downloads the newest Kayly release and installs it only if `checksums.txt` carries a valid signature by the release key and the binary matches its checksum (it uses `openssl`, installed if missing);
4. writes and starts the `kayly` systemd unit;
5. prints a login command with a fresh API token.

Every app gets `https://<service>.<project>.<ip>.sslip.io` at once, with a real certificate, before you set up any DNS. The UI and API are at `https://kayly.<ip>.sslip.io`.

Options, as environment variables before `sh`:

| Variable | Default | Use |
|---|---|---|
| `KAYLY_VERSION` | latest | Install a given release, e.g. `v1.0.0`. |
| `KAYLY_DOMAIN` | `kayly.<base domain>` | Serve the UI and API on your own hostname (point its A record first). |
| `KAYLY_EMAIL` | none | Contact address for Let's Encrypt expiry notices. |
| `KAYLY_BASE_DOMAIN` | `<ip>.sslip.io` | Where services get their addresses. `localhost` serves everything with Caddy's local certificates, for a test machine. |
| `KAYLY_BINARY` | none | Install this binary instead of downloading one (not signature-checked). |

## Sign in

On your laptop, install the same binary (from the [releases page](https://github.com/kotlovyim-dev/kayly/releases)) and run the command the installer printed:

```sh
kayly login https://kayly.203-0-113-7.sslip.io kayly_…
```

The web UI takes the same token. More tokens, with less power, for CI and agents:

```sh
sudo kayly token create github-actions -scope deploy -p my-app
```

## Alerts and off-site backups

Do these two now, before the first app: a server you do not hear from is a server you cannot trust.

```sh
echo "$BOT_TOKEN" | sudo kayly notify telegram <chat-id>
sudo kayly notify test
echo "$S3_SECRET" | sudo kayly backups setup <endpoint> <bucket> <access-key>
```

Store the recovery key that `backups setup` prints somewhere off the server. See [Backups](backups.md).

## Upgrade

```sh
sudo kayly upgrade            # or: sudo kayly upgrade v1.1.0
```

It checks the release's signature and checksum, snapshots Kayly's database and key, swaps the binary (keeping the old one as `/usr/local/bin/kayly.previous`) and restarts the daemon. Deploys in progress finish first. Apps and the proxy are separate containers and keep serving: an upgrade drops no request.

Kayly's database migrates only forward. To go back, put the previous binary back and restore the snapshot the upgrade took (the upgrade prints its name; it holds `kayly.db` and `master.key`):

```sh
sudo systemctl stop kayly
sudo mv /usr/local/bin/kayly.previous /usr/local/bin/kayly
sudo rm -f /var/lib/kayly/kayly.db-wal /var/lib/kayly/kayly.db-shm
sudo tar -xf /var/lib/kayly/backups/_kayly/<snapshot>.kayly.tar -C /var/lib/kayly
sudo systemctl start kayly
```

Apps keep serving while the daemon is stopped.

## Uninstall

`kayly delete -p <project>` for each project removes its containers, add-ons and volumes (backups stay). Then `sudo systemctl disable --now kayly`, `docker rm -f kayly-proxy`, and remove `/usr/local/bin/kayly` and `/var/lib/kayly`.
