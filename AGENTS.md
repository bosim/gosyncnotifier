# AGENTS.md

## Project overview

`gosyncnotifier` is a small Go utility that watches one or more directories for file changes and triggers a sync or other command when activity happens or a timer elapses.

The project is intended for workflows like:
- syncing local files to a remote destination with `rsync` or `rclone`
- running periodic sync jobs even when files are not changing
- triggering other commands as a result of filesystem events

## Repository layout

- `cmd/gosn/` — CLI entry point for the `gosn` binary
- `internal/` — runtime logic for watchers, timers, runner execution, and notifications
- `README.md` — usage, overview, and examples
- `go.mod` / `go.sum` — Go module metadata and dependency lockfile
- `LICENSE` — MIT license

## Build and validation

From the repository root, validate the project with:

```bash
go build ./...
```

If tests are present or added in a change, run:

```bash
go test ./...
```

When making a code change, prefer the smallest validations that confirm the modified behavior.

## Architecture notes

- The file watcher monitors configured paths for filesystem events.
- Timer-based triggers allow periodic execution even when no files changed.
- The runner executes the command passed to the program.
- Notification behavior is separate from the sync logic and should remain isolated unless intentionally changed.

## Coding expectations

- Keep Go code idiomatic and readable.
- Prefer small, well-named functions and minimal scope changes.
- Do not add unnecessary dependencies.
- Maintain the existing CLI contract unless the change intentionally updates it.
- Update `README.md` when behavior or usage changes.

## Change guidance

Before submitting a change:
- confirm the change still matches the watcher/timer/sync execution model
- validate the project still builds cleanly
- keep PRs focused and avoid unrelated cleanup in the same patch

## PR expectations

- Title should be specific and describe the change clearly.
- Summaries should explain the motivation and the behavior being changed.
- Include test/build validation in the PR description.
