# Security model

What Kayly protects, how, and what it does not.

## Who can do what

- **Root on the server can do everything.** The CLI on the server talks to a root-only socket without a token; the master key and every backup are on the disk. Protect SSH access to the server as you would the apps themselves.
- **Everyone else needs a token.** Tokens are 256-bit random strings, stored hashed. Each has a scope (`read` < `deploy` < `admin`), optionally one project and an expiry. A project-bound token sees and touches only its project. Give CI and agents a `deploy` token bound to one project, with an expiry.
- **Every change is audited**, whichever door it came through (CLI, UI, API, MCP), with the token's name: `sudo kayly audit`.
- **Guessing is slow.** An address that presents 10 unknown tokens in a row waits a minute per further attempt (sign-in, API and MCP alike).
- **The browser** holds the token in an HttpOnly, Secure, SameSite=Strict cookie; every write also needs a custom header, which a cross-site form cannot send.

## Secrets

- Env values and add-on passwords are encrypted in the database with XChaCha20-Poly1305. The key is `/var/lib/kayly/master.key` (mode 0600), outside the database, so a copy of `kayly.db` alone reveals nothing.
- Nothing reads a value back: not the UI, the API, the CLI or MCP. Add-on connection URLs carry the superuser password and are shown only to `admin` tokens, never over MCP.
- A value reaches only the services its `to` lists, and only keys declared in `kayly.toml` are delivered at all; there is no shared `.env`.
- Off-site backups are encrypted on the server before upload, under a recovery key that is printed once and never stored in the bucket.

## Containers

- Every service runs with all Linux capabilities dropped, `no-new-privileges`, and optional read-only root filesystem and memory/CPU caps.
- Add-ons live on the project's private network and are not reachable from other projects or the internet.
- **Limit:** every service with a port shares one network with the proxy, so services of different projects can reach each other by name. That is fine while one owner runs every project; do not host mutually untrusted apps on one Kayly yet.

## Releases of Kayly

- `install.sh` and `kayly upgrade` install a Kayly binary only when `checksums.txt` carries a valid ed25519 signature by the release key and the binary matches its checksum. The public key is in the installer and in the binary (`internal/upgrade/release.pub`); the private key is a secret of the release workflow only.
- Railpack, Caddy, Postgres and Redis images are pinned by version and digest.

## Reporting a vulnerability

Please report it privately through GitHub's security advisories on the repository, not in a public issue.
