---
name: kayly
description: Self-hosted deploy platform; the web UI is a framed sheet in the Linear, Vercel and Notion family, built with shadcn/ui.
colors:
  frame: "#f6f9f9"
  sheet: "#ffffff"
  raised: "#f4f6f8"
  active: "#ebedee"
  border: "#e8eaec"
  rule: "#f0f1f2"
  control: "#cdced1"
  text: "#131416"
  muted: "#5d5f60"
  primary: "#131416"
  data: "#1b6c80"
  positive: "#1e7b4f"
  negative: "#c4242a"
  warning: "#92400e"
  pill-positive: "#dcfae5 / #04250e"
  pill-negative: "#ffedea / #831c1e"
  pill-warning: "#fffbeb / #92400e, edge #fde68a"
  pill-neutral: "#ebedee / #353736"
dark:
  frame: "#090a0b"
  sheet: "#111213"
  raised: "#1a1c1d"
  active: "#25282a"
  border: "#232527"
  text: "#f2f3f3"
  muted: "#a9adb0"
  primary: "#f2f3f3"
  data: "#4fb3c9"
typography:
  sans: "IBM Plex Sans Variable, 400/500/600"
  mono: "IBM Plex Mono 400/500, for release numbers, SHAs, env keys, hostnames and commands only"
  sizes: "small 12/16 · body 14/20 · section 16/24 · title 22/28 · figure 28/36"
rounded:
  chip: "4px"
  control: "6px"
  card: "8px"
  sheet: "16px"
spacing:
  base: "4px"
  gap: "24px"
  pad: "24px (16px on a phone)"
  control: "32px; 36px for form inputs"
  row: "48px"
  sidebar: "228px"
  top-bar: "48px"
---

# Design system: kayly

## Overview

**The framed sheet.** A tinted L-shaped frame holds the sidebar and the top bar with no line between them; the work sits on one white sheet inset 8px from the window's right and bottom edges, with a 1px edge, 16px corners and no shadow. It is the structure of Linear and Vercel, with Notion's quiet type: one grotesk, sentence case, grey for what is secondary.

Colour goes on data and state, not on chrome. The chrome is grey; the one primary action is near-black; the data hue (a deep teal) marks the release that is live; green, amber and red mark live, in progress and failed. Under 3% of the pixels on any screen carry a hue.

The components are [shadcn/ui](https://ui.shadcn.com) (Radix primitives, `class-variance-authority`), copied into `web/src/components/ui/` and themed through the tokens in `web/src/styles.css`. The art direction comes from the `design-taste-frontend` skill's app run (style `framed-sheet`), checked with its inspector at 1440, 768 and 390px.

**Key characteristics**
- One frame tint, one white sheet, 1px edges, no shadow on anything at rest.
- Each screen answers one question first, in the line under its title ("r13 failed 38 minutes ago: … r12 kept serving.").
- State reads from shape as well as colour: dots are filled, ringed, struck or hollow, and every pill carries its word.
- Anything a developer types or copies is in IBM Plex Mono; everything else is IBM Plex Sans.
- Standby-then-go before anything that changes what is live or deletes a value.

## Colors

- **Frame** (`frame`): the sidebar and top bar, one low-chroma step of the data teal. The phone's top bar keeps it.
- **Sheet** (`sheet`): the page. Cards on it are the sheet's white with a `border` edge; settings panels are `raised`.
- **Lines in three steps:** `border` for card edges, `rule` (lighter) between rows, `control` (darker) on inputs and outlined buttons.
- **Text in two greys:** `text`, then `muted` for labels, sublines and timestamps; both reach 4.5:1 on the sheet and the frame.
- **Primary** (`primary`): near-black fill, white label. One per screen: Redeploy, or "Why r13 failed" when the newest release failed.
- **Data** (`data`): the live step of the release track and the mark. Nothing else.
- **Status pairs:** pale tint with dark ink of the same hue, for pills; the full `positive`, `negative` and `warning` for dots and single words.

Dark mode follows the system, or the owner's choice in the account menu (remembered in the browser). It uses true neutral darks, not navy; the data hue lifts to `#4fb3c9`.

**Rules.** No gradients, glows or glass. No violet or indigo. No colour on chrome: the active nav item is a grey pill, the active tab a 2px ink underline. Status colours mean status and nothing else.

## Typography

IBM Plex Sans for everything, IBM Plex Mono for one job (release numbers, SHAs, env keys, hostnames, commands, logs). Five sizes: small 12, body 14, section 16, title 22, figure 28. Weights 400, 500 and 600; 600 for titles and the active label. Sentence case everywhere; no tracked capitals. Tabular figures on every number that is compared or updates.

## Layout

- **Desktop (≥1024px):** a 228px sidebar and a 48px top bar on the frame; the sheet fills the rest, inset 8px right and bottom. Inside the sheet: 24px padding, the title row, the underline tabs on a full-width rule, then the content.
- **Grid:** one 24px gap, horizontally and vertically. The overview's second row is three columns: releases span two, services and add-ons take one (at 1440px: 1440 − 228 sidebar − 8 inset − 2 × 24 padding = 1156 = 3 × 369.3 + 2 × 24).
- **Settings:** each section is a row: title and description on the left (18rem), a `raised` panel with 16px corners on the right; sections split by a full-width rule.
- **Phone:** the frame disappears; the sheet is the screen, under a 48px bar with the menu button (the sidebar opens as a sheet), the page name and search. Tables become two-line rows; the live card's facts become label-value rows; title actions stretch to full width.

## Components

- **Button** (shadcn): `default` (near-black primary), `outline`, `ghost`, `destructive`; 32px, `sm` 28px, `lg` 40px; 6px corners. Busy shows a spinner and blocks presses.
- **Standby-then-go** (`ArmButton`): the first press arms it (amber tint, blinking dot, the label rewritten to the exact outcome: "Go: r11 live"); the second press within four seconds runs it; Escape, blur or the timeout stands it down. Used for redeploy, rollback, deleting an env value and removing a domain. Removing an add-on deletes data and asks for the typed project name instead.
- **Status dot and pill** (`StatusDot`, `StatusBadge`): live filled, in progress a blinking ring, failed struck through, previous hollow, off a faint dot. Pills are 20px, pale tint and dark ink.
- **Release track** (signature): one 20px step per release, oldest to newest; the live one filled in the data hue, failed ones struck in red, previous ones hollow (darker while their image is kept for rollback). It sits in the live-release card; a step opens that release. When a release goes live its step lands (scale 0.4 → 1.2 → 1 over 420ms); this and the blink stop under reduced motion.
- **Panel:** a card with a 48px header (title, optional count, optional action) over its rows. Rows are 48px with `rule` dividers, no zebra, no header band; numbers right-aligned. A panel keeps its own height.
- **Release sheet:** a side sheet (shadcn `Sheet`) with one release: created, commit, the failure reason on the negative tint, rollback, and its deploy log.
- **Command palette** (shadcn `Command` on cmdk): ⌘K / Ctrl K from anywhere: projects, the current project's tabs, and "Create project" for a valid new name.
- **Command line:** a `raised` box with one mono command and a copy button, wherever the next step is the CLI's job.

## Do and don't

- **Do** answer the screen's question in the line under its title, and put its main job one click away.
- **Do** carry state in shape and words as well as colour.
- **Do** say plainly when something is the CLI's job, with the exact command.
- **Don't** put a shadow on anything at rest; shadows are for dialogs, sheets and menus.
- **Don't** colour chrome, tabs or nav; don't use the data hue for anything but the live release.
- **Don't** add icons in tinted squares, sparklines, KPI tiles or a hero header.
- **Don't** stretch a card over empty space; let it keep its height and pair blocks instead.
