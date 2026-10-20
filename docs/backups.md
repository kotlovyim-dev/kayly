# Add-ons and backups

## Add-ons

```sh
kayly addons add postgres     # or redis
```

An add-on runs next to the project's services on the project's private network, with a generated password. Services get `DATABASE_URL` (Postgres superuser) or `REDIS_URL` on the next deploy or `kayly redeploy`. For a least-privilege setup, declare `DATABASE_URL` yourself in `kayly.toml` with a template, and give the superuser URL only to the release step:

```toml
[env]
DB_APP_PASSWORD    = { generate = "hex:32", to = [] }
DATABASE_URL       = { template = "postgres://app:${DB_APP_PASSWORD}@${postgres.host}:5432/myapp" }
DATABASE_ADMIN_URL = { template = "${postgres.url}", to = ["release"] }
```

Add-ons outlive releases: no deploy, rollback or upgrade ever recreates them. The image is pinned when the add-on is created, so upgrading Kayly never upgrades your database. Removing one deletes its data (backups stay) and asks you to type the project name.

## What is backed up, and when

| What | How | When |
|---|---|---|
| Postgres | `pg_dump -Fc` | before every release step, daily, on demand |
| Redis | a point-in-time RDB snapshot | daily, on demand |
| Volumes | a `tar` of the volume | daily, on demand |
| Kayly itself | its database and master key | daily, before every upgrade, on demand (`kayly backups kayly`) |

The last 14 of each kind stay in `/var/lib/kayly/backups/<project>` on the server: `kayly backups ls`, `kayly backups get <name>`, or download them from the UI.

## Off-site, for a lost server

```sh
echo "$S3_SECRET_KEY" | sudo kayly backups setup fsn1.your-objectstorage.com my-bucket <access-key>
```

Any S3-compatible bucket works (Hetzner, Cloudflare R2, Backblaze B2, AWS, MinIO). Every backup is uploaded once written, encrypted on the server with XChaCha20-Poly1305 under a **recovery key** that setup prints once. Keep that key off the server, in a password manager: the bucket alone reveals nothing, and without the key the backups cannot be read. Kayly applies retention itself: everything from the last 7 days, then one a week for 4 weeks.

Every week Kayly restores the newest off-site Postgres dump of every project into a throwaway container to prove it works (`kayly backups drill` runs it now). A failed backup, upload or drill sends an alert.

## Restore

```sh
kayly backups restore latest              # the newest local backup of each kind
kayly backups restore <name> -remote      # one from the bucket
kayly backups restore latest -from leryo -p leryo-staging   # rehearse in a throwaway project
```

A restore asks for the project name, takes a fresh backup of what it is about to replace, and runs under the project's lock (no deploy at the same time). Postgres is restored in one transaction; a volume is restored with the services that mount it stopped.

## A lost server

On a new server:

```sh
curl -fsSL https://raw.githubusercontent.com/kotlovyim-dev/kayly/main/install.sh | sudo sh
echo "$S3_SECRET_KEY" | sudo kayly restore -endpoint … -bucket … -access-key … -recovery-key …
```

`kayly restore` stops the daemon, sets the fresh install's empty state aside (in `/var/lib/kayly/pre-restore-<time>`), brings back Kayly's database and key, downloads the newest backup of every project, and starts the daemon again. It refuses a server whose state holds any project: it brings back a lost server, it never replaces a working one. The token the installer printed belonged to the empty state; the restored database brings back your old tokens.

Then deploy each app again (images are not backed up) and `kayly backups restore latest -p <project>` for each project with data. Kayly's own backup is daily, so you lose at most the configuration changes of the last day; app data is as fresh as its newest backup.
