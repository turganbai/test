# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

`/Users/tuk/code/mcp` is the root of a new Go module (module path and package layout not chosen
yet — see below). It's a git repo (`main` branch, single "initial commit").

Older reference material — copies of installed Claude Code plugins (`Engineering-1.2.0-v33`,
`GitKraken-1.0.0-v9`, `Productivity-1.3.1-v34`), the `dashboard.html` tool, and a set of Russian
Claude Code practice notes (`checklist-*.md`, `claude-code-*.md`) — has been archived under
`checklists/`. Treat everything under `checklists/` as reference, not source: don't build, lint,
or run installers against it. Two files in there are worth knowing about specifically:
- `checklists/claude-code-setup-for-teams.md` — the template this CLAUDE.md, and the skills in
  `.claude/skills/`, are based on (CLAUDE.md/rules/skills mechanics, line budget, phrasing style).
- `checklists/checklist-dead-code-audit-php-go.md` and
  `checklists/checklist-file-function-size-refactor-trigger.md` — the specific Go linters and
  numeric thresholds referenced below (don't re-derive these from scratch, read those files).

## Go module

- Module path: `github.com/turganbay/mcp` (no git remote yet; the path is a placeholder that matches
  the directory name — change it here *and* in every import if the repo moves)
- Go version: `1.27`
- Dependencies: none. Standard library only, deliberately — keep it that way unless there is a
  reason that survives review.
- Layout:
  - `cmd/jira-returns/` — CLI wiring, flags, JSON + table output. No domain logic.
  - `internal/jira/` — transport: HTTP, basic auth, retries, DTOs of the Jira Cloud REST API v3.
  - `internal/analytics/` — domain model and the pure aggregation. Knows nothing about HTTP;
    declares the `IssueFetcher` interface it consumes.
  - `internal/collect/` — the adapter that implements `analytics.IssueFetcher` on top of
    `internal/jira`. The only package importing both.
  - `internal/config/` — env loading and validation.

Dependency direction is one-way: `cmd` → `collect` → {`jira`, `analytics`}, and `analytics` imports
nothing of ours. Don't let `analytics` grow an import of `internal/jira`.

Commands:
- Build: `go build ./...`
- Vet: `go vet ./...`
- Test all: `go test ./...`
- Test one: `go test ./... -run '^TestName$'`
- Format check: `gofmt -l .` (should print nothing; `gofmt -w .` to fix)
- Lint: `golangci-lint run` — once adopted; see `checklists/checklist-dead-code-audit-php-go.md`
  and `checklists/checklist-file-function-size-refactor-trigger.md` for which linters
  (`gocyclo`, `funlen`, `unused`, `dupl`) and thresholds to enable.

### Internal packages / frequently-used APIs

This table exists specifically so Claude doesn't have to re-derive or web-search things that are
local to this repo — see the "Working with Claude Code" section below.

| Package/service | What it does | Docs |
|---|---|---|
| `internal/jira` | `Client.SearchIssues` (POST `/rest/api/3/search/jql`, `nextPageToken` cursor), `Client.IssueChangelog` (GET `/rest/api/3/issue/{key}/changelog`, `startAt` offsets), `Client.StatusCatalog` (GET `/rest/api/3/status`). Retries 429/5xx with `Retry-After` + jittered backoff. | [Issue search](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-search/), [Issues](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/) |
| `internal/analytics` | `Compute([]Issue, Options, []Warning) Report` — pure, no I/O. `Run` is the only thing here that touches a fetcher. | — |
| `internal/collect` | `Collector.FetchIssues` maps Jira DTOs onto `analytics.Issue`, flattening histories/items into time-ordered `Change`s. | — |
| `cmd/jira-returns` | `-jql` / `-stories`, `-from` / `-to`, `-out`. Config comes from env only (see `internal/config`). | `README.md` |

Two invariants worth not rediscovering the hard way:
- **Status transitions are matched on `item.to` (the status *id*), never `toString`.** Status names
  are localized and get renamed; ids don't. Same for `fieldId == "status"` over `field`.
- **A return is attributed to the Developer custom field, never to `history.author`.** The author is
  the QA engineer who moved the ticket; the metric is about the developer.

## Working with Claude Code on this repo

1. **Commit and open PRs through Claude Code itself** (or `/ship`, below) — not by pasting a diff
   into another terminal. Work committed outside Claude Code isn't attributed to it even when it
   did the work.
2. **Plan before multi-step work, then implement with intermediate commits.** Use `/clear` or
   `/compact` between stages instead of letting one session sprawl. Lots of short sessions with no
   commit is fine for genuine exploration/consultation — but the same bug or feature reopened 3+
   times with no progress is the signal to stop and change approach (write a test that pins down
   the problem, or ask for 2-3 alternative approaches).
3. **Run an explicit refactor/shrink pass periodically** (`/refactor-pass`, below) rather than
   relying on incidental cleanup during feature work. Don't read the added/removed line ratio
   itself as a cleanliness signal — it only tracks accepted edits and never sees later deletions.
4. **Check installed skills/connectors before doing something manually and repeatedly.** This repo
   has the Engineering, GitKraken, and Productivity plugins installed (see `checklists/*-v*/skills/`
   for what's available, or `/help`).
5. **Point at an existing similar file when asking for a new one from scratch**, rather than a bare
   description — first-draft accuracy is measurably lower on from-scratch file creation than on
   editing an existing file.
6. **Keep the "internal packages" table above current** as real code lands — that's the concrete
   fix for unnecessary web search on things that are actually local to this repo.
7. In Claude.ai chat specifically (a separate surface from Claude Code): turn on extended thinking
   for architecture/design decisions and non-obvious bug analysis before writing code, and keep a
   chat Project per major feature with key docs loaded, to avoid re-explaining context each time.
