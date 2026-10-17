---
version: 1
slug: "web-src-app-tsx"
primary_target: "web/src/App.tsx"
related_targets: []
---

# Surface: Kayly console (web UI)

Scope: the whole web UI served by the daemon: sign in, projects list, project page. Visitor mode: Operate.

Audience and job: open-source self-hosters on a laptop setting up and configuring projects (create a project, attach Postgres or Redis, set env values, add domains) and seeing what is live; on a phone, checking status, reading logs and rolling back. Deploys come from the CLI; the UI shows their outcome and offers redeploy and rollback.

Constraints: kayly.toml and source upload are the CLI's job; the UI says so with the exact command. Env values are write-only. Removing an add-on requires typing the project name. No external requests (fonts bundled). WCAG 2.2 AA, light and dark.

## Direction contract

THESIS: Every release is a cue in a stage manager's prompt book: it stands by until it proves itself, then goes, and the previous one bows out. Refuses the category default of a dark sidebar dashboard with a grid of project cards.

OWN-WORLD: Running-light blue prompt-desk bar (#1B2A5E) over a cool rehearsal-floor grey ground with white sheets for work areas; cue lamps in standby amber and go green, failed red, each state also a distinct lamp shape (filled live, ringed standby, struck failed, hollow superseded); every project marked by a short spike-tape strip in a fluorescent tape colour derived from its name. One workhorse family in two widths (sans and mono), four sizes.

STORY: The visitor sees which projects are live, opens one, attaches a database in one click, sets the values Kayly asks for, and redeploys; when something fails they read the reason on the cue sheet and roll back with a standby-then-go confirmation.

FIRST VIEWPORT: Blue prompt-desk bar: kayly wordmark left, project switcher, sign out right. Project row: spike strip and name, the live release number as the largest type on the page beside a go lamp, service URLs, Redeploy button right. Work area: releases cue sheet (lamp column, release, status, when, reason, Roll back) leading the left two thirds with only the live row reversed; service logs below it with a service tab row. Right third: add-ons (Postgres and Redis with Add), env keys, domains, backups, as quiet sections.

FORM: Stage manager's prompt book and cue light, my grounded candidate 4 of 7, seed key 86383e99. Signature interaction: the standby-then-go control for roll back and redeploy (first press arms and lights the row's lamp amber, second press within a few seconds goes; Escape or timeout stands down), and on switch the go lamp hands over from the old row to the new one.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
