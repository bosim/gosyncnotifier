# gosyncnotifier — Agent Protocol for File Change Triggers

## Project Overview

`gosyncnotifier`: A small Go utility that watches a directory for file changes and triggers a specified command when activity occurs or a timer elapses.

The project is intended for workflows like:
- Syncing local files to a remote destination with `rsync`, `rclone`, or similar tools
- Running periodic sync jobs even when files are not changing
- Triggering other commands as a result of filesystem events
- Reducing manual sync command usage after frequent file changes

## Repository Layout

- `cmd/gosn/` — CLI entry point for the `gosn` binary
- `internal/` — runtime logic for watchers, timers, runner execution, and notifications
- `internal/*_test.go` — unit tests for the watcher, timer, runner, syncevent, notifier, and util packages
- `README.md` — usage, overview, and examples in Markdown format
- `go.mod` / `go.sum` — Go module metadata and dependency lockfile
- `.github/workflows/go.yml` — CI workflow running `go build -v ./...` and `go test -v ./...`
- `.gitignore` — Git ignore rules

## Build and Validation

From the repository root (or after installation), validate with:

```bash
# For local source
go build ./...

# With test suite
go test ./...
```

CI (`.github/workflows/go.yml`) runs the same build and test commands on Go 1.26.

## Architecture Notes

1. **File Watcher**: Monitors a single directory recursively for filesystem
   events using `fsnotify` (inotify on Linux, kqueue/FSEvents on macOS). No
   external watcher programs (`inotifywait`, `fswatch`) are used. Directories
   created while running are watched automatically, removed ones are unwatched.

2. **Timer Trigger**: Configurable interval (default 10 minutes) allows periodic execution independent of file activity. Timer uses Go's `time.Ticker` to stay responsive even when system is suspended.

3. **Command Execution**: The runner parses user-provided command as `<program> arg1 arg2...` and executes it.

4. **Notification System**: Desktop notifications use three levels via the freedesktop.org D-Bus notification service, or optionally via a `notify-send`-compatible executable:
   - `low`: Debug/debugging information (for developers)
   - `normal`: General notification (for users of the tool)
   - `critical`: Error/failure notification

5. **Event Types**: A `SyncEvent` carries an event type and an origin, which are
   two separate enums:

   `SyncEventType`:
   - `SyncEventNone`: No file activity (e.g. timer or init trigger)
   - `SyncEventCreated`: New file detected
   - `SyncEventModified`: File content changed
   - `SyncEventDeleted`: File removed

   `OriginType`:
   - `OriginNone`: Unset
   - `OriginInit`: Initialization signal (runs the command once at startup)
   - `OriginTimer`: Timer interval elapsed
   - `OriginWatcher`: Filesystem event observed

6. **Execution Model**: Events flow from `syncChannel` through runner, notifying notifier to send desktop messages (if enabled). The command runs once immediately at startup (`OriginInit`), file activity resets the timer interval, and commands run serially (one at a time, further events wait) with no debounce.

7. **Shutdown**: A shared `context.Context` from `signal.NotifyContext` covers
   SIGINT (Ctrl-C) and SIGTERM. On cancellation the watcher and timer stop
   immediately, an in-flight command is allowed to finish, and no further
   commands are started. Failure and post-command notifications are suppressed
   during shutdown. After the first signal the default disposition is restored,
   so a second Ctrl-C force-quits even if a command hangs.

## Notification Modes

The notifier supports a three-level notification mechanism:

```go
type Notifier interface {
    Low(message string) error      // "Debug/Development info"
    Normal(message string) error   // "User-facing notification"
    Critical(message string) error // "Error/Failure notification"
    Close() error
}
```

The default `dbus` backend connects to the session bus once and reconnects if a
notification fails. A failed startup connection is logged as a warning and is
not fatal; later sends retry the connection. An unknown `-notifier-backend`
value exits with an error at startup. The `command` backend runs a
`notify-send`-compatible executable and is selected with
`-notifier-backend=command`; if the executable is missing, notifications are
disabled with a warning and the program keeps running.

## Usage Patterns

### Basic Configuration

Use the `-watcher-path` flag to specify the directory to monitor for file
changes. The command to execute is passed after `--`:

```bash
gosn -watcher-path ~/projects/source -- rsync user@host:/dir/...
```

### Recursive Watching

Only one watch path can be configured. Files are watched recursively,
including subdirectories:

```bash
gosn -watcher-path /var/log -- notify-send error "File changed in /var/log"
```

### Disabled Watchers (Polling Only)

Configure for periodic execution only, no file change detection:

```bash
gosn -timer-interval 10 -watcher-path "" -- rsync ...
```

Use empty string or omit watcher entirely to poll the command periodically.

### Silent Operation (Full Polling Without Notifications)

Disable notifications while still running:

```bash
gosn -notifier-enabled=false -watcher-path /test -- rsync user@host:/dir...
```

### Custom Log Levels

Set log level for filtering output:

```bash
gosn -log-level=debug ...    # Detailed logs
gosn -log-level=warn ...     # Warning messages and above
gosn -log-level=error ...    # Error messages and above (no info)
```

### Post-Command Notification

Send a secondary low-priority notification when command completes:

```bash
gosn -notifier-post-command=true ...
```

### Notification Backend

Use the session D-Bus notification service (default):

```bash
gosn -notifier-backend=dbus ...
```

Use a `notify-send`-compatible executable instead:

```bash
gosn -notifier-backend=command -notifier-exec=notify-send ...
```

## CLI Reference

### Commands and Options

| Argument | Default | Description |
|----------|---------|-------------|
| `--timer-interval` (flags) | 10 minutes | Timer triggers; 0 disables timer |
| `--watcher-path` (optional) | empty string | Path to monitor for events |
| `--log-level` (flags) | info | Minimum log level |
| `--notifier-enabled` | true | Enable desktop notifications |
| `--notifier-backend` | `dbus` | Notification backend: `dbus` or `command` |
| `--notifier-exec` (default: notify-send) | `notify-send` | Executable name for the `command` backend (must be available) |
| `--notifier-post-command` | false | Send follow-up notification |

### CLI Syntax

```bash
gosn [flags] -- <command>
```

Full options are printed with `gosn -help`.

## Key Considerations

- **Watch Scope**: Limit watched paths to avoid excessive syncs on slow environments
- **Suspension Handling**: Use Go ticker (`time.NewTicker`) instead of sleep loops for responsive suspends
- **Notifier Dependency**: The `dbus` backend requires a reachable session bus (`DBUS_SESSION_BUS_ADDRESS`); the `command` backend requires a `notify-send`-compatible executable in PATH
- **Execution Performance**: Command execution runs regardless of file changes unless explicitly disabled

## Development Workflow

When modifying code:

1. **Validate local** builds and tests first
2. **Confirm model integrity** — event flow matches architecture notes
3. **Test build cleanly** — no compilation errors or warnings
4. **Document usage** — update README.md appropriately
5. **Consider edge cases** — file permissions, network access, race conditions

## License

MIT License - See LICENSE file for details.
