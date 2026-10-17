---
name: kayly
description: Self-hosted deploy platform; the web UI is a stage manager's prompt book for releases.
colors:
  desk: "#1b2a5e"
  desk-ink: "#e8ecfa"
  desk-ink-2: "#aab5dc"
  floor: "#eceef2"
  sheet: "#ffffff"
  ink: "#141b2e"
  ink-2: "#4c5670"
  rule: "#d5d9e2"
  hover: "#f3f5f9"
  plate: "#1b2a5e"
  plate-ink: "#ffffff"
  plate-ink-2: "#c3cbea"
  action: "#1b2a5e"
  action-hover: "#26397c"
  action-ink: "#ffffff"
  go: "#2e9e5b"
  go-ink: "#1a6b3d"
  standby: "#e8a317"
  standby-ink: "#855700"
  fail: "#d2402f"
  fail-ink: "#b42318"
  focus: "#3d63dd"
  code: "#f4f6fa"
  tape-pink: "#ff3d8b"
  tape-yellow: "#f5d800"
  tape-green: "#38c96a"
  tape-blue: "#2f7bff"
  tape-orange: "#ff7a1a"
  tape-violet: "#9b5cff"
  tape-cyan: "#00b8c4"
typography:
  cue:
    fontFamily: "Atkinson Hyperlegible Mono Variable, ui-monospace, monospace"
    fontSize: "2rem"
    fontWeight: 700
    lineHeight: "2rem"
    letterSpacing: "-0.025em"
  title:
    fontFamily: "Atkinson Hyperlegible Next Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1.1875rem"
    fontWeight: 700
    lineHeight: "1.5rem"
  body:
    fontFamily: "Atkinson Hyperlegible Next Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.9375rem"
    fontWeight: 400
    lineHeight: "1.375rem"
  meta:
    fontFamily: "Atkinson Hyperlegible Next Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.8125rem"
    fontWeight: 400
    lineHeight: "1.125rem"
  mono:
    fontFamily: "Atkinson Hyperlegible Mono Variable, ui-monospace, monospace"
    fontSize: "0.8125rem"
    fontWeight: 400
    lineHeight: "1.125rem"
rounded:
  tape: "1px"
  sm: "4px"
  md: "6px"
  lg: "8px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "12px"
  lg: "16px"
  xl: "20px"
components:
  desk-bar:
    backgroundColor: "{colors.desk}"
    textColor: "{colors.desk-ink}"
    height: "48px"
    padding: "0 24px"
  button-primary:
    backgroundColor: "{colors.action}"
    textColor: "{colors.action-ink}"
    typography: "{typography.body}"
    rounded: "{rounded.md}"
    padding: "0 14px"
    height: "36px"
  button-primary-hover:
    backgroundColor: "{colors.action-hover}"
  button-quiet:
    backgroundColor: "{colors.sheet}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    rounded: "{rounded.md}"
    padding: "0 14px"
    height: "36px"
  button-quiet-hover:
    backgroundColor: "{colors.hover}"
  button-danger:
    backgroundColor: "{colors.sheet}"
    textColor: "{colors.fail-ink}"
    rounded: "{rounded.md}"
    padding: "0 14px"
    height: "36px"
  button-small:
    typography: "{typography.meta}"
    rounded: "{rounded.md}"
    padding: "0 10px"
    height: "32px"
  standby-go-armed:
    backgroundColor: "{colors.standby}"
    textColor: "{colors.ink}"
    rounded: "{rounded.md}"
    height: "36px"
  icon-button:
    textColor: "{colors.ink-2}"
    rounded: "{rounded.md}"
    size: "32px"
  input:
    backgroundColor: "{colors.sheet}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    rounded: "{rounded.md}"
    padding: "0 12px"
    height: "36px"
  sheet:
    backgroundColor: "{colors.sheet}"
    rounded: "{rounded.lg}"
    padding: "8px 16px"
  release-row-live:
    backgroundColor: "{colors.plate}"
    textColor: "{colors.plate-ink}"
    padding: "10px 12px"
  command:
    backgroundColor: "{colors.code}"
    textColor: "{colors.ink}"
    typography: "{typography.mono}"
    rounded: "{rounded.md}"
    padding: "4px 4px 4px 12px"
  spike-strip:
    rounded: "{rounded.tape}"
    width: "22px"
    height: "7px"
---

# Design System: kayly

## Overview

**Creative North Star: "The Prompt Book"**

Every release is a cue in a stage manager's prompt book: it stands by until it proves itself, then goes, and the previous one bows out. The interface is a running-light blue prompt-desk bar over a cool rehearsal-floor grey. The work sits on white sheets, cue lamps report state, and a short strip of spike tape marks each project.

The system is dense, quiet and data-first. One workhorse family in two widths (Atkinson Hyperlegible Next and Mono) carries everything at four sizes. Colour is scarce. Blue belongs to the desk, the primary action and the single reversed plate. Green, amber and red belong to the lamps and their ink. The fluorescent tape colours appear only on spike strips. Depth comes from rules and the sheet-on-floor contrast, never from shadows.

Motion is limited to one authored moment: the go lamp landing on its new row when a release hands over. Everything else is a 150 ms colour transition.

**Key Characteristics:**
- A blue desk bar on top, a grey floor below, white sheets for primary work, and ruled quiet sections for supporting work.
- Cue lamps whose state reads from shape as well as colour.
- Spike-tape strips derived from the project name.
- Four type sizes, two widths of one family.
- A two-press standby-then-go call before any action that changes what is live or deletes configuration.
- Flat, ruled, 1px borders; no shadows.

## Colors

The palette is cool and restrained. Running-light blue does the structural work, signal colours are reserved for state, and fluorescent tape is reserved for identity.

### Primary
- **Running-Light Blue** (desk / plate / action): the prompt-desk bar, the primary button, the live release plate, and the selected log tab. In dark mode the desk keeps the same blue. The plate and action step up to a brighter blue (#2a3f8f / #3a56c4) so they stay visible against a neutral ground.
- **Desk Ink** (desk-ink, desk-ink-2): the wordmark and project switcher on the desk; the secondary tone is for sign out and other quiet desk controls.
- **Focus Blue** (focus): focus outline, caret and selection tint. It is used for nothing else.

### Secondary
- **Go Green** (go, go-ink): the live lamp fill; the ink tone is for success text such as the "copied" check.
- **Standby Amber** (standby, standby-ink): the standby ring lamp and the armed standby-then-go control (15% tint fill, amber border).
- **Fail Red** (fail, fail-ink): the struck failed lamp, failure status words, danger button text and invalid input borders.

### Tertiary
- **Spike Tape** (tape-pink, tape-yellow, tape-green, tape-blue, tape-orange, tape-violet, tape-cyan): one is picked by a hash of the project name. These colours appear only on the spike strip.

### Neutral
- **Rehearsal Floor** (floor): the page ground. In dark mode this is a neutral grey (#141518), not blue.
- **Sheet White** (sheet): full sheets, buttons, inputs.
- **Ink** (ink, ink-2): primary text, then meta text, labels, table headers and timestamps.
- **Rule** (rule): every border and divider, plus the scrollbar thumb.
- **Hover** (hover): row and quiet-button hover wash.
- **Code Ground** (code): command boxes and the log pane.

### Named Rules
**The Lamp Colour Rule.** Green, amber and red mean live, standby and failed. They are never used to decorate.

**The Neutral Ground Rule.** In dark mode the blue stays on the desk and the plate. The floor, sheets and rules are neutral greys.

**The Tape Is Identity Rule.** Fluorescent tape colours appear only on spike strips. They never carry state.

## Typography

**Body Font:** Atkinson Hyperlegible Next Variable (fallbacks: ui-sans-serif, system-ui)
**Label/Mono Font:** Atkinson Hyperlegible Mono Variable (fallback: ui-monospace)

**Character:** A legibility-first workhorse used at two widths. Mono is used for anything a user might type or copy: release numbers, env keys, hostnames, commands and logs.

### Hierarchy
- **Cue** (700, 2rem/2rem, tight tracking): the largest type on any page. It is used for the live release number in mono beside its go lamp, and for the sign-in wordmark.
- **Title** (700, 1.1875rem/1.5rem): the project name beside its spike strip and the desk wordmark.
- **Body** (400; 600 for buttons, 700 for sheet headings, 15px/22px): running text, table cells and controls. Prose is capped at 62ch.
- **Meta** (400 or 600, 0.8125rem/1.125rem): field labels, hints, table headers, timestamps, small buttons, commands and logs.

### Named Rules
**The Four Sizes Rule.** There are exactly four type sizes: meta, body, title and cue. Express hierarchy through weight (400/600/700) and the mono width, never through a fifth size.

**The Sentence Case Rule.** Every label, heading and button uses plain sentence case, with no uppercase tracking. A heading names its sheet and nothing restates it.

## Layout

The desk bar is sticky and 48px tall. The page is capped at 1440px wide, with 16px gutters (24px from `sm`). On a project page, a header row carries the spike strip, name, the live cue number, service links and Redeploy on the right. Below it sits a two-column grid at `lg` (1024px): the work column (`minmax(0,1fr)`) holds Releases and then Logs. The support column (320 to 400px) holds Add-ons, Env values, Domains and Database backups. Under `lg` the columns stack. On mobile, the "When" column folds under the release number and Redeploy stretches to full width.

The rhythm is a 4px base. Sheets and sections are 20px apart. Sheet content is inset 16px. List rows are at least 44px tall and sheet headers at least 48px. Controls are 36px tall (32px for small ones).

## Elevation & Depth

The system is flat. No element casts a shadow. Depth comes from three layers: the grey floor, white sheets with a 1px rule border, and the blue desk and plate as the only reversed surfaces. Quiet sections lie on the floor itself, separated only by a top rule.

### Named Rules
**The Ruled Not Raised Rule.** Separate surfaces with 1px rules and the sheet-against-floor contrast. Do not add box-shadows.

**The One Plate Rule.** The live release row is the only reversed row in the interface. The selected tab of the log service switcher uses the same plate colour as a selection state, but no other row or card is ever reversed.

## Shapes

Corners are small and practical: 8px for sheets, 6px for buttons, inputs, command boxes and tab groups, 4px for focus outlines and tab segments. Spike strips are 22 by 7px, with a 1px corner and a -6 degree tilt, like tape laid by hand. Lamps are 12px SVG discs (14px in the wordmark, 18px beside the cue number).

## Components

### Buttons
Buttons are plain and firm: semibold body text on a 36px, 6px-radius box with a 1px border.
- **Primary:** action blue with white ink. The border is transparent and hover deepens to action-hover. Use it for the one forward action in a context; Redeploy becomes primary only when pending values need a redeploy.
- **Quiet (default):** sheet ground, ink text and a rule border; hover washes to hover.
- **Danger:** sheet ground with fail-ink text; on hover the border turns fail red.
- **Small:** 32px tall, 10px horizontal padding, meta size. Use it in rows and sheet headers.
- **Busy / disabled:** a spinning loader icon, 55% opacity, not-allowed cursor.
- **Icon button:** a 32px square with ink-2 icons (16px Lucide SVG). It has a hover wash and always an accessible name.

### Standby-then-go (signature)
This is the stage manager's call. The first press arms the control: an amber border, a 15% amber fill and a blinking standby lamp, with the label rewritten to state exactly what will go ("Go: r9 live"). A second press within 4 seconds runs the action. Escape, blur or the timeout stands it down. It is used for roll back, redeploy, and deleting an env value or domain. Removing an add-on deletes data and needs a typed project name instead.

### Cue Lamp (signature)
State is carried by shape as well as colour:
- **Live:** a filled green disc with a soft halo.
- **Standby:** an amber ring that blinks at 1.1s.
- **Failed:** a red disc struck through.
- **Superseded:** a hollow ring at 55% opacity.
- **Off:** a grey disc at 28% opacity.

When a release goes live, its go lamp lands on the new row (scale 0.4 to 1.25 to 1 over 420 ms, `cubic-bezier(0.16, 1, 0.3, 1)`). Both animations stop under reduced motion.

### Cards / Containers
- **Sheet:** white ground, 1px rule border and 8px corners. The header row is at least 48px tall, with a bold body-size title and an optional action on the right, above a rule. Use it for primary work: Releases, Logs and the project list.
- **Quiet sheet:** a top rule only, lying on the floor, with the same header. Use it for supporting sections in the right column.
- **Release cue sheet:** a table with a lamp column, mono release number, when, status and an action. Rows are divided by rules and wash on hover. The live row is the plate.

### Inputs / Fields
- **Style:** 36px tall, sheet ground, 1px rule border, 6px corners, 12px padding. The border darkens toward ink-2 on hover.
- **Focus:** the border turns focus blue, with a 2px focus outline at 40% opacity.
- **Error:** an invalid field gets a fail-red border. An error note shows the message in semibold fail-ink, then the next step in ink-2.
- **Field:** a semibold meta label above the control and an optional meta hint below it.

### Navigation
The desk bar holds the wordmark (live lamp plus "kayly" at title size, bold) on the left. Next to it is a project switcher: a 32px select on the desk with a 20% desk-ink border. Sign out sits on the right in desk-ink-2 and collapses to its icon on small screens. There is no sidebar.

### Command
A code-ground box with 6px corners holds a single-line mono command and a copy icon button. Use it wherever the next step is a CLI command.

## Do's and Don'ts

### Do:
- **Do** mark every project with its spike strip, using the colour derived from the name.
- **Do** carry lamp state through shape as well as colour (filled, ringed, struck, hollow, grey disc).
- **Do** gate roll back, redeploy and env/domain deletion behind standby-then-go, with a go label that names the exact outcome.
- **Do** put primary work on full sheets and supporting work in quiet sections on the floor.
- **Do** set anything a user types or copies in Atkinson Hyperlegible Mono.
- **Do** keep transitions to 150 ms colour changes and respect reduced motion.

### Don't:
- **Don't** add a fifth type size.
- **Don't** reverse any row other than the live release.
- **Don't** use box-shadows; separate surfaces with 1px rules.
- **Don't** put blue on the floor or the sheets in dark mode; keep blue on the desk, plate and action.
- **Don't** use lamp green, amber or red, or spike-tape colours, for decoration.
- **Don't** use a dark sidebar with a grid of project cards; projects are a ruled list on a sheet.
