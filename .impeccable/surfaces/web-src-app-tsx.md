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

Owner decisions (2026-10-17): the window never scrolls; every view fits the viewport and only long content (release history, logs, backups) scrolls inside its own pane. A project has three tabs: Overview, Logs, Settings (add-ons, env, domains, backups on one fitted screen). Projects are switched from a top-bar switcher and a ⌘K palette; no sidebar rail. Minimal and modern.

## Direction contract

THESIS: Kayly as a small, exact instrument: one black display, colour keys, every release a step in the sequence. Refuses the dark-neutral Vercel clone and the sidebar-plus-card-grid dashboard.

OWN-WORLD: Anodized-aluminium light ground (near-white warm grey), a near-black display band that carries live state, colour keys with fixed jobs: orange is the one go action, blue is selection and focus, green is live, red struck is failed. Square-ish 4px-grid geometry, hairline rules, no shadows. A tight grotesk for UI and a technical mono for anything typed or copied, four sizes. State as marks: filled, ringed, struck, hollow, never colour alone.

STORY: The visitor sees at a glance which release is live, steps through history on the display, rolls back with an arm-then-go press, reads logs in a full-height pane, and configures add-ons, env and domains on one fitted Settings screen.

FIRST VIEWPORT: Top bar: kayly mark, project switcher (⌘K), sign out. Black display band: project name, live release number at the largest size, service URL, the release step row (one cell per release, live lit green), Redeploy as the orange key right. Tabs Overview · Logs · Settings under the band; Overview shows the selected step's detail and the release list in a pane that fills the rest of the window.

FORM: Field Kit (pocket synth and field-recorder product language), my grounded candidate 3 of 7, seed key 9de862db. Signature move: the release step row on the display; selecting a step shows its detail, and on switch the live light moves to the new step. Raises: fixed capacity (j-card), one geometry (transit), strict four-size scale (bitmap specimen), state as marks (centre rail), detail only on focus (title wall).

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
