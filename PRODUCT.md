# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack

UI: React 19, Vite 8, Tailwind v4, TypeScript, built into `web/dist` and embedded in the Go binary with `go:embed`; served by the Kayly daemon behind Caddy at the Kayly host. No external requests at runtime (fonts and assets are bundled), because Kayly runs on servers people own.

## Users

Developers who self-host: they install Kayly from GitHub on a VPS they own and run their own web apps, APIs, workers and bots on it. The first of them is the project's author, running two real apps (a Vite/Node game and a Fastify + Postgres SaaS). They know Git, Docker and a terminal, but do not want to operate Docker, a proxy or certificates by hand.

They use the web UI on a laptop to set up and configure projects. On a phone they check status, read logs and roll back when something breaks.

## Product Purpose

Kayly deploys apps to a server the user owns with the deploy model of a managed platform: a release goes live only after its build, migration step and health checks pass; a failed step leaves the live release serving; rollback is instant and needs no rebuild.

The web UI's main job is setup and configuration: create a project, attach a database (Postgres) or Redis in one click, set environment values, add domains, and see what is live and reachable. Deploys normally come from the CLI (`kayly deploy`); the UI shows their outcome and offers redeploy and rollback.

Success: a self-hoster goes from a fresh install to a configured project with a database without reading documentation, and can tell at a glance whether every project is live.

## Positioning

The managed-platform deploy model (immutable releases, release step that cannot take production down, zero-downtime switch, instant rollback, per-service secrets) on the user's own server, in one Go binary using about 40 MB of RAM, with first-class Telegram bots and Mini Apps planned. Open source (AGPL-3.0).

## Operating Context

- Install: one `curl … | sudo sh` on an Ubuntu or Debian VPS; it prints the Kayly URL and an API token.
- Sign-in: with an API token (`kayly token create <name>` on the server). There are no user accounts in v0.1: one owner.
- Projects are described by `kayly.toml` in the app repository (services, ports, health paths, release command, declared env keys). The UI cannot edit that file; it manages what lives in Kayly: env values, add-ons, domains, releases.
- Every service with a port is reachable at `https://<service>.<project>.<ip>.sslip.io` before any DNS exists; custom domains need an A record.
- Env values are never shown again after they are set; the UI shows keys only. New values and add-ons reach services on the next deploy or a redeploy.
- Deploys, redeploys and rollbacks stream their progress as text.

## Capabilities and Constraints

- API at `/api/v1` (`internal/api/api.go`): projects, project detail, releases, deploy (CLI upload), redeploy, rollback, service logs (streamed text), env, domains, add-ons (`postgres`, `redis`), backups (list, create, download).
- Terms used consistently: project, service, release (`r12`), live, superseded, failed, release step, add-on, redeploy, roll back, domain, env value, backup.
- Removing an add-on deletes its data (backups are kept) and requires typing the project name.
- The UI cannot upload source code or edit `kayly.toml`; it must point to the CLI for those.
- Undecided: multi-user accounts, GitHub-triggered deploys, Telegram bot type, a project settings page.

## Brand Commitments

Name: Kayly, written lowercase as `kayly` in the CLI. Copy is English, plain, sentence case. The author rejects decorative filler: no taglines, no grey subtitles restating a heading; every element shows real data or does real work.

## Evidence on Hand

No logo, screenshots, testimonials or customer names exist. The two real apps (ten00, leryo) are the author's own and must not be presented as customers. Measured facts that may be stated: about 40 MB of RAM for the control plane; zero failed requests across a redeploy in a local test.

## Product Principles

1. The live release is sacred: the UI makes it obvious what is live, and nothing destructive happens without an explicit, specific confirmation.
2. Show the next action, not the system: every state (empty, failed, missing value) says what to do next, often as the exact CLI command.
3. Dense, quiet, fast: one screen per project, no decorative text, data first.
4. Honest about limits: when something is the CLI's job, say so plainly instead of hiding it.

## Accessibility & Inclusion

WCAG 2.2 AA: keyboard operable, visible focus, sufficient contrast in light and dark themes, state never conveyed by colour alone, reduced motion respected.
