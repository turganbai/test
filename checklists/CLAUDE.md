# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this directory is

This is not a single application — it's a personal working folder with two distinct kinds of content:

1. **Plugin bundle copies** (`Engineering-1.2.0-v33`, `GitKraken-1.0.0-v9`, `Productivity-1.3.1-v34`) —
   full copies of installed Claude Code plugins, each with a `.claude-plugin/plugin.json` manifest and a
   `skills/` directory. Treat these as reference/vendor copies, not code to modify:
   - `Engineering-1.2.0-v33` — engineering workflow skills (standup, code-review, debug, deploy-checklist,
     documentation, incident-response, architecture, system-design, tech-debt, testing-strategy).
   - `GitKraken-1.0.0-v9` — MCP-only plugin (`mcp.json`) giving access to Git/PR/issue context across repos
     (GitHub, GitLab, Azure DevOps, Bitbucket, Jira). No skills of its own.
   - `Productivity-1.3.1-v34` — task/memory/planning skills (`start`, `task-management`, `memory-management`,
     `update`), plus its own copy of `dashboard.html`.

2. **User notes and a standalone tool** at the top level:
   - `dashboard.html` — a byte-for-byte copy of `Productivity-1.3.1-v34`: a
     self-contained, dependency-free HTML/CSS/JS single-file app (no backend, no build step) that's a local
     GUI for managing a `TASKS.md` file and a project's `memory/` directory. It uses the browser's File
     System Access API (`showOpenFilePicker`/`showDirectoryPicker`), so it only runs in Chromium-based
     browsers — open the file directly, there's nothing to build or serve. All state lives in top-level `let`
     variables in one `<script>` block; task-file edits autosave to disk and a 1s poller reloads external
     changes. If editing it, prefer editing the copy in `Productivity-1.3.1-v34` as the source of
     truth and re-sync the top-level copy (or vice versa) — the two are expected to stay identical.
   - `checklist-*.md`, `claude-code-1on1-checklist.md`, `claude-code-setup-for-teams.md` — the user's own
     reference notes (in Russian) on Claude Code team practices: balancing added/removed lines as a
     refactor signal, dead-code audits, file/function-size refactor triggers, turning Claude Code sessions
     into commits/PRs, running 1-on-1s around Claude Code usage metrics, and team-wide Claude Code
     configuration (three-mechanism setup: what goes where, what loads when). These are prose reference
     docs, not source for any build.

## Working here

There is no build, lint, or test command — nothing in this directory is compiled or has an automated test
suite. Verify any change to `dashboard.html` by opening it directly in a Chromium-based browser and
exercising the UI. Don't run installers/build tooling against the plugin bundle directories; they're
self-contained snapshots of already-installed plugins, not projects with their own dev workflow.
