---
name: context-check
description: Checks what's missing from CLAUDE.md that's causing the model to search the web for things that are actually local to this repo.
disable-model-invocation: true
---

Go through the repo and the current CLAUDE.md and list what has to get re-discovered every
session instead of being available locally:

1. Versions of key dependencies and the Go runtime.
2. Internal packages or services with no public documentation.
3. Conventions that differ from tool defaults.
4. Build/test/run commands, including external requirements (databases, env vars).

Output the proposed lines for CLAUDE.md — don't edit the file directly. Keep CLAUDE.md near its
~200-line budget: if there's more than a few lines' worth, propose a `.claude/rules/*.md` file
(with a `paths:` frontmatter matcher) instead of appending to the main file.
