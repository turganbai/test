---
name: refactor-pass
description: A pass over recently-changed code aimed at shrinking and removing, not adding. Use after a run of feature work, once duplication has accumulated, or when asked to assess code health.
---

Look at the code changed on this branch (`git diff main...HEAD --name-only`) and find:

- Duplication that collapses into one function — cross-check with `dupl -threshold 50 ./...` if
  it's installed.
- Dead code: unused exports, branches, flags, commented-out blocks — cross-check with
  `deadcode ./...` / `staticcheck ./...` if installed.
- Wrapper/abstraction layers with exactly one caller.
- Dependencies in `go.mod` no longer used anywhere — `go mod tidy -v`.

For each finding: the path, what you'd remove or merge, and the risk of doing so.

Show the list first. Only make edits after the user confirms. Priority is fewer lines with no
behavior change — tests must stay green throughout.

For the specific thresholds behind these checks (file/function size, cyclomatic complexity, dup
length), see `checklists/checklist-dead-code-audit-php-go.md` and
`checklists/checklist-file-function-size-refactor-trigger.md` rather than guessing new ones.
