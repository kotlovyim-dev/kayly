# Kayly

Self-hosted deploy platform: Vercel-style UX for web apps, APIs, workers and Telegram bots on a server you own. Open source from the first commit.

**Current goal:** v0.1, a stable, convenient home for two real apps, `../ten.00` and `../leryo`. Build only what one of them needs ([ARCHITECTURE.md §1](ARCHITECTURE.md)).

## Working with the owner

- **Everything is in English:** replies to the owner, code, comments, commits, PRs and all docs.
- The owner builds through AI agents and reviews results. At the end of a task, say what was verified and how, and what to check by hand.
- Architecture decisions are recorded in [ARCHITECTURE.md](ARCHITECTURE.md). A change that contradicts it updates the document first, in the same change.

## Map

| Where | What |
|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | Invariants, components, data model, `kayly.toml`, deploy pipeline, layout, quality bar |
| [RESEARCH.md](RESEARCH.md) | Competitors, UX principles, positioning |

## Non-negotiables

1. The invariants in ARCHITECTURE.md §2. Above all: nothing before the proxy switch touches the live release.
2. Code quality is part of the product: this repo is public. Small packages with one job, errors wrapped with context, `context.Context` on every blocking call, no global state, no dead code, no TODOs without an issue.
3. Every behaviour change ships with a test. Pipeline changes are tested against the fake runtime, including a crash at each step.
4. Pin every external image and binary by version (images also by digest).
