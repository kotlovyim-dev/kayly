# Compatibility

From v1.0, Kayly's contracts follow [semantic versioning](https://semver.org). Within 1.x:

| Contract | Promise |
|---|---|
| [`kayly.toml`](reference/kayly-toml.md) | A file valid for 1.x stays valid and means the same. New keys may be added. |
| [HTTP API `/api/v1`](reference/api.md) | Routes, fields and their meaning stay. New routes and new response fields may be added: ignore fields you do not know. A breaking change ships as `/api/v2`, next to v1 for at least one minor release. |
| [CLI](reference/cli.md) | Commands, flags and exit codes keep working. Output written for people may change; scripts should use exit codes and the API. |
| [MCP tools](reference/mcp.md) | Tool names, arguments and the meaning of results stay. New tools and optional arguments may be added. |

Not covered: the web UI's look and layout, log line formats, the internal database schema, and anything under `internal/` in the source.

## Upgrades and downgrades

- `kayly upgrade` moves between any two 1.x releases, newest included, without stopping apps.
- Kayly's database migrates only forward. A downgrade means restoring the Kayly backup the upgrade took; see [Install](install.md#upgrade).
- Add-on images (Postgres, Redis) are pinned per add-on when it is created: no Kayly upgrade changes a database's major version under you.
