---
name: ship
description: Runs checks, commits, and opens a PR for the current changes.
disable-model-invocation: true
allowed-tools: Bash(git status *) Bash(git diff *) Bash(git add *) Bash(git commit *) Bash(gh pr create *) Bash(go *) Bash(gofmt *) Bash(golangci-lint *)
---

## Current state

- Status: !`git status --short`
- Diff: !`git diff HEAD`

## Task

1. Run `gofmt -l .`, `go vet ./...`, and `go test ./...` (and `golangci-lint run` if a config file
   exists). If anything fails, stop and show the failure — don't go further.
2. Group the changes into meaningful commits (not one `wip` covering everything). Commit message
   format: `<type>(<scope>): <what changed>`, imperative mood, subject line ≤72 characters.
3. Open a PR via `gh pr create` describing what changed, why, and how to verify it.
4. Print the PR link.

If there's nothing to commit, say so and do nothing.
