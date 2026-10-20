# MCP reference

The daemon serves the [Model Context Protocol](https://modelcontextprotocol.io) at `https://<kayly host>/mcp` over stateless Streamable HTTP, so a coding agent can deploy, read logs and roll back within the limits of its token.

```sh
# on the server: a token that can deploy one project and nothing else, for 30 days
sudo kayly token create claude -scope deploy -p leryo -expires-days 30

# on the laptop
claude mcp add --transport http kayly https://kayly.<host>/mcp --header "Authorization: Bearer <token>"
```

Cursor, Codex and other clients that take a URL and a header work the same way. OAuth, which hosted chat clients need for remote connectors, comes after 1.0.

## Tools

A token sees only the tools its scope allows, and every call is checked again against the project it names. Calls are audited as `mcp <tool>`.

| Scope | Tool | What it does |
|---|---|---|
| read | `list_projects` | Projects with their live release. |
| read | `get_project` | Live release, services (state, restarts, URLs), add-ons, domains, recent releases, and env keys the live release needs but lacks. |
| read | `list_releases` | Newest releases with status, error and commit. |
| read | `get_release` | One release: status, error and the end of its deploy log. |
| read | `get_logs` | Recent output of a service (service, tail, grep). |
| read | `list_env` | Env keys: whether each has a value, whether `kayly.toml` declares it, which services get it. Never values. |
| read | `list_domains` | Custom domains and their services. |
| read | `list_backups` | Backups, local or off-site. |
| read | `server_status` | Disk, memory and versions. |
| read | `validate_config` | Checks the text of a `kayly.toml`; returns the first error with its line. |
| deploy | `deploy` | Deploys a git ref of the project's source. Returns the release number at once; `wait` blocks up to 60 s for the outcome. |
| deploy | `redeploy` | The live image again with current env and add-ons. |
| deploy | `rollback` | An earlier release, live again. |
| deploy | `restart_service` | Stops and starts one service. |
| deploy | `set_env` | Sets a value; nothing reads it back. |
| deploy | `delete_env` | Deletes a value. Destructive: needs `confirm` = the project name. |
| deploy | `add_domain` / `remove_domain` | Custom domains; removal needs `confirm`. |
| deploy | `create_backup` | Backs up add-ons and volumes now. |
| admin | `add_addon` | Attaches Postgres or Redis. |
| admin | `remove_addon` | Deletes an add-on and its data; needs `confirm`. |
| admin | `restore_backup` | Replaces live data with a backup; needs `confirm`. |
| admin | `delete_project` | Deletes a project; needs `confirm`. |

No tool returns an env value or a credential; add-on URLs, which carry the superuser password, are left out of every answer. Output is capped (logs at 32 KB). Deploying an uncommitted working tree stays the CLI's job: the agent runs `kayly deploy` like any command.

## Compatibility

From v1.0, tool names, their arguments and the meaning of their results follow semantic versioning. New tools and new optional arguments may appear in a minor release.
