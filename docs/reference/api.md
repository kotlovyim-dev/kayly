# HTTP API reference (`/api/v1`)

The daemon serves the API on two listeners: behind the proxy at `https://<kayly host>/api/v1` (a token is required), and on the root-only admin socket `/run/kayly/admin.sock` (no token; the token routes exist only there).

## Authentication

- **Bearer token:** `Authorization: Bearer kayly_…`. Create tokens on the server with `kayly token create`.
- **Session cookie (the web UI):** `POST /api/v1/session` with `{"token": "…"}` sets an HttpOnly, Secure, SameSite=Strict cookie. Every cookie-authenticated request other than GET or HEAD must also carry `X-Kayly-Request: 1`. `DELETE /api/v1/session` signs out.
- **Scopes:** `read` < `deploy` < `admin`. A token may be bound to one project; it then sees and touches only that project.
- **Rate limit:** an address that presents 10 unknown tokens in a row (sign-in, bearer or MCP) gets `429` with `Retry-After` and may try once a minute after that.

Every write is recorded in the audit log with the token's name.

## Errors

Errors have one shape. `hint` says what to do next, when Kayly knows:

```json
{ "error": { "code": "forbidden", "message": "this token has the read scope; this needs deploy", "hint": "create a token with a wider scope: kayly token create <name> -scope deploy" } }
```

## Long operations

Deploys, redeploys, rollbacks and restores answer `200` with an NDJSON stream: `{"log": "…"}` lines, then one `{"result": {"ok": true, "release": 12}}` or `{"result": {"ok": false, "release": 13, "error": "…"}}`. The operation runs to the end even if the client goes away; read the outcome from the releases list.

## Routes

| Method and path | Scope | Body / query | Answer |
|---|---|---|---|
| `GET /projects` | read | | `[{name, live_release?, live_since?, created_at}]` |
| `POST /projects` | admin | `{name}` | `201` the project; `409` if it exists |
| `GET /projects/{p}` | read | | the project with `services`, `addons`, `releases`, `domains`, `env_keys` |
| `DELETE /projects/{p}` | admin | `?confirm=<p>` | `204` |
| `GET /projects/{p}/services` | read | | services of the live release with `state`, `restarts`, `stats {cpu_percent, memory_bytes, memory_limit}` |
| `POST /projects/{p}/services/{s}/restart` | deploy | | `204` |
| `GET /projects/{p}/releases` | read | | `[{seq, status, error?, created_at, commit?, commit_message?, can_roll_back?}]`, newest first. `status` is `building`, `releasing`, `starting`, `live`, `failed` or `superseded`. |
| `GET /projects/{p}/releases/{seq}/log` | read | | the deploy's output, text |
| `POST /projects/{p}/deploys` | deploy | gzip tarball of the source; optional headers `X-Kayly-Commit`, `X-Kayly-Commit-Message` (URL-escaped) | stream |
| `POST /projects/{p}/deploys/git` | deploy | `{ref?}` | stream |
| `POST /projects/{p}/redeploy` | deploy | | stream |
| `POST /projects/{p}/rollback` | deploy | `{release}` | stream |
| `GET /projects/{p}/logs` | read | `?service=&tail=100&follow=1` | text, streamed while following |
| `GET /projects/{p}/env` | read | | `{keys: [...]}`; values are never returned |
| `PUT /projects/{p}/env/{KEY}` | deploy | `{value}` | `204` |
| `DELETE /projects/{p}/env/{KEY}` | deploy | | `204` |
| `GET /projects/{p}/domains` | read | | `[{hostname, service}]` |
| `POST /projects/{p}/domains` | deploy | `{hostname, service}` | `201` |
| `DELETE /projects/{p}/domains/{hostname}` | deploy | | `204` |
| `GET /projects/{p}/addons` | read | | `[{kind, image, env_var, url, status, created_at}]`; `url` is left out below `admin` |
| `POST /projects/{p}/addons` | admin | `{kind: "postgres" \| "redis"}` | the add-ons |
| `DELETE /projects/{p}/addons/{kind}` | admin | | `204` |
| `GET /projects/{p}/backups` | read | `?remote=1` for the bucket | `[{name, size, created_at}]` |
| `POST /projects/{p}/backups` | deploy | | the backups taken |
| `GET /projects/{p}/backups/{name}` | admin | | the file |
| `POST /projects/{p}/backups/restore` | admin | `{name, remote?, from_project?, confirm: <p>}` | stream |
| `GET` / `PUT` / `DELETE /projects/{p}/source` | read / admin / admin | `{url, branch}` | `{url, branch, public_key, webhook_url, webhook_secret?}`; the secret only when created |
| `POST /hooks/github/{p}` | HMAC | GitHub `push` payload with `X-Hub-Signature-256` | `202` |
| `GET /server` | read | | `{version, disk_used, disk_total, memory_total, memory_available, docker_version}` |
| `GET` / `PUT /notifications`, `POST /notifications/test` | admin | `{telegram_token?, telegram_chat?, webhook_url?}` | `{telegram, telegram_chat?, webhook}`; secrets are never echoed |
| `GET` / `PUT /offsite`, `POST /offsite/sync`, `POST /offsite/drill` | admin | `{endpoint, bucket, access_key, secret_key, region?}` | status; `PUT` answers the recovery key once |
| `POST /backups/kayly` | admin | | the snapshot of Kayly's database and key |
| `GET /audit` | admin | `?limit=100` (1–1000) | `[{at, token, action, project?}]` |
| `GET` / `POST /tokens`, `DELETE /tokens/{id}` | admin socket only | `{name, scope?, project?, expires_days?}` | the token, shown once on creation |

## Compatibility

From v1.0, `/api/v1` follows semantic versioning: routes, fields and their meaning stay; new routes and new response fields may be added, so clients should ignore fields they do not know. A breaking change gets `/api/v2`, served next to v1 for at least one minor release.
